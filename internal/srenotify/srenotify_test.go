package srenotify

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/sre"
)

func TestConvertCarriesAllFields(t *testing.T) {
	got := Convert(sre.NotifyDigest{
		InvestigationID: "sre-p-1", ProblemID: "p-1", State: sre.StateDone, Finished: true, Sufficient: true,
		TopKind: "deploy", TopLabel: "Deploy",
		Evidence: []sre.NotifyEvidence{{Check: "deploy_near", Source: "deploys"}},
		Missing:  []sre.GateGap{{Code: sre.GapBlindSource, Source: "events", Status: sre.ReachError}},
		Blind:    []sre.ReachEntry{{Source: "pivot", Status: sre.ReachTimedOut}},
	})
	if got.InvestigationID != "sre-p-1" || !got.Finished || !got.Sufficient || got.TopKind != "deploy" ||
		len(got.Evidence) != 1 || got.Evidence[0].Source != "deploys" ||
		len(got.Missing) != 1 || got.Missing[0].Status != sre.ReachError ||
		len(got.Blind) != 1 || got.Blind[0].Source != "pivot" {
		t.Fatalf("%+v", got)
	}
}
