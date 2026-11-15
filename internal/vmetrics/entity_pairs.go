package vmetrics

// entity_pairs.go — v0.10.2261 (Entity S6 parça 2, P-c;
// docs/architecture/entity-s6-cost-note.md seçenek C): entity katmanının
// metrik kolu. Lider syncer'ı 15 dk'da bir (entity.MetricsEvery) çağırır;
// sorgu sayısı geçiş başına sabit ≤ 3, seri tavanı topk ile:
//
//  1. servis adları: /api/v1/label/<servis>/values, match[] {<servis>!=""},
//     son 15 dk, VM limit = entity.MaxMetricServices;
//  2. pod → servis: topk(N, count by (<servis>, <pod>[, <ns>])
//     (last_over_time(target_info{<servis>!="", <pod>!=""}[15m])));
//  3. host → servis: aynı, pod etiketi boş ve host_name dolu seriler.
//
// Etiket adları operatörün LabelMap'inden (labelmap.go): pod etiketi
// YAPILANDIRILMAMIŞSA geçiş koşmaz (entity.ErrMetricLabelsUnconfigured;
// syncer bir kez günlükler). Servis etiketi yapılandırılmamışsa ürün
// konvansiyonu (service_name). Ham metric_points okunmaz; target_info
// OTLP kaynak öznitelik serisidir — Prometheus kazımasıyla gelen ve
// target_info basmayan kurulumda pod / host eşleşmesi boş döner (servis
// adları yine gelir).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/promapi"
)

// entityPairsMetric — eşleşmenin okunduğu tek metrik (OTel kaynak serisi).
const entityPairsMetric = "target_info"

// entityHostLabel — OTel host.name'in promLabel karşılığı.
var entityHostLabel = promLabel("host.name")

// EntityPairQueries — SAF: eşleme → pod ve host sorguları (testte pinli).
// ok=false: pod etiketi yapılandırılmamış.
func EntityPairQueries(m LabelMap, window time.Duration, maxSeries int) (svc, podQ, hostQ string, ok bool) {
	m = m.Normalized()
	pod := m.Get(RolePod)
	if pod == "" || !ValidLabelName(pod) {
		return "", "", "", false
	}
	svc = m.Get(RoleService)
	if svc == "" || !ValidLabelName(svc) {
		svc = LabelName("service.name")
	}
	by := svc + ", " + pod
	if ns := m.Get(RoleNamespace); ns != "" && ValidLabelName(ns) {
		by += ", " + ns
	}
	w := strconv.Itoa(int(window/time.Second)) + "s"
	podQ = fmt.Sprintf(`topk(%d, count by (%s) (last_over_time(%s{%s!="", %s!=""}[%s])))`,
		maxSeries, by, entityPairsMetric, svc, pod, w)
	hostQ = fmt.Sprintf(`topk(%d, count by (%s, %s) (last_over_time(%s{%s!="", %s="", %s!=""}[%s])))`,
		maxSeries, svc, entityHostLabel, entityPairsMetric, svc, pod, entityHostLabel, w)
	return svc, podQ, hostQ, true
}

// EntityMetricPairs — entity.MetricSource. Her çağrı ≤ 3 VM isteği.
func (s *Service) EntityMetricPairs(ctx context.Context) (entity.MetricSnapshot, error) {
	var out entity.MetricSnapshot
	cfg, err := s.ready()
	if err != nil {
		return out, fmt.Errorf("%w: %v", entity.ErrMetricLabelsUnconfigured, err)
	}
	m := cfg.LabelMap.Normalized()
	svc, podQ, hostQ, ok := EntityPairQueries(m, entity.MetricsWindow, entity.MaxMetricSeries)
	if !ok {
		return out, fmt.Errorf("%w: VictoriaMetrics etiket eşlemesinde pod etiketi yok", entity.ErrMetricLabelsUnconfigured)
	}
	now := time.Now()
	vals, partial, err := s.LabelValues(ctx, svc, `{`+svc+`!=""}`, now.Add(-entity.MetricsWindow), now, entity.MaxMetricServices)
	out.Queries++
	if err != nil {
		return out, fmt.Errorf("servis etiket değerleri: %w", err)
	}
	out.Services = vals
	out.Truncated = partial || len(vals) >= entity.MaxMetricServices
	pod, ns := m.Get(RolePod), m.Get(RoleNamespace)
	podRows, ptr, err := s.entityVector(ctx, cfg, podQ, now)
	out.Queries++
	if err != nil {
		return out, fmt.Errorf("pod → servis: %w", err)
	}
	for _, l := range podRows {
		if l[svc] != "" && l[pod] != "" {
			out.Runs = append(out.Runs, entity.MetricRun{Service: l[svc], Pod: l[pod], Namespace: l[ns]})
		}
	}
	hostRows, htr, err := s.entityVector(ctx, cfg, hostQ, now)
	out.Queries++
	if err != nil {
		return out, fmt.Errorf("host → servis: %w", err)
	}
	for _, l := range hostRows {
		if l[svc] != "" && l[entityHostLabel] != "" {
			out.Runs = append(out.Runs, entity.MetricRun{Service: l[svc], Host: l[entityHostLabel]})
		}
	}
	out.Truncated = out.Truncated || ptr || htr
	return out, nil
}

// entityVector — anlık sorgu, yalnız etiket kümeleri. promapi'nin 1000
// serilik ayrıştırma tavanı burada geçerli değil (topk N ile sınırlı;
// gövde promapi.MaxBodyBytes'ta kesilir). truncated: N'e ulaşıldı ya da
// vmselect isPartial.
func (s *Service) entityVector(ctx context.Context, cfg Settings, q string, at time.Time) ([]map[string]string, bool, error) {
	data, partial, err := promapi.QueryData(ctx, s.request("/api/v1/query", url.Values{"query": {q}, "time": {promTime(at)}}, cfg))
	if err != nil {
		return nil, false, err
	}
	rows, err := decodeEntityVector(data)
	if err != nil {
		return nil, false, err
	}
	return rows, partial || len(rows) >= entity.MaxMetricSeries, nil
}

// decodeEntityVector — SAF: `data` → etiket kümeleri (vector sonucu).
func decodeEntityVector(data json.RawMessage) ([]map[string]string, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var d struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("entity pairs decode: %w", err)
	}
	if d.ResultType != "" && !strings.EqualFold(d.ResultType, "vector") {
		return nil, fmt.Errorf("entity pairs: beklenmeyen sonuç tipi %q", d.ResultType)
	}
	out := make([]map[string]string, 0, len(d.Result))
	for _, r := range d.Result {
		out = append(out, r.Metric)
	}
	return out, nil
}
