package api

// v0.10.1280 — CoSRE adım 8: kalıcılık + geri bildirim → ai_calls.
//
// (1) KADEME KAPSAMI — her CoSRE turu, hangi kademe cevaplarsa cevaplasın,
//     cosre_interactions'a TEK satır yazar (recordChatExchange, copilotChat'in
//     tek defer'i). Gerçek handler + sahte model; satır test dikişinden
//     (cosreInteractionSeam) yakalanır. Davranışsal tablo ulaşılabilir
//     kademeleri koşar; yapısal test exchange kimliği basıldıktan sonra
//     hiçbir erken dönüşün defer'den ÖNCE kalmadığını pinler (rag / doc-rows /
//     operation dahil — fixture'sız kademeler aynı defer'den geçer).
// (2) answer.callId — oylanabilir cevap, onu ÜRETEN ai_calls satırının
//     kimliğini taşır (döngüde cevaptan sonra yazılan toplu satır; anlatım
//     kademelerinde kökle kaydedilen son çağrı). LLM'siz cevap callId taşımaz.

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/wiki"
)

// captureCosreRows — dikişi kurar; turların satırlarını toplar.
func captureCosreRows(t *testing.T) func() []chstore.CosreInteraction {
	t.Helper()
	var mu sync.Mutex
	var rows []chstore.CosreInteraction
	prev := cosreInteractionSeam
	cosreInteractionSeam = func(r chstore.CosreInteraction) {
		mu.Lock()
		rows = append(rows, r)
		mu.Unlock()
	}
	t.Cleanup(func() { cosreInteractionSeam = prev })
	return func() []chstore.CosreInteraction {
		mu.Lock()
		defer mu.Unlock()
		return append([]chstore.CosreInteraction(nil), rows...)
	}
}

func TestEveryTierRecordsCosreInteraction(t *testing.T) {
	type tierCase struct {
		name      string
		tier      string
		setup     func(t *testing.T) *loopLLM
		body      func() map[string]any
		wantModel bool // tur model çağırdı mı (token/model alanları dolu)
		wantCall  bool // cevap oylanabilir ve callId taşımalı
	}
	cases := []tierCase{
		{
			name: "kapsam (/help) — LLM'siz", tier: aisurface.TierScope,
			setup: noLLM,
			body: func() map[string]any {
				return map[string]any{"messages": []map[string]any{{"role": "user", "text": "/help"}},
					"context": map[string]any{"command": "help", "conversation": "conv-scope"}}
			},
		},
		{
			// v0.10.1289 — hafıza kademesi: açık metin → onay kartı, model yok.
			name: "hafıza (hatırla:) — LLM'siz", tier: aisurface.TierMemory,
			setup: noLLM,
			body: func() map[string]any {
				return map[string]any{"messages": []map[string]any{{"role": "user", "text": "hatırla: checkout'ta 02:00 batch'i 5xx üretir"}}}
			},
		},
		{
			// v0.10.1289 — "bunu hatırla": son soru + cevaptan tek kural önerisi (chat-rule).
			name: "hafıza (bunu hatırla) — LLM'li", tier: aisurface.TierMemory,
			setup: func(t *testing.T) *loopLLM {
				return &loopLLM{srv: newMatrixProvider(t).srv}
			},
			body: func() map[string]any {
				return map[string]any{"messages": []map[string]any{
					{"role": "user", "text": "checkout neden gece 02:00'de 5xx veriyor?"},
					{"role": "assistant", "text": "02:00–02:20 arası checkout-batch işi çalışıyor; bu pencerede POST /pay %3 hata veriyor."},
					{"role": "user", "text": "bunu hatırla"},
				}}
			},
			wantModel: true, wantCall: true,
		},
		{
			name: "guided (self_meta) — LLM'siz", tier: aisurface.TierGuided,
			setup: noLLM,
			body: func() map[string]any {
				return map[string]any{"messages": []map[string]any{{"role": "user", "text": "sen hangi modelsin"}}}
			},
		},
		{
			name: "wiki (bulunamadı) — LLM'siz", tier: aisurface.TierWiki,
			setup: func(t *testing.T) *loopLLM {
				withWikiService(t, wiki.Config{Enabled: true}, &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": runbookText}})
				setGeneralQuestions(t, false) // v0.10.1549 — açıkken genel bilgi cevabı (LLM'li)
				return noLLM(t)
			},
			body: func() map[string]any {
				return map[string]any{"messages": []map[string]any{{"role": "user", "text": "kafka sertifika yenileme runbook"}}}
			},
		},
		{
			name: "niyet (genel cevap) — LLM'li", tier: aisurface.TierIntent,
			setup: func(t *testing.T) *loopLLM {
				return &loopLLM{srv: newMatrixProvider(t).srv}
			},
			body: func() map[string]any {
				return map[string]any{"messages": []map[string]any{{"role": "user", "text": "türkiyenin başkenti neresi"}}}
			},
			wantModel: true, wantCall: true,
		},
		{
			name: "çekmece (açıklama bağlamı) — LLM'li", tier: aisurface.TierDrawer,
			setup: func(t *testing.T) *loopLLM {
				return &loopLLM{srv: newMatrixProvider(t).srv}
			},
			body: func() map[string]any {
				return map[string]any{"messages": []map[string]any{{"role": "user", "text": "bunu biraz daha açar mısın"}},
					"context": map[string]any{"explain": "KONU: anomali\n**Bulgu** — ödeme adımında gecikme arttı."}}
			},
			wantModel: true, wantCall: true,
		},
		{
			name: "serbest döngü (trace takibi) — LLM'li", tier: aisurface.TierLoop,
			setup: func(t *testing.T) *loopLLM {
				return newLoopLLM(t, func(int, map[string]any) map[string]any {
					return answerMsg("**Bulgu** — checkout ödeme adımı yavaş.")
				})
			},
			body: func() map[string]any {
				return traceDrawerBody("Loglarda bu hatanın karşılığı var mı?", anchoredToMs(), "conv-loop")
			},
			wantModel: true, wantCall: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := captureCosreRows(t)
			llm := c.setup(t)
			s, _ := newLoopTestServer(t, llm, "u-t")
			rec := withExchangeRecorder(s)
			fr := postLoopChat(t, s, context.Background(), "u-t", c.body())
			x := collectExchange(t, rec)

			got := rows()
			if len(got) != 1 {
				t.Fatalf("tur başına TEK cosre_interactions satırı bekleniyordu, %d: %+v", len(got), got)
			}
			r := got[0]
			if r.Tier != c.tier || r.Surface != x.turn.Surface || !strings.HasPrefix(r.Surface, aisurface.TurnPrefix+":"+c.tier) {
				t.Errorf("kademe/yüzey: tier=%q surface=%q (ai_calls etkileşim %q)", r.Tier, r.Surface, x.turn.Surface)
			}
			if r.ExchangeID != x.root() || !reExchangeRoot.MatchString(r.ExchangeID) {
				t.Errorf("exchange_id kök olmalı: %q (kök %q)", r.ExchangeID, x.root())
			}
			if r.Status != "ok" || r.TotalMs == 0 && r.CreatedAt.IsZero() || r.UserEmail == "" || r.Question == "" {
				t.Errorf("sonuç/süre/kullanıcı/soru: %+v", r)
			}
			if c.wantModel != (r.ModelCalls > 0 && r.Model != "") {
				t.Errorf("model alanları: calls=%d model=%q (beklenen LLM=%v)", r.ModelCalls, r.Model, c.wantModel)
			}
			if c.wantModel && r.InputTokens+r.OutputTokens == 0 {
				t.Errorf("token sayıları boş: %+v", r)
			}
			ans := framesOf(fr, "answer")
			if len(ans) == 0 {
				t.Fatal("cevap olayı yok")
			}
			last := ans[len(ans)-1]
			callID, hasCall := last["callId"].(string)
			if c.wantCall != hasCall {
				t.Fatalf("callId varlığı %v, beklenen %v: %v", hasCall, c.wantCall, last)
			}
			if !c.wantCall {
				return
			}
			if xid, _ := last["exchangeId"].(string); xid != x.root() {
				t.Errorf("callId'li cevabın exchangeId'si kök değil: %q", xid)
			}
			// callId turun GERÇEK bir ai_calls satırı, kökle (çocuk rol değil).
			var match *copilot.CallRecord
			for i := range x.calls {
				if x.calls[i].ID == callID {
					match = &x.calls[i]
				}
			}
			if match == nil {
				t.Fatalf("callId %q turun ai_calls satırlarında yok: %+v", callID, x.calls)
			}
			if match.ExchangeID != x.root() {
				t.Errorf("callId yardımcı çağrıyı gösteriyor (exchange %q)", match.ExchangeID)
			}
		})
	}
}

// Yapısal: exchange kimliği basıldıktan sonraki HER return, kayıt defer'i
// kurulduktan sonra gelir; her kademe adı defer'den sonra yazılır. Yeni bir
// kademe defer'in önüne erken dönüş koyarsa o kademenin turları
// cosre_interactions'a hiç düşmezdi.
func TestCopilotChatRecordsBeforeAnyTierReturn(t *testing.T) {
	src, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "copilot_chat.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	for _, d := range af.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == "copilotChatWith" { // v0.10.1590 — gövde chatSink dikişinin arkasında
			fn = f
		}
	}
	if fn == nil {
		t.Fatal("copilotChatWith bulunamadı")
	}
	var minted, deferAt token.Pos
	var returns, tiers []token.Pos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			if id, ok := v.Lhs[0].(*ast.Ident); ok && id.Name == "exchangeID" && minted == token.NoPos {
				minted = v.Pos()
			}
		case *ast.DeferStmt:
			if strings.Contains(string(src[fset.Position(v.Pos()).Offset:fset.Position(v.End()).Offset]), "recordChatExchange") {
				deferAt = v.Pos()
			}
		case *ast.ReturnStmt:
			returns = append(returns, v.Pos())
		case *ast.CallExpr:
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "tier" {
				tiers = append(tiers, v.Pos())
			}
		case *ast.FuncLit:
			return false // closure'ların return'leri handler'ın değil
		}
		return true
	})
	if minted == token.NoPos || deferAt == token.NoPos {
		t.Fatalf("exchangeID ataması (%v) ya da recordChatExchange defer'i (%v) bulunamadı", minted, deferAt)
	}
	for _, r := range returns {
		if r > minted && r < deferAt {
			t.Errorf("%s: exchange basıldıktan sonra kayıt defer'inden ÖNCE erken dönüş — tur kaydedilmez", fset.Position(r))
		}
	}
	if len(tiers) < 8 {
		t.Fatalf("yalnız %d kademe çağrısı bulundu — tarama bozuldu mu?", len(tiers))
	}
	for _, tp := range tiers {
		if tp < deferAt {
			t.Errorf("%s: kademe kayıt defer'inden önce", fset.Position(tp))
		}
	}
}

func TestAnswerWithCallID(t *testing.T) {
	const root, call = "r1", "c1"
	cases := []struct {
		name string
		in   any
		want string // beklenen callId ("" = eklenmez)
	}{
		{"oylanabilir map[string]any", map[string]any{"text": "x", "exchangeId": root}, call},
		{"oylanabilir map[string]string", map[string]string{"text": "x", "exchangeId": root}, call},
		{"exchangeId yok (LLM'siz)", map[string]any{"text": "x"}, ""},
		{"başka exchange", map[string]any{"text": "x", "exchangeId": "r2"}, ""},
		{"zaten var — ezilmez", map[string]any{"text": "x", "exchangeId": root, "callId": "c0"}, "c0"},
		{"tanınmayan tip", "düz metin", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := answerWithCallID(c.in, root, call)
			got := eventField(out, "callId")
			if got != c.want {
				t.Errorf("callId %q, beklenen %q", got, c.want)
			}
			// Kopya: çağıranın haritası değişmez.
			if m, ok := c.in.(map[string]any); ok && c.want == call {
				if _, has := m["callId"]; has {
					t.Error("girdi haritası mutasyona uğradı")
				}
			}
		})
	}
	if out := answerWithCallID(map[string]any{"exchangeId": root}, root, ""); eventField(out, "callId") != "" {
		t.Error("kimlik bilinmiyorken callId eklenmemeli (eski davranış)")
	}
}

func TestNoteModelCallPicksRootCall(t *testing.T) {
	x := newChatExchange("root1", "q", false, time.Now())
	x.noteModelCall(copilot.CallNote{CallID: "a", ExchangeID: "root1", Model: "m"})
	x.noteModelCall(copilot.CallNote{CallID: "b", ExchangeID: "root1:chat-intent", Model: "m"}) // yardımcı: aday değil
	if x.finalCallID != "a" {
		t.Errorf("yardımcı çağrı cevabın çağrısı sayıldı: %q", x.finalCallID)
	}
	x.noteFinalCall("loop")
	if x.finalCallID != "loop" || x.modelCalls != 2 {
		t.Errorf("döngü kimliği: %q calls=%d", x.finalCallID, x.modelCalls)
	}
}

func TestStepEvidenceSummary(t *testing.T) {
	n := func(v int) *int { return &v }
	cases := []struct {
		name         string
		single, list any
		want         string
	}{
		{"kanıtsız", nil, nil, ""},
		{"tek kalem satır", mcptools.ChatEvidence{Tool: "query_logs", Source: "logs", Rows: n(42), Query: strings.Repeat("q", 500)}, nil, "logs:42"},
		{"seri", mcptools.ChatEvidence{Source: "metrics", Series: n(3)}, nil, "metrics:s3"},
		{"sayısız, kaynaksız", mcptools.ChatEvidence{Tool: "query_traces"}, nil, "query_traces:?"},
		{"bileşik liste önce", mcptools.ChatEvidence{Source: "traces", Rows: n(1)},
			[]mcptools.ChatEvidence{{Source: "traces", Hop: "trace", Rows: n(1)}, {Source: "metrics", Hop: "jvm", Series: n(4)}},
			"traces#trace:1,metrics#jvm:s4"},
	}
	for _, c := range cases {
		if got := stepEvidenceSummary(c.single, c.list); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
	var long []mcptools.ChatEvidence
	for i := 0; i < 100; i++ {
		long = append(long, mcptools.ChatEvidence{Source: "logs", Rows: n(i)})
	}
	if got := stepEvidenceSummary(nil, long); len(got) > cosreToolEvidenceMax {
		t.Errorf("özet tavanı aşıldı: %d", len(got))
	}
}
