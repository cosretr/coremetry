package api

// cosre_user_prefs.go — v0.10.2334 (conversation-audit §6.1 G; karar 13, 15):
// CoSRE'nin KULLANICI BAŞINA deposu — hitap tercihi (yalnız sohbet turu
// okur ve yazar; HTTP ucu yok — v0.10.2462'de eş ad uçlarıyla kaldırıldı).
//
// Depolama (invariant #5, karar 15): saved_views page='cosre_user', kullanıcı
// başına TEK satır — id deterministik 'cosre_user:<uid>', owner_id=uid,
// query_string = JSON nesnesi {v, addr?, …}. Okunan tek alan addr;
// tanınmayan anahtarlar okumada yok sayılır, yazımda AYNEN korunur (v0.10.2462:
// kaldırılan kişisel eş adların eski satırlardaki `m` anahtarı silinmez,
// taşınmaz — bkz. DECISIONS 2026-11-22).
//
// Yalnız ÇAĞIRANIN kendi satırı (claims.UserID; okumada owner yine
// doğrulanır); viewer dahil her rol kendi kaydını yazar (kişisel durum —
// preferences_display.go emsali: rol kapısı yok, audit yok).
//
// Okuma: tur başına en çok bir nokta okuma (GetSavedView FINAL LIMIT 1),
// süreç içi 30 s önbellekli — kaydı olmayan kullanıcı da önbelleğe girer
// (boş kayıt), yazım önbelleği hemen günceller.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	cosreUserPage     = "cosre_user"
	cosreUserCacheTTL = 30 * time.Second
	cosreUserCacheMax = 4096
)

func cosreUserRowID(uid string) string { return "cosre_user:" + uid }

// cosreUserPrefs — satırın okunan alanı + tanınmayan anahtarlar (yazımda korunur).
type cosreUserPrefs struct {
	Addr  string                     // sen | siz (karar 13)
	extra map[string]json.RawMessage // v, addr dışındaki saklı anahtarlar
}

// encodeCosreUserPrefs — SAF: anahtarlar sıralı (aynı kayıt aynı bayt).
func encodeCosreUserPrefs(p cosreUserPrefs) string {
	m := make(map[string]any, len(p.extra)+2)
	for k, v := range p.extra {
		m[k] = v
	}
	m["v"] = 1
	if p.Addr != "" {
		m["addr"] = p.Addr
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// decodeCosreUserPrefs — SAF, hoşgörülü: bozuk gövde = boş kayıt.
func decodeCosreUserPrefs(s string) cosreUserPrefs {
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(s), &raw) != nil {
		return cosreUserPrefs{}
	}
	var p cosreUserPrefs
	if a, ok := raw["addr"]; ok {
		_ = json.Unmarshal(a, &p.Addr)
	}
	delete(raw, "addr")
	delete(raw, "v")
	if len(raw) > 0 {
		p.extra = raw
	}
	return p
}

// ── depo + önbellek ─────────────────────────────────────────────────────────

// cosreUserPrefsStoreOf — depo dikişi (test sahte depo verir).
var cosreUserPrefsStoreOf = func(s *Server) displayPrefStore { return storeOrNil(s.store) }

type cosreUserPrefsEntry struct {
	p  cosreUserPrefs
	at time.Time
}

var cosreUserPrefsCache = struct {
	sync.Mutex
	m map[string]cosreUserPrefsEntry
}{m: map[string]cosreUserPrefsEntry{}}

// cosreUserPrefsWriteMu — süreç içi okuma-değiştirme-yazma sırası.
var cosreUserPrefsWriteMu sync.Mutex

func cosreUserPrefsCachePut(uid string, p cosreUserPrefs) {
	cosreUserPrefsCache.Lock()
	if len(cosreUserPrefsCache.m) >= cosreUserCacheMax {
		cosreUserPrefsCache.m = map[string]cosreUserPrefsEntry{}
	}
	cosreUserPrefsCache.m[uid] = cosreUserPrefsEntry{p: p, at: time.Now()}
	cosreUserPrefsCache.Unlock()
}

// resetCosreUserPrefsCache — testler.
func resetCosreUserPrefsCache() {
	cosreUserPrefsCache.Lock()
	cosreUserPrefsCache.m = map[string]cosreUserPrefsEntry{}
	cosreUserPrefsCache.Unlock()
}

// cosreUserPrefsOf — kullanıcının kaydı (yoksa boş); 30 s önbellekli.
func (s *Server) cosreUserPrefsOf(ctx context.Context, uid string, fresh bool) (cosreUserPrefs, error) {
	return cosreUserPrefsFrom(ctx, cosreUserPrefsStoreOf(s), uid, fresh)
}

func cosreUserPrefsFrom(ctx context.Context, st displayPrefStore, uid string, fresh bool) (cosreUserPrefs, error) {
	if !fresh {
		cosreUserPrefsCache.Lock()
		e, ok := cosreUserPrefsCache.m[uid]
		cosreUserPrefsCache.Unlock()
		if ok && time.Since(e.at) < cosreUserCacheTTL {
			return e.p, nil
		}
	}
	if st == nil {
		return cosreUserPrefs{}, nil
	}
	v, err := st.GetSavedView(ctx, cosreUserRowID(uid))
	if err != nil {
		return cosreUserPrefs{}, err
	}
	p := cosreUserPrefs{}
	if v != nil && v.OwnerID == uid && v.Page == cosreUserPage && v.Name == cosreUserPage {
		p = decodeCosreUserPrefs(v.QueryString)
	}
	cosreUserPrefsCachePut(uid, p)
	return p, nil
}

var errCosreUserNoStore = errors.New("tercih deposu bağlı değil")

// updateCosreUserPrefsIn — taze okuma + değişiklik + tam satır upsert
// (tanınmayan anahtarlar taşınır). fn false dönerse yazım yok.
func updateCosreUserPrefsIn(ctx context.Context, st displayPrefStore, uid string, fn func(cosreUserPrefs) (cosreUserPrefs, bool)) (cosreUserPrefs, error) {
	if st == nil {
		return cosreUserPrefs{}, errCosreUserNoStore
	}
	cosreUserPrefsWriteMu.Lock()
	defer cosreUserPrefsWriteMu.Unlock()
	cur, err := cosreUserPrefsFrom(ctx, st, uid, true)
	if err != nil {
		return cur, err
	}
	next, changed := fn(cur)
	if !changed {
		return cur, nil
	}
	if err := st.UpsertSavedView(ctx, chstore.SavedView{
		ID: cosreUserRowID(uid), OwnerID: uid, Name: cosreUserPage, Page: cosreUserPage,
		QueryString: encodeCosreUserPrefs(next), Pinned: false, CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		return cur, err
	}
	cosreUserPrefsCachePut(uid, next)
	return next, nil
}

// cosreUserTurnPrefs — sohbet turunun kaydı: kullanıcısız istek ve
// değerlendirme koşusu (chatEvalMode) hiç okumaz; okuma hatası = kayıt yok.
func (s *Server) cosreUserTurnPrefs(ctx context.Context) (string, cosreUserPrefs) {
	if chatEvalModeFrom(ctx) != nil {
		return "", cosreUserPrefs{}
	}
	c := auth.FromContext(ctx)
	if c == nil || c.UserID == "" {
		return "", cosreUserPrefs{}
	}
	p, err := s.cosreUserPrefsOf(ctx, c.UserID, false)
	if err != nil {
		return c.UserID, cosreUserPrefs{}
	}
	return c.UserID, p
}

// cosreUserAddressJob — karar 13: turda kullanıcının yazımından AÇIKÇA
// çıkan hitap kayıttakinden farklıysa yazım işi (nil = iş yok). Karar ve
// depo seçimi çağıranda (eşzamanlı); yazım işin kendisinde.
func (s *Server) cosreUserAddressJob(uid, addr string) func() {
	if uid == "" || (addr != addressSen && addr != addressSiz) {
		return nil
	}
	cosreUserPrefsCache.Lock()
	e, ok := cosreUserPrefsCache.m[uid]
	cosreUserPrefsCache.Unlock()
	if (ok && e.p.Addr == addr) || (!ok && addr == addressSen) || (ok && e.p.Addr == "" && addr == addressSen) {
		return nil // değişmedi; kaydı olmayan kullanıcıda varsayılan için satır açılmaz
	}
	st := cosreUserPrefsStoreOf(s)
	if st == nil {
		return nil
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cosreUserWriteAddress(ctx, st, uid, addr)
	}
}

func cosreUserWriteAddress(ctx context.Context, st displayPrefStore, uid, addr string) {
	_, _ = updateCosreUserPrefsIn(ctx, st, uid, func(p cosreUserPrefs) (cosreUserPrefs, bool) {
		if p.Addr == addr || (p.Addr == "" && addr == addressSen) {
			return p, false
		}
		p.Addr = addr
		return p, true
	})
}
