package api

// anomaly_feed_routes.go — v0.10.2080: GET /api/anomalies/events gövdesi
// (rota api.go'da kalır; gövde buraya taşındı, api.go büyümez).
//
// Değişen: liste eski olaylarla birlikte ML olaylarını da taşır
// (chstore.ListAnomalyFeed, kind "ml:<dedektör>"); ML'e taşınmış eski
// satır (legacyId) bir kez görünür. Servis sayfası ve Pod şeridi bu ucu
// okur. Ek maliyet: istek başına bir ml_events okuması (FINAL, last_seen
// WHERE, LIMIT, max_execution_time 5), 30 sn önbellekli. ES çağrısı yok.

import (
	"context"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// anomalyFeedCachePrefix — /api/anomalies/events anahtarı (api.go); ML
// kararı / ayarı yazımı düşürür (ml.go).
const anomalyFeedCachePrefix = "anomaly:events:"

func (s *Server) buildAnomalyEvents(ctx context.Context, sinceNs int64, limit int) (any, error) {
	rows, err := s.store.ListAnomalyFeed(ctx, chstore.ListAnomalyEventsFilter{SinceNs: sinceNs, Limit: limit})
	if err != nil {
		return nil, err
	}
	sqlActive, sqlCleared, cErr := s.store.CountAnomalyEventsByStatus(ctx, sinceNs, 0)
	if cErr != nil {
		// Soft-fail: sayım yoksa frontend sayfa-türevine düşer.
		sqlActive, sqlCleared = 0, 0
	}
	activeN, clearedN := feedStatusTotals(rows, limit, sqlActive, sqlCleared)
	// v0.10.1032 — zenginleştirme zinciri (cluster → deploy → kök neden
	// → karar) tekil okumayla ORTAK: anomaly_event_get.go. ML satırında kök
	// neden / eski karar bulunmaz, ML kararı okumada eklenmiştir.
	rows = enrichAnomalyEvents(ctx, s.store, rows)
	return map[string]any{
		"items":        rows,
		"activeTotal":  activeN,
		"clearedTotal": clearedN,
		"truncated":    len(rows) >= limit,
	}, nil
}

// feedStatusTotals — SAF: zarfın durum sayaçları LİSTEYLE aynı kümeyi
// saysın. Liste kesilmediyse sayaçlar satırlardan (eski + ML, tekilleşmiş);
// kesildiyse eski tablonun SQL sayımı + listedeki ML satırları (kesik
// listede ML'in tam sayısı bilinmez; zarf truncated=true söyler).
func feedStatusTotals(rows []chstore.AnomalyEvent, limit int, sqlActive, sqlCleared uint64) (active, cleared uint64) {
	truncated := len(rows) >= limit
	for _, e := range rows {
		if truncated && !chstore.IsMLAnomalyKind(e.Kind) {
			continue
		}
		if e.Status == "active" {
			active++
		} else {
			cleared++
		}
	}
	if truncated {
		active += sqlActive
		cleared += sqlCleared
	}
	return active, cleared
}
