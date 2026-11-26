package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/assemble"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/tempo"
)

// evidence_scale_test.go — v0.10.2378: kanıt bütçeleri modelin yürürlükteki
// penceresiyle ölçeklenir. Üç iddia pinlenir:
//
//  1. Ölçek 1'de (yeteneksiz model, küçük açılmış geçit, AI yok) her bütçe
//     yeri bugünkü SABİTLE aynı çıktıyı verir — beklenen değer burada sabitten
//     elle kurulur, ölçekli fonksiyonun kendisinden değil.
//  2. Ölçek 8'de eskiden kesilen uzun fikstür tam taşınır; hâlâ uzunsa kırpma
//     notu gerçek sayılarla basılır.
//  3. Bütün-istem koruması: patolojik fikstür (dev trace + uzun geçmiş + büyük
//     araç sonuçları) pencere payının içinde kalır.
//
// Fikstürler sentetik (servis / host / kurum adı yok); saat ve dilim bağımsız
// (sabit UTC anı, time.Now yok).

const (
	esQwen    = "qwen38-flash-next-fp8"
	esGemma   = "gemma-4-31b-it"
	esTrace   = "5b8aa5a2d2c872e8321cf37308d69df2"
	esExType  = "com.example.orders.OrderRejectedException"
	esLastApp = "com.example.orders.Step%03d.run(Step%03d.java:%d)"
)

var esT0 = time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)

// esScale — testte kanıt bütçesi girdilerini sabitler (dikiş).
func esScale(t *testing.T, scale, window, maxScale int) {
	t.Helper()
	prev := evidenceBudgetSeam
	evidenceBudgetSeam = func(context.Context) copilot.EvidenceBudget {
		return copilot.EvidenceBudget{Scale: scale, Window: window, MaxScale: maxScale}
	}
	t.Cleanup(func() { evidenceBudgetSeam = prev })
}

// esCop — ağsız servis: pencere ad tablosundan.
func esCop(model string) *copilot.Service { return copilot.New(copilot.ProviderOpenAI, "k", model) }

// esText — n rune'luk Türkçe (çok baytlı) metin; her 10 rune'da sıra damgası.
func esText(tag string, n int) string {
	var b strings.Builder
	for i := 0; utf8.RuneCountInString(b.String()) < n; i++ {
		fmt.Fprintf(&b, "%s-%04d çöğüş ", tag, i)
	}
	return string([]rune(b.String())[:n])
}

func esRunes(s string) int { return utf8.RuneCountInString(s) }

// ── ölçek: model + yürürlükteki pencere ───────────────────────────────────

func TestEvidenceScalePerModel(t *testing.T) {
	for _, c := range []struct {
		model string
		want  int
	}{
		{esQwen, 8}, {"Qwen/Qwen3.8-Flash-Next-FP8", 8},
		{esGemma, 1}, {"gemma4", 1}, {"qwen3-30b", 1}, {"qwen3.8-plus", 1}, {"my-model", 1}, {"", 1},
	} {
		s := &Server{copilot: esCop(c.model)}
		if got := s.evidenceScale(context.Background()); got != c.want {
			t.Errorf("%q: ölçek %d, want %d", c.model, got, c.want)
		}
		if got := s.chatEntryEvidenceScale(context.Background(), ""); got != c.want {
			t.Errorf("%q: sohbet giriş ölçeği %d, want %d", c.model, got, c.want)
		}
	}
	var nilSrv *Server
	if nilSrv.evidenceScale(context.Background()) != 1 || (&Server{}).evidenceScale(context.Background()) != 1 {
		t.Error("AI yokken ölçek 1 olmalı")
	}
	// yürürlükteki pencere (geçit küçük açmış) ölçeği indirir — dikiş copilot'un döndürdüğünü taşır
	for _, c := range []struct{ scale, want int }{{8, 8}, {2, 2}, {1, 1}, {0, 1}} {
		esScale(t, c.scale, 0, c.scale)
		if got := (&Server{}).evidenceScale(context.Background()); got != c.want {
			t.Errorf("dikiş ölçek %d → %d, want %d", c.scale, got, c.want)
		}
	}
	if scaleCap(6000, 0) != 6000 || scaleCap(6000, 1) != 6000 || scaleCap(6000, 8) != 48000 {
		t.Error("scaleCap")
	}
}

// Açık profil seçimi geçmiş ölçeğini o profilden çözer.
func TestChatEntryEvidenceScaleFollowsExplicitProfile(t *testing.T) {
	cop := esCop(esGemma)
	cop.SetProfiles([]copilot.ModelProfile{
		{ID: "small", Provider: copilot.ProviderOpenAI, APIKey: "k", Model: esGemma},
		{ID: "wide", Provider: copilot.ProviderOpenAI, APIKey: "k", Model: esQwen},
	}, "small", nil)
	s := &Server{copilot: cop}
	if got := s.chatEntryEvidenceScale(context.Background(), ""); got != 1 {
		t.Fatalf("varsayılan gemma: %d", got)
	}
	if got := s.chatEntryEvidenceScale(context.Background(), " wide "); got != 8 {
		t.Fatalf("açık profil: %d", got)
	}
}

// ── bütçe yerleri: ölçek 1 = bugünkü sabit, ölçek 8 = tam, aşınca not ─────

func TestToolResultBudgetScales(t *testing.T) {
	in := esText("sonuç", 7000)
	want1 := string([]rune(in)[:6000]) + fmt.Sprintf(chatToolResultTruncNote, 6000, 1000)
	if chatToolResultMaxRunes != 6000 {
		t.Fatalf("taban sabit değişti: %d", chatToolResultMaxRunes)
	}
	for name, got := range map[string]string{
		"clampToolResultForModel":   first(clampToolResultForModel(in)),
		"clampToolResultScaled(1)":  first(clampToolResultScaled(in, 1)),
		"clampToolResultScaled(0)":  first(clampToolResultScaled(in, 0)),
		"clampToolResultForLoop v1": first(clampToolResultForLoop(nil, in)),
		"ForLoopScaled v1 ölçek 1":  first(clampToolResultForLoopScaled(nil, in, 1)),
	} {
		if got != want1 {
			t.Errorf("%s: ölçek 1 çıktısı bugünkü sabitle aynı değil", name)
		}
	}
	if got, cut := clampToolResultScaled(in, 8); cut || got != in {
		t.Error("ölçek 8: 7000 rune'luk sonuç tam taşınmalı")
	}
	huge := esText("sonuç", 50000)
	got, cut := clampToolResultForLoopScaled(nil, huge, 8)
	if !cut || got != string([]rune(huge)[:48000])+sprintfTruncNote(48000, 2000) {
		t.Error("ölçek 8: 50000 rune → ilk 48000 + doğru sayılı not")
	}
	// v2: bayt tavanı planın kendisinde ölçekli; operatör ayarı yalnız daraltır.
	if c := v2LoopCeilings(deepMode{}, time.Minute, 1); c.ToolTextBytes != 2048 {
		t.Errorf("v2 tavanı ölçek 1: %d", c.ToolTextBytes)
	}
	c8 := v2LoopCeilings(deepMode{}, time.Minute, 8)
	if c8.ToolTextBytes != 16384 {
		t.Errorf("v2 tavanı ölçek 8: %d", c8.ToolTextBytes)
	}
	if b := narrowLoopBudget(chstore.AILoopBudget{ToolTextBytes: 1024}, c8); b.ToolTextBytes != 1024 {
		t.Errorf("operatör daraltması korunmalı: %d", b.ToolTextBytes)
	}
	ascii := strings.Repeat("a", 10000)
	if out, cut := clampToolResultForLoopScaled(&v2LoopPlan{Budget: narrowLoopBudget(chstore.AILoopBudget{}, c8)}, ascii, 8); cut || out != ascii {
		t.Error("v2 ölçek 8: 10000 bayt tam taşınmalı")
	}
	if out, cut := clampToolResultForLoopScaled(&v2LoopPlan{Budget: narrowLoopBudget(chstore.AILoopBudget{}, v2LoopCeilings(deepMode{}, time.Minute, 1))}, ascii, 1); !cut ||
		out != ascii[:2048]+sprintfTruncNote(2048, 10000-2048) {
		t.Error("v2 ölçek 1: 2048 bayt + not (bugünkü)")
	}
}

func first(s string, _ bool) string { return s }

func TestOpPrefetchSeedScales(t *testing.T) {
	msgs := []copilot.ChatMessage{{Role: "user", Text: "siparişler neden yavaş?"}}
	p := &opPrefetch{argsJSON: `{"service":"orders-api"}`, Drill: esText("kırılım", 9000)}
	base, s1, s8 := p.seed(msgs), p.seedScaled(msgs, 1), p.seedScaled(msgs, 8)
	b1, _ := json.Marshal(base)
	b2, _ := json.Marshal(s1)
	if string(b1) != string(b2) {
		t.Fatal("ölçek 1 tohumu bugünküyle bayt bayt aynı olmalı")
	}
	want1 := string([]rune(p.Drill)[:6000]) + sprintfTruncNote(6000, 3000)
	if got := base[len(base)-1].ToolResults[0].Content; got != want1 {
		t.Error("ölçek 1: kırılım 6000 rune + not")
	}
	if got := s8[len(s8)-1].ToolResults[0].Content; got != p.Drill {
		t.Error("ölçek 8: 9000 rune'luk kırılım tam taşınmalı")
	}
}

func TestHistoryBudgetScales(t *testing.T) {
	turns := make([]string, 12)
	for i := range turns {
		turns[i] = esText(fmt.Sprintf("tur%02d", i), 1500)
	}
	lens := assemble.RuneLens(turns)
	if assemble.HistoryMaxRunes != 6000 {
		t.Fatalf("taban sabit değişti: %d", assemble.HistoryMaxRunes)
	}
	k1, tr1 := assemble.ClampHistory(lens, chatMaxMessages, scaleCap(assemble.HistoryMaxRunes, 1))
	k0, tr0 := assemble.ClampHistory(lens, chatMaxMessages, assemble.HistoryMaxRunes)
	if k1 != 4 || tr1 != 8 || k1 != k0 || tr1 != tr0 || assemble.TrimNoteIfNeeded(tr1) != assemble.HistoryTrimNote {
		t.Fatalf("ölçek 1: %d tur / %d kırpma (bugünkü 4 / 8 + not)", k1, tr1)
	}
	if k8, tr8 := assemble.ClampHistory(lens, chatMaxMessages, scaleCap(assemble.HistoryMaxRunes, 8)); k8 != 12 || tr8 != 0 || assemble.TrimNoteIfNeeded(tr8) != "" {
		t.Fatalf("ölçek 8: 18000 rune'luk geçmiş tam taşınmalı, notsuz: %d / %d", k8, tr8)
	}
	long := make([]int, 40)
	for i := range long {
		long[i] = 1500
	}
	if k, tr := assemble.ClampHistory(long, chatMaxMessages, scaleCap(assemble.HistoryMaxRunes, 8)); k != 32 || tr != 8 || assemble.TrimNoteIfNeeded(tr) == "" {
		t.Fatalf("ölçek 8, 60000 rune: 32 tur + not beklenir: %d / %d", k, tr)
	}
	// bağlanma: sohbet girişi ölçekli bütçeyi kullanır
	src, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, pin := range []string{
		"scaleCap(assemble.HistoryMaxRunes, s.chatEntryEvidenceScale(r.Context(), req.Context.Profile))",
		"opPre.seedScaled(req.Messages, evScale)",
		"clampToolResultForLoopScaled(v2Plan, tr.Content, evScale)",
		"v2LoopCeilings(dm, exchangeMax, s.evidenceScale(ctx))",
		"tf.Scale = evScale",
	} {
		if !strings.Contains(string(src), pin) {
			t.Errorf("copilot_chat.go %q bağlantısını kaybetti", pin)
		}
	}
}

func esGuidedMsgs(turns, runes int) []copilot.ChatMessage {
	var msgs []copilot.ChatMessage
	for i := 0; i < turns; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, copilot.ChatMessage{Role: role, Text: esText(fmt.Sprintf("g%02d", i), runes)})
	}
	return append(msgs, copilot.ChatMessage{Role: "user", Text: "peki dünkü?"})
}

func TestGuidedHistoryBudgetScales(t *testing.T) {
	msgs := esGuidedMsgs(5, 800) // 5 önceki tur × 800 = 4000 rune (taban bütçe 1500)
	if guidedHistoryMaxRunes != 1500 {
		t.Fatalf("taban sabit değişti: %d", guidedHistoryMaxRunes)
	}
	h1 := guidedHistorySectionScaled(msgs, 1)
	// bugünkü kural: en yeniden geriye, 1500'ü aşmadan → yalnız son tur (800) kalır
	if h1 != guidedHistoryHeader+assemble.HistoryTrimNote+"\nK: "+msgs[4].Text {
		t.Errorf("ölçek 1: son tur + kırpma notu beklenir:\n%.200s", h1)
	}
	if h1 != guidedHistorySection(msgs) {
		t.Error("ölçek 1 = bugünkü fonksiyon")
	}
	h8 := guidedHistorySectionScaled(msgs, 8)
	if assemble.HasTrimNote(h8) {
		t.Error("ölçek 8: 4000 rune'luk geçmiş notsuz taşınmalı")
	}
	for i := 0; i < 5; i++ {
		if !strings.Contains(h8, msgs[i].Text) {
			t.Errorf("ölçek 8: tur %d eksik", i)
		}
	}
	// tur tavanı (6) ölçeklenmez; rune tavanı 12000'i aşınca not
	big := guidedHistorySectionScaled(esGuidedMsgs(6, 3000), 8)
	if !assemble.HasTrimNote(big) || esRunes(big) > 12000+esRunes(guidedHistoryHeader)+esRunes(assemble.HistoryTrimNote)+8 {
		t.Errorf("ölçek 8, 18000 rune: not + ≤12000 rune gövde (%d)", esRunes(big))
	}
	// anlatım bloğu: ölçek 1 bayt bayt bugünkü
	if guidedNarrationUserScaled("s?", "veri", esText("açk", 3500), msgs, 1) != guidedNarrationUser("s?", "veri", esText("açk", 3500), msgs) {
		t.Error("guidedNarrationUser ölçek 1")
	}
}

func TestDrawerBudgetsScale(t *testing.T) {
	if drawerExplainMaxRunes != 3000 || drawerEvidenceMaxRunes != 6000 || drawerEvidenceMinHead != 800 {
		t.Fatal("taban sabitler değişti")
	}
	// açıklama
	ex := strings.TrimSpace(esText("açıklama", 3500))
	want1 := strings.TrimSpace(string([]rune(ex)[:3000])) + drawerTruncSuffix
	if clampDrawerExplain(ex) != want1 || clampDrawerExplainScaled(ex, 1) != want1 {
		t.Error("açıklama ölçek 1: 3000 rune + (kısaltıldı)")
	}
	if clampDrawerExplainScaled(ex, 8) != ex {
		t.Error("açıklama ölçek 8: 3500 rune tam")
	}
	if got := clampDrawerExplainScaled(esText("açıklama", 30000), 8); !strings.HasSuffix(got, drawerTruncSuffix) || esRunes(got) > 24000+esRunes(drawerTruncSuffix) {
		t.Error("açıklama ölçek 8: 30000 rune → 24000 + (kısaltıldı)")
	}
	// kanıt: span listesi budanır, loglar korunur
	logs := "\n\nLOGLAR:\n" + esText("log", 1000)
	head := esText("span", 7000)
	full := head + logs
	wantEv1 := cutRunes(head, 6000-esRunes(logs)) + drawerEvidenceTruncNote + logs
	if clampDrawerEvidence(full, logs) != wantEv1 || clampDrawerEvidenceScaled(full, logs, 1) != wantEv1 {
		t.Error("kanıt ölçek 1: baş 6000−log'a budanır + not + loglar")
	}
	if clampDrawerEvidenceScaled(full, logs, 8) != full {
		t.Error("kanıt ölçek 8: 8000 rune'luk paket tam taşınmalı")
	}
	bigHead := esText("span", 60000)
	got := clampDrawerEvidenceScaled(bigHead+logs, logs, 8)
	if !strings.Contains(got, drawerEvidenceTruncNote) || !strings.HasSuffix(got, logs) ||
		esRunes(got) != 48000-esRunes(logs)+esRunes(drawerEvidenceTruncNote)+esRunes(logs) {
		t.Errorf("kanıt ölçek 8: hâlâ uzunsa not + loglar korunur (%d rune)", esRunes(got))
	}
	if got := clampDrawerEvidenceScaled(esText("tek", 50000), "", 8); !strings.HasSuffix(got, drawerEvidenceCutNote) {
		t.Error("kanıt ölçek 8, logsuz: kuyruk notu")
	}
	// anlatım bloğu
	msgs := []copilot.ChatMessage{{Role: "user", Text: "logda ne yazıyor?"}}
	if drawerNarrationUserScaled("logda ne yazıyor?", ex, msgs, "kanıt", 1) != drawerNarrationUser("logda ne yazıyor?", ex, msgs, "kanıt") {
		t.Error("drawerNarrationUser ölçek 1")
	}
	if !strings.Contains(drawerNarrationUserScaled("s", ex, msgs, "kanıt", 8), ex) {
		t.Error("drawerNarrationUser ölçek 8: açıklama tam")
	}
	// trace takibi: önceki açıklama
	tf := &traceFollowUp{TraceID: esTrace}
	p1 := traceFollowUpPromptTR(tf, ex)
	if !strings.Contains(p1, drawerTruncSuffix) || strings.Contains(p1, string([]rune(ex)[3200:])) {
		t.Error("takip ölçek 0/1: açıklama 3000'de kesilir (bugünkü)")
	}
	tf.Scale = 8
	if p8 := traceFollowUpPromptTR(tf, ex); strings.Contains(p8, drawerTruncSuffix) || !strings.Contains(p8, string([]rune(ex)[3200:])) {
		t.Error("takip ölçek 8: açıklama tam")
	}
}

func TestRecordsBudgetScales(t *testing.T) {
	if recordsMaxRunes != 24000 {
		t.Fatalf("taban sabit değişti: %d", recordsMaxRunes)
	}
	data := esText("satır", 30000)
	const cutNote = "KESİLDİ"
	p1 := recordsUserPromptScaled("kaç kayıt var?", "tablo", data, 1)
	if p1 != recordsUserPrompt("kaç kayıt var?", "tablo", data) || !strings.Contains(p1, cutNote) ||
		!strings.Contains(p1, string([]rune(data)[23990:24000])) || strings.Contains(p1, string([]rune(data)[24000:24020])) {
		t.Error("ölçek 1: 24000 rune + kesildi notu (bugünkü)")
	}
	if p8 := recordsUserPromptScaled("kaç kayıt var?", "tablo", data, 8); strings.Contains(p8, cutNote) || !strings.Contains(p8, string([]rune(data)[29000:])) {
		t.Error("ölçek 8: 30000 rune tam, notsuz")
	}
	if p := recordsUserPromptScaled("s", "tablo", esText("satır", 200000), 8); !strings.Contains(p, cutNote) {
		t.Error("ölçek 8: 192000'i aşınca not")
	}
}

// esLongStack — n uygulama frame'li Java stack'i (katlanmaz: her satır anlamlı).
func esLongStack(n int) string {
	var b strings.Builder
	b.WriteString(esExType + ": sipariş reddedildi\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "\tat "+esLastApp+"\n", i, i, 10+i)
	}
	return b.String()
}

func TestTraceEventBudgetsScale(t *testing.T) {
	msg := esText("mesaj", 3000)
	status := esText("durum", 1000)
	sp := evSpan("aaaaaaaaaaaaaaa1", 0, 5, true, evExceptionEvent(esExType, msg, esLongStack(80)))
	sp.StatusMessage = status
	spans := []chstore.SpanRow{sp}

	// ölçek 1 — bugünkü sabitler
	d1 := buildTraceEventDigestLim(spans, traceEventLimitsFor(1))
	if d1.Block() != buildTraceEventDigest(spans).Block() {
		t.Fatal("ölçek 1 bloğu bugünkü kurucuyla aynı olmalı")
	}
	e1 := d1.Spans[0].Events[0]
	if e1.ExMessage != string([]rune(msg)[:500])+"…" {
		t.Error("ölçek 1: exception mesajı 500 rune'da kesilir")
	}
	if d1.Spans[0].StatusMsg != string([]rune(status)[:300])+"…" {
		t.Error("ölçek 1: status mesajı 300 rune'da kesilir")
	}
	if traceStackMaxLines != 12 {
		t.Fatalf("taban sabit değişti: %d", traceStackMaxLines)
	}
	if len(e1.Stack) != 12 {
		t.Errorf("ölçek 1: stack 12 satır (%d)", len(e1.Stack))
	}
	if n := esRunes(marshalTraceEvents(d1.Spans)); n > traceEventsBlockMaxRunes {
		t.Errorf("ölçek 1: blok %d > 4500", n)
	}
	// ölçek 8 — mesaj, status ve 80 frame'lik stack tam
	d8 := buildTraceEventDigestLim(spans, traceEventLimitsFor(8))
	e8 := d8.Spans[0].Events[0]
	if e8.ExMessage != msg || d8.Spans[0].StatusMsg != status {
		t.Error("ölçek 8: exception ve status mesajı tam taşınmalı")
	}
	if len(e8.Stack) != 80 || e8.Stack[79] != "at "+fmt.Sprintf(esLastApp, 79, 79, 89) {
		t.Errorf("ölçek 8: 80 frame'in tamamı (%d)", len(e8.Stack))
	}
	// hâlâ uzunsa tavan tutar: 96 satır, blok ≤ 36000
	huge := evSpan("aaaaaaaaaaaaaaa2", 0, 5, true, evExceptionEvent(esExType, esText("mesaj", 9000), esLongStack(400)))
	dh := buildTraceEventDigestLim([]chstore.SpanRow{huge}, traceEventLimitsFor(8))
	eh := dh.Spans[0].Events[0]
	if len(eh.Stack) != 96 || esRunes(eh.ExMessage) != 4001 || !strings.HasSuffix(eh.ExMessage, "…") {
		t.Errorf("ölçek 8 tavanı: stack %d satır, mesaj %d rune", len(eh.Stack), esRunes(eh.ExMessage))
	}
	if n := esRunes(marshalTraceEvents(dh.Spans)); n > 36000 {
		t.Errorf("ölçek 8: blok %d > 36000", n)
	}
	// span sayısı: 12 exception'lı hata span'i — ölçek 1'de 5 + atlanan notu, 8'de hepsi
	var many []chstore.SpanRow
	for i := 0; i < 12; i++ {
		many = append(many, evSpan(fmt.Sprintf("%016x", 0xb000+i), int64(i), 5, true,
			evExceptionEvent(fmt.Sprintf("%s%02d", esExType, i), "red", "")))
	}
	m1, m8 := buildTraceEventDigestLim(many, traceEventLimitsFor(1)), buildTraceEventDigestLim(many, traceEventLimitsFor(8))
	if len(m1.Spans) != 5 || m1.SkippedSpans != 7 || !strings.Contains(m1.Block(), "(7 span'in event'leri tavan/bütçe nedeniyle yazılmadı.)") {
		t.Errorf("ölçek 1: 5 span + 7 atlandı notu (%d / %d)", len(m1.Spans), m1.SkippedSpans)
	}
	if len(m8.Spans) != 12 || m8.SkippedSpans != 0 || strings.Contains(m8.Block(), "yazılmadı") {
		t.Errorf("ölçek 8: 12 span'in hepsi, notsuz (%d / %d)", len(m8.Spans), m8.SkippedSpans)
	}
	// çerçeve katlaması parametreli; taban 3
	if got := exceptionStackHead(evKafkaStack, evTopicExType, 12, 1500); strings.Join(got, "\n") != strings.Join(exceptionStackHeadFW(evKafkaStack, evTopicExType, 12, 1500, traceStackTopFramework), "\n") {
		t.Error("exceptionStackHead taban = 3 çerçeve satırı")
	}
}

func TestSpanExplainBudgetsScale(t *testing.T) {
	var spans []chstore.SpanRow
	root := chstore.SpanRow{SpanID: "aaaaaaaaaaaa0000", Name: "GET /orders", ServiceName: "orders-api", Kind: "server", StartTime: 0, EndTime: 500_000_000}
	root.Attributes = map[string]string{}
	for i := 0; i < 60; i++ {
		root.Attributes[fmt.Sprintf("attr.%02d", i)] = esText("değer", 700)
	}
	spans = append(spans, root)
	for i := 1; i < 120; i++ {
		spans = append(spans, chstore.SpanRow{SpanID: fmt.Sprintf("aaaaaaaaaaaa%04x", i), ParentSpanID: root.SpanID, Name: fmt.Sprintf("step %03d", i),
			ServiceName: "orders-api", Kind: "internal", StartTime: int64(i) * 1_000_000, EndTime: int64(i)*1_000_000 + 2_000_000})
	}
	u1, u8 := spanExplainUserScaled(esTrace, spans, root, 1), spanExplainUserScaled(esTrace, spans, root, 8)
	if u1 != spanExplainUser(esTrace, spans, root) {
		t.Fatal("ölçek 1 = bugünkü fonksiyon")
	}
	for _, want := range []string{`"listed":40`, `"childrenOmitted":104`, `"attrsOmitted":20`} {
		if !strings.Contains(u1, want) {
			t.Errorf("ölçek 1: %s beklenir (bugünkü tavanlar 40 / 15 / 40)", want)
		}
	}
	if strings.Contains(u1, root.Attributes["attr.00"]) || !strings.Contains(u1, string([]rune(root.Attributes["attr.00"])[:300])+"…") {
		t.Error("ölçek 1: attribute 300 rune'da kesilir")
	}
	for _, want := range []string{`"listed":120`, `"childrenOmitted":`, `"attrsOmitted":`} {
		if has := strings.Contains(u8, want); has != (want == `"listed":120`) {
			t.Errorf("ölçek 8: %s (var=%v)", want, has)
		}
	}
	if !strings.Contains(u8, root.Attributes["attr.59"]) {
		t.Error("ölçek 8: 60 attribute tam değerleriyle")
	}
}

func TestOracleBlockBudgetScales(t *testing.T) {
	names := map[string]string{"src1": "hata-tablosu"}
	rows := func(n int) []chstore.OracleErrorRow {
		out := make([]chstore.OracleErrorRow, n)
		for i := range out {
			out[i] = chstore.OracleErrorRow{SourceID: "src1", Time: esT0.Add(time.Duration(i) * time.Second), RowID: uint64(i),
				OperationCode: "ADDRESS_SAVE", ErrorCode: fmt.Sprintf("ERR-%03d", i), Body: esText("gövde", 900)}
		}
		return out
	}
	b1, n1 := oracleExplainBlockScaled(rows(30), names, 1)
	b0, n0 := oracleExplainBlock(rows(30), names)
	if b1 != b0 || n1 != n0 || n1 != 10 || !strings.Contains(b1, "(Toplam 30 satırın ilk 10 tanesi gösterildi.)") || strings.Contains(b1, "ERR-010") {
		t.Errorf("ölçek 1: 10 satır + not (bugünkü): n=%d", n1)
	}
	if strings.Contains(b1, esText("gövde", 900)) {
		t.Error("ölçek 1: gövde 200 rune'da kesilir")
	}
	b8, n8 := oracleExplainBlockScaled(rows(30), names, 8)
	if n8 != 30 || strings.Contains(b8, "gösterildi") || !strings.Contains(b8, "ERR-029") || !strings.Contains(b8, esText("gövde", 900)) {
		t.Errorf("ölçek 8: 30 satır tam gövdeyle, notsuz: n=%d", n8)
	}
	if b, n := oracleExplainBlockScaled(rows(100), names, 8); n != 80 || !strings.Contains(b, "(Toplam 100 satırın ilk 80 tanesi gösterildi.)") {
		t.Errorf("ölçek 8: 100 satır → 80 + not: n=%d", n)
	}
}

func TestTraceDrillLogBudgetScales(t *testing.T) {
	mk := func(n int) *logstore.Page {
		p := &logstore.Page{}
		for i := 0; i < n; i++ {
			p.Logs = append(p.Logs, &logstore.LogRecord{TraceID: esTrace, Severity: 17, SeverityText: "ERROR", ServiceName: "orders-api",
				Timestamp: esT0.Add(time.Duration(i) * time.Second).UnixNano(), Body: fmt.Sprintf("satır-%03d ", i) + esText("gövde", 900)})
		}
		return p
	}
	ids := []string{esTrace}
	if traceDrillLogsPerTrace != 8 || traceDrillLogsLimit != 60 || traceDrillLogBodyRunes != 200 {
		t.Fatal("taban sabitler değişti")
	}
	o1 := renderTraceDrillLogsScaledTR(false, mk(20), nil, ids, traceDrillLogBodyRunes, 0, 1)
	if o1 != renderTraceDrillLogsBudgetTR(false, mk(20), nil, ids, traceDrillLogBodyRunes, 0) || o1 != renderTraceDrillLogsTR(false, mk(20), nil, ids) {
		t.Fatal("ölçek 1 = bugünkü fonksiyon")
	}
	if n := strings.Count(o1, "KANIT: log "); n != 8 {
		t.Errorf("ölçek 1: trace başına 8 satır (%d)", n)
	}
	o8 := renderTraceDrillLogsScaledTR(false, mk(20), nil, ids, scaleCap(traceDrillLogBodyRunes, 8), 0, 8)
	if n := strings.Count(o8, "KANIT: log "); n != 20 || strings.Contains(o8, "tavanına ulaştı") {
		t.Errorf("ölçek 8: 20 satırın hepsi, notsuz (%d)", n)
	}
	if !strings.Contains(o8, string([]rune(esText("gövde", 900))[:800])) {
		t.Error("ölçek 8: gövde 1600 rune'a kadar tam")
	}
	// tavan notu gerçek sayılarla
	if o := renderTraceDrillLogsScaledTR(false, mk(60), nil, ids, 200, 0, 1); !strings.Contains(o, "log okuması 60 kayıt tavanına ulaştı; trace başına en çok 8 satır gösterildi.") {
		t.Error("ölçek 1 tavan notu")
	}
	if o := renderTraceDrillLogsScaledTR(false, mk(480), nil, ids, 1600, 0, 8); !strings.Contains(o, "log okuması 480 kayıt tavanına ulaştı; trace başına en çok 64 satır gösterildi.") ||
		strings.Count(o, "KANIT: log ") != 64 {
		t.Error("ölçek 8 tavan notu gerçek sayıları basmalı (480 / 64)")
	}
}

// Profil ayrımının anlatım girdisi (araç sonucu özetleri + geçmiş): ölçek 1
// bugünkü tavanlar; ölçek 8'de 4000 baytlık sonuçlar kesilmeden.
func TestSplitNarratorBudgetsScale(t *testing.T) {
	if splitDigestEach != 900 || splitDigestTotal != 6*1024 || splitHistoryMax != 1536 {
		t.Fatal("taban sabitler değişti")
	}
	body := strings.Repeat("ğ", 2000) // 4000 bayt
	var conv []copilot.ChatMessage
	for i := 0; i < 12; i++ {
		conv = append(conv, copilot.ChatMessage{Role: "user", ToolResults: []copilot.ToolResult{{Name: "t", Content: body}}})
	}
	d1, d8 := splitDigestsScaled(conv, 1), splitDigestsScaled(conv, 8)
	if d1 != splitDigests(conv) || strings.Count(d1, "t: ") != 6 || len(d1) > 6*1024 || strings.Contains(d1, body) {
		t.Errorf("ölçek 1: 6 özet × ≤900 bayt, toplam ≤6 KB (%d bayt, %d özet)", len(d1), strings.Count(d1, "t: "))
	}
	if strings.Count(d8, "t: "+body) != 12 || len(d8) > 8*6*1024 {
		t.Errorf("ölçek 8: 12 sonucun hepsi tam (%d bayt, %d tam)", len(d8), strings.Count(d8, "t: "+body))
	}
	msgs := []copilot.ChatMessage{{Role: "user", Text: "a " + body}, {Role: "assistant", Text: "b " + body}, {Role: "user", Text: "soru"}}
	h1, h8 := splitHistoryScaled(msgs, 1), splitHistoryScaled(msgs, 8)
	if h1 != splitHistory(msgs) || len(h1) > 1536 || strings.Contains(h1, "a ") {
		t.Errorf("ölçek 1: geçmiş ≤1536 bayt (%d)", len(h1))
	}
	if !strings.Contains(h8, "Kullanıcı: a "+body) || !strings.Contains(h8, "Asistan: b "+body) {
		t.Error("ölçek 8: iki tur da tam")
	}
}

// ── uçtan uca: trace açıklama girdisi ─────────────────────────────────────

// esTempo — n span'lik trace: kök + çocuklar; 1. çocuk hata + exception event
// (msgRunes mesaj, frames satırlık stack); her 7. çocuk statusRunes'luk status
// mesajlı hata.
func esTempo(t *testing.T, n, msgRunes, frames, statusRunes int) *tempo.Service {
	t.Helper()
	ns := esT0.UnixNano()
	str := func(k, v string) map[string]any {
		return map[string]any{"key": k, "value": map[string]any{"stringValue": v}}
	}
	spans := make([]any, 0, n)
	for i := 0; i < n; i++ {
		sp := map[string]any{"traceId": esTrace, "spanId": fmt.Sprintf("%016x", 0xc000+i), "name": fmt.Sprintf("OrderFlow.step%04d", i), "kind": 1,
			"startTimeUnixNano": fmt.Sprint(ns + int64(i)*1_000_000), "endTimeUnixNano": fmt.Sprint(ns + int64(i)*1_000_000 + int64(n-i)*2_000_000)}
		if i > 0 {
			sp["parentSpanId"] = fmt.Sprintf("%016x", 0xc000)
		}
		switch {
		case i == 1:
			sp["status"] = map[string]any{"code": 2, "message": "sipariş reddedildi"}
			sp["events"] = []any{map[string]any{"timeUnixNano": fmt.Sprint(ns + 1_500_000), "name": "exception",
				"attributes": []any{str("exception.type", esExType), str("exception.message", esText("mesaj", msgRunes)), str("exception.stacktrace", esLongStack(frames))}}}
		case i > 1 && i%7 == 0 && statusRunes > 0:
			sp["status"] = map[string]any{"code": 2, "message": esText(fmt.Sprintf("durum%04d", i), statusRunes)}
		}
		spans = append(spans, sp)
	}
	body, _ := json.Marshal(map[string]any{"batches": []any{map[string]any{
		"resource":   map[string]any{"attributes": []any{str("service.name", "orders-api")}},
		"scopeSpans": []any{map[string]any{"spans": spans}},
	}}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	tp := tempo.New()
	tp.Configure(tempo.Settings{Enabled: true, BaseURL: srv.URL})
	return tp
}

// esLogs — n log: hepsi ERROR; ilki stack'li; gövdeler bodyRunes.
func esLogs(n, bodyRunes, frames int) []*logstore.LogRecord {
	var out []*logstore.LogRecord
	for i := 0; i < n; i++ {
		r := &logstore.LogRecord{Timestamp: esT0.Add(time.Duration(i) * time.Millisecond).UnixNano(), Severity: 17, SeverityText: "ERROR",
			ServiceName: "orders-api", TraceID: esTrace, Body: fmt.Sprintf("log-%04d ", i) + esText("gövde", bodyRunes)}
		if i == 0 {
			r.Attributes = map[string]string{"exception.type": esExType, "exception.stacktrace": esLongStack(frames)}
		}
		out = append(out, r)
	}
	return out
}

func esInput(t *testing.T, s *Server) traceExplainInput {
	t.Helper()
	in, err := s.buildTraceExplainInput(context.Background(), esTrace)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// Yeteneksiz ve bilinmeyen modelde trace açıklama girdisi AI'sız kurulumla
// (ölçek kavramı yokken) bayt bayt aynı; ölçekli modelde eskiden kesilen
// span'ler, stack ve log satırları tam.
func TestTraceExplainInputScalesWithModel(t *testing.T) {
	tp := esTempo(t, 300, 2000, 70, 0)
	logs := &cbLogStore{recs: esLogs(40, 1500, 70)}
	base := esInput(t, &Server{tempo: tp, logs: logs}) // AI yok = bugünkü sabitler
	for _, model := range []string{esGemma, "gemma4", "qwen3-30b", "qwen3.8-plus", "my-model"} {
		if got := esInput(t, &Server{tempo: tp, logs: logs, copilot: esCop(model)}); got.User != base.User || got.LogsBlock != base.LogsBlock || got.ErrorText != base.ErrorText {
			t.Fatalf("%s: girdi bugünküyle bayt bayt aynı olmalı", model)
		}
	}
	// geçit modeli küçük açmış (ölçek 1) → yine aynı
	func() {
		esScale(t, 1, 16384, 1)
		defer func() { evidenceBudgetSeam = nil }()
		if got := esInput(t, &Server{tempo: tp, logs: logs, copilot: esCop(esQwen)}); got.User != base.User {
			t.Fatal("yetenekli model + 16384 pencere: girdi bugünküyle aynı olmalı")
		}
	}()
	evidenceBudgetSeam = nil
	// bugünkü kesimler tabanda görünür
	lastFrame := "at " + fmt.Sprintf(esLastApp, 69, 69, 79)
	if !strings.Contains(base.User, "(trace'in tamamı 300 span; hatalar + en yavaşlar öncelikli 100 span analiz edildi)") ||
		strings.Contains(base.User, lastFrame) || !strings.Contains(base.User, "ERROR orders-api ×40") || strings.Contains(base.User, "log-0001") {
		// v0.10.2456 — aynı imzalı 40 ERROR satırı tek desen satırı (×40), temsilci stack'li ilk kayıt.
		t.Fatal("taban fikstür bugünkü kesimleri göstermeli (100 span, 12 satır stack, 40 log → tek desen)")
	}
	wide := esInput(t, &Server{tempo: tp, logs: logs, copilot: esCop(esQwen)})
	if strings.Contains(wide.User, "trace'in tamamı") || strings.Count(wide.User, `"name":"OrderFlow.step`) < 300 {
		t.Error("ölçek 8: 300 span'in tamamı, kısmi analiz notu yok")
	}
	if !strings.Contains(wide.User, "OrderFlow.step0299") {
		t.Error("ölçek 8: son span listede olmalı")
	}
	if strings.Count(wide.User, lastFrame) < 2 { // span event'i + ilk logun stack'i
		t.Error("ölçek 8: 70 frame'lik stack hem event bloğunda hem logda tam")
	}
	if !strings.Contains(wide.User, esText("mesaj", 2000)) {
		t.Error("ölçek 8: 2000 rune'luk exception mesajı tam")
	}
	if !strings.Contains(wide.User, "log-0000 "+esText("gövde", 1500)) {
		t.Error("ölçek 8: desen temsilcisi tam gövdeyle")
	}
	// hâlâ uzunsa notlar: 1000 span → 800 + not; 200 log → 120
	big := esInput(t, &Server{tempo: esTempo(t, 1000, 100, 5, 0), logs: &cbLogStore{recs: esLogs(200, 50, 5)}, copilot: esCop(esQwen)})
	if !strings.Contains(big.User, "(trace'in tamamı 1000 span; hatalar + en yavaşlar öncelikli 800 span analiz edildi)") {
		t.Error("ölçek 8: 1000 span → 800 + doğru sayılı not")
	}
	if !strings.Contains(big.User, "ERROR orders-api ×200") || strings.Contains(big.User, "GÖSTERİLMEYEN") {
		t.Error("ölçek 8: 200 aynı imzalı satır tek desen, gösterilmeyen yok")
	}
	// log bloğunun toplam bütçesi (192 KB): uzun gövdeli 120 satır sığmaz → en ciddiler + not
	heavy := esInput(t, &Server{tempo: esTempo(t, 20, 100, 5, 0), logs: &cbLogStore{recs: esLogs(200, 4000, 5)}, copilot: esCop(esQwen)})
	if !strings.Contains(heavy.LogsBlock, "ERROR orders-api ×200") {
		t.Error("ölçek 8: uzun gövdeli 200 satır tek desen")
	}
	if len(heavy.LogsBlock) > 8*traceExplainLogsBlockBytes*5/4 {
		t.Errorf("ölçek 8: log bloğu %d bayt, toplam bütçenin (%d) çok üstünde", len(heavy.LogsBlock), 8*traceExplainLogsBlockBytes)
	}
}

// Ölçüm (rapor için; -v ile basılır): tipik ve tavan fikstürlerinde trace
// açıklama girdisi ölçek 1 → 8.
func TestTraceExplainPromptGrowthMeasured(t *testing.T) {
	for _, fx := range []struct {
		name              string
		spans, errs, logs int
		ceil              bool
	}{
		{"küçük gerçekçi (12 span, 3 log)", 12, 2, 3, false},
		{"orta (150 span, 30 log)", 150, 10, 30, false},
		{"tavan (150 span, 100 log, uzun alanlar)", 150, 10, 100, true},
	} {
		tp, logs := cbTempo(t, esT0, fx.spans, fx.errs, fx.ceil), &cbLogStore{recs: cbLogs(esT0, fx.logs, fx.ceil)}
		in1, err := (&Server{tempo: tp, logs: logs}).buildTraceExplainInput(context.Background(), cbTrace)
		if err != nil {
			t.Fatal(err)
		}
		in8, err := (&Server{tempo: tp, logs: logs, copilot: esCop(esQwen)}).buildTraceExplainInput(context.Background(), cbTrace)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: ölçek 1 = %d rune / %d bayt (~%d token); ölçek 8 = %d rune / %d bayt (~%d token)", fx.name,
			esRunes(in1.User), len(in1.User), len(in1.User)/3, esRunes(in8.User), len(in8.User), len(in8.User)/3)
		if len(in8.User) < len(in1.User) {
			t.Errorf("%s: ölçek 8 girdisi küçülmemeli", fx.name)
		}
		// tipik fikstür sınırın çok altında; tavan fikstürü bile korumaya
		// (orta kesim) düşmeden Derin kipin sınırına sığar.
		lim := promptByteLimit(262144, 49152)
		if !fx.ceil && len(in8.User) > lim/8 {
			t.Errorf("%s: tipik girdi (%d bayt) sınırın sekizde birini (%d) aşmamalı", fx.name, len(in8.User), lim/8)
		}
		if len(in8.User) > lim*2/3 {
			t.Errorf("%s: girdi (%d bayt) Derin sınırının üçte ikisini (%d) aşmamalı", fx.name, len(in8.User), lim*2/3)
		}
	}
	// sohbet turu: 20 tur × 1500 rune geçmiş + 3 araç sonucu × 20000 rune
	hist := make([]int, 20)
	for i := range hist {
		hist[i] = 1500
	}
	tool := esText("sonuç", 20000)
	for _, scale := range []int{1, 8} {
		keep, _ := assemble.ClampHistory(hist, chatMaxMessages, scaleCap(assemble.HistoryMaxRunes, scale))
		res, _ := clampToolResultScaled(tool, scale)
		t.Logf("sohbet turu ölçek %d: geçmiş %d rune + 3 araç sonucu × %d rune = %d rune (~%d token, 4 karakter/token)",
			scale, keep*1500, esRunes(res), keep*1500+3*esRunes(res), (keep*1500+3*esRunes(res))/4)
	}
}

// ── bütün-istem koruması ──────────────────────────────────────────────────

func TestClampPromptBytes(t *testing.T) {
	short := esText("kısa", 500)
	if got, cut := clampPromptBytes(short, 4096); cut || got != short {
		t.Error("sığan istem aynen dönmeli")
	}
	in := "BAŞ-İŞARETİ " + esText("orta", 20000) + " SON-İŞARETİ SORU: neden?"
	got, cut := clampPromptBytes(in, 8000)
	if !cut || len(got) > 8000 || !utf8.ValidString(got) {
		t.Fatalf("kesim: cut=%v len=%d geçerli=%v", cut, len(got), utf8.ValidString(got))
	}
	if !strings.HasPrefix(got, "BAŞ-İŞARETİ ") || !strings.HasSuffix(got, " SON-İŞARETİ SORU: neden?") {
		t.Error("baş ve son korunmalı (soru sonda)")
	}
	var removed, head, tail int
	i := strings.Index(got, "\n\n[kırpıldı: istem modelin bağlam penceresine sığmadığı için ortasından ")
	if i < 0 {
		t.Fatal("kesim notu yok")
	}
	if _, err := fmt.Sscanf(got[i:], "\n\n[kırpıldı: istem modelin bağlam penceresine sığmadığı için ortasından %d karakter çıkarıldı (ilk %d ve son %d karakter verildi).", &removed, &head, &tail); err != nil {
		t.Fatal(err)
	}
	j := strings.Index(got, "eksik olduğunu cevapta söyle.]\n\n") + len("eksik olduğunu cevapta söyle.]\n\n")
	if head != esRunes(got[:i]) || tail != esRunes(got[j:]) || removed+head+tail != esRunes(in) {
		t.Errorf("not sayıları yanlış: çıkarılan %d + baş %d + son %d ≠ %d", removed, head, tail, esRunes(in))
	}
	if got, cut := clampPromptBytes(in, 100); cut || got != in {
		t.Error("not için bile yer yoksa istem aynen (bugünkü istek)")
	}
}

// Tek-atış: patolojik trace açıklaması (800 span × uzun status + 120 uzun log)
// gerçek handler'dan geçer; sağlayıcıya giden istek pencere payının içinde.
// Yeteneksiz modelde aynı fikstürün isteği AI'sız kurulan girdiyle aynı.
func TestSingleShotGuardKeepsExplainWithinWindow(t *testing.T) {
	forceTraceExplainStyle(t, chstore.TraceExplainSections) // v0.10.2480 — bölümlü biçimin pini
	tp := esTempo(t, 1000, 4000, 400, 2400)
	logs := &cbLogStore{recs: esLogs(200, 6000, 400)}
	call := func(model string, deep bool) (sys, user string, s *Server) {
		p := newDefProvider(t, "cevap")
		cop := copilot.New(copilot.ProviderOpenAI, "test-key", model)
		cop.Configure(copilot.ProviderOpenAI, "test-key", model, p.srv.URL, false, true)
		// v0.10.2458 — bu test modelin düşünme-varsayılan bütçesini (32768) pinler:
		// yüzeyler inherit (profil thinking boş = modele dokunma); varsayılan off
		// bütçeyi 4096'ya indirir ve fikstür patolojik olmaktan çıkar.
		inherit := map[string]string{}
		for _, i := range copilot.ThinkSurfaces() {
			inherit[string(i.ID)] = copilot.ThinkModeInherit
		}
		cop.SetThinkingModes(inherit)
		s = &Server{copilot: cop, cache: newMemCache(), tempo: tp, logs: logs}
		r := httptest.NewRequest(http.MethodPost, "/api/copilot/explain-trace/"+esTrace, strings.NewReader(""))
		if deep {
			r = r.WithContext(copilot.WithDeepAnswer(r.Context()))
		}
		r.SetPathValue("id", esTrace)
		w := httptest.NewRecorder()
		s.copilotExplainTrace(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: HTTP %d: %s", model, w.Code, w.Body.String())
		}
		sys, user = p.lastMessages(t)
		return sys, user, s
	}
	// yeteneksiz model: istek = bugünkü girdi (koruma devreye girmez)
	base := esInput(t, &Server{tempo: tp, logs: logs})
	if _, user, _ := call(esGemma, false); user != base.User {
		t.Fatal("gemma: sağlayıcıya giden user bloğu bugünkü girdiyle bayt bayt aynı olmalı")
	}
	for _, deep := range []bool{false, true} {
		sys, user, s := call(esQwen, deep)
		maxTok := 32768
		if deep {
			maxTok = 49152
		}
		ctx := context.Background()
		if deep {
			ctx = copilot.WithDeepAnswer(ctx)
		}
		if got := s.roundBudgetMaxTokens(ctx); got != maxTok {
			t.Fatalf("derin=%v: cevap payı %d, want %d", deep, got, maxTok)
		}
		raw := esInput(t, s).User
		lim := promptByteLimit(262144, maxTok)
		if len(raw) <= lim {
			t.Fatalf("fikstür patolojik değil: ham girdi %d ≤ sınır %d", len(raw), lim)
		}
		if len(sys)+len(user) > lim {
			t.Errorf("derin=%v: istek %d bayt > sınır %d (pencere payı)", deep, len(sys)+len(user), lim)
		}
		if est := roundEstimate(len(sys)+len(user), 0, 0, 0); est+maxTok > 262144 {
			t.Errorf("derin=%v: tahmin %d + cevap %d pencereyi aşıyor", deep, est, maxTok)
		}
		if !strings.Contains(user, "[kırpıldı: istem modelin bağlam penceresine sığmadığı için ortasından ") {
			t.Errorf("derin=%v: kesim modele söylenmeli", deep)
		}
		if !strings.HasPrefix(user, raw[:2000]) || !strings.HasSuffix(user, raw[len(raw)-2000:]) {
			t.Errorf("derin=%v: baş (zincir + span listesi başı) ve son (loglar) korunmalı", deep)
		}
	}
}

// fitSingleShot: ölçekli istem OLAMAYAN kurulumda (MaxScale 1) taşan istem bile
// aynen gider (bugünkü davranış); karma kurulumda dar profilin penceresine iner.
func TestFitSingleShotOnlyWhereScalingExists(t *testing.T) {
	user := esText("kanıt", 400000)
	s := &Server{}
	esScale(t, 1, 8192, 1)
	if got := s.fitSingleShot(context.Background(), "sistem", user); got != user {
		t.Fatal("ölçeksiz kurulum: istem aynen")
	}
	esScale(t, 1, 0, 8)
	if got := s.fitSingleShot(context.Background(), "sistem", user); got != user {
		t.Fatal("pencere bilinmiyor: istem aynen")
	}
	esScale(t, 1, 8192, 8) // karma, pencere yalnız AD TAHMİNİ (belirsiz ad → 8192): güvenilmez
	if got := s.fitSingleShot(context.Background(), "sistem", user); got != user {
		t.Fatal("karma kurulum + ad tahmini pencere: bugün çalışan istem kesilmemeli")
	}
	// karma: bu çağrının profili dar ve penceresi geçitten okunmuş, başka profil ölçekli
	prevSeam := evidenceBudgetSeam
	evidenceBudgetSeam = func(context.Context) copilot.EvidenceBudget {
		return copilot.EvidenceBudget{Scale: 1, Window: 32768, WindowProbed: true, MaxScale: 8}
	}
	defer func() { evidenceBudgetSeam = prevSeam }()
	got := s.fitSingleShot(context.Background(), "sistem", user)
	if lim := promptByteLimit(32768, 0); len("sistem")+len(got) > lim || got == user {
		t.Fatalf("karma kurulum: %d bayt > sınır %d", len(got), lim)
	}
	small := esText("kanıt", 1000)
	if got := s.fitSingleShot(context.Background(), "sistem", small); got != small {
		t.Fatal("sınırın altında istem aynen")
	}
	src, err := os.ReadFile("ai_observability.go")
	if err != nil || !strings.Contains(string(src), "user = s.fitSingleShot(ctx, system, user)") {
		t.Error("aiCall bütün-istem korumasını çağırmıyor")
	}
}

func TestReclampToolResultKeepsOriginalCounts(t *testing.T) {
	orig := esText("sonuç", 60000)
	scaled, _ := clampToolResultScaled(orig, 8) // ilk 48000 + not(48000, 12000)
	base, cut := reclampToolResult(scaled, 6000, 0)
	if !cut || base != string([]rune(orig)[:6000])+sprintfTruncNote(6000, 54000) {
		t.Error("yeniden kırpma: ilk 6000 + ÖZGÜN sonuca göre 54000 atlandı")
	}
	want1, _ := clampToolResultForModel(orig)
	if base != want1 {
		t.Error("taban hâli, sonucun baştan ölçek 1'de kırpılmış hâliyle aynı olmalı")
	}
	if got, cut := reclampToolResult(base, 6000, 0); cut || got != base {
		t.Error("zaten tabanda: aynen")
	}
	if got, cut := reclampToolResult("kısa", 6000, 0); cut || got != "kısa" {
		t.Error("kısa sonuç aynen")
	}
	ascii := strings.Repeat("a", 20000)
	v2scaled, _ := clampToolResultBytes(ascii, 16384)
	if got, cut := reclampToolResult(v2scaled, 6000, 2048); !cut || got != ascii[:2048]+sprintfTruncNote(2048, 20000-2048) {
		t.Error("v2 bayt tavanı: ilk 2048 + özgün sayıyla not")
	}
}

// esPathologicalConv — uzun geçmiş (48000 rune) + son turda 6 büyük araç
// sonucu (her biri ölçek 8 tavanında, çok baytlı). Hepsi korunan sınıfta:
// saplama/çift çıkarma yer açamaz.
func esPathologicalConv() []copilot.ChatMessage {
	var conv []copilot.ChatMessage
	for i := 0; i < 31; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		conv = append(conv, copilot.ChatMessage{Role: role, Text: esText(fmt.Sprintf("tur%02d", i), 1500)})
	}
	conv = append(conv, copilot.ChatMessage{Role: "user", Text: "hepsini birlikte yorumla"})
	var calls []copilot.ToolCall
	var results []copilot.ToolResult
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("c%d", i)
		name := fmt.Sprintf("custom_tool_%d", i) // her biri kendi ailesi → hepsi korunur
		content, _ := clampToolResultScaled(strings.Repeat("ğüşiöç", 10000), 8)
		calls = append(calls, copilot.ToolCall{ID: id, Name: name, Input: json.RawMessage(`{"service":"orders-api"}`)})
		results = append(results, copilot.ToolResult{CallID: id, Name: name, Content: content})
	}
	return append(conv, copilot.ChatMessage{Role: "assistant", ToolCalls: calls}, copilot.ChatMessage{Role: "user", ToolResults: results})
}

// Serbest döngü: patolojik konuşma pencere payına sığmıyorsa taban bütçeye
// iner — notlar gerçek sayılarla, çiftler bozulmadan.
func TestRoundBudgetFallsBackToBaseBudgets(t *testing.T) {
	s := &Server{copilot: esCop(esQwen)} // ağsız: pencere ad tablosundan (262144), ölçek 8
	conv := esPathologicalConv()
	sys := esText("sistem", 20000)
	var steps []string
	emit := func(kind string, v any) {
		if m, ok := v.(map[string]string); ok && kind == "step" {
			steps = append(steps, m["label"])
		}
	}
	for _, deep := range []bool{false, true} {
		steps = nil
		ctx := context.Background()
		if deep {
			ctx = copilot.WithDeepAnswer(ctx)
		}
		maxTok := s.roundBudgetMaxTokens(ctx)
		lim := roundBudgetLimit(262144, maxTok)
		if est := roundEstimate(len(sys)+chatConvBytes(conv), 0, 0, 0); est <= lim {
			t.Fatalf("fikstür patolojik değil: tahmin %d ≤ sınır %d", est, lim)
		}
		b := &roundBudget{}
		out := s.fitRoundBudget(withRoundBudget(ctx, b), emit, sys, conv, nil)
		est := roundEstimate(len(sys)+chatConvBytes(out), 0, 0, 0)
		if est > lim || est+maxTok > 262144 {
			t.Fatalf("derin=%v: tahmin %d > sınır %d (pencere payı)", deep, est, lim)
		}
		if b.sentBytes != len(sys)+chatConvBytes(out) {
			t.Errorf("derin=%v: gönderilen bayt kaydı %d ≠ %d", deep, b.sentBytes, len(sys)+chatConvBytes(out))
		}
		pairsIntact(t, out)
		last := out[len(out)-1].ToolResults
		if len(last) != 6 {
			t.Fatalf("son turun 6 sonucu durmalı: %d", len(last))
		}
		for _, tr := range last {
			if !strings.HasSuffix(tr.Content, sprintfTruncNote(6000, 54000)) || esRunes(tr.Content) != 6000+esRunes(sprintfTruncNote(6000, 54000)) {
				t.Fatalf("sonuç taban tavana inmeli, not özgün sayılarla: …%s", tr.Content[len(tr.Content)-120:])
			}
		}
		if out[0].Role != "user" || out[0].Text != assemble.HistoryTrimNote {
			t.Error("geçmiş kırpması modele söylenmeli (HistoryTrimNote başta)")
		}
		notes, histRunes := 0, 0
		for _, m := range out {
			if assemble.HasTrimNote(m.Text) {
				notes++
			} else if len(m.ToolCalls) == 0 && len(m.ToolResults) == 0 {
				histRunes += esRunes(m.Text)
			}
		}
		if notes != 1 || histRunes > assemble.HistoryMaxRunes {
			t.Errorf("geçmiş: %d not, %d rune (taban 6000)", notes, histRunes)
		}
		if q := out[len(out)-3]; q.Text != "hepsini birlikte yorumla" {
			t.Errorf("aktif soru korunmalı: %q", q.Text)
		}
		if len(steps) != 1 || steps[0] != roundBudgetBaseChip {
			t.Errorf("adım satırı: %v", steps)
		}
		// girdi konuşması değişmedi (doğrulayıcı tam çıktıyı okur)
		if c := conv[len(conv)-1].ToolResults[0].Content; esRunes(c) < 48000 {
			t.Fatal("girdi konuşması değişti")
		}
	}
}

// Ölçekli ama sığan konuşma AYNEN gider; yeteneksiz modelde (ad tahmini
// pencere) aynı patolojik konuşmaya bugünkü gibi dokunulmaz.
func TestRoundBudgetScaledUnderLimitAndUnscaledUntouched(t *testing.T) {
	sameSlice := func(a, b []copilot.ChatMessage) bool { return len(a) == len(b) && len(a) > 0 && &a[0] == &b[0] }
	noEmit := func(kind string, _ any) { t.Errorf("iz beklenmiyordu: %s", kind) }
	// ölçek 8, sığıyor: geçmiş 18000 rune + 2 sonuç × 20000 rune
	var conv []copilot.ChatMessage
	for i := 0; i < 12; i++ {
		conv = append(conv, copilot.ChatMessage{Role: "user", Text: esText("tur", 1500)})
	}
	conv = append(conv, budgetPair("c0", "get_service_health", `{"service":"orders-api"}`, esText("sonuç", 20000))...)
	conv = append(conv, budgetPair("c1", "compare_periods", `{"service":"orders-api"}`, esText("sonuç", 20000))...)
	wide := &Server{copilot: esCop(esQwen)}
	if out := wide.fitRoundBudget(withRoundBudget(context.Background(), &roundBudget{}), noEmit, "sistem", conv, nil); !sameSlice(out, conv) {
		t.Fatal("ölçek 8, sınırın altında: konuşma dilimi aynen dönmeli")
	}
	// yeteneksiz model: pencere ad tahmini → proaktif adım yok, konuşma aynen
	for _, model := range []string{esGemma, "qwen3-30b", "my-model"} {
		s := &Server{copilot: esCop(model)}
		path := esPathologicalConv()
		if out := s.fitRoundBudget(withRoundBudget(context.Background(), &roundBudget{}), noEmit, "sistem", path, nil); !sameSlice(out, path) {
			t.Fatalf("%s: konuşmaya dokunulmamalı (bugünkü davranış)", model)
		}
	}
}

// Karma kurulum: çağrının profili dar (ölçek 1), konuşma geniş profilin
// ölçeğiyle kurulmuş → çağrıdan önce bu profilin bütçesine iner. Zaten tabanda
// olan konuşma aynen.
func TestRoundBudgetNarrowProfileGetsOwnScale(t *testing.T) {
	esScale(t, 1, 0, 8)
	prev := roundBudgetWindowSeam
	roundBudgetWindowSeam = func(context.Context) copilot.ContextWindowInfo {
		return copilot.ContextWindowInfo{Tokens: 8192, Source: "name"}
	}
	t.Cleanup(func() { roundBudgetWindowSeam = prev })
	s := &Server{}
	conv := esPathologicalConv()
	out := s.fitRoundBudget(withRoundBudget(context.Background(), &roundBudget{}), func(string, any) {}, "sistem", conv, nil)
	for _, tr := range out[len(out)-1].ToolResults {
		if esRunes(tr.Content) != 6000+esRunes(sprintfTruncNote(6000, 54000)) {
			t.Fatal("dar profil: sonuç taban tavana inmeli")
		}
	}
	if out[0].Text != assemble.HistoryTrimNote {
		t.Fatal("dar profil: geçmiş taban bütçeye inmeli, notla")
	}
	pairsIntact(t, out)
	again := s.fitRoundBudget(withRoundBudget(context.Background(), &roundBudget{}), func(string, any) {}, "sistem", out, nil)
	if len(again) != len(out) || &again[0] != &out[0] {
		t.Fatal("zaten tabandaki konuşma aynen dönmeli (idempotent)")
	}
	// evidenceConvAt: bütçenin içindeki konuşmaya dokunmaz
	small := budgetConv()
	if got, changed := evidenceConvAt(small, 0, 1); changed || &got[0] != &small[0] {
		t.Fatal("taban bütçenin içindeki konuşma aynen")
	}
}

// Model adı modelcaps DIŞINDA aranmaz: bütçe yerleri yalnız erişimciyi sorar.
func TestEvidenceScaleNoModelNamesOutsideModelcaps(t *testing.T) {
	for _, f := range []string{"evidence_scale.go", "chat_tool_budget.go", "explain_trace_events.go", "explain_trace_input.go",
		"explain_span.go", "copilot_drawer.go", "chat_trace_drill.go", "chat_source_discovery.go", "chat_round_budget.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := strings.ToLower(stripGoComments(string(b)))
		for _, bad := range []string{"qwen", "flash", "gemma", "modelcaps.for("} {
			if strings.Contains(src, bad) {
				t.Errorf("%s: model adı / yetenek sorgusu (%q) bütçe yerinde olmamalı — s.evidenceScale(ctx) kullan", f, bad)
			}
		}
	}
	for f, pins := range map[string][]string{
		"explain_trace_input.go":   {"evScale := s.evidenceScale(ctx)", "scaleCap(traceExplainSpanCap, evScale)", "traceEventLimitsFor(evScale)"},
		"explain_trace_logs.go":    {"scaleCap(traceExplainLogLines, evScale)", "scaleCap(traceExplainLogsBlockBytes, evScale)"},
		"explain_trace_events.go":  {"evScale := s.evidenceScale(ctx)"},
		"explain_span.go":          {"spanExplainUserScaled(traceID, spans, *target, s.evidenceScale(ctx))"},
		"copilot_drawer.go":        {`s.evidenceScaleSurface(ctx, "chat-drawer")`, "clampDrawerEvidenceScaled(in.User, in.LogsBlock, evScale)"},
		"copilot_guided.go":        {`s.evidenceScaleSurface(ctx, "chat-guided")`},
		"chat_source_discovery.go": {"s.evidenceScaleSurface(ctx, aisurface.ChatRecords)"},
		"chat_trace_drill.go":      {"evScale := s.evidenceScale(ctx)", "renderTraceDrillLogsScaledTR("},
		"chat_round_budget.go":     {"evidenceConvAt(conv, loopToolBytesAt(v2LoopPlanFrom(ctx), 1), 1)", "s.evidenceBudgetOf(ctx).Scale <= 1"},
	} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, pin := range pins {
			if !strings.Contains(string(b), pin) {
				t.Errorf("%s %q bağlantısını kaybetti", f, pin)
			}
		}
	}
}

// Soğuk önbellek: ölçekli modelde ilk bütçe sorusu pencereyi GEÇİTTEN okur
// (geçit modeli küçük açmışsa ölçek ad tablosunun 8'i değil, gerçek pencerenin
// çarpanı olur); ikinci soru geçide gitmez. Ölçeği 1 olan modelde geçide HİÇ
// gidilmez (fazladan istek yok).
func TestEvidenceScaleReadsGatewayWindowOnColdCache(t *testing.T) {
	var models atomic.Int64
	gw := func(model string, win int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/models") {
				models.Add(1)
				fmt.Fprintf(w, `{"data":[{"id":%q,"max_model_len":%d}]}`, model, win)
				return
			}
			http.NotFound(w, r)
		}))
	}
	wide := gw("qwen3.8-flash-next-fp8", 65536)
	defer wide.Close()
	cop := copilot.New(copilot.ProviderOpenAI, "k", "qwen3.8-flash-next-fp8")
	cop.Configure(copilot.ProviderOpenAI, "k", "qwen3.8-flash-next-fp8", wide.URL, false, true)
	s := &Server{copilot: cop}
	if got := s.evidenceScale(context.Background()); got != 2 {
		t.Fatalf("soğuk önbellekte ölçek geçidin penceresinden (65536 → 2) gelmeli, %d", got)
	}
	if n := models.Load(); n != 1 {
		t.Fatalf("geçit bir kez okunmalı, %d", n)
	}
	if got := s.evidenceScale(context.Background()); got != 2 || models.Load() != 1 {
		t.Fatalf("ikinci soru önbellekten: ölçek %d, geçit okuması %d", got, models.Load())
	}

	models.Store(0)
	small := gw("gemma4", 131072)
	defer small.Close()
	cop2 := copilot.New(copilot.ProviderOpenAI, "k", "gemma4")
	cop2.Configure(copilot.ProviderOpenAI, "k", "gemma4", small.URL, false, true)
	if got := (&Server{copilot: cop2}).evidenceScale(context.Background()); got != 1 || models.Load() != 0 {
		t.Fatalf("yeteneksiz model: ölçek 1 ve geçide istek yok; ölçek %d, okuma %d", got, models.Load())
	}
}
