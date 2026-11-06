package api

// preferences_display.go — v0.10.1832 (Log Search Faz 1 dilim 1.9; audit §8
// "Görünüm tercihleri: GET/PUT /api/preferences/{key}/display (ayrı dosya;
// saved_views, page='table:<key>')").
//
//   GET /api/preferences/{key}/display → {key, display|null, updatedAt}
//   PUT /api/preferences/{key}/display ← {v:1, rh?, hh?, den?, pp?, smp?} (≤4 KiB)
//
// Neden ayrı uç: /api/preferences/{key} gövdesi YALNIZ kolon modelini kabul
// eder (preferences_routes.go validateColumnModel); görünüm ayarlarını oraya
// sıkıştırmak kolon modelinin şemasını ve imzasını (sig) bozardı.
//
// Depolama invariant #5: yeni tablo YOK — saved_views satırı,
// page='table:<key>', owner_id=claims.UserID, name='display', id
// deterministik 'pref:<uid>:<key>:display' (kolon satırı 'pref:<uid>:<key>'
// ile çakışmaz) → UpsertSavedView tam-satır upsert (ReplacingMergeTree
// version; TÜM alanlar her yazımda taşınır), GetSavedView FINAL nokta okuma
// (LIMIT 1 — sınırlı okuma). Yalnız ÇAĞIRANIN kendi satırı: id uid taşır ve
// okumada owner yine doğrulanır; claims yoksa 401 (asla owner_id='').
// Kişisel durum: rol kapısı yok (viewer dahil), audit yok, serveCached yok
// (preferences_routes.go gerekçesi aynen). Sunucu yalnız ŞEKLİ doğrular.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("preferences_display", (*Server).registerDisplayPrefRoutes) }

func (s *Server) registerDisplayPrefRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/preferences/{key}/display", s.getDisplayPref)
	mux.HandleFunc("PUT /api/preferences/{key}/display", s.putDisplayPref)
}

const displayPrefBodyMax = 4 << 10

// displayPrefStore — handler'ın ihtiyaç duyduğu iki çağrı (test ikilisi için).
type displayPrefStore interface {
	GetSavedView(ctx context.Context, id string) (*chstore.SavedView, error)
	UpsertSavedView(ctx context.Context, v chstore.SavedView) error
}

// displayPrefBody — lib/types.ts DisplayPref aynası. Boş alan = "tercih yok".
type displayPrefBody struct {
	V   int    `json:"v"`
	RH  string `json:"rh,omitempty"`
	HH  string `json:"hh,omitempty"`
	Den string `json:"den,omitempty"`
	PP  int    `json:"pp,omitempty"`
	Smp int    `json:"smp,omitempty"`
}

func displayPrefID(uid, key string) string { return preferenceID(uid, key) + ":display" }

// validateDisplayPref — SAF: değerler URL sözleşmesiyle aynı kümeden
// (lib/logsDisplay.ts, lib/logsSample.ts); smp üst sınırı sabit tavan
// (logs_search.maxSample çalışma anında istemcide kırpılır).
func validateDisplayPref(b displayPrefBody) error {
	if b.V != 1 {
		return errors.New("v=1 bekleniyor")
	}
	lines := map[string]bool{"": true, "1": true, "2": true, "3": true, "auto": true}
	if !lines[b.RH] || !lines[b.HH] {
		return errors.New("rh/hh: 1 | 2 | 3 | auto")
	}
	if b.Den != "" && b.Den != "compact" && b.Den != "normal" && b.Den != "expanded" {
		return errors.New("den: compact | normal | expanded")
	}
	if b.PP != 0 && b.PP != 50 && b.PP != 100 && b.PP != 250 && b.PP != 500 {
		return errors.New("pp: 50 | 100 | 250 | 500")
	}
	if b.Smp != 0 && (b.Smp < logsSampleMin || b.Smp > logsSampleHardCap) {
		return fmt.Errorf("smp %d–%d olmalı", logsSampleMin, logsSampleHardCap)
	}
	return nil
}

// canonicalDisplayPrefJSON — sabit alan sırası (aynı tercih aynı bayt).
func canonicalDisplayPrefJSON(b displayPrefBody) string {
	b.V = 1
	out, _ := json.Marshal(b)
	return string(out)
}

func (s *Server) getDisplayPref(w http.ResponseWriter, r *http.Request) {
	serveDisplayPrefGet(w, r, s, s.store)
}

func (s *Server) putDisplayPref(w http.ResponseWriter, r *http.Request) {
	serveDisplayPrefPut(w, r, s, s.store)
}

// storeOrNil — *chstore.Store nil iken arayüze nil-tipli değer sızmasın.
func storeOrNil(st *chstore.Store) displayPrefStore {
	if st == nil {
		return nil
	}
	return st
}

func serveDisplayPrefGet(w http.ResponseWriter, r *http.Request, s *Server, st *chstore.Store) {
	serveDisplayPrefGetWith(w, r, s, storeOrNil(st))
}

func serveDisplayPrefPut(w http.ResponseWriter, r *http.Request, s *Server, st *chstore.Store) {
	serveDisplayPrefPutWith(w, r, s, storeOrNil(st))
}

func serveDisplayPrefGetWith(w http.ResponseWriter, r *http.Request, s *Server, st displayPrefStore) {
	claims, key, ok := s.preferenceClaims(w, r)
	if !ok {
		return
	}
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "tercih deposu bağlı değil")
		return
	}
	v, err := st.GetSavedView(r.Context(), displayPrefID(claims.UserID, key))
	if err != nil {
		writeErr(w, err)
		return
	}
	resp := map[string]any{"key": key, "display": nil}
	if v != nil && v.Name == "display" && v.OwnerID == claims.UserID {
		resp["display"] = json.RawMessage(v.QueryString)
		resp["updatedAt"] = v.CreatedAt
	}
	writeJSON(w, resp)
}

func serveDisplayPrefPutWith(w http.ResponseWriter, r *http.Request, s *Server, st displayPrefStore) {
	claims, key, ok := s.preferenceClaims(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, displayPrefBodyMax))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "gövde 4 KiB'ı aşamaz")
		return
	}
	var b displayPrefBody
	if err := json.Unmarshal(body, &b); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if err := validateDisplayPref(b); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "tercih deposu bağlı değil")
		return
	}
	now := time.Now().UnixNano()
	// Tam-satır upsert: TÜM alanlar (id, owner, name, page, query_string,
	// pinned=false, created_at) her yazımda — ReplacingMergeTree son sürümü tutar.
	if err := st.UpsertSavedView(r.Context(), chstore.SavedView{
		ID: displayPrefID(claims.UserID, key), OwnerID: claims.UserID, Name: "display",
		Page: preferencePagePrefix + key, QueryString: canonicalDisplayPrefJSON(b), Pinned: false, CreatedAt: now,
	}); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "key": key, "updatedAt": now})
}
