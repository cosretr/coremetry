package mcptools

// v0.10.1299 — operatör 2026-10-17: 5 dk p99 gürültüsü yanlış bulgu üretiyordu.
// (1) gecikme adayı ham span ile doğrulanmadan bulgu olmaz; (2) operasyon
// grafiği ≤ 6 sa'te 1 dk tabanlı (spanmetrics_1m) p50 + p99 + önceki pencere
// p99'u (+ doğrulandıysa yavaş istek sayısı); (3) log örneği ham JSON değil
// kısa insan satırı (ham gövde yalnız açılır tabloda).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/healthverdict"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestSweepChartStep(t *testing.T) {
	for _, c := range []struct {
		win  time.Duration
		step int
		fine bool
	}{
		{15 * time.Minute, 60, true}, {time.Hour, 60, true}, {2 * time.Hour, 60, true},
		{3 * time.Hour, 120, true}, {6 * time.Hour, 180, true}, {12 * time.Hour, 600, false},
	} {
		step, fine := sweepChartStep(c.win)
		if step != c.step || fine != c.fine {
			t.Errorf("%s: step %d fine %v (beklenen %d %v)", c.win, step, fine, c.step, c.fine)
		}
		if pts := int(c.win/time.Second) / step; pts > evBlockMaxPoints {
			t.Errorf("%s: %d nokta > %d", c.win, pts, evBlockMaxPoints)
		}
	}
	if sweepBucketLabel(60) != "1 dk" || sweepBucketLabel(180) != "3 dk" {
		t.Fatal("kova etiketi")
	}
}

func TestSweepOpLatencyChart(t *testing.T) {
	base := sweepTo.Add(-time.Hour).Unix()
	var pts []chstore.OpLatencyPoint
	for i := int64(0); i < 60; i++ {
		pts = append(pts, chstore.OpLatencyPoint{TS: base + i*60, Calls: 100, Errors: uint64(i % 2), P50Ms: 120, P99Ms: 300})
	}
	corr := &chstore.SlowSpanCorroboration{ThresholdMs: 250, Minutes: []chstore.SlowSpanMinute{{TS: base + 60, Count: 4}, {TS: base + 120, Count: 6}}}
	c, ok := sweepOpLatencyChart("POST /pay", "", pts, 60, 200, corr, EvBlockWindow{})
	// v0.10.1391 — başlık "p50/p99" demez (count panelinde yanıltıcıydı).
	if !ok || c.Title != "POST /pay · 1 dk kova · örneklenmiş span (spanmetrics_1m)" {
		t.Fatalf("başlık %q", c.Title)
	}
	byName := map[string]EvChartSeries{}
	for _, s := range c.Series {
		byName[s.Name] = s
	}
	if byName["p50"].Style != "" || byName["p99"].Style != evStyleSecondary || byName["önceki pencere p99"].Style != evStyleDashed {
		t.Fatalf("stiller: %+v", c.Series)
	}
	if v := byName["önceki pencere p99"].Points[59][1]; v != 200 {
		t.Fatalf("baseline her noktada önceki pencere p99'u: %v", v)
	}
	slow := byName["yavaş istek (> 250 ms)"]
	if len(slow.Points) != 60 || slow.Points[1][1] != 4 || slow.Points[2][1] != 6 || slow.Points[3][1] != 0 {
		t.Fatalf("yavaş istek serisi: %+v", slow.Points[:4])
	}
	if len(byName["errors"].Points) != 60 {
		t.Fatal("hata serisi")
	}
	// 3 dk adım: dakika sayımları adım kovasına TOPLANIR.
	c3, _ := sweepOpLatencyChart("op", "", []chstore.OpLatencyPoint{{TS: base}, {TS: base + 180}}, 180, 0, corr, EvBlockWindow{})
	for _, s := range c3.Series {
		if strings.HasPrefix(s.Name, "yavaş") && s.Points[0][1] != 10 {
			t.Fatalf("3 dk kovaya toplam 10: %+v", s.Points)
		}
		if s.Name == "önceki pencere p99" {
			t.Fatal("baseline 0 → kesikli seri yok")
		}
	}
}

func TestSweepSparkChartFallback(t *testing.T) {
	op := chstore.OperationSummary{Name: "op", P99Sparkline: []float64{2, 7, 3}, P50Sparkline: []float64{1, 1, 1}, ErrorsSparkline: []uint64{0, 1, 0}, PriorP99Ms: 4}
	c, ok := sweepSparkChart("op", "", op, sweepTo.Add(-15*time.Minute), sweepTo)
	if !ok || !strings.Contains(c.Title, "5 dk kova · örneklenmiş span (operation_summary_5m)") || len(c.Series) != 4 {
		t.Fatalf("%q %d", c.Title, len(c.Series))
	}
}

func TestLogBodyPreview(t *testing.T) {
	long := strings.Repeat("ş", 300)
	for _, c := range []struct{ in, want string }{
		{`{"service_name":"CALLCENTER_VOICEIVR_LOGIN_GET_CUSTOMER_INFORMATION_SERVICE","error_code":"CQNF_123","duration":12}`,
			"[CQNF_123] CALLCENTER_VOICEIVR_LOGIN_GET_CUSTOMER_INFORMATION_SERVICE"},
		{`{"level":"ERROR","message":"Connection refused","error_code":"E42"}`, "[E42] Connection refused"},
		{`{"msg":"timeout calling ledger","exception":"java.net.SocketTimeoutException: Read timed out\n\tat x.y(Z.java:1)"}`,
			"timeout calling ledger — java.net.SocketTimeoutException: Read timed out"},
		{`{"error":{"message":"deadlock detected"}}`, "deadlock detected"},
		{`{"foo":"bar"}`, `{"foo":"bar"}`},
		{"plain   text\n  line", "plain text line"},
		{`{not json`, `{not json`},
	} {
		if got := LogBodyPreview(c.in, 200); got != c.want {
			t.Errorf("%q → %q, beklenen %q", c.in, got, c.want)
		}
	}
	if n := utf8.RuneCountInString(LogBodyPreview(`{"message":"`+long+`"}`, 200)); n > 200 {
		t.Fatalf("≤ 200 rune: %d", n)
	}
}

func TestSweepRollupAggWindowTotal(t *testing.T) {
	agg := sweepRollupAgg([]chstore.RollupSeries{{Group: "F1",
		Points: []chstore.RollupPoint{{Calls: 100, P99Ms: 10}, {Calls: 100, P99Ms: 30}},
		Total:  &chstore.RollupPoint{Calls: 200, Errors: 4, P99Ms: 28}}})
	if r := agg["F1"]; r.P99Ms != 28 || r.Spans != 200 || r.ErrorRate != 2 {
		t.Fatalf("pencere birleşik değer kullanılmalı (kova ortalaması 20 değil): %+v", r)
	}
	if r := sweepRollupAgg([]chstore.RollupSeries{{Group: "F1", Points: []chstore.RollupPoint{{Calls: 100, P99Ms: 10}, {Calls: 100, P99Ms: 30}}}})["F1"]; r.P99Ms != 20 {
		t.Fatalf("Total yoksa geri dönüş: %+v", r)
	}
}

// latencyReaders — tek gecikme adayı (hata kolu yok): 200 → 300 ms, 5000 çağrı.
func latencyReaders(slow func(context.Context, string, string, float64, time.Time, time.Time, time.Time) (chstore.SlowSpanCorroboration, error)) sweepReaders {
	r := baseSweepReaders()
	r.servicePods, r.fnSpans, r.runtime = nil, nil, nil
	r.logs = &syncLogStore{page: &logstore.Page{}}
	r.ops = func(context.Context, string, time.Time, time.Time) ([]chstore.OperationSummary, error) {
		return []chstore.OperationSummary{
			{Name: "SELECT db0p.WGT_TKART", SpanCount: 5000, P99Ms: 3, HasPrior: true, PriorSpanCount: 5000, PriorP99Ms: 2,
				P99Sparkline: []float64{2, 7, 3}, PriorP99Sparkline: []float64{2, 7, 3, 6, 2, 7}},
			{Name: "POST /pay", SpanCount: 5000, P99Ms: 300, HasPrior: true, PriorSpanCount: 5000, PriorP99Ms: 200,
				P99Sparkline: []float64{250, 300}},
		}, nil
	}
	r.slowSpans = slow
	return r
}

func findingNamed(res SweepResult, n string) *SweepFinding {
	for i := range res.Findings {
		if res.Findings[i].Name == n {
			return &res.Findings[i]
		}
	}
	return nil
}

func TestSweepLatencyCorroboration(t *testing.T) {
	calls := map[string]int{}
	slow := func(cur, prior uint64, err error) func(context.Context, string, string, float64, time.Time, time.Time, time.Time) (chstore.SlowSpanCorroboration, error) {
		return func(_ context.Context, _, op string, thr float64, curFrom, curTo, priorFrom time.Time) (chstore.SlowSpanCorroboration, error) {
			calls[op]++
			if thr != 250 || !curTo.Equal(sweepTo) || !priorFrom.Equal(curFrom.Add(-time.Hour)) {
				t.Errorf("eşik/pencere: %v %s %s", thr, curTo, priorFrom)
			}
			return chstore.SlowSpanCorroboration{ThresholdMs: thr, CurSlow: cur, PriorSlow: prior, MaxMs: 1200, ExemplarID: strings.Repeat("cd", 16), SampleN: 1}, err
		}
	}
	t.Run("doğrulandı → uyarı bulgusu + gerekçede ham sayılar", func(t *testing.T) {
		res, _ := runServiceSweep(context.Background(), latencyReaders(slow(50, 5, nil)), sweepReq(), nil)
		f := findingNamed(res, "POST /pay")
		if f == nil || f.Level != levelWarn || !strings.Contains(strings.Join(f.Reasons, ";"), "ham span: 50 istek > 250 ms") {
			t.Fatalf("%+v", res.Findings)
		}
		if res.Scanned.LatencyCandidates != 1 || res.Scanned.LatencyCorroborated != 1 || sectionByName(res, sweepHopCorrob).State != hopOK {
			t.Fatalf("sayaç/bölüm: %+v %+v", res.Scanned, sectionByName(res, sweepHopCorrob))
		}
	})
	t.Run("doğrulanmadı → bulgu yok, satır gerekçesi söyler", func(t *testing.T) {
		res, _ := runServiceSweep(context.Background(), latencyReaders(slow(3, 2, nil)), sweepReq(), nil)
		if f := findingNamed(res, "POST /pay"); f != nil {
			t.Fatalf("doğrulanmayan gecikme bulgu olmamalı: %+v", f)
		}
		for _, row := range res.Data.Operations {
			if row.Name == "POST /pay" && (row.Level != levelOK || row.Corroboration != corrobRejected || !strings.Contains(row.Reason, "ham span doğrulamadı")) {
				t.Fatalf("satır: %+v", row)
			}
		}
	})
	t.Run("okuma hatası → olası (seviye kalır, söylenir)", func(t *testing.T) {
		res, _ := runServiceSweep(context.Background(), latencyReaders(slow(0, 0, errors.New("timeout"))), sweepReq(), nil)
		f := findingNamed(res, "POST /pay")
		if f == nil || f.Level != levelWarn || !strings.Contains(strings.Join(f.Reasons, ";"), "olası") {
			t.Fatalf("%+v", res.Findings)
		}
		if st := sectionByName(res, sweepHopCorrob).State; st != hopError || !res.Partial {
			t.Fatalf("bölüm %s partial %v", st, res.Partial)
		}
	})
	t.Run("operatör vakası: 2 → 3 ms ne bulgu ne doğrulama okuması", func(t *testing.T) {
		calls = map[string]int{}
		res, _ := runServiceSweep(context.Background(), latencyReaders(slow(50, 5, nil)), sweepReq(), nil)
		if findingNamed(res, "SELECT db0p.WGT_TKART") != nil || calls["SELECT db0p.WGT_TKART"] != 0 {
			t.Fatalf("3 ms sorgu bulgu/okuma üretmemeli: %+v %v", res.Findings, calls)
		}
		for _, row := range res.Data.Operations {
			if row.Name == "SELECT db0p.WGT_TKART" && !strings.Contains(row.Reason, "100 ms altı") {
				t.Fatalf("susturma gerekçesi satırda: %+v", row)
			}
		}
	})
}

// TestSweepOpChartBlock — operasyon bulgusunun grafiği ince seriden (≤ 6 sa).
func TestSweepOpChartBlock(t *testing.T) {
	r := latencyReaders(func(_ context.Context, _, _ string, thr float64, _, _, _ time.Time) (chstore.SlowSpanCorroboration, error) {
		return chstore.SlowSpanCorroboration{ThresholdMs: thr, CurSlow: 40, Minutes: []chstore.SlowSpanMinute{{TS: sweepTo.Add(-30 * time.Minute).Unix(), Count: 40}}}, nil
	})
	var gotStep int
	r.opLatency = func(_ context.Context, _, _ string, from, _ time.Time, step int) ([]chstore.OpLatencyPoint, error) {
		gotStep = step
		return []chstore.OpLatencyPoint{{TS: from.Unix(), Calls: 10, P50Ms: 100, P99Ms: 280}, {TS: from.Unix() + 60, Calls: 10, P50Ms: 110, P99Ms: 320}}, nil
	}
	res, _ := runServiceSweep(context.Background(), r, sweepReq(), nil)
	if gotStep != 60 {
		t.Fatalf("1 sa pencere → 60 s adım: %d", gotStep)
	}
	var chart *EvChartPayload
	for _, b := range res.Data.Blocks {
		if c, ok := b.Payload.(EvChartPayload); ok && strings.HasPrefix(c.Title, "POST /pay") {
			chart = &c
		}
	}
	if chart == nil || !strings.Contains(chart.Title, "1 dk kova · örneklenmiş span (spanmetrics_1m)") {
		t.Fatalf("grafik bloğu: %+v", res.Data.Blocks)
	}
	names := []string{}
	for _, s := range chart.Series {
		names = append(names, s.Name)
	}
	if got := strings.Join(names, ","); got != "p50,p99,önceki pencere p99,yavaş istek (> 250 ms),errors" {
		t.Fatalf("seriler: %s", got)
	}
}

// TestSweepFindingLogPreview — bulgu logu (modele / anlatıma giden) önizleme;
// açılır tablo ham gövdeyi taşır.
func TestSweepFindingLogPreview(t *testing.T) {
	raw := `{"service_name":"CALLCENTER_VOICEIVR_LOGIN_GET_CUSTOMER_INFORMATION_SERVICE","error_code":"CQNF_123","trace":"x"}`
	r := baseSweepReaders()
	r.logs = &syncLogStore{page: &logstore.Page{Logs: []*logstore.LogRecord{{Severity: 17, Body: raw, ServiceName: "checkout", Timestamp: sweepTo.UnixNano()}}}}
	res, err := runServiceSweep(context.Background(), r, sweepReq(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "[CQNF_123] CALLCENTER_VOICEIVR_LOGIN_GET_CUSTOMER_INFORMATION_SERVICE"
	sawTable := false
	for _, f := range res.Findings {
		if f.Logs == nil || len(f.Logs.Rows) == 0 {
			continue
		}
		if strings.Contains(f.Logs.Summary, `{"service_name"`) || !strings.Contains(f.Logs.Summary, want) || f.Logs.Rows[0].Body != want {
			t.Fatalf("bulgu logu ham JSON taşımamalı: %q / %q", f.Logs.Summary, f.Logs.Rows[0].Body)
		}
	}
	for _, fl := range res.Data.FindingLogs {
		if fl.Rows[0].Body != want {
			t.Fatalf("data.findingLogs önizleme: %q", fl.Rows[0].Body)
		}
	}
	for _, b := range res.Data.Blocks {
		if tp, ok := b.Payload.(EvTablePayload); ok && tp.Title == "sorunluların logları" {
			sawTable = true
			if body, _ := tp.Rows[0][3].(string); !strings.HasPrefix(body, `{"service_name"`) {
				t.Fatalf("açılır tablo HAM gövdeyi taşır: %q", body)
			}
		}
	}
	if !sawTable {
		t.Fatal("log tablosu bloğu yok")
	}
}

func TestPivotOpLevelFloors(t *testing.T) {
	for _, c := range []struct {
		span, p99 float64
		want      string
	}{
		{3, 2, levelOK},       // 1.5× ama 3 ms
		{120, 90, levelOK},    // fark 30 ms < 50
		{400, 200, levelCrit}, // 2× ve ≥ 200 ms
		{300, 200, levelWarn}, // 1.5×, fark 100 ms
		{90, 30, levelOK},     // 3× ama < 100 ms
	} {
		if got, _ := pivotOpLevel(c.span, c.p99); got != c.want {
			t.Errorf("%v/%v: %s (beklenen %s)", c.span, c.p99, got, c.want)
		}
	}
	_ = healthverdict.LatMinP99Ms
}
