package ml

// log_templates_masker_test.go — v0.10.1829: maske sürümü geçişinde ML şablon
// dedektörleri. Operator-reported ("GUID, UUID, id değerlerini yeni log
// pattern'i algılamasın") düzeltmesi Drain şablonlarını değiştirdi: (a) eski
// maskeli izlenen aile yeni maskeli adayla aynı aile sayılmalı, (b) sessiz
// pencerede rare_template olay üretmemeli.

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestSelectFamilies_OldMaskerSampleDedupsNewCandidate(t *testing.T) {
	tracked := map[string]tplFamily{}
	// v1 maskesi pod son ekini maskelemiyordu.
	old := tplFamily{service: "orders-api", phrase: "replica became ready after", sample: "pod orders-api-7d9f6-x2k4p replica became ready after rollout"}
	tracked[old.part()+"\x00orders-api"] = old
	cands := []chstore.LogTemplate{
		{Template: "pod orders-api-<*> replica became ready after rollout", Services: []string{"orders-api"}},
	}
	if got := selectFamilies(tracked, cands, 100); len(got) != 0 {
		t.Fatalf("old-masker family must absorb the new-masker candidate, got %+v", got)
	}
}

func TestRareTemplateQuiet(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	if rareTemplateQuietAt(chstore.LogMaskerState{Version: 2}, nil, now) {
		t.Fatal("settled version must not be quiet")
	}
	if !rareTemplateQuietAt(chstore.LogMaskerState{Version: 2, QuietUntil: now.Add(time.Hour).UnixNano()}, nil, now) {
		t.Fatal("inside window must be quiet")
	}
	if !rareTemplateQuietAt(chstore.LogMaskerState{}, errors.New("ch"), now) {
		t.Fatal("unreadable state must be quiet (fail-closed)")
	}
}

// Kapı ulaşılabilir: rare_template dalı rareQuiet'e bakıyor.
func TestRareTemplateGateWired(t *testing.T) {
	src, err := os.ReadFile("log_templates.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "rareQuiet := rareTemplateQuiet(deps.Store.LogMaskerStateCached(ctx))") ||
		!strings.Contains(s, "if !rareQuiet && cfg.DetectorOn(detRareTemplate)") {
		t.Fatal("rare_template quiet gate not wired into Tick")
	}
}
