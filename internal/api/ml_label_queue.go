package api

// ml_label_queue.go — v0.10.2484 (precision programı B.3/2; api.go
// BÜYÜMEZ, registerRoutesExtra):
//
//   GET /api/ml/label-queue — her rol (etiket düğmesi editor+; viewer listeyi görür)
//
// Sinyaller sekmesinin "Bu hafta etiketle (10)" çipi: son 7 günün etiketsiz
// ML olaylarından deterministik, tabakalı örnek (chstore.MLLabelQueueSample).
// Bu hafta kuyruktan verilen oy kadar liste kısalır (haftada 10). Etiket olay
// sayfasındaki mevcut düğmeyle verilir; kuyruktan açılan sayfa oyu
// origin='queue' ile yazar. SALT RAPOR — hiçbir çalışma zamanı kararı yok.

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("ml-label-queue", (*Server).registerMLLabelQueueRoutes) }

func (s *Server) registerMLLabelQueueRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ml/label-queue", auth.RequireAnyRole(mlReadRoles, s.getMLLabelQueue))
}

// mlScorecardRowsKey — karne satırları (tek CH toplaması) ortak önbellek
// anahtarı: /api/ml/scorecard ve kuyruk aynı hesabı paylaşır. Girdi yalnız
// sabit pencere (30 g); oy PUT'u "ml:" önekini düşürür.
const mlScorecardRowsKey = "ml:scorecard-rows:v1:30d"

// mlScorecardRowsTTL — tek CH toplamasının önbelleği (precision programı B.1:
// 60 sn → 5 dk). Zarf (/api/ml/scorecard: tik, baseline) 60 sn kalır.
const mlScorecardRowsTTL = 5 * time.Minute

// mlScorecardRows — karne satırları (önbellekli; pencere 30 g).
func (s *Server) mlScorecardRows(ctx context.Context) ([]chstore.MLDetectorScore, error) {
	body, _, err := s.cachedJSON(ctx, mlScorecardRowsKey, mlScorecardRowsTTL, false, func(ctx context.Context) (any, error) {
		rows, err := s.store.MLScorecard(ctx, time.Now().Add(-mlScoreWindow))
		if rows == nil {
			rows = []chstore.MLDetectorScore{}
		}
		return rows, err
	})
	if err != nil {
		return nil, err
	}
	var rows []chstore.MLDetectorScore
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []chstore.MLDetectorScore{}
	}
	return rows, nil
}

// mlLabelQueueResponse — kuyruk yanıtı.
type mlLabelQueueResponse struct {
	Week      string                     `json:"week"`      // ISO hafta (örnekleyici tohumu)
	Size      int                        `json:"size"`      // haftalık hedef (10)
	DoneWeek  int                        `json:"doneWeek"`  // bu hafta kuyruktan verilen oy
	Items     []chstore.MLLabelQueueItem `json:"items"`     // kalan örnek (≤ size − doneWeek)
	Truncated bool                       `json:"truncated"` // aday okuması tavana değdi
}

// mlLabelQueueKey — önbellek anahtarı: hafta tek girdi (pencere sabit).
func mlLabelQueueKey(week string) string { return "ml:label-queue:v1:" + week }

func (s *Server) getMLLabelQueue(w http.ResponseWriter, r *http.Request) {
	week := chstore.MLISOWeek(time.Now())
	s.serveCached(w, r, mlLabelQueueKey(week), mlScorecardRowsTTL, func(ctx context.Context) (any, error) {
		now := time.Now() // SWR tazelemesi kendi anını okur
		cands, truncated, err := s.store.MLLabelQueueCandidates(ctx, now.Add(-chstore.MLLabelQueueWindow))
		if err != nil {
			return nil, err
		}
		done, err := s.store.MLQueueLabelsSince(ctx, chstore.MLISOWeekStart(now))
		if err != nil {
			return nil, err
		}
		n := map[string]int64{}
		if rows, err := s.mlScorecardRows(ctx); err == nil { // karne yoksa hepsi n < 20 sayılır (sıra yine deterministik)
			for _, r := range rows {
				n[r.Detector] = r.Samples
			}
		}
		left := chstore.MLLabelQueueSize - done
		return mlLabelQueueResponse{Week: week, Size: chstore.MLLabelQueueSize, DoneWeek: done,
			Items: chstore.MLLabelQueueSample(cands, week, left, n), Truncated: truncated}, nil
	})
}
