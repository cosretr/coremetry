package anomaly

// sampling_shadow.go — v0.10.2441: while ingest.sampling.mode = shadow the
// span-sourced error_rate decision is re-run on model-weighted series
// (chstore/sampling_rule_shadow.go): every bucket and seasonal value goes
// through ErrorPct, the request-rate series through RateFactor. The z-score
// is nearly scale-free, so the difference comes from the ABSOLUTE gates:
// AbsFloor (1 %), the ramp's MinRatioPct / MinErrorsPerSec and the
// MinBaselineRate volume gate. Observation only — the real outcome is
// applied unchanged. Approximates mode=on after the baseline re-start
// (BaselineFloor): it scores today's history as if it had been weighted.

import "github.com/cilcenk/coremetry/internal/chstore"

// weightSeries — SAF: model-weighted copy of an error-% series.
func weightSeries(m chstore.SamplingShadowModel, vals []float64) []float64 {
	if vals == nil {
		return nil
	}
	out := make([]float64, len(vals))
	for i, v := range vals {
		out[i] = m.ErrorPct(v)
	}
	return out
}

// weightRates — SAF: request rate × represented/stored at that bucket's
// stored error %; a series not aligned with the buckets is left as is.
func weightRates(m chstore.SamplingShadowModel, rates, buckets []float64) []float64 {
	if len(rates) != len(buckets) {
		return rates
	}
	out := make([]float64, len(rates))
	for i, r := range rates {
		out[i] = r * m.RateFactor(buckets[i])
	}
	return out
}

// shadowAnomalyErrorRate — SAF: the observation for one service, ok=false
// when not shadowed (not error_rate, VM-sourced series, no model).
func shadowAnomalyErrorRate(m chstore.SamplingShadowModel, modelOK bool, metric, svc string, vm bool,
	buckets, seasonal, rates []float64, minSamples int, hasOpen bool, bl batchLatSeries,
	sens chstore.AnomalySensitivityConfig, today anomalyOutcome) (chstore.RuleShadowObs, bool) {
	if !modelOK || vm || metric != errorRampMetric || len(buckets) == 0 {
		return chstore.RuleShadowObs{}, false
	}
	w := evaluateAnomaly(metric, weightSeries(m, buckets), weightSeries(m, seasonal), weightRates(m, rates, buckets), minSamples, hasOpen, bl, sens)
	cur := buckets[len(buckets)-1]
	return chstore.RuleShadowObs{
		Family: chstore.RuleShadowFamilyAnomaly, RuleID: "anomaly:*:" + metric, Name: "ML error-rate anomaly (z / ramp)",
		Metric: metric, Threshold: policyFor(metric, sens).absFloor, Service: svc,
		Today: cur, Weighted: m.ErrorPct(cur),
		TodayFires: today.Action == "open", WeightedFires: w.Action == "open",
	}, true
}
