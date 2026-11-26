package api

// ml_source_series.go — v0.10.2036 (anomali detayı: kalan olay türleri kendi
// paneliyle).
//
//   GET /api/ml/source-series?detector=&service=&code=&from=<ns>&to=<ns>
//
// Metrik (VM ya da Thanos) ve bağımlılık olayının detay grafiği: dedektörün
// KENDİ ifadesi servis kapsamlı (ml.BuildSourceQuery, saf), TEK aralık sorgusu.
// İfade istemciden gelmez (Settings'teki aile ifadesi; servis/hedef matcher
// olarak kaçışlı). Pencere/adım snapAnomalyLogChartWindow ile (≤ 3 sa → 1 dk;
// uzunda adım büyür, ≤ 180 nokta, ≤ 7 gün). Thanos: cluster ADIYLA çözülen
// etkin kayıt, işçi sınırları (seri tavanı 50, 20 s); kesik/kısmi yanıt boş
// sayılır. Salt okunur (viewer görür); önbellek 30 s, anahtar tüm girdiler.
// İfade kurulamaz ya da kaynak yoksa 200 + boş seri: istemci kayıtlı
// görüntüye düşer, başlık bunu söyler.
//
// v0.10.2055 — trace_pod_outlier (code = ölçü err_ratio | p95, ?family=
// kanıttaki RED ailesi): dedektörün kaynağı VM RED ailesi pod başına
// (ml.TracePodSourceQuery); aile başına bir etiket keşfi + bir aralık sorgusu.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
	"github.com/cilcenk/coremetry/internal/thanos"
)

func init() {
	registerRoutesExtra("ml-source-series", (*Server).registerMLSourceSeriesRoutes)
}

func (s *Server) registerMLSourceSeriesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ml/source-series", auth.RequireAnyRole(mlReadRoles, s.getMLSourceSeries))
}

const (
	mlSourceSeriesTTL       = 30 * time.Second
	mlSourceSeriesTimeout   = 20 * time.Second
	mlSourceSeriesMaxSeries = 50
)

type mlSourceSeriesResponse struct {
	Query     ml.SourceQuery             `json:"query"`
	StepSec   int                        `json:"stepSec"`
	Series    []chstore.SpanMetricSeries `json:"series"`
	Truncated bool                       `json:"truncated,omitempty"`
	// Reason — v0.10.2061: canlı seri neden yok (tek düz cümle); seri varsa boş.
	Reason string `json:"reason,omitempty"`
	// ReasonCode — v0.10.2480: nedenin makine sınıfı (srcCode*); istemci
	// arayüz dilinde söyler. Detail: kaynağın kendi hata metni (URL'siz,
	// kısaltılmış; sır içermez). Kaynak hatası artık 502 değil 200 + kod:
	// sayfa "kaynak okunamadı" diye genelleyip nedeni yutuyordu.
	ReasonCode string `json:"reasonCode,omitempty"`
	Detail     string `json:"detail,omitempty"`
	// Labels — v0.10.2480: endpoint ifadesinin etiketleri nereden geldi:
	// "event" (olay kanıtı, dedektörün tik anındaki etiketleri) | "discovery".
	Labels string `json:"labels,omitempty"`
}

// Canlı seri yokken detayın tek satırı (v0.10.2061; eskiden kayıtlı görüntüye
// sessizce düşülüyordu).
const (
	srcGapNoVM        = "metrik deposu (VictoriaMetrics) yapılandırılmamış"
	srcGapNoExpr      = "olayın ölçtüğü ifade ayarlarda artık yok (Settings › Anomaly › ML'de değişmiş ya da silinmiş)"
	srcGapNoCluster   = "olayın Thanos cluster'ı ayarlarda etkin değil"
	srcGapPartial     = "Thanos yanıtı kısmi geldi; eksik veriden grafik çizilmez"
	srcGapNoPodLabels = "RED metriklerinde pod etiketi bulunamadı"
	srcGapEmpty       = "olay penceresinde kaynakta seri yok (saklama süresi dolmuş ya da metrik adı değişmiş olabilir)"
)

// Nedenin makine sınıfı (v0.10.2480). İstemci bu kodla TR/EN cümle kurar.
const (
	srcCodeNoVM      = "no_vm"
	srcCodeNoExpr    = "no_expr"
	srcCodeNoCluster = "no_cluster"
	srcCodePartial   = "partial"
	srcCodeNoLabels  = "no_labels"
	srcCodeEmpty     = "empty"
	srcCodeTimeout   = "timeout"
	srcCodeQueryErr  = "query_error"
)

const (
	srcGapTimeout  = "kaynak sorgusu zaman aşımına uğradı"
	srcGapQueryErr = "kaynak sorguyu reddetti ya da yanıt vermedi"
)

// srcCodeOf — SAF: gerekçe cümlesi → kod.
func srcCodeOf(reason string) string {
	switch reason {
	case srcGapNoVM:
		return srcCodeNoVM
	case srcGapNoExpr:
		return srcCodeNoExpr
	case srcGapNoCluster:
		return srcCodeNoCluster
	case srcGapPartial:
		return srcCodePartial
	case srcGapNoPodLabels:
		return srcCodeNoLabels
	case srcGapTimeout:
		return srcCodeTimeout
	case srcGapQueryErr:
		return srcCodeQueryErr
	}
	return srcCodeEmpty
}

var srcURLRe = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"']+`)

// srcErrDetail — SAF: kaynak hatasının gösterilebilir metni. URL'ler (kimlik
// bilgisi taşıyabilir) "<url>" olur; en çok 300 rune.
func srcErrDetail(err error) string {
	if err == nil {
		return ""
	}
	d := srcURLRe.ReplaceAllString(err.Error(), "<url>")
	d = strings.TrimPrefix(d, errUpstream.Error()+": ")
	if r := []rune(d); len(r) > 300 {
		d = string(r[:300]) + "…"
	}
	return d
}

// withFailure — SAF (v0.10.2480): kaynak hatası → 200 + kod + metin (zaman
// aşımı ayrı sınıf). Seri boşaltılır; Query (kurulduysa) korunur ki teknik
// ayrıntı denenen ifadeyi göstersin.
func withFailure(out mlSourceSeriesResponse, err error) mlSourceSeriesResponse {
	out.Series = []chstore.SpanMetricSeries{}
	reason := srcGapQueryErr
	low := strings.ToLower(err.Error())
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(low, "deadline exceeded") || strings.Contains(low, "timeout") {
		reason = srcGapTimeout
	}
	out = withReason(out, reason)
	out.Detail = srcErrDetail(err)
	return out
}

// withReason — SAF: seri yoksa ve gerekçe verilmemişse boş-pencere gerekçesi.
func withReason(out mlSourceSeriesResponse, reason string) mlSourceSeriesResponse {
	if len(out.Series) > 0 {
		out.Reason, out.ReasonCode = "", ""
		return out
	}
	if reason == "" {
		reason = srcGapEmpty
	}
	out.Reason, out.ReasonCode = reason, srcCodeOf(reason)
	return out
}

// mlSourceSeriesKey — SAF; her girdi anahtarda (v0.5.187). Serbest metinler
// fnvStr ile ayrı ayrı.
func mlSourceSeriesKey(detector, service, code, family string, from, to time.Time, step int) string {
	return fmt.Sprintf("ml-source-series:v3:%s:from=%d:to=%d:s=%d", fnvStr(detector, service, code, family), from.Unix(), to.Unix(), step)
}

func (s *Server) getMLSourceSeries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	det, svc, code := strings.TrimSpace(q.Get("detector")), strings.TrimSpace(q.Get("service")), strings.TrimSpace(q.Get("code"))
	if det == "" || svc == "" || code == "" {
		writeJSONError(w, http.StatusBadRequest, "detector, service ve code zorunlu")
		return
	}
	from, to, step, ok := snapAnomalyLogChartWindow(parseTime(q.Get("from")), parseTime(q.Get("to")), time.Now())
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "from zorunlu ve to'dan önce olmalı")
		return
	}
	family := strings.TrimSpace(q.Get("family"))
	measure := strings.TrimSpace(q.Get("measure")) // v0.10.2204: trace_red ölçüsü (p95 | slow_ratio | err_ratio | rate)
	// v0.10.2480: olay kanıtındaki etiket ADLARI (dedektörün tik anında
	// seçtiği); ifade yine sunucuda, dedektörün kurucusuyla kurulur.
	svcL, routeL, statusL := strings.TrimSpace(q.Get("svcLabel")), strings.TrimSpace(q.Get("routeLabel")), strings.TrimSpace(q.Get("statusLabel"))
	extra := strings.Join([]string{family, measure, svcL, routeL, statusL}, "\x00")
	s.serveCached(w, r, mlSourceSeriesKey(det, svc, code, extra, from, to, step), mlSourceSeriesTTL, func(ctx context.Context) (any, error) {
		out := mlSourceSeriesResponse{StepSec: step, Series: []chstore.SpanMetricSeries{}}
		rd, vmOK := s.metricChartReaderFor()
		if det == "trace_pod_outlier" {
			if !vmOK {
				return withReason(out, srcGapNoVM), nil
			}
			return tracePodSourceSeries(ctx, rd, out, svc, code, family, from, to, step), nil
		}
		if isTraceREDDetector(det) {
			if !vmOK {
				return withReason(out, srcGapNoVM), nil
			}
			cfg := s.store.GetMLConfig(ctx)
			return traceREDSourceSeries(ctx, rd, out, traceREDChartIn{Service: svc, Route: code, Measure: traceREDMeasure(det, measure),
				Family: family, SvcLabel: svcL, RouteLabel: routeL, StatusLabel: statusL, BucketSec: cfg.BucketSec, SlowSec: cfg.SlowRequestSec},
				from, to, step), nil
		}
		svcLabel := ""
		if vmOK {
			svcLabel = rd.MetricLabelMap().Normalized().Service
		}
		// v0.10.2060 — Thanos olayı: Pods sekmesinin namespace + deployment'ı
		// servis metadata'sından (tek anahtarlı okuma; yoksa pod regex'i).
		var scope ml.PodScope
		if strings.Contains(code, "@") {
			if m, _ := s.store.GetServiceMetadata(ctx, svc); m != nil {
				scope = ml.PodScope{Namespace: m.Namespace, Deploy: m.Deployment}
				if scope.Namespace != "" && scope.Deploy == "" {
					scope.Deploy = svc // Pods sekmesinin yedeği: deployment yoksa servis adı
				}
			}
		}
		sq, ok := ml.BuildSourceQuery(s.store.GetMLConfig(ctx), det, code, svc, svcLabel, scope)
		if !ok {
			return withReason(out, srcGapNoExpr), nil
		}
		out.Query = sq
		cctx, cancel := context.WithTimeout(ctx, mlSourceSeriesTimeout)
		defer cancel()
		if sq.Cluster == "" {
			if !vmOK {
				return withReason(out, srcGapNoVM), nil
			}
			res, err := rd.QueryPromQLRange(cctx, sq.Expr, from, to, step, anomalyLogChartMaxBuckets+1)
			if err != nil {
				return withFailure(out, err), nil
			}
			if res != nil {
				out.Series = res
			}
			return withReason(out, ""), nil
		}
		c, found := s.thanos.ClusterByName(sq.Cluster)
		if !found {
			return withReason(out, srcGapNoCluster), nil
		}
		res, err := s.thanos.ConsoleQueryRange(cctx, c, thanos.ConsoleRangeQuery{Query: sq.Expr, Start: from, End: to, Step: time.Duration(step) * time.Second},
			thanos.ConsoleLimits{Timeout: mlSourceSeriesTimeout, MaxSeries: mlSourceSeriesMaxSeries, MaxBodyBytes: 8 << 20})
		if err != nil {
			return withFailure(out, err), nil
		}
		if res.Partial() {
			return withReason(out, srcGapPartial), nil
		}
		out.Truncated = res.Truncated
		out.Series = decodeMatrixSeries(res.ResultType, res.Result, sq.By)
		return withReason(out, ""), nil
	})
}

// tracePodSourceSeries — v0.10.2055: trace_pod_outlier detay grafiği. Kaynak
// dedektörünki (VM RED ailesi, pod etiketi); aile kanıttan gelirse yalnız o,
// gelmezse (eski olay) ailelerin sırasıyla ilk veri dönen. Aile başına bir
// etiket keşfi (VM metadata) + bir aralık sorgusu; en çok 2 aile.
// v0.10.2480: kaynak hatası 200 + kod (withFailure); bir ailenin keşif hatası
// diğer aileyi denemeyi kesmez.
func tracePodSourceSeries(ctx context.Context, rd metricChartReader, out mlSourceSeriesResponse, svc, measure, family string,
	from, to time.Time, step int) mlSourceSeriesResponse {
	cctx, cancel := context.WithTimeout(ctx, mlSourceSeriesTimeout)
	defer cancel()
	pref := rd.MetricLabelMap().Normalized().Service
	var firstErr error
	for _, pin := range anomaly.REDFamilyPins() {
		if family != "" && pin.ID != family {
			continue
		}
		names, _, err := rd.MetricLabelNamesIn(cctx, ml.REDDiscoveryMetric(pin), from.Add(-time.Hour), to)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		labels, ok := ml.PickTracePodLabels(names, pref, pin)
		if !ok {
			continue
		}
		sq, ok := ml.TracePodSourceQuery(pin, labels, svc, measure)
		if !ok {
			continue
		}
		out.Query = sq
		res, err := rd.QueryPromQLRange(cctx, sq.Expr, from, to, step, anomalyLogChartMaxBuckets+1)
		if err != nil {
			return withFailure(out, err)
		}
		out.Series = seriesByLabel(res, sq.By, mlSourceSeriesMaxSeries)
		out.Truncated = len(res) > mlSourceSeriesMaxSeries
		if len(out.Series) > 0 {
			return out
		}
	}
	if out.Query.Expr == "" {
		if firstErr != nil {
			return withFailure(out, firstErr)
		}
		return withReason(out, srcGapNoPodLabels)
	}
	return withReason(out, "")
}

// isTraceREDDetector — SAF (v0.10.2204): kaynağı VM RED ailesi olan endpoint
// dedektörleri (ml/trace_red.go); Code = route etiketinin değeri.
func isTraceREDDetector(det string) bool {
	return det == "op_latency" || det == "op_error_rate" || det == "throughput_drop"
}

// traceREDMeasure — SAF: dedektör + kanıttaki ölçü → sorgu ölçüsü.
func traceREDMeasure(det, measure string) string {
	switch det {
	case "op_error_rate":
		return "err_ratio"
	case "throughput_drop":
		return "rate"
	}
	if measure == "p95" {
		return "p95"
	}
	return "slow_ratio" // v0.10.2059 öncesi olay ve "slow" ölçüsü
}

// traceREDChartIn — endpoint olayının grafik girdisi. Family/…Label: olay
// kanıtındaki (dedektörün tik anında seçtiği) aile ve etiket adları; boşsa
// eski olay → keşif.
type traceREDChartIn struct {
	Service, Route, Measure                   string
	Family, SvcLabel, RouteLabel, StatusLabel string
	BucketSec                                 int
	SlowSec                                   float64
}

// traceREDSourceSeries — v0.10.2204: endpoint olayının detay grafiği,
// dedektörün kendi kaynağı (VM RED ailesi, servis + route etiketi, dedektörün
// kova penceresi). Eskiden Traces şeridi ClickHouse span'lerinde name =
// Code arıyordu; gRPC'de Code rpc_method değeri ("callBagService"), span adı
// "…BagService/callBagService" → hiç nokta yok.
//
// v0.10.2480 (operatör: taze olayda "kaynak okunamadı"): ifade olay
// kanıtındaki aile + etiketlerle, dedektörün kayıt kurucusundan (tek aralık
// sorgusu, keşif YOK). Eskiden her açılışta ailelerin FİLO ÇAPINDA etiket
// keşfi (pencere + 1 sa, /api/v1/labels) koşuyordu: dedektör bunu kendi kısa
// penceresiyle yapıp saatlik önbellekliyor ve hatasını yutuyor; grafik ise
// İLK ailenin keşif hatasında (zaman aşımı / VM seri sınırı) 502 dönüyor,
// sayfa nedeni yutup "kaynak okunamadı" diyordu. Keşif yalnız kanıtında
// etiket olmayan eski olaylarda, aile başına hatası yalıtılmış.
func traceREDSourceSeries(ctx context.Context, rd metricChartReader, out mlSourceSeriesResponse, in traceREDChartIn,
	from, to time.Time, step int) mlSourceSeriesResponse {
	cctx, cancel := context.WithTimeout(ctx, mlSourceSeriesTimeout)
	defer cancel()
	// run — tek aralık sorgusu; done: karar verildi (seri ya da hata).
	run := func(sq ml.SourceQuery) (mlSourceSeriesResponse, bool) {
		out.Query = sq
		res, err := rd.QueryPromQLRange(cctx, sq.Expr, from, to, step, anomalyLogChartMaxBuckets+1)
		if err != nil {
			return withFailure(out, err), true
		}
		if len(res) > 0 && len(res[0].Points) > 0 {
			out.Series = res[:1]
			return out, true
		}
		return out, false
	}
	if pin, l, ok := ml.TraceREDEventSource(in.Family, in.SvcLabel, in.RouteLabel, in.StatusLabel); ok {
		out.Labels = "event"
		sq, ok := ml.TraceREDSourceQuery(pin, l, in.Service, in.Route, in.Measure, in.BucketSec, in.SlowSec)
		if !ok {
			return withReason(out, srcGapNoExpr)
		}
		if r, done := run(sq); done {
			return r
		}
		return withReason(out, "")
	}
	out.Labels = "discovery"
	pref := rd.MetricLabelMap().Normalized().Service
	var firstErr error
	for _, pin := range anomaly.REDFamilyPins() {
		if in.Family != "" && pin.ID != in.Family {
			continue
		}
		names, _, err := rd.MetricLabelNamesIn(cctx, ml.REDDiscoveryMetric(pin), from, to)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		labels, ok := ml.PickTraceREDLabels(names, pref, pin)
		if !ok {
			continue
		}
		sq, ok := ml.TraceREDSourceQuery(pin, labels, in.Service, in.Route, in.Measure, in.BucketSec, in.SlowSec)
		if !ok {
			continue
		}
		if r, done := run(sq); done {
			return r
		}
	}
	if out.Query.Expr == "" {
		if firstErr != nil {
			return withFailure(out, firstErr)
		}
		return withReason(out, srcGapNoExpr)
	}
	return withReason(out, "")
}

// seriesByLabel — SAF: VM aralık serilerinin grup anahtarını tek etiketin
// değerine indirger (pod adı); boş değerli seri düşer, ada göre sıralı, en
// çok capN seri.
func seriesByLabel(in []chstore.SpanMetricSeries, by string, capN int) []chstore.SpanMetricSeries {
	out := []chstore.SpanMetricSeries{}
	for _, sr := range in {
		v := ""
		for _, kv := range sr.GroupKey {
			k, val, ok := strings.Cut(kv, "=")
			if !ok || k != by {
				continue
			}
			if u, err := strconv.Unquote(val); err == nil {
				val = u
			}
			v = val
		}
		if v == "" || len(sr.Points) == 0 {
			continue
		}
		sr.GroupKey = []string{v}
		out = append(out, sr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GroupKey[0] < out[j].GroupKey[0] })
	if len(out) > capN {
		out = out[:capN]
	}
	return out
}

// decodeMatrixSeries — SAF: Prometheus matrisi → seriler (sonlu değerler);
// grup anahtarı `by` etiketinin değeri (boşsa tek seri ""). Ada göre sıralı.
func decodeMatrixSeries(resultType string, raw json.RawMessage, by string) []chstore.SpanMetricSeries {
	out := []chstore.SpanMetricSeries{}
	if resultType != "matrix" || len(raw) == 0 {
		return out
	}
	var rows []struct {
		Metric map[string]string `json:"metric"`
		Values [][2]any          `json:"values"`
	}
	if json.Unmarshal(raw, &rows) != nil {
		return out
	}
	for _, r := range rows {
		key := ""
		if by != "" {
			key = r.Metric[by]
		}
		sr := chstore.SpanMetricSeries{GroupKey: []string{key}}
		for _, v := range r.Values {
			ts, ok1 := v[0].(float64)
			vs, ok2 := v[1].(string)
			if !ok1 || !ok2 {
				continue
			}
			f, err := strconv.ParseFloat(vs, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				continue
			}
			sr.Points = append(sr.Points, chstore.SpanMetricPoint{Time: int64(ts * 1e9), Value: f})
		}
		if len(sr.Points) > 0 {
			out = append(out, sr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GroupKey[0] < out[j].GroupKey[0] })
	return out
}
