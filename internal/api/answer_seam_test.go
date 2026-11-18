package api

// answer_seam_test.go — v0.10.2338 (conversation-audit §6.1 D): cevap dikişi.
// Pinler: süzgeç tablosu (silinen / korunan cümle), cümle kapısı (ilk cümle
// tutulur, kalıpsa hiç görünmez, tavan 160 rune, reset), gerçek v2 handler'ında
// otoriter cevap ve taslak, üslup kapalıyken akış bayt bayt aynı, sunucu
// satırlarının çipe taşınması (karar 9).

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestSeamFilterTable(t *testing.T) {
	b := seamBannedLists([]string{"Sorunuzu inceledim"})
	g := &answerSeam{ctx: context.Background(), t: &answerStyleTurn{}}
	cases := []struct {
		name, in, want string
		n              int
	}{
		{"selam açılışı silinir", "Merhaba! checkout son saatte sağlıklı görünüyor. Hata yok.", "checkout son saatte sağlıklı görünüyor. Hata yok.", 1},
		{"Tabii ki silinir", "Tabii ki. Hata yok, gecikme normal.", "Hata yok, gecikme normal.", 1},
		{"kapanış silinir", "Hata yok, gecikme normal. Umarım yardımcı olmuştur.", "Hata yok, gecikme normal.", 1},
		{"ikisi birden", "Selam! Hata yok. Gecikme normal. Başka bir sorunuz olursa sormaktan çekinmeyin!", "Hata yok. Gecikme normal.", 2},
		{"aşağıda + kapanış", "Aşağıda detayları paylaşıyorum:\n- hata yok\n- gecikme normal\nKolay gelsin.", "- hata yok\n- gecikme normal", 2},
		{"operatör eki (bannedPhrases)", "Sorunuzu inceledim. Hata yok.", "Hata yok.", 1},
		{"sayı taşıyan açılış korunur", "Tabii, hata oranı %2,4. Gecikme normal.", "Tabii, hata oranı %2,4. Gecikme normal.", 0},
		{"saat taşıyan kapanış korunur", "Hata yok. Umarım 14:30'daki sorun tekrarlamaz, iyi çalışmalar.", "Hata yok. Umarım 14:30'daki sorun tekrarlamaz, iyi çalışmalar.", 0},
		{"kesme işaretli ad korunur", "Anladım, payments'a bakıyorum. Hata yok.", "Anladım, payments'a bakıyorum. Hata yok.", 0},
		{"ters tırnaklı ad korunur", "Tabii, `orders-api` için durum şu. Hata yok.", "Tabii, `orders-api` için durum şu. Hata yok.", 0},
		{"tek cümle korunur", "Merhaba!", "Merhaba!", 0},
		{"iki cümlenin ikisi kalıp → yalnız ilki", "Merhaba! Umarım yardımcı olmuştur.", "Umarım yardımcı olmuştur.", 1},
		{"kalıpsız metin aynen", "**Bulgu** — orders-api p95 yükseldi.", "**Bulgu** — orders-api p95 yükseldi.", 0},
		{"çit içindeki son satır dokunulmaz", "Hata yok.\n```\nkolay gelsin", "Hata yok.\n```\nkolay gelsin", 0},
	}
	for _, c := range cases {
		got, n := seamFilter(c.in, b, g.carriesFinding)
		if got != c.want || n != c.n {
			t.Errorf("%s:\n got (%q, %d)\nwant (%q, %d)", c.name, got, n, c.want, c.n)
		}
	}
}

func TestSeamFindingFromState(t *testing.T) {
	st := &chatContextState{ctx: ChatContext{Service: "inventory"}}
	g := &answerSeam{ctx: ctxWithChatContext(context.Background(), st), t: &answerStyleTurn{}}
	if !g.carriesFinding("Tabii, inventory tarafında sorun yok.") {
		t.Fatal("durumdaki servis adı bulgu sayılmadı")
	}
	if g.carriesFinding("Tabii ki yardımcı olayım.") {
		t.Fatal("bulgusuz cümle bulgu sayıldı")
	}
}

func TestSeamSentenceEnd(t *testing.T) {
	for _, c := range []struct {
		in    string
		final bool
		want  int
	}{
		{"Merhaba! devam", false, len("Merhaba!")},
		{"p95 2.", false, -1},
		{"p95 2.", true, len("p95 2.")},
		{"p95 2.5 ms", false, -1},
		{"yükseldi.", false, len("yükseldi.")},
		{"\n\nBaşlık\nmetin", false, len("\n\nBaşlık")},
		{"  ", true, -1},
	} {
		if got := seamSentenceEnd(c.in, c.final); got != c.want {
			t.Errorf("%q final=%v → %d, want %d", c.in, c.final, got, c.want)
		}
	}
}

// seamRig — doğrudan dikiş (anlatım akıyor, üslup açık); delta metinleri toplanır.
func seamRig(narrating bool) (*answerSeam, *[]string, *[]emitted) {
	var deltas []string
	var all []emitted
	g := &answerSeam{ctx: context.Background(), t: &answerStyleTurn{on: true, narrating: narrating}, style: true,
		banned: seamBannedLists(nil)}
	g.emit = func(k string, p any) {
		all = append(all, emitted{k, p})
		if k == "delta" {
			txt, _, _ := seamDeltaFields(p)
			deltas = append(deltas, txt)
		}
	}
	return g, &deltas, &all
}

func TestSeamDraftGate(t *testing.T) {
	g, deltas, _ := seamRig(true)
	g.emitFn("delta", map[string]string{"text": "Mer"})
	g.emitFn("delta", map[string]string{"text": "haba!"})
	if len(*deltas) != 0 {
		t.Fatalf("ilk cümle bitmeden taslak çıktı: %q", *deltas)
	}
	g.emitFn("delta", map[string]string{"text": " checkout"})
	g.emitFn("delta", map[string]string{"text": " sağlıklı."})
	if got := strings.Join(*deltas, ""); got != "checkout sağlıklı." {
		t.Fatalf("taslak: %q", got)
	}
	// reset → kapı yeniden kurulur.
	g.emitFn("delta", map[string]any{"text": "", "reset": true})
	g.emitFn("delta", map[string]string{"text": "Hata oranı %2. Umarım"})
	if got := (*deltas)[len(*deltas)-1]; got != "Hata oranı %2. Umarım" {
		t.Fatalf("temiz ilk cümle ve sonrası geldiği gibi: %q", got)
	}
}

func TestSeamDraftGateBound(t *testing.T) {
	g, deltas, _ := seamRig(true)
	long := "Tabii " + strings.Repeat("ve ", 60)
	g.emitFn("delta", map[string]string{"text": long[:40]})
	if len(*deltas) != 0 {
		t.Fatal("tavan dolmadan bırakıldı")
	}
	g.emitFn("delta", map[string]string{"text": long[40:]})
	if strings.Join(*deltas, "") != long {
		t.Fatalf("160 rune tavanında taslak bırakılmalı: %q", *deltas)
	}
	// Anlatım dışı akış (ör. wiki kademesi) kapıdan geçmez: yük aynen.
	g2, _, all := seamRig(false)
	p := map[string]string{"text": "Merhaba!"}
	g2.emitFn("delta", p)
	if len(*all) != 1 || (*all)[0].v.(map[string]string)["text"] != "Merhaba!" {
		t.Fatalf("anlatım dışı delta değişti: %+v", *all)
	}
	// Cevapsız biten akış: tutulan taslak bırakılır.
	g3, d3, _ := seamRig(true)
	g3.emitFn("delta", map[string]string{"text": "checkout sağlıklı"})
	g3.emitFn("done", map[string]bool{"ok": true})
	if strings.Join(*d3, "") != "checkout sağlıklı" {
		t.Fatalf("done'da tutulan taslak: %q", *d3)
	}
}

// v2TextDeltas — akıştaki bütün text-delta parçaları (sıra korunur).
func v2TextDeltas(t *testing.T, sse string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(sse, "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var p map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &p) != nil {
			continue
		}
		if p["type"] == "text-delta" {
			s, _ := p["delta"].(string)
			out = append(out, s)
		}
	}
	return out
}

func seamLoopRun(t *testing.T, answer string) string {
	t.Helper()
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		if n > 0 {
			return answerMsg(answer)
		}
		return toolCallMsg([2]string{"get_service_health", `{"service":"orders-api"}`})
	})
	s, _ := newLoopTestServer(t, llm, "u-golden")
	setCosreV2(t, s, true)
	ft := &fakeTools{}
	ft.install(t)
	return serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", chatGoldenLoopBody(), true)).Body.String()
}

func TestSeamLoopHandlerStyleOn(t *testing.T) {
	setConversationStyle(t, true)
	body := seamLoopRun(t, "Merhaba! orders-api p95 yükseldi. Umarım yardımcı olmuştur.")
	if got := v2AnswerText(body); got != "orders-api p95 yükseldi." {
		t.Fatalf("otoriter cevap: %q", got)
	}
	for _, d := range v2TextDeltas(t, body) {
		if strings.Contains(d, "Merhaba") {
			t.Fatalf("kalıp açılış bir text-delta'da göründü: %q", d)
		}
	}
	setConversationStyle(t, false)
	off := seamLoopRun(t, "Merhaba! orders-api p95 yükseldi. Umarım yardımcı olmuştur.")
	if got := v2AnswerText(off); got != "Merhaba! orders-api p95 yükseldi. Umarım yardımcı olmuştur." {
		t.Fatalf("üslup kapalıyken cevap değişti: %q", got)
	}
}

// Üslup kapalıyken v2 akışı bayt bayt altın dosya (sarıcı hiç kurulmaz).
func TestSeamStyleOffGolden(t *testing.T) {
	setConversationStyle(t, false)
	s, _ := newLoopTestServer(t, goldenLoopLLM(t), "u-golden")
	setCosreV2(t, s, true)
	ft := &fakeTools{}
	ft.install(t)
	w := serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", chatGoldenLoopBody(), true))
	compareChatGolden(t, "v2_loop.sse", normalizeChatStream(w.Body.String()))
	// v1 yolu üslup açıkken de aynen.
	setConversationStyle(t, true)
	s1, _ := newLoopTestServer(t, goldenLoopLLM(t), "u-golden")
	ft.install(t)
	w1 := httptest.NewRecorder()
	s1.copilotChat(w1, chatGoldenRequest("/api/copilot/chat", chatGoldenLoopBody(), true))
	compareChatGolden(t, "v1_loop.sse", normalizeChatStream(w1.Body.String()))
}

// Karar 9 — sunucu satırları v2 + üslup açıkken çipe.
func TestSeamServerLinesToChips(t *testing.T) {
	v2 := withCosreV2Loop(context.Background())
	for _, on := range []bool{false, true} {
		setConversationStyle(t, on)
		ev, emit := collectEmit()
		got := guidedSourceNote(v2, emit, "servis RED özeti + baseline")
		if !on {
			if got != "\n\nKaynak: servis RED özeti + baseline" || len(*ev) != 0 {
				t.Fatalf("kapalı: %q %+v", got, *ev)
			}
			continue
		}
		if got != "" || len(*ev) != 1 || (*ev)[0].kind != "step" || (*ev)[0].v.(map[string]string)["label"] != "Kaynak: servis RED özeti + baseline" {
			t.Fatalf("açık: %q %+v", got, *ev)
		}
		if multi := guidedSourceNote(v2, emit, "a\nb"); multi != "\n\nKaynak: a\nb" {
			t.Fatalf("çok satırlı künye metinde kalmalı: %q", multi)
		}
	}
	// v1 (işaretsiz ctx) üslup açıkken de eski künye.
	setConversationStyle(t, true)
	ev, emit := collectEmit()
	if got := guidedSourceNote(context.Background(), emit, "x"); got != "\n\nKaynak: x" || len(*ev) != 0 {
		t.Fatalf("v1: %q", got)
	}
}

func TestSeamGeneralNoteChip(t *testing.T) {
	setGeneralQuestions(t, false)
	for _, on := range []bool{false, true} {
		setConversationStyle(t, on)
		l := newOffTopicLLM(t, "none")
		s, _ := offTopicTestServer(t, l)
		ctx := withCosreV2Loop(copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "xroot-seam", UserID: "u1"}))
		ev, handled := runIntentTurn(t, s, ctx, offTopicCapitalQ)
		if !handled {
			t.Fatal("genel cevap kademesi cevaplamalı")
		}
		text, _ := answerOf(t, ev)["text"].(string)
		chip, noteDelta := false, false
		for _, e := range ev {
			if m, ok := e.v.(map[string]string); ok {
				chip = chip || (e.kind == "step" && m["label"] == generalNoteChipTR)
				noteDelta = noteDelta || (e.kind == "delta" && strings.Contains(m["text"], intentGeneralNoteTR))
			}
		}
		if on != chip || on == noteDelta || on == strings.HasPrefix(text, intentGeneralNoteTR) || strings.TrimSpace(text) == "" {
			t.Fatalf("üslup=%v: çip=%v not-delta=%v metin=%q", on, chip, noteDelta, text)
		}
	}
}
