package api

// user_directory.go — v0.10.1733 (kullanıcı detay sayfası S3, operatör onayı
// 2026-10-31).
//
//   POST /api/users/{id}/directory-check → "AD ile denetle": kullanıcıyı
//        servis hesabıyla dizinden yeniden okur, {before, after, diff} döner,
//        fark varsa satırı yazar, audit user.directory_check. Rol DEĞİŞMEZ
//        (rol yalnız girişte grup eşlemesinden; bkz. user_detail.go kilit).
//   GET  /api/users/{id}/directory       → Dizin sekmesi: ham öznitelikler
//        (ikili değerler özet), 60 sn önbellek.
//   GET  /api/users/{id}/org             → Organizasyon: yönetici zinciri ≤4
//        üst + doğrudan bağlılar ≤50, 10 dk önbellek; Coremetry hesabı olan
//        düğümler accountId taşır.
//
// Üçü de admin. GET'ler yalnız sekme/kart açılınca çağrılır (istemci). Önbellek
// anahtarı kullanıcı kimliği + dizin yapılandırması özeti: eşleme değişince
// eski sonuç anahtar dışı kalır. LDAP hatası 502 + mesaj (istemci satır içi
// hata durumu çizer, boş panel değil).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ldap"
)

func init() { registerRoutesExtra("user-directory", (*Server).registerUserDirectoryRoutes) }

func (s *Server) registerUserDirectoryRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/users/{id}/directory-check",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.checkUserDirectory)))
	mux.Handle("GET /api/users/{id}/directory",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.getUserDirectory)))
	mux.Handle("GET /api/users/{id}/org",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.getUserOrg)))
}

const (
	userDirectoryTTL = time.Minute
	userOrgTTL       = 10 * time.Minute
)

// userDirectoryCacheKey — SAF: TÜM girdiler (tür, kullanıcı, yapılandırma özeti).
func userDirectoryCacheKey(kind, userID string, cfgDigest uint64) string {
	return fmt.Sprintf("user-%s:%s:%x", kind, userID, cfgDigest)
}

// directoryLookupName — SAF: dizinde aranacak kimlik (saklı sAMAccountName,
// yoksa e-posta; giriş filtresi ikisini de eşler).
func directoryLookupName(u *chstore.User) string {
	if u.LdapUsername != "" {
		return u.LdapUsername
	}
	return u.Email
}

// ldapUserForDirectory — kullanıcıyı okur ve LDAP okumasına uygun olduğunu
// doğrular; hata yazıldıysa nil.
func (s *Server) ldapUserForDirectory(w http.ResponseWriter, r *http.Request) *chstore.User {
	u, err := s.store.GetUserByID(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return nil
	}
	if u == nil {
		writeJSONError(w, http.StatusNotFound, "user not found")
		return nil
	}
	if u.AuthProvider != "ldap" && u.LdapUsername == "" {
		writeJSONError(w, http.StatusBadRequest, "bu hesap dizinden gelmiyor (LDAP değil)")
		return nil
	}
	if !s.ldap.Enabled() {
		writeJSONError(w, http.StatusBadRequest, "LDAP yapılandırılmamış ya da kapalı")
		return nil
	}
	return u
}

// ldapCachedErr — serveCached gövdesinden dönen LDAP hatasını writeErr'in
// sınıflarına bağlar: dizinde yok → 404, diğer her şey → 502 (dış kaynak;
// Coremetry arızası değil, öz-gözlem error_rate'ini şişirmesin).
func ldapCachedErr(err error) error {
	if errors.Is(err, ldap.ErrDirectoryUserNotFound) {
		return fmt.Errorf("%w: kullanıcı dizinde bulunamadı", errNotFound)
	}
	return fmt.Errorf("%w: dizin okunamadı: %v", errUpstream, err)
}

func writeLdapErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ldap.ErrDirectoryUserNotFound) {
		writeJSONError(w, http.StatusNotFound, "kullanıcı dizinde bulunamadı")
		return
	}
	writeJSONError(w, http.StatusBadGateway, "dizin okunamadı: "+err.Error())
}

// userFieldDiff — denetimde değişen bir alan.
type userFieldDiff struct {
	Field  string `json:"field"`
	Label  string `json:"label"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// applyDirectoryRead — SAF: giriş yolunun dizinden yazdığı alanları (ad,
// kuruluş, takım, kullanıcı adı, foto, dizin öznitelikleri) okumadan
// uygular. loginViaLDAP ile aynı kural: boş dizin değeri saklıyı silmez;
// foto dizindekini izler. Rol, e-posta, kilit, özel rol dokunulmaz.
func applyDirectoryRead(u chstore.User, d ldap.LDAPUser) chstore.User {
	return applyDirectoryReadAt(u, d, time.Time{})
}

// applyDirectoryReadAt — applyDirectoryRead + dizin JSON'una okuma damgası
// (v0.10.1741; sıfır an = damgasız).
func applyDirectoryReadAt(u chstore.User, d ldap.LDAPUser, now time.Time) chstore.User {
	if v := strings.TrimSpace(d.DisplayName); v != "" {
		u.FullName = v
	}
	if v := strings.TrimSpace(d.Company); v != "" {
		u.Org = v
	}
	if v := strings.TrimSpace(d.Department); v != "" {
		u.Team = v
	}
	if v := strings.ToLower(strings.TrimSpace(d.Username)); v != "" {
		u.LdapUsername = v
	}
	u.Photo = d.Photo
	u.HasPhoto = len(d.Photo) > 0
	if dj := directoryJSONAt(d.Directory, now); dj != nil {
		u.Directory = dj
	}
	return u
}

// directoryStaleAfter — saklı dizin bu süreden eskiyse detay açılışı yeniden
// okur (operatör: varsayılan dolu gelsin; giriş yenilemesi aynen).
const directoryStaleAfter = 24 * time.Hour

// directoryFresh — SAF: saklı dizin dolu ve son 24 saatte okunmuş mu.
// Damgasız (v0.10.1741 öncesi) satır taze sayılmaz.
func directoryFresh(raw json.RawMessage, now time.Time) bool {
	d := parseDirectory(raw)
	return !d.Empty() && d.SyncedAt > 0 && now.Sub(time.Unix(0, d.SyncedAt)) < directoryStaleAfter
}

func parseDirectory(raw json.RawMessage) ldap.DirectoryInfo {
	var d ldap.DirectoryInfo
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &d)
	}
	return d
}

// userDirectoryDiff — SAF: denetimin değiştirdiği alanlar, sabit sırada.
func userDirectoryDiff(before, after chstore.User) []userFieldDiff {
	out := []userFieldDiff{}
	add := func(field, label, b, a string) {
		if b != a {
			out = append(out, userFieldDiff{Field: field, Label: label, Before: b, After: a})
		}
	}
	add("fullName", "Ad soyad", before.FullName, after.FullName)
	add("org", "Şirket", before.Org, after.Org)
	add("team", "Birincil takım", before.Team, after.Team)
	add("ldapUsername", "Dizin kullanıcı adı", before.LdapUsername, after.LdapUsername)
	if !bytes.Equal(before.Photo, after.Photo) {
		ph := func(p []byte) string {
			if len(p) == 0 {
				return "yok"
			}
			return fmt.Sprintf("%d KB", (len(p)+1023)/1024)
		}
		out = append(out, userFieldDiff{Field: "photo", Label: "Fotoğraf", Before: ph(before.Photo), After: ph(after.Photo)})
	}
	bd, ad := parseDirectory(before.Directory), parseDirectory(after.Directory)
	add("directory.title", "Ünvan", bd.Title, ad.Title)
	add("directory.department", "Departman", bd.Department, ad.Department)
	add("directory.division", "Müdürlük", bd.Division, ad.Division)
	add("directory.level", "Seviye", bd.Level, ad.Level)
	add("directory.employeeId", "Sicil no", bd.EmployeeID, ad.EmployeeID)
	add("directory.mobile", "Telefon", bd.Mobile, ad.Mobile)
	add("directory.office", "Ofis", bd.Office, ad.Office)
	add("directory.managerDn", "Yönetici", bd.ManagerDN, ad.ManagerDN)
	return out
}

type directoryCheckResp struct {
	Before  chstore.User    `json:"before"`
	After   chstore.User    `json:"after"`
	Diff    []userFieldDiff `json:"diff"`
	Written bool            `json:"written"`
	// DirectoryStored — v0.10.1741: okunan dizin öznitelikleri satıra gerçekten
	// yazıldı mı (yeniden okumada var mı). false + dolu fark = sunucu dizini
	// saklayamıyor (users.directory kolonu henüz görünmüyor).
	DirectoryStored bool `json:"directoryStored"`
}

// runDirectoryCheck — denetimin çekirdeği (okuma → fark → yazım → yeniden
// okuma). v0.10.1741 (operator-reported): cevabın After'ı YAZILAN satırdır
// (yeniden okuma), hesaplanan değil — users.directory kolonu görünmüyorken
// UpsertUser kolonu sessizce düşürür; eskiden cevap okunanı "yazıldı" diye
// döndürüyor, kartlar "—" kalıyordu. Fark yoksa da damgasız / eski dizin
// tazelenir (detay otomatik doldurması 24 saat bir daha okumasın).
func runDirectoryCheck(before chstore.User, rd ldap.LDAPUser, now time.Time,
	upsert func(chstore.User) error, reread func() (*chstore.User, error)) (directoryCheckResp, error) {
	after := applyDirectoryReadAt(before, rd, now)
	diff := userDirectoryDiff(before, after)
	resp := directoryCheckResp{Before: before, After: before, Diff: diff,
		DirectoryStored: rd.Directory.Empty() || len(before.Directory) > 0}
	if len(diff) == 0 && (rd.Directory.Empty() || directoryFresh(before.Directory, now)) {
		return resp, nil
	}
	if err := upsert(after); err != nil {
		return resp, err
	}
	resp.Written = true
	saved, err := reread()
	if err != nil {
		return resp, err
	}
	if saved == nil {
		return resp, fmt.Errorf("%w: kullanıcı yazımdan sonra okunamadı", errNotFound)
	}
	resp.After = *saved
	resp.DirectoryStored = rd.Directory.Empty() || len(saved.Directory) > 0
	return resp, nil
}

func (s *Server) checkUserDirectory(w http.ResponseWriter, r *http.Request) {
	u := s.ldapUserForDirectory(w, r)
	if u == nil {
		return
	}
	rd, err := s.ldap.ReadUser(r.Context(), directoryLookupName(u))
	if err != nil {
		writeLdapErr(w, err)
		return
	}
	ctx := r.Context()
	s.store.ReprobeUserDirectoryCol(ctx) // v0.10.1741 — kolon görünmüyorsa yazmadan önce yeniden yokla
	resp, err := runDirectoryCheck(*u, rd.User, time.Now(),
		func(nu chstore.User) error { return s.store.UpsertUser(ctx, nu) },
		func() (*chstore.User, error) { return s.store.GetUserByID(ctx, u.ID) })
	if err != nil {
		writeErr(w, err)
		return
	}
	if resp.Written {
		s.meUsers.clear()
	}
	fields := make([]string, 0, len(resp.Diff))
	for _, d := range resp.Diff {
		fields = append(fields, d.Field)
	}
	details, _ := json.Marshal(map[string]any{"email": u.Email, "changed": fields})
	s.audit(r, "user.directory_check", "user", u.ID, string(details))
	writeJSON(w, resp)
}

type userDirectoryResp struct {
	DN         string              `json:"dn"`
	Attributes map[string][]string `json:"attributes"`
}

func (s *Server) getUserDirectory(w http.ResponseWriter, r *http.Request) {
	u := s.ldapUserForDirectory(w, r)
	if u == nil {
		return
	}
	name := directoryLookupName(u)
	key := userDirectoryCacheKey("dir", u.ID+"\x00"+name, s.ldap.DirectoryConfigDigest())
	s.serveCached(w, r, key, userDirectoryTTL, func(ctx context.Context) (any, error) {
		rd, err := s.ldap.ReadUser(ctx, name)
		if err != nil {
			return nil, ldapCachedErr(err)
		}
		return userDirectoryResp{DN: rd.User.DN, Attributes: rd.Attributes}, nil
	})
}

// orgPerson — organizasyon düğümü + eşleşen Coremetry hesabı.
type orgPerson struct {
	ldap.DirectoryPerson
	AccountID string `json:"accountId,omitempty"`
}

type userOrgResp struct {
	Self             orgPerson   `json:"self"`
	Managers         []orgPerson `json:"managers"`
	Reports          []orgPerson `json:"reports"`
	ReportsTruncated bool        `json:"reportsTruncated,omitempty"`
	// Zincir bir kademede okunamadıysa (yönetici silinmiş / erişim yok) o ana
	// kadarki zincir döner, neden burada.
	ChainError string `json:"chainError,omitempty"`
}

// linkOrgAccounts — SAF: düğümleri Coremetry hesaplarına bağlar (küçük harf
// e-posta, sonra kullanıcı adı = saklı ldap_username ya da e-posta yerel
// kısmı). Silinmiş/engelli hesap bağlanmaz (users yalnız aktifleri taşır).
func linkOrgAccounts(chart *ldap.OrgChart, users []chstore.User) userOrgResp {
	byEmail, byName := map[string]string{}, map[string]string{}
	for _, u := range users {
		if u.Disabled {
			continue
		}
		e := strings.ToLower(u.Email)
		byEmail[e] = u.ID
		if u.LdapUsername != "" {
			byName[strings.ToLower(u.LdapUsername)] = u.ID
		}
		if at := strings.IndexByte(e, '@'); at > 0 {
			if _, ok := byName[e[:at]]; !ok {
				byName[e[:at]] = u.ID
			}
		}
	}
	link := func(p ldap.DirectoryPerson) orgPerson {
		op := orgPerson{DirectoryPerson: p}
		if id, ok := byEmail[strings.ToLower(p.Email)]; ok && p.Email != "" {
			op.AccountID = id
		} else if id, ok := byName[strings.ToLower(p.Username)]; ok && p.Username != "" {
			op.AccountID = id
		}
		return op
	}
	out := userOrgResp{Self: link(chart.Self), Managers: []orgPerson{}, Reports: []orgPerson{},
		ReportsTruncated: chart.ReportsTruncated}
	for _, p := range chart.Managers {
		out.Managers = append(out.Managers, link(p))
	}
	for _, p := range chart.Reports {
		out.Reports = append(out.Reports, link(p))
	}
	return out
}

func (s *Server) getUserOrg(w http.ResponseWriter, r *http.Request) {
	u := s.ldapUserForDirectory(w, r)
	if u == nil {
		return
	}
	name := directoryLookupName(u)
	key := userDirectoryCacheKey("org", u.ID+"\x00"+name, s.ldap.DirectoryConfigDigest())
	s.serveCached(w, r, key, userOrgTTL, func(ctx context.Context) (any, error) {
		chart, err := s.ldap.OrgChart(ctx, name)
		if chart == nil {
			return nil, ldapCachedErr(err)
		}
		users, uerr := s.store.ListUsers(ctx)
		if uerr != nil {
			return nil, uerr
		}
		resp := linkOrgAccounts(chart, users)
		if err != nil {
			resp.ChainError = err.Error()
		}
		return resp, nil
	})
}
