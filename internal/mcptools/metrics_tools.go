package mcptools

// metrics_tools.go — v0.10.944 (CoSRE araştırma asistanı, Faz A): metrik
// sorusunun KANITA dönüştüğü iki araç.
//
//   - query_metric (yükseltilmiş; ad korunur — operatörün "query_metrics"i
//     budur): servis/env/cluster/namespace/pod bağlam argümanları GERÇEK
//     etiketlere eşlenir (operatör LabelMap'i → konvansiyon → canlı keşif);
//     eşlenemeyen süzgeç UYGULANMAZ ve sonuç kısmi (partial) işaretlenir.
//     labels/group_by metriğin canlı etiket kümesine karşı doğrulanır;
//     trace_id/span_id etiket olarak REDDEDİLİR (kardinalite; köprü exemplar).
//   - list_metric_labels (yeni): metriğin gerçek etiket adları (+ bir
//     etiketin değerleri) ve rol eşlemesi — query_metric'in keşif eşi.
//
// NEDEN: eski query_metric env argümanı almıyordu, 401'i düz metin, VM
// isPartial'ı ve 1000 seri tavanını hiç görmüyordu; boş seri "sorun yok"
// diye okunuyordu. Artık her sonuç `source` (sourcestate.Status) taşır:
// kaynak hatası (erişilemedi/yetki/zaman aşımı/yapılandırılmamış) BAŞARILI
// bir sonuçta durum olarak döner ki model diğer kaynaklarla devam etsin;
// Go hatası yalnız argüman hatası ve iptal içindir.
//
// Kabiliyetler Deps'e dokunmadan TİP İDDİASIYLA aranır (metricNoteSource
// deseni): VictoriaMetrics kaynağı (vmetrics.Service ya da api router'ı)
// keşif + eşleme + bayraklı sorgu sunar; ClickHouse kaynağı sunmaz ve araç
// ClickHouse konvansiyon yoluna düşer (metric_points süzgeç anahtarları sabit).
//
// ZAMAN: VM unix saniye, CH ns — dönüşüm adaptör kenarında; buradan çıkan
// her zaman UTC RFC3339 (`*_iso`). Pencere rangeWindow/çıpadan geçer.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// Sınırlar (açıklamalarda ilan edilir).
const (
	mtMaxSeries      = 20
	mtMaxPoints      = 120
	mtStepBudget     = mtMaxPoints - 2 // v0.10.944 — adım hizası kenarda en çok 2 kova ekler (CH toStartOfInterval baştaki kısmi kova; VM start/end adım hizası) → ≤120 nokta garanti
	mtMaxLabels      = 8
	mtMaxLabelNames  = 200
	mtMaxWindow      = 7 * 24 * time.Hour
	mtDiscoverFloor  = time.Hour // keşif penceresi tabanı (VM etiket indeksi günlük)
	mtDiscoverBudget = 5 * time.Second
	mtQueryBudget    = 10 * time.Second
	mtDelayRecent    = 10 * time.Minute
	mtDelayMinSlack  = 120 * time.Second
	mtSourceMetrics  = "metrics"
	mtBackendCH      = "clickhouse"
)

// mtAggregations — mevcut enum (iki backend de çevirebiliyor).
var mtAggregations = []string{"avg", "sum", "min", "max", "last", "p50", "p95", "p99"}

// ── İsteğe bağlı kaynak kabiliyetleri (tip iddiası) ──────────────────────

// metricBackendNamer — okumanın gittiği depo ("victoriametrics" | "clickhouse").
type metricBackendNamer interface{ MetricBackend() string }

// metricLabelDiscoverer — pencere içi GERÇEK etiket adları (VM /api/v1/labels).
// v0.10.944 — ikinci dönüş VM `isPartial`: küme eksik olabilir, "yok" kanıtı
// değildir (görülmeyen etiket reddedilmez, doğrulanmadan uygulanır + partial).
type metricLabelDiscoverer interface {
	MetricLabelNamesIn(ctx context.Context, metric string, from, to time.Time) ([]string, bool, error)
}

// metricDetailQuerier — isPartial + seri tavanı + adım + cevap anı taşıyan sorgu.
type metricDetailQuerier interface {
	QueryMetricDetailed(ctx context.Context, f chstore.MetricQueryFilter) (vmetrics.QueryDetail, error)
}

// metricLabelMapper — operatörün VM LabelMap'i.
type metricLabelMapper interface{ MetricLabelMap() vmetrics.LabelMap }

// metricLabelValuesIn — pencereli etiket değerleri (VM); ikinci dönüş isPartial.
type metricLabelValuesIn interface {
	MetricLabelValuesIn(ctx context.Context, metric, label string, from, to time.Time, limit int) ([]string, bool, error)
}

// metricAttrKeyReader / metricLabelValueReader — ClickHouse yolu (since tabanlı;
// *chstore.Store ve api router'ı taşır).
type metricAttrKeyReader interface {
	MetricAttrKeys(ctx context.Context, metric, service string, since time.Duration) ([]string, error)
}

type metricLabelValueReader interface {
	MetricLabelValues(ctx context.Context, metric, key string, since time.Duration, q string, limit int) ([]string, error)
}

// mtSource — metrik kaynağı; ikisi de yoksa nil (not_configured). d.metrics()
// nil *chstore.Store'u arayüze sarıp nil-OLMAYAN bir değer döndürebilirdi —
// o yüzden burada açıkça bakılır.
func mtSource(d Deps) MetricSource {
	if d.Metrics != nil {
		return d.Metrics
	}
	if d.Store != nil {
		return d.Store
	}
	return nil
}

func mtBackend(src MetricSource) string {
	if n, ok := src.(metricBackendNamer); ok {
		if b := n.MetricBackend(); b != "" {
			return b
		}
	}
	return mtBackendCH
}

// mtVMCapable — VictoriaMetrics yolu: keşif + bayraklı sorgu birlikte.
func mtVMCapable(src MetricSource) (metricLabelDiscoverer, metricDetailQuerier, bool) {
	disc, ok1 := src.(metricLabelDiscoverer)
	det, ok2 := src.(metricDetailQuerier)
	return disc, det, ok1 && ok2
}

func mtLabelMap(src MetricSource) vmetrics.LabelMap {
	if m, ok := src.(metricLabelMapper); ok {
		return m.MetricLabelMap()
	}
	return vmetrics.LabelMap{}
}

// ── Argüman yardımcıları (SAF) ───────────────────────────────────────────

// mtStringList — group_by: JSON dizi YA DA virgüllü metin (eski sözleşme).
type mtStringList []string

func (l *mtStringList) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*l = mtSplit(s)
		return nil
	}
	var arr []string
	if err := json.Unmarshal(b, &arr); err != nil {
		return fmt.Errorf("group_by: etiket adı listesi ya da virgüllü metin olmalı: %w", err)
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	*l = out
	return nil
}

func mtSplit(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// mtWindow — from_iso+to_iso (ikisi birden, RFC3339) YA DA range_s (çıpalı
// rangeWindow). max aşılırsa argüman hatası: sessiz kırpma pencereyi modelin
// sandığından farklı yapar. Sonuç UTC.
func mtWindow(ctx context.Context, rangeS int, fromISO, toISO string, max time.Duration) (time.Time, time.Time, error) {
	fromISO, toISO = strings.TrimSpace(fromISO), strings.TrimSpace(toISO)
	if fromISO == "" && toISO == "" {
		from, to := rangeWindow(ctx, rangeS)
		return from.UTC(), to.UTC(), nil
	}
	if fromISO == "" || toISO == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("pencere geçersiz: from_iso ve to_iso birlikte verilmeli (ya da yalnız range_s)")
	}
	from, err := time.Parse(time.RFC3339, fromISO)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from_iso geçersiz: RFC3339 olmalı (ör. 2026-09-26T10:00:00Z): %q", fromISO)
	}
	to, err := time.Parse(time.RFC3339, toISO)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("to_iso geçersiz: RFC3339 olmalı (ör. 2026-09-26T11:00:00Z): %q", toISO)
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("pencere geçersiz: to_iso from_iso'dan sonra olmalı")
	}
	if to.Sub(from) > max {
		return time.Time{}, time.Time{}, fmt.Errorf("pencere geçersiz: en fazla %s olmalı (istenen %s)", max, to.Sub(from))
	}
	return from.UTC(), to.UTC(), nil
}

// mtIsTraceIDLabel — trace/span kimliği biçimli etiket adı mı: harf/rakam
// dışı atılır, küçük harfe çevrilir; "traceid"/"spanid" ile biten her yazım
// (trace_id, traceId, TraceId, trace.id, otel_trace_id, parent_span_id).
func mtIsTraceIDLabel(k string) bool {
	n := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, k)
	return strings.HasSuffix(n, "traceid") || strings.HasSuffix(n, "spanid")
}

func mtTraceLabelErr(k string) error {
	return fmt.Errorf("etiket %q geçersiz: trace/span kimliği metrik etiketi değildir — metrikten "+
		"trace'e geçmek için get_exemplar_traces kullan", k)
}

// mtValidateArgs — kaynağa gitmeden önceki doğrulama (SAF).
func mtValidateArgs(labels map[string]string, groupBy []string) error {
	if len(labels) > mtMaxLabels {
		return fmt.Errorf("labels en fazla %d etiket olmalı (geçersiz: %d)", mtMaxLabels, len(labels))
	}
	if len(groupBy) > mtMaxLabels {
		return fmt.Errorf("group_by en fazla %d etiket olmalı (geçersiz: %d)", mtMaxLabels, len(groupBy))
	}
	for _, k := range mtSortedKeys(labels) {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("labels: boş etiket adı geçersiz")
		}
		if mtIsTraceIDLabel(k) {
			return mtTraceLabelErr(k)
		}
		if strings.TrimSpace(labels[k]) == "" {
			return fmt.Errorf("labels[%q] geçersiz: değer boş (tam eşleşme değeri ver)", k)
		}
	}
	for _, g := range groupBy {
		if mtIsTraceIDLabel(g) {
			return mtTraceLabelErr(g)
		}
	}
	return nil
}

func mtSortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// mtEffectiveStep — v0.10.944 — step_s iki yönde sınırlanır: ≤120 nokta için
// YUKARI, pencere uzunluğuna AŞAĞI (VM'de step rollup penceresidir —
// rate(x[<step>s]); pencereden büyük step tüm retention'ı tarar ve tek
// örnek start'ta düşer → boş seri). 0 = otomatik (MaxDataPoints=mtStepBudget).
// Taban ceil(pencere/118): pencere/120'ye yükseltilen adım hizayla 121–122
// kova üretir ve sıradan bir sorgu "truncated" okunurdu.
func mtEffectiveStep(from, to time.Time, stepS int) (step int, raised, lowered bool) {
	if stepS <= 0 {
		return 0, false, false
	}
	win := int(math.Ceil(to.Sub(from).Seconds()))
	if stepS > win {
		return win, false, true
	}
	min := int(math.Ceil(float64(win) / float64(mtStepBudget)))
	if stepS < min {
		return min, true, false
	}
	return stepS, false, false
}

// ── Sonuç şekli ─────────────────────────────────────────────────────────

type mtPoint struct {
	TISO string  `json:"t_iso"` // kova BAŞLANGICI, UTC
	V    float64 `json:"v"`
}

type mtSeries struct {
	Labels map[string]string `json:"labels"`
	Points []mtPoint         `json:"points"`
}

// mtRound — v0.10.944 — en az 6 anlamlı basamak (0.1234567891 gibi kesir
// basamakları bütçe yer, kanıt değildir) AMA tam sayı basamakları ASLA
// kırpılmaz: epoch-saniye gauge'ları (process_start_time_seconds) ve 10^6
// üstü sayaçlar 6 basamakta saatlerce / binlerce birim aynı görünürdü.
func mtRound(v float64) float64 {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	digits := 6
	if a := math.Abs(v); a >= 1e6 {
		digits = int(math.Floor(math.Log10(a))) + 1
		if digits > 17 {
			digits = 17
		}
	}
	r, err := strconv.ParseFloat(strconv.FormatFloat(v, 'g', digits, 64), 64)
	if err != nil {
		return v
	}
	return r
}

func mtISO(ns int64) string { return time.Unix(0, ns).UTC().Format(time.RFC3339) }

// mtShapeSeries — seri tavanı (alan büyükten küçüğe; eşitlikte etiket
// metni) + nokta seyreltme + etiket nesnesi. SAF. total = tavandan önceki
// seri sayısı; pointsCut = en az bir seri 120 noktadan fazlaydı.
func mtShapeSeries(in []chstore.SpanMetricSeries, groupBy []string, maxSeries, maxPoints int) (out []mtSeries, total int, pointsCut bool) {
	type ranked struct {
		s     chstore.SpanMetricSeries
		area  float64
		label string
	}
	rs := make([]ranked, 0, len(in))
	for _, s := range in {
		a := 0.0
		for _, p := range s.Points {
			a += math.Abs(p.Value)
		}
		rs = append(rs, ranked{s: s, area: a, label: strings.Join(s.GroupKey, "\x1f")})
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].area != rs[j].area {
			return rs[i].area > rs[j].area
		}
		return rs[i].label < rs[j].label
	})
	total = len(rs)
	if len(rs) > maxSeries {
		rs = rs[:maxSeries]
	}
	out = make([]mtSeries, 0, len(rs))
	for _, r := range rs {
		labels := make(map[string]string, len(groupBy))
		for i, g := range groupBy {
			v := ""
			if i < len(r.s.GroupKey) {
				v = r.s.GroupKey[i]
			}
			labels[g] = v
		}
		pts := r.s.Points
		if len(pts) > maxPoints {
			pointsCut = true
			pts = thinEvery(pts, maxPoints)
		}
		mp := make([]mtPoint, 0, len(pts))
		for _, p := range pts {
			mp = append(mp, mtPoint{TISO: mtISO(p.Time), V: mtRound(p.Value)})
		}
		out = append(out, mtSeries{Labels: labels, Points: mp})
	}
	return out, total, pointsCut
}

// mtDelayed — gecikme kuralı (spec): pencere son 10 dk içinde bitiyor VE her
// serinin en yeni örneği window_end − max(2×step, 120 s)'den eski. Noktalar
// kova BAŞLANGICIYLA damgalı; örnek anı = başlangıç + step. `now` sıfırsa
// (çıpalı pencere, duvar saati bilinmiyor) karar verilmez. SAF.
func mtDelayed(series []chstore.SpanMetricSeries, windowEnd time.Time, stepSec int, now time.Time) (bool, time.Time) {
	if now.IsZero() || len(series) == 0 || now.Sub(windowEnd) > mtDelayRecent {
		return false, time.Time{}
	}
	// Geleceğe uzanan pencere (to_iso > şimdi): henüz var olamayacak veri
	// "gecikmiş" sayılmaz — eşik şimdiye göre.
	if windowEnd.After(now) {
		windowEnd = now
	}
	step := time.Duration(stepSec) * time.Second
	var newest time.Time
	for _, s := range series {
		if len(s.Points) == 0 {
			continue
		}
		t := time.Unix(0, s.Points[len(s.Points)-1].Time).Add(step)
		if t.After(newest) {
			newest = t
		}
	}
	if newest.IsZero() {
		return false, time.Time{}
	}
	slack := 2 * step
	if slack < mtDelayMinSlack {
		slack = mtDelayMinSlack
	}
	return newest.Before(windowEnd.Add(-slack)), newest.UTC()
}

// mtStepFromPoints — ClickHouse yolu adımı bilmez (otomatik merdiven);
// ardışık noktaların en küçük pozitif farkından kestirilir.
func mtStepFromPoints(series []chstore.SpanMetricSeries, from, to time.Time) int {
	best := int64(0)
	for _, s := range series {
		for i := 1; i < len(s.Points); i++ {
			if d := s.Points[i].Time - s.Points[i-1].Time; d > 0 && (best == 0 || d < best) {
				best = d
			}
		}
	}
	if best > 0 {
		return int(best / int64(time.Second))
	}
	return int(math.Ceil(to.Sub(from).Seconds() / float64(mtMaxPoints)))
}

func mtWindowMap(from, to time.Time) map[string]string {
	return map[string]string{"from_iso": from.UTC().Format(time.RFC3339), "to_iso": to.UTC().Format(time.RFC3339)}
}

// mtCHRoleKeys — ClickHouse metric_points süzgeç anahtarları (filterexpr.go
// metricPointsWellKnown + resource.* dizileri). Servis kolon kısayoludur
// (MetricQueryFilter.Service).
var mtCHRoleKeys = map[string]string{
	vmetrics.RoleService:   "service_name",
	vmetrics.RoleEnv:       "deployment.environment",
	vmetrics.RoleCluster:   "cluster",
	vmetrics.RoleNamespace: "resource.k8s.namespace.name",
	vmetrics.RolePod:       "resource.k8s.pod.name",
	vmetrics.RoleVersion:   "resource.service.version",
}

// mtCHColumnKeys — v0.10.944: list_metric_labels notunda ilan edilen
// metric_points kolon anahtarları (mtCHWellKnownKey'in kabul ettikleri).
var mtCHColumnKeys = []string{"service.name", "host.name", "deployment.environment", "cluster"}

// mtCHWellKnownKey — SAF (v0.10.944): metric_points'in kolon-destekli iyi
// bilinen anahtarları (chstore.IsMetricPointsWellKnownKey) CH keşif listesinde
// görünmez (MetricAttrKeys yalnız veri noktası öznitelikleri) ve strict modda
// bad_args alıyordu — host gruplamasının rol takma adı da yok. Eşanlamlılar
// TEK kanonik anahtara iner: `used` çakışma denetimi service=checkout +
// labels{service.name:payments}'i iki çelişik süzgeç (sessiz boş sonuç) yerine
// çakışma hatası olarak yakalar.
func mtCHWellKnownKey(k string) (string, bool) {
	switch k {
	case "service.name", "service_name", "service":
		return "service_name", true
	case "deployment.environment.name", "deployment.environment":
		return "deployment.environment", true // ikisi de aynı metricEnvExpr
	}
	if chstore.IsMetricPointsWellKnownKey(k) {
		return k, true // host.name, host_name, cluster
	}
	return "", false
}

func mtCHMapping() map[string]string {
	out := make(map[string]string, len(vmetrics.LabelRoles))
	for _, r := range vmetrics.LabelRoles {
		out[r] = mtCHRoleKeys[r] + " (" + vmetrics.SourceConvention + ")"
	}
	return out
}

// mtResolveRoles — v0.10.944 — keşif kümesinden rol çözümü. Keşif KISMİYSA
// (VM isPartial) çözülemeyen rol keşifsiz kurala düşer: servis konvansiyonu
// (service_name) görülmemiş olabilir ama geçerlidir; diğer roller none kalır
// ve not "görülmemiş olabilir" der ("aday yok" değil).
func mtResolveRoles(lm vmetrics.LabelMap, present map[string]bool, discPartial bool) []vmetrics.LabelResolution {
	out := make([]vmetrics.LabelResolution, 0, len(vmetrics.LabelRoles))
	for _, role := range vmetrics.LabelRoles {
		r := vmetrics.ResolveLabelRole(role, lm, present, true)
		if !r.Applied() && discPartial {
			r = vmetrics.ResolveLabelRole(role, lm, present, false)
		}
		out = append(out, r)
	}
	return out
}

func mtMappingOf(res []vmetrics.LabelResolution) map[string]string {
	out := make(map[string]string, len(res))
	for _, r := range res {
		out[r.Role] = r.String()
	}
	return out
}

func mtNoneMapping() map[string]string {
	out := make(map[string]string, len(vmetrics.LabelRoles))
	for _, r := range vmetrics.LabelRoles {
		out[r] = vmetrics.SourceNone
	}
	return out
}

// mtResolveLabel — bir labels/group_by anahtarının gerçek etiketi: (1) aynen
// kümede, (2) rol adı (service/env/…) ve rol çözülmüş, (3) promLabel yazımı
// (http.route → http_route) kümede. Bulunamazsa "".
func mtResolveLabel(key string, present map[string]bool, roles map[string]vmetrics.LabelResolution) string {
	k := strings.TrimSpace(key)
	if present[k] {
		return k
	}
	if r, ok := roles[k]; ok && r.Applied() {
		return r.Label
	}
	if l := vmetrics.LabelName(k); present[l] {
		return l
	}
	return ""
}

func mtUnknownLabelErr(unknown []string, present []string) error {
	shown := present
	more := ""
	if len(shown) > 30 {
		shown, more = shown[:30], fmt.Sprintf(" … +%d", len(present)-30)
	}
	return fmt.Errorf("etiket geçersiz: %s bu metrikte yok (pencerede görülen etiketler: %s%s) — "+
		"list_metric_labels ile doğrula", strings.Join(unknown, ", "), strings.Join(shown, ", "), more)
}

// ── query_metric ────────────────────────────────────────────────────────

type queryMetricInvArgs struct {
	Name        string            `json:"name"`
	Service     string            `json:"service,omitempty"`
	Env         string            `json:"env,omitempty"`
	Cluster     string            `json:"cluster,omitempty"`
	Namespace   string            `json:"namespace,omitempty"`
	Pod         string            `json:"pod,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Aggregation string            `json:"aggregation,omitempty"`
	GroupBy     mtStringList      `json:"group_by,omitempty"`
	RangeS      int               `json:"range_s,omitempty"`
	FromISO     string            `json:"from_iso,omitempty"`
	ToISO       string            `json:"to_iso,omitempty"`
	StepS       int               `json:"step_s,omitempty"`
}

func (a queryMetricInvArgs) roleValues() map[string]string {
	return map[string]string{
		vmetrics.RoleService:   strings.TrimSpace(a.Service),
		vmetrics.RoleEnv:       strings.TrimSpace(a.Env),
		vmetrics.RoleCluster:   strings.TrimSpace(a.Cluster),
		vmetrics.RoleNamespace: strings.TrimSpace(a.Namespace),
		vmetrics.RolePod:       strings.TrimSpace(a.Pod),
	}
}

// mtQuery — bir query_metric çağrısının çözülmüş hâli.
type mtQuery struct {
	name, agg string
	from, to  time.Time
	step      int
	raised    bool
	lowered   bool // v0.10.944 — step_s pencere uzunluğuna indirildi
	roles     map[string]string
	labels    map[string]string
	groupBy   []string
}

// queryMetricInvestigateTool — v0.10.944 yükseltilmiş query_metric. Entegratör
// tools.go'daki eski queryMetricTool'un yerine ToolList'e bunu koyar (ad
// "query_metric" korunur).
func queryMetricInvestigateTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             "query_metric",
		ShortDescription: "Metrik serisi (VM/CH, ≤20×120); süzgeç gerçek etikete eşlenir, eşlenemeyen partial.",
		Description: "Query one metric as time series from the live metric backend (VictoriaMetrics when the operator " +
			"made it the metric store, else ClickHouse metric_points) and get at most 20 series x 120 points " +
			"{t_iso (UTC bucket START), v}, one series per group_by combination (largest first). " +
			"Context args service/env/cluster/namespace/pod are mapped to REAL labels — operator LabelMap first, then " +
			"convention (service_name), then live label discovery — and `mapping` reports each role as " +
			"'<label> (configured|convention|discovered|none)'; 'discovered; ayrıca: x' means other spellings exist and " +
			"only the first was filtered (partial). service on service_name is an exact match; on an identity label " +
			"such as job/name it is matched with ^(.*/)?<service>$ (the namespace prefix is tolerated; an env-suffixed " +
			"name also matches its unsuffixed form). A context arg that cannot be mapped is NOT applied: " +
			"source.state becomes partial and a note says so — never read those series as filtered (e.g. env " +
			"unapplied = all environments). `labels` (exact match, max 8) and `group_by` must be real label names of " +
			"this metric (list_metric_labels; if VictoriaMetrics reports label discovery isPartial, unseen names are " +
			"applied unverified and the result is partial); trace_id/span_id are never metric labels — for metric-to-trace use " +
			"get_exemplar_traces (ClickHouse exemplars; VictoriaMetrics exemplars are not read, so this tool makes no " +
			"exemplar claim). Window: range_s (default 1800) or from_iso+to_iso (RFC3339, both), max 7 days. " +
			"aggregation: avg for gauges, sum/min/max/last, p50/p95/p99 for histograms (on VictoriaMetrics a " +
			"percentile needs a service or label filter). `source` always says what the result proves: " +
			"ok | empty (no series for this filter — NOT 'no problem') | partial | truncated | delayed (newest " +
			"sample older than the window end) | unreachable | unauthorized | timeout | not_configured. " +
			"Values keep all integer digits; fractions are rounded to 6 significant digits. `unit` is omitted when " +
			"values are counts (e.g. *_count, *_bucket without a percentile). " +
			"Take the metric name from list_metric_names.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":      map[string]any{"type": "string", "description": "Metric name exactly as list_metric_names returned it."},
				"service":   map[string]any{"type": "string", "description": "Exact service name; mapped to the backend's service label (see mapping.service)."},
				"env":       map[string]any{"type": "string", "description": "Environment value as stored (e.g. prod, uat, int, prep); applied only if an env label maps, else partial."},
				"cluster":   map[string]any{"type": "string", "description": "Cluster value (e.g. cluster-a); mapped like env."},
				"namespace": map[string]any{"type": "string", "description": "Kubernetes namespace; mapped like env."},
				"pod":       map[string]any{"type": "string", "description": "Pod name; mapped like env."},
				"labels": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
					"description":          "Extra exact-match label filters {label: value}, max 8, real label names only (list_metric_labels). No trace_id/span_id.",
				},
				"aggregation": map[string]any{"type": "string", "enum": mtAggregations, "description": "Default 'avg'."},
				"group_by": map[string]any{
					"type": "array", "items": map[string]any{"type": "string"}, "maxItems": mtMaxLabels,
					"description": "Real label names to split by (e.g. [\"k8s_pod_name\"]); role names (pod, namespace…) resolve through mapping. Comma-separated string also accepted.",
				},
				"range_s":  map[string]any{"type": "integer", "minimum": 0, "maximum": 604800, "description": "Lookback seconds ending at the conversation anchor. Default 1800. Ignored when from_iso/to_iso are given."},
				"from_iso": map[string]any{"type": "string", "description": "Absolute window start, RFC3339 UTC (with to_iso)."},
				"to_iso":   map[string]any{"type": "string", "description": "Absolute window end, RFC3339 UTC (with from_iso)."},
				"step_s":   map[string]any{"type": "integer", "minimum": 0, "maximum": 604800, "description": "Bucket seconds. 0 = auto (~120 points); capped at the window length; raised if it would exceed 120 points."},
			},
			"required": []string{"name"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a queryMetricInvArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			q, err := mtParseQueryArgs(ctx, a)
			if err != nil {
				return nil, err
			}
			src := mtSource(d)
			if src == nil {
				st := sourcestate.FromError(mtSourceMetrics, "", sourcestate.ErrNotConfigured).WithWindow(q.from, q.to)
				return mtQueryResult(q, nil, 0, mtNoneMapping(), nil, st, "", ""), nil
			}
			if disc, det, ok := mtVMCapable(src); ok {
				return mtQueryVM(ctx, src, disc, det, q)
			}
			return mtQueryCH(ctx, src, q)
		},
	}
}

// mtParseQueryArgs — kaynağa gitmeden önceki tüm argüman kararları.
func mtParseQueryArgs(ctx context.Context, a queryMetricInvArgs) (mtQuery, error) {
	q := mtQuery{name: strings.TrimSpace(a.Name), agg: strings.ToLower(strings.TrimSpace(a.Aggregation))}
	if q.name == "" {
		return q, fmt.Errorf("name is required (list_metric_names)")
	}
	if q.agg == "" {
		q.agg = "avg"
	}
	okAgg := false
	for _, v := range mtAggregations {
		if v == q.agg {
			okAgg = true
		}
	}
	if !okAgg {
		return q, fmt.Errorf("aggregation %q geçersiz — şunlardan biri olmalı: %s", a.Aggregation, strings.Join(mtAggregations, ", "))
	}
	if err := mtValidateArgs(a.Labels, a.GroupBy); err != nil {
		return q, err
	}
	from, to, err := mtWindow(ctx, a.RangeS, a.FromISO, a.ToISO, mtMaxWindow)
	if err != nil {
		return q, err
	}
	q.from, q.to = from, to
	q.step, q.raised, q.lowered = mtEffectiveStep(from, to, a.StepS)
	q.roles = a.roleValues()
	q.labels = map[string]string{}
	for k, v := range a.Labels {
		q.labels[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	q.groupBy = []string(a.GroupBy)
	return q, nil
}

// mtQueryOut — v0.10.944 — query_metric sonucu, SIRALI. map[string]any'de
// json.Marshal anahtarları sıralar ve iri `series` dizisi `source`'tan ÖNCE
// yazılırdı; iki tüketici de sonucu BAŞTAN keser (model 6000 rune, adım
// önizlemesi/durum rozeti 4 KB) → partial/delayed/truncated/uygulanamayan
// süzgeç ne modele ne rozete ulaşıyordu. Durum önce, veri en sonda.
type mtQueryOut struct {
	Metric         string             `json:"metric"`
	Aggregation    string             `json:"aggregation"`
	Source         sourcestate.Status `json:"source"`
	Mapping        map[string]string  `json:"mapping"`
	AppliedFilters []string           `json:"applied_filters"`
	Window         map[string]string  `json:"window"`
	StepS          int                `json:"step_s,omitempty"`
	Unit           string             `json:"unit,omitempty"`
	Note           string             `json:"note,omitempty"`
	Count          int                `json:"count"`
	TotalSeries    int                `json:"total_series"`
	Series         []mtSeries         `json:"series"`
}

func mtQueryResult(q mtQuery, series []mtSeries, total int, mapping map[string]string, applied []string, st sourcestate.Status, unit, note string) mtQueryOut {
	if series == nil {
		series = []mtSeries{}
	}
	if applied == nil {
		applied = []string{}
	}
	return mtQueryOut{
		Metric: q.name, Aggregation: q.agg, Source: st, Mapping: mapping,
		AppliedFilters: applied, Window: mtWindowMap(q.from, q.to),
		StepS: q.step, Unit: unit, Note: note,
		Count: len(series), TotalSeries: total, Series: series,
	}
}

// mtIsRefusal — VM çevirisinin İSTEK reddi (filtresiz kova taraması ya da
// ifade edilemeyen sorgu): kaynak sağlıklı, argüman değişmeli → bad_args.
func mtIsRefusal(err error) bool {
	return errors.Is(err, vmetrics.ErrUnfilteredBuckets) || errors.Is(err, vmetrics.ErrUnsupported)
}

// mtCancelled — çağıran vazgeçti mi (kalan sorgular çalıştırılmaz).
func mtCancelled(ctx context.Context, err error) bool {
	return sourcestate.IsCancelled(err) || errors.Is(ctx.Err(), context.Canceled)
}

// mtQueryVM — VictoriaMetrics yolu: keşif → eşleme → doğrulama → bayraklı sorgu.
func mtQueryVM(ctx context.Context, src MetricSource, disc metricLabelDiscoverer, det metricDetailQuerier, q mtQuery) (any, error) {
	backend := mtBackend(src)
	lm := mtLabelMap(src)

	dFrom := q.from
	if q.to.Sub(dFrom) < mtDiscoverFloor {
		dFrom = q.to.Add(-mtDiscoverFloor)
	}
	dctx, cancel := context.WithTimeout(ctx, mtDiscoverBudget)
	present, discPartial, err := disc.MetricLabelNamesIn(dctx, q.name, dFrom, q.to)
	cancel()
	// v0.10.944 — keşif yalnız doğrulama ve eşleme içindir; hatası (labels-API
	// seri/süre limiti, vmauth rotası, 5 s bütçe — binlerce servisli filoda en
	// çok yüksek kardinaliteli metriklerde) query_range'i ENGELLEMEZ. İptal
	// dışında her hata kısmi keşif gibi ele alınır: eşleme konvansiyon/
	// yapılandırmayla yapılır, görülmeyen etiketler doğrulanmadan uygulanır ve
	// bu SÖYLENİR. Hata/zaman aşımı durumu yalnız sorgunun kendisinden gelir.
	var discClass sourcestate.State
	if err != nil {
		if mtCancelled(ctx, err) {
			return nil, err
		}
		discClass = sourcestate.Classify(err)
		present, discPartial = nil, true
	}
	discFailed := discClass != ""
	presentSet := make(map[string]bool, len(present))
	for _, p := range present {
		presentSet[p] = true
	}
	res := mtResolveRoles(lm, presentSet, discPartial)
	mapping := mtMappingOf(res)
	// v0.10.944 — boş küme ancak keşif TAM ise "seri yok" kanıtıdır; isPartial
	// bir boş liste yalnız "cevap veren düğümlerde görülmedi" demek.
	if len(present) == 0 && !discPartial {
		st := sourcestate.Result(mtSourceMetrics, backend, sourcestate.Outcome{Limit: mtMaxSeries}).WithWindow(q.from, q.to)
		st.Notes = append(st.Notes, "metrik bu pencerede hiç seri taşımıyor — adı list_metric_names ile doğrula ya da pencereyi genişlet")
		return mtQueryResult(q, nil, 0, mapping, nil, st, "", ""), nil
	}
	byRole := make(map[string]vmetrics.LabelResolution, len(res))
	for _, r := range res {
		byRole[r.Role] = r
	}
	convService := vmetrics.LabelName("service.name")

	var (
		filters    []chstore.FilterExpr
		applied    []string
		notes      []string
		partial    bool
		used       = map[string]string{} // etiket → değer (çelişki denetimi)
		regexUsed  = map[string]bool{}   // v0.10.944 — used[etiket] bir =~ deseni (tam değer değil)
		unverified []string              // v0.10.944 — kısmi keşifte görülmeden uygulanan etiketler
		seenUnver  = map[string]bool{}
	)
	for _, role := range vmetrics.LabelRoles {
		v := q.roles[role]
		if v == "" {
			continue
		}
		r := byRole[role]
		if !r.Applied() {
			partial = true
			why := fmt.Sprintf("LabelMap.%s yapılandırılmamış, bu metrikte aday etiket yok", role)
			if discFailed {
				why = fmt.Sprintf("LabelMap.%s yapılandırılmamış; etiket keşfi başarısız (%s)", role, discClass)
			} else if discPartial {
				why = fmt.Sprintf("LabelMap.%s yapılandırılmamış; etiket keşfi kısmi (isPartial) — görülmemiş olabilir", role)
			}
			notes = append(notes, fmt.Sprintf("%s süzgeci uygulanamadı: etiket bulunamadı (%s) — seriler bu boyutta SÜZÜLMEDİ", role, why))
			continue
		}
		// v0.10.944 — birden çok aday yazım: MetricsQL iki etiket ADINI tek
		// eşleştiricide VEYA'layamaz; yalnız ilki süzülür ve bu SÖYLENİR.
		if len(r.Alternatives) > 0 {
			partial = true
			notes = append(notes, fmt.Sprintf("%s birden çok etikette: %s, %s — yalnız %s süzüldü; diğer yazımı taşıyan seriler dışarıda kalmış olabilir (LabelMap.%s ile sabitle)",
				role, r.Label, strings.Join(r.Alternatives, ", "), r.Label, role))
		}
		if r.Source == vmetrics.SourceConfigured && !r.Present && !discFailed { // v0.10.944 — keşif düştüyse "görülmedi" kanıt değil
			notes = append(notes, fmt.Sprintf("%s etiketi %s (configured) bu metrikte pencere içinde görülmedi — sonuç boş olabilir", role, r.Label))
		}
		// v0.10.944 — servis bir KİMLİK etiketine (job/name/service/k8s_*;
		// ya da service_name dışında yapılandırılmış bir etikete) eşlendiyse
		// değer biçimi bilinmiyor: job çoğu kurulumda "<namespace>/<servis>",
		// name ortam ekisiz (job_service.go). Tam `=` bu kurulumlarda sessizce
		// boş döner; servis→metrik eşleyicisinin deseni (JobServiceRegex)
		// kullanılır. service_name (konvansiyon) değer olarak servis adıdır, tam.
		if role == vmetrics.RoleService && r.Label != convService {
			pat := chstore.JobServiceRegex(v)
			filters = append(filters, chstore.FilterExpr{Key: r.Label, Op: "=~", Values: []string{pat}})
			applied = append(applied, r.Label+"=~"+strconv.Quote(pat))
			notes = append(notes, fmt.Sprintf("service %s etiketiyle eşlendi (%s): değer biçimi doğrulanmadı — '<namespace>/%s' öneki ve ortam eki soyulmuş hâli de eşlenir (servis→metrik eşleyicisiyle aynı desen)", r.Label, r.Source, v))
			if st := chstore.StripEnvSuffix(v); st != v && q.roles[vmetrics.RoleEnv] == "" {
				partial = true
				notes = append(notes, "ortam eki soyulmuş ad ("+st+") diğer ortamların serilerini de kapsayabilir — env ile süz")
			}
			used[r.Label] = v
			regexUsed[r.Label] = true
			continue
		}
		filters = append(filters, chstore.FilterExpr{Key: r.Label, Op: "=", Values: []string{v}})
		applied = append(applied, r.Label+"="+strconv.Quote(v))
		used[r.Label] = v
	}

	// resolve — labels/group_by anahtarının gerçek etiketi. Keşif KISMİYSA
	// (isPartial) görülmeyen ad reddedilmez: promLabel yazımı doğrulanmadan
	// uygulanır ve not düşülür; yalnız etiket DİLBİLGİSİNE uymayan ad reddedilir.
	var unknown []string
	resolve := func(k string) (string, error) {
		if l := mtResolveLabel(k, presentSet, byRole); l != "" {
			return l, nil
		}
		if !discPartial {
			unknown = append(unknown, k)
			return "", nil
		}
		l := vmetrics.LabelName(k)
		if !vmetrics.ValidLabelName(l) {
			return "", fmt.Errorf("etiket %q geçersiz: VictoriaMetrics etiket adı [a-zA-Z_][a-zA-Z0-9_]* olmalı", k)
		}
		if !seenUnver[l] {
			seenUnver[l] = true
			unverified = append(unverified, l)
		}
		return l, nil
	}
	for _, k := range mtSortedKeys(q.labels) {
		l, err := resolve(k)
		if err != nil {
			return nil, err
		}
		if l == "" {
			continue
		}
		v := q.labels[k]
		// Tam değer yalnız tam değerle karşılaştırılır; servis =~ deseniyle
		// aynı etikete verilen tam değer İKİNCİ bir koşul olarak eklenir
		// (ör. job="shop/checkout" deseni daraltır) — sessizce atlanmaz.
		if prev, ok := used[l]; ok && !regexUsed[l] {
			if prev != v {
				return nil, fmt.Errorf("labels[%q]=%q geçersiz: aynı etiket (%s) başka bir argümanla %q olarak süzülüyor", k, v, l, prev)
			}
			continue
		}
		filters = append(filters, chstore.FilterExpr{Key: l, Op: "=", Values: []string{v}})
		applied = append(applied, l+"="+strconv.Quote(v))
		used[l] = v
		regexUsed[l] = false
	}
	var groupBy []string
	seenGB := map[string]bool{}
	for _, g := range q.groupBy {
		l, err := resolve(g)
		if err != nil {
			return nil, err
		}
		if l == "" {
			continue
		}
		if !seenGB[l] {
			seenGB[l] = true
			groupBy = append(groupBy, l)
		}
	}
	if len(unknown) > 0 {
		return nil, mtUnknownLabelErr(unknown, present)
	}
	if discFailed {
		n := fmt.Sprintf("etiket keşfi başarısız (%s) — eşleme konvansiyon/yapılandırmayla yapıldı", discClass)
		if len(unverified) > 0 {
			n += "; " + strings.Join(unverified, ", ") + " doğrulanmadan uygulandı"
		}
		notes = append(notes, n)
	} else if discPartial {
		if len(unverified) > 0 {
			notes = append(notes, "etiket keşfi kısmi (VictoriaMetrics isPartial=true): "+strings.Join(unverified, ", ")+" doğrulanamadı, olduğu gibi uygulandı")
		} else {
			notes = append(notes, "etiket keşfi kısmi (VictoriaMetrics isPartial=true): etiket kümesi eksik olabilir — eşleme tam doğrulanamadı")
		}
	}
	q.groupBy = groupBy

	// İptal = Go hatası, kalan sorgu çalışmaz. Süre bütçesi dolduysa
	// (DeadlineExceeded) sorgu hemen düşer ve timeout DURUMU olarak döner.
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}
	f := chstore.MetricQueryFilter{
		Name: q.name, Aggregation: q.agg, GroupBy: groupBy, Filters: filters,
		From: q.from, To: q.to, StepSeconds: q.step, MaxDataPoints: mtStepBudget,
	}
	qctx, qcancel := context.WithTimeout(ctx, mtQueryBudget)
	detail, err := det.QueryMetricDetailed(qctx, f)
	qcancel()
	if err != nil {
		if mtCancelled(ctx, err) {
			return nil, err
		}
		if mtIsRefusal(err) {
			return nil, fmt.Errorf("sorgu geçersiz (kapsam): %w", err)
		}
		st := sourcestate.FromError(mtSourceMetrics, backend, err).WithWindow(q.from, q.to)
		st.Notes = append(st.Notes, notes...)
		return mtQueryResult(q, nil, 0, mapping, applied, st, "", ""), nil
	}
	q.step = detail.Step
	series, total, pointsCut := mtShapeSeries(detail.Series, groupBy, mtMaxSeries, mtMaxPoints)
	delayed, newest := mtDelayed(detail.Series, q.to, detail.Step, detail.AnsweredAt)
	st := sourcestate.Result(mtSourceMetrics, backend, sourcestate.Outcome{
		Returned: len(series), Limit: mtMaxSeries,
		Partial:   partial || detail.Partial || discPartial,
		Truncated: detail.Truncated || total > mtMaxSeries || pointsCut,
		Delayed:   delayed,
	}).WithWindow(q.from, q.to)
	if detail.Partial {
		notes = append(notes, "VictoriaMetrics isPartial=true: bazı depolama düğümleri cevap vermedi — değerler eksik olabilir")
	}
	notes = append(notes, mtCapNotes(detail.Truncated, detail.TotalSeries, total, pointsCut, q.raised, q.lowered, q.step)...)
	if delayed {
		notes = append(notes, "veri gecikmiş: en yeni örnek "+newest.Format(time.RFC3339)+
			" — pencerenin sonu kaynakta henüz yok; son dakikaları 'düşüş' diye okuma")
	}
	st.Notes = append(st.Notes, notes...)
	return mtQueryResult(q, series, total, mapping, applied, st, vmetrics.ResultUnit(q.name, q.agg), detail.Note), nil
}

// mtCapNotes — tavan notları (iki yolda aynı metin).
func mtCapNotes(parseCap bool, backendTotal, total int, pointsCut, raised, lowered bool, step int) []string {
	var notes []string
	if parseCap {
		notes = append(notes, fmt.Sprintf("backend %d seri döndürdü; ayrıştırma tavanı 1000 — sıralama yalnız ilk 1000 üzerinden", backendTotal))
	}
	if total > mtMaxSeries {
		notes = append(notes, fmt.Sprintf("%d seriden en büyük %d'si (alan) döndü — group_by'ı daralt ya da labels ile süz", total, mtMaxSeries))
	}
	if pointsCut {
		notes = append(notes, fmt.Sprintf("noktalar %d'ye seyreltildi", mtMaxPoints))
	}
	if raised {
		notes = append(notes, fmt.Sprintf("step_s %d s'ye yükseltildi (%d nokta tavanı)", step, mtMaxPoints))
	}
	if lowered {
		notes = append(notes, fmt.Sprintf("step_s pencere uzunluğuna (%d s) indirildi", step))
	}
	return notes
}

// mtQueryCH — ClickHouse yolu (VM yapılandırılmamış): rol anahtarları sabit
// (metric_points süzgeç derleyicisi), etiket doğrulaması MetricAttrKeys ile
// mümkün olduğunda.
func mtQueryCH(ctx context.Context, src MetricSource, q mtQuery) (any, error) {
	backend := mtBackend(src)
	mapping := mtCHMapping()
	var notes []string

	// Etiket doğrulaması: CH yalnız veri noktası özniteliklerini listeler (tavan
	// 100) ve penceresi "şimdi"ye göre; çıpalı / geçmiş pencerede ya da tavanda
	// KESİN ret verilmez, not düşülür. resource.* anahtarları listede olmaz, geçer.
	var keys map[string]bool
	strict := false
	live := false
	if ak, ok := src.(metricAttrKeyReader); ok && (len(q.labels) > 0 || len(q.groupBy) > 0) {
		// v0.10.944 — MetricAttrKeys duvar saatinden GERİYE bakar ([şimdi−since, şimdi]);
		// pencere şimdide bitmiyorsa (geçmiş from_iso/to_iso) keşif başka bir aralığı
		// görür → kesin ret yok, yalnız "doğrulanamadı" notu.
		now := nowOrAnchor(ctx)
		live = anchorOf(ctx).IsZero() && now.Sub(q.to) <= mtDelayRecent
		since := q.to.Sub(q.from)
		if live {
			since = now.Sub(q.from) // pencerenin başı da keşfe girsin (≤ 7 g + 10 dk)
		}
		if since < mtDiscoverFloor {
			since = mtDiscoverFloor
		}
		dctx, cancel := context.WithTimeout(ctx, mtDiscoverBudget)
		list, err := ak.MetricAttrKeys(dctx, q.name, "", since)
		cancel()
		if err != nil && mtCancelled(ctx, err) {
			return nil, err
		}
		if err == nil {
			keys = make(map[string]bool, len(list))
			for _, k := range list {
				keys[k] = true
			}
			// Boş liste = metrik bu pencerede veri noktası taşımıyor (ya da
			// özniteliksiz); "etiket yok" diye reddetmek yanıltır.
			strict = live && len(list) > 0 && len(list) < 100
		}
	}
	chKey := func(k string) (string, bool) {
		k = strings.TrimSpace(k)
		if rk, ok := mtCHRoleKeys[k]; ok {
			return rk, true
		}
		if ck, ok := mtCHWellKnownKey(k); ok { // v0.10.944 — kolon-destekli anahtarlar
			return ck, true
		}
		if strings.HasPrefix(k, "resource.") || keys == nil || keys[k] {
			return k, true
		}
		return k, !strict
	}

	f := chstore.MetricQueryFilter{Name: q.name, Aggregation: q.agg, From: q.from, To: q.to, StepSeconds: q.step, MaxDataPoints: mtStepBudget}
	var applied, unknown, unseen []string
	used := map[string]string{}
	for _, role := range vmetrics.LabelRoles {
		v := q.roles[role]
		if v == "" {
			continue
		}
		k := mtCHRoleKeys[role]
		if role == vmetrics.RoleService {
			f.Service = v
		} else {
			f.Filters = append(f.Filters, chstore.FilterExpr{Key: k, Op: "=", Values: []string{v}})
		}
		applied = append(applied, k+"="+strconv.Quote(v))
		used[k] = v
	}
	for _, lk := range mtSortedKeys(q.labels) {
		k, ok := chKey(lk)
		if !ok {
			unknown = append(unknown, lk)
			continue
		}
		if keys != nil && !keys[k] && !strings.HasPrefix(k, "resource.") && mtCHRoleKeys[lk] == "" &&
			!chstore.IsMetricPointsWellKnownKey(k) { // v0.10.944 — kolon anahtarı keşifte görünmez, "görülmedi" değil
			unseen = append(unseen, k)
		}
		v := q.labels[lk]
		if prev, dup := used[k]; dup {
			if prev != v {
				return nil, fmt.Errorf("labels[%q]=%q geçersiz: aynı anahtar (%s) başka bir argümanla %q olarak süzülüyor", lk, v, k, prev)
			}
			continue
		}
		if k == "service_name" {
			f.Service = v
		} else {
			f.Filters = append(f.Filters, chstore.FilterExpr{Key: k, Op: "=", Values: []string{v}})
		}
		applied = append(applied, k+"="+strconv.Quote(v))
		used[k] = v
	}
	var groupBy []string
	for _, g := range q.groupBy {
		k, ok := chKey(g)
		if !ok {
			unknown = append(unknown, g)
			continue
		}
		groupBy = append(groupBy, k)
	}
	if len(unknown) > 0 {
		list := make([]string, 0, len(keys))
		for k := range keys {
			list = append(list, k)
		}
		sort.Strings(list)
		// v0.10.944 — CH'de listede olmayan ama geçerli anahtarlar da söylenir.
		return nil, fmt.Errorf("%w; ClickHouse'ta resource.* ve kolon anahtarları (%s) da geçerli",
			mtUnknownLabelErr(unknown, list), strings.Join(mtCHColumnKeys, ", "))
	}
	if len(unseen) > 0 {
		if live {
			notes = append(notes, "etiket son pencerede görülmedi (doğrulanamadı): "+strings.Join(unseen, ", "))
		} else {
			notes = append(notes, "ClickHouse etiket keşfi 'şimdi'ye göreli; pencere geçmişte (ya da çıpalı) olduğu için etiket doğrulanamadı, olduğu gibi uygulandı: "+strings.Join(unseen, ", "))
		}
	}
	f.GroupBy = groupBy
	q.groupBy = groupBy

	// İptal = Go hatası, kalan sorgu çalışmaz. Süre bütçesi dolduysa
	// (DeadlineExceeded) sorgu hemen düşer ve timeout DURUMU olarak döner.
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}
	qctx, qcancel := context.WithTimeout(ctx, mtQueryBudget)
	raw, err := src.QueryMetric(qctx, f)
	qcancel()
	if err != nil {
		if mtCancelled(ctx, err) {
			return nil, err
		}
		if mtIsRefusal(err) {
			return nil, fmt.Errorf("sorgu geçersiz (kapsam): %w", err)
		}
		st := sourcestate.FromError(mtSourceMetrics, backend, err).WithWindow(q.from, q.to)
		st.Notes = append(st.Notes, notes...)
		return mtQueryResult(q, nil, 0, mapping, applied, st, "", ""), nil
	}
	step := mtStepFromPoints(raw, q.from, q.to)
	if q.step == 0 {
		q.step = step
	}
	series, total, pointsCut := mtShapeSeries(raw, groupBy, mtMaxSeries, mtMaxPoints)
	// Duvar saati yalnız çıpasız pencerede bilinir (nowOrAnchor = şimdi);
	// çıpalı geçmiş pencerede gecikme kararı verilmez.
	var now time.Time
	if anchorOf(ctx).IsZero() {
		now = nowOrAnchor(ctx)
	}
	delayed, newest := mtDelayed(raw, q.to, step, now)
	st := sourcestate.Result(mtSourceMetrics, backend, sourcestate.Outcome{
		Returned: len(series), Limit: mtMaxSeries,
		Truncated: total > mtMaxSeries || pointsCut,
		Delayed:   delayed,
	}).WithWindow(q.from, q.to)
	notes = append(notes, mtCapNotes(false, 0, total, pointsCut, q.raised, q.lowered, q.step)...)
	if delayed {
		notes = append(notes, "veri gecikmiş: en yeni örnek "+newest.Format(time.RFC3339)+
			" — pencerenin sonu kaynakta henüz yok; son dakikaları 'düşüş' diye okuma")
	}
	st.Notes = append(st.Notes, notes...)
	return mtQueryResult(q, series, total, mapping, applied, st, "", ""), nil
}

// ── list_metric_labels ──────────────────────────────────────────────────

type listMetricLabelsArgs struct {
	Name   string `json:"name"`
	Label  string `json:"label,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	RangeS int    `json:"range_s,omitempty"`
}

// mtLabelsDefaultRange — keşif varsayılanı 24 saat: etiket kümesi pencereye
// göre değil metriğe göre sorulur; dar pencere seyrek etiketi "yok" gösterir.
const mtLabelsDefaultRange = 86400

// mtListLabelsOut — v0.10.944 — list_metric_labels sonucu, SIRALI (mtQueryOut
// gerekçesi): 200 etiket adı `source`'u baştan kesen tüketicilerin (4 KB
// önizleme, 6000 rune model bütçesi) dışına itiyordu. Durum önce, listeler
// en sonda; yapı her dönüş noktasında SONDA kurulur.
type mtListLabelsOut struct {
	Metric         string             `json:"metric"`
	Source         sourcestate.Status `json:"source"`
	Mapping        map[string]string  `json:"mapping"`
	Window         map[string]string  `json:"window"`
	AnchoredWindow map[string]string  `json:"anchored_window,omitempty"`
	Label          string             `json:"label,omitempty"`
	Count          int                `json:"count"`
	Total          int                `json:"total"`
	HasMore        bool               `json:"has_more"`
	ValuesHasMore  bool               `json:"values_has_more,omitempty"`
	Labels         []string           `json:"labels"`
	Values         []string           `json:"values,omitempty"`
	// ColumnKeys — v0.10.944: yalnız ClickHouse; `labels` listesinde görünmeyen
	// ama labels/group_by'da geçerli metric_points kolon anahtarları.
	ColumnKeys []string `json:"column_keys,omitempty"`
}

// mtReaderNow — v0.10.944 — ClickHouse etiket okuyucularının (MetricAttrKeys /
// MetricLabelValues) KENDİ saati: `since`i duvar saatinden geriye uygularlar.
// Pencere SEÇMEZ; okunan aralığı DOĞRU ilan etmek içindir (çıpasız context →
// nowOrAnchor = duvar saati; anchor kapısı ham time.Now()'u reddeder).
func mtReaderNow() time.Time { return nowOrAnchor(context.Background()).UTC() }

func listMetricLabelsTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             "list_metric_labels",
		ShortDescription: "Metriğin gerçek etiketleri/değerleri; etiket adı uydurma.",
		Description: "List the REAL label names a metric carries in the live metric backend (VictoriaMetrics /api/v1/labels, " +
			"or ClickHouse data-point attribute keys), optionally the values of one `label` (max `limit`, default 50, " +
			"max 100), plus `mapping`: which label each context role (service, env, cluster, namespace, pod, version) " +
			"maps to — '<label> (configured|convention|discovered|none)'; 'discovered; ayrıca: x' lists other spellings " +
			"also present (query_metric filters only the first and reports partial). Call it before query_metric when you need " +
			"labels/group_by, or to learn why a context filter was reported unapplied. Default window 24 h (range_s, " +
			"max 7 d); on VictoriaMetrics it ends at the conversation anchor, on ClickHouse the read is relative to now " +
			"and is widened to cover the anchor — `window` always states the range actually read. " +
			"`has_more` / `values_has_more` say when a list was cut. " +
			"`source` carries the backend state (ok | empty | partial | truncated | unreachable | unauthorized | timeout | " +
			"not_configured); partial means VictoriaMetrics answered isPartial (a list may be incomplete); empty means " +
			"the metric had no series in the window, not that it does not exist.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":    map[string]any{"type": "string", "description": "Metric name exactly as list_metric_names returned it."},
				"label":   map[string]any{"type": "string", "description": "Optional: return this label's values (real label name or a role name such as namespace)."},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Max values for `label`. Default 50."},
				"range_s": map[string]any{"type": "integer", "minimum": 0, "maximum": 604800, "description": "Discovery lookback seconds. Default 86400."},
			},
			"required": []string{"name"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a listMetricLabelsArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			name := strings.TrimSpace(a.Name)
			if name == "" {
				return nil, fmt.Errorf("name is required (list_metric_names)")
			}
			label := strings.TrimSpace(a.Label)
			limit := clampLimit(a.Limit, 50, 100)
			rangeS := a.RangeS
			if rangeS <= 0 {
				rangeS = mtLabelsDefaultRange
			}
			from, to := rangeWindow(ctx, rangeS)
			from, to = from.UTC(), to.UTC()

			src := mtSource(d)
			if src == nil {
				return mtListLabelsOut{
					Metric:  name,
					Source:  sourcestate.FromError(mtSourceMetrics, "", sourcestate.ErrNotConfigured).WithWindow(from, to),
					Mapping: mtNoneMapping(), Window: mtWindowMap(from, to), Labels: []string{},
				}, nil
			}
			backend := mtBackend(src)
			if disc, _, ok := mtVMCapable(src); ok {
				return mtListLabelsVM(ctx, src, disc, name, label, limit, from, to, backend)
			}
			return mtListLabelsCH(ctx, src, name, label, limit, from, to, backend)
		},
	}
}

func mtListLabelsVM(ctx context.Context, src MetricSource, disc metricLabelDiscoverer, name, label string, limit int, from, to time.Time, backend string) (any, error) {
	lm := mtLabelMap(src)
	window := mtWindowMap(from, to)
	dctx, cancel := context.WithTimeout(ctx, mtDiscoverBudget)
	present, namesPartial, err := disc.MetricLabelNamesIn(dctx, name, from, to)
	cancel()
	if err != nil {
		if mtCancelled(ctx, err) {
			return nil, err
		}
		return mtListLabelsOut{
			Metric:  name,
			Source:  sourcestate.FromError(mtSourceMetrics, backend, err).WithWindow(from, to),
			Mapping: mtMappingOf(vmetrics.ResolveLabelRoles(lm, nil, false)), Window: window, Labels: []string{},
		}, nil
	}
	set := make(map[string]bool, len(present))
	for _, p := range present {
		set[p] = true
	}
	res := mtResolveRoles(lm, set, namesPartial)
	mapping := mtMappingOf(res)
	total := len(present)
	shown := present
	if len(shown) > mtMaxLabelNames {
		shown = shown[:mtMaxLabelNames]
	}
	if shown == nil {
		shown = []string{}
	}
	var (
		lbl      string
		vals     []string
		valsMore bool
		notes    []string
	)
	// done — sonuç SONDA kurulur (sıralı yapı; durum önce).
	done := func(st sourcestate.Status) mtListLabelsOut {
		return mtListLabelsOut{
			Metric: name, Source: st, Mapping: mapping, Window: window, Label: lbl,
			Count: len(shown), Total: total, HasMore: total > len(shown), ValuesHasMore: valsMore,
			Labels: shown, Values: vals,
		}
	}
	// v0.10.944 — isPartial etiket listesi: bazı vmstorage düğümleri cevap
	// vermedi; liste eksik olabilir (ok DEĞİL, "yok" kanıtı da değil).
	if namesPartial {
		notes = append(notes, "VictoriaMetrics isPartial=true: bazı depolama düğümleri cevap vermedi — liste eksik olabilir")
	}
	outcome := sourcestate.Outcome{Returned: total, Limit: mtMaxLabelNames, Truncated: total > len(shown), Partial: namesPartial}
	if label != "" && (total > 0 || namesPartial) {
		byRole := make(map[string]vmetrics.LabelResolution, len(res))
		for _, r := range res {
			byRole[r.Role] = r
		}
		l := mtResolveLabel(label, set, byRole)
		if l == "" {
			if !namesPartial {
				return nil, mtUnknownLabelErr([]string{label}, present)
			}
			// Kısmi listede görülmemesi "yok" demek değil: promLabel yazımı
			// doğrulanmadan okunur, yalnız dilbilgisine uymayan ad reddedilir.
			l = vmetrics.LabelName(label)
			if !vmetrics.ValidLabelName(l) {
				return nil, fmt.Errorf("label %q geçersiz: VictoriaMetrics etiket adı [a-zA-Z_][a-zA-Z0-9_]* olmalı", label)
			}
			notes = append(notes, "etiket keşfi kısmi: "+l+" listede görülmedi, değerleri doğrulanmadan okundu")
		}
		vr, ok := src.(metricLabelValuesIn)
		if !ok {
			return nil, fmt.Errorf("label değerleri bu kaynakta okunamıyor")
		}
		vctx, vcancel := context.WithTimeout(ctx, mtDiscoverBudget)
		got, valsPartial, err := vr.MetricLabelValuesIn(vctx, name, l, from, to, limit+1)
		vcancel()
		lbl = l
		if err != nil {
			if mtCancelled(ctx, err) {
				return nil, err
			}
			st := sourcestate.FromError(mtSourceMetrics, backend, err).WithWindow(from, to)
			st.Notes = append(st.Notes, notes...)
			return done(st), nil
		}
		more := len(got) > limit
		if more {
			got = got[:limit]
		}
		if got == nil {
			got = []string{}
		}
		if valsPartial && !namesPartial {
			notes = append(notes, "VictoriaMetrics isPartial=true (etiket değerleri): bazı depolama düğümleri cevap vermedi — değer listesi eksik olabilir")
		}
		vals, valsMore = got, more
		// Değer dalı Outcome'u yeniden kurar — kısmi bayrağı (ad ya da değer)
		// düşmemeli.
		outcome = sourcestate.Outcome{Returned: len(got), Limit: limit, Truncated: more || outcome.Truncated,
			Partial: namesPartial || valsPartial}
	}
	st := sourcestate.Result(mtSourceMetrics, backend, outcome).WithWindow(from, to)
	if total == 0 && !namesPartial {
		st.Notes = append(st.Notes, "metrik bu pencerede hiç seri taşımıyor — adı list_metric_names ile doğrula ya da range_s'i büyüt")
	}
	st.Notes = append(st.Notes, notes...)
	return done(st), nil
}

func mtListLabelsCH(ctx context.Context, src MetricSource, name, label string, limit int, from, to time.Time, backend string) (any, error) {
	// v0.10.944 — CH okuyucuları (MetricAttrKeys/MetricLabelValues) "şimdi"ye
	// göreli; çıpalı pencerede okuma çıpalı başlangıçtan şimdiye genişler (7 g
	// tavan) ve window/source OKUNAN aralığı söyler (anchor.go: ilan = okunan).
	readTo := mtReaderNow()
	since := readTo.Sub(from)
	if since < to.Sub(from) {
		since = to.Sub(from)
	}
	anchored := !anchorOf(ctx).IsZero()
	// Çıpasız 7 g'lük pencere birkaç ms taşar (from rangeWindow'da kuruldu);
	// o taşma "kapsanmadı" sayılmaz — yalnız çıpalı pencere kapsam dışı kalabilir
	// ve şimdiye 10 dk'dan yakın çıpa (canlı sayılır) partial üretmez.
	uncovered := anchored && since > mtMaxWindow+mtDelayRecent
	if since > mtMaxWindow {
		since = mtMaxWindow
	}
	var anchoredWindow map[string]string
	if anchored {
		anchoredWindow = mtWindowMap(from, to)
	}
	from, to = readTo.Add(-since), readTo
	window := mtWindowMap(from, to)

	var (
		keys     = []string{}
		capped   bool
		lbl      string
		vals     []string
		valsMore bool
	)
	done := func(st sourcestate.Status) mtListLabelsOut {
		return mtListLabelsOut{
			Metric: name, Source: st, Mapping: mtCHMapping(), Window: window, AnchoredWindow: anchoredWindow,
			Label: lbl, Count: len(keys), Total: len(keys), HasMore: capped, ValuesHasMore: valsMore,
			Labels: keys, Values: vals, ColumnKeys: mtCHColumnKeys,
		}
	}
	ak, ok := src.(metricAttrKeyReader)
	if !ok {
		return done(sourcestate.FromError(mtSourceMetrics, backend, sourcestate.ErrNotConfigured).WithWindow(from, to)), nil
	}
	dctx, cancel := context.WithTimeout(ctx, mtDiscoverBudget)
	got, err := ak.MetricAttrKeys(dctx, name, "", since)
	cancel()
	if err != nil {
		if mtCancelled(ctx, err) {
			return nil, err
		}
		return done(sourcestate.FromError(mtSourceMetrics, backend, err).WithWindow(from, to)), nil
	}
	if got != nil {
		keys = got
	}
	// CH okuyucusu 100'de keser: tam 100 = alt sınır.
	capped = len(keys) >= 100
	outcome := sourcestate.Outcome{Returned: len(keys), Limit: 100, Truncated: capped}
	notes := []string{"ClickHouse: yalnız veri noktası öznitelikleri listelenir; resource.* anahtarları " +
		"(ör. resource.k8s.pod.name) listede yok ama süzgeçte kullanılabilir",
		// v0.10.944 — kolon-destekli anahtarlar keşifte görünmez; host gruplaması buradan bulunur.
		"ClickHouse kolon anahtarları (listede yok, labels/group_by'da geçerli): " + strings.Join(mtCHColumnKeys, ", ")}
	if anchored && !uncovered {
		notes = append(notes, "ClickHouse etiket okuyucusu 'şimdi'ye göreli: okuma çıpalı pencereyi kapsayacak şekilde şimdiye genişletildi — liste çıpa sonrası değerleri de içerebilir")
	}
	if label != "" {
		key := label
		if rk, ok := mtCHRoleKeys[label]; ok {
			key = rk
		}
		vr, ok := src.(metricLabelValueReader)
		if !ok {
			return nil, fmt.Errorf("label değerleri bu kaynakta okunamıyor")
		}
		lbl = key
		vctx, vcancel := context.WithTimeout(ctx, mtDiscoverBudget)
		got, err := vr.MetricLabelValues(vctx, name, key, since, "", limit+1)
		vcancel()
		if err != nil {
			if mtCancelled(ctx, err) {
				return nil, err
			}
			return done(sourcestate.FromError(mtSourceMetrics, backend, err).WithWindow(from, to)), nil
		}
		more := len(got) > limit
		if more {
			got = got[:limit]
		}
		if got == nil {
			got = []string{}
		}
		vals, valsMore = got, more
		outcome = sourcestate.Outcome{Returned: len(got), Limit: limit, Truncated: more}
	}
	st := sourcestate.Result(mtSourceMetrics, backend, outcome).WithWindow(from, to)
	st.Notes = append(st.Notes, notes...)
	if uncovered {
		st = st.WithNote("çıpalı pencere 7 günden eski — ClickHouse yalnız son 7 günü okuyabildi; liste o anı yansıtmaz", true)
	}
	return done(st), nil
}
