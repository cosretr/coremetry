package api

// dashboard_list_meta.go — v0.10.1996 (Dashboards Faz 3 dilim 3.2; audit
// §5.2, mockup görünüm 4; operatör kararı S7).
//
//	GET  /api/dashboards/editors → {editors:[{dashboardId, actor, at}]}
//	GET  /api/dashboards/recent  → {entries:[{id, at}…en yeni önce], enabled}
//	POST /api/dashboards/recent  {id}
//
// "Son düzenleyen": audit_log'dan türetilir (dashboards'a kolon YOK — şema
// değişmez, S7). Paylaşılan bilgi → serveCached 60 sn; anahtarın girdisi
// yok (tüm panolar, tek pencere = audit TTL'i), bu yüzden sabit.
//
// "Son görüntülemeniz": KİŞİSEL, saved_views page='dashboard-recent', kullanıcı
// başına TEK blob (son 50 pano id + zaman) — metrics_history.go deseni
// (invariant #5: yeni tablo YOK). İstemci pano açılınca POST eder. Viewer
// dahil her kimlikli kullanıcı yazar: yalnız kendi blobunu değiştirir;
// audit YOK (kişisel durum, yönetimsel yazım değil — metrics-history kararı);
// serveCached YOK (mutasyona uğrayan kişisel satır).
//
// NEDEN dashboard-star gibi satır-başına-kayıt DEĞİL: her açılış bir satır
// üretirdi (createSavedView upsert değil); tek blob okuma yolunu tek GET'e,
// yazımı tek upsert'e indirir.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("dashboard-list-meta", (*Server).registerDashboardListMetaRoutes) }

func (s *Server) registerDashboardListMetaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/dashboards/editors", s.getDashboardEditors)
	mux.HandleFunc("GET /api/dashboards/recent", s.getDashboardRecent)
	mux.HandleFunc("POST /api/dashboards/recent", s.postDashboardRecent)
}

// ── Son düzenleyen ──────────────────────────────────────────────────────────

const dashboardEditorsKey = "dash-editors:v1"

func (s *Server) getDashboardEditors(w http.ResponseWriter, r *http.Request) {
	s.serveCached(w, r, dashboardEditorsKey, 60*time.Second, func(ctx context.Context) (any, error) {
		rows, err := s.store.DashboardLastEditors(ctx)
		if err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []chstore.DashboardEditor{}
		}
		return map[string]any{"editors": rows}, nil
	})
}

// ── Son görüntülemeniz ──────────────────────────────────────────────────────

const (
	dashboardRecentPage       = "dashboard-recent"
	dashboardRecentName       = "Dashboard recent"
	dashboardRecentMaxEntries = 50
	dashboardRecentMaxIDLen   = 128
)

type dashboardRecentEntry struct {
	ID string `json:"id"`
	At int64  `json:"at"` // unix ms
}

type dashboardRecentBlob struct {
	V       int                    `json:"v"`
	Entries []dashboardRecentEntry `json:"entries"`
}

func dashboardRecentID(uid string) string { return dashboardRecentPage + ":" + uid }

// dashboardRecentPush — SAF (dashboard_list_meta_test.go). e en öne; aynı
// pano listede başka yerdeyse oradan kalkar (pano başına tek giriş);
// maxEntries'e kırpılır. Girdi dilimi değiştirilmez.
func dashboardRecentPush(entries []dashboardRecentEntry, e dashboardRecentEntry, maxEntries int) []dashboardRecentEntry {
	out := make([]dashboardRecentEntry, 0, len(entries)+1)
	out = append(out, e)
	for _, x := range entries {
		if x.ID != e.ID {
			out = append(out, x)
		}
	}
	if maxEntries > 0 && len(out) > maxEntries {
		out = out[:maxEntries]
	}
	return out
}

func loadDashboardRecent(ctx context.Context, st promqlHistoryStore, uid string) ([]dashboardRecentEntry, error) {
	v, err := st.GetSavedView(ctx, dashboardRecentID(uid))
	if err != nil {
		return nil, err
	}
	if v == nil || v.Name == "" || v.Page != dashboardRecentPage || v.OwnerID != uid {
		return nil, nil
	}
	var blob dashboardRecentBlob
	if err := json.Unmarshal([]byte(v.QueryString), &blob); err != nil {
		log.Printf("[dashboards] son görüntülenen gövdesi okunamadı (uid=%s): %v", uid, err)
		return nil, nil
	}
	return blob.Entries, nil
}

func (s *Server) getDashboardRecent(w http.ResponseWriter, r *http.Request) {
	uid, ok := promqlUser(w, r)
	if !ok {
		return
	}
	resp := map[string]any{"entries": []dashboardRecentEntry{}, "enabled": !promqlHistoryDisabledFor(uid)}
	st := promqlHistoryStoreOf(s)
	if promqlHistoryDisabledFor(uid) || st == nil {
		writeJSON(w, resp)
		return
	}
	entries, err := loadDashboardRecent(r.Context(), st, uid)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			w.WriteHeader(statusClientClosedRequest)
			return
		}
		log.Printf("[dashboards] son görüntülenen GET (uid=%s): %v", uid, err)
		writeJSONError(w, http.StatusServiceUnavailable, "recent dashboards are temporarily unavailable")
		return
	}
	if len(entries) > 0 {
		resp["entries"] = entries
	}
	writeJSON(w, resp)
}

func (s *Server) postDashboardRecent(w http.ResponseWriter, r *http.Request) {
	uid, ok := promqlUser(w, r)
	if !ok {
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" || len(in.ID) > dashboardRecentMaxIDLen {
		writeJSONError(w, http.StatusBadRequest, "id 1-128 karakter olmalı")
		return
	}
	if promqlHistoryDisabledFor(uid) {
		writeJSON(w, map[string]any{"ok": true, "stored": false})
		return
	}
	st := promqlHistoryStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "recent dashboards store is not connected")
		return
	}
	release, got := promqlHistoryAcquire(uid, promqlHistoryWriteTimeout)
	if !got {
		writeJSONError(w, http.StatusServiceUnavailable, "recent dashboards are temporarily unavailable")
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), promqlHistoryWriteTimeout)
	defer cancel()
	cur, err := loadDashboardRecent(ctx, st, uid)
	if err != nil {
		// Okuma hatasında YAZILMAZ: boş listeyle üzerine yazmak listeyi silerdi.
		log.Printf("[dashboards] son görüntülenen okunamadı, ekleme atlandı (uid=%s): %v", uid, err)
		writeJSONError(w, http.StatusServiceUnavailable, "recent dashboards are temporarily unavailable")
		return
	}
	next := dashboardRecentPush(cur, dashboardRecentEntry{ID: in.ID, At: time.Now().UnixMilli()}, dashboardRecentMaxEntries)
	raw, err := json.Marshal(dashboardRecentBlob{V: 1, Entries: next})
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := st.UpsertSavedView(ctx, chstore.SavedView{
		ID: dashboardRecentID(uid), OwnerID: uid, Name: dashboardRecentName, Page: dashboardRecentPage,
		QueryString: string(raw), CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		log.Printf("[dashboards] son görüntülenen yazılamadı (uid=%s): %v", uid, err)
		writeJSONError(w, http.StatusServiceUnavailable, "recent dashboards are temporarily unavailable")
		return
	}
	writeJSON(w, map[string]any{"ok": true, "stored": true})
}
