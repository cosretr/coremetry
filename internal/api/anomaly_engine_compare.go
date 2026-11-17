package api

// anomaly_engine_compare.go — v0.10.2309: Davis özneleri ↔ ML gölge
// karşılaştırma raporu (operatör: iki motor bir hafta yan yana koşsun,
// salt-okunur rapor).
//
//	GET /api/anomalies/engine-compare?days=N   (1..14, varsayılan 7)
//
// Kardeş ucun (ml_svc_shadow.go) şekli: kendi dosyası, registerRoutesExtra,
// serveCached (5 dk), anahtar tek girdiyi (days, kelepçeden sonra) taşır.
// Okumalar yalnız ClickHouse, şema değişikliği yok: özne olayları
// (ListAnomalyEvents{Kind: subject_red}, tavan 5000), ML olayları
// (ListEngineCompareMLEvents, tavan 10000), referans Problem'ler
// (ListEngineCompareRefProblems, tavan 2000); her biri zaman sınırlı +
// LIMIT + max_execution_time. Eşleme SAF (ml.CompareEngines).
//
// Rol kapısı ML okuma uçlarıyla aynı (mlReadRoles; viewer görür). api.go
// büyümez. Oylar / kararlar etiket olarak KULLANILMAZ (operatör kuralı).

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func init() {
	registerRoutesExtra("anomaly-engine-compare", (*Server).registerAnomalyEngineCompareRoutes)
}

func (s *Server) registerAnomalyEngineCompareRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/anomalies/engine-compare", auth.RequireAnyRole(mlReadRoles, s.getAnomalyEngineCompare))
}

const (
	engineCompareTTL        = 5 * time.Minute
	engineCompareSubjectCap = 5000
)

// engineCompareDays — SAF: ?days= kelepçesi (1..14, bozuk/boş/aralık dışı → 7).
func engineCompareDays(raw string) int {
	d, err := strconv.Atoi(raw)
	if err != nil || d < 1 || d > 14 {
		return 7
	}
	return d
}

// engineCompareKey — SAF: önbellek anahtarı (tek girdi days).
func engineCompareKey(days int) string {
	return fmt.Sprintf("anomalies:engine-compare:v1:days=%d", days)
}

type engineCompareSubjectsCfg struct {
	Endpoint      bool   `json:"endpoint"`
	Queue         bool   `json:"queue"`
	PromotionMode string `json:"promotionMode"`
}

type engineCompareMLCfg struct {
	Enabled       bool     `json:"enabled"`
	GroupProblems bool     `json:"groupProblems"`
	DetectorsOff  []string `json:"detectorsOff"`
}

type engineCompareConfig struct {
	Subjects engineCompareSubjectsCfg `json:"subjects"`
	ML       engineCompareMLCfg       `json:"ml"`
}

type engineCompareTruncated struct {
	Subject  bool `json:"subject"`
	ML       bool `json:"ml"`
	Problems bool `json:"problems"`
	Rows     bool `json:"rows"`
}

type engineCompareResponse struct {
	Days         int                    `json:"days"`
	FromNs       int64                  `json:"fromNs"`
	ToNs         int64                  `json:"toNs"`
	ToleranceSec int64                  `json:"toleranceSec"`
	ShortSec     int64                  `json:"shortSec"`
	Config       engineCompareConfig    `json:"config"`
	RefProblems  int                    `json:"refProblems"`
	Totals       ml.EngineCompareTotals `json:"totals"`
	Rows         []ml.EngineCompareRow  `json:"rows"`
	Caveats      []string               `json:"caveats"`
	Truncated    engineCompareTruncated `json:"truncated"`
}

// engineCompareConfigOf — SAF: ayarlar → cevabın config bölümü.
func engineCompareConfigOf(sub chstore.AnomalySubjectsConfig, mc chstore.MLConfig) engineCompareConfig {
	off := []string{}
	for _, d := range chstore.EngineCompareMLDetectors {
		if !mc.DetectorOn(d) {
			off = append(off, d)
		}
	}
	return engineCompareConfig{
		Subjects: engineCompareSubjectsCfg{Endpoint: sub.Endpoint.Enabled, Queue: sub.Queue.Enabled, PromotionMode: sub.PromotionMode},
		ML:       engineCompareMLCfg{Enabled: mc.Enabled, GroupProblems: mc.GroupProblemsOn(), DetectorsOff: off},
	}
}

// engineCompareCaveats — SAF: okuyucuya sınırlar (UI kodu çevirir).
func engineCompareCaveats(cfg engineCompareConfig, refProblems int) []string {
	out := []string{"subject_last_episode_only"}
	if cfg.ML.GroupProblems && cfg.Subjects.PromotionMode != chstore.SubjectPromotionLive {
		out = append(out, "ml_group_problems_live")
	}
	if !cfg.ML.Enabled {
		out = append(out, "ml_off")
	} else {
		for _, d := range cfg.ML.DetectorsOff {
			out = append(out, "ml_detector_off:"+d)
		}
	}
	if !cfg.Subjects.Endpoint {
		out = append(out, "subject_kind_off:endpoint")
	}
	if !cfg.Subjects.Queue {
		out = append(out, "subject_kind_off:queue")
	}
	if cfg.Subjects.PromotionMode == chstore.SubjectPromotionOff {
		out = append(out, "subject_promotion_off")
	}
	if refProblems == 0 {
		out = append(out, "no_reference_problems")
	}
	return out
}

// engineCompareBuild — SAF: okuma satırları → cevap.
func engineCompareBuild(days int, from, to time.Time, cfg engineCompareConfig,
	subjects []chstore.AnomalyEvent, mlRows []chstore.EngineCompareMLRow, probs []chstore.EngineCompareProblemRow) engineCompareResponse {
	si := make([]ml.EngineSubjectIn, 0, len(subjects))
	for _, ev := range subjects {
		if chstore.IsSubjectAnomaly(ev) {
			si = append(si, ml.EngineSubjectInOf(ev))
		}
	}
	mi := make([]ml.EngineMLIn, 0, len(mlRows))
	for _, e := range mlRows {
		mi = append(mi, ml.EngineMLInOf(e))
	}
	refs := make([]ml.EngineRefProblem, 0, len(probs))
	for _, p := range probs {
		if ml.EngineRefRule(p.RuleID) {
			refs = append(refs, ml.EngineRefProblem{Service: p.Service, Start: p.StartedAt, End: p.ResolvedAt})
		}
	}
	res := ml.CompareEngines(si, mi, refs, days, from.UnixNano(), to.UnixNano())
	return engineCompareResponse{
		Days: days, FromNs: from.UnixNano(), ToNs: to.UnixNano(),
		ToleranceSec: int64(ml.EngineCompareTolerance / time.Second), ShortSec: ml.EngineCompareShortSec,
		Config: cfg, RefProblems: len(refs), Totals: res.Totals, Rows: res.Rows,
		Caveats: engineCompareCaveats(cfg, len(refs)),
		Truncated: engineCompareTruncated{
			Subject:  len(subjects) >= engineCompareSubjectCap,
			ML:       len(mlRows) >= chstore.EngineCompareMLCap,
			Problems: len(probs) >= chstore.EngineCompareProblemsCap,
			Rows:     res.RowsTruncated,
		},
	}
}

func (s *Server) getAnomalyEngineCompare(w http.ResponseWriter, r *http.Request) {
	days := engineCompareDays(r.URL.Query().Get("days"))
	s.serveCached(w, r, engineCompareKey(days), engineCompareTTL, func(ctx context.Context) (any, error) {
		to := time.Now()
		from := to.Add(-time.Duration(days) * 24 * time.Hour)
		subjects, err := s.store.ListAnomalyEvents(ctx, chstore.ListAnomalyEventsFilter{
			Kind: chstore.SubjectAnomalyKind, SinceNs: from.UnixNano(), Limit: engineCompareSubjectCap, MaxExecSec: 10,
		})
		if err != nil {
			return nil, err
		}
		mlRows, err := s.store.ListEngineCompareMLEvents(ctx, from)
		if err != nil {
			return nil, err
		}
		// Referans Problem'in başlangıcı pencereden tolerans kadar önce olabilir.
		probs, err := s.store.ListEngineCompareRefProblems(ctx, from.Add(-ml.EngineCompareTolerance))
		if err != nil {
			return nil, err
		}
		cfg := engineCompareConfigOf(s.store.AnomalySensitivity().Subjects, s.store.GetMLConfig(ctx))
		return engineCompareBuild(days, from, to, cfg, subjects, mlRows, probs), nil
	})
}
