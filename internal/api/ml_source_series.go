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
}

// mlSourceSeriesKey — SAF; her girdi anahtarda (v0.5.187). Serbest metinler
// fnvStr ile ayrı ayrı.
func mlSourceSeriesKey(detector, service, code string, from, to time.Time, step int) string {
	return fmt.Sprintf("ml-source-series:v1:%s:from=%d:to=%d:s=%d", fnvStr(detector, service, code), from.Unix(), to.Unix(), step)
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
	s.serveCached(w, r, mlSourceSeriesKey(det, svc, code, from, to, step), mlSourceSeriesTTL, func(ctx context.Context) (any, error) {
		out := mlSourceSeriesResponse{StepSec: step, Series: []chstore.SpanMetricSeries{}}
		rd, vmOK := s.metricChartReaderFor()
		svcLabel := ""
		if vmOK {
			svcLabel = rd.MetricLabelMap().Normalized().Service
		}
		sq, ok := ml.BuildSourceQuery(s.store.GetMLConfig(ctx), det, code, svc, svcLabel)
		if !ok {
			return out, nil
		}
		out.Query = sq
		cctx, cancel := context.WithTimeout(ctx, mlSourceSeriesTimeout)
		defer cancel()
		if sq.Cluster == "" {
			if !vmOK {
				return out, nil
			}
			res, err := rd.QueryPromQLRange(cctx, sq.Expr, from, to, step, anomalyLogChartMaxBuckets+1)
			if err != nil {
				return nil, fmt.Errorf("%w: vm: %v", errUpstream, err)
			}
			if res != nil {
				out.Series = res
			}
			return out, nil
		}
		c, found := s.thanos.ClusterByName(sq.Cluster)
		if !found {
			return out, nil
		}
		res, err := s.thanos.ConsoleQueryRange(cctx, c, thanos.ConsoleRangeQuery{Query: sq.Expr, Start: from, End: to, Step: time.Duration(step) * time.Second},
			thanos.ConsoleLimits{Timeout: mlSourceSeriesTimeout, MaxSeries: mlSourceSeriesMaxSeries, MaxBodyBytes: 8 << 20})
		if err != nil {
			return nil, fmt.Errorf("%w: thanos: %v", errUpstream, err)
		}
		if res.Partial() {
			return out, nil
		}
		out.Truncated = res.Truncated
		out.Series = decodeMatrixSeries(res.ResultType, res.Result, sq.By)
		return out, nil
	})
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
