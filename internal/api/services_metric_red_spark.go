package api

// services_metric_red_spark.go — v0.10.2437 (operatör onaylı): Services
// listesinin mini-grafikleri, satırın değerleriyle AYNI örneklenmemiş
// kaynaktan. v0.10.2434/2435 hücre DEĞERLERİNİ VM'den doldurdu
// (services_metric_red_list.go) ama mini-grafikler hâlâ örneklenmiş
// service_summary_5m'den geliyordu: "2.4k istek" yanında 1/20 ölçekli eğri.
//
// GET /api/services-red-metric-spark?services=a,b&sources=http,grpc_legacy
//     &from&to[&slots][&env][&cluster][&infra=1]
// services ve sources PARALEL listeler: sources[i] = services[i]'nin
// /api/services-red-metric cevabındaki seçilmiş ailesi (istemci zaten
// biliyor — kaynak çözümü TEKRARLANMAZ, tur/yoklama yok). Metrik kaynağı
// olmayan servis istenmez; liste ona span mini-grafiğini ("örneklenmiş"
// rozetiyle) çizer.
//
// SORGU BÜTÇESİ: ailede TEK aralık sorgusu — sayı×durum, p99 ve ortalama
// label_replace(… "cm_k" …) ile `or` altında birleşik; en çok 5 aile = 5
// sorgu (+ env/cluster süzgecinde aile başına ortam etiketi çözümü, liste
// ucuyla aynı). Servis kümesi `svc=~"a|b"` (filo geneli sorgu yok), ≤
// servicesRedMetricMax servis; seri ≤ servis × (durum değeri + 2); adım ≥ 60
// sn, nokta ≤ slots (≤ 200); sorgu başına 4 sn, toplam 10 sn; sonuç 30 sn
// önbellekte, anahtar TÜM girdiler (sıralı servis=aile çiftlerinin FNV'si,
// env, cluster, pencere kovası, slots, LabelMap, infra).

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func init() {
	registerRoutesExtra("services-red-metric-spark", (*Server).registerServicesREDSparkRoutes)
}

func (s *Server) registerServicesREDSparkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services-red-metric-spark", s.getServicesREDSpark)
}

const (
	servicesRedSparkDefaultSlots = 80
	servicesRedSparkMinSlots     = 10
	servicesRedSparkMaxSlots     = 200
	servicesRedSparkMinStep      = 60
	servicesRedSparkKindLabel    = "cm_k"
)

// serviceREDSparkBucket — span mini-grafiğiyle (SparklineBucket) aynı biçim:
// spans = adımdaki istek, errs = adımdaki sunucu hatası.
type serviceREDSparkBucket struct {
	T     int64   `json:"t"`
	Spans float64 `json:"spans"`
	Errs  float64 `json:"errs"`
	AvgMs float64 `json:"avgMs"`
	P99Ms float64 `json:"p99Ms"`
}

// serviceREDSparkRow — HasErrors yalnız durum etiketi taşıyan seri varsa
// (yoksa hata eğrisi span'e düşer; 0 sanılmasın); HasLatency p99 ya da
// ortalama noktası varsa.
type serviceREDSparkRow struct {
	Source     string                  `json:"source"`
	Buckets    []serviceREDSparkBucket `json:"buckets"`
	HasErrors  bool                    `json:"hasErrors"`
	HasLatency bool                    `json:"hasLatency"`
}

type servicesREDSparkResponse struct {
	Source   string                        `json:"source"`
	StepSec  int                           `json:"stepSec"`
	Services map[string]serviceREDSparkRow `json:"services"`
	Queries  int                           `json:"queries"`
	Notes    []string                      `json:"notes,omitempty"`
}

// servicesREDSparkPairs — SAF: paralel listeler → aile → sıralı servisler.
// Bilinmeyen aile, boş ad, tekrar atlanır; toplam ≤ servicesRedMetricMax.
func servicesREDSparkPairs(servicesRaw, sourcesRaw string) map[string][]string {
	svcs, srcs := strings.Split(servicesRaw, ","), strings.Split(sourcesRaw, ",")
	out := map[string][]string{}
	seen := map[string]bool{}
	n := 0
	for i, svc := range svcs {
		svc = strings.TrimSpace(svc)
		if svc == "" || seen[svc] || i >= len(srcs) || n >= servicesRedMetricMax {
			continue
		}
		fam := strings.TrimSpace(srcs[i])
		if _, ok := redFamilyByID(fam); !ok {
			continue
		}
		seen[svc] = true
		out[fam] = append(out[fam], svc)
		n++
	}
	for f := range out {
		sort.Strings(out[f])
	}
	return out
}

// servicesREDSparkKey — SAF: önbellek anahtarı; TÜM girdiler (v0.5.187).
func servicesREDSparkKey(pairs map[string][]string, env, cluster string, from, to time.Time, slots int, lm vmetrics.LabelMap, showInfra bool) string {
	var flat []string
	for f, ss := range pairs {
		for _, s := range ss {
			flat = append(flat, s+"\x00"+f)
		}
	}
	sort.Strings(flat)
	parts := append([]string{env, cluster, cacheBucket(from, to), strconv.Itoa(slots), lm.Service, lm.Env, lm.Cluster, fmt.Sprint(showInfra)}, flat...)
	return "svc-red-spark:v1:" + fnvStr(parts...)
}

// servicesREDSparkStep — SAF: adım (sn) = max(60, ⌈pencere/slots⌉).
func servicesREDSparkStep(win time.Duration, slots int) int {
	step := int(math.Ceil(win.Seconds() / float64(max(slots, 1))))
	return max(step, servicesRedSparkMinStep)
}

// servicesRedSparkMinRateWin — rate penceresinin tabanı (sn). Uygulama
// metrikleri tipik 60 sn'de bir gelir; pencere en az üç örnek aralığı olsun
// (Overview grafiklerinin rate[180s] kuralı).
const servicesRedSparkMinRateWin = 180

// servicesREDSparkRateWin — SAF: rate penceresi (sn) = max(adım, 180).
func servicesREDSparkRateWin(stepS int) int { return max(stepS, servicesRedSparkMinRateWin) }

// servicesREDSparkQuery — SAF: ailenin TEK birleşik aralık sorgusu.
//
// v0.10.2470 (operatör: "sparkline'lar doğru görünmüyor / bozuluyor"): eski
// şekil increase(...[adım]) idi. VictoriaMetrics'te increase, pencere içindeki
// son örnek ile pencereden önceki son örneğin FARKIDIR; adım örnekleme
// aralığının tam katı değilken (135 sn adım, 60 sn örnek) kovalara dönüşümlü
// 2 ve 3 aralıklık artış düşer → testere dişi; adım = örnek aralığıyken kova
// 0 ya da 2 aralık alır. rate() farkı iki örnek arasındaki GERÇEK süreye böler,
// örnek sayısından bağımsızdır. İstek sayısı = rate × adım ("adım başına
// istek" birimi korunur); pencere ≥ 180 sn (adımdan uzunsa hareketli ortalama).
func servicesREDSparkQuery(f redFamily, svcLabel string, services []string, envMatcher string, stepS int, showInfra bool) string {
	sel := servicesREDMetricSelectors(f, svcLabel, services, envMatcher, showInfra)
	w := fmt.Sprintf("[%ds]", servicesREDSparkRateWin(stepS))
	by := append([]string{svcLabel}, f.StatusLabels...)
	c := "sum by (" + strings.Join(by, ", ") + ") (rate(" + sel("count") + w + ")) * " + strconv.Itoa(stepS)
	scale := "1000"
	if f.Unit == "ms" {
		scale = "1"
	}
	p := "histogram_quantile(0.99, sum by (" + svcLabel + ", le) (rate(" + sel("bucket") + w + "))) * " + scale
	a := "sum by (" + svcLabel + ") (rate(" + sel("sum") + w + ")) / sum by (" + svcLabel + ") (rate(" + sel("count") + w + ")) * " + scale
	tag := func(q, k string) string {
		return `label_replace(` + q + `, "` + servicesRedSparkKindLabel + `", "` + k + `", "", ".*")`
	}
	return tag(c, "c") + " or " + tag(p, "p") + " or " + tag(a, "a")
}

// servicesREDSparkGrid — SAF: pencereyi adım ızgarasına hizalar (her yenilemede
// aynı kova zamanları; kayan başlangıç mini-grafiği titretiyordu).
func servicesREDSparkGrid(from, to time.Time, stepS int) (time.Time, time.Time) {
	st := int64(stepS)
	return time.Unix(from.Unix()/st*st, 0), time.Unix(to.Unix()/st*st, 0)
}

// servicesREDSparkFill — SAF: servisin eksik kovalarını sıfırla doldurur
// (trafiksiz adım = 0 istek). Eksik kova çizimde zaman eksenini sıkıştırıyordu.
// Kova zamanı ns; ızgara [from, to] adım adım.
func servicesREDSparkFill(row serviceREDSparkRow, from, to time.Time, stepS int) serviceREDSparkRow {
	if stepS <= 0 || !to.After(from) {
		return row
	}
	have := make(map[int64]serviceREDSparkBucket, len(row.Buckets))
	onGrid := false
	stepNs := int64(stepS) * int64(time.Second)
	for _, b := range row.Buckets {
		have[b.T] = b
		if d := b.T - from.UnixNano(); d >= 0 && d%stepNs == 0 {
			onGrid = true
		}
	}
	if !onGrid {
		return row // kaynak başka bir ızgara döndürdü: veriyi sıfırlarla ezme
	}
	n := int(to.Sub(from)/(time.Duration(stepS)*time.Second)) + 1
	out := make([]serviceREDSparkBucket, 0, n)
	for i := 0; i < n; i++ {
		t := from.Add(time.Duration(i*stepS) * time.Second).UnixNano()
		if b, ok := have[t]; ok {
			out = append(out, b)
		} else {
			out = append(out, serviceREDSparkBucket{T: t})
		}
	}
	row.Buckets = out
	return row
}

// servicesREDSparkFold — SAF: birleşik sorgu sonucu → servis satırları.
// Yalnız `want` içindeki servisler; istek noktası olmayan servis yok.
func servicesREDSparkFold(f redFamily, svcLabel string, want []string, rows []chstore.SpanMetricSeries) map[string]serviceREDSparkRow {
	wanted := map[string]bool{}
	for _, s := range want {
		wanted[s] = true
	}
	type acc struct {
		by        map[int64]*serviceREDSparkBucket
		errs, lat bool
		reqs      bool
	}
	per := map[string]*acc{}
	get := func(svc string, t int64) (*acc, *serviceREDSparkBucket) {
		a := per[svc]
		if a == nil {
			a = &acc{by: map[int64]*serviceREDSparkBucket{}}
			per[svc] = a
		}
		b := a.by[t]
		if b == nil {
			b = &serviceREDSparkBucket{T: t}
			a.by[t] = b
		}
		return a, b
	}
	for _, sr := range rows {
		svc, ok := briefingGroupKey(sr.GroupKey, svcLabel)
		kind, kok := briefingGroupKey(sr.GroupKey, servicesRedSparkKindLabel)
		if !ok || !kok || !wanted[svc] {
			continue
		}
		code, hasStatus := "", false
		if kind == "c" {
			for _, l := range f.StatusLabels {
				if v, ok := briefingGroupKey(sr.GroupKey, l); ok && v != "" {
					code, hasStatus = v, true
					break
				}
			}
		}
		for _, pt := range sr.Points {
			v := pt.Value
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			a, b := get(svc, pt.Time)
			switch kind {
			case "c":
				b.Spans += v
				if v > 0 {
					a.reqs = true
				}
				if hasStatus {
					a.errs = true
					if f.IsServerError(code) {
						b.Errs += v
					}
				}
			case "p":
				b.P99Ms, a.lat = v, true
			case "a":
				b.AvgMs, a.lat = v, true
			}
		}
	}
	out := map[string]serviceREDSparkRow{}
	for svc, a := range per {
		if !a.reqs {
			continue
		}
		row := serviceREDSparkRow{Source: f.ID, HasErrors: a.errs, HasLatency: a.lat, Buckets: make([]serviceREDSparkBucket, 0, len(a.by))}
		for _, b := range a.by {
			row.Buckets = append(row.Buckets, *b)
		}
		sort.Slice(row.Buckets, func(i, j int) bool { return row.Buckets[i].T < row.Buckets[j].T })
		out[svc] = row
	}
	return out
}

// servicesREDSparkReader — VM okuyucusunun bu ucun kullandığı yüzü.
type servicesREDSparkReader interface {
	httpEnvLabelReader
	QueryPromQLRange(ctx context.Context, query string, from, to time.Time, stepSeconds, maxDataPoints int) ([]chstore.SpanMetricSeries, error)
}

// buildServicesREDSpark — aile başına tek aralık sorgusu, aileler paralel.
func (s *Server) buildServicesREDSpark(ctx context.Context, rd servicesREDSparkReader, pairs map[string][]string, env, cluster string, from, to time.Time, slots int, showInfra bool) servicesREDSparkResponse {
	if win := to.Sub(from); win < servicesRedMetricMinWindow {
		from = to.Add(-servicesRedMetricMinWindow)
	} else if win > servicesRedMetricMaxWindow {
		from = to.Add(-servicesRedMetricMaxWindow)
	}
	step := servicesREDSparkStep(to.Sub(from), slots)
	from, to = servicesREDSparkGrid(from, to, step)
	out := servicesREDSparkResponse{Source: metricSourceVM, StepSec: step, Services: map[string]serviceREDSparkRow{}}
	svcLabel := redFamilySvcLabel(rd.MetricLabelMap().Normalized())
	terms := httpEnvScopeTerms(env, cluster)
	ctx, cancel := context.WithTimeout(ctx, servicesRedMetricBudget)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, f := range redFamilies { // katalog sırası: not sırası belirlenimci
		svcs := pairs[f.ID]
		if len(svcs) == 0 {
			continue
		}
		wg.Add(1)
		go func(f redFamily, svcs []string) {
			defer wg.Done()
			note := func(n string) { mu.Lock(); out.Notes = append(out.Notes, n); mu.Unlock() }
			envMatcher := ""
			if len(terms) > 0 {
				sc, err := s.resolveMetricEnvScope(ctx, rd, f.Probe, terms, to)
				if err != nil {
					note(f.ID + ": ortam etiketi okunamadı (" + string(sourcestate.Classify(err)) + ") — mini-grafik span'e düştü")
					return
				}
				if !sc.Applied || sc.Matcher == "" {
					return
				}
				envMatcher = sc.Matcher
			}
			qctx, qcancel := context.WithTimeout(ctx, servicesRedMetricQueryCap)
			defer qcancel()
			rows, err := rd.QueryPromQLRange(qctx, servicesREDSparkQuery(f, svcLabel, svcs, envMatcher, step, showInfra), from, to, step, slots)
			mu.Lock()
			defer mu.Unlock()
			out.Queries++
			if err != nil {
				out.Notes = append(out.Notes, f.ID+": mini-grafik okunamadı ("+string(sourcestate.Classify(err))+") — span'e düştü")
				return
			}
			for svc, r := range servicesREDSparkFold(f, svcLabel, svcs, rows) {
				out.Services[svc] = servicesREDSparkFill(r, from, to, step)
			}
		}(f, svcs)
	}
	wg.Wait()
	sort.Strings(out.Notes)
	return out
}

// getServicesREDSpark — GET /api/services-red-metric-spark
func (s *Server) getServicesREDSpark(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pairs := servicesREDSparkPairs(q.Get("services"), q.Get("sources"))
	env, cluster := strings.TrimSpace(q.Get("env")), strings.TrimSpace(q.Get("cluster"))
	if len(env) > httpEnvScopeTermMax || len(cluster) > httpEnvScopeTermMax {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("env/cluster en çok %d karakter", httpEnvScopeTermMax))
		return
	}
	slots := servicesRedSparkDefaultSlots
	if v, err := strconv.Atoi(q.Get("slots")); err == nil {
		slots = min(max(v, servicesRedSparkMinSlots), servicesRedSparkMaxSlots)
	}
	from, to := parseFromTo(r, time.Hour)
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	vm, ok := src.(vmMetricSource)
	if !ok || len(pairs) == 0 {
		writeJSON(w, servicesREDSparkResponse{Services: map[string]serviceREDSparkRow{}})
		return
	}
	showInfra := q.Get("infra") == "1"
	key := servicesREDSparkKey(pairs, env, cluster, from, to, slots, vm.MetricLabelMap().Normalized(), showInfra)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		return s.buildServicesREDSpark(ctx, vm, pairs, env, cluster, from, to, slots, showInfra), nil
	})
}
