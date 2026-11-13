package api

// ai_evalset_v2sink_test.go — v0.10.2106 (CoSRE kalite döngüsü D1,
// docs/cosre/quality-loop-audit.md §3.1): evalset ajan vakası v2 yolunda.
//
// Pinlenenler: (1) part sink'i uistream çeviricisinin ürettiği parçaları
// eksiksiz ve sırayla toplar (fikstür ui-stream-v1-linkcard); tavan aşılırsa
// sayar, düşürür; Close idempotent. (2) sunucu ajanı v2 gövdesinden koşar —
// akışta v2-yalnız data-scope parçası var (v1 yazıcısı onu düşürür);
// cevap metni (rubrik girdisi) Sink'ten aynen gelir. (3) koşu satırı
// path=v2. (4) farklı yollu iki koşunun kıyası Comparable=false + not.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

// feedFixture — testdata/cosre/<name>.events.jsonl olaylarını sink'e verir.
func feedFixture(t *testing.T, name string, sink chatSink) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "cosre", name+".events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev struct {
			Kind    string          `json:"kind"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		sink.Emit(ev.Kind, ev.Payload)
	}
	sink.Close()
}

func partTypes(parts []uistream.Part) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s, _ := p["type"].(string)
		out = append(out, s)
	}
	return out
}

func TestEvalPartSinkCollectsLinkCardFixture(t *testing.T) {
	ps := newEvalPartSink()
	feedFixture(t, "ui-stream-v1-linkcard", ps)
	parts, dropped := ps.snapshot()
	if dropped != 0 {
		t.Fatalf("düşen parça %d", dropped)
	}
	if got, want := partsJSON(t, parts), emitterParts(t, "ui-stream-v1-linkcard"); got != want {
		t.Fatalf("parçalar Emitter'ın yazdığıyla aynı değil:\n got %s\nwant %s", got, want)
	}
	types := partTypes(parts)
	for _, need := range []string{"start", "tool-input-available", "data-links", "finish"} {
		if !hasName(types, need) {
			t.Errorf("%q parçası yok: %v", need, types)
		}
	}
	var links map[string]any
	for _, p := range parts {
		if p["type"] == "data-links" {
			links, _ = p["data"].(map[string]any)
		}
	}
	if links == nil || links["title"] == nil {
		t.Errorf("data-links kartı title taşımalı: %v", links)
	}
	// Close idempotent: ikinci kapanış parça eklemez.
	ps.Close()
	if again, _ := ps.snapshot(); len(again) != len(parts) {
		t.Fatalf("ikinci Close parça ekledi: %d → %d", len(parts), len(again))
	}
}

// emitterParts — aynı fikstür üretim yazıcısından (uistream.Emitter, v2 ucu)
// geçince teldeki `data:` parçaları ([DONE] hariç), JSON dizi olarak.
func emitterParts(t *testing.T, name string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	em, ok := uistream.NewEmitter(rec, uistream.Options{MessageID: func() string { return "" }})
	if !ok {
		t.Fatal("emitter")
	}
	feedFixture(t, name, em)
	var parts []json.RawMessage
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == uistream.DoneMarker {
			continue
		}
		parts = append(parts, json.RawMessage(data))
	}
	b, _ := json.Marshal(parts)
	return string(b)
}

func partsJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestEvalPartSinkMatchesEmitterAllFixtures — her v2 fikstüründe sink,
// üretim yazıcısının teldeki parça dizisinin AYNISINI toplar.
func TestEvalPartSinkMatchesEmitterAllFixtures(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "cosre", "ui-stream-v1*.events.jsonl"))
	if len(files) < 11 {
		t.Fatalf("fikstür sayısı %d (<11)", len(files))
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".events.jsonl")
		t.Run(name, func(t *testing.T) {
			ps := newEvalPartSink()
			feedFixture(t, name, ps)
			parts, _ := ps.snapshot()
			if got, want := partsJSON(t, parts), emitterParts(t, name); got != want {
				t.Fatalf("got %s\nwant %s", got, want)
			}
		})
	}
}

func TestEvalPartSinkBounded(t *testing.T) {
	ps := newEvalPartSink()
	ps.max = 3
	feedFixture(t, "ui-stream-v1-linkcard", ps)
	parts, dropped := ps.snapshot()
	if len(parts) != 3 || dropped == 0 {
		t.Fatalf("tavan: %d parça, %d düşen", len(parts), dropped)
	}
}

// TestEvalAgentRunsV2Path — sunucu ajanı v2 gövdesinden koşar (UserTeam'siz
// vaka dahil): akışta data-scope (v2-yalnız) ve cevap metni; outcome cevabı
// aynen; kapsam çözümü OnScope'a ulaştı.
func TestEvalAgentRunsV2Path(t *testing.T) {
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		if n == 0 {
			return toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
		}
		return answerMsg("checkout p95 1830 ms; dünden yüksek.")
	})
	s, _ := newLoopTestServer(t, llm, "u1")
	s.copilot.SetIntentClassify(copilot.IntentOff)
	(&fakeTools{}).install(t)
	c := agentCase("a-v2", evalExpect{ExpectedTools: []string{"compare_periods"}, ExpectTier: "loop"})
	o, st, err := agentServerFn(s)(fullCtx(), c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(o.Answer, "1830") {
		t.Fatalf("cevap: %q", o.Answer)
	}
	types := partTypes(st.Parts)
	for _, need := range []string{"start", "data-scope", "tool-input-available", "finish"} {
		if !hasName(types, need) {
			t.Errorf("v2 akışında %q yok: %v", need, types)
		}
	}
	if st.Scope == nil {
		t.Error("v2 döngüsü kapsamı bildirmedi (OnScope)")
	}
}

// TestEvalRunRecordsV2Path — koşu satırı path=v2 (başlangıç + son yazım).
func TestEvalRunRecordsV2Path(t *testing.T) {
	c := agentCase("a-path", evalExpect{ExpectedTools: []string{"compare_periods"}, ExpectTier: "loop"})
	h, _, _ := newAgentEvalHarness(t, func(n int, _ map[string]any) map[string]any {
		if n == 0 {
			return toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
		}
		return answerMsg("checkout p95 1830 ms.")
	}, []evalCase{c})
	run, _ := runAgentEval(t, h, "")
	if run.Path != chstore.EvalRunPathV2 {
		t.Fatalf("özet path %q, want v2", run.Path)
	}
	h.store.mu.Lock()
	saves := append([]chstore.EvalRun(nil), h.store.saves...)
	h.store.mu.Unlock()
	if len(saves) == 0 {
		t.Fatal("satır yazılmadı")
	}
	for _, r := range saves {
		if r.Path != chstore.EvalRunPathV2 {
			t.Fatalf("yazılan satır path %q, want v2", r.Path)
		}
	}
}

// TestEvalCompareDifferentPath — yol farklı → kıyaslanamaz + not; aynı yol
// → model aynıysa kıyaslanır.
func TestEvalCompareDifferentPath(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	base := chstore.EvalRun{ID: "ev-a", StartedAt: at, Model: "m", Status: evalStatusDone, Path: chstore.EvalRunPathV1}
	head := chstore.EvalRun{ID: "ev-b", StartedAt: at, Model: "m", Status: evalStatusDone, Path: chstore.EvalRunPathV2}
	cmp := evalCompareRuns(base, head)
	if cmp.Comparable || !strings.Contains(cmp.Note, "v1 → v2") {
		t.Fatalf("farklı yol: comparable=%v note=%q", cmp.Comparable, cmp.Note)
	}
	if cmp.Base.Path != "v1" || cmp.Head.Path != "v2" {
		t.Fatalf("taraf yolları: %q %q", cmp.Base.Path, cmp.Head.Path)
	}
	head.Path = chstore.EvalRunPathV1
	if cmp := evalCompareRuns(base, head); !cmp.Comparable || strings.Contains(cmp.Note, "Sohbet yolu farklı") {
		t.Fatalf("aynı yol: comparable=%v note=%q", cmp.Comparable, cmp.Note)
	}
}
