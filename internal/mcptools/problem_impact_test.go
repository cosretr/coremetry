package mcptools

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2482 — get_problem etki alanı: insan etiketli özet + birkaç sayı.
func TestImpactFieldOf(t *testing.T) {
	if impactFieldOf(nil) != nil {
		t.Error("özetsiz Problem alan taşımamalı")
	}
	s := chstore.FinishImpactSummary(&chstore.StdImpactSummary{Kind: chstore.ImpactSumErrors, Scope: chstore.ImpactScopeRoute,
		Route: "POST /checkout", PerMin: 340.4, SharePct: 14.04})
	f := impactFieldOf(s)
	if f["summary"] != "~340 istek/dk başarısız · uç nokta trafiğinin %14'ü" || f["failingRequestsPerMin"] != 340.0 ||
		f["sharePct"] != 14.0 || f["route"] != "POST /checkout" || f["kind"] != "errors" {
		t.Errorf("alan: %v", f)
	}
	if len(f) > 6 {
		t.Errorf("alan küçük kalmalı: %d anahtar", len(f))
	}
}
