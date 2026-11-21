package chstore

// sampling_rule_shadow.go — v0.10.2441: what the span error-rate rules
// would do under sampling weighting, measured while ingest.sampling.mode =
// shadow (docs/architecture/sampling-weight.md "Rule thresholds").
//
// WHY NOT THE WEIGHTED SQL: in shadow the converter derives the weight but
// STORES 1 (IngestSampling.Writes), so the weighted MV states equal the
// unweighted ones — re-running a read through weighSQL would show zero
// difference. The shadow therefore re-scores the value the rule ALREADY
// read with a two-class model taken from the operator's own rules:
//
//	failing spans weigh ErrWeight, every other span OkWeight
//	weighted error % = p·E / (p·E + (1−p)·O)          (p = stored fraction)
//	represented / stored spans = p·E + (1−p)·O
//
// Zero extra ClickHouse reads; one multiply per (rule, service) per tick.
// Known bias: non-failing spans inside a failing trace are kept at weight 1
// upstream but weigh O here, so the estimate is at or BELOW the weighted
// value mode=on will read ("would stop firing" is an upper bound).
//
// The transform is monotone, so a threshold T on stored rates and
// ErrorPct(T) on weighted rates fire on exactly the same values under the
// model — that is the recommended retune value shown per rule.
//
// Nothing here changes alerting: mode off / on → no recording at all.
// Counts are process-local on the evaluator leader and persisted as the
// system_settings blob `sampling.ruleShadow` (≤ every 5 min) so the api pod
// can show them; a leader change starts a new window (Since).

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/selfobs"
)

// RuleShadowKey — system_settings key of the persisted counts.
const RuleShadowKey = "sampling.ruleShadow"

// Rule-shadow families.
const (
	RuleShadowFamilyAlert   = "alert"   // alert rules (error_rate, mq_*_rate)
	RuleShadowFamilyAnomaly = "anomaly" // anomaly:<svc>:error_rate (z + absFloor + ramp)
)

const (
	ruleShadowMaxRows    = 256
	ruleShadowFlushEvery = 5 * time.Minute
)

// SamplingShadowModel — the two-class weight model.
type SamplingShadowModel struct {
	ErrWeight float64 `json:"errWeight"`
	OkWeight  float64 `json:"okWeight"`
	// Basis — "error rule" (an explicit status=error / traceError rule set
	// ErrWeight) or "min/max weight" (smallest configured weight = the
	// unsampled class, largest = the sampled class).
	Basis string `json:"basis"`
}

// SamplingShadowModelFor — SAF: model from the settings; ok=false when the
// rules give no weight spread (tracestate-only weights, or every weight
// equal) — then nothing can be estimated.
func SamplingShadowModelFor(c IngestSampling) (SamplingShadowModel, bool) {
	def := c.DefaultWeight
	if def < 1 {
		def = 1
	}
	lo, hi := def, def
	errW := 0.0
	for _, r := range c.Rules {
		if r.Weight <= 0 {
			continue
		}
		lo, hi = math.Min(lo, r.Weight), math.Max(hi, r.Weight)
		if errW == 0 && (r.Status == "error" || r.TraceError) {
			errW = r.Weight
		}
	}
	m := SamplingShadowModel{ErrWeight: errW, OkWeight: hi, Basis: "error rule"}
	if errW == 0 {
		m.ErrWeight, m.Basis = lo, "min/max weight"
	}
	if m.OkWeight <= m.ErrWeight {
		return SamplingShadowModel{}, false
	}
	return m, true
}

func shadowClampPct(pct float64) float64 {
	if math.IsNaN(pct) || pct < 0 {
		return 0
	}
	return math.Min(pct, 100)
}

// ErrorPct — SAF: stored error % → model-weighted error %.
func (m SamplingShadowModel) ErrorPct(pct float64) float64 {
	p := shadowClampPct(pct) / 100
	num := p * m.ErrWeight
	den := num + (1-p)*m.OkWeight
	if den <= 0 {
		return 0
	}
	return 100 * num / den
}

// RateFactor — SAF: represented / stored spans at the given stored error %.
func (m SamplingShadowModel) RateFactor(pct float64) float64 {
	p := shadowClampPct(pct) / 100
	return p*m.ErrWeight + (1-p)*m.OkWeight
}

// RuleShadowModel — the model while mode = shadow (ok=false otherwise:
// off and on record nothing).
func RuleShadowModel() (SamplingShadowModel, bool) {
	c := CurrentIngestSampling()
	if c.Mode != SamplingModeShadow {
		return SamplingShadowModel{}, false
	}
	return SamplingShadowModelFor(c)
}

// RuleShadowEligible — SAF: an alert metric whose read becomes weighted
// under mode=on and whose shift the two-class model describes. Since
// v0.10.2442 every span read is weighted (MV and raw, any window), so:
// error_rate and every transport *_rate whose numerator is a failing span
// (status_code='error', or HTTP 5xx — a server span with 5xx is an error
// span by OTel semconv). http_4xx_rate is NOT: 4xx server spans are not
// error spans, they carry the non-failing weight, and the model's E/O
// transform does not describe their ratio. The window no longer matters
// (the parameter stays for the callers' shape).
func RuleShadowEligible(metric string, _ uint32) bool {
	if metric == "error_rate" {
		return true
	}
	if TransportOp(metric) != "error_rate" {
		return false
	}
	_, num, ok := TransportFilter(metric)
	return ok && (num == "status_code='error'" || num == "http_status >= 500")
}

// RuleShadowRow — one rule's counts. Evals = (rule, service) evaluations.
type RuleShadowRow struct {
	Family     string  `json:"family"`
	RuleID     string  `json:"ruleId"`
	Name       string  `json:"name,omitempty"`
	Metric     string  `json:"metric"`
	Comparator string  `json:"comparator,omitempty"`
	Threshold  float64 `json:"threshold"`
	// Retune — the threshold on weighted rates that fires on the same
	// values under the model (ErrorPct(Threshold)); 0 for anomaly rows.
	Retune        float64 `json:"retune"`
	Evals         uint64  `json:"evals"`
	TodayFires    uint64  `json:"todayFires"`
	WeightedFires uint64  `json:"weightedFires"`
	TodayOnly     uint64  `json:"todayOnly"`    // fires today, would not under weighting
	WeightedOnly  uint64  `json:"weightedOnly"` // would fire under weighting, does not today
	LastService   string  `json:"lastService,omitempty"`
	LastToday     float64 `json:"lastToday"`
	LastWeighted  float64 `json:"lastWeighted"`
}

// RuleShadowState — the persisted blob.
type RuleShadowState struct {
	Since     time.Time           `json:"since"`
	UpdatedAt time.Time           `json:"updatedAt"`
	Model     SamplingShadowModel `json:"model"`
	Rows      []RuleShadowRow     `json:"rows"`
	Overflow  uint64              `json:"overflow,omitempty"`
}

// RuleShadowObs — one observation.
type RuleShadowObs struct {
	Family, RuleID, Name, Metric, Comparator string
	Threshold                                float64
	Service                                  string
	Today, Weighted                          float64 // values (stored / model-weighted)
	TodayFires, WeightedFires                bool
}

type ruleShadowTally struct {
	mu        sync.Mutex
	since     time.Time
	model     SamplingShadowModel
	rows      map[string]*RuleShadowRow
	overflow  uint64
	dirty     bool
	lastFlush time.Time
}

var ruleShadow ruleShadowTally

// ruleShadowOutcome — SAF: bounded self-obs label.
func ruleShadowOutcome(today, weighted bool) string {
	switch {
	case today && weighted:
		return "both"
	case today:
		return "today_only"
	case weighted:
		return "weighted_only"
	}
	return "neither"
}

// RecordRuleShadow — adds one observation under model m. A changed model
// (operator edited the rules) restarts the window.
func RecordRuleShadow(m SamplingShadowModel, o RuleShadowObs) {
	selfobs.CountSamplingRuleShadow(o.Family, ruleShadowOutcome(o.TodayFires, o.WeightedFires))
	ruleShadow.record(m, o, time.Now())
}

func (t *ruleShadowTally) record(m SamplingShadowModel, o RuleShadowObs, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rows == nil || t.model != m {
		t.rows, t.overflow, t.since, t.model = map[string]*RuleShadowRow{}, 0, now, m
	}
	key := o.Family + "|" + o.RuleID
	r, ok := t.rows[key]
	if !ok {
		if len(t.rows) >= ruleShadowMaxRows {
			t.overflow++
			return
		}
		r = &RuleShadowRow{Family: o.Family, RuleID: o.RuleID}
		t.rows[key] = r
	}
	r.Name, r.Metric, r.Comparator, r.Threshold = o.Name, o.Metric, o.Comparator, o.Threshold
	if o.Family == RuleShadowFamilyAlert {
		r.Retune = m.ErrorPct(o.Threshold)
	}
	r.Evals++
	if o.TodayFires {
		r.TodayFires++
	}
	if o.WeightedFires {
		r.WeightedFires++
	}
	if o.TodayFires && !o.WeightedFires {
		r.TodayOnly++
	}
	if o.WeightedFires && !o.TodayFires {
		r.WeightedOnly++
	}
	if o.TodayFires || o.WeightedFires || r.LastService == "" {
		r.LastService, r.LastToday, r.LastWeighted = o.Service, o.Today, o.Weighted
	}
	t.dirty = true
}

func (t *ruleShadowTally) snapshot(now time.Time) RuleShadowState {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := RuleShadowState{Since: t.since, UpdatedAt: now, Model: t.model, Overflow: t.overflow,
		Rows: make([]RuleShadowRow, 0, len(t.rows))}
	for _, r := range t.rows {
		out.Rows = append(out.Rows, *r)
	}
	sortRuleShadowRows(out.Rows)
	return out
}

// sortRuleShadowRows — SAF: largest disagreement first, then rule id.
func sortRuleShadowRows(rows []RuleShadowRow) {
	sort.Slice(rows, func(i, j int) bool {
		di, dj := rows[i].TodayOnly+rows[i].WeightedOnly, rows[j].TodayOnly+rows[j].WeightedOnly
		if di != dj {
			return di > dj
		}
		if rows[i].Family != rows[j].Family {
			return rows[i].Family < rows[j].Family
		}
		return rows[i].RuleID < rows[j].RuleID
	})
}

// ruleShadowFlushDue — SAF.
func ruleShadowFlushDue(dirty bool, last, now time.Time) bool {
	return dirty && (last.IsZero() || now.Sub(last) >= ruleShadowFlushEvery)
}

// FlushRuleShadow — evaluator leader, end of tick: persists the counts at
// most every 5 minutes, only when something was recorded (mode off / on =
// never dirty = zero writes).
func (s *Store) FlushRuleShadow(ctx context.Context) error {
	now := time.Now()
	ruleShadow.mu.Lock()
	due := ruleShadowFlushDue(ruleShadow.dirty, ruleShadow.lastFlush, now)
	ruleShadow.mu.Unlock()
	if !due {
		return nil
	}
	raw, err := json.Marshal(ruleShadow.snapshot(now))
	if err != nil {
		return err
	}
	if err := s.PutSetting(ctx, RuleShadowKey, raw); err != nil {
		return err
	}
	ruleShadow.mu.Lock()
	ruleShadow.dirty, ruleShadow.lastFlush = false, now
	ruleShadow.mu.Unlock()
	return nil
}

// LoadRuleShadow — the persisted counts (zero state when never written).
func (s *Store) LoadRuleShadow(ctx context.Context) (RuleShadowState, error) {
	raw, err := s.GetSetting(ctx, RuleShadowKey)
	if err != nil || len(raw) == 0 {
		return RuleShadowState{Rows: []RuleShadowRow{}}, err
	}
	var st RuleShadowState
	if err := json.Unmarshal(raw, &st); err != nil {
		return RuleShadowState{Rows: []RuleShadowRow{}}, err
	}
	if st.Rows == nil {
		st.Rows = []RuleShadowRow{}
	}
	return st, nil
}

// RuleRetuneHint — recommended value for one absolute span threshold.
type RuleRetuneHint struct {
	RuleID     string  `json:"ruleId"`
	Name       string  `json:"name"`
	Metric     string  `json:"metric"`
	Comparator string  `json:"comparator"`
	Threshold  float64 `json:"threshold"`
	Retune     float64 `json:"retune"`
	// Note — how Retune was derived ("same firing under the model" for
	// error rates; "×OkWeight upper bound" for span counts / request rate).
	Note string `json:"note"`
}

// RuleRetuneHints — SAF: retune values for the enabled alert rules whose
// absolute threshold shifts under mode=on (span path; a rule the VM source
// serves for a service is unaffected for that service — not knowable here).
func RuleRetuneHints(rules []AlertRule, m SamplingShadowModel) []RuleRetuneHint {
	out := []RuleRetuneHint{}
	for _, r := range rules {
		if !r.Enabled || r.LogQuery != "" || r.WatcherJSON != "" || r.Target != nil {
			continue
		}
		h := RuleRetuneHint{RuleID: r.ID, Name: r.Name, Metric: r.Metric, Comparator: r.Comparator, Threshold: r.Threshold}
		switch {
		case RuleShadowEligible(r.Metric, r.WindowSec):
			h.Retune, h.Note = m.ErrorPct(r.Threshold), "same firing under the model"
		case r.Metric == "request_rate": // v0.10.2442 — raw (sub-5m) reads weighted too
			h.Retune, h.Note = r.Threshold*m.OkWeight, "×okWeight (upper bound; less where errors are a large share)"
		default:
			continue
		}
		out = append(out, h)
	}
	return out
}
