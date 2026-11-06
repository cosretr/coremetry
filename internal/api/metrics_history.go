package api

// metrics_history.go — v0.10.1864 (Metrics Explorer Faz 2 dilim 2.4; audit
// §3.1 satır 17, operatör kararı S9: "sorgu geçmişi sunucuda, kişisel 50").
//
//	GET    /api/metrics/history  → {entries:[…en yeni önce], enabled, maxEntries}
//	POST   /api/metrics/history  {q, cluster, status, series, durationMs, line?, col?}
//	DELETE /api/metrics/history  → tombstone
//
// Depo: PromQL konsolunun deseni (promql_console_history.go) — kullanıcı
// başına TEK saved_views blobu, page='metrics-history', id
// 'metrics-history:<uid>', owner_id=uid (invariant #5: yeni tablo YOK).
// Kilit, depo dikişi, token/boş-kimlik kuralı ve G/Ç son tarihi o dosyadan
// PAYLAŞILIR; yalnız sayfa, giriş şekli ve ekleme yolu farklı.
//
// NEDEN İSTEMCİ YAZAR (konsolun "handler yazar" kararının tersi): Metrics
// Explorer'ın kod kipi /api/metrics/promql'i serveCached arkasından
// çağırır; handler'ın bir isabetin seri sayısını ve süresini bilmesi
// gövdeyi yeniden çözmek demek. Geçmiş KİŞİSEL ve kendi blobuna yazılır:
// sahte bir POST yalnız çağıranın kendi 50 satırını değiştirebilir
// (başkasının geçmişine ya da paylaşımlı duruma erişim yok). Girdi
// sınırlanır: sorgu ≤ 8 KiB (maxPromQLQueryLen), durum ok|error, sayılar
// negatif değil. Ardışık özdeş koşu tek girişe iner.
//
// Rol: her kimlikli kullanıcı (viewer dahil — Metrics Explorer salt-okur
// bir yüzey, geçmiş kişisel tercih gibi). Audit YOK (konsol kararı 2:
// kişisel durum, paylaşılan/yönetimsel yazım değil). serveCached YOK
// (kişisel, mutasyona uğrayan satır).

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

func init() { registerRoutesExtra("metrics-history", (*Server).registerMetricsHistoryRoutes) }

func (s *Server) registerMetricsHistoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/metrics/history", s.getMetricsHistory)
	mux.HandleFunc("POST /api/metrics/history", s.postMetricsHistory)
	mux.HandleFunc("DELETE /api/metrics/history", s.deleteMetricsHistory)
}

const (
	metricsHistoryPage       = "metrics-history"
	metricsHistoryName       = "Metrics history"
	metricsHistoryMaxEntries = 50
	metricsHistoryMaxBytes   = 128 << 10
)

// metricsHistoryEntry — tek koşu. Kısa alan adları: 50 giriş blob duvarından yer.
type metricsHistoryEntry struct {
	Query      string `json:"q"`
	Cluster    string `json:"cluster,omitempty"`
	Status     string `json:"status"` // ok | error
	Series     int    `json:"series"`
	DurationMs int64  `json:"durationMs"`
	Line       int    `json:"line,omitempty"` // hata konumu (1 tabanlı)
	Col        int    `json:"col,omitempty"`
	At         int64  `json:"at"` // unix ms
}

type metricsHistoryBlob struct {
	V       int                   `json:"v"`
	Entries []metricsHistoryEntry `json:"entries"`
}

func metricsHistoryID(uid string) string { return metricsHistoryPage + ":" + uid }

// metricsHistoryPush — SAF (metrics_history_test.go). e en öne; baştaki
// özdeş (q + cluster) giriş onunla yer değiştirir; maxEntries'e kırpılır;
// JSON maxBytes'ı aşarsa en eski düşer. Girdi dilimi değiştirilmez.
func metricsHistoryPush(entries []metricsHistoryEntry, e metricsHistoryEntry, maxEntries, maxBytes int) ([]metricsHistoryEntry, string, error) {
	rest := entries
	if len(rest) > 0 && rest[0].Query == e.Query && rest[0].Cluster == e.Cluster {
		rest = rest[1:]
	}
	out := make([]metricsHistoryEntry, 0, len(rest)+1)
	out = append(out, e)
	out = append(out, rest...)
	if maxEntries > 0 && len(out) > maxEntries {
		out = out[:maxEntries]
	}
	for {
		raw, err := json.Marshal(metricsHistoryBlob{V: 1, Entries: out})
		if err != nil {
			return nil, "", err
		}
		if len(raw) <= maxBytes {
			return out, string(raw), nil
		}
		if len(out) <= 1 {
			return nil, "", errPromQLHistoryEntryTooLarge
		}
		out = out[:len(out)-1]
	}
}

// metricsHistoryValidate — SAF: POST gövdesinin sınırları.
func metricsHistoryValidate(e *metricsHistoryEntry) error {
	e.Query = strings.TrimSpace(e.Query)
	e.Cluster = strings.TrimSpace(e.Cluster)
	switch {
	case e.Query == "":
		return errors.New("q zorunlu")
	case len(e.Query) > maxPromQLQueryLen:
		return errors.New("q 8 KiB sınırını aşıyor")
	case len(e.Cluster) > 256:
		return errors.New("cluster çok uzun")
	case e.Status != "ok" && e.Status != "error":
		return errors.New("status ok ya da error olmalı")
	case e.Series < 0 || e.DurationMs < 0 || e.Line < 0 || e.Col < 0:
		return errors.New("sayılar negatif olamaz")
	}
	return nil
}

func loadMetricsHistory(ctx context.Context, st promqlHistoryStore, uid string) ([]metricsHistoryEntry, error) {
	v, err := st.GetSavedView(ctx, metricsHistoryID(uid))
	if err != nil {
		return nil, err
	}
	if v == nil || v.Name == "" || v.Page != metricsHistoryPage || v.OwnerID != uid {
		return nil, nil
	}
	var blob metricsHistoryBlob
	if err := json.Unmarshal([]byte(v.QueryString), &blob); err != nil {
		log.Printf("[metrics] geçmiş gövdesi okunamadı (uid=%s): %v", uid, err)
		return nil, nil
	}
	return blob.Entries, nil
}

type metricsHistoryResponse struct {
	Entries    []metricsHistoryEntry `json:"entries"`
	Enabled    bool                  `json:"enabled"`
	MaxEntries int                   `json:"maxEntries"`
}

func (s *Server) getMetricsHistory(w http.ResponseWriter, r *http.Request) {
	uid, ok := promqlUser(w, r)
	if !ok {
		return
	}
	resp := metricsHistoryResponse{Entries: []metricsHistoryEntry{}, Enabled: !promqlHistoryDisabledFor(uid), MaxEntries: metricsHistoryMaxEntries}
	st := promqlHistoryStoreOf(s)
	if !resp.Enabled || st == nil {
		writeJSON(w, resp)
		return
	}
	entries, err := loadMetricsHistory(r.Context(), st, uid)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			w.WriteHeader(statusClientClosedRequest)
			return
		}
		log.Printf("[metrics] geçmiş GET (uid=%s): %v", uid, err)
		writeJSONError(w, http.StatusServiceUnavailable, "query history is temporarily unavailable")
		return
	}
	if len(entries) > 0 {
		resp.Entries = entries
	}
	writeJSON(w, resp)
}

func (s *Server) postMetricsHistory(w http.ResponseWriter, r *http.Request) {
	uid, ok := promqlUser(w, r)
	if !ok {
		return
	}
	var e metricsHistoryEntry
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&e); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if err := metricsHistoryValidate(&e); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	e.At = time.Now().UnixMilli()
	if promqlHistoryDisabledFor(uid) {
		writeJSON(w, map[string]any{"ok": true, "stored": false})
		return
	}
	st := promqlHistoryStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "query history store is not connected")
		return
	}
	release, got := promqlHistoryAcquire(uid, promqlHistoryWriteTimeout)
	if !got {
		writeJSONError(w, http.StatusServiceUnavailable, "query history is temporarily unavailable")
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), promqlHistoryWriteTimeout)
	defer cancel()
	cur, err := loadMetricsHistory(ctx, st, uid)
	if err != nil {
		// Okuma hatasında YAZILMAZ: boş listeyle üzerine yazmak geçmişi silerdi.
		log.Printf("[metrics] geçmiş okunamadı, ekleme atlandı (uid=%s): %v", uid, err)
		writeJSONError(w, http.StatusServiceUnavailable, "query history is temporarily unavailable")
		return
	}
	_, raw, err := metricsHistoryPush(cur, e, metricsHistoryMaxEntries, metricsHistoryMaxBytes)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := st.UpsertSavedView(ctx, chstore.SavedView{
		ID: metricsHistoryID(uid), OwnerID: uid, Name: metricsHistoryName, Page: metricsHistoryPage,
		QueryString: raw, CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		log.Printf("[metrics] geçmiş yazılamadı (uid=%s): %v", uid, err)
		writeJSONError(w, http.StatusServiceUnavailable, "query history is temporarily unavailable")
		return
	}
	writeJSON(w, map[string]any{"ok": true, "stored": true})
}

func (s *Server) deleteMetricsHistory(w http.ResponseWriter, r *http.Request) {
	uid, ok := promqlUser(w, r)
	if !ok {
		return
	}
	if promqlHistoryDisabledFor(uid) {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	st := promqlHistoryStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "query history store is not connected")
		return
	}
	release, got := promqlHistoryAcquire(uid, promqlHistoryWriteTimeout)
	if !got {
		writeJSONError(w, http.StatusServiceUnavailable, "query history is temporarily unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), promqlHistoryWriteTimeout)
	err := st.UpsertSavedView(ctx, chstore.SavedView{
		ID: metricsHistoryID(uid), OwnerID: uid, Name: "", Page: metricsHistoryPage,
		QueryString: "", CreatedAt: time.Now().UnixNano(),
	})
	cancel()
	release()
	if err != nil {
		log.Printf("[metrics] geçmiş DELETE (uid=%s): %v", uid, err)
		writeJSONError(w, http.StatusServiceUnavailable, "query history is temporarily unavailable")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
