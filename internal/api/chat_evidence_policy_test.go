package api

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot/evalset"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func evCand(typ string, keys ...string) mcptools.EvidencePart {
	return mcptools.EvidencePart{Block: mcptools.EvidenceBlock{Type: typ, Payload: map[string]any{"href": "/x/" + typ}}, ModelView: `{"evidence":"` + typ + `"}`, Keys: keys}
}

const evTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

func TestDecideEvidenceTable(t *testing.T) {
	all := []mcptools.EvidencePart{
		evCand(blocks.TypeTraceList, evTraceID), evCand(blocks.TypeLogs, "zzzz-log"), evCand(blocks.TypeAnomaly, "ml-0001"),
		evCand(blocks.TypeTrace, evTraceID, "POST /api/payments"), evCand(blocks.TypeException, "SocketTimeoutException"),
		evCand(blocks.TypeException, "IllegalStateException"), // tür başına son aday
	}
	cases := []struct {
		name, q, answer string
		class           answerClass
		cands           []mcptools.EvidencePart
		want            string
		cap             int
		missingAction   bool
	}{
		{"eşleşen tek kanıt", "checkout'ta ne oluyor", "Hatalar IllegalStateException kaynaklı, 412 kez.", classErrorHunt, all, "exception", 2, false},
		{"iki eşleşme öncelik sırası", "neden yavaş", "4bf92f35 trace'inde POST /api/payments 1,2 sn; IllegalStateException.", classExplain, all, "exception,trace", 2, false},
		{"tavan 2", "neden", "IllegalStateException 4bf92f35 ml-0001 zzzz-log", classExplain, all, "exception,trace", 2, false},
		{"göster turu tavan 4", "kanıtı göster", "Hatalar burada.", classErrorHunt, all, "exception,trace,logs,anomaly", 4, false},
		{"bilgi sınıfı 0", "kanıtı göster rollback nedir", "Rollback önceki sürüme dönmektir, IllegalStateException örnek.", classKnowledge, all, "", 0, false},
		{"sohbet sınıfı 0", "selam", "Merhaba, 2 dakikadır buradayım.", classChat, all, "", 0, false},
		{"yedek kural: iddia var eşleşme yok", "durum", "Son bir saatte 37 hata satırı var.", classStatus, all, "exception", 2, false},
		{"iddia yok eşleşme yok", "durum", "Her şey yolunda görünüyor.", classStatus, all, "", 2, false},
		{"eylem yönlendirmesi kanıtla", "ne yapayım", "payments'ı geri al.", classExplain, all, "exception", 2, false},
		{"eylem var aday yok", "ne yapayım", "payments için rollback öneririm.", classExplain, nil, "", 2, true},
		{"araçsız tur", "selam nasılsın", "İyiyim.", classNone, nil, "", 2, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := decideEvidence(evidenceDecisionIn{Class: c.class, Question: c.q, Answer: c.answer, Cands: c.cands})
			if got := strings.Join(d.Types, ","); got != c.want || d.Cap != c.cap || d.MissingAction != c.missingAction {
				t.Fatalf("tipler %q cap %d missing %v; beklenen %q %d %v", got, d.Cap, d.MissingAction, c.want, c.cap, c.missingAction)
			}
			seen := map[string]bool{}
			for _, ty := range d.Types {
				if seen[ty] {
					t.Fatalf("tür başına 1 bozuldu: %v", d.Types)
				}
				seen[ty] = true
			}
		})
	}
	// Yedek kural tür başına SON adayı seçer.
	d := decideEvidence(evidenceDecisionIn{Class: classErrorHunt, Question: "kanıt göster", Answer: "x", Cands: all})
	if k := d.Picked[0].Keys[0]; k != "IllegalStateException" {
		t.Errorf("son aday değil: %s", k)
	}
}

const evTraceContent = `{"trace_id":"` + evTraceID + `","found":true,"wall_ms":12,"root":{"service":"orders","name":"GET /"},"span_count":3,"error_span_count":0,"page_url":"/trace/` + evTraceID + `"}`

// Kanıt kapalıyken (v1 / off) dikiş metni ve akışı değiştirmez; shadow yalnız sayar.
func TestEvidenceTurnModes(t *testing.T) {
	var nilTurn *evidenceTurn
	if got := nilTurn.collect(mcptools.ExplainTraceName, evTraceContent, false); got != evTraceContent {
		t.Fatal("nil tur metni değiştirdi")
	}
	var emitted []string
	emit := func(k string, _ any) { emitted = append(emitted, k) }
	var seq blocks.Sequencer
	nilTurn.attach(context.Background(), emit, &seq, "x")
	if len(emitted) != 0 || (*v2LoopPlan)(nil).evidenceOf() != nil {
		t.Fatal("nil tur yayın yaptı")
	}
	sh := &evidenceTurn{mode: chstore.CosreEvidenceShadow, question: "kanıt göster"}
	if got := sh.collect(mcptools.ExplainTraceName, evTraceContent, false); got != evTraceContent {
		t.Fatal("shadow metni değiştirdi")
	}
	sh.attach(context.Background(), emit, &seq, "4bf92f35")
	if len(emitted) != 0 || len(sh.cands) != 1 {
		t.Fatalf("shadow yayın yaptı ya da saymadı: %v %d", emitted, len(sh.cands))
	}
	on := &evidenceTurn{mode: chstore.CosreEvidenceOn, question: "kanıt göster"}
	if got := on.collect(mcptools.ExplainTraceName, evTraceContent, true); got != evTraceContent {
		t.Fatal("hata sonucu değişti")
	}
	view := on.collect(mcptools.ExplainTraceName, evTraceContent, false)
	if view == evTraceContent || len(view) > mcptools.EvModelViewMax || !strings.Contains(view, `"evidence":"trace"`) {
		t.Fatalf("model görünümü: %s", view)
	}
	if got := on.collect("get_service_health", `{"a":1}`, false); got != `{"a":1}` {
		t.Fatal("blok kurmayan araç değişti")
	}
	on.attach(context.Background(), emit, &seq, "4bf92f35")
	on.attach(context.Background(), emit, &seq, "4bf92f35") // ikinci çağrı yok sayılır
	if strings.Join(emitted, ",") != "block" || seq.Count() != 1 {
		t.Fatalf("on yayını: %v", emitted)
	}
	// Bayrak yalnız on.
	if mcptools.EvidencePartsOn(withV2LoopPlan(context.Background(), &v2LoopPlan{Evidence: sh})) ||
		!mcptools.EvidencePartsOn(withV2LoopPlan(context.Background(), &v2LoopPlan{Evidence: on})) ||
		mcptools.EvidencePartsOn(withV2LoopPlan(context.Background(), &v2LoopPlan{})) {
		t.Fatal("araç ek alan bayrağı")
	}
}

func TestEvalStreamContractEvidenceBlocks(t *testing.T) {
	blk := func(typ, href string) uistream.Part {
		return uistream.Part{"type": "data-block-" + typ, "data": map[string]any{"type": typ, "payload": map[string]any{"href": href}}}
	}
	if ok, note := evalStreamContract([]uistream.Part{blk("trace", "/trace/x"), blk("exception", "/problems?exc=a")}); !ok {
		t.Fatal(note)
	}
	if ok, _ := evalStreamContract([]uistream.Part{blk("logs", "")}); ok {
		t.Fatal("href'siz blok geçti")
	}
	five := []uistream.Part{blk("trace", "/a"), blk("logs", "/a"), blk("anomaly", "/a"), blk("exception", "/a"), blk("trace", "/a")}
	if ok, _ := evalStreamContract(five); ok {
		t.Fatal("tavan aşımı geçti")
	}
}

// evidence.json: üç S3 vakası kendi kanıt kipini açar; bilgi turunda tavan 0.
func TestEvidenceEvalCases(t *testing.T) {
	cases, err := loadEvalConversationFS(evalset.ConversationFS)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"conv-evidence-show-me": false, "conv-evidence-none-on-knowledge": false, "conv-evidence-action-needs-proof": false}
	for _, c := range cases {
		if _, ok := want[c.ID]; !ok {
			continue
		}
		want[c.ID] = c.file == "evidence.json" && c.Evidence == chstore.CosreEvidenceOn
		for i, tr := range c.Turns {
			e := tr.Expect.Evidence
			if e == nil {
				t.Errorf("%s/t%d: evidence beklentisi yok", c.ID, i+1)
				continue
			}
			if tr.Expect.Class == "knowledge" && (e.Max == nil || *e.Max != 0) {
				t.Errorf("%s/t%d: bilgi turunda max 0 değil", c.ID, i+1)
			}
		}
	}
	for id, ok := range want {
		if !ok {
			t.Errorf("%s: evidence.json'da yok ya da kanıt kipi on değil", id)
		}
	}
}
