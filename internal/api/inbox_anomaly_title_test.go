package api

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2487 (entity tutarlılığı dilim 3, denetim §1.4) — anomali Inbox
// satırının Title'ı standart manşetle AYNI cümle; ham "kind · pattern" ya da
// dedektör kimliği kullanıcıya gitmez.
func TestAnomalyInboxTitleIsStdHeadline(t *testing.T) {
	cases := []chstore.AnomalyEvent{
		{ID: "a1", Kind: "log_pattern", Pattern: "timeout to db", Service: "svc-a", PeakRatio: 3.2, CurrentRatio: 1.1},
		{ID: "a2", Kind: "ml:svc_error_rate", Pattern: "", Service: "svc-b", PeakRatio: 2},
		{ID: "a3", Kind: "heap", Pattern: "old-gen", Service: "svc-c"},
	}
	for _, e := range cases {
		it := anomalyToInbox(e)
		std := chstore.StandardizeAnomalyEvent(e, chstore.StdFacts{})
		if it.Title != std.Headline {
			t.Errorf("%s: Title %q != std.Headline %q", e.Kind, it.Title, std.Headline)
		}
		if strings.Contains(it.Title, e.Kind) {
			t.Errorf("%s: ham tür kimliği başlıkta: %q", e.Kind, it.Title)
		}
	}
}
