package chstore

// span_peers.go — span detay panelinin "akranlar" dağılımı (v0.10.2411'in
// ham /api/spans/heatmap okumasının MV-öncelikli hali, 2026-11-21).
//
// Panelin tek ihtiyacı (servis, operasyon) için ±15 dk'lık 1-B süre
// dağılımı ve span'in o dağılımdaki sırası. spanmetrics_1m aynı
// popülasyonu (tüm kind'lar, filtre yalnız service_name + name) 1 dk
// kovada quantilesTDigest state'iyle taşır; TDigest state'i seviyeden
// bağımsızdır, okuma istediği seviyelerle merge eder. Seviyeler arası
// log-doğrusal CDF heatmap'in 28 log-ms kovasına dağıtılır — şekil
// TDigest hatası içinde aynı, sayılar yaklaşık (Source "mv").
//
// MV satırı yoksa (yeni kurulum, MV kapalı, hata) ham heatmap yedek yol
// (Source "raw"; LIMIT + max_execution_time + zaman sınırlı WHERE).

import (
	"context"
	"math"
	"strconv"
	"time"
)

// SpanPeerDist — LatencyHeatmap şekli (FE foldPeers aynen katlar) + kaynak.
type SpanPeerDist struct {
	LatencyHeatmap
	Source string `json:"source"` // "mv" | "raw"
}

// peerQuantileLevels — 0 ve 1 TDigest'in min/max'ı; uçlarda sık ki kuyruk
// (span'in kendisi çoğu zaman oradadır) kaba kovaya ezilmesin.
var peerQuantileLevels = []float64{0, 0.01, 0.02, 0.05, 0.1, 0.15, 0.2, 0.25, 0.3, 0.35, 0.4, 0.45,
	0.5, 0.55, 0.6, 0.65, 0.7, 0.75, 0.8, 0.85, 0.9, 0.95, 0.98, 0.99, 0.995, 0.999, 1}

// Heatmap ızgarasıyla aynı sabitler (latencyHeatmapWhere).
const (
	peerMinLogMs = -1
	peerSubBins  = 4
	peerBins     = 28
)

func spanPeersMVSQL() string {
	lv := ""
	for i, l := range peerQuantileLevels {
		if i > 0 {
			lv += ", "
		}
		lv += strconv.FormatFloat(l, 'f', -1, 64)
	}
	return `
		SELECT countMerge(calls_state),
		       arrayMap(x -> toFloat64(x), quantilesTDigestMerge(` + lv + `)(duration_q_state))
		FROM spanmetrics_1m
		WHERE service_name = ? AND name = ? AND time_bucket >= ? AND time_bucket < ?
		LIMIT 1
		SETTINGS max_execution_time = 5`
}

// SpanPeerDistribution — MV-öncelikli akran dağılımı.
func (s *Store) SpanPeerDistribution(ctx context.Context, service, operation string, from, to time.Time) (*SpanPeerDist, error) {
	var total uint64
	var qs []float64
	err := s.telemetryReadConn().QueryRow(ctx, spanPeersMVSQL(), service, operation,
		chDateTimeArg(from.Truncate(time.Minute)), chDateTimeArg(to)).Scan(&total, &qs)
	if err == nil && total > 0 && len(qs) == len(peerQuantileLevels) {
		bins := heatmapDurationBins(peerMinLogMs, peerSubBins, peerBins)
		counts := peerBinsFromQuantiles(peerQuantileLevels, qs, total, bins)
		var mx uint32
		for _, c := range counts {
			if c > mx {
				mx = c
			}
		}
		return &SpanPeerDist{LatencyHeatmap: LatencyHeatmap{
			Times: []int64{from.UnixNano()}, DurationBins: bins,
			Counts: [][]uint32{counts}, MaxCount: mx, SamplingRate: 1,
		}, Source: "mv"}, nil
	}
	h, herr := s.GetLatencyHeatmap(ctx, []FilterExpr{
		{Key: "service.name", Op: "=", Values: []string{service}},
		{Key: "name", Op: "=", Values: []string{operation}},
	}, from, to, 30)
	if herr != nil {
		return nil, herr
	}
	return &SpanPeerDist{LatencyHeatmap: *h, Source: "raw"}, nil
}

// peerBinsFromQuantiles — SAF. (levels[k], valsNs[k]) noktaları log10(ms)
// ekseninde doğrusal bir CDF kurar; kova j'ye total × (F(üst_j) − F(üst_{j−1}))
// düşer. İlk kova alttan, son kova üstten açık (SQL'deki clamp ile aynı).
func peerBinsFromQuantiles(levels, valsNs []float64, total uint64, upperMs []float64) []uint32 {
	n := len(levels)
	if n == 0 || len(valsNs) != n || len(upperMs) == 0 {
		return make([]uint32, len(upperMs))
	}
	lx := make([]float64, n)
	prev := math.Inf(-1)
	for k, v := range valsNs {
		x := math.Log10(math.Max(v, 0)/1e6 + 0.0001)
		if x < prev { // TDigest kuantilleri monoton olmalı; değilse düzelt
			x = prev
		}
		lx[k], prev = x, x
	}
	cdf := func(ms float64) float64 {
		x := math.Log10(ms + 0.0001)
		k := -1
		for i := range lx {
			if lx[i] <= x {
				k = i
			}
		}
		switch {
		case k < 0:
			return 0
		case k == n-1:
			return 1
		}
		return levels[k] + (levels[k+1]-levels[k])*(x-lx[k])/(lx[k+1]-lx[k])
	}
	out := make([]uint32, len(upperMs))
	lo := 0.0
	for j, ub := range upperMs {
		hi := 1.0
		if j < len(upperMs)-1 {
			hi = cdf(ub)
		}
		if d := hi - lo; d > 0 {
			out[j] = uint32(math.Round(d * float64(total)))
		}
		lo = math.Max(lo, hi)
	}
	return out
}
