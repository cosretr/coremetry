package api

// trace_error_histogram.go — v0.10.1082 — GET /api/traces/error-histogram.
//
// Operator-reported (prod): "Error seçildiğinde histogram gelmiyor." /traces'te
// Errors + nitelik çipi (`function_code = …`, `k8s.pod.name = …`) → liste dolu,
// hacim şeridi 0. Şerit /api/spans/metric-batch'ten okuyordu ve Errors'u
// SPAN düzeyinde (`status = error` çipi) AND'liyordu; liste ise hatayı iki
// basamakta (çipe uyan hatalı span ↔ trace düzeyi) arıyor. Kök neden ve
// kip kararı: chstore/trace_error_histogram.go başlığı.
//
// Neden ayrı uç (metric-batch'e bayrak DEĞİL): şeridin bu sınıfta listeyle
// AYNI kümeyi sayması için girdi listenin KENDİ süzgeci olmalı. Uç /api/traces
// ile aynı sorgu dizesini alır ve aynı ayrıştırıcıdan (parseTraceFilter)
// geçirir — çip çevirisi, env/cluster/servis kapsamı, kök tanımı ayrışamaz.
// metric-batch'in gövdesi (filters + dsl + bağlam çipleri) bu eşlemeyi ancak
// elle yeniden kurarak taşıyabilirdi; tam o elle kurulum bu bug'ı üretti.
//
//	GET /api/traces/error-histogram?<listenin süzgeç parametreleri>&step=&stat=p50|p95|p99
//
// Rol kapısı YOK — /api/traces gibi salt-okunur. serveCached 30s (şeridin
// metric-batch TTL'i); anahtar listenin anahtar şekli (cacheRawQuery: tüm
// parametreler, from/to ızgaraya oturmuş) + kök tanımı (rootOnly iken).

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() {
	registerRoutesExtra("traces-error-histogram", (*Server).registerTraceErrorHistogramRoutes)
}

func (s *Server) registerTraceErrorHistogramRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/traces/error-histogram", s.getTraceErrorHistogram)
}

// traceErrorHistogramQuantile — SAF: şerit istatistiği beyaz listesi
// (frontend stripStat.ts). Bilinmeyen → medyan (şeridin varsayılanı).
func traceErrorHistogramQuantile(stat string) float64 {
	switch strings.ToLower(strings.TrimSpace(stat)) {
	case "p95":
		return 0.95
	case "p99":
		return 0.99
	default:
		return 0.5
	}
}

// traceErrorHistogramKey — SAF: listenin anahtar şekli + bu ucun öneki.
func traceErrorHistogramKey(rawQuery, rootSuffix string) string {
	return "traces-error-histogram:" + cacheRawQueryString(rawQuery) + rootSuffix
}

// traceErrorHistogramPayload — SAF: metric-batch zarfıyla aynı şekil
// ({series:{count,errors,rt}, stepSeconds}) + kip / tavan; frontend'in
// buildVolumeSeries'i iki uçtan gelen seriyi aynı yoldan çizer.
func traceErrorHistogramPayload(h chstore.TraceErrorHistogram) map[string]any {
	one := func(p []chstore.SpanMetricPoint) []chstore.SpanMetricSeries {
		if len(p) == 0 {
			return []chstore.SpanMetricSeries{}
		}
		return []chstore.SpanMetricSeries{{GroupKey: []string{}, Points: p}}
	}
	return map[string]any{
		"series": map[string]any{
			"count":  one(h.Count),
			"errors": one(h.Errors),
			"rt":     one(h.RT),
		},
		"stepSeconds": h.Step,
		"mode":        h.Mode,
		"capped":      h.Capped,
	}
}

func (s *Server) getTraceErrorHistogram(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, err := parseTraceFilter(q)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !chstore.TraceErrorHistogramEligible(f) {
		writeJSONError(w, http.StatusBadRequest,
			"error-histogram yalnız Errors + span-düzeyi çip içindir (arama / süre / services / traceId yok); diğer hâller /api/spans/metric-batch")
		return
	}
	step := parseInt(q.Get("step"), 0)
	quant := traceErrorHistogramQuantile(q.Get("stat"))
	key := traceErrorHistogramKey(r.URL.RawQuery, tracesRootDefKeySuffix(q, s.store.TraceRootDef()))
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		h, err := s.store.TraceErrorHistogram(ctx, f, step, quant)
		if err != nil {
			return nil, err
		}
		return traceErrorHistogramPayload(h), nil
	})
}
