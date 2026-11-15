package anomaly

// red_vm_select_test.go — v0.10.1478: kaynak seçimi tablosu, uçtan uca
// hazırlık (sahte VM), taban eşdeğerliği (aynı girdi → kaynaktan bağımsız
// aynı karar), kanıt satırı ve aiops kalkanı: VM seçildiyse örneklenmiş span
// error_rate'i kullanılmaz.

import (
	"context"
	"errors"
	"math"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func TestPickREDSourceTable(t *testing.T) {
	ok := vmPickInput{Configured: true, FamilyFound: true, MetricOK: true, Enough: true}
	cases := []struct {
		name   string
		in     vmPickInput
		wantVM bool
		reason string
	}{
		{"VM var + aile var", ok, true, ""},
		{"kaynak ayarı span", vmPickInput{SpanSetting: true, Configured: true, FamilyFound: true, MetricOK: true, Enough: true}, false, redReasonSpanSetting},
		{"VM yapılandırılmamış", vmPickInput{FamilyFound: true, MetricOK: true, Enough: true}, false, redReasonNotConfigured},
		{"aile yok (gerekçesiz)", vmPickInput{Configured: true}, false, redReasonNoService},
		{"aile yok (histogram yok)", vmPickInput{Configured: true, FamilyReason: redReasonNoFamily}, false, redReasonNoFamily},
		{"VM hatası", vmPickInput{Configured: true, FamilyReason: redReasonVMError + ": etiket okuması"}, false, redReasonVMError + ": etiket okuması"},
		{"metrik ölçülemez", vmPickInput{Configured: true, FamilyFound: true, MetricReason: redReasonNoStatus}, false, redReasonNoStatus},
		{"seri kısa", vmPickInput{Configured: true, FamilyFound: true, MetricOK: true}, false, redReasonShort},
	}
	for _, c := range cases {
		vm, r := pickREDSource(c.in)
		if vm != c.wantVM || r != c.reason {
			t.Errorf("%s: (%v, %q), beklenen (%v, %q)", c.name, vm, r, c.wantVM, c.reason)
		}
	}
}

func TestREDSourceEvidenceTR(t *testing.T) {
	if got := redSourceEvidenceTR(redSourceUsed{VM: true, Family: "HTTP"}); got != "kaynak: VictoriaMetrics (örneklenmemiş) · HTTP sunucu histogramı" {
		t.Errorf("VM kanıtı: %q", got)
	}
	if got := redSourceEvidenceTR(redSourceUsed{}); got != "kaynak: span (örneklenmiş)" {
		t.Errorf("gerekçesiz span: %q", got)
	}
	d := withREDEvidence("Error rate up on a — x.", "kaynak: span (örneklenmiş)")
	if d != "Error rate up on a — x. kaynak: span (örneklenmiş)." {
		t.Errorf("açıklama eki: %q", d)
	}
	if withREDEvidence("x", "") != "x" {
		t.Error("boş kanıt açıklamayı değiştirmemeli")
	}
}

// ── sahte VM ────────────────────────────────────────────────────────────────

// fakeREDVM — sorgu türünü metinden tanır (histogram_quantile → p99, hata
// eşleştiricisi → err, aksi count), cm_off ofsetlerini çözer ve
// QueryPromQLRange'in damga sözleşmesini (nokta = kova başı, `from`daki örnek
// düşer) taklit eder.
type fakeREDVM struct {
	notConfigured bool
	labels        map[string][]string // probe → etiket adları
	labelErr      error
	rangeErr      error
	statusSvcs    []string
	// val — (servis, aile probe'u, tür, kova başı) → değer; ok=false seri yok.
	val     func(svc, probe string, k vmREDKind, bucket int64) (float64, bool)
	svcs    []string
	queries atomic.Int64 // execute paralel koşar
}

var offRe = regexp.MustCompile(`"cm_off", "(\d+)"`)

func (f *fakeREDVM) Configured() bool                  { return !f.notConfigured }
func (f *fakeREDVM) MetricLabelMap() vmetrics.LabelMap { return vmetrics.LabelMap{} }
func (f *fakeREDVM) MetricLabelNamesIn(_ context.Context, metric string, _, _ time.Time) ([]string, bool, error) {
	if f.labelErr != nil {
		return nil, false, f.labelErr
	}
	return f.labels[metric], false, nil
}
func (f *fakeREDVM) MetricLabelValuesIn(_ context.Context, _, _ string, _, _ time.Time, _ int) ([]string, bool, error) {
	return []string{"prod-eu-a", "test-eu-a"}, false, nil
}
func (f *fakeREDVM) QueryPromQLInstant(_ context.Context, _ string, at time.Time) ([]chstore.SpanMetricSeries, error) {
	f.queries.Add(1)
	var out []chstore.SpanMetricSeries
	for _, s := range f.statusSvcs {
		out = append(out, chstore.SpanMetricSeries{GroupKey: []string{`service_name="` + s + `"`}, Points: []chstore.SpanMetricPoint{{Time: at.UnixNano(), Value: 1}}})
	}
	return out, nil
}
func (f *fakeREDVM) QueryPromQLRange(_ context.Context, q string, from, to time.Time, step, _ int) ([]chstore.SpanMetricSeries, error) {
	f.queries.Add(1)
	if f.rangeErr != nil {
		return nil, f.rangeErr
	}
	k := vmKindCount
	switch {
	case strings.Contains(q, "histogram_quantile"):
		k = vmKindP99
	case strings.Contains(q, `=~"5.."`) || strings.Contains(q, `=~"2|4|8`):
		k = vmKindErr
	}
	probe := vmREDFamilies[0].Probe
	if strings.Contains(q, "rpc_server_duration_milliseconds") {
		probe = vmREDFamilies[1].Probe
	}
	var out []chstore.SpanMetricSeries
	for _, m := range offRe.FindAllStringSubmatch(q, -1) {
		off, _ := strconv.ParseInt(m[1], 10, 64)
		for _, svc := range f.svcs {
			s := chstore.SpanMetricSeries{GroupKey: []string{`cm_off="` + m[1] + `"`, `service_name="` + svc + `"`}}
			for ts := from.Unix() + int64(step); ts <= to.Unix(); ts += int64(step) {
				bucket := ts - int64(step)
				if v, ok := f.val(svc, probe, k, bucket-off); ok {
					s.Points = append(s.Points, chstore.SpanMetricPoint{Time: bucket * int64(time.Second), Value: v})
				}
			}
			if len(s.Points) > 0 {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// warm — görünüm ısınana dek prepare (her tik bütçe sınırlı).
func warm(t *testing.T, src *vmREDSource, now time.Time, sens chstore.AnomalySensitivityConfig) *vmREDView {
	t.Helper()
	var v *vmREDView
	for i := 0; i < 400; i++ {
		v = src.prepare(context.Background(), now, sens, seasonalDays, seasonalNeighborBuckets)
		if v.pending == 0 {
			return v
		}
	}
	t.Fatalf("görünüm ısınmadı: bekleyen %d", v.pending)
	return v
}

// steadyVal — orders-api: HTTP, 10 istek/kova·sn ≈ sabit, %1 hata, p99 120 ms.
func steadyVal(svc, probe string, k vmREDKind, _ int64) (float64, bool) {
	if svc != "orders-api" || probe != vmREDFamilies[0].Probe {
		return 0, false
	}
	switch k {
	case vmKindCount:
		return 3000, true
	case vmKindErr:
		return 30, true
	}
	return 120, true
}

func newFakeSource(f *fakeREDVM) *vmREDSource {
	src := newVMREDSource()
	src.wire.Store(&vmREDBox{vm: f})
	return src
}

// TestREDSourceSelection — VM var / aile yok / VM hatası → span (uçtan uca).
func TestREDSourceSelection(t *testing.T) {
	now := time.Date(2026, 10, 22, 14, 31, 0, 0, time.UTC)
	sens := chstore.DefaultAnomalySensitivity()
	httpLabels := map[string][]string{vmREDFamilies[0].Probe: {"service_name", "http_response_status_code", "le"}}

	f := &fakeREDVM{labels: httpLabels, statusSvcs: []string{"orders-api"}, val: steadyVal, svcs: []string{"orders-api"}}
	v := warm(t, newFakeSource(f), now, sens)
	for _, m := range []string{"error_rate", "p99_ms", "request_rate"} {
		s, u := v.seriesFor("orders-api", m, sens.DwellBuckets)
		if !u.VM || u.Family != "HTTP" {
			t.Fatalf("%s: VM bekleniyordu, %+v", m, u)
		}
		if len(s.Values) < minSamples+sens.DwellBuckets || len(s.Seasonal) == 0 {
			t.Fatalf("%s: seri %d / mevsimsel %d", m, len(s.Values), len(s.Seasonal))
		}
	}
	if s, _ := v.seriesFor("orders-api", "error_rate", 3); math.Abs(s.Values[len(s.Values)-1]-1) > 1e-9 {
		t.Errorf("VM error_rate %%1 olmalı: %v", s.Values[len(s.Values)-1])
	}
	if s, _ := v.seriesFor("orders-api", "request_rate", 3); math.Abs(s.Values[len(s.Values)-1]-10) > 1e-9 {
		t.Errorf("VM request_rate 3000/300 = 10 olmalı: %v", s.Values[len(s.Values)-1])
	}
	// Ailesi olmayan servis → span, gerekçeli.
	if _, u := v.seriesFor("legacy-svc", "error_rate", 3); u.VM || u.Reason != redReasonNoService {
		t.Errorf("ailesiz servis span + %q olmalı: %+v", redReasonNoService, u)
	}
	// Durum etiketi taşımayan servis: hata oranı VM'den ölçülmez.
	f2 := &fakeREDVM{labels: httpLabels, statusSvcs: nil, val: steadyVal, svcs: []string{"orders-api"}}
	v2 := warm(t, newFakeSource(f2), now, sens)
	if _, u := v2.seriesFor("orders-api", "error_rate", 3); u.VM || u.Reason != redReasonNoStatus {
		t.Errorf("durum etiketsiz servis error_rate span olmalı: %+v", u)
	}
	if _, u := v2.seriesFor("orders-api", "p99_ms", 3); !u.VM {
		t.Errorf("p99 yine VM olmalı: %+v", u)
	}
	// Histogram hiç yok → span + "VM'de yok".
	v3 := newFakeSource(&fakeREDVM{labels: map[string][]string{}, val: steadyVal}).prepare(context.Background(), now, sens, seasonalDays, seasonalNeighborBuckets)
	if _, u := v3.seriesFor("orders-api", "p99_ms", 3); u.VM || u.Reason != redReasonNoFamily {
		t.Errorf("histogram yokken: %+v", u)
	}
	// Etiket okuma hatası → span + okunamadı (yok DEĞİL).
	v4 := newFakeSource(&fakeREDVM{labelErr: errors.New("dial tcp: refused"), val: steadyVal}).prepare(context.Background(), now, sens, seasonalDays, seasonalNeighborBuckets)
	if _, u := v4.seriesFor("orders-api", "p99_ms", 3); u.VM || !strings.HasPrefix(u.Reason, redReasonVMError) {
		t.Errorf("etiket hatasında: %+v", u)
	}
	// Sorgu hatası → span + okunamadı; geri çekilme sorgu selini keser.
	f5 := &fakeREDVM{labels: httpLabels, statusSvcs: []string{"orders-api"}, rangeErr: errors.New("timeout"), val: steadyVal, svcs: []string{"orders-api"}}
	src5 := newFakeSource(f5)
	v5 := src5.prepare(context.Background(), now, sens, seasonalDays, seasonalNeighborBuckets)
	if _, u := v5.seriesFor("orders-api", "p99_ms", 3); u.VM || !strings.HasPrefix(u.Reason, redReasonVMError) {
		t.Errorf("sorgu hatasında: %+v", u)
	}
	before := f5.queries.Load()
	src5.prepare(context.Background(), now.Add(2*time.Minute), sens, seasonalDays, seasonalNeighborBuckets)
	if f5.queries.Load() != before {
		t.Errorf("geri çekilmede sorgu koşmamalı (%d → %d)", before, f5.queries.Load())
	}
	// VM yapılandırılmamış / kaynak ayarı span.
	v6 := newFakeSource(&fakeREDVM{notConfigured: true}).prepare(context.Background(), now, sens, seasonalDays, seasonalNeighborBuckets)
	if _, u := v6.seriesFor("orders-api", "p99_ms", 3); u.VM || u.Reason != redReasonNotConfigured {
		t.Errorf("yapılandırılmamış: %+v", u)
	}
	spanSens := sens
	spanSens.REDSource = chstore.AnomalyREDSourceSpan
	f7 := &fakeREDVM{labels: httpLabels, val: steadyVal, svcs: []string{"orders-api"}}
	v7 := newFakeSource(f7).prepare(context.Background(), now, spanSens, seasonalDays, seasonalNeighborBuckets)
	if _, u := v7.seriesFor("orders-api", "p99_ms", 3); u.VM || u.Reason != redReasonSpanSetting || f7.queries.Load() != 0 {
		t.Errorf("span ayarında VM'e HİÇ gidilmemeli: %+v, %d sorgu", u, f7.queries.Load())
	}
}

// TestREDSourceEnvScope — ortam kapsamı eşleşmezse VM kullanılmaz (karışmaz).
func TestREDSourceEnvScope(t *testing.T) {
	now := time.Date(2026, 10, 22, 14, 31, 0, 0, time.UTC)
	sens := chstore.DefaultAnomalySensitivity()
	sens.REDSourceEnv = "nope-env"
	f := &fakeREDVM{labels: map[string][]string{vmREDFamilies[0].Probe: {"service_name", "deployment_environment_name"}}, val: steadyVal, svcs: []string{"orders-api"}}
	v := newFakeSource(f).prepare(context.Background(), now, sens, seasonalDays, seasonalNeighborBuckets)
	if _, u := v.seriesFor("orders-api", "p99_ms", 3); u.VM || !strings.Contains(u.Reason, `ortam "nope-env" eşleşmedi`) {
		t.Errorf("eşleşmeyen ortam span olmalı: %+v", u)
	}
	sens.REDSourceEnv = "prod-eu-a"
	src := newFakeSource(f)
	src.prepare(context.Background(), now, sens, seasonalDays, seasonalNeighborBuckets)
	if m := src.meta[0].scope.EnvMatcher; m != `deployment_environment_name="prod-eu-a"` {
		t.Errorf("ortam eşleştiricisi: %q", m)
	}
}

// TestREDBaselineEquivalence — AYNI kova verisi span yolundan (SQL'in
// ürettiği şekilde: yalnız istekli kovalar + kuyruk sıfırı) ve VM yolundan
// geçince evaluateAnomaly AYNI kararı verir — tabanı, dwell'i, kapıları kaynak
// değiştirmez.
func TestREDBaselineEquivalence(t *testing.T) {
	now := time.Date(2026, 10, 22, 14, 31, 0, 0, time.UTC)
	sens := chstore.DefaultAnomalySensitivity()
	vnow := now.Add(-vmIngestLag)
	upper := lastCompleteBucketStart(vnow).Unix()
	// gün içi dalga + son 3 kovada hata sıçraması; gece bir kova boş (istek yok).
	val := func(svc, probe string, k vmREDKind, b int64) (float64, bool) {
		if svc != "orders-api" || probe != vmREDFamilies[0].Probe {
			return 0, false
		}
		h := float64(time.Unix(b, 0).UTC().Hour())
		count := 2000 + 1500*math.Sin(h/24*2*math.Pi) + float64((b/300)%7)*13
		if (b/300)%97 == 0 {
			return 0, false // boş kova: SQL satırı yok
		}
		spike := b >= upper-3*300
		switch k {
		case vmKindCount:
			return count, true
		case vmKindErr:
			if spike {
				return count * 0.30, true
			}
			return count * (0.01 + float64((b/300)%5)*0.001), true
		}
		if spike {
			return 900, true
		}
		return 110 + float64((b/300)%11), true
	}
	f := &fakeREDVM{labels: map[string][]string{vmREDFamilies[0].Probe: {"service_name", "http_response_status_code"}},
		statusSvcs: []string{"orders-api"}, val: val, svcs: []string{"orders-api"}}
	v := warm(t, newFakeSource(f), now, sens)

	// Span ikizi: aynı kovalar, SQL ifadelerinin Go karşılığıyla.
	spanSeries := func(metric string, buckets []int64, pad bool) ([]float64, []float64) {
		var vals, rates []float64
		var last int64
		for _, b := range buckets {
			c, ok := val("orders-api", vmREDFamilies[0].Probe, vmKindCount, b)
			if !ok || c <= 0 {
				continue
			}
			e, _ := val("orders-api", vmREDFamilies[0].Probe, vmKindErr, b)
			p, _ := val("orders-api", vmREDFamilies[0].Probe, vmKindP99, b)
			x := map[string]float64{"error_rate": e / c * 100, "request_rate": c / 300, "p99_ms": p}[metric]
			vals, rates, last = append(vals, x), append(rates, c/300), b
		}
		if pad {
			vals, rates = padTrailingSilence(vals, last, upper), padTrailingSilence(rates, last, upper)
		}
		return vals, rates
	}
	opened := 0
	for _, m := range []string{"error_rate", "p99_ms", "request_rate"} {
		s, u := v.seriesFor("orders-api", m, sens.DwellBuckets)
		if !u.VM {
			t.Fatalf("%s: VM bekleniyordu: %+v", m, u)
		}
		bv, br := spanSeries(m, vmConsecutiveBuckets(vnow), true)
		sv, _ := spanSeries(m, vmSeasonalBuckets(vnow, seasonalDays, seasonalNeighborBuckets), false)
		if !floatsClose(s.Values, bv) || !floatsClose(s.Rates, br) || !floatsClose(s.Seasonal, sv) {
			t.Fatalf("%s: VM serisi span ikiziyle aynı değil (%d/%d, %d/%d)", m, len(s.Values), len(bv), len(s.Seasonal), len(sv))
		}
		got := evaluateAnomaly(m, s.Values, s.Seasonal, s.Rates, seasonalMinSamples, false, batchLatSeries{}, sens)
		want := evaluateAnomaly(m, bv, sv, br, seasonalMinSamples, false, batchLatSeries{}, sens)
		if !reflect.DeepEqual(roundOutcome(got), roundOutcome(want)) {
			t.Fatalf("%s: karar kaynağa göre değişti\nVM   %+v\nspan %+v", m, got, want)
		}
		if got.Action == "open" {
			opened++
		}
	}
	if opened == 0 {
		t.Fatal("sıçrama hiçbir metrikte açılmadı — test girdisi kararı sınamıyor")
	}
}

func floatsClose(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-3*math.Max(1, math.Abs(b[i])) {
			return false
		}
	}
	return true
}

// roundOutcome — float32 bellek yuvarlamasını karşılaştırmadan ayıklar.
func roundOutcome(o anomalyOutcome) anomalyOutcome {
	r := func(x float64) float64 { return math.Round(x*100) / 100 }
	o.Current, o.Median, o.MAD, o.Z = r(o.Current), r(o.Median), r(o.MAD), math.Round(o.Z*10)/10
	o.Ramp.Threshold, o.Ramp.Median, o.Ramp.P90 = r(o.Ramp.Threshold), r(o.Ramp.Median), r(o.Ramp.P90)
	o.Ramp.CurRatio, o.Ramp.CurEPS = r(o.Ramp.CurRatio), r(o.Ramp.CurEPS)
	return o
}

// fakeProvider — overlay kalkanı için.
type fakeProvider map[string]vmSeries

func (p fakeProvider) seriesFor(svc, metric string, _ int) (vmSeries, redSourceUsed) {
	if s, ok := p[svc+"|"+metric]; ok {
		return s, redSourceUsed{VM: true, Family: s.Family}
	}
	return vmSeries{}, redSourceUsed{Reason: redReasonNoService}
}

// TestOverlayNeverMixesSampledSpanErrorRate — aiops kalkanı: VM seçilen
// çiftte örneklenmiş span error_rate'i ne cari değerde ne tabanda kalır; VM
// mevsimsel tabanı yoksa span'inki SİLİNİR (ardışık VM penceresine düşülür).
func TestOverlayNeverMixesSampledSpanErrorRate(t *testing.T) {
	spanErr := []float64{40, 41, 39, 40, 42, 40} // kuyruk örneklemesiyle şişmiş
	buckets := map[string]map[string][]float64{"error_rate": {"orders-api": spanErr, "billing-api": spanErr}}
	seasonal := map[string]map[string][]float64{"error_rate": {"orders-api": spanErr, "billing-api": spanErr}, "p99_ms": {"orders-api": {500}}}
	rates := map[string]map[string][]float64{"error_rate": {"orders-api": {0.5}, "billing-api": {0.5}}}
	seasonalRates := map[string]map[string][]float64{"p99_ms": {"orders-api": {0.5}}}
	vmErr := []float64{1, 1.1, 0.9, 1, 1.2, 1}
	p := fakeProvider{
		"orders-api|error_rate": {Family: "HTTP", Values: vmErr, Rates: []float64{10}},
		"orders-api|p99_ms":     {Family: "HTTP", Values: []float64{120}, Rates: []float64{10}, Seasonal: nil},
	}
	used := overlayREDSeries(p, []string{"orders-api", "billing-api"}, []string{"error_rate", "p99_ms"}, 3, buckets, seasonal, rates, seasonalRates)
	if !reflect.DeepEqual(buckets["error_rate"]["orders-api"], vmErr) {
		t.Fatalf("VM seçilen serviste error_rate VM olmalı: %v", buckets["error_rate"]["orders-api"])
	}
	if _, ok := seasonal["error_rate"]["orders-api"]; ok {
		t.Fatal("VM mevsimsel tabanı yokken span tabanı KALMAMALI (örneklenmiş taban VM cari değeriyle karışır)")
	}
	if _, ok := seasonal["p99_ms"]["orders-api"]; ok {
		t.Fatal("p99 span tabanı silinmeli")
	}
	if _, ok := seasonalRates["p99_ms"]["orders-api"]; ok {
		t.Fatal("p99 span mevsimsel hacmi silinmeli")
	}
	if !reflect.DeepEqual(rates["error_rate"]["orders-api"], []float64{10}) {
		t.Fatal("hacim serisi de VM'den gelmeli (kapı aynı kaynağı okur)")
	}
	if !reflect.DeepEqual(buckets["error_rate"]["billing-api"], spanErr) || !reflect.DeepEqual(seasonal["error_rate"]["billing-api"], spanErr) {
		t.Fatal("VM'siz servis span'de kalmalı")
	}
	if u := used["orders-api|error_rate"]; !u.VM {
		t.Fatalf("kaynak kaydı: %+v", u)
	}
	if u := used["billing-api|error_rate"]; u.VM || u.Reason != redReasonNoService {
		t.Fatalf("kaynak kaydı: %+v", u)
	}
}

// TestScanOverlaysBeforeVerdict — kaynak pini: scan() VM bindirmesini karar
// fazından (evaluateAnomaly) ÖNCE yapar ve kanıtı karara iliştirir.
func TestScanOverlaysBeforeVerdict(t *testing.T) {
	b, err := os.ReadFile("anomaly.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (d *Detector) scan(")
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j > 0 {
		body = body[:j]
	}
	ov := strings.Index(body, "overlayREDSeries(redView, services, tracked")
	ev := strings.Index(body, "evaluateAnomaly(m, buckets, seasonal, rates")
	if ov < 0 || ev < 0 || ov > ev {
		t.Fatal("scan(): overlayREDSeries evaluateAnomaly'den önce çağrılmalı")
	}
	if !strings.Contains(body, `oc.Evidence = redSourceEvidenceTR(redUsed[svc+"|"+m])`) {
		t.Fatal("scan(): karar kanıtı kaynak satırını taşımalı")
	}
	if !strings.Contains(src, "desc = withREDEvidence(desc, oc.Evidence)") {
		t.Fatal("applyOutcome: Problem açıklaması kaynak satırını taşımalı")
	}
}

// TestSourceReasonHiddenOnlyWhenVMNotConfigured — operatör 2026-10-24: VM hiç
// yapılandırılmamış kurulumda "VM kullanılmadı: VictoriaMetrics
// yapılandırılmamış" gürültü; gizlenir. VM yapılandırılmış ama kullanılamamışsa
// (okunamadı, seri yok, aile yok, ölçülemiyor, kaynak ayarı span) gerekçe kalır.
func TestSourceReasonHiddenOnlyWhenVMNotConfigured(t *testing.T) {
	cases := []struct {
		name   string
		src    AlertSource
		wantEv string
		shown  string
	}{
		{"vm yapılandırılmamış", SpanAlertSource(redReasonNotConfigured), "kaynak: span (örneklenmiş)", ""},
		{"vm okunamadı", SpanAlertSource(redReasonVMError + ": timeout"), "kaynak: span (örneklenmiş) — VM kullanılmadı: " + redReasonVMError + ": timeout", redReasonVMError + ": timeout"},
		{"seri yok", SpanAlertSource(redReasonNoService), "kaynak: span (örneklenmiş) — VM kullanılmadı: " + redReasonNoService, redReasonNoService},
		{"aile yok", SpanAlertSource(redReasonNoFamily), "kaynak: span (örneklenmiş) — VM kullanılmadı: " + redReasonNoFamily, redReasonNoFamily},
		{"durum etiketi yok", SpanAlertSource(redReasonNoStatus), "kaynak: span (örneklenmiş) — VM kullanılmadı: " + redReasonNoStatus, redReasonNoStatus},
		{"seri yetersiz", SpanAlertSource(redReasonShort), "kaynak: span (örneklenmiş) — VM kullanılmadı: " + redReasonShort, redReasonShort},
		{"kaynak ayarı span", SpanAlertSource(redReasonSpanSetting), "kaynak: span (örneklenmiş) — VM kullanılmadı: " + redReasonSpanSetting, redReasonSpanSetting},
		{"gerekçesiz", AlertSource{}, "kaynak: span (örneklenmiş)", ""},
		{"vm", AlertSource{VM: true, Family: "gRPC"}, "kaynak: VictoriaMetrics (örneklenmemiş) · gRPC sunucu histogramı", ""},
	}
	for _, c := range cases {
		if got := c.src.Evidence(); got != c.wantEv {
			t.Errorf("%s: kanıt %q, beklenen %q", c.name, got, c.wantEv)
		}
		if got := redSourceEvidenceTR(redSourceUsed{VM: c.src.VM, Family: c.src.Family, Reason: c.src.Reason}); got != c.wantEv {
			t.Errorf("%s: anomali kanıtı %q", c.name, got)
		}
		if got := c.src.ShownReason(); got != c.shown {
			t.Errorf("%s: gösterilen gerekçe %q, beklenen %q", c.name, got, c.shown)
		}
	}
}
