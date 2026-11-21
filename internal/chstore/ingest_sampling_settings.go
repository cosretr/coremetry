package chstore

// ingest_sampling_settings.go — v0.10.2430: upstream-sampling weight rules
// (docs/architecture/sampling-weight.md, docs/operator/tail-sampling-weights.md).
//
// system_settings `ingest.sampling` blob:
//
//	{"mode":"on","defaultWeight":1,"honorTracestate":true,"honorAttributes":true,
//	 "rules":[{"name":"errors","attr":"tailsampling.policy","value":"keep-errors","weight":1},
//	          {"name":"rest","attr":"tailsampling.policy","value":"sample-10","weight":10}]}
//
// The OTLP converter stamps every span with spans.sample_weight = how many
// original spans this stored span stands for. Off by default: mode "off" (or
// a missing blob) → every span gets weight 1, zero per-span work → behaviour
// unchanged. Mode "shadow" derives + counts the weight (admin card) but still
// writes 1 — the operator verifies the distribution before relying on it.
// Template ai_turn_limits_settings.go: boot load + 30 s cfgRefresh, process-
// local atomic (the ingest pods read it; the admin PUT lives on the api pod
// and reaches ingest pods through the refresher). Write path
// api/ingest_sampling.go (admin PUT + audit settings.ingest_sampling.update).

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"math/big"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// IngestSamplingKey — system_settings key.
const IngestSamplingKey = "ingest.sampling"

const (
	// MaxSampleWeight — clamp for every weight source. 1e6 = a 0.0001 %
	// sampler; anything larger is almost certainly a unit mistake (a
	// percentage written as a weight) and would swamp every sum.
	MaxSampleWeight = 1e6
	// MaxSamplingRules — rule list cap (rules run per span on the ingest hot
	// path; first match wins, so a long list is also an operator smell).
	MaxSamplingRules = 32
	maxRuleField     = 200
)

// SamplingRule — one operator rule. All set conditions must hold (AND):
//   - Attr (+ Value): attribute present on the span, else its scope, else its
//     resource (the tail_sampling processor writes tailsampling.policy on the
//     SCOPE attributes). Empty Value = the key is present with any value.
//   - Status "error": the span's own status is ERROR.
//   - TraceError: some span of the same trace in the same export request has
//     status ERROR (approximates the tail sampler's trace-level decision).
//   - HashPercent (+ HashSalt, HashBucket "in"|"out"): the trace id falls
//     inside / outside the bucket the OTel Collector tail_sampling
//     `probabilistic` policy keeps — FNV-1a 64 over salt then trace-id bytes
//     <= MaxUint64*pct/100, salt default "default-hash-seed" (contrib
//     v0.146.0 internal/sampling/probabilistic.go, reproduced exactly). Lets
//     Coremetry tell "kept by the percentage" from "kept only because it
//     failed" with no collector change; silently wrong if the salt or
//     percentage differ from the collector's.
//
// A rule with no condition matches every span (a catch-all, e.g. "else 10").
type SamplingRule struct {
	Name       string `json:"name"`
	Attr       string `json:"attr,omitempty"`
	Value      string `json:"value,omitempty"`
	Status     string `json:"status,omitempty"` // "" | "error"
	TraceError bool   `json:"traceError,omitempty"`
	// HashPercent — 0 = no hash condition; (0, 100].
	HashPercent float64 `json:"hashPercent,omitempty"`
	HashSalt    string  `json:"hashSalt,omitempty"`
	HashBucket  string  `json:"hashBucket,omitempty"` // "in" (default) | "out"
	Weight      float64 `json:"weight"`

	hashThreshold uint64 // derived in NormalizeIngestSampling
}

// DefaultTailSamplingHashSalt — tail_sampling probabilistic policy default.
const DefaultTailSamplingHashSalt = "default-hash-seed"

// HashThreshold — the derived tail_sampling threshold (0 when no hash).
func (r SamplingRule) HashThreshold() uint64 { return r.hashThreshold }

// TailSamplingThreshold — SAF: the collector's calculateThreshold, same
// big.Float arithmetic (boundary precision 64 x NewFloat(ratio)) so the
// boundary trace ids land on the same side.
func TailSamplingThreshold(percent float64) uint64 {
	boundary := new(big.Float).SetInt(new(big.Int).SetUint64(math.MaxUint64))
	res, _ := boundary.Mul(boundary, big.NewFloat(percent/100)).Uint64()
	return res
}

// Sampling modes.
const (
	SamplingModeOff    = "off"
	SamplingModeShadow = "shadow"
	SamplingModeOn     = "on"
)

// IngestSampling — blob.
type IngestSampling struct {
	Mode string `json:"mode"` // off | shadow | on
	// DefaultWeight — weight when no source and no rule decides (1 = the
	// span was not sampled, or Coremetry cannot tell).
	DefaultWeight float64 `json:"defaultWeight"`
	// HonorTracestate — read the W3C tracestate `ot=th:` threshold (OTel
	// probability sampling) when present. Nil = true.
	HonorTracestate *bool `json:"honorTracestate,omitempty"`
	// HonorAttributes — read explicit weight attributes (sampling.weight,
	// SampleRate) when present. Nil = true.
	HonorAttributes *bool          `json:"honorAttributes,omitempty"`
	Rules           []SamplingRule `json:"rules"`
	// OnSince — when mode last switched to "on" (v0.10.2435, Phase 2):
	// weighted reads, the UI "sampling-corrected from …" marker and the
	// error-rate baseline reset start here. Zero while not on. Server-owned:
	// SaveIngestSampling stamps it, a client value is ignored.
	OnSince   time.Time `json:"onSince,omitzero"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

// Active — the converter derives weights (shadow or on).
func (c IngestSampling) Active() bool {
	return c.Mode == SamplingModeShadow || c.Mode == SamplingModeOn
}

// Writes — the derived weight is stored (on); shadow stores 1.
func (c IngestSampling) Writes() bool { return c.Mode == SamplingModeOn }

// TracestateOn / AttributesOn — nil pointer = on.
func (c IngestSampling) TracestateOn() bool { return c.HonorTracestate == nil || *c.HonorTracestate }
func (c IngestSampling) AttributesOn() bool { return c.HonorAttributes == nil || *c.HonorAttributes }

// UsesTraceError — whether any rule needs the per-request trace-error set
// (the converter builds it only then).
func (c IngestSampling) UsesTraceError() bool {
	for _, r := range c.Rules {
		if r.TraceError {
			return true
		}
	}
	return false
}

// ClampSampleWeight — SAF: NaN/Inf/≤0 → 0 (= "not a weight", the caller
// falls through to the next source); <1 → 1 (an adjusted count is never
// below one); > MaxSampleWeight → MaxSampleWeight.
func ClampSampleWeight(w float64) float64 {
	if math.IsNaN(w) || math.IsInf(w, 0) || w <= 0 {
		return 0
	}
	if w < 1 {
		return 1
	}
	if w > MaxSampleWeight {
		return MaxSampleWeight
	}
	return w
}

// NormalizeIngestSampling — SAF: default weight 1, weights clamped, rules
// trimmed, invalid rules (weight ≤ 0, unknown status) dropped, list capped.
func NormalizeIngestSampling(c IngestSampling) IngestSampling {
	switch c.Mode = strings.ToLower(strings.TrimSpace(c.Mode)); c.Mode {
	case SamplingModeShadow, SamplingModeOn:
	default:
		c.Mode = SamplingModeOff
	}
	if w := ClampSampleWeight(c.DefaultWeight); w > 0 {
		c.DefaultWeight = w
	} else {
		c.DefaultWeight = 1
	}
	rules := make([]SamplingRule, 0, len(c.Rules))
	for _, r := range c.Rules {
		r.Name = clipField(r.Name)
		r.Attr = clipField(r.Attr)
		r.Value = clipField(r.Value)
		r.Status = strings.ToLower(strings.TrimSpace(r.Status))
		if r.Status != "" && r.Status != "error" {
			continue
		}
		if r.Attr == "" {
			r.Value = ""
		}
		r.hashThreshold = 0
		if r.HashPercent != 0 {
			if math.IsNaN(r.HashPercent) || r.HashPercent < 0 || r.HashPercent > 100 {
				continue
			}
			r.HashSalt = clipField(r.HashSalt)
			if r.HashSalt == "" {
				r.HashSalt = DefaultTailSamplingHashSalt
			}
			switch r.HashBucket = strings.ToLower(strings.TrimSpace(r.HashBucket)); r.HashBucket {
			case "", "in":
				r.HashBucket = "in"
			case "out":
			default:
				continue
			}
			r.hashThreshold = TailSamplingThreshold(r.HashPercent)
		} else {
			r.HashSalt, r.HashBucket = "", ""
		}
		w := ClampSampleWeight(r.Weight)
		if w == 0 {
			continue
		}
		r.Weight = w
		if r.Name == "" {
			r.Name = "rule-" + strconv.Itoa(len(rules)+1)
		}
		rules = append(rules, r)
		if len(rules) == MaxSamplingRules {
			break
		}
	}
	c.Rules = rules
	return c
}

func clipField(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxRuleField {
		s = s[:maxRuleField]
	}
	return s
}

var ingestSamplingCur atomic.Pointer[IngestSampling]

// CurrentIngestSampling — process-local last loaded setting (no CH read; the
// converter calls it once per export request).
func CurrentIngestSampling() IngestSampling {
	if c := ingestSamplingCur.Load(); c != nil {
		return *c
	}
	return NormalizeIngestSampling(IngestSampling{})
}

// SetIngestSampling — publishes to this pod immediately.
func SetIngestSampling(c IngestSampling) {
	n := NormalizeIngestSampling(c)
	ingestSamplingCur.Store(&n)
}

// parseIngestSampling — SAF: empty blob = default (off), corrupt = ok=false.
func parseIngestSampling(raw []byte) (IngestSampling, bool) {
	if len(raw) == 0 {
		return NormalizeIngestSampling(IngestSampling{}), true
	}
	var c IngestSampling
	if err := json.Unmarshal(raw, &c); err != nil {
		return IngestSampling{}, false
	}
	return NormalizeIngestSampling(c), true
}

// SaveIngestSampling — writes the normalized blob and publishes it here.
func (s *Store) SaveIngestSampling(ctx context.Context, c IngestSampling) (IngestSampling, error) {
	var prev IngestSampling
	if raw, err := s.GetSetting(WithoutSettingsSnapshot(ctx), IngestSamplingKey); err == nil {
		prev, _ = parseIngestSampling(raw)
	}
	c.OnSince = samplingOnSince(prev, c.Mode, time.Now())
	n := NormalizeIngestSampling(c)
	raw, err := json.Marshal(n)
	if err != nil {
		return IngestSampling{}, err
	}
	if err := s.PutSetting(ctx, IngestSamplingKey, raw); err != nil {
		return IngestSampling{}, err
	}
	SetIngestSampling(n)
	return n, nil
}

// samplingOnSince — SAF: keeps the previous OnSince while mode stays on,
// stamps now on an off/shadow → on transition, clears it otherwise.
func samplingOnSince(prev IngestSampling, newMode string, now time.Time) time.Time {
	if strings.ToLower(strings.TrimSpace(newMode)) != SamplingModeOn {
		return time.Time{}
	}
	if prev.Mode == SamplingModeOn && !prev.OnSince.IsZero() {
		return prev.OnSince
	}
	return now.UTC().Truncate(time.Second)
}

// LoadIngestSampling — boot + 30 s refresh. A corrupt blob keeps the last value.
func (s *Store) LoadIngestSampling(ctx context.Context) error {
	raw, err := s.GetSetting(ctx, IngestSamplingKey)
	if err != nil {
		return err
	}
	c, ok := parseIngestSampling(raw)
	if !ok {
		log.Printf("[ingest-sampling] ingest.sampling setting is corrupt — keeping the last value")
		return nil
	}
	SetIngestSampling(c)
	return nil
}
