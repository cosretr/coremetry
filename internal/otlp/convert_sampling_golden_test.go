package otlp

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/cilcenk/coremetry/internal/chstore"
	tracecollpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// v0.10.2430 — spans.sample_weight golden test (/otel-conventions §2.6/§5,
// TAM-struct). Contract (sampling_weight.go):
//   - tracestate ot=th wins (th:c → p=0.25 → 4; th:0 → 1; invalid th falls
//     through to the rules);
//   - then sampling.weight / SampleRate on span → scope → resource;
//   - then operator rules, first match: scope attr tailsampling.policy (the
//     tail_sampling processor writes it on SCOPE attributes), then
//     traceError (any ERROR span of the same trace in the request), then a
//     catch-all;
//   - mode shadow stores 1 but tallies; mode off stores 1 and tallies nothing;
//   - attribute arrays stay verbatim (scope attributes are not stored).

func loadSamplingFixture(t *testing.T) *tracecollpb.ExportTraceServiceRequest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "span_sampling_weight.json"))
	if err != nil {
		t.Fatal(err)
	}
	var req tracecollpb.ExportTraceServiceRequest
	if err := protojson.Unmarshal(raw, &req); err != nil {
		t.Fatalf("fixture protojson: %v", err)
	}
	return &req
}

func withSampling(t *testing.T, c chstore.IngestSampling) {
	t.Helper()
	prev := chstore.CurrentIngestSampling()
	chstore.SetIngestSampling(c)
	resetWeightTally()
	t.Cleanup(func() { chstore.SetIngestSampling(prev); resetWeightTally() })
}

var goldenRules = []chstore.SamplingRule{
	{Name: "errors", Attr: "tailsampling.policy", Value: "keep-errors", Weight: 1},
	{Name: "rest", Attr: "tailsampling.policy", Value: "sample-10", Weight: 10},
	{Name: "trace-error", TraceError: true, Weight: 1},
	{Name: "else", Weight: 10},
}

func TestConvertSpanSamplingWeightGolden(t *testing.T) {
	withSampling(t, chstore.IngestSampling{Mode: chstore.SamplingModeOn, Rules: goldenRules})
	rows, _ := ConvertTraces(loadSamplingFixture(t))
	if len(rows) != 11 {
		t.Fatalf("11 spans expected, got %d", len(rows))
	}
	tid := func(n int) string { return fmt.Sprintf("%032x", n) }
	sid := func(n int) string { return fmt.Sprintf("%016x", n) }
	ts := time.Unix(0, 1_700_000_000_000_000_000).UTC()
	type exp struct {
		trace, span int
		svc, scope  string
		err         bool
		traceState  string
		attrK       []string
		attrV       []string
		weight      float32
	}
	exps := []exp{
		{1, 1, "checkout", "tail", true, "", nil, nil, 1},
		{1, 2, "checkout", "tail", false, "", nil, nil, 1},
		{2, 3, "checkout", "tail", false, "", nil, nil, 10},
		{5, 4, "checkout", "sdk", false, "", nil, nil, 4},
		{6, 5, "checkout", "sdk", false, "", nil, nil, 1},
		{7, 6, "checkout", "sdk", false, "", []string{"sampling.weight"}, []string{"10"}, 10},
		{3, 7, "checkout", "sdk", false, "", nil, nil, 1},
		{3, 8, "checkout", "sdk", true, "", nil, nil, 1},
		{4, 9, "checkout", "sdk", false, "", nil, nil, 10},
		{8, 10, "checkout", "sdk", false, "", nil, nil, 10},
		{9, 11, "legacy", "sdk", false, "", nil, nil, 20},
	}
	for i, e := range exps {
		resK, resV := []string{"service.name"}, []string{e.svc}
		if e.svc == "legacy" {
			resK, resV = []string{"service.name", "SampleRate"}, []string{"legacy", "20"}
		}
		ak, av := e.attrK, e.attrV
		if ak == nil {
			ak, av = []string{}, []string{}
		}
		want := chstore.Span{
			TraceID: tid(e.trace), SpanID: sid(e.span), Name: "work", OpGroup: rows[i].OpGroup,
			Kind: "internal", ServiceName: e.svc, StatusCode: "unset",
			Time: ts, Duration: 1_000_000,
			AttrKeys: ak, AttrValues: av, ResKeys: resK, ResValues: resV,
			ScopeName: e.scope, SampleWeight: e.weight,
		}
		if e.err {
			want.StatusCode = "error"
		}
		got := *rows[i]
		got.Time = got.Time.UTC()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("span %d (-want +got):\n want %+v\n  got %+v", i, want, got)
		}
	}
	tally, overflow := SampleWeightTally()
	if overflow != 0 {
		t.Fatalf("overflow %d", overflow)
	}
	got := map[string]uint64{}
	for _, r := range tally {
		got[fmt.Sprintf("%s=%g", r.Source, r.Weight)] = r.Spans
	}
	want := map[string]uint64{
		"rule:errors=1": 2, "rule:rest=10": 1, "tracestate=4": 1, "tracestate=1": 1,
		"attribute=10": 1, "rule:trace-error=1": 2, "rule:else=10": 2, "attribute=20": 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tally = %v, want %v", got, want)
	}
}

func TestConvertSpanSamplingWeightShadowAndOff(t *testing.T) {
	withSampling(t, chstore.IngestSampling{Mode: chstore.SamplingModeShadow, Rules: goldenRules})
	rows, _ := ConvertTraces(loadSamplingFixture(t))
	for i, r := range rows {
		if r.SampleWeight != 1 {
			t.Fatalf("shadow: span %d stored weight %v, want 1", i, r.SampleWeight)
		}
	}
	if tally, _ := SampleWeightTally(); len(tally) == 0 {
		t.Fatal("shadow must tally")
	}

	withSampling(t, chstore.IngestSampling{Mode: chstore.SamplingModeOff, Rules: goldenRules})
	rows, _ = ConvertTraces(loadSamplingFixture(t))
	for i, r := range rows {
		if r.SampleWeight != 1 {
			t.Fatalf("off: span %d stored weight %v, want 1", i, r.SampleWeight)
		}
	}
	if tally, _ := SampleWeightTally(); len(tally) != 0 {
		t.Fatalf("off must not tally: %v", tally)
	}
}

func TestTracestateWeight(t *testing.T) {
	cases := []struct {
		ts   string
		want float64
		ok   bool
	}{
		{"ot=th:0", 1, true},
		{"ot=th:8", 2, true},
		{"ot=th:c", 4, true},
		{"ot=th:e6666666666666", 1 / (1 - float64(0xe6666666666666)/float64(uint64(1)<<56)), true}, // 10 %
		{"ot=th:fd70a4", 1 / (1 - float64(0xfd70a400000000)/float64(uint64(1)<<56)), true},         // spec example ≈ 1 %
		{"a=b, ot=rv:00112233445566;th:8", 2, true},
		{"ot=th:", 0, false},
		{"ot=th:C", 0, false},               // spec: lowercase hex
		{"ot=th:123456789abcdef", 0, false}, // > 14 digits
		{"ot=rv:00112233445566", 0, false},
		{"xot=th:8", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := tracestateWeight(c.ts)
		if ok != c.ok || (ok && math.Abs(got-c.want) > 1e-9*c.want) {
			t.Errorf("tracestateWeight(%q) = %v,%v want %v,%v", c.ts, got, ok, c.want, c.ok)
		}
	}
}

func TestIngestSamplingNormalize(t *testing.T) {
	c := chstore.NormalizeIngestSampling(chstore.IngestSampling{
		Mode: " ON ", DefaultWeight: -3,
		Rules: []chstore.SamplingRule{
			{Name: "bad", Weight: 0},
			{Status: "ok", Weight: 2},
			{Status: "ERROR", Weight: 0.5},
			{Attr: "", Value: "x", Weight: 2e9},
		},
	})
	if c.Mode != chstore.SamplingModeOn || c.DefaultWeight != 1 {
		t.Fatalf("mode/default = %q/%v", c.Mode, c.DefaultWeight)
	}
	if len(c.Rules) != 2 || c.Rules[0].Status != "error" || c.Rules[0].Weight != 1 ||
		c.Rules[1].Value != "" || c.Rules[1].Weight != chstore.MaxSampleWeight || c.Rules[1].Name != "rule-2" {
		t.Fatalf("rules = %+v", c.Rules)
	}
	if chstore.NormalizeIngestSampling(chstore.IngestSampling{Mode: "bogus"}).Mode != chstore.SamplingModeOff {
		t.Fatal("unknown mode must normalize to off")
	}
}

// Tail-sampling hash reproduction (contrib v0.146.0 probabilistic policy).
// The FNV-1a value is computed independently (FNV-1a 64 offset/prime over
// "default-hash-seed" ‖ 0x01..0x10) so a drift in our byte order or salt
// handling fails here, and the ratio check uses the collector test's own
// PCG(123, 456) trace-id generator.
func TestTailSamplingHashReproduction(t *testing.T) {
	id := make([]byte, 16)
	for i := range id {
		id[i] = byte(i + 1)
	}
	const want = uint64(0xdbcf64d5420f4cad) // ≈ 0.8586 of MaxUint64
	if !inTailSamplingBucket(chstore.DefaultTailSamplingHashSalt, id, want) ||
		inTailSamplingBucket(chstore.DefaultTailSamplingHashSalt, id, want-1) {
		t.Fatal("FNV-1a(salt‖traceID) differs from the collector's hashTraceID")
	}
	if !inTailSamplingBucket(chstore.DefaultTailSamplingHashSalt, id, chstore.TailSamplingThreshold(86)) ||
		inTailSamplingBucket(chstore.DefaultTailSamplingHashSalt, id, chstore.TailSamplingThreshold(85)) {
		t.Fatal("threshold arithmetic off")
	}
	if chstore.TailSamplingThreshold(100) != math.MaxUint64 {
		t.Fatal("100 % must keep every trace")
	}
	r := rand.New(rand.NewPCG(123, 456))
	th := chstore.TailSamplingThreshold(5)
	in := 0
	const n = 100_000
	for range n {
		var b [16]byte
		binary.BigEndian.PutUint64(b[:8], r.Uint64())
		binary.BigEndian.PutUint64(b[8:], r.Uint64())
		if inTailSamplingBucket(chstore.DefaultTailSamplingHashSalt, b[:], th) {
			in++
		}
	}
	if p := float64(in) / n * 100; math.Abs(p-5) > 0.2 {
		t.Fatalf("5 %% bucket kept %.2f %%", p)
	}
}

// Hash rules end to end: out-of-bucket trace ⇒ kept only for failing ⇒ 1;
// in-bucket trace without a visible error ⇒ 20; in-bucket trace with an
// error span in the request ⇒ 1.
func TestHashRulesDeriveWeight(t *testing.T) {
	cfg := chstore.NormalizeIngestSampling(chstore.IngestSampling{Mode: "on", Rules: []chstore.SamplingRule{
		{Name: "failing-trace", TraceError: true, Weight: 1},
		{Name: "error-only", HashPercent: 5, HashBucket: "out", Weight: 1},
		{Name: "in-bucket", HashPercent: 5, Weight: 20},
	}})
	if cfg.Rules[2].HashSalt != chstore.DefaultTailSamplingHashSalt || cfg.Rules[2].HashBucket != "in" {
		t.Fatalf("hash defaults: %+v", cfg.Rules[2])
	}
	th := chstore.TailSamplingThreshold(5)
	var inID, outID []byte
	for i := 0; inID == nil || outID == nil; i++ {
		b := make([]byte, 16)
		binary.BigEndian.PutUint64(b[8:], uint64(i))
		if inTailSamplingBucket(chstore.DefaultTailSamplingHashSalt, b, th) {
			if inID == nil {
				inID = b
			}
		} else if outID == nil {
			outID = b
		}
	}
	cases := []struct {
		in  weightInput
		w   float64
		src string
	}{
		{weightInput{traceID: outID}, 1, "rule:error-only"},
		{weightInput{traceID: inID}, 20, "rule:in-bucket"},
		{weightInput{traceID: inID, traceError: true}, 1, "rule:failing-trace"},
	}
	for i, c := range cases {
		if w, src := deriveWeight(&cfg, c.in); w != c.w || src != c.src {
			t.Errorf("case %d: %v %q, want %v %q", i, w, src, c.w, c.src)
		}
	}
}
