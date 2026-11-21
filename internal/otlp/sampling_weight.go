package otlp

// sampling_weight.go — v0.10.2430: ingest-time spans.sample_weight
// (docs/architecture/sampling-weight.md). How many ORIGINAL spans one stored
// span stands for when traces are sampled upstream of Coremetry. Priority:
//
//  1. W3C tracestate `ot=th:<hex>` — OTel probability sampling threshold
//     (spec: trace/tracestate-probability-sampling). Rejection threshold
//     T = th right-padded to 14 hex digits (56 bits); adjusted count =
//     2^56 / (2^56 − T). th:0 = kept with probability 1 → weight 1.
//  2. Explicit weight attribute on span → scope → resource:
//     `sampling.weight`, `SampleRate` (Honeycomb convention), `sampleRate`.
//  3. Operator rules (system_settings ingest.sampling), first match wins.
//  4. defaultWeight (1 unless the operator set otherwise).
//
// Mode off (default) → none of this runs; every span gets 1. Mode shadow →
// derived + counted but 1 is written. The attribute arrays stay verbatim.

import (
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Weight source labels (the admin card's "which rule matched" column).
const (
	weightSrcTracestate = "tracestate"
	weightSrcAttribute  = "attribute"
	weightSrcDefault    = "default"
	weightSrcRulePrefix = "rule:"
)

// weightAttrKeys — explicit weight attributes, in lookup order.
var weightAttrKeys = [...]string{"sampling.weight", "SampleRate", "sampleRate"}

// tracestateWeight returns the adjusted count from an `ot=th:` threshold,
// ok=false when the tracestate carries no valid threshold. Per the OTel
// spec the th value is 1–14 lowercase hex digits with trailing zeros
// omitted; anything else is "inconsistent" and ignored (the spec tells
// samplers to erase such a threshold, i.e. adjusted count unknown).
func tracestateWeight(ts string) (float64, bool) {
	if ts == "" {
		return 0, false
	}
	for _, member := range strings.Split(ts, ",") {
		member = strings.TrimSpace(member)
		v, found := strings.CutPrefix(member, "ot=")
		if !found {
			continue
		}
		for _, kv := range strings.Split(v, ";") {
			th, ok := strings.CutPrefix(kv, "th:")
			if !ok {
				continue
			}
			return thresholdAdjustedCount(th)
		}
		return 0, false
	}
	return 0, false
}

// thresholdAdjustedCount — SAF: th hex → 2^56 / (2^56 − T).
func thresholdAdjustedCount(th string) (float64, bool) {
	const digits = 14
	if th == "" || len(th) > digits {
		return 0, false
	}
	for i := 0; i < len(th); i++ {
		c := th[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return 0, false
		}
	}
	t, err := strconv.ParseUint(th, 16, 64)
	if err != nil {
		return 0, false
	}
	t <<= 4 * uint(digits-len(th))
	const maxth = uint64(1) << 56
	if t >= maxth {
		return 0, false
	}
	return float64(maxth) / float64(maxth-t), true
}

// attrWeight — first explicit weight attribute across span → scope →
// resource. Numeric strings are accepted (Honeycomb SDKs send ints, some
// pipelines stringify). Invalid values fall through.
func attrWeight(levels ...[]*commonpb.KeyValue) (float64, bool) {
	for _, attrs := range levels {
		for _, kv := range attrs {
			if !isWeightKey(kv.Key) || kv.Value == nil {
				continue
			}
			var w float64
			switch v := kv.Value.Value.(type) {
			case *commonpb.AnyValue_IntValue:
				w = float64(v.IntValue)
			case *commonpb.AnyValue_DoubleValue:
				w = v.DoubleValue
			case *commonpb.AnyValue_StringValue:
				f, err := strconv.ParseFloat(strings.TrimSpace(v.StringValue), 64)
				if err != nil {
					continue
				}
				w = f
			default:
				continue
			}
			if w = chstore.ClampSampleWeight(w); w > 0 {
				return w, true
			}
		}
	}
	return 0, false
}

func isWeightKey(k string) bool {
	for _, w := range weightAttrKeys {
		if k == w {
			return true
		}
	}
	return false
}

// attrLookup — value of key on span → scope → resource.
func attrLookup(key string, levels ...[]*commonpb.KeyValue) (string, bool) {
	for _, attrs := range levels {
		for _, kv := range attrs {
			if kv.Key == key {
				return anyValStr(kv.Value), true
			}
		}
	}
	return "", false
}

// weightInput — what one span offers the derivation.
type weightInput struct {
	traceID    []byte
	traceState string
	spanAttrs  []*commonpb.KeyValue
	scopeAttrs []*commonpb.KeyValue
	resAttrs   []*commonpb.KeyValue
	isError    bool
	traceError bool
}

// deriveWeight — SAF: (weight, source label). cfg must be Active.
func deriveWeight(cfg *chstore.IngestSampling, in weightInput) (float64, string) {
	if cfg.TracestateOn() {
		if w, ok := tracestateWeight(in.traceState); ok {
			return chstore.ClampSampleWeight(w), weightSrcTracestate
		}
	}
	if cfg.AttributesOn() {
		if w, ok := attrWeight(in.spanAttrs, in.scopeAttrs, in.resAttrs); ok {
			return w, weightSrcAttribute
		}
	}
	for i := range cfg.Rules {
		r := &cfg.Rules[i]
		if r.Status == "error" && !in.isError {
			continue
		}
		if r.TraceError && !in.traceError {
			continue
		}
		if r.HashPercent > 0 && inTailSamplingBucket(r.HashSalt, in.traceID, r.HashThreshold()) != (r.HashBucket == "in") {
			continue
		}
		if r.Attr != "" {
			v, ok := attrLookup(r.Attr, in.spanAttrs, in.scopeAttrs, in.resAttrs)
			if !ok || (r.Value != "" && v != r.Value) {
				continue
			}
		}
		return r.Weight, weightSrcRulePrefix + r.Name
	}
	return cfg.DefaultWeight, weightSrcDefault
}

// inTailSamplingBucket — the tail_sampling probabilistic policy's decision
// (contrib v0.146.0 hashTraceID: FNV-1a 64 over the salt bytes then the 16
// trace-id bytes; sampled iff hash <= threshold).
func inTailSamplingBucket(salt string, traceID []byte, threshold uint64) bool {
	h := fnv.New64a()
	_, _ = h.Write([]byte(salt))
	_, _ = h.Write(traceID)
	return h.Sum64() <= threshold
}

// errorTraces — trace ids (raw bytes as string) with at least one ERROR span
// in this export request. Built only when a rule asks for traceError.
func errorTraces(rss []*tracepb.ResourceSpans) map[string]struct{} {
	m := map[string]struct{}{}
	for _, rs := range rss {
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				if sp.Status != nil && sp.Status.Code == tracepb.Status_STATUS_CODE_ERROR {
					m[string(sp.TraceId)] = struct{}{}
				}
			}
		}
	}
	return m
}

// ── Self-observability: pod-local (source, weight) tally ─────────────────

const maxWeightTallyKeys = 256

type weightKey struct {
	source string
	weight float64
}

var weightTally struct {
	mu       sync.Mutex
	counts   map[weightKey]uint64
	overflow uint64
}

// weightBatch — per-request local tally, flushed once (one lock per export).
type weightBatch map[weightKey]uint64

func (b weightBatch) add(source string, w float64) {
	// 4 significant digits: th-derived weights (e.g. 3.3333) must not mint a
	// new key per float rounding.
	b[weightKey{source, roundSig(w, 4)}]++
}

func (b weightBatch) flush() {
	if len(b) == 0 {
		return
	}
	weightTally.mu.Lock()
	defer weightTally.mu.Unlock()
	if weightTally.counts == nil {
		weightTally.counts = map[weightKey]uint64{}
	}
	for k, n := range b {
		if _, ok := weightTally.counts[k]; !ok && len(weightTally.counts) >= maxWeightTallyKeys {
			weightTally.overflow += n
			continue
		}
		weightTally.counts[k] += n
	}
}

func roundSig(w float64, sig int) float64 {
	if w == 0 {
		return 0
	}
	p := math.Pow(10, float64(sig-1-int(math.Floor(math.Log10(math.Abs(w))))))
	return math.Round(w*p) / p
}

// WeightTallyRow — one (source, weight) cell.
type WeightTallyRow struct {
	Source string  `json:"source"`
	Weight float64 `json:"weight"`
	Spans  uint64  `json:"spans"`
}

// SampleWeightTally — pod-local counts since process start (sorted by spans
// desc), plus spans that did not fit the bounded table.
func SampleWeightTally() ([]WeightTallyRow, uint64) {
	weightTally.mu.Lock()
	defer weightTally.mu.Unlock()
	out := make([]WeightTallyRow, 0, len(weightTally.counts))
	for k, n := range weightTally.counts {
		out = append(out, WeightTallyRow{Source: k.source, Weight: k.weight, Spans: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Spans != out[j].Spans {
			return out[i].Spans > out[j].Spans
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Weight < out[j].Weight
	})
	return out, weightTally.overflow
}

// resetWeightTally — tests.
func resetWeightTally() {
	weightTally.mu.Lock()
	weightTally.counts, weightTally.overflow = nil, 0
	weightTally.mu.Unlock()
}
