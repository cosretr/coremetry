package api

// user_directory_autofill.go — v0.10.1741 (operator-reported): v0.10.1732
// öncesi son girişi olan LDAP hesabının detayında İletişim / Kuruluş dizin
// alanları "—" geliyordu, yalnız "AD ile denetle"den sonra görünüyordu; yönetici
// ham DN'di.
//
//   - Otomatik doldurma: admin LDAP hesabının detayını açtığında saklı dizin
//     boşsa ya da 24 saatten eskiyse sunucu admin bind ile satır içi okur
//     (≤2 sn), yazar ve cevaba koyar. Hata / zaman aşımı 10 dk hatırlanır
//     (sayfa hiç asılmaz, her açılış LDAP'a gitmez). Otomatik okuma audit
//     yazmaz (operatör: elle eylem değil). Takım giriş kuralıyla taşınır
//     (carryLdapLoginRow) — elle düzenlenen takımı otomatik okuma ezmez.
//   - Yönetici: saklı managerDn tek base-scope okumayla görünen ad + sicile
//     çözülür (organizasyon kartının okuması; 10 dk önbellek, hata 10 dk
//     negatif). DN yalnız Dizin sekmesinde.

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ldap"
)

const (
	directoryAutofillTimeout = 2 * time.Second
	directoryAutofillRetry   = 10 * time.Minute
	userManagerTTL           = 10 * time.Minute
)

// directoryAutofillNeeded — SAF: detay açılışı dizini satır içi okusun mu.
// Yalnız etkin LDAP hesabı (auth_provider=ldap — kartlar da yalnız onda),
// LDAP açık, saklı dizin boş ya da bayat.
func directoryAutofillNeeded(u *chstore.User, ldapOn bool, now time.Time) bool {
	if u == nil || !ldapOn || u.Disabled || u.AuthProvider != "ldap" {
		return false
	}
	return !directoryFresh(u.Directory, now)
}

// tryLog — anahtar başına son deneme; claim aralık dolmadan ikinci denemeyi
// reddeder (başarısızlık / zaman aşımı 10 dk hatırlanır). Süreç içi.
type tryLog struct {
	mu sync.Mutex
	at map[string]time.Time
}

func (l *tryLog) claim(key string, now time.Time, every time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.at == nil {
		l.at = map[string]time.Time{}
	}
	if t, ok := l.at[key]; ok && now.Sub(t) < every {
		return false
	}
	if len(l.at) > 4096 { // sınır: eskileri at
		for k, t := range l.at {
			if now.Sub(t) >= every {
				delete(l.at, k)
			}
		}
	}
	l.at[key] = now
	return true
}

func (l *tryLog) recent(key string, now time.Time, every time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.at[key]
	return ok && now.Sub(t) < every
}

var (
	dirAutofillTries tryLog // kullanıcı kimliği → son otomatik okuma
	managerFailures  tryLog // yönetici önbellek anahtarı → son başarısız okuma
)

// readWithin — fn'i süre sınırıyla koşar; aşılırsa ctx hatası döner (fn arka
// planda kendi zaman aşımıyla biter, sayfa beklemez).
func readWithin[T any](ctx context.Context, d time.Duration, fn func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1)
	go func() {
		v, err := fn(ctx)
		ch <- res{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// autofillUserDirectory — gerekiyorsa dizini okur + yazar; yazılan satırı
// (yeniden okuma) döndürür. Gerekmiyorsa / başarısızsa nil (detay saklıyla).
func (s *Server) autofillUserDirectory(ctx context.Context, u *chstore.User) *chstore.User {
	if s.ldap == nil || s.store == nil {
		return nil
	}
	now := time.Now()
	if !directoryAutofillNeeded(u, s.ldap.Enabled(), now) || !dirAutofillTries.claim(u.ID, now, directoryAutofillRetry) {
		return nil
	}
	s.store.ReprobeUserDirectoryCol(ctx)
	name := directoryLookupName(u)
	rd, err := readWithin(ctx, directoryAutofillTimeout, func(c context.Context) (*ldap.DirectoryRead, error) {
		return s.ldap.ReadUser(c, name)
	})
	if err != nil || rd == nil || rd.User.Directory.Empty() {
		if err != nil {
			log.Printf("[users] dizin otomatik okuması %s: %v (10 dk sonra yeniden)", u.ID, err)
		}
		return nil
	}
	after := applyDirectoryReadAt(*u, rd.User, now)
	after.Team = u.Team // giriş kuralı: takım elle düzenlenir, otomatik okuma ezmez
	if err := s.store.UpsertUser(ctx, after); err != nil {
		log.Printf("[users] dizin otomatik yazımı %s: %v", u.ID, err)
		return nil
	}
	s.meUsers.clear()
	if saved, err := s.store.GetUserByIDAnyState(ctx, u.ID); err == nil && saved != nil {
		return saved
	}
	return &after
}

// userManager — Kuruluş kartının yöneticisi (lib/types.ts UserManager).
type userManager struct {
	Name       string `json:"name"`
	EmployeeID string `json:"employeeId,omitempty"`
}

// managerDisplay — SAF: yönetici etiketi. Sıra: dizin düğümünün görünen adı,
// saklı managerName, DN'nin CN'i; hiçbiri yoksa "" (ham DN ASLA — DN yalnız
// Dizin sekmesinde). Sicil yalnız okunan düğümden.
func managerDisplay(dn string, p *ldap.DirectoryPerson, storedName string) userManager {
	m := userManager{}
	if p != nil {
		m.Name, m.EmployeeID = strings.TrimSpace(p.Name), strings.TrimSpace(p.EmployeeID)
	}
	if m.Name == "" {
		m.Name = strings.TrimSpace(storedName)
	}
	if m.Name == "" {
		m.Name = ldap.CNFromDN(dn)
	}
	return m
}

// userManagerKey — SAF: TÜM girdiler (DN + dizin yapılandırması özeti).
func userManagerKey(dn string, cfgDigest uint64) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(dn))))
	return fmt.Sprintf("user-mgr:%x:%x", h.Sum64(), cfgDigest)
}

// userDirectoryManager — saklı managerDn → görünen ad + sicil. LDAP kapalı /
// okuma başarısız → saklı ad (CN); yönetici yoksa nil.
func (s *Server) userDirectoryManager(ctx context.Context, u *chstore.User) *userManager {
	d := parseDirectory(u.Directory)
	dn := strings.TrimSpace(d.ManagerDN)
	if dn == "" {
		return nil
	}
	var p *ldap.DirectoryPerson
	if s.ldap != nil && s.ldap.Enabled() {
		key := userManagerKey(dn, s.ldap.DirectoryConfigDigest())
		now := time.Now()
		if !managerFailures.recent(key, now, userManagerTTL) {
			body, _, err := s.cachedJSON(ctx, key, userManagerTTL, false, func(c context.Context) (any, error) {
				return readWithin(c, directoryAutofillTimeout, func(c context.Context) (ldap.DirectoryPerson, error) {
					return s.ldap.PersonByDN(c, dn)
				})
			})
			var got ldap.DirectoryPerson
			if err == nil && json.Unmarshal(body, &got) == nil {
				p = &got
			} else {
				managerFailures.claim(key, now, userManagerTTL)
			}
		}
	}
	m := managerDisplay(dn, p, d.ManagerName)
	if m.Name == "" {
		return nil
	}
	return &m
}
