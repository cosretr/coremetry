package api

// chat_clarify_entity_flow_test.go — v0.10.1576 (operator-reported, prod
// ekran görüntüsü): "Takımımın servisleri nasıl?" → CoSRE takım sordu →
// kullanıcı listedeki takım adını yazdı → cevap YÜKLENEN TABLOLARDAN geldi
// ("Yüklenen tablolarda … 200 satır bulundu"), takımın servisleri değil.
// Operatör: "Bu durumda ragda aramasın, yüklü dokümanlarda arayayım mı diye
// sormasını beklerim." Çıplak bilinen varlık adı (takım / servis) + tabloda
// eşleşme → doküman kademeleri kendiliğinden cevaplamaz, düz metin soru sorar.

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	entTeam    = "Payments-Core"
	entTeamAsk = "Takımımın servisleri nasıl?"
	entTeamQ   = "Hesabına takım atanmamış. Hangi takımdasın? Uygulama takımları: Payments-Core, Ledger"
)

// newEntityDocRig — clarifyRig + takım kataloğu + takım adını taşıyan tablo.
func newEntityDocRig(t *testing.T) *clarifyRig {
	t.Helper()
	rg := newClarifyRig(t, nil)
	rg.mc.mu.Lock()
	rg.mc.m["copilot:guided:teamcat:v2"] = []byte(`[{"team":"Payments-Core","services":3,"owner":3},{"team":"Ledger","services":2,"owner":2}]`)
	rg.mc.m["copilot:guided:svcnames"] = []byte(`["checkout","payments","order-api"]`)
	rg.mc.mu.Unlock()
	table := "ekip_envanteri · satır 2 — Takım: Payments-Core · Sunucu: srvpay01 · Ortam: PROD\n" +
		"ekip_envanteri · satır 3 — Takım: Payments-Core · Sunucu: srvpay02 · Ortam: UAT\n" +
		"ekip_envanteri · satır 4 — Takım: Ledger · Sunucu: srvled01 · Ortam: PROD\n" +
		"ekip_envanteri · satır 5 — Uygulama: order-api · Sunucu: srvord01 · Ortam: PROD\n"
	prev := docRowChunksSeam
	docRowChunksSeam = func(_ context.Context, needle string) ([]chstore.LocalDocChunk, error) {
		if !strings.Contains(strings.ToLower(table), strings.ToLower(needle)) {
			return nil, nil
		}
		return []chstore.LocalDocChunk{{Title: "ekip_envanteri", Text: table}}, nil
	}
	t.Cleanup(func() { docRowChunksSeam = prev })
	return rg
}

// TestBareTeamNameAsksDocsOrTeam — ekran görüntüsünün kendisi: takım sorusunun
// cevabı olarak yazılan çıplak takım adı tablo cevabına KAÇMAZ, soru sorulur.
func TestBareTeamNameAsksDocsOrTeam(t *testing.T) {
	rg := newEntityDocRig(t)
	fr := rg.post(t, entTeamAsk, entTeamQ, entTeam)
	r := lastRow(t, rg.rows())
	got := lastAnswerText(fr)
	if r.Tier != aisurface.TierClarify || !strings.Contains(got, "yüklü dokümanlarda mı arayayım") {
		t.Fatalf("çıplak takım adı soru sormalı: tier=%q surface=%q cevap=%q", r.Tier, r.Surface, got)
	}
	if strings.Contains(got, "srvpay01") {
		t.Fatalf("tablo cevabı basılmamalı: %q", got)
	}
}

// TestBareTeamNameAskShape — soru LLM'siz, çipsiz, exchangeId'siz; bekleyen
// soru doc_or_entity olarak özgün adı taşır.
func TestBareTeamNameAskShape(t *testing.T) {
	rg := newEntityDocRig(t)
	fr := rg.post(t, entTeamAsk, entTeamQ, entTeam)
	r := lastRow(t, rg.rows())
	if r.Surface != "cosre-turn:clarify/doc_or_entity_ask" {
		t.Fatalf("surface=%q", r.Surface)
	}
	if got := lastAnswerText(fr); got != docOrEntityAskTR(bareEntityTeam, entTeam) {
		t.Fatalf("soru metni: %q", got)
	}
	a := framesOf(fr, "answer")[0]
	if a["suggestions"] != nil || a["exchangeId"] != nil {
		t.Fatalf("çip / exchangeId yok: %v", a)
	}
	if n := len(rg.llm.calls()); n != 0 || r.ModelCalls != 0 {
		t.Fatalf("soru LLM'siz olmalı: %d çağrı", n)
	}
	if p := rg.pending(t); p == nil || p.Kind != pendingKindDocOrEntity || p.Question != entTeam || p.Entity != bareEntityTeam {
		t.Fatalf("bekleyen soru: %+v", p)
	}
}

// TestBareTeamNameReplyDocs — "dokümanlarda" → ÖZGÜN ad tablo satırlarında.
func TestBareTeamNameReplyDocs(t *testing.T) {
	rg := newEntityDocRig(t)
	rg.post(t, entTeamAsk, entTeamQ, entTeam)
	ask := docOrEntityAskTR(bareEntityTeam, entTeam)
	fr := rg.post(t, entTeamAsk, entTeamQ, entTeam, ask, "yüklü dokümanlarda ara")
	r := lastRow(t, rg.rows())
	if r.Tier != aisurface.TierDocRows || !strings.Contains(lastAnswerText(fr), "srvpay01") {
		t.Fatalf("doküman seçildi: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
	if m := replyTool(r); m["kind"] != pendingKindDocOrEntity || m["reply"] != entityReplyDocs {
		t.Fatalf("clarify_reply kaydı: %v", m)
	}
	if p := rg.pending(t); p != nil {
		t.Fatalf("bekleyen soru silinmeli: %+v", p)
	}
}

// TestBareTeamNameReplyEntity — "takımın servisleri" → guided team_services
// ÖZGÜN adla; doküman kademeleri atlanır, soru yeniden sorulmaz.
func TestBareTeamNameReplyEntity(t *testing.T) {
	for _, reply := range []string{"takımın servislerini getir", "takım", "Coremetry'de"} {
		t.Run(reply, func(t *testing.T) {
			rg := newEntityDocRig(t)
			rg.post(t, entTeamAsk, entTeamQ, entTeam)
			ask := docOrEntityAskTR(bareEntityTeam, entTeam)
			fr := rg.post(t, entTeamAsk, entTeamQ, entTeam, ask, reply)
			r := lastRow(t, rg.rows())
			if r.Tier != aisurface.TierGuided || len(*rg.routes) == 0 || (*rg.routes)[len(*rg.routes)-1] != string(guidedTeamServices) {
				t.Fatalf("takım seçildi: tier=%q rotalar=%v cevap=%q", r.Tier, *rg.routes, lastAnswerText(fr))
			}
			if strings.Contains(lastAnswerText(fr), "srvpay01") {
				t.Fatalf("tablo cevabı gelmemeli: %q", lastAnswerText(fr))
			}
			if m := replyTool(r); m["reply"] != entityReplyEntity {
				t.Fatalf("clarify_reply kaydı: %v", m)
			}
			if p := rg.pending(t); p != nil {
				t.Fatalf("yeniden sorulmamalı: %+v", p)
			}
		})
	}
}

// TestBareTeamNameReplyUnrelatedDropped — ilgisiz cevap: soru düşer (ekranda
// adım yok, v0.10.1575), tur yeni soru gibi.
func TestBareTeamNameReplyUnrelatedDropped(t *testing.T) {
	rg := newEntityDocRig(t)
	rg.post(t, entTeamAsk, entTeamQ, entTeam)
	ask := docOrEntityAskTR(bareEntityTeam, entTeam)
	fr := rg.post(t, entTeamAsk, entTeamQ, entTeam, ask, "checkout sağlığı nasıl?")
	for _, f := range fr {
		if f.event == "step" && f.data["tool"] == clarifyReplyTool {
			t.Fatalf("düşen soru adım çizmemeli: %v", f.data)
		}
	}
	r := lastRow(t, rg.rows())
	if r.Tier != aisurface.TierGuided || len(*rg.routes) == 0 || (*rg.routes)[0] != string(guidedServiceHealth) {
		t.Fatalf("yeni soru guided'a: tier=%q rotalar=%v", r.Tier, *rg.routes)
	}
	if m := replyTool(r); m["reply"] != clarifyReplyDropped {
		t.Fatalf("dropped: %v", m)
	}
}

// TestBareServiceNameAsks — servis varyantı.
func TestBareServiceNameAsks(t *testing.T) {
	rg := newEntityDocRig(t)
	fr := rg.post(t, "order-api")
	if r := lastRow(t, rg.rows()); r.Tier != aisurface.TierClarify || lastAnswerText(fr) != docOrEntityAskTR(bareEntityService, "order-api") {
		t.Fatalf("servis sorusu: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
}

// TestDocQuestionNotBareEntityUnchanged — çıplak varlık OLMAYAN doküman sorusu
// tabloyu doğrudan alır (bugünkü davranış); tabloda eşleşmesi olmayan çıplak
// takım adı soru sormadan guided'a gider.
func TestDocQuestionNotBareEntityUnchanged(t *testing.T) {
	rg := newEntityDocRig(t)
	fr := rg.post(t, "Payments-Core sunucuları hangi ortamda")
	if r := lastRow(t, rg.rows()); r.Tier != aisurface.TierDocRows || !strings.Contains(lastAnswerText(fr), "srvpay02") {
		t.Fatalf("doküman sorusu doğrudan tablo: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
	if p := rg.pending(t); p != nil {
		t.Fatalf("soru sorulmamalı: %+v", p)
	}

	rg2 := newEntityDocRig(t)
	docRowChunksSeam = func(context.Context, string) ([]chstore.LocalDocChunk, error) { return nil, nil }
	rg2.post(t, entTeamAsk, entTeamQ, entTeam)
	if r := lastRow(t, rg2.rows()); r.Tier != aisurface.TierGuided || len(*rg2.routes) == 0 || (*rg2.routes)[0] != string(guidedTeamServices) {
		t.Fatalf("tabloda yoksa doğrudan takım: tier=%q rotalar=%v", r.Tier, *rg2.routes)
	}
}

// TestBareEntityOf — SAF: çıplak varlık tanıma.
func TestBareEntityOf(t *testing.T) {
	teams := []string{"Payments-Core", "Ledger"}
	svcs := []string{"checkout", "order-api"}
	for _, c := range []struct{ in, kind, name string }{
		{"Payments-Core", bareEntityTeam, "Payments-Core"},
		{"payments-core?", bareEntityTeam, "Payments-Core"},
		{" Ledger ", bareEntityTeam, "Ledger"},
		{"order-api", bareEntityService, "order-api"},
		{"order-api.", bareEntityService, "order-api"},
		{"Payments-Core sunucuları", "", ""},
		{"order-api hataları", "", ""},
		{"Payments-Core sunucuları hangi ortamda", "", ""},
		{"CRMHouse", "", ""},
		{"", "", ""},
	} {
		k, n := bareEntityOf(c.in, teams, svcs)
		if k != c.kind || n != c.name {
			t.Errorf("bareEntityOf(%q) = %q %q, want %q %q", c.in, k, n, c.kind, c.name)
		}
	}
}

// TestClassifyDocOrEntityReply — SAF: cevap sınıfları; "evet" tek başına sınıf değil.
func TestClassifyDocOrEntityReply(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"dokümanlarda", entityReplyDocs},
		{"yüklü dokümanlarda ara", entityReplyDocs},
		{"tabloda bak", entityReplyDocs},
		{"excel", entityReplyDocs},
		{"takımın servislerini getir", entityReplyEntity},
		{"servisin durumunu", entityReplyEntity},
		{"Coremetry'de", entityReplyEntity},
		{"evet", ""},
		{"tamam", ""},
		{"takım dokümanı", ""},
		{"checkout sağlığı nasıl?", ""},
	} {
		if got := classifyDocOrEntityReply(c.in); got != c.want {
			t.Errorf("classifyDocOrEntityReply(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
