package api

// ml_impact_replay.go — v0.10.2392 (etki olayı programı; operatör: "yeni etki
// kuralları kaç Problem açar, ölçemiyorum"): ETKİ KURALI GERİ OYNATMA ucu.
//
//	GET /api/ml/impact-replay?days=N&detector=D
//	    days 1..14 (bozuk → 7); detector failure_rate | response_time |
//	    consumer_lag (bozuk → failure_rate)
//
// Yalnız ADMIN: operatör aracı ve soğuk hesabı ağır (≤ 200 servisin
// geriye bakışlı 5 dk serisi). İstek üzerine (ekranda düğme), yoklama yok.
// Hesap SAF çekirdekte (ml.ImpactReplay); okumalar yalnız ml_series, iki
// sorgu ailesi (chstore/impact_replay_read.go), her biri zaman sınırlı +
// LIMIT + max_execution_time. Bütçe 10 sn: aşılırsa kısmi cevap + not.
//
// serveCached 10 dk; anahtar TÜM girdileri hash'ler (v0.5.187): days,
// detector, ML ayarının ConfigHash'i (taban günü / örnek eşiği / kova) ve
// takvim + mevsimsel saat dilimi (taban günlerini değiştirir). Ayar
// değişince eski sonuç servis edilmez. Ayar / migration / LLM yok. api.go
// büyümez (registerRoutesExtra).

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func init() { registerRoutesExtra("ml-impact-replay", (*Server).registerMLImpactReplayRoutes) }

func (s *Server) registerMLImpactReplayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ml/impact-replay", auth.RequireRole(auth.RoleAdmin, s.getMLImpactReplay))
}

const impactReplayTTL = 10 * time.Minute

// impactReplayDays — SAF: ?days= kelepçesi (1..14, bozuk/boş/aralık dışı → 7).
func impactReplayDays(raw string) int {
	d, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || d < 1 || d > 14 {
		return 7
	}
	return d
}

// impactReplayDetector — SAF: ?detector= (bilinmeyen → failure_rate).
func impactReplayDetector(raw string) string {
	if d := strings.TrimSpace(raw); ml.ImpactReplayDetectorOK(d) {
		return d
	}
	return ml.ImpactReplayFailureRate
}

// impactReplayInputs — oynatmanın ayar girdileri (anahtar + hesap aynı değerden).
type impactReplayInputs struct {
	Cfg  chstore.MLConfig
	Sens chstore.AnomalySensitivityConfig
	Prio chstore.ProblemPriorityConfig
}

// impactReplayKey — SAF: önbellek anahtarı; girdilerin hepsi hash'te.
// Öncelik ayarı da girer (P1/P2 bölünmesini değiştirir).
func impactReplayKey(days int, detector string, in impactReplayInputs) string {
	h := fnv.New64a()
	cal, _ := json.Marshal(in.Sens.Calendar)
	prio, _ := json.Marshal(in.Prio)
	fmt.Fprintf(h, "days=%d\x00det=%s\x00cfg=%s\x00tz=%s\x00cal=%s\x00prio=%s",
		days, detector, in.Cfg.ConfigHash(), in.Sens.SeasonalTimezone, cal, prio)
	return fmt.Sprintf("ml:impact-replay:v1:%x", h.Sum64())
}

// impactReplaySource — test dikişi: okuyucu + ayar girdileri. Üretimde
// *chstore.Store; handler testi sahte okuyucu verir.
var impactReplaySource = func(s *Server, ctx context.Context) (ml.ImpactReplayReader, impactReplayInputs) {
	return s.store, impactReplayInputs{
		Cfg:  s.store.GetMLConfig(ctx),
		Sens: s.store.AnomalySensitivityForDetectors(),
		Prio: chstore.CurrentProblemPriority(),
	}
}

func (s *Server) getMLImpactReplay(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days, det := impactReplayDays(q.Get("days")), impactReplayDetector(q.Get("detector"))
	rd, in := impactReplaySource(s, r.Context())
	s.serveCached(w, r, impactReplayKey(days, det, in), impactReplayTTL, func(ctx context.Context) (any, error) {
		// Kapalı gelen ctx kullanılır (SWR tazelemesi isteğe bağlanmaz, v0.8.319).
		// Okumalar bütçe + pay ile kesilir; bütçe kontrolü çekirdekte.
		cctx, cancel := context.WithTimeout(ctx, ml.ImpactReplayBudget+5*time.Second)
		defer cancel()
		return ml.ImpactReplay(cctx, rd, ml.ImpactReplayRequest{
			Detector: det, Days: days, Now: time.Now(), Cfg: in.Cfg, Sens: in.Sens, Prio: in.Prio, Budget: ml.ImpactReplayBudget,
		})
	})
}
