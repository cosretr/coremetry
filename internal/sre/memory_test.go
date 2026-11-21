package sre

// CoSRE Faz E — ekip notları AI SRE anlatımında: blok + çelişki eki yalnız
// anlatıma girer; replay varyantı noMemory kapatır; replay'de Problem'den
// sonra öğrenilen notlar ve Problem'in kendi kök neden notu girmez (cevap
// sızıntısı yok); skor / plan / kontrol notları görmez.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/cosrememory"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestSRENarrationMemory(t *testing.T) {
	at := start.UnixNano()
	src := func(at int64, problem string) cosrememory.Source {
		return cosrememory.Source{Kind: cosrememory.SourceRootCause, By: "Ece", At: at, ProblemID: problem}
	}
	items := []cosrememory.Item{
		{ID: "old", Statement: "checkout geçmişte pay-db havuzu doldu", Scope: cosrememory.Scope{Kind: "service", Value: "checkout"}, Source: src(at-int64(time.Hour), "p0"), Status: "active"},
		{ID: "own", Statement: "bu problemin kendi kök nedeni sızmamalı", Scope: cosrememory.Scope{Kind: "service", Value: "checkout"}, Source: src(at-int64(time.Minute), "p1"), Status: "active"},
		{ID: "later", Statement: "problemden sonra öğrenilen not sızmamalı", Scope: cosrememory.Scope{Kind: "service", Value: "checkout"}, Source: src(at+int64(time.Hour), "p9"), Status: "active"},
	}
	run := func(inv *Investigation) *fakeNarrator {
		n := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: x"}
		rd := okReaders(&calls{}, n)
		rd.memory = func() []cosrememory.Item { return items }
		if st := narrate(context.Background(), rd, inv, chstore.Problem{ID: "p1", Service: "checkout", StartedAt: at}, time.Now); st.State != StepOK {
			t.Fatalf("anlatım: %+v", st)
		}
		return n
	}
	// Canlı: kendi kök neden notu hariç, diğerleri girer.
	inv := newInv()
	n := run(inv)
	if !strings.Contains(n.user, "[N1] [servis checkout]") || !strings.Contains(n.user, cosrememory.FenceClose) || strings.Contains(n.user, "kendi kök nedeni") {
		t.Fatalf("canlı anlatım:\n%s", n.user)
	}
	if !strings.HasPrefix(n.sys, copilot.CosreMemoryAddendum()) || !strings.HasSuffix(n.sys, copilot.DataNotInstruction) || inv.MemoryNotes != 2 {
		t.Fatalf("ek önde, çerçeve sonda; not sayısı %d", inv.MemoryNotes)
	}
	// Replay: yalnız Problem'den önce öğrenilen + kendi notu hariç.
	rinv := newInv()
	rinv.Kind, rinv.ProblemID, rinv.ProblemAt, rinv.AsOf = KindReplay, "p1", at, at+int64(30*time.Minute)
	n = run(rinv)
	if !strings.Contains(n.user, "pay-db havuzu doldu") || strings.Contains(n.user, "sonra öğrenilen") || strings.Contains(n.user, "kendi kök nedeni") || rinv.MemoryNotes != 1 {
		t.Fatalf("replay süzgeci:\n%s", n.user)
	}
	// Varyant noMemory: blok ve ek yok, metin bayt bayt notsuz.
	ninv := newInv()
	ninv.NoMemory = true
	n = run(ninv)
	if strings.Contains(n.user, cosrememory.BlockHeader) || strings.Contains(n.sys, "EKİP NOTLARI") || ninv.MemoryNotes != 0 {
		t.Fatal("noMemory varyantı notları kapatmalı")
	}
	if n.sys != copilot.SystemPromptSREInvestigation() {
		t.Error("notsuz sistem mesajı eski olmalı")
	}
	if got := replayInvestigation(ReplayRequest{ProblemID: "p1", NoMemory: true, ProblemStartedAt: at}); !got.NoMemory {
		t.Error("replay isteği noMemory'yi kayda taşımalı")
	}
}

// TestMemoryNeverReachesScoring — ekip notlarına dokunan tek yer anlatım
// (narrate.go) ve okuyucu dikişi (orchestrator.go).
func TestMemoryNeverReachesScoring(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "narrate.go" || f == "orchestrator.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{"cosrememory", "CurrentCosreMemory", ".memory("} {
			if strings.Contains(string(b), n) {
				t.Errorf("%s: %q — ekip notu yalnız anlatıma girer", f, n)
			}
		}
	}
}
