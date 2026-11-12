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
	"fmt"
	"math"
	"net/http"
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

// withReason — SAF: seri yoksa ve gerekçe verilmemişse boş-pencere gerekçesi.
func withReason(out mlSourceSeriesResponse, reason string) mlSourceSeriesResponse {
	if len(out.Series) > 0 {
		out.Reason = ""
		return out
	}
	if reason == "" {
		reason = srcGapEmpty
	}
	out.Reason = reason
	return out
}

// mlSourceSeriesKey — SAF; her girdi anahtarda (v0.5.187). Serbest metinler
// fnvStr ile ayrı ayrı.
func mlSourceSeriesKey(detector, service, code, family string, from, to time.Time, step int) string {
	return fmt.Sprintf("ml-source-series:v2:%s:from=%d:to=%d:s=%d", fnvStr(detector, service, code, family), from.Unix(), to.Unix(), step)
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
	s.serveCached(w, r, mlSourceSeriesKey(det, svc, code, family, from, to, step), mlSourceSeriesTTL, func(ctx context.Context) (any, error) {
		out := mlSourceSeriesResponse{StepSec: step, Series: []chstore.SpanMetricSeries{}}
		rd, vmOK := s.metricChartReaderFor()
		if det == "trace_pod_outlier" {
			if !vmOK {
				return withReason(out, srcGapNoVM), nil
			}
			return s.tracePodSourceSeries(ctx, rd, out, svc, code, family, from, to, step)
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
				return nil, fmt.Errorf("%w: vm: %v", errUpstream, err)
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
			return nil, fmt.Errorf("%w: thanos: %v", errUpstream, err)
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
func (s *Server) tracePodSourceSeries(ctx context.Context, rd metricChartReader, out mlSourceSeriesResponse, svc, measure, family string,
	from, to time.Time, step int) (any, error) {
	cctx, cancel := context.WithTimeout(ctx, mlSourceSeriesTimeout)
	defer cancel()
	pref := rd.MetricLabelMap().Normalized().Service
	for _, pin := range anomaly.REDFamilyPins() {
		if family != "" && pin.ID != family {
			continue
		}
		names, _, err := rd.MetricLabelNamesIn(cctx, pin.Names+"_count", from.Add(-time.Hour), to)
		if err != nil {
			return nil, fmt.Errorf("%w: vm: %v", errUpstream, err)
		}
		labels, ok := ml.PickTracePodLabels(names, pref, pin)
		if !ok {
			continue
		}
		sq, ok := ml.TracePodSourceQuery(pin, labels, svc, measure)
		if !ok {
			continue
		}
		res, err := rd.QueryPromQLRange(cctx, sq.Expr, from, to, step, anomalyLogChartMaxBuckets+1)
		if err != nil {
			return nil, fmt.Errorf("%w: vm: %v", errUpstream, err)
		}
		out.Query = sq
		out.Series = seriesByLabel(res, sq.By, mlSourceSeriesMaxSeries)
		out.Truncated = len(res) > mlSourceSeriesMaxSeries
		if len(out.Series) > 0 {
			return out, nil
		}
	}
	if out.Query.Expr == "" {
		return withReason(out, srcGapNoPodLabels), nil
	}
	return withReason(out, ""), nil
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
