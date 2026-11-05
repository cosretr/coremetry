package pipeline

// metric_name_dropper_test.go — v0.10.1809.
//
// Sözleşme: Settings › Pipeline metrik DROP kuralları yalnız yazma
// anında uygulanıyordu (CH ingest + VM forward). Kuraldan ÖNCE yazılmış
// adlar (örn. healthcheck metrikleri) metrik listelerinde görünmeye
// devam ediyordu. MetricNameDropper okuma tarafının aynı kararı vermesi
// için kuralların YALNIZ ADLA karar verilebilen alt kümesini derler:
//   - koşulları yalnız ad anahtarında (metric / name / metric.name) olan
//     etkin metrics/drop kuralı adı gizler;
//   - etiket/öznitelik/servis koşullu kural adı GİZLEMEZ (ad tek başına
//     karar veremez; adın başka serileri hâlâ geliyor olabilir);
//   - digest kural kümesinin TAMAMINI hash'ler (v0.5.187: uzunluk değil).

import "testing"

func TestMetricNameDropper(t *testing.T) {
	drop := func(id string, when Condition, and ...Condition) Rule {
		return Rule{ID: id, Kind: KindDrop, Signal: SignalMetrics, Enabled: true, When: when, And: and}
	}
	cases := []struct {
		name    string
		rules   []Rule
		dropped []string
		kept    []string
		active  bool
	}{
		{"no rules", nil, nil, []string{"healthcheck_total"}, false},
		{"eq", []Rule{drop("a", Condition{Key: "metric", Op: OpEq, Value: "healthcheck_total"})},
			[]string{"healthcheck_total"}, []string{"healthcheck_total_x", "http_requests"}, true},
		{"regex unanchored", []Rule{drop("a", Condition{Key: "metric", Op: OpMatches, Value: "healthcheck"})},
			[]string{"healthcheck_total", "app.healthcheck.duration"}, []string{"http_requests"}, true},
		{"neq", []Rule{drop("a", Condition{Key: "name", Op: OpNeq, Value: "keep_me"})},
			[]string{"anything"}, []string{"keep_me"}, true},
		{"startsWith alias metric.name", []Rule{drop("a", Condition{Key: "metric.name", Op: OpStartsWith, Value: "debug_"})},
			[]string{"debug_x"}, []string{"x_debug_"}, true},
		{"name AND name", []Rule{drop("a", Condition{Key: "metric", Op: OpStartsWith, Value: "hc"},
			Condition{Key: "metric", Op: OpEndsWith, Value: "_total"})},
			[]string{"hc_total"}, []string{"hc_seconds", "x_total"}, true},
		{"disabled ignored", []Rule{{ID: "a", Kind: KindDrop, Signal: SignalMetrics, Enabled: false,
			When: Condition{Key: "metric", Op: OpEq, Value: "hc"}}},
			nil, []string{"hc"}, false},
		{"label-conditioned When ignored", []Rule{drop("a", Condition{Key: "attr.route", Op: OpEq, Value: "/health"})},
			nil, []string{"hc", "http_requests"}, false},
		{"label-conditioned And ignored", []Rule{drop("a", Condition{Key: "metric", Op: OpEq, Value: "hc"},
			Condition{Key: "service.name", Op: OpEq, Value: "svc-a"})},
			nil, []string{"hc"}, false},
		{"other signal ignored", []Rule{{ID: "a", Kind: KindDrop, Signal: SignalSpans, Enabled: true,
			When: Condition{Key: "name", Op: OpEq, Value: "hc"}}},
			nil, []string{"hc"}, false},
		{"sample kind ignored", []Rule{{ID: "a", Kind: KindSample, Signal: SignalMetrics, Enabled: true, Rate: 0.1,
			When: Condition{Key: "metric", Op: OpEq, Value: "hc"}}},
			nil, []string{"hc"}, false},
		{"mixed: only name rule applies", []Rule{
			drop("a", Condition{Key: "metric", Op: OpMatches, Value: "^hc"}),
			drop("b", Condition{Key: "attr.k", Op: OpEq, Value: "v"}),
		}, []string{"hc_total"}, []string{"other"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &Engine{rules: tc.rules}
			fn, digest := e.MetricNameDropper()
			if (fn != nil) != tc.active {
				t.Fatalf("active=%v want %v", fn != nil, tc.active)
			}
			if !tc.active && digest != "" {
				t.Errorf("inactive dropper digest=%q want empty", digest)
			}
			if tc.active && digest == "" {
				t.Errorf("active dropper has empty digest")
			}
			for _, n := range tc.dropped {
				if fn == nil || !fn(n) {
					t.Errorf("%q should be dropped", n)
				}
			}
			for _, n := range tc.kept {
				if fn != nil && fn(n) {
					t.Errorf("%q should be kept", n)
				}
			}
		})
	}
}

func TestMetricNameDropperNilEngine(t *testing.T) {
	var e *Engine
	if fn, d := e.MetricNameDropper(); fn != nil || d != "" {
		t.Fatalf("nil engine: fn=%v digest=%q", fn != nil, d)
	}
}

func TestMetricNameDropperDigest(t *testing.T) {
	r := func(id, v string) Rule {
		return Rule{ID: id, Kind: KindDrop, Signal: SignalMetrics, Enabled: true,
			When: Condition{Key: "metric", Op: OpMatches, Value: v}}
	}
	dig := func(rules ...Rule) string {
		_, d := (&Engine{rules: rules}).MetricNameDropper()
		return d
	}
	base := dig(r("a", "hc"), r("b", "debug"))
	if got := dig(r("b", "debug"), r("a", "hc")); got != base {
		t.Errorf("order-dependent digest: %q vs %q", got, base)
	}
	// Same COUNT, different pattern — the v0.5.187 len-only poisoning.
	if got := dig(r("a", "hc"), r("b", "trace")); got == base {
		t.Errorf("digest unchanged when a pattern changed")
	}
	if got := dig(r("a", "hc")); got == base {
		t.Errorf("digest unchanged when a rule was removed")
	}
	opChanged := Rule{ID: "a", Kind: KindDrop, Signal: SignalMetrics, Enabled: true,
		When: Condition{Key: "metric", Op: OpEq, Value: "hc"}}
	if dig(opChanged, r("b", "debug")) == base {
		t.Errorf("digest unchanged when an op changed")
	}
	// A label-conditioned rule does not affect the NAME verdict, so it must
	// not cold-start every name cache entry either.
	label := Rule{ID: "c", Kind: KindDrop, Signal: SignalMetrics, Enabled: true,
		When: Condition{Key: "attr.k", Op: OpEq, Value: "v"}}
	if got := dig(r("a", "hc"), r("b", "debug"), label); got != base {
		t.Errorf("label-conditioned rule changed the name digest")
	}
}
