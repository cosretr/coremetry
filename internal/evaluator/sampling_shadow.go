package evaluator

// sampling_shadow.go — v0.10.2441: while ingest.sampling.mode = shadow,
// every span-sourced error-rate rule evaluation is also scored on the
// model-weighted value (chstore/sampling_rule_shadow.go). Observation only:
// the breach decision, the Problem and the notification use the stored
// value exactly as before. Zero extra reads.

import "github.com/cilcenk/coremetry/internal/chstore"

// samplingShadowObs — SAF: the observation for one evaluation, ok=false
// when the pair is not shadowed (mode ≠ shadow → no model, VM source =
// unsampled, metric/window whose read stays unweighted under mode=on).
func samplingShadowObs(m chstore.SamplingShadowModel, modelOK bool, r chstore.AlertRule, service string, value float64, vm, breached bool) (chstore.RuleShadowObs, bool) {
	if !modelOK || vm || !chstore.RuleShadowEligible(r.Metric, r.WindowSec) {
		return chstore.RuleShadowObs{}, false
	}
	w := m.ErrorPct(value)
	return chstore.RuleShadowObs{
		Family: chstore.RuleShadowFamilyAlert, RuleID: r.ID, Name: r.Name, Metric: r.Metric,
		Comparator: r.Comparator, Threshold: r.Threshold, Service: service,
		Today: value, Weighted: w, TodayFires: breached, WeightedFires: compare(w, r.Comparator, r.Threshold),
	}, true
}

func recordSamplingShadow(r chstore.AlertRule, service string, m ruleMeasurement, breached bool) {
	model, ok := chstore.RuleShadowModel()
	if o, rec := samplingShadowObs(model, ok, r, service, m.value, m.src.VM, breached); rec {
		chstore.RecordRuleShadow(model, o)
	}
}
