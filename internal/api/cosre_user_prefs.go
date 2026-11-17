package api

// cosre_user_prefs.go — v0.10.2334 (conversation-audit §6.1 G; karar 13, 15):
// CoSRE'nin KULLANICI BAŞINA deposu — düzeltmeden öğrenilen eş adlar ve
// hitap tercihi.
//
//   GET    /api/cosre/user-prefs                  → {address, aliases[{phrase, entityId, name, notId, notName, at, stale}], maxAliases, bytes, maxBytes}
//   POST   /api/cosre/user-prefs/aliases          ← {phrase, entityId, notId?, exchangeId?}  (eş ad önerisi kartının Kaydet'i)
//   DELETE /api/cosre/user-prefs/aliases?phrase=… (kendi eşlemesini siler)
//
// Depolama (invariant #5, karar 15): saved_views page='cosre_user', kullanıcı
// başına TEK satır — id deterministik 'cosre_user:<uid>', owner_id=uid,
// query_string = kanonik JSON {v, addr?, m[{p, id, not?, at}]}. Tavanlar: ≤50
// eşleme ve ≤4 KiB; aşan yazımda EN ESKİ eşleme düşer. Deyim ≤40 rune ve
// yalnız kullanıcının yazdığı varlık deyimi (mesaj metni değil); hedef ve
// yanlış aday entity kimliği (svc:<ad>). Başka kullanıcının verisi yok.
//
// Yalnız ÇAĞIRANIN kendi satırı (claims.UserID; okumada owner yine
// doğrulanır); viewer dahil her rol kendi kaydını yazar (kişisel durum —
// preferences_display.go emsali: rol kapısı yok, audit yok). Bayat eşleme
// (hedef canlı servis kataloğunda yok) çözücüde yok sayılır, GET'te
// `stale: true` ile listelenir.
//
// Okuma: tur başına en çok bir nokta okuma (GetSavedView FINAL LIMIT 1),
// süreç içi 30 s önbellekli — kaydı olmayan kullanıcı da önbelleğe girer
// (boş kayıt), yazım önbelleği hemen günceller.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func init() { registerRoutesExtra("cosre-user-prefs", (*Server).registerCosreUserPrefsRoutes) }

func (s *Server) registerCosreUserPrefsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cosre/user-prefs", s.getCosreUserPrefs)
	mux.HandleFunc("POST /api/cosre/user-prefs/aliases", s.postCosreUserAlias)
	mux.HandleFunc("DELETE /api/cosre/user-prefs/aliases", s.deleteCosreUserAlias)
}

const (
	cosreUserPage           = "cosre_user"
	cosreUserMaxAliases     = 50
	cosreUserMaxBytes       = 4 << 10
	cosreUserPhraseMaxRunes = 40
	cosreUserPhraseMinRunes = 3
	cosreUserCacheTTL       = 30 * time.Second
	cosreUserCacheMax       = 4096
)

func cosreUserRowID(uid string) string { return "cosre_user:" + uid }

// cosreUserPrefs — satırın kanonik gövdesi. M en eskiden yeniye.
type cosreUserPrefs struct {
	V    int              `json:"v"`
	Addr string           `json:"addr,omitempty"` // sen | siz (karar 13)
	M    []cosreUserAlias `json:"m,omitempty"`
}

// cosreUserAlias — tek eşleme: deyim → hedef, deyim ↛ yanlış aday.
type cosreUserAlias struct {
	P   string `json:"p"`             // kullanıcının yazdığı deyim (≤40 rune)
	ID  string `json:"id"`            // hedef entity id (svc:<ad>)
	Not string `json:"not,omitempty"` // yanlış çıkmış aday (svc:<ad>)
	At  int64  `json:"at"`            // unix s
}

// encodeCosreUserPrefs — SAF: sabit alan sırası (aynı kayıt aynı bayt).
func encodeCosreUserPrefs(p cosreUserPrefs) string {
	p.V = 1
	b, _ := json.Marshal(p)
	return string(b)
}

// decodeCosreUserPrefs — SAF, hoşgörülü: bozuk gövde = boş kayıt.
func decodeCosreUserPrefs(s string) cosreUserPrefs {
	var p cosreUserPrefs
	if json.Unmarshal([]byte(s), &p) != nil {
		return cosreUserPrefs{V: 1}
	}
	return capCosreUserPrefs(p)
}

// capCosreUserPrefs — SAF: ≤50 eşleme ve ≤4 KiB; en eski önce düşer.
func capCosreUserPrefs(p cosreUserPrefs) cosreUserPrefs {
	p.V = 1
	if len(p.M) > cosreUserMaxAliases {
		p.M = append([]cosreUserAlias(nil), p.M[len(p.M)-cosreUserMaxAliases:]...)
	}
	for len(p.M) > 0 && len(encodeCosreUserPrefs(p)) > cosreUserMaxBytes {
		p.M = append([]cosreUserAlias(nil), p.M[1:]...)
	}
	return p
}

// withCosreUserAlias — SAF: aynı deyim anahtarlı eski eşleme düşer, yenisi
// sona eklenir, tavanlar uygulanır.
func withCosreUserAlias(p cosreUserPrefs, a cosreUserAlias) cosreUserPrefs {
	key := mcptools.AliasKey(a.P)
	out := make([]cosreUserAlias, 0, len(p.M)+1)
	for _, m := range p.M {
		if mcptools.AliasKey(m.P) != key {
			out = append(out, m)
		}
	}
	p.M = append(out, a)
	return capCosreUserPrefs(p)
}

// withoutCosreUserAlias — SAF: deyim anahtarlı eşlemeyi siler.
func withoutCosreUserAlias(p cosreUserPrefs, phrase string) (cosreUserPrefs, bool) {
	key := mcptools.AliasKey(phrase)
	out := make([]cosreUserAlias, 0, len(p.M))
	for _, m := range p.M {
		if mcptools.AliasKey(m.P) != key {
			out = append(out, m)
		}
	}
	removed := len(out) != len(p.M)
	p.M = out
	if len(p.M) == 0 {
		p.M = nil
	}
	return p, removed
}

// cosreUserEntityAliases — SAF: çözücüye giden eş adlar (en yeni önce).
func cosreUserEntityAliases(p cosreUserPrefs) []mcptools.EntityAlias {
	out := make([]mcptools.EntityAlias, 0, len(p.M))
	for i := len(p.M) - 1; i >= 0; i-- {
		out = append(out, mcptools.EntityAlias{Phrase: p.M[i].P, ID: p.M[i].ID, Not: p.M[i].Not})
	}
	return out
}

// cosreAliasPhrase — SAF: deyimi doğrular ve tek boşluklu biçime getirir.
// Katalogdaki bir servisin adı eş ad olamaz (açık ad zaten çözülür).
func cosreAliasPhrase(phrase string, services []string) (string, error) {
	p := strings.Join(strings.Fields(phrase), " ")
	n := utf8.RuneCountInString(p)
	if n < cosreUserPhraseMinRunes || n > cosreUserPhraseMaxRunes || len(mcptools.AliasStems(p)) == 0 {
		return "", errors.New("deyim 3–40 karakter olmalı")
	}
	key := mcptools.AliasKey(p)
	for _, s := range services {
		if mcptools.AliasKey(s) == key {
			return "", errors.New("bu bir servis adı; eş ad gerekmez")
		}
	}
	return p, nil
}

// svcIDName — "svc:<ad>" → ad ("" = servis kimliği değil).
func svcIDName(id string) string {
	name, ok := strings.CutPrefix(strings.TrimSpace(id), "svc:")
	if !ok || name == "" || len(name) > 256 {
		return ""
	}
	return name
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
		return cosreUserPrefs{V: 1}, nil
	}
	v, err := st.GetSavedView(ctx, cosreUserRowID(uid))
	if err != nil {
		return cosreUserPrefs{V: 1}, err
	}
	p := cosreUserPrefs{V: 1}
	if v != nil && v.OwnerID == uid && v.Page == cosreUserPage && v.Name == cosreUserPage {
		p = decodeCosreUserPrefs(v.QueryString)
	}
	cosreUserPrefsCachePut(uid, p)
	return p, nil
}

var errCosreUserNoStore = errors.New("tercih deposu bağlı değil")

// updateCosreUserPrefs — taze okuma + değişiklik + tam satır upsert.
// fn false dönerse yazım yok.
func (s *Server) updateCosreUserPrefs(ctx context.Context, uid string, fn func(cosreUserPrefs) (cosreUserPrefs, bool)) (cosreUserPrefs, error) {
	return updateCosreUserPrefsIn(ctx, cosreUserPrefsStoreOf(s), uid, fn)
}

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
	next = capCosreUserPrefs(next)
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

// ── uçlar ───────────────────────────────────────────────────────────────────

type cosreUserAliasView struct {
	Phrase   string `json:"phrase"`
	EntityID string `json:"entityId"`
	Name     string `json:"name"`
	NotID    string `json:"notId,omitempty"`
	NotName  string `json:"notName,omitempty"`
	At       int64  `json:"at"`
	Stale    bool   `json:"stale"`
}

// cosreUserPrefsView — SAF: GET gövdesi (en yeni önce; bayat işaretli).
func cosreUserPrefsView(p cosreUserPrefs, services []string) map[string]any {
	live := make(map[string]bool, len(services))
	for _, s := range services {
		live[s] = true
	}
	list := make([]cosreUserAliasView, 0, len(p.M))
	for i := len(p.M) - 1; i >= 0; i-- {
		m := p.M[i]
		name := svcIDName(m.ID)
		list = append(list, cosreUserAliasView{Phrase: m.P, EntityID: m.ID, Name: name, NotID: m.Not, NotName: svcIDName(m.Not),
			At: m.At, Stale: name == "" || !live[name]})
	}
	p.V = 1
	return map[string]any{"address": p.Addr, "aliases": list, "maxAliases": cosreUserMaxAliases,
		"bytes": len(encodeCosreUserPrefs(p)), "maxBytes": cosreUserMaxBytes}
}

func cosreUserClaims(w http.ResponseWriter, r *http.Request) (*auth.Claims, bool) {
	c := auth.FromContext(r.Context())
	if c == nil || c.UserID == "" {
		writeJSONError(w, http.StatusUnauthorized, "kimlik gerekli")
		return nil, false
	}
	return c, true
}

func (s *Server) getCosreUserPrefs(w http.ResponseWriter, r *http.Request) {
	c, ok := cosreUserClaims(w, r)
	if !ok {
		return
	}
	p, err := s.cosreUserPrefsOf(r.Context(), c.UserID, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, cosreUserPrefsView(p, s.guidedServiceNames(r.Context())))
}

type cosreUserAliasPost struct {
	Phrase     string `json:"phrase"`
	EntityID   string `json:"entityId"`
	NotID      string `json:"notId"`
	ExchangeID string `json:"exchangeId"`
}

func (s *Server) postCosreUserAlias(w http.ResponseWriter, r *http.Request) {
	c, ok := cosreUserClaims(w, r)
	if !ok {
		return
	}
	var in cosreUserAliasPost
	r.Body = http.MaxBytesReader(w, r.Body, 2<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON")
		return
	}
	services := s.guidedServiceNames(r.Context())
	phrase, err := cosreAliasPhrase(in.Phrase, services)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := svcIDName(in.EntityID)
	if name == "" || !containsExact(services, name) {
		writeJSONError(w, http.StatusBadRequest, "entityId: katalogda olan bir servis (svc:<ad>)")
		return
	}
	not := ""
	if strings.TrimSpace(in.NotID) != "" {
		if n := svcIDName(in.NotID); n == "" || n == name {
			writeJSONError(w, http.StatusBadRequest, "notId: hedeften farklı bir servis (svc:<ad>)")
			return
		}
		not = "svc:" + svcIDName(in.NotID)
	}
	a := cosreUserAlias{P: phrase, ID: "svc:" + name, Not: not, At: time.Now().Unix()}
	p, err := s.updateCosreUserPrefs(r.Context(), c.UserID, func(p cosreUserPrefs) (cosreUserPrefs, bool) {
		return withCosreUserAlias(p, a), true
	})
	if err != nil {
		if errors.Is(err, errCosreUserNoStore) {
			writeJSONError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeErr(w, err)
		return
	}
	// Tek nötr sistem sinyali (kalite puanına girmez; deyim ve kullanıcı yok).
	xid := clipBytes(strings.TrimSpace(in.ExchangeID), aiFeedbackMaxIDLen)
	s.writeSystemSignals(r.Context(), xid, []chstore.CosreSignal{{ExchangeID: xid, Kind: cosrelearn.SigAliasSaved,
		UserEmail: cosrelearn.SystemSignalActor, Detail: "kind=service"}})
	writeJSON(w, cosreUserPrefsView(p, services))
}

func (s *Server) deleteCosreUserAlias(w http.ResponseWriter, r *http.Request) {
	c, ok := cosreUserClaims(w, r)
	if !ok {
		return
	}
	phrase := strings.TrimSpace(r.URL.Query().Get("phrase"))
	if phrase == "" {
		writeJSONError(w, http.StatusBadRequest, "phrase gerekli")
		return
	}
	removed := false
	p, err := s.updateCosreUserPrefs(r.Context(), c.UserID, func(p cosreUserPrefs) (cosreUserPrefs, bool) {
		next, ok := withoutCosreUserAlias(p, phrase)
		removed = ok
		return next, ok
	})
	if err != nil {
		if errors.Is(err, errCosreUserNoStore) {
			writeJSONError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeErr(w, err)
		return
	}
	if !removed {
		writeJSONError(w, http.StatusNotFound, "eşleme yok")
		return
	}
	writeJSON(w, cosreUserPrefsView(p, s.guidedServiceNames(r.Context())))
}

func containsExact(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
