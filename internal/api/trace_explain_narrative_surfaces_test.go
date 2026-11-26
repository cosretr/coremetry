package api

// trace_explain_narrative_surfaces_test.go — v0.10.2482: anlatım biçiminin
// takip, kod dalı, odaklı soru ve prefix sayımı yüzeyleri.

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
)

// Anlatım koşusunun sinyalleri işaretli (ayrı sayım); işaret "action=" önekini
// bozmaz; bölümlü koşu işaretsiz (bayt bayt eski ayrıntı).
func TestLatencyOwnerSignalsNarrativeMarked(t *testing.T) {
	ev := latencyOwnerEvent{Applied: true, TraceID: "t1", Target: "db.ext.example", Action: prefixActionAdded, Narrative: true}
	got := latencyOwnerSignals("x1", ev)
	if len(got) != 2 || !strings.HasPrefix(got[0].Detail, chstore.PrefixStyleNarrative+" ") ||
		!strings.HasPrefix(got[1].Detail, "action=added "+chstore.PrefixStyleNarrative+" ") {
		t.Fatalf("%+v", got)
	}
	ev.Narrative = false
	got = latencyOwnerSignals("x1", ev)
	if got[0].Detail != "trace=t1" || got[1].Detail != "action=added trace=t1 target=db.ext.example" {
		t.Fatalf("bölümlü ayrıntı değişmemeli: %+v", got)
	}
	if !traceNarrativeStyleFrom(traceNarrativeCtx(context.Background())) || traceNarrativeStyleFrom(context.Background()) {
		t.Fatal("ctx işareti")
	}
	if !traceNarrativeStyleFrom(traceNarrativeCodeCtx(context.Background())) {
		t.Fatal("kod dalı ctx işareti")
	}
}

// Takip: anlatımda talimat değişir, bağlam/kanıt blokları aynen kalır;
// bölümlü takip bayt bayt eski.
func TestTraceFollowUpPromptNarrative(t *testing.T) {
	tf := traceFollowUp{TraceID: strings.Repeat("a", 32), Env: "prod", Chain: "\n\nHATA ZİNCİRİ — x", Critical: "\n\nKRİTİK YOL — y"}
	old := traceFollowUpPromptTR(&tf, "Business hata\nKARAR: z")
	if !strings.Contains(old, copilot.TraceFollowUpAddendumFor(false)) {
		t.Fatal("bölümlü takip eki")
	}
	tf.Narrative = true
	nar := traceFollowUpPromptTR(&tf, "Business hata\nKARAR: z")
	if !strings.Contains(nar, copilot.TraceFollowUpAddendumNarrativeFor(false)) || strings.Contains(nar, "**Özet:**") {
		t.Fatal("anlatım takip eki")
	}
	for _, keep := range []string{"HATA ZİNCİRİ — x", "KRİTİK YOL — y", "KARAR: z", "- env: prod"} {
		if !strings.Contains(nar, keep) {
			t.Errorf("anlatım takibi kanıtı/bağlamı korumalı: %q", keep)
		}
	}
}

// Kod dalı: bölümlü istem bayt bayt eski; anlatımda anlatım kod istemleri.
func TestTraceCodePromptAndKeyFor(t *testing.T) {
	stack := devops.CodeContext{Windows: []devops.CodeWindow{{Path: "a.java", Line: 3, FromLine: 1, ToLine: 5, Content: "1| a\n2| b\n>>> 3| c\n4| d\n5| e"}}}
	cand := devops.CodeContext{Source: devops.CodeSourceOperation, OperationSource: devops.OperationSourceFunctionCode, Windows: stack.Windows}
	for _, cc := range []devops.CodeContext{stack, cand, {}} {
		s0, k0 := traceCodePromptAndKey(cc, schemaEvidence{})
		s1, k1 := traceCodePromptAndKeyFor(false, cc, schemaEvidence{})
		if s0 != s1 || k0 != k1 {
			t.Fatal("bölümlü kod istemi değişmemeli")
		}
		s2, k2 := traceCodePromptAndKeyFor(true, cc, schemaEvidence{})
		if k2 != k0 || !strings.HasPrefix(s2, strings.TrimSuffix(copilot.SystemPromptTraceNarrative(), copilot.AnswerInTurkish)) {
			t.Fatal("anlatım kod istemi anlatım gövdesinden")
		}
	}
	if s, _ := traceCodePromptAndKeyFor(true, cand, schemaEvidence{}); s != copilot.SystemPromptTraceNarrativeWithCandidate() {
		t.Fatal("aday operasyonda aday eki")
	}
}

// TestTraceEvidenceCarriesNoCodeContent — v0.9.1254 kuralı ("kod İÇERİĞİ
// tarayıcıya gitmez") v0.10.2482'de AYNEN: trace_evidence bloğunun ve MCP
// explain_trace evidence özünün JSON şeklinde kod içeriği taşıyabilecek alan
// yok (yansımayla tüm iç tipler gezilir).
func TestTraceEvidenceCarriesNoCodeContent(t *testing.T) {
	banned := map[string]bool{"code": true, "content": true, "text": true, "source": true, "excerpt": true, "snippet": true, "lines": true, "window": true, "windows": true}
	seen := map[reflect.Type]bool{}
	var walk func(rt reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if banned[strings.ToLower(name)] || banned[strings.ToLower(f.Name)] {
				t.Errorf("%s.%s (json %q): kod içeriği taşıyabilecek alan tarayıcı yüküne girmemeli", path, f.Name, name)
			}
			walk(f.Type, path+"."+f.Name)
		}
	}
	walk(reflect.TypeOf(traceEvidenceView{}), "traceEvidenceView")
	walk(reflect.TypeOf(mcpTraceEvidenceView{}), "mcpTraceEvidenceView")
}

// Odaklı soru (anlatım): kanıt bloğu modelden ÖNCE, künye anlatım kipinde,
// önbelleğe girmez. Kod turu (anlatım): blok + anlatım kod istemi + kod künyesi.
func TestNarrativeFocusedAndCodeTurns(t *testing.T) {
	p := newDefProvider(t, "Yavaşlık\nKARAR: x.\nSonraki adım: y.")
	s := defServer(t, p)
	var evs []xtEvent
	to := time.Now()
	if h, o := s.guidedTraceExplain(context.Background(), xtRecorder(&evs), guidedRoute{Intent: guidedTraceByID, TraceID: defTestTrace},
		"bu trace neden yavaş", to.Add(-time.Hour), to, ""); !h || !o {
		t.Fatal("odaklı soru")
	}
	kinds := xtKinds(evs)
	if iB, iA := strings.Index(kinds, blocks.TypeTraceEvidence), strings.Index(kinds, "answer"); iB < 0 || iA < iB {
		t.Fatalf("kanıt bloğu cevaptan önce yayılmalı: %s", kinds)
	}
	meta, _ := xtAnswer(t, evs)["explain"].(chatExplainMeta)
	if meta.Renderer != explainRendererTraceNarrative || meta.Cached {
		t.Fatalf("künye %+v", meta)
	}
	if sys, _ := p.lastMessages(t); sys != copilot.SystemPromptTraceNarrative() {
		t.Fatal("odaklı soru anlatım istemiyle")
	}

	evs = nil
	h, ok := s.chatExplainTurn(context.Background(), xtRecorder(&evs), &chatExplainAsk{Kind: "trace", ID: defTestTrace, Code: true}, time.UTC)
	if !h || !ok {
		t.Fatalf("kod turu h=%v ok=%v", h, ok)
	}
	if !strings.Contains(xtKinds(evs), blocks.TypeTraceEvidence) {
		t.Fatalf("kod turu kanıt bloğu yaymalı: %s", xtKinds(evs))
	}
	if sys, _ := p.lastMessages(t); sys != copilot.SystemPromptTraceNarrativeWithCode() {
		t.Fatal("kod turu anlatım kod istemiyle")
	}
	m2, _ := xtAnswer(t, evs)["explain"].(chatExplainMeta)
	if m2.Renderer != explainRendererTraceNarrative || m2.Code == nil {
		t.Fatalf("kod turu künyesi %+v", m2)
	}
}

// Odaklı soru künyesi: anlatım çizim kipi, önbellek alanı yok.
func TestTraceFocusedExplainMeta(t *testing.T) {
	m := traceFocusedExplainMeta("abc", traceExplainInput{Evidence: []string{"s1"}})
	if m.Renderer != explainRendererTraceNarrative || m.Kind != explainAskTrace || m.ID != "abc" || m.Cached || len(m.EvidenceSpanIDs) != 1 {
		t.Fatalf("%+v", m)
	}
}
