package ml

// svc_response_time_test.go — v0.10.2385 (etki olayı programı, dilim I4).
// SÖZLEŞME: servisin medyan (p50) ve en yavaş %10 (p90) yanıt süresi 1
// DAKİKALIK örneklerle ve etki kuralıyla AYRI yargılanır (medyan ≥ max(taban ×
// 1,5, taban + 100 ms); en yavaş %10 ≥ max(taban × 2, taban + 1000 ms); son 5
// dakikanın en az 3'ü ve en yenisi; dakikada en az 10 istek). Biri yeterse
// olay; olayın değerleri ateşleyen ölçünün (ikisi de ateşlerse oranı büyük
// olanın). Örnekler servis × le bucket'larının ANLIK sorgusundan (aralık
// sorgusu yok), dakika başına aile başına bir kez. Açık olay pencerede ihlal
// kaldıkça uzar, günlük tavanı harcamaz. Olay tek başına servis Problem'ini
// açar. Testler saat ve saat dilimi bağımsız: `now` hep açık verilir.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// rtLes — sahte histogramın bucket sınırları (sn; +Inf ayrıca).
var rtLes = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// rtShape — dakikanın dağılımı: isteklerin %80'i fastLe bucket'ında, %20'si
// slowLe bucket'ında. Yüzdelikler (bucket içi doğrusal):
//
//	hızlı 0,25 / yavaş 1   → p50 193,75 ms, p90 750 ms   (olağan)
//	hızlı 0,5  / yavaş 1   → p50 406,25 ms, p90 750 ms   (yalnız medyan)
//	hızlı 0,25 / yavaş 5   → p50 193,75 ms, p90 3750 ms  (yalnız en yavaş %10)
//	hızlı 0,5  / yavaş 5   → p50 406,25 ms, p90 3750 ms  (ikisi)
type rtShape struct{ fastLe, slowLe float64 }

var (
	rtNormal   = rtShape{0.25, 1}
	rtMedSlow  = rtShape{0.5, 1}
	rtTailSlow = rtShape{0.25, 5}
	rtBothSlow = rtShape{0.5, 5}
)

const (
	rtBaseP50 = 193.75
	rtBaseP90 = 750.0
)

// rtCum — dağılımın kümülatif bucket sayıları (le → istek), +Inf dahil.
func rtCum(sh rtShape, load float64) map[float64]float64 {
	out := map[float64]float64{}
	for _, le := range append(append([]float64{}, rtLes...), math.Inf(1)) {
		c := 0.0
		if le >= sh.fastLe {
			c += 0.8 * load
		}
		if le >= sh.slowLe {
			c += 0.2 * load
		}
		out[le] = c
	}
	return out
}

// rtRows — VM anlık yanıtı biçiminde servis × le satırları.
func rtRows(svcLabel, svc string, cum map[float64]float64) []chstore.SpanMetricSeries {
	les := make([]float64, 0, len(cum))
	for le := range cum {
		les = append(les, le)
	}
	sort.Float64s(les)
	var out []chstore.SpanMetricSeries
	for _, le := range les {
		l := fmt.Sprintf("%g", le)
		if math.IsInf(le, 1) {
			l = "+Inf"
		}
		out = append(out, chstore.SpanMetricSeries{GroupKey: []string{fmt.Sprintf("le=%q", l), fmt.Sprintf("%s=%q", svcLabel, svc)},
			Points: []chstore.SpanMetricPoint{{Time: 1, Value: cum[le]}}})
	}
	return out
}

// rtVM — dakika dakika histogram üreten sahte VM (yalnız anlık sorgu):
// pencere ifadeden ([60s] / [300s]), dakikalar sorgu anından geriye toplanır.
type rtVM struct {
	fakeVM
	t0      time.Time
	svcs    map[string]func(min int) (rtShape, float64, bool)
	fam     map[string]string // servis → aile metrik adı parçası ("" = her aile)
	instant int
	ranges  int
	queries []string
	fail    func(q string) error
}

func (f *rtVM) QueryPromQLRange(context.Context, string, time.Time, time.Time, int, int) ([]chstore.SpanMetricSeries, error) {
	f.ranges++
	return nil, nil
}

func (f *rtVM) QueryPromQLInstant(_ context.Context, q string, at time.Time) ([]chstore.SpanMetricSeries, error) {
	f.instant++
	f.queries = append(f.queries, q)
	if f.fail != nil {
		if err := f.fail(q); err != nil {
			return nil, err
		}
	}
	win := int64(60)
	if strings.Contains(q, "[300s]") {
		win = 300
	}
	names := make([]string, 0, len(f.svcs))
	for svc := range f.svcs {
		names = append(names, svc)
	}
	sort.Strings(names)
	var out []chstore.SpanMetricSeries
	for _, svc := range names {
		if want := f.fam[svc]; want != "" && !strings.Contains(q, want) {
			continue
		}
		sum := map[float64]float64{}
		any := false
		for m := at.Unix() - win; m < at.Unix(); m += 60 {
			sh, load, ok := f.svcs[svc](int((m - f.t0.Unix()) / 60))
			if !ok {
				continue
			}
			any = true
			for le, c := range rtCum(sh, load) {
				sum[le] += c
			}
		}
		if any {
			out = append(out, rtRows("service_name", svc, sum)...)
		}
	}
	return out, nil
}

// rtIncident — [from, to) dakikalarında bad, dışında olağan dağılım; load istek/dk.
func rtIncident(from, to int, bad rtShape, load float64) func(int) (rtShape, float64, bool) {
	return func(m int) (rtShape, float64, bool) {
		if m >= from && m < to {
			return bad, load, true
		}
		return rtNormal, load, true
	}
}

func rtTestBases(svcs ...string) map[string]rtBase {
	out := map[string]rtBase{}
	for _, s := range svcs {
		out[s] = rtBaseOf(repeat(rtBaseP50, 30), repeat(rtBaseP90, 30))
	}
	return out
}

func rtFixture(t0 time.Time) (*TraceRED, *rtVM, []impFam) {
	d := NewTraceRED()
	pin := anomaly.REDFamilyPins()[0]
	d.labels[pin.ID] = traceFamilyLabels{svc: "service_name", route: "http_route", status: "http_response_status_code", at: time.Now(), ok: true}
	vm := &rtVM{t0: t0, svcs: map[string]func(int) (rtShape, float64, bool){}, fam: map[string]string{}}
	return d, vm, d.cachedFamilies()
}

func rtS(v, load float64) impactSample { return impactSample{Value: v, Load: load, OK: true} }

func rtRep(s impactSample) []impactSample { return repeatS(s) }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// ── saf parçalar ─────────────────────────────────────────────────────────

func TestHistQuantile(t *testing.T) {
	inf := math.Inf(1)
	bs := []histBucket{{0.1, 0}, {0.25, 80}, {0.5, 80}, {1, 100}, {inf, 100}}
	cases := []struct {
		name string
		q    float64
		bs   []histBucket
		want float64
		ok   bool
	}{
		{"medyan bucket içinde doğrusal", 0.5, bs, 0.1 + 0.15*50/80, true},
		{"en yavaş %10 sonraki dolu bucket'ta", 0.9, bs, 0.5 + 0.5*10/20, true},
		{"ilk bucket: alt sınır 0", 0.5, []histBucket{{0.1, 100}, {inf, 100}}, 0.05, true},
		{"yüzdelik +Inf bucket'ında → en büyük sonlu sınır (ölçüm tavanı)", 0.9, []histBucket{{1, 50}, {10, 80}, {inf, 100}}, 10, true},
		{"+Inf bucket'ı yok → ölçülmedi", 0.5, []histBucket{{0.1, 5}, {1, 10}}, 0, false},
		{"istek yok → ölçülmedi", 0.5, []histBucket{{0.1, 0}, {inf, 0}}, 0, false},
		{"tek bucket → ölçülmedi", 0.5, []histBucket{{inf, 10}}, 0, false},
	}
	for _, c := range cases {
		got, ok := histQuantile(c.q, c.bs)
		if ok != c.ok || (ok && !near(got, c.want)) {
			t.Errorf("%s: %v,%v want %v,%v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestRTFromBuckets(t *testing.T) {
	rows := rtRows("service_name", "orders", rtCum(rtNormal, 600))
	rows = append(rows, rtRows("service_name", "checkout", rtCum(rtBothSlow, 40))...)
	// +Inf'siz servis, bozuk le, servissiz satır: yok sayılır
	rows = append(rows,
		chstore.SpanMetricSeries{GroupKey: []string{`le="0.5"`, `service_name="noinf"`}, Points: []chstore.SpanMetricPoint{{Value: 9}}},
		chstore.SpanMetricSeries{GroupKey: []string{`le="abc"`, `service_name="orders"`}, Points: []chstore.SpanMetricPoint{{Value: 9999}}},
		chstore.SpanMetricSeries{GroupKey: []string{`le="+Inf"`}, Points: []chstore.SpanMetricPoint{{Value: 9999}}})
	got := rtFromBuckets(rows, "service_name", 1000)
	if len(got) != 2 {
		t.Fatalf("yalnız yüzdeliği hesaplanan servisler: %+v", got)
	}
	if p := got["orders"]; !near(p.Q[0], rtBaseP50) || !near(p.Q[1], rtBaseP90) || p.Load != 600 {
		t.Errorf("orders: %+v", p)
	}
	if p := got["checkout"]; !near(p.Q[0], 406.25) || !near(p.Q[1], 3750) || p.Load != 40 {
		t.Errorf("checkout: %+v", p)
	}
	// ms birimli aile: çarpan 1
	if p := rtFromBuckets(rtRows("svc", "a", map[float64]float64{100: 50, 200: 100, math.Inf(1): 100}), "svc", 1)["a"]; !near(p.Q[0], 100) || !near(p.Q[1], 180) {
		t.Errorf("ms ailesi: %+v", p)
	}
	// sayaç sıfırlanması artığı (kümülatif azalıyor) tekdüze yapılır
	if p, ok := rtFromBuckets(rtRows("svc", "a", map[float64]float64{0.1: 60, 0.5: 40, math.Inf(1): 100}), "svc", 1000)["a"]; !ok || math.IsNaN(p.Q[1]) || p.Q[1] < p.Q[0] {
		t.Errorf("tekdüze olmayan bucket: %+v %v", p, ok)
	}
}

func TestRTBucketQuery(t *testing.T) {
	pin := anomaly.REDFamilyPins()[0]
	for win, tag := range map[int]string{60: "[60s]", 300: "[300s]"} {
		q := rtBucketQuery(pin, "service_name", win)
		if !strings.HasPrefix(q, "sum by (service_name, le) (increase(") || !strings.Contains(q, "(increase("+redSel(pin, partBucket)+tag+"))") || !strings.Contains(q, tag) ||
			strings.Contains(q, "http_route") || strings.Contains(q, "topk") || strings.Contains(q, "histogram_quantile") || strings.Contains(q, "_count") {
			t.Fatalf("sorgu: %s", q)
		}
	}
	if pinMsScale(anomaly.REDFamilyPin{Unit: "s"}) != 1000 || pinMsScale(anomaly.REDFamilyPin{Unit: "ms"}) != 1 {
		t.Fatal("birim çarpanı")
	}
}

// "İlk aile kazanır"; alınamayan ailede durulur (alt aileler güvenilmez).
func TestRTPickFirst(t *testing.T) {
	p := func(v, load float64) rtPoint { return rtPoint{Q: [2]float64{v, v * 2}, Load: load} }
	got := map[int]map[string]rtPoint{
		0: {"orders": p(100, 50), "idle": p(100, 0)},
		1: {"orders": p(900, 50), "idle": p(300, 20), "rpc": p(400, 30)},
	}
	all := rtPickFirst([]int{0, 1}, got)
	if all["orders"].Q[0] != 100 || all["orders"].fam != 0 || all["idle"].fam != 1 || all["rpc"].Q[0] != 400 {
		t.Fatalf("ilk aile / isteği olan ilk aile: %+v", all)
	}
	// aile 0 alınamadı: aile 1 verisi olsa da kimse ölçülmedi
	if got := rtPickFirst([]int{0, 1}, map[int]map[string]rtPoint{1: got[1]}); len(got) != 0 {
		t.Fatalf("üst aile alınamadıysa alt aile güvenilmez: %+v", got)
	}
	// aile 1 alınamadı: aile 0 servisleri ölçülür, yalnız aile 1'dekiler ölçülmez
	if got := rtPickFirst([]int{0, 1}, map[int]map[string]rtPoint{0: got[0]}); len(got) != 1 || got["orders"].fam != 0 {
		t.Fatalf("üst aile ölçülür: %+v", got)
	}
}

func TestRTDecide(t *testing.T) {
	const L = 600
	base := rtBaseOf(repeat(rtBaseP50, 30), repeat(rtBaseP90, 30))
	ok50, ok90 := rtRep(rtS(rtBaseP50, L)), rtRep(rtS(rtBaseP90, L))
	last3 := func(normal, bad, load float64) []impactSample {
		return []impactSample{rtS(normal, load), rtS(normal, load), rtS(bad, load), rtS(bad, load), rtS(bad, load)}
	}
	cases := []struct {
		name       string
		s50, s90   []impactSample
		base       rtBase
		open       bool
		fire, emit bool
		lead       int
		fired      [2]bool
		reason     string
	}{
		{"yalnız medyan", last3(rtBaseP50, 406.25, L), ok90, base, false, true, true, 0, [2]bool{true, false}, ""},
		{"yalnız en yavaş %10", ok50, last3(rtBaseP90, 3750, L), base, false, true, true, 1, [2]bool{false, true}, ""},
		{"ikisi: oranı büyük olan (p90 5×, p50 2,1×)", last3(rtBaseP50, 406.25, L), last3(rtBaseP90, 3750, L), base, false, true, true, 1, [2]bool{true, true}, ""},
		{"ikisi: oranı büyük olan (p50 6×, p90 2,4×)", last3(rtBaseP50, 1162.5, L), last3(rtBaseP90, 1800, L), base, false, true, true, 0, [2]bool{true, true}, ""},
		{"medyan göreli aşıldı, mutlak aşılmadı (+90 ms) → açmaz", last3(rtBaseP50, rtBaseP50+90, L), ok90, base, false, false, false, -1, [2]bool{}, "medyan: pencere temiz"},
		{"en yavaş %10 2 kat ama +1 sn değil (1500 < 1750) → açmaz", ok50, last3(rtBaseP90, 1500, L), base, false, false, false, -1, [2]bool{}, "pencere temiz"},
		{"2 ihlal → açmaz", []impactSample{rtS(rtBaseP50, L), rtS(rtBaseP50, L), rtS(rtBaseP50, L), rtS(500, L), rtS(500, L)}, ok90, base, false, false, false, -1, [2]bool{}, "ihlal 2/5"},
		{"3 ihlal ama en yeni temiz → açmaz", []impactSample{rtS(500, L), rtS(500, L), rtS(500, L), rtS(rtBaseP50, L), rtS(rtBaseP50, L)}, ok90, base, false, false, false, -1, [2]bool{}, "düzeliyor"},
		{"yük kapısı: dakikada 9 istek → açmaz", last3(rtBaseP50, 900, 9), last3(rtBaseP90, 9000, 9), base, false, false, false, -1, [2]bool{}, "yük az"},
		{"yük tam 10 → açar", last3(rtBaseP50, 900, 10), ok90, base, false, true, true, 0, [2]bool{true, false}, ""},
		{"taban yok → sessiz", last3(rtBaseP50, 900, L), last3(rtBaseP90, 9000, L), rtBaseOf(repeat(rtBaseP50, 19), repeat(rtBaseP90, 19)), false, false, false, -1, [2]bool{}, "eksik veri: taban örneği az"},
		{"yalnız medyanın tabanı var: p90 yargılanmaz", last3(rtBaseP50, 900, L), last3(rtBaseP90, 9000, L), rtBaseOf(repeat(rtBaseP50, 30), repeat(rtBaseP90, 5)), false, true, true, 0, [2]bool{true, false}, ""},
		{"yalnız p90'ın tabanı var: medyan yargılanmaz", last3(rtBaseP50, 900, L), last3(rtBaseP90, 9000, L), rtBaseOf(repeat(rtBaseP50, 5), repeat(rtBaseP90, 30)), false, true, true, 1, [2]bool{false, true}, ""},
		{"açık koşu: pencerede tek ihlal → uzar, ateşlemez", []impactSample{rtS(500, L), rtS(rtBaseP50, L), rtS(rtBaseP50, L), rtS(rtBaseP50, L), rtS(rtBaseP50, L)}, ok90, base, true, false, true, 0, [2]bool{}, ""},
		{"açık koşu: pencere temiz → uzamaz", ok50, ok90, base, true, false, false, -1, [2]bool{}, "pencere temiz"},
		{"ölçüm yok → sessiz", rtRep(impGap), rtRep(impGap), base, true, false, false, -1, [2]bool{}, "ölçüm yok"},
	}
	for _, c := range cases {
		v := rtDecide([2][]impactSample{c.s50, c.s90}, c.base, 20, c.open)
		if v.Fire != c.fire || v.Emit != c.emit || v.Lead != c.lead || v.Fired != c.fired || !strings.Contains(v.Reason, c.reason) {
			t.Errorf("%s: fire=%v emit=%v lead=%d fired=%v reason=%q", c.name, v.Fire, v.Emit, v.Lead, v.Fired, v.Reason)
		}
	}
	// eşikler: medyan max(× 1,5, + 100 ms), en yavaş %10 max(× 2, + 1000 ms)
	v := rtDecide([2][]impactSample{ok50, ok90}, base, 20, false)
	if !near(v.M[0].Threshold, rtBaseP50+100) || !near(v.M[1].Threshold, rtBaseP90+1000) {
		t.Fatalf("eşikler: %v %v", v.M[0].Threshold, v.M[1].Threshold)
	}
	big := rtDecide([2][]impactSample{ok50, ok90}, rtBaseOf(repeat(400, 30), repeat(3000, 30)), 20, false)
	if !near(big.M[0].Threshold, 600) || !near(big.M[1].Threshold, 6000) {
		t.Fatalf("büyük tabanda göreli eşik: %v %v", big.M[0].Threshold, big.M[1].Threshold)
	}
}

// Olay alanları: gözlenen / taban ateşleyen ölçünün; başlangıç ateşleyen
// ölçülerin ilk ihlal dakikası; kanıt kararı açıklar.
func TestRTEventFields(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	end := time.Date(2026, 11, 20, 12, 5, 0, 0, time.UTC)
	base := rtBaseOf(repeat(rtBaseP50, 30), repeat(rtBaseP90, 30))
	s := [2][]impactSample{
		{rtS(rtBaseP50, 600), impGap, rtS(406.25, 610), rtS(410, 620), rtS(420, 630)},
		{rtS(rtBaseP90, 600), impGap, rtS(rtBaseP90, 610), rtS(rtBaseP90, 620), rtS(rtBaseP90, 630)},
	}
	v := rtDecide(s, base, 20, false)
	ev := rtEvent("orders", s, v, end, 0, cfg, "weekday")
	onset := end.Add(-3 * time.Minute)
	if ev.ID != chstore.MLEventID(detSvcResponseTime, "orders", "") || ev.Detector != detSvcResponseTime || ev.Signal != chstore.MLSignalTrace ||
		ev.Service != "orders" || ev.Code != "" || ev.Direction != "up" || ev.Stage != chstore.MLStageShadow {
		t.Fatalf("kimlik: %+v", ev)
	}
	if ev.StartedAt != onset.UnixNano() || ev.LastSeen != end.UnixNano() || ev.WindowSec != uint32(cfg.BucketSec) {
		t.Fatalf("zaman: started=%d last=%d window=%d", ev.StartedAt, ev.LastSeen, ev.WindowSec)
	}
	if ev.Observed != 420 || ev.Baseline != rtBaseP50 || ev.Score < cfg.CriticalZ {
		t.Fatalf("değer: %+v", ev)
	}
	var m struct {
		Rule       string     `json:"rule"`
		Source     string     `json:"source"`
		Unit       string     `json:"unit"`
		Measure    string     `json:"measure"`
		Fired      bool       `json:"fired"`
		FiredM     []string   `json:"firedMeasures"`
		Threshold  float64    `json:"threshold"`
		Violations int        `json:"violations"`
		Window     int        `json:"window"`
		MinViol    int        `json:"minViolations"`
		MinLoad    float64    `json:"minLoad"`
		SampleSec  int        `json:"sampleSec"`
		MinuteFrom int64      `json:"minuteFrom"`
		P50s       []*float64 `json:"p50s"`
		P90s       []*float64 `json:"p90s"`
		Loads      []float64  `json:"loads"`
		Dwell      *int       `json:"dwell"`
		Median     struct {
			Baseline, Threshold, Observed float64
			Violations                    int
			Fired                         bool
		} `json:"median"`
		Slow struct {
			Baseline, Threshold float64
			Violations          int
			Fired               bool
		} `json:"slow"`
	}
	if err := json.Unmarshal([]byte(ev.Evidence), &m); err != nil {
		t.Fatal(err)
	}
	if m.Rule != "impact" || m.Source != "vm" || m.Unit != "ms" || m.Measure != svcMeasureP50 || !m.Fired || !reflect.DeepEqual(m.FiredM, []string{"median"}) ||
		m.Violations != 3 || m.Window != impactWindow || m.MinViol != impactViolations || m.MinLoad != 10 || m.SampleSec != 60 ||
		m.MinuteFrom != end.Add(-5*time.Minute).Unix() || !near(m.Threshold, rtBaseP50+100) || m.Dwell != nil {
		t.Fatalf("kanıt: %s", ev.Evidence)
	}
	if len(m.P50s) != 5 || m.P50s[1] != nil || m.P50s[4] == nil || *m.P50s[4] != 420 || len(m.P90s) != 5 || *m.P90s[0] != rtBaseP90 ||
		len(m.Loads) != 5 || m.Loads[2] != 610 || m.Loads[1] != 0 {
		t.Fatalf("dakika dizileri: %s", ev.Evidence)
	}
	if !m.Median.Fired || m.Median.Violations != 3 || m.Median.Observed != 420 || m.Slow.Fired || m.Slow.Violations != 0 || !near(m.Slow.Threshold, rtBaseP90+1000) || m.Slow.Baseline != rtBaseP90 {
		t.Fatalf("ölçü başına ayrıntı: %s", ev.Evidence)
	}
	if mk, ok := impactMarkOf(ev.Evidence); !ok || !mk.Fired || mk.Violations != 3 || mk.Window != 5 {
		t.Fatalf("etki damgası: %+v %v", mk, ok)
	}
	// ikisi de ateşler: ölçü = oranı büyük olan (p90), başlangıç en erken ateşleyen ölçünün ilk ihlali
	both := [2][]impactSample{
		{rtS(rtBaseP50, 600), rtS(rtBaseP50, 600), rtS(406.25, 600), rtS(406.25, 600), rtS(406.25, 600)},
		{rtS(rtBaseP90, 600), rtS(3750, 600), rtS(3750, 600), rtS(3750, 600), rtS(3750, 600)},
	}
	be := rtEvent("orders", both, rtDecide(both, base, 20, false), end, 0, cfg, "weekday")
	bm := evidenceMap(be.Evidence)
	if be.Observed != 3750 || be.Baseline != rtBaseP90 || bm["measure"] != svcMeasureP90 || fmt.Sprint(bm["firedMeasures"]) != "[median slow]" ||
		be.StartedAt != end.Add(-4*time.Minute).UnixNano() {
		t.Fatalf("iki ölçü: obs=%v base=%v started=%d %s", be.Observed, be.Baseline, be.StartedAt, be.Evidence)
	}
	// açık koşunun yazılmış başlangıcı daha erkense o kalır
	early := onset.Add(-7 * time.Minute).UnixNano()
	if got := rtEvent("orders", s, v, end, early, cfg, "weekday").StartedAt; got != early {
		t.Fatalf("açık koşunun başlangıcı korunmalı: %d", got)
	}
}

// ── dakika önbelleği, sorgu sayısı, yalıtım ──────────────────────────────

// Soğuk başlangıç 5 anlık sorgu; sonraki dakika 1; aynı pencere bitişi 0
// (erken tik yoklamanın dakikalarını kullanır). Aralık sorgusu hiç yok.
func TestRTMinutesQueryBudget(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	d, vm, fams := rtFixture(t0)
	vm.svcs["orders"] = rtIncident(6, 40, rtMedSlow, 600)
	deps := Deps{VM: vm, Backoff: newVMBackoff(), meter: newTickMeter()}
	ctx := context.Background()
	now := t0.Add(11*time.Minute + 30*time.Second) // pencere bitişi 10:09 → dakika 4..8
	end := svcImpactEnd(now, 120)
	w, err := d.rtMinutes(ctx, deps, fams, end, now)
	if err != nil || vm.instant != impactWindow || vm.ranges != 0 || w.blindFrom != -1 {
		t.Fatalf("soğuk başlangıç aile başına 5 anlık sorgu: err=%v anlık=%d aralık=%d kör=%d", err, vm.instant, vm.ranges, w.blindFrom)
	}
	for _, q := range vm.queries {
		if !strings.Contains(q, "[60s]") || !strings.Contains(q, "_bucket") {
			t.Fatalf("1 dk'lık bucket sorgusu bekleniyordu: %s", q)
		}
	}
	s, fam := rtSamples(w, "orders")
	want := []float64{rtBaseP50, rtBaseP50, 406.25, 406.25, 406.25}
	for i := range want {
		if !s[0][i].OK || !near(s[0][i].Value, want[i]) || s[0][i].Load != 600 || !near(s[1][i].Value, rtBaseP90) {
			t.Fatalf("örnek %d: %+v %+v", i, s[0][i], s[1][i])
		}
	}
	if fam != fams[0].idx {
		t.Fatalf("aile: %d", fam)
	}
	// erken tik: aynı pencere bitişi → sorgu yok
	if _, err := d.rtMinutes(ctx, deps, fams, svcImpactEnd(now.Add(5*time.Second), 120), now.Add(5*time.Second)); err != nil || vm.instant != impactWindow {
		t.Fatalf("aynı pencere bitişinde yeniden sorgu olmamalı: %v anlık=%d", err, vm.instant)
	}
	// sonraki dakika: yalnız yeni dakika sorulur
	next := now.Add(time.Minute)
	if _, err := d.rtMinutes(ctx, deps, fams, svcImpactEnd(next, 120), next); err != nil || vm.instant != impactWindow+1 {
		t.Fatalf("yeni dakikada 1 sorgu: %v anlık=%d", err, vm.instant)
	}
	// pencereden çıkan dakika saklanmaz
	if len(d.rtMin) != impactWindow {
		t.Fatalf("saklı dakika sayısı %d", len(d.rtMin))
	}
	// 5 dk sonra (yoklamasız zamanlı tik): pencere tamamen yeni → 5 sorgu
	later := next.Add(5 * time.Minute)
	if _, err := d.rtMinutes(ctx, deps, fams, svcImpactEnd(later, 120), later); err != nil || vm.instant != 2*impactWindow+1 {
		t.Fatalf("örtüşmeyen pencerede 5 sorgu: %v anlık=%d", err, vm.instant)
	}
	if deps.meter.wasSkipped() {
		t.Fatal("yanıt süresi sorguları tik ölçerine yazılmamalı")
	}
}

// Hata yalıtımı: düşen dakika saklanmaz ve sonraki geçişte yeniden sorulur;
// geçiş başına aile başına tek başarısız sorgu; ağır hata KENDİ backoff
// ailesine yazılır ve işlem düzeyi `trace:` ailesini bekletmez; atlama tik
// ölçerini "kör" işaretlemez.
func TestRTMinutesFailureIsIsolated(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	d, vm, fams := rtFixture(t0)
	pin := fams[0].pin
	vm.svcs["orders"] = rtIncident(6, 40, rtMedSlow, 600)
	bo := newVMBackoff()
	meter := newTickMeter()
	deps := Deps{VM: vm, Backoff: bo, meter: meter}
	ctx := context.Background()
	now := t0.Add(11*time.Minute + 30*time.Second)
	end := svcImpactEnd(now, 120)
	vm.fail = func(string) error { return errors.New("connection refused") }
	w, err := d.rtMinutes(ctx, deps, fams, end, now)
	if err == nil || vm.instant != 1 || w.blindFrom != fams[0].idx || len(d.rtMin) != 0 {
		t.Fatalf("hata: tek sorgu, hiçbir dakika saklanmaz: err=%v anlık=%d kör=%d saklı=%d", err, vm.instant, w.blindFrom, len(d.rtMin))
	}
	if s, fam := rtSamples(w, "orders"); s[0][4].OK || fam != -1 {
		t.Fatal("alınamayan dakika ölçülmüş sayılmamalı")
	}
	if bo.skipping(rtBackoff+pin.ID, now) {
		t.Fatal("ağ hatası backoff'a girmez")
	}
	// ağır hata → kendi ailesi beklemede; trace: ailesi değil; sonraki geçiş VM'e gitmez, ölçer işaretlenmez
	vm.fail = func(string) error { return errors.New("HTTP 422 maxSamplesPerQuery") }
	if _, err := d.rtMinutes(ctx, deps, fams, end, now); err == nil || vm.instant != 2 {
		t.Fatalf("ağır hata dönmeli: %v anlık=%d", err, vm.instant)
	}
	if !bo.skipping(rtBackoff+pin.ID, now.Add(time.Second)) || bo.skipping("trace:"+pin.ID, now.Add(time.Second)) ||
		bo.skipping(svcImpactBackoff+pin.ID, now.Add(time.Second)) || bo.skipping(rtBaseBackoff+pin.ID, now.Add(time.Second)) {
		t.Fatal("yalnız impact-rt ailesi beklemeye girmeli")
	}
	w, err = d.rtMinutes(ctx, deps, fams, end, now.Add(time.Second))
	if err != nil || vm.instant != 2 || w.blindFrom != fams[0].idx || meter.wasSkipped() {
		t.Fatalf("beklemede: sorgu yok, hata yok, kör işaretli, tik ölçeri temiz: err=%v anlık=%d kör=%d ölçer=%v", err, vm.instant, w.blindFrom, meter.wasSkipped())
	}
	// bekleme bitince düzelir: eksik dakikalar yeniden sorulur
	vm.fail = nil
	if w, err := d.rtMinutes(ctx, deps, fams, end, now.Add(6*time.Minute)); err != nil || w.blindFrom != -1 || vm.instant != 2+impactWindow {
		t.Fatalf("bekleme sonrası: err=%v kör=%d anlık=%d", err, w.blindFrom, vm.instant)
	}
}

// İki aile: üst sıradaki ailenin sorgusu düşerse alt aile o dakika güvenilmez
// (HTTP tabanıyla gRPC süresi kıyaslanmaz); alt aile düşerse üst ailenin
// servisleri ölçülmeye devam eder.
func TestRTMinutesFamilyOrder(t *testing.T) {
	pins := anomaly.REDFamilyPins()
	if len(pins) < 2 {
		t.Skip("tek aile")
	}
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	d, vm, _ := rtFixture(t0)
	d.labels[pins[1].ID] = traceFamilyLabels{svc: "service_name", route: "rpc_method", status: "rpc_grpc_status_code", at: time.Now(), ok: true}
	fams := d.cachedFamilies()
	vm.svcs["web"] = rtIncident(0, 0, rtNormal, 600)
	vm.svcs["rpc"] = rtIncident(0, 0, rtNormal, 600)
	vm.fam["web"], vm.fam["rpc"] = redSel(pins[0], partBucket), redSel(pins[1], partBucket)
	ctx := context.Background()
	now := t0.Add(11*time.Minute + 30*time.Second)
	end := svcImpactEnd(now, 120)
	// alt aile (1) düşer
	vm.fail = func(q string) error {
		if strings.Contains(q, redSel(pins[1], partBucket)) {
			return errors.New("connection refused")
		}
		return nil
	}
	w, err := d.rtMinutes(ctx, Deps{VM: vm, Backoff: newVMBackoff()}, fams, end, now)
	sw, _ := rtSamples(w, "web")
	sr, _ := rtSamples(w, "rpc")
	if err == nil || w.blindFrom != 1 || !sw[0][4].OK || sr[0][4].OK {
		t.Fatalf("alt aile düştü: err=%v kör=%d web=%v rpc=%v", err, w.blindFrom, sw[0][4].OK, sr[0][4].OK)
	}
	if note := rtNoteOf(w, fams, err, nil); !strings.HasPrefix(note, "kısmen ölçülüyor: ") {
		t.Fatalf("not: %q", note)
	}
	// üst aile (0) düşer: kimse ölçülmez, alt aileye sorgu da gitmez
	d2, vm2, _ := rtFixture(t0)
	d2.labels[pins[1].ID] = d.labels[pins[1].ID]
	vm2.svcs, vm2.fam = vm.svcs, vm.fam
	vm2.fail = func(q string) error {
		if strings.Contains(q, redSel(pins[0], partBucket)) {
			return errors.New("connection refused")
		}
		return nil
	}
	w2, err2 := d2.rtMinutes(ctx, Deps{VM: vm2, Backoff: newVMBackoff()}, d2.cachedFamilies(), end, now)
	sw2, _ := rtSamples(w2, "web")
	sr2, _ := rtSamples(w2, "rpc")
	if err2 == nil || w2.blindFrom != 0 || sw2[0][4].OK || sr2[0][4].OK || vm2.instant != 1 {
		t.Fatalf("üst aile düştü: err=%v kör=%d web=%v rpc=%v anlık=%d", err2, w2.blindFrom, sw2[0][4].OK, sr2[0][4].OK, vm2.instant)
	}
	if note := rtNoteOf(w2, d2.cachedFamilies(), err2, nil); !strings.HasPrefix(note, "ölçülemedi: ") {
		t.Fatalf("not: %q", note)
	}
}

// 5 dk taban serisi: aile başına 1 anlık sorgu (kova penceresi), iki part,
// kovada 10 istekten az varsa örnek yok; hata yalıtılır (kendi backoff ailesi).
func TestRTBaseSeries(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	_, vm, fams := rtFixture(t0)
	vm.svcs["orders"] = rtIncident(0, 0, rtNormal, 600)
	vm.svcs["tiny"] = rtIncident(0, 0, rtNormal, 1) // 5 dk'da 5 istek
	series := map[string]map[int64]float64{}
	last := t0.Add(10 * time.Minute)
	meter := newTickMeter()
	bo := newVMBackoff()
	deps := Deps{VM: vm, Backoff: bo, meter: meter}
	now := last.Add(8 * time.Minute)
	if err := rtBaseSeries(context.Background(), deps, fams, last.Add(5*time.Minute), last.Unix(), 300, series, now); err != nil {
		t.Fatal(err)
	}
	if vm.instant != 1 || vm.ranges != 0 || !strings.Contains(vm.queries[0], "[300s]") {
		t.Fatalf("aile başına 1 anlık sorgu, kova pencereli: %d %v", vm.instant, vm.queries)
	}
	p50 := series[chstore.MLSeriesKey("orders", tracePart(svcRoute, svcMeasureP50))]
	p90 := series[chstore.MLSeriesKey("orders", tracePart(svcRoute, svcMeasureP90))]
	if len(series) != 2 || !near(p50[last.Unix()], rtBaseP50) || !near(p90[last.Unix()], rtBaseP90) {
		t.Fatalf("taban serisi: %+v", series)
	}
	// part adları route döngülerinin okuduğu ölçülerle çakışmaz
	if _, m := splitTracePart(tracePart(svcRoute, svcMeasureP50)); m == "count" || m == "err" || m == "p95" || m == "slow" {
		t.Fatalf("ölçü adı çakışıyor: %s", m)
	}
	// ağır hata: hata döner, seri yazılmaz, kendi ailesi beklemede, tik ölçeri temiz
	vm.fail = func(string) error { return errors.New("HTTP 422 maxSamplesPerQuery") }
	s2 := map[string]map[int64]float64{}
	if err := rtBaseSeries(context.Background(), deps, fams, last.Add(5*time.Minute), last.Unix(), 300, s2, now); err == nil || len(s2) != 0 {
		t.Fatalf("hata dönmeli, seri yazılmamalı: %v %+v", err, s2)
	}
	pin := fams[0].pin
	if !bo.skipping(rtBaseBackoff+pin.ID, now.Add(time.Second)) || bo.skipping(rtBackoff+pin.ID, now.Add(time.Second)) || bo.skipping("trace:"+pin.ID, now.Add(time.Second)) {
		t.Fatal("yalnız impact-rt5 ailesi beklemeye girmeli")
	}
	q := vm.instant
	if err := rtBaseSeries(context.Background(), deps, fams, last.Add(5*time.Minute), last.Unix(), 300, s2, now.Add(time.Second)); err == nil || vm.instant != q || meter.wasSkipped() {
		t.Fatalf("beklemede: sorgu yok, not için hata döner, tik ölçeri temiz: %v", err)
	}
}

// Taban özeti TraceRED'in taban okumasından; yeterli örnek yoksa servis aday değil.
func TestRTBases(t *testing.T) {
	k := func(svc, m string) string { return chstore.MLSeriesKey(svc, tracePart(svcRoute, m)) }
	base := map[string][]float64{
		k("orders", svcMeasureP50): repeat(200, 30), k("orders", svcMeasureP90): repeat(900, 30),
		k("fresh", svcMeasureP50): repeat(200, 5), k("fresh", svcMeasureP90): repeat(900, 5),
		k("half", svcMeasureP90):     repeat(900, 25),
		k("orders", svcMeasureRatio): repeat(0.01, 30), chstore.MLSeriesKey("orders", tracePart("GET /a", "p95")): repeat(5, 30),
	}
	bs := rtBases(base)
	if len(bs) != 3 || bs["orders"].Median != [2]float64{200, 900} || bs["orders"].N != [2]int{30, 30} || bs["half"].N != [2]int{0, 25} {
		t.Fatalf("özet: %+v", bs)
	}
	if got := rtEligible(bs, 20); !reflect.DeepEqual(got, []string{"half", "orders"}) {
		t.Fatalf("tabanı yeterli servisler: %v", got)
	}
}

// ── kayan pencere simülasyonu ────────────────────────────────────────────

// rtSim — dakika dakika histogram üreten sahte VM + gerçek dakika önbelleği,
// koşu izleyicisi ve kalite süzgeci.
type rtSim struct {
	d     *TraceRED
	vm    *rtVM
	fams  []impFam
	tr    *tracker
	cfg   chstore.MLConfig
	t0    time.Time
	bases map[string]rtBase
	deps  Deps
}

func newRTSim(tickSec int) *rtSim {
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	d, vm, fams := rtFixture(t0)
	cfg := chstore.DefaultMLConfig()
	cfg.TickSec, cfg.IngestLagSec = tickSec, 120
	return &rtSim{d: d, vm: vm, fams: fams, tr: newTracker(), cfg: cfg, t0: t0, bases: map[string]rtBase{}, deps: Deps{VM: vm, Backoff: newVMBackoff()}}
}

func (s *rtSim) service(svc string, f func(int) (rtShape, float64, bool)) {
	s.vm.svcs[svc] = f
	s.bases[svc] = rtBaseOf(repeat(rtBaseP50, 30), repeat(rtBaseP90, 30))
}

// tick — t0 + k dk + 30 sn anındaki tik. Döner: yazılan olaylar, kapanışlar.
func (s *rtSim) tick(t *testing.T, k int) (kept, closing []chstore.MLEvent) {
	t.Helper()
	now := s.t0.Add(time.Duration(k)*time.Minute + 30*time.Second)
	tick := time.Duration(s.cfg.TickSec) * time.Second
	end := svcImpactEnd(now, s.cfg.IngestLagSec)
	w, err := s.d.rtMinutes(context.Background(), s.deps, s.fams, end, now)
	if err != nil {
		t.Fatalf("k=%d: %v", k, err)
	}
	deps := s.deps
	deps.openRun = func(id string) bool { return s.tr.openRun(id, now, tick) }
	deps.runStart = func(id string) int64 { return s.tr.startedOf(id, now, tick) }
	evs, _ := s.d.rtEvents(deps, w, s.bases, end, s.cfg, "weekday", now)
	kept, _ = qualityFilter(evs, s.cfg, s.tr, now)
	closing, _ = s.tr.closeStale(now, tickCoverage{})
	return kept, closing
}

// Dakikalık tik: yavaşlama 10:10'da başlar, 10:20'de biter. İlk olay üçüncü
// ihlal dakikası (10:12) ingest payını geçince (10:15:30 tiki); 15..22 ateşler,
// 23..26 uzar (gözlenen tabana dönmüştür: kalite süzgeci açık koşuyu kesmez),
// 27'de pencere temiz, 29'da kapanır. Üç biçim: yalnız medyan, yalnız en yavaş
// %10, ikisi.
func TestRTSlidingWindowMinuteTicks(t *testing.T) {
	for _, c := range []struct {
		name        string
		bad         rtShape
		measure     string
		obs, base   float64
		firedM      string
		recoverObs  float64
		recoverBase float64
	}{
		{"yalnız medyan", rtMedSlow, svcMeasureP50, 406.25, rtBaseP50, "[median]", rtBaseP50, rtBaseP50},
		{"yalnız en yavaş %10", rtTailSlow, svcMeasureP90, 3750, rtBaseP90, "[slow]", rtBaseP90, rtBaseP90},
		{"ikisi (oranı büyük olan: p90)", rtBothSlow, svcMeasureP90, 3750, rtBaseP90, "[median slow]", rtBaseP90, rtBaseP90},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newRTSim(60)
			s.cfg.DailyEventCap = 1 // uzatmalar tavan harcamaz: tek hak yeter
			s.service("orders", rtIncident(10, 20, c.bad, 600))
			s.service("checkout", rtIncident(0, 0, rtNormal, 600)) // hep sağlıklı
			onset := s.t0.Add(10 * time.Minute).UnixNano()
			var id string
			closed := 0
			for k := 5; k <= 40; k++ {
				kept, closing := s.tick(t, k)
				for _, e := range kept {
					if e.Service != "orders" {
						t.Fatalf("k=%d sağlıklı servis olay üretti: %+v", k, e)
					}
				}
				switch {
				case k < 15:
					if len(kept) != 0 {
						t.Fatalf("k=%d erken olay: %+v", k, kept)
					}
				case k <= 26:
					if len(kept) != 1 {
						t.Fatalf("k=%d olay bekleniyordu, %d geldi", k, len(kept))
					}
					e := kept[0]
					if id == "" {
						id = e.ID
					}
					if e.ID != id || e.StartedAt != onset {
						t.Fatalf("k=%d kimlik/başlangıç oynadı: id=%s started=%d (want %s / %d)", k, e.ID, e.StartedAt, id, onset)
					}
					m := evidenceMap(e.Evidence)
					fired, _ := m["fired"].(bool)
					wantObs, wantFired := c.obs, c.firedM
					if k > 22 {
						wantObs, wantFired = c.recoverObs, "[]"
					}
					if fired != (k <= 22) || !near(e.Observed, wantObs) || !near(e.Baseline, c.base) || m["measure"] != c.measure || fmt.Sprint(m["firedMeasures"]) != wantFired {
						t.Fatalf("k=%d fired=%v observed=%v baseline=%v measure=%v firedMeasures=%v", k, fired, e.Observed, e.Baseline, m["measure"], m["firedMeasures"])
					}
					if wantLast := s.t0.Add(time.Duration(k-2) * time.Minute).UnixNano(); e.LastSeen != wantLast {
						t.Fatalf("k=%d last_seen=%d want %d", k, e.LastSeen, wantLast)
					}
				default:
					if len(kept) != 0 {
						t.Fatalf("k=%d temiz pencerede olay: %+v", k, kept)
					}
				}
				for _, cl := range closing {
					closed++
					if k != 29 || cl.ID != id || cl.Stage != chstore.MLStageClosed || cl.StartedAt != onset {
						t.Fatalf("k=%d kapanış: %+v", k, cl)
					}
				}
			}
			if closed != 1 {
				t.Fatalf("tek kapanış bekleniyordu: %d", closed)
			}
			// 36 tik, pencere her tikte 1 dakika kayar: soğuk 5 + 35 yeni dakika
			if s.vm.instant != impactWindow+35 || s.vm.ranges != 0 {
				t.Fatalf("sorgu: dakika başına 1 anlık (%d), aralık %d", s.vm.instant, s.vm.ranges)
			}
		})
	}
}

// 5 dakikalık tik: pencere tikten tike kayar (örtüşmez); başlangıç ilk ihlal
// dakikasında kalır; dalgalı yavaşlama (pencerede tek ihlal) olayı kapatıp
// yeniden açmaz; az yüklü dakika (yük kapısı) ve scrape boşluğu ihlal sayılmaz.
func TestRTSlidingWindowFiveMinuteTicks(t *testing.T) {
	s := newRTSim(300)
	s.service("orders", func(m int) (rtShape, float64, bool) {
		switch {
		case m >= 10 && m < 18:
			return rtTailSlow, 600, true
		case m >= 18 && m < 33 && m%4 == 0:
			return rtTailSlow, 600, true
		case m == 21:
			return rtNormal, 0, false // scrape boşluğu
		}
		return rtNormal, 600, true
	})
	s.service("quiet", func(m int) (rtShape, float64, bool) { return rtBothSlow, 5, true }) // hep yavaş ama dakikada 5 istek
	onset := s.t0.Add(10 * time.Minute).UnixNano()
	var id string
	for k := 5; k <= 60; k += 5 {
		kept, closing := s.tick(t, k)
		for _, e := range kept {
			if e.Service == "quiet" {
				t.Fatalf("k=%d yük kapısının altındaki servis olay üretti: %+v", k, e)
			}
		}
		switch {
		case k < 15:
			if len(kept) != 0 {
				t.Fatalf("k=%d erken olay", k)
			}
		case k <= 35:
			if len(kept) != 1 {
				t.Fatalf("k=%d olay bekleniyordu, %d geldi", k, len(kept))
			}
			if id == "" {
				id = kept[0].ID
			}
			if kept[0].ID != id || kept[0].StartedAt != onset {
				t.Fatalf("k=%d kimlik/başlangıç oynadı: %+v", k, kept[0])
			}
		default:
			if len(kept) != 0 {
				t.Fatalf("k=%d temiz pencerede olay", k)
			}
		}
		if len(closing) > 0 && k != 50 {
			t.Fatalf("k=%d beklenmeyen kapanış (3 kaçırılan tik: 40, 45, 50)", k)
		}
	}
}

// Günlük tavan yalnız YENİ olayı sayar; tavan doluyken açık olay uzar.
func TestRTDayCap(t *testing.T) {
	s := newRTSim(60)
	s.cfg.DailyEventCap = 1
	s.service("orders", rtIncident(10, 40, rtMedSlow, 600))
	s.service("checkout", rtIncident(20, 40, rtMedSlow, 600))
	seen := map[string]int{}
	for k := 5; k <= 35; k++ {
		kept, _ := s.tick(t, k)
		for _, e := range kept {
			seen[e.Service]++
		}
	}
	if seen["orders"] != 21 || seen["checkout"] != 0 {
		t.Fatalf("tavan: orders 15..35 arası her tik (21), checkout hiç: %v", seen)
	}
}

// Açık koşu ÖLÇÜLEMEDİYSE (ailesi beklemede / düştü) kaçırma sayılmaz, tutulur;
// ölçüldü ve olay yoksa kaçırma sayılır ve 3 tikte kapanır. Başka dedektörün
// koşusu bu kancadan etkilenmez.
func TestRTRunBlindHoldsOpenRun(t *testing.T) {
	pins := anomaly.REDFamilyPins()
	d := NewTraceRED()
	tr := newTracker()
	tick := 5 * time.Minute
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	open := func(det, svc, evidence string) string {
		ev := chstore.MLEvent{ID: chstore.MLEventID(det, svc, ""), Detector: det, Service: svc, Evidence: evidence}
		_, ep := tr.observe(ev, now, tick)
		ev.ID = ep
		tr.setEvent(ev)
		return ep
	}
	rt := open(detSvcResponseTime, "orders", fmt.Sprintf(`{"rule":"impact","redFamily":%q}`, pins[0].ID))
	open(detSvcErrorRate, "orders", `{"rule":"impact"}`)
	cov := tickCoverage{runBlind: runBlindOf([]Detector{d})}
	if d.runBlind(chstore.MLEvent{Detector: detSvcResponseTime}) {
		t.Fatal("dedektör hiç koşmadıysa kör sayılmaz")
	}
	d.setRTState("ölçülemedi: x", 0) // ilk aile ölçülemedi
	var closedIDs []string
	for i := 1; i <= episodeCloseMisses; i++ {
		closing, _ := tr.closeStale(now.Add(time.Duration(i)*tick), cov)
		for _, c := range closing {
			closedIDs = append(closedIDs, c.ID)
		}
	}
	if len(closedIDs) != 1 || closedIDs[0] == rt {
		t.Fatalf("yalnız hata oranı koşusu kapanmalı, yanıt süresi koşusu tutulmalı: %v", closedIDs)
	}
	if !tr.openRun(chstore.MLEventID(detSvcResponseTime, "orders", ""), now.Add(3*tick), tick) {
		t.Fatal("ölçülemeyen koşu açık kalmalı")
	}
	// ölçüm döndü: kaçırma sayılır, 3 tikte kapanır
	d.setRTState("", -1)
	n := 0
	for i := 4; i <= 3+episodeCloseMisses; i++ {
		closing, _ := tr.closeStale(now.Add(time.Duration(i)*tick), cov)
		n += len(closing)
	}
	if n != 1 {
		t.Fatalf("ölçülen ve olaysız koşu 3 tikte kapanmalı: %d", n)
	}
	// alt aile ölçülemedi: üst ailedeki koşu kör değil, alt / bilinmeyen ailedeki kör
	d.setRTState("kısmen ölçülüyor: x", 1)
	if d.runBlind(chstore.MLEvent{Detector: detSvcResponseTime, Evidence: fmt.Sprintf(`{"redFamily":%q}`, pins[0].ID)}) ||
		!d.runBlind(chstore.MLEvent{Detector: detSvcResponseTime, Evidence: `{"redFamily":"bilinmeyen"}`}) ||
		d.runBlind(chstore.MLEvent{Detector: detOpLatency, Evidence: `{"redFamily":"bilinmeyen"}`}) {
		t.Fatal("aile sırasına göre körlük")
	}
	if len(pins) > 1 && !d.runBlind(chstore.MLEvent{Detector: detSvcResponseTime, Evidence: fmt.Sprintf(`{"redFamily":%q}`, pins[1].ID)}) {
		t.Fatal("ölçülemeyen ailedeki koşu kör sayılmalı")
	}
	d.clearRT()
	if d.runBlind(chstore.MLEvent{Detector: detSvcResponseTime}) || d.Unmeasured() != nil {
		t.Fatal("dedektör kapanınca durum silinmeli")
	}
}

// Tik durumu notu: yalnız bu dedektör; hata oranı notundan bağımsız.
func TestRTUnmeasuredNote(t *testing.T) {
	d := NewTraceRED()
	d.setRTState("ölçülemedi: taban oluşuyor", -1)
	if u := d.Unmeasured(); len(u) != 1 || !strings.HasPrefix(u[detSvcResponseTime], "ölçülemedi: ") {
		t.Fatalf("%v", u)
	}
	d.setImpactNote(errors.New("x"))
	if u := d.Unmeasured(); len(u) != 2 || u[detSvcErrorRate] == "" {
		t.Fatalf("%v", u)
	}
	d.setRTState("", -1)
	if u := d.Unmeasured(); len(u) != 1 || u[detSvcResponseTime] != "" {
		t.Fatalf("%v", u)
	}
	if n := rtNoteOf(rtWindow{blindFrom: -1}, nil, nil, errors.New("vm http/svc_rt_5m: timeout")); !strings.HasPrefix(n, "kısmen ölçülüyor: taban örneği yazılamadı") {
		t.Fatalf("taban notu: %q", n)
	}
	if n := rtNoteOf(rtWindow{blindFrom: -1}, nil, nil, nil); n != "" {
		t.Fatalf("temiz: %q", n)
	}
}

// ── yoklama ──────────────────────────────────────────────────────────────

func rtProbeCfg() chstore.MLConfig {
	cfg := chstore.DefaultMLConfig()
	cfg.Enabled = true
	return cfg
}

func (d *TraceRED) testSetRTBase(b map[string]rtBase, at time.Time) {
	d.mu.Lock()
	d.rtBase, d.rtBaseAt = b, at
	d.mu.Unlock()
}

// Yoklama: taban son tikin özetinden; taban yok / yetersiz / bayat → sessiz ve
// SORGUSUZ; karar tikle aynı; hata döner.
func TestSvcResponseTimeProbe(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	now := t0.Add(11*time.Minute + 30*time.Second) // pencere bitişi 10:09 → dakika 4..8
	cfg := rtProbeCfg()
	ctx := context.Background()
	tick := time.Duration(cfg.TickSec) * time.Second
	d, vm, _ := rtFixture(t0)
	vm.svcs["orders"] = rtIncident(6, 40, rtTailSlow, 600)
	vm.svcs["checkout"] = rtIncident(0, 0, rtNormal, 600)
	deps := Deps{VM: vm, Backoff: newVMBackoff()}
	p := svcRTProbe{d}
	if c, err := p.probeImpact(ctx, deps, cfg, now); err != nil || len(c) != 0 || vm.instant != 0 {
		t.Fatalf("taban yokken sessiz ve sorgusuz: %v %v sorgu=%d", c, err, vm.instant)
	}
	// hiçbir servisin tabanı yeterli değil → sorgu yok (yeni kurulumda yoklama bedava)
	d.testSetRTBase(map[string]rtBase{"orders": rtBaseOf(repeat(rtBaseP50, 5), repeat(rtBaseP90, 5))}, now.Add(-time.Minute))
	if c, err := p.probeImpact(ctx, deps, cfg, now); err != nil || len(c) != 0 || vm.instant != 0 {
		t.Fatalf("taban yetersizken sessiz ve sorgusuz: %v %v sorgu=%d", c, err, vm.instant)
	}
	d.testSetRTBase(rtTestBases("orders", "checkout"), now.Add(-time.Minute))
	c, err := p.probeImpact(ctx, deps, cfg, now)
	if err != nil || candServices(c) != "orders" || c[0].BaseID != chstore.MLEventID(detSvcResponseTime, "orders", "") || vm.instant != impactWindow || vm.ranges != 0 {
		t.Fatalf("orders adayı bekleniyordu: %v %v sorgu=%d", c, err, vm.instant)
	}
	// sonraki dakikanın yoklaması: 1 sorgu
	if c, _ := p.probeImpact(ctx, deps, cfg, now.Add(time.Minute)); candServices(c) != "orders" || vm.instant != impactWindow+1 {
		t.Fatalf("sonraki yoklama 1 sorgu: %v sorgu=%d", c, vm.instant)
	}
	// 2 ihlal dakikası: aday yok
	d2, vm2, _ := rtFixture(t0)
	vm2.svcs["orders"] = rtIncident(7, 40, rtTailSlow, 600)
	d2.testSetRTBase(rtTestBases("orders"), now.Add(-time.Minute))
	if c, _ := (svcRTProbe{d2}).probeImpact(ctx, Deps{VM: vm2, Backoff: newVMBackoff()}, cfg, now); len(c) != 0 {
		t.Fatalf("2 ihlalde aday olmamalı: %v", c)
	}
	// bayat taban → sorgusuz
	d.testSetRTBase(rtTestBases("orders"), now.Add(-impactBaseMaxTicks*tick-time.Second))
	q := vm.instant
	if c, _ := p.probeImpact(ctx, deps, cfg, now.Add(2*time.Minute)); len(c) != 0 || vm.instant != q {
		t.Fatalf("bayat taban yok sayılmalı: %v sorgu=%d→%d", c, q, vm.instant)
	}
	// sorgu hatası: hata döner, aday yok
	d.testSetRTBase(rtTestBases("orders"), now.Add(2*time.Minute))
	vm.fail = func(string) error { return errors.New("connection refused") }
	if c, err := p.probeImpact(ctx, deps, cfg, now.Add(3*time.Minute)); err == nil || len(c) != 0 {
		t.Fatalf("sorgu hatası dönmeli, aday olmamalı: %v %v", c, err)
	}
	// VM yok / anahtar
	if c, err := p.probeImpact(ctx, Deps{}, cfg, now); err != nil || len(c) != 0 {
		t.Fatalf("VM yokken sessiz: %v %v", c, err)
	}
	off := rtProbeCfg()
	off.Detectors[detSvcResponseTime] = false
	onlyRT := rtProbeCfg()
	onlyRT.Detectors[detSvcErrorRate] = false
	if !p.probeOn(cfg) || p.probeOn(off) || !p.probeOn(onlyRT) || d.probeOn(onlyRT) {
		t.Fatal("her yoklama yalnız kendi dedektörü açıkken")
	}
	if ps := impactProbersOf(d); len(ps) != 2 {
		t.Fatalf("TraceRED iki yoklama taşır (hata oranı + yanıt süresi): %d", len(ps))
	}
	if ps := impactProbersOf(&LogRED{}); len(ps) != 0 {
		t.Fatalf("yoklaması olmayan dedektör: %d", len(ps))
	}
}

// Recorder: yanıt süresi adayı erken tik açar, bekleme süresince yeniden
// açmaz, açık koşu varken açmaz; erken tik yoklamanın dakikalarını kullanır.
func TestSvcResponseTimeProbeTriggersEarlyTick(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	now := t0.Add(11*time.Minute + 30*time.Second)
	cfg := rtProbeCfg()
	cfg.Detectors[detSvcErrorRate] = false
	ctx := context.Background()
	d, vm, fams := rtFixture(t0)
	vm.svcs["orders"] = rtIncident(6, 40, rtMedSlow, 600)
	d.testSetRTBase(rtTestBases("orders"), now.Add(-time.Minute))
	r := &Recorder{detectors: []Detector{d}, runs: newTracker(), backoff: newVMBackoff(), vm: vm}
	r.admit(cfg, true, true, nil, false)
	if !r.probeImpact(ctx, cfg, true, now) || vm.instant != impactWindow || vm.ranges != 0 {
		t.Fatalf("ateşleyen servis erken tik istemeli (5 anlık sorgu, aralık yok): anlık=%d aralık=%d", vm.instant, vm.ranges)
	}
	// erken tik aynı pencere bitişinde: sorgu yok, aynı karar
	early := now.Add(5 * time.Second)
	end := svcImpactEnd(early, cfg.IngestLagSec)
	w, err := d.rtMinutes(ctx, Deps{VM: vm, Backoff: r.backoff}, fams, end, early)
	if err != nil || vm.instant != impactWindow {
		t.Fatalf("erken tik yoklamanın dakikalarını kullanmalı: %v anlık=%d", err, vm.instant)
	}
	evs, _ := d.rtEvents(Deps{}, w, rtTestBases("orders"), end, cfg, "weekday", early)
	if len(evs) != 1 || evs[0].Service != "orders" {
		t.Fatalf("erken tik olayı yazmalı: %+v", evs)
	}
	if r.probeImpact(ctx, cfg, true, now.Add(time.Minute)) {
		t.Fatal("bekleme süresi içinde aynı aday yeniden erken tik açmamalı")
	}
	at := now.Add(impactProbeCooldown + 2*time.Minute)
	d.testSetRTBase(rtTestBases("orders"), at)
	r.runs.observe(chstore.MLEvent{ID: chstore.MLEventID(detSvcResponseTime, "orders", ""), Detector: detSvcResponseTime, Service: "orders"}, at, 5*time.Minute)
	if r.probeImpact(ctx, cfg, true, at) {
		t.Fatal("açık koşusu olan servis erken tik açmamalı")
	}
}

// ── Problem kararı ───────────────────────────────────────────────────────

func rtProblemEv(id, svc string, startMin int, part string, base, obs float64) chstore.MLEvent {
	e := svcEv(id, detSvcResponseTime, svc, startMin, base, obs)
	e.Evidence = fmt.Sprintf(`{"rule":"impact","source":"vm","measure":%q,"fired":true}`, part)
	return e
}

func TestSvcResponseTimeProblemDecision(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	prio := chstore.DefaultProblemPriority()
	// tek başına: medyan 1,6 × → P2; medyan ≥ 2 × → P1; en yavaş %10 (kural gereği ≥ 2 ×) → P1
	for _, c := range []struct {
		name      string
		ev        chstore.MLEvent
		prio      string
		value, th float64
	}{
		{"medyan 1,6 × (taban 400 ms) → P2", rtProblemEv("r1", "orders", 0, svcMeasureP50, 400, 640), "P2", 640, 400},
		{"medyan 2 × → P1", rtProblemEv("r1", "orders", 0, svcMeasureP50, 400, 800), "P1", 800, 400},
		{"medyan taban 50 ms, eşik 150 ms (3 ×) → P1", rtProblemEv("r1", "orders", 0, svcMeasureP50, 50, 150), "P1", 150, 50},
		{"en yavaş %10 kuralın eşiğinde (2 ×) → P1", rtProblemEv("r1", "orders", 0, svcMeasureP90, 1500, 3000), "P1", 3000, 1500},
	} {
		d := decideSvcShadow([]chstore.MLEvent{c.ev}, nil, nil, prio, now)["r1"]
		if !d.WouldOpen || !d.Owner || d.RuleID != "anomaly-auto:svc:orders" || d.Priority != c.prio || d.Value != c.value || d.Threshold != c.th || d.JoinsRule != "" {
			t.Errorf("%s: %+v", c.name, d)
		}
	}
	// aynı serviste daha erken başlamış hata oranı olayı sahip; yanıt süresi üye, öncelik en yükseği
	ds := decideSvcShadow([]chstore.MLEvent{
		rtProblemEv("r1", "orders", 5, svcMeasureP90, 1500, 3000),
		svcEv("e1", detSvcErrorRate, "orders", 0, 0.05, 0.08)}, nil, nil, prio, now)
	if !ds["e1"].Owner || ds["r1"].Owner || !ds["r1"].WouldOpen || ds["r1"].Priority != "P1" || ds["e1"].Priority != "P1" || !strings.Contains(ds["r1"].Reason, "üye") {
		t.Fatalf("üyelik: %+v", ds)
	}
	// yaygın yavaşlama sahipken de üye
	ds = decideSvcShadow([]chstore.MLEvent{
		rtProblemEv("r1", "orders", 5, svcMeasureP50, 400, 640),
		svcEv("b1", detSvcBreadth, "orders", 0, 1000, 1500)}, nil, nil, prio, now)
	if !ds["b1"].Owner || ds["r1"].Owner || !ds["r1"].WouldOpen {
		t.Fatalf("yaygın yavaşlama sahip: %+v", ds)
	}
	// canlı girdi: tek servis satırı, cümle + VM kaynak işareti, ölçü gecikme ailesinde
	one := rtProblemEv("r1", "orders", 0, svcMeasureP50, 180, 920)
	in := svcProblemInputs([]chstore.MLEvent{one}, decideSvcShadow([]chstore.MLEvent{one}, nil, nil, prio, now))
	if len(in) != 1 || in[0].Metric != "p50_ms" || in[0].Value != 920 || in[0].Threshold != 180 ||
		in[0].Desc != "orders servisinde yanıt süresi (medyan) normalde 180 ms, şimdi 920 ms (5,1 kat). "+chstore.REDEvidenceVM+"." {
		t.Fatalf("girdi: %+v", in)
	}
	p := newSvcProblem(in[0])
	if chstore.ProblemCategory(p) != chstore.CategorySlowdown || p.Severity != "critical" || p.ID != "anomaly-auto:svc:orders" {
		t.Fatalf("Problem satırı: %+v", p)
	}
	if h, ok := chstore.MLPromotedHeadline(p); !ok || h != "orders servisinde yanıt süresi (medyan) normalde 180 ms, şimdi 920 ms (5,1 kat)." {
		t.Fatalf("manşet: %q", h)
	}
	slow := rtProblemEv("r2", "orders", 0, svcMeasureP90, 800, 2900)
	in = svcProblemInputs([]chstore.MLEvent{slow}, decideSvcShadow([]chstore.MLEvent{slow}, nil, nil, prio, now))
	if in[0].Metric != "p90_ms" || !strings.HasPrefix(in[0].Desc, "orders servisinde yanıt süresi (en yavaş %10) normalde 800 ms, şimdi 2.900 ms (3,6 kat).") ||
		chstore.ProblemCategory(newSvcProblem(in[0])) != chstore.CategorySlowdown {
		t.Fatalf("en yavaş %%10 girdisi: %+v", in)
	}
	// üç dedektör aynı serviste: tek satır, cümle sırası hata oranı → yanıt süresi → yaygın yavaşlama
	three := []chstore.MLEvent{svcEv("b1", detSvcBreadth, "orders", 2, 1000, 1500), one, svcEv("e1", detSvcErrorRate, "orders", 1, 0.004, 0.03)}
	in = svcProblemInputs(three, decideSvcShadow(three, nil, nil, prio, now))
	i1, i2, i3 := strings.Index(in[0].Desc, "hata oranı"), strings.Index(in[0].Desc, "yanıt süresi"), strings.Index(in[0].Desc, "operasyon birden")
	if len(in) != 1 || i1 < 0 || i2 < i1 || i3 < i2 || len(in[0].Events) != 3 {
		t.Fatalf("üç olay tek satır: %+v", in)
	}
	// plan: servisin açık sahibi varsa yeni satır açılmaz, olay ona bağlanır
	owner := newSvcProblem(svcIn("orders", 0.05, 0.01, "e1"))
	plan := planMLProblems(nil, svcProblemInputs([]chstore.MLEvent{one}, decideSvcShadow([]chstore.MLEvent{one}, nil, nil, prio, now)), []chstore.Problem{owner}, true, descStub)
	if len(plan.Open) != 0 || plan.LiveSvc["r1"] != owner.ID {
		t.Fatalf("açık sahip varken üye olmalı: %+v", plan)
	}
	alone := planMLProblems(nil, svcProblemInputs([]chstore.MLEvent{one}, decideSvcShadow([]chstore.MLEvent{one}, nil, nil, prio, now)), nil, true, descStub)
	if len(alone.Open) != 1 || alone.Open[0].ID != "anomaly-auto:svc:orders" || !alone.SvcOpen[alone.Open[0].ID] || freshPriority(alone.Open[0]) != "P1" {
		t.Fatalf("tek başına Problem açmalı: %+v", alone)
	}
}

// ── kayıt (anomali kartı sözleşmesi) ─────────────────────────────────────

func TestSvcResponseTimeRecordRoundTrip(t *testing.T) {
	from := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for mi, m := range rtMeasures {
		base := []float64{180, 800}[mi]
		bad := []float64{920, 2900}[mi]
		for _, closed := range []bool{false, true} {
			var pts, calls []chstore.SpanMetricPoint
			for i := 0; i <= 60; i++ {
				at := from.Add(time.Duration(i) * time.Minute).UnixNano()
				v := base
				switch {
				case i == 50:
					v = bad * 1.5
				case i >= 40 && i < 55:
					v = bad
				}
				pts = append(pts, chstore.SpanMetricPoint{Time: at, Value: v})
				calls = append(calls, chstore.SpanMetricPoint{Time: at, Value: 100})
			}
			exp, lo, hi, method := rtRecordBand(m, base)
			rec, ok := BuildREDRecord(REDRecordInput{Detector: detSvcResponseTime, Service: "orders", Family: "http",
				Series:    latSeries{Query: "histogram_quantile(q, x[60s])", VolumeQuery: "v", Unit: "ms", Aggregation: m.agg, Scale: 1},
				Direction: "up", BucketSec: impactStepSec, Expected: exp, Lo: lo, Hi: hi, Method: method,
				DetectedAt: from.Add(45 * time.Minute), Closed: closed, Points: pts, Calls: calls, From: from, To: from.Add(time.Hour),
				Confirmers: []string{"op_latency:abc", "svc_breadth:def"}, Head: "Yanıt süresi (" + m.agg + ")", Label: "yanıt süresi (" + m.agg + ")", Fmt: msFmt})
			if !ok {
				t.Fatal("kayıt kurulmalı")
			}
			if vs := ValidateRecord(rec); len(vs) != 0 {
				t.Fatalf("%s closed=%v doğrulayıcıdan geçmeli: %+v (%s)", m.key, closed, vs, rec.Title)
			}
			if rec.OnsetAt != from.Add(40*time.Minute).UnixNano() || rec.PeakValue != bad*1.5 || rec.ValueAtDetection != bad ||
				rec.Baseline.Lo != 0 || rec.Baseline.Hi != impactRules[m.kind].threshold(base) || rec.Baseline.Expected != base || rec.Subject.Operation != "" {
				t.Fatalf("%s seriden türetme / bant: %+v", m.key, rec)
			}
			if closed != (rec.EndedAt == from.Add(55*time.Minute).UnixNano()) || rec.Series.StepSec != 60 || rec.Snapshot.StepSec != 60 || rec.Class != ClassServiceAnomaly {
				t.Fatalf("%s closed=%v ended=%d", m.key, closed, rec.EndedAt)
			}
			// gölge yayın kapıları: gecikme serisi (art arda ≥ 3 dk, ≥ 100 çağrı, ≥ 25 ms fark), 2 bağımsız kanıt
			gates := EvaluateGates(rec)
			if o := gateVerdict(rec, gates); o.Drop || o.WouldClass != "" || len(o.Failed) != 0 {
				t.Fatalf("%s gölge kapıları geçmeli: %+v %+v", m.key, o, gates)
			}
			if seriesMeasure(rec.Series) != "latency" {
				t.Fatal("kayıt gecikme ölçüsü olmalı")
			}
			raw, _ := json.Marshal(rec)
			var back EventRecord
			if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, rec) {
				t.Fatalf("gidiş-dönüş: %v", err)
			}
		}
	}
	if _, lo, hi, method := rtRecordBand(rtMeasures[0], 400); lo != 0 || hi != 600 || method != rtBandMethodMedian {
		t.Fatalf("medyan bandı: %v %v %s", lo, hi, method)
	}
	if _, lo, hi, method := rtRecordBand(rtMeasures[1], 400); lo != 0 || hi != 1400 || method != rtBandMethodSlow {
		t.Fatalf("en yavaş %%10 bandı: %v %v %s", lo, hi, method)
	}
}

// rtRecVM — kayıt aralık sorgularını yakalayan sahte VM: yüzdelik sorgusuna
// dakikalık seri (tespitten 4 dk önce 180 → 920 ms), hacme 600 döner.
type rtRecVM struct {
	fakeVM
	det     time.Time
	bad     float64
	queries []string
	froms   []time.Time
	tos     []time.Time
}

func (f *rtRecVM) QueryPromQLRange(_ context.Context, q string, from, to time.Time, _, _ int) ([]chstore.SpanMetricSeries, error) {
	f.queries, f.froms, f.tos = append(f.queries, q), append(f.froms, from), append(f.tos, to)
	var pts []chstore.SpanMetricPoint
	for at := from; !at.After(to); at = at.Add(time.Minute) {
		v := 180.0
		if strings.HasPrefix(q, "sum(increase(") {
			v = 600
		} else if !at.Before(f.det.Add(-4 * time.Minute)) {
			v = f.bad
		}
		pts = append(pts, chstore.SpanMetricPoint{Time: at.UnixNano(), Value: v})
	}
	return []chstore.SpanMetricSeries{{Points: pts}}, nil
}

// Kayıt dedektörün yargıladığı 1 DAKİKALIK seridir: tek servisin yüzdeliği,
// [60s], dakika başına hizalı, ölçülen son tam dakikada biter, bant = [0, eşik].
func TestSvcResponseTimeRecordIsJudgedSeries(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 8, 30, 0, time.UTC)
	det := now.Add(-30 * time.Second) // 12:08:00
	pin := anomaly.REDFamilyPins()[0]
	cfg := chstore.DefaultMLConfig()
	for _, c := range []struct {
		part, q, method string
		hi, bad         float64
	}{
		{svcMeasureP50, "histogram_quantile(0.5,", rtBandMethodMedian, 280, 920},
		{svcMeasureP90, "histogram_quantile(0.9,", rtBandMethodSlow, 1180, 2900},
	} {
		vm := &rtRecVM{det: det, bad: c.bad}
		evs := []chstore.MLEvent{{ID: "ep1", Detector: detSvcResponseTime, Signal: chstore.MLSignalTrace, Service: "orders", Stage: chstore.MLStageShadow,
			Baseline: 180, Observed: c.bad, StartedAt: det.Add(-4 * time.Minute).UnixNano(), LastSeen: det.UnixNano(), WindowSec: 300,
			Evidence: fmt.Sprintf(`{"rule":"impact","measure":%q,"redFamily":%q,"svcLabel":"service_name"}`, c.part, pin.ID)},
			{ID: "other", Detector: detSvcErrorRate, Service: "orders"}}
		budget := 5
		attachSvcRTRecords(context.Background(), Deps{VM: vm}, cfg, evs, false, func(string) time.Time { return det }, nil, &budget, now)
		if budget != 4 || len(vm.queries) != 2 {
			t.Fatalf("olay başına 2 aralık sorgusu, yalnız bu dedektör: bütçe %d, sorgu %d", budget, len(vm.queries))
		}
		q := vm.queries[0]
		if !strings.HasPrefix(q, c.q) || !strings.Contains(q, "[60s]") || !strings.Contains(q, `service_name="orders"`) || !strings.Contains(q, "sum by (le)") ||
			!strings.Contains(q, redSel(pin, partBucket, mEq("service_name", "orders"))) || strings.Contains(q, "http_route") {
			t.Fatalf("ifade tek servisin 1 dk'lık yüzdeliği olmalı: %s", q)
		}
		if want := det.Add(-5*time.Minute - lagRecordPad); !vm.froms[0].Equal(want) || vm.froms[0].Unix()%60 != 0 {
			t.Fatalf("pencere başı %s want %s (dakika başı)", vm.froms[0], want)
		}
		if want := svcImpactEnd(now, cfg.IngestLagSec); !vm.tos[0].Equal(want) || !vm.tos[1].Equal(want) || !want.Before(now) {
			t.Fatalf("pencere sonu %s / %s want %s", vm.tos[0], vm.tos[1], want)
		}
		ev := applyRecordContractShadow(evs[0])
		if ev.PublishState != PublishPublished || evs[1].Record != "" {
			t.Fatalf("kayıt doğrulayıcıdan geçmeli: %q", ev.PublishState)
		}
		var rec EventRecord
		if err := json.Unmarshal([]byte(ev.Record), &rec); err != nil {
			t.Fatal(err)
		}
		if rec.Series.StepSec != 60 || rec.Baseline.StepSec != 60 || rec.Snapshot.StepSec != 60 || rec.Series.Unit != "ms" || rec.Baseline.Method != c.method ||
			rec.Baseline.Lo != 0 || rec.Baseline.Hi != c.hi || rec.Series.Source != "vm" {
			t.Fatalf("seri/bant: %+v %+v", rec.Series, rec.Baseline)
		}
		if rec.OnsetAt != det.Add(-4*time.Minute).UnixNano() || rec.PeakValue != c.bad || rec.ValueAtDetection != c.bad || ev.OnsetAt != rec.OnsetAt {
			t.Fatalf("onset/tepe seriden: onset=%d peak=%v det=%v", rec.OnsetAt, rec.PeakValue, rec.ValueAtDetection)
		}
		if last := rec.Snapshot.T0 + int64(len(rec.Snapshot.V)-1)*60; last != svcImpactEnd(now, cfg.IngestLagSec).Unix() {
			t.Fatalf("kayıt ölçülen son tam dakikada bitmeli: %d", last)
		}
	}
	// aile kanıtı yoksa kayıt yazılmaz, sorgu da gitmez
	vm := &rtRecVM{det: det, bad: 920}
	evs := []chstore.MLEvent{{ID: "x", Detector: detSvcResponseTime, Service: "orders", Evidence: `{"rule":"impact"}`}}
	budget := 5
	attachSvcRTRecords(context.Background(), Deps{VM: vm}, cfg, evs, false, func(string) time.Time { return det }, nil, &budget, now)
	if budget != 5 || len(vm.queries) != 0 || evs[0].Record != "" {
		t.Fatal("aile kanıtı olmayan olaya kayıt / sorgu olmamalı")
	}
}

// ── kart metni ───────────────────────────────────────────────────────────

func TestSvcResponseTimeCard(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	end := time.Date(2026, 11, 20, 12, 5, 0, 0, time.UTC)
	mk := func(p50, p90 float64) chstore.MLEvent {
		s := [2][]impactSample{rtRep(rtS(p50, 600)), rtRep(rtS(p90, 600))}
		return rtEvent("orders", s, rtDecide(s, rtBaseOf(repeat(180, 30), repeat(800, 30)), 20, false), end, 0, cfg, "weekday")
	}
	in := CardInputFromEvent(mk(920, 800), end.Add(3*time.Minute), time.UTC)
	if in.ImpactRule == nil || in.DwellBuckets != 0 || in.Part != tracePart(svcRoute, svcMeasureP50) {
		t.Fatalf("girdi: %+v part=%q", in.ImpactRule, in.Part)
	}
	if got := ExplainSummary(in); !strings.HasPrefix(got, "orders: yanıt süresi (medyan) normalde 180 ms, şimdi 920 ms") {
		t.Fatalf("özet: %s", got)
	}
	in.DwellBuckets, in.ImpactMinRel = 2, 0.2 // olay anı kaydının eşikleri (ApplySnapshot)
	card := BuildExplainCard(in)
	if want := "Etki kuralı: son 5 dakikalık örnekte en az 3 ihlal · eşik 280 ms · dakikada en az 10 istek"; card.Technical.Thresholds != want {
		t.Fatalf("teknik satır:\n got %q\nwant %q", card.Technical.Thresholds, want)
	}
	if !strings.HasPrefix(card.HowMuch, "Yanıt süresi (medyan) 920 ms; ") {
		t.Fatalf("ne kadar: %s", card.HowMuch)
	}
	slow := CardInputFromEvent(mk(180, 2900), end.Add(3*time.Minute), time.UTC)
	if got := ExplainSummary(slow); !strings.HasPrefix(got, "orders: yanıt süresi (en yavaş %10) normalde 800 ms, şimdi 2,9 sn") {
		t.Fatalf("özet (en yavaş %%10): %s", got)
	}
	sc := BuildExplainCard(slow)
	if !strings.HasPrefix(sc.HowMuch, "Yanıt süresi (en yavaş %10) 2,9 sn; ") || !strings.Contains(sc.Technical.Thresholds, "eşik 1,8 sn") {
		t.Fatalf("en yavaş %%10 kartı: %s | %s", sc.HowMuch, sc.Technical.Thresholds)
	}
}

// ── kayıt (registration) ─────────────────────────────────────────────────

func TestSvcResponseTimeRegistration(t *testing.T) {
	const det = "svc_response_time"
	if detSvcResponseTime != det {
		t.Fatal("dedektör adı")
	}
	found := false
	for _, n := range chstore.MLTraceDetectorNames {
		found = found || n == det
	}
	cfg := chstore.DefaultMLConfig()
	if !found || chstore.MLDetectorSignal(det) != chstore.MLSignalTrace || !chstore.MLDetectorDefaultOn(det) || !cfg.Detectors[det] {
		t.Fatalf("ayar listesi / sinyal / varsayılan açık: found=%v", found)
	}
	// operatör kapatabilir: kayıtlı false korunur
	c := chstore.DefaultMLConfig()
	c.Detectors[det] = false
	if chstore.NormalizeMLConfig(c).Detectors[det] {
		t.Fatal("operatörün kapattığı değer korunmalı")
	}
	on := chstore.DefaultMLConfig()
	on.Enabled = true
	only := chstore.DefaultMLConfig()
	only.Enabled = true
	for k := range only.Detectors {
		only.Detectors[k] = k == det
	}
	if !(&TraceRED{}).Enabled(only) || !on.DetectorOn(det) {
		t.Fatal("yalnız bu dedektör açıkken de TraceRED koşmalı")
	}
	ev := chstore.MLEvent{Detector: det, Direction: "up", Evidence: `{"rule":"impact","fired":true}`}
	if !isSvcDetector(det) || !impactRuleRun(ev) || eventSourceMarker(ev) != chstore.REDEvidenceVM {
		t.Fatal("servis düzeyi yol / etki koşusu / kaynak işareti")
	}
	if w, cnt := singleWorsening(ev); w || cnt {
		t.Fatal("tek sinyal kolu servis düzeyi olayı saymaz (çift sayım olmaz)")
	}
	if batchMeasureOf(ev, nil) != batchMeasureOther {
		t.Fatal("gecikme batch kuralında yük değildir")
	}
	if UnifiedSignalOf(det, "") != chstore.MLSignalTrace {
		t.Fatal("birleşik liste sinyali")
	}
	if k, _, ok := mlSide(EngineMLIn{Detector: det, Service: "orders"}); !ok || k.family != EngFamLatency {
		t.Fatalf("motor karşılaştırması gecikme ailesi: %+v", k)
	}
	if got := eventPart(chstore.MLEvent{Detector: det, Signal: chstore.MLSignalTrace, Evidence: `{"measure":"svc_p90"}`}); got != "*|svc_p90" {
		t.Fatalf("oynatma serisi: %q", got)
	}
	if _, ok := mlDetSpecs[det]; !ok {
		t.Fatal("kart sözlüğü")
	}
	for _, f := range []struct{ path, want string }{
		{"../../frontend/src/pages/settings/MLSubsection.tsx", "id: 'svc_response_time'"},
		{"../../frontend/src/lib/mlDetectorLabels.ts", "svc_response_time: 'Servis yanıt süresi'"},
		{"../../frontend/src/lib/i18n.common.ts", "'mlDetector.svc_response_time'"}, // v0.10.2400 — EN_EAGER i18n.ts'ten taşındı
		{"../../frontend/src/lib/signalKind.ts", "'svc_response_time'"},
	} {
		if raw, err := os.ReadFile(f.path); err != nil || !strings.Contains(string(raw), f.want) {
			t.Errorf("%s: %s yok (%v)", f.path, f.want, err)
		}
	}
}
