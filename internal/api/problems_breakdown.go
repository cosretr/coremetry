package api

// problems_breakdown.go — v0.10.2478 (operatör: "Çok fazla problem üretiyor,
// gerçek problemler olsun isterim"). Problems KAYNAK DÖKÜMÜ: hangi kural
// ailesi kaç Problem açıyor, ne kadar sürüyor, ne kadarı 15 dk'da kendiliğinden
// kapanıyor. Eşikleri körlemesine sıkmak yerine önce gürültünün kaynağı tek
// ekranda görünsün diye.
//
//   GET /api/problems/breakdown?since=24h|7d|30d   (varsayılan 7d)
//
// SALT OKUMA: dedektör, kapı, öncelik, bildirim — hiçbiri değişmez; uç
// yalnız `problems` tablosunu sayar. Rol kapısı YOK — viewer görür (ekran
// bir teşhis; yazma eylemi yok). serveCached 5 dk; anahtar tek girdinin
// (since basamağı) hepsini taşır — pencere "şimdi"ye göreli, TTL tazeliği
// sınırlar. Basamak dışı ?since= varsayılana düşer, cache'e sınırsız anahtar
// basılmaz (v0.8.270).
//
// Öncelik (P1/P2/P3) neden YOK: liste önceliği satır başına config + etki
// gerçekleriyle hesaplanıyor (computePriority + inbox görünüm kuralı);
// agregat düzeyde ucuz değil. Döküm KAYITLI severity'yi böler ve arayüz bunu
// etiketinde söyler.
//
// Neden api.go'da değil: TestApiGoDoesNotGrow (/api-route). Sorgu
// chstore/problem_source_breakdown.go'da, aile sözlüğü de orada (tek tablo,
// Go + SQL ikizi).

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("problems-breakdown", (*Server).registerProblemsBreakdownRoutes) }

func (s *Server) registerProblemsBreakdownRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/problems/breakdown", s.getProblemsBreakdown)
}

// problemsBreakdownWindows — izinli pencereler + seri kovası. 24 sa saatlik,
// 7/30 gün günlük kova (sparkline).
var problemsBreakdownWindows = map[string]struct {
	d      time.Duration
	bucket int64
}{
	"24h": {24 * time.Hour, 3600},
	"7d":  {7 * 24 * time.Hour, 86400},
	"30d": {30 * 24 * time.Hour, 86400},
}

const (
	problemsBreakdownDefault = "7d"
	problemsBreakdownTTL     = 5 * time.Minute
	problemsBreakdownTopSvc  = 5
	problemsBreakdownChurnN  = 3 // aynı servis + aile bir günde ≥ 3 açılış
)

// problemsBreakdownSince — SAF: ?since= → basamak (bilinmeyen = 7d).
func problemsBreakdownSince(raw string) string {
	raw = strings.TrimSpace(raw)
	if _, ok := problemsBreakdownWindows[raw]; ok {
		return raw
	}
	return problemsBreakdownDefault
}

// problemsBreakdownKey — SAF: cache anahtarı; tek girdi since basamağı.
func problemsBreakdownKey(since string) string {
	return "problems:breakdown:v1:since=" + since
}

// problemsBreakdownRead — depo dikişi (test sahte sonuç verir).
var problemsBreakdownRead = func(ctx context.Context, s *Server, from, to time.Time, bucketSec int64) (chstore.ProblemSourceAgg, error) {
	return s.store.ProblemSourceBreakdown(ctx, from, to, bucketSec)
}

type problemsBreakdownPoint struct {
	T int64 `json:"t"` // kova başı, unix sn
	N int64 `json:"n"`
}

type problemsBreakdownService struct {
	Service string `json:"service"`
	Count   int64  `json:"count"`
}

// problemsBreakdownStats — aile ve aile × metrik satırının ortak ölçüleri.
type problemsBreakdownStats struct {
	Count      int64   `json:"count"`
	SharePct   float64 `json:"sharePct"`
	Services   int64   `json:"services"`
	OpenNow    int64   `json:"openNow"`
	Critical   int64   `json:"critical"`
	Warning    int64   `json:"warning"`
	OtherSev   int64   `json:"otherSev"`
	MedianSec  float64 `json:"medianSec"`
	Short15    int64   `json:"short15"`
	Short15Pct float64 `json:"short15Pct"`
	Long1h     int64   `json:"long1h"`
}

type problemsBreakdownMetric struct {
	Metric string `json:"metric"`
	problemsBreakdownStats
}

type problemsBreakdownFamily struct {
	Family string `json:"family"`
	problemsBreakdownStats
	// ChurnPairs — bir günde ≥ 3 açılan (servis, gün) çifti; ChurnCount o
	// çiftlerdeki Problem sayısı.
	ChurnPairs  int64                      `json:"churnPairs"`
	ChurnCount  int64                      `json:"churnCount"`
	TopServices []problemsBreakdownService `json:"topServices"`
	Metrics     []problemsBreakdownMetric  `json:"metrics"`
}

type problemsBreakdownResponse struct {
	Since     string                    `json:"since"`
	From      int64                     `json:"from"` // unix sn
	To        int64                     `json:"to"`
	BucketSec int64                     `json:"bucketSec"`
	Total     int64                     `json:"total"`
	OpenNow   int64                     `json:"openNow"`
	Short15   int64                     `json:"short15"` // < 15 dk'da kapananlar
	Series    []problemsBreakdownPoint  `json:"series"`
	Families  []problemsBreakdownFamily `json:"families"`
	// SeverityBasis — P1/P2/P3 değil, kayıtlı severity bölünüyor.
	SeverityBasis string `json:"severityBasis"`
	Capped        bool   `json:"capped,omitempty"`
	Partial       bool   `json:"partial,omitempty"`
}

func breakdownPct(a, b int64) float64 {
	if b <= 0 {
		return 0
	}
	return float64(a) * 100 / float64(b)
}

func breakdownStats(r chstore.ProblemSourceAggRow, total int64) problemsBreakdownStats {
	n := int64(r.N)
	return problemsBreakdownStats{
		Count: n, SharePct: breakdownPct(n, total), Services: int64(r.Services), OpenNow: int64(r.OpenNow),
		Critical: int64(r.Critical), Warning: int64(r.Warning), OtherSev: n - int64(r.Critical) - int64(r.Warning),
		MedianSec: r.MedianSec, Short15: int64(r.Short15), Short15Pct: breakdownPct(int64(r.Short15), n),
		Long1h: int64(r.Long1h),
	}
}

// buildProblemsBreakdown — SAF: GROUPING SETS satırları → yanıt. Seri
// [from, to) kovalarına sıfır doldurulur; aileler sayıya göre azalan.
func buildProblemsBreakdown(agg chstore.ProblemSourceAgg, since string, from, to time.Time, bucketSec int64) problemsBreakdownResponse {
	out := problemsBreakdownResponse{
		Since: since, From: from.Unix(), To: to.Unix(), BucketSec: bucketSec,
		SeverityBasis: "severity", Capped: agg.Capped, Partial: agg.Partial,
		Series: []problemsBreakdownPoint{}, Families: []problemsBreakdownFamily{},
	}
	fams := map[string]*problemsBreakdownFamily{}
	var famRows, metricRows, svcRows []chstore.ProblemSourceAggRow
	series := map[int64]int64{}
	for _, r := range agg.Rows {
		switch r.Set() {
		case "series":
			series[r.Bucket] += int64(r.N)
		case "family":
			famRows = append(famRows, r)
			out.Total += int64(r.N)
			out.OpenNow += int64(r.OpenNow)
			out.Short15 += int64(r.Short15)
		case "metric":
			metricRows = append(metricRows, r)
		case "service":
			svcRows = append(svcRows, r)
		}
	}
	for _, r := range famRows {
		f := &problemsBreakdownFamily{Family: r.Family, problemsBreakdownStats: breakdownStats(r, out.Total),
			TopServices: []problemsBreakdownService{}, Metrics: []problemsBreakdownMetric{}}
		fams[r.Family] = f
	}
	for _, r := range metricRows {
		if f := fams[r.Family]; f != nil {
			f.Metrics = append(f.Metrics, problemsBreakdownMetric{Metric: r.Metric, problemsBreakdownStats: breakdownStats(r, f.Count)})
		}
	}
	for _, r := range svcRows {
		f := fams[r.Family]
		if f == nil {
			continue
		}
		if r.Service != "-" {
			f.TopServices = append(f.TopServices, problemsBreakdownService{Service: r.Service, Count: int64(r.N)})
		}
		for _, c := range r.DayCounts {
			if c >= problemsBreakdownChurnN {
				f.ChurnPairs++
				f.ChurnCount += int64(c)
			}
		}
	}
	for _, f := range fams {
		sort.Slice(f.Metrics, func(i, j int) bool {
			if f.Metrics[i].Count != f.Metrics[j].Count {
				return f.Metrics[i].Count > f.Metrics[j].Count
			}
			return f.Metrics[i].Metric < f.Metrics[j].Metric
		})
		sort.Slice(f.TopServices, func(i, j int) bool {
			if f.TopServices[i].Count != f.TopServices[j].Count {
				return f.TopServices[i].Count > f.TopServices[j].Count
			}
			return f.TopServices[i].Service < f.TopServices[j].Service
		})
		if len(f.TopServices) > problemsBreakdownTopSvc {
			f.TopServices = f.TopServices[:problemsBreakdownTopSvc]
		}
		out.Families = append(out.Families, *f)
	}
	sort.Slice(out.Families, func(i, j int) bool {
		if out.Families[i].Count != out.Families[j].Count {
			return out.Families[i].Count > out.Families[j].Count
		}
		return out.Families[i].Family < out.Families[j].Family
	})
	if bucketSec > 0 {
		for t := from.Unix() / bucketSec * bucketSec; t < to.Unix(); t += bucketSec {
			out.Series = append(out.Series, problemsBreakdownPoint{T: t, N: series[t]})
		}
	}
	return out
}

func (s *Server) getProblemsBreakdown(w http.ResponseWriter, r *http.Request) {
	since := problemsBreakdownSince(r.URL.Query().Get("since"))
	win := problemsBreakdownWindows[since]
	s.serveCached(w, r, problemsBreakdownKey(since), problemsBreakdownTTL, func(ctx context.Context) (any, error) {
		to := time.Now().UTC()
		from := to.Add(-win.d)
		agg, err := problemsBreakdownRead(ctx, s, from, to, win.bucket)
		if err != nil {
			return nil, err
		}
		return buildProblemsBreakdown(agg, since, from, to, win.bucket), nil
	})
}
