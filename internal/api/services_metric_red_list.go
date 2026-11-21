package api

// services_metric_red_list.go — v0.10.2434 (operatör onaylı "RED metrik-önce",
// kısa vade): Services listesinin RED sütunları ÖRNEKLENMEMİŞ metrikten.
//
// NEDEN: prod trace'leri gateway'de örnekleniyor (hatalar hep + %5 taban);
// service_summary_5m'den okunan liste istek hızını ~1/20, hata oranını ~19×
// gösteriyor. VictoriaMetrics örneklemeden ÖNCE sayılmış RED taşıyor (uygulama
// HTTP/gRPC sunucu histogramları + gateway spanmetrics connector'ı).
//
// GET /api/services-red-metric?services=a,b,…&from&to[&env][&cluster]
// Sayfanın GÖRÜNEN servisleri (≤ servicesRedMetricMax) için servis başına:
// istek sayısı + req/s, sunucu-hatası oranı (HTTP 5xx / gRPC sunucu kodları /
// spanmetrics STATUS_CODE_ERROR; 4xx hata DEĞİL), ortalama ve p99 (ms), ve
// SEÇİLEN kaynak (aile kimliği). Metrikte olmayan servis cevapta YOK — liste
// onu span değeriyle "örneklenmiş span verisi" rozetiyle gösterir.
//
// KAYNAK ÖNCELİĞİ (resolveServiceREDFamily ile aynı): aile kataloğu sırasıyla
// tur tur — http (yeni semconv) → grpc_legacy → grpc_stable → http_legacy →
// spanmetrics; bir servis İLK veri veren ailede kalır, sonraki turlar yalnız
// ÇÖZÜLMEMİŞ servisleri sorar (aileler asla toplanmaz). Tek fark: eski semconv
// HTTP + gRPC birlikte yazan serviste gRPC kazanır (anomaly kataloğu sırası).
//
// SINIRLAR: tur başına 3 ANLIK sorgu (sayı×durum, p99, ortalama) paralel;
// en iyi 3, en kötü 5 tur = 15 sorgu; servis kümesi `svc=~"a|b"` (filo geneli
// sorgu yok), seri sayısı ≤ servis × durum değeri; pencere [60 sn, 7 g];
// sorgu başına 4 sn, toplam 10 sn; sonuç 30 sn önbellekte, anahtar TÜM
// girdiler (sıralı servis kümesinin FNV'si, env, cluster, pencere kovası,
// LabelMap). VM olmayan kaynakta boş cevap + not.

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/infraroutes"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func init() { registerRoutesExtra("services-red-metric", (*Server).registerServicesREDMetricRoutes) }

func (s *Server) registerServicesREDMetricRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services-red-metric", s.getServicesREDMetric)
}

const (
	servicesRedMetricMax       = 100
	servicesRedMetricQueryCap  = 4 * time.Second
	servicesRedMetricBudget    = 10 * time.Second
	servicesRedMetricMinWindow = 60 * time.Second
	servicesRedMetricMaxWindow = 7 * 24 * time.Hour
)

// serviceREDMetricRow — bir servisin örneklenmemiş RED'i.
type serviceREDMetricRow struct {
	Source    string   `json:"source"` // aile kimliği (http | http_legacy | grpc_legacy | grpc_stable | spanmetrics)
	Requests  float64  `json:"requests"`
	RatePerS  float64  `json:"ratePerSec"`
	ErrorRate *float64 `json:"errorRate,omitempty"` // YÜZDE; durum etiketi yoksa nil (0 sanılmasın)
	AvgMs     *float64 `json:"avgMs,omitempty"`
	P99Ms     *float64 `json:"p99Ms,omitempty"`
}

type servicesREDMetricResponse struct {
	Source   string                         `json:"source"` // metrik deposu (vm) ya da "" (VM yok)
	WindowS  int                            `json:"windowSec"`
	Services map[string]serviceREDMetricRow `json:"services"`
	Queries  int                            `json:"queries"`
	Notes    []string                       `json:"notes,omitempty"`
}

// servicesREDMetricKey — SAF: önbellek anahtarı; TÜM girdiler (v0.5.187).
func servicesREDMetricKey(names []string, env, cluster string, from, to time.Time, lm vmetrics.LabelMap, showInfra bool) string {
	parts := append([]string{env, cluster, cacheBucket(from, to), lm.Service, lm.Env, lm.Cluster, fmt.Sprint(showInfra)}, names...)
	return "svc-red-metric:v1:" + fnvStr(parts...)
}

// servicesREDMetricNames — SAF: ?services= → tekil, sıralı, boşsuz, tavanlı.
func servicesREDMetricNames(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range strings.Split(raw, ",") {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	if len(out) > servicesRedMetricMax {
		out = out[:servicesRedMetricMax]
	}
	return out
}

// servicesREDMetricQueries — SAF: bir ailenin tur sorguları (sayı×durum, p99,
// ortalama). Seçici metricChartSelector'dan (ad yazımı, servis kümesi, ortam,
// ailenin sabit eşleyicisi — spanmetrics giriş span'leri).
func servicesREDMetricQueries(f redFamily, svcLabel string, services []string, envMatcher string, winS int, showInfra bool) (count, p99, avg string) {
	// Tesisat rotaları (health / scrape / ping) HTTP ailelerinde varsayılan dışlanır.
	infra := ""
	if !showInfra && f.Kind == redKindHTTP && f.RouteLabel != "" {
		infra = f.RouteLabel + "!~" + vmetrics.PromStrTS(infraroutes.RouteRegex())
	}
	sel := func(kind string) string {
		v := metricChartVariant{Probe: f.Probe, Names: []string{f.names()}, Kind: kind, Match: f.Match}
		return metricChartSelector(metricChartQueryInput{Variant: v, SvcLabel: svcLabel, SvcValues: services, EnvMatcher: envMatcher}, infra)
	}
	w := fmt.Sprintf("[%ds]", winS)
	by := append([]string{svcLabel}, f.StatusLabels...)
	count = "sum by (" + strings.Join(by, ", ") + ") (increase(" + sel("count") + w + "))"
	scale := "1000"
	if f.Unit == "ms" {
		scale = "1"
	}
	p99 = "histogram_quantile(0.99, sum by (" + svcLabel + ", le) (increase(" + sel("bucket") + w + "))) * " + scale
	avg = "sum by (" + svcLabel + ") (increase(" + sel("sum") + w + ")) / sum by (" + svcLabel + ") (increase(" + sel("count") + w + ")) * " + scale
	return count, p99, avg
}

// servicesREDMetricFold — SAF: bir turun sonuçları → servis satırları (yalnız
// istek > 0 olanlar). Hata oranı yalnız durum etiketi taşıyan seri varsa.
func servicesREDMetricFold(f redFamily, svcLabel string, winS int, count, p99, avg []chstore.SpanMetricSeries) map[string]serviceREDMetricRow {
	type acc struct {
		total, errs float64
		status      bool
	}
	per := map[string]*acc{}
	for _, sr := range count {
		svc, ok := briefingGroupKey(sr.GroupKey, svcLabel)
		v := lastFinite(sr)
		if !ok || v == nil {
			continue
		}
		a := per[svc]
		if a == nil {
			a = &acc{}
			per[svc] = a
		}
		a.total += *v
		for _, l := range f.StatusLabels {
			if code, ok := briefingGroupKey(sr.GroupKey, l); ok && code != "" {
				a.status = true
				if f.IsServerError(code) {
					a.errs += *v
				}
				break
			}
		}
	}
	byLabel := func(ser []chstore.SpanMetricSeries) map[string]float64 {
		out := map[string]float64{}
		for _, sr := range ser {
			if svc, ok := briefingGroupKey(sr.GroupKey, svcLabel); ok {
				if v := lastFinite(sr); v != nil {
					out[svc] = *v
				}
			}
		}
		return out
	}
	p99s, avgs := byLabel(p99), byLabel(avg)
	out := map[string]serviceREDMetricRow{}
	for svc, a := range per {
		if a.total <= 0 {
			continue
		}
		row := serviceREDMetricRow{Source: f.ID, Requests: a.total, RatePerS: a.total / float64(winS)}
		if a.status {
			pct := math.Min(100, 100*a.errs/a.total)
			row.ErrorRate = &pct
		}
		if v, ok := p99s[svc]; ok {
			v := v
			row.P99Ms = &v
		}
		if v, ok := avgs[svc]; ok {
			v := v
			row.AvgMs = &v
		}
		out[svc] = row
	}
	return out
}

// lastFinite — anlık sonucun son sonlu değeri (yoksa nil).
func lastFinite(sr chstore.SpanMetricSeries) *float64 {
	for i := len(sr.Points) - 1; i >= 0; i-- {
		v := sr.Points[i].Value
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return &v
		}
	}
	return nil
}

// servicesREDMetricReader — VM okuyucusunun bu ucun kullandığı yüzü.
type servicesREDMetricReader interface {
	httpEnvLabelReader
	QueryPromQLInstant(ctx context.Context, query string, at time.Time) ([]chstore.SpanMetricSeries, error)
}

// buildServicesREDMetric — tur tur öncelikli okuma (dosya başı).
func (s *Server) buildServicesREDMetric(ctx context.Context, rd servicesREDMetricReader, names []string, env, cluster string, from, to time.Time, showInfra bool) servicesREDMetricResponse {
	win := to.Sub(from)
	if win < servicesRedMetricMinWindow {
		win = servicesRedMetricMinWindow
	}
	if win > servicesRedMetricMaxWindow {
		win = servicesRedMetricMaxWindow
	}
	winS := int(win / time.Second)
	out := servicesREDMetricResponse{Source: metricSourceVM, WindowS: winS, Services: map[string]serviceREDMetricRow{}}
	lm := rd.MetricLabelMap().Normalized()
	svcLabel := redFamilySvcLabel(lm)
	terms := httpEnvScopeTerms(env, cluster)
	ctx, cancel := context.WithTimeout(ctx, servicesRedMetricBudget)
	defer cancel()
	pending := names
	for _, f := range redFamilies {
		if len(pending) == 0 {
			break
		}
		envMatcher := ""
		if len(terms) > 0 {
			sc, err := s.resolveMetricEnvScope(ctx, rd, f.Probe, terms, to)
			switch {
			case err != nil:
				out.Notes = append(out.Notes, f.ID+": ortam etiketi okunamadı ("+string(sourcestate.Classify(err))+") — atlandı")
				continue
			case !sc.Applied || sc.Matcher == "":
				continue // bu ailede istenen ortam yok: o ortamda bu aileden servis yok
			}
			envMatcher = sc.Matcher
		}
		cq, pq, aq := servicesREDMetricQueries(f, svcLabel, pending, envMatcher, winS, showInfra)
		qs := []string{cq, pq, aq}
		res := make([][]chstore.SpanMetricSeries, len(qs))
		errs := make([]error, len(qs))
		var wg sync.WaitGroup
		for i, q := range qs {
			wg.Add(1)
			go func(i int, q string) {
				defer wg.Done()
				qctx, qcancel := context.WithTimeout(ctx, servicesRedMetricQueryCap)
				defer qcancel()
				res[i], errs[i] = rd.QueryPromQLInstant(qctx, q, to)
			}(i, q)
		}
		wg.Wait()
		out.Queries += len(qs)
		if errs[0] != nil {
			out.Notes = append(out.Notes, f.ID+": istek sayısı okunamadı ("+string(sourcestate.Classify(errs[0]))+") — bu ailenin servisleri span'e düştü")
			continue
		}
		rows := servicesREDMetricFold(f, svcLabel, winS, res[0], res[1], res[2])
		if errs[1] != nil || errs[2] != nil {
			out.Notes = append(out.Notes, f.ID+": gecikme okunamadı — p99/ortalama eksik")
		}
		var next []string
		for _, n := range pending {
			if r, ok := rows[n]; ok {
				out.Services[n] = r
			} else {
				next = append(next, n)
			}
		}
		pending = next
	}
	return out
}

// getServicesREDMetric — GET /api/services-red-metric
func (s *Server) getServicesREDMetric(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	names := servicesREDMetricNames(q.Get("services"))
	env, cluster := strings.TrimSpace(q.Get("env")), strings.TrimSpace(q.Get("cluster"))
	if len(env) > httpEnvScopeTermMax || len(cluster) > httpEnvScopeTermMax {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("env/cluster en çok %d karakter", httpEnvScopeTermMax))
		return
	}
	from, to := parseFromTo(r, time.Hour)
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	vm, ok := src.(vmMetricSource)
	if !ok || len(names) == 0 {
		resp := servicesREDMetricResponse{Services: map[string]serviceREDMetricRow{}}
		if !ok {
			resp.Notes = []string{"metrik kaynağı VictoriaMetrics değil — liste span verisinden (örneklenmiş)"}
		}
		writeJSON(w, resp)
		return
	}
	showInfra := q.Get("infra") == "1" // tesisat rotaları dahil (varsayılan dışlanır)
	key := servicesREDMetricKey(names, env, cluster, from, to, vm.MetricLabelMap().Normalized(), showInfra)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		return s.buildServicesREDMetric(ctx, vm, names, env, cluster, from, to, showInfra), nil
	})
}
