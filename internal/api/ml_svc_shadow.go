package api

// ml_svc_shadow.go — v0.10.2089 (spec docs/plans/spec-ml-service-level.md
// dilim 3, operatör onayı 2026-11-13): servis düzeyi ML gölge kararlarının
// eski yollarla karşılaştırması.
//
//	GET /api/ml/svc-shadow-compare?days=N   (1..14, varsayılan 14)
//
// Spec "mevcut ML karşılaştırma raporuna" diyor; kodda böyle bir rapor YOK —
// tablo Anomalies › ML "Teknik detay" bölümünde (MLTab), bu uçtan.
// Eski kol: problems'ta `svc-slowdown:*` ve `anomaly:*:error_rate`
// (chstore.ListServiceLevelProblems, started_at sınırlı, LIMIT 1000).
// Yeni kol: ml_events svc_error_rate / svc_breadth satırları (iki
// ListMLEvents okuması, last_seen sınırlı, LIMIT 2000) ve kanıttaki
// `svcShadow` kararı (yalnız açacak / katılacak olanlar). Eşleme SAF
// (ml.CompareSvcShadow).
//
// Rol kapısı YOK — salt-okunur (viewer görür). serveCached 60 sn; anahtar
// tek girdiyi (days, kelepçeden sonra) taşır. api.go büyümez.

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func init() { registerRoutesExtra("ml-svc-shadow", (*Server).registerMLSvcShadowRoutes) }

func (s *Server) registerMLSvcShadowRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ml/svc-shadow-compare", s.getMLSvcShadowCompare)
}

// svcShadowDays — SAF: ?days= kelepçesi (1..14, bozuk/boş → 14).
func svcShadowDays(raw string) int {
	d, err := strconv.Atoi(raw)
	if err != nil || d < 1 || d > 14 {
		return 14
	}
	return d
}

// svcShadowKey — SAF: önbellek anahtarı (tek girdi days).
func svcShadowKey(days int) string { return fmt.Sprintf("ml:svc-shadow-compare:v1:days=%d", days) }

type svcShadowCompareResponse struct {
	Days      int                `json:"days"`
	Rows      []ml.SvcCompareRow `json:"rows"`
	Counts    map[string]int     `json:"counts"`
	Truncated bool               `json:"truncated"`
}

// svcShadowInputs — SAF: okuma satırları → karşılaştırma girdileri.
func svcShadowInputs(olds []chstore.Problem, news []chstore.MLEvent, now time.Time) ([]ml.SvcCompareOld, []ml.SvcCompareNew) {
	// Öncelik okuma hattıyla AYNI saf işlevden (computePriority); deploy
	// zenginleştirmesi önceliğe karışmaz (v0.9.612), ağır okuma gerekmez.
	cfg, nowNs := chstore.CurrentProblemPriority(), now.UnixNano()
	o := make([]ml.SvcCompareOld, 0, len(olds))
	for _, p := range olds {
		if !ml.SvcCompareOldFamily(p.RuleID) {
			continue
		}
		end := int64(0)
		if p.ResolvedAt != nil {
			end = *p.ResolvedAt
		}
		prio, _ := chstore.PreviewProblemPriority(p, nowNs, cfg)
		o = append(o, ml.SvcCompareOld{Service: p.Service, RuleID: p.RuleID, Priority: prio, Start: p.StartedAt, End: end})
	}
	n := make([]ml.SvcCompareNew, 0, len(news))
	for _, e := range news {
		d, ok := ml.SvcShadowOf(e.Evidence)
		if !ok || !d.WouldOpen {
			continue
		}
		end := e.LastSeen
		if e.Stage != chstore.MLStageClosed {
			end = 0
		}
		n = append(n, ml.SvcCompareNew{Service: e.Service, Detector: e.Detector, Priority: d.Priority, Start: e.StartedAt, End: end})
	}
	return o, n
}

func (s *Server) getMLSvcShadowCompare(w http.ResponseWriter, r *http.Request) {
	days := svcShadowDays(r.URL.Query().Get("days"))
	s.serveCached(w, r, svcShadowKey(days), 60*time.Second, func(ctx context.Context) (any, error) {
		now := time.Now()
		since := now.Add(-time.Duration(days) * 24 * time.Hour)
		olds, err := s.store.ListServiceLevelProblems(ctx, since)
		if err != nil {
			return nil, err
		}
		var news []chstore.MLEvent
		truncated := len(olds) >= chstore.ServiceLevelProblemsCap
		for _, det := range []string{"svc_error_rate", "svc_breadth"} {
			evs, err := s.store.ListMLEvents(ctx, chstore.MLEventsFilter{Since: since, Detector: det, Limit: chstore.MLUnifiedCap})
			if err != nil {
				return nil, err
			}
			truncated = truncated || len(evs) >= chstore.MLUnifiedCap
			news = append(news, evs...)
		}
		o, n := svcShadowInputs(olds, news, now)
		rows := ml.CompareSvcShadow(o, n, now)
		if rows == nil {
			rows = []ml.SvcCompareRow{}
		}
		counts := map[string]int{}
		for _, row := range rows {
			counts[row.Diff]++
		}
		return svcShadowCompareResponse{Days: days, Rows: rows, Counts: counts, Truncated: truncated}, nil
	})
}
