package selfobs

// sampling_rule_shadow.go — v0.10.2441: sampling.rule_shadow{family, outcome}
// — span error-rate rule evaluations scored on stored and on model-weighted
// values while ingest.sampling.mode = shadow (chstore/sampling_rule_shadow.go).
// family = alert | anomaly; outcome = both | today_only | weighted_only |
// neither (cardinality 2×4; the per-rule breakdown lives in the admin card).

import (
	"context"
	"log"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	ruleShadowOnce sync.Once
	ruleShadowCtr  metric.Int64Counter
	ruleShadowSets setCache[[2]string]
)

// CountSamplingRuleShadow — family and outcome must be FIXED values.
func CountSamplingRuleShadow(family, outcome string) {
	ruleShadowOnce.Do(func() {
		var e error
		if ruleShadowCtr, e = Meter().Int64Counter("sampling.rule_shadow",
			metric.WithDescription("Span error-rate rule evaluations, stored vs sampling-weighted (shadow): family=alert|anomaly, outcome=both|today_only|weighted_only|neither")); e != nil {
			log.Printf("[selfobs] sampling.rule_shadow: %v", e)
		}
	})
	if ruleShadowCtr == nil {
		return
	}
	set := ruleShadowSets.get([2]string{family, outcome}, func() attribute.Set {
		return attribute.NewSet(attribute.String("family", family), attribute.String("outcome", outcome))
	})
	ruleShadowCtr.Add(context.Background(), 1, metric.WithAttributeSet(set))
}
