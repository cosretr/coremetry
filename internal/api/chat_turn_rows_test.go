package api

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// chat_turn_rows_test.go — v0.10.2472: son turun satırları, sıra sözü
// çözücüsü, önsöz bloğu ve gerçek v2 handler'ında iki turluk seçim.

var (
	tidA = strings.Repeat("a", 32)
	tidB = strings.Repeat("b", 32)
	tidC = strings.Repeat("c", 32)
)

func threeRows() []chatRow {
	return []chatRow{
		{Kind: rowKindEndpoint, Name: "GET /api/cart", ID: endpointRowID("checkout", "GET /api/cart"), Service: "checkout", RangeS: 3600},
		{Kind: rowKindEndpoint, Name: "POST /api/pay", ID: endpointRowID("checkout", "POST /api/pay"), Service: "checkout", RangeS: 3600},
		{Kind: rowKindEndpoint, Name: "GET /health", ID: endpointRowID("checkout", "GET /health"), Service: "checkout", RangeS: 3600},
	}
}

func TestResolveRowOrdinalTable(t *testing.T) {
	rows := threeRows()
	one := rows[:1]
	cases := []struct {
		q      string
		rows   []chatRow
		picked int
		idx    int
		rest   string
		ok     bool
	}{
		// TR sıra sözleri
		{"ilkine bak", rows, 0, 0, "", true},
		{"ilkini aç", rows, 0, 0, "", true},
		{"birincisi", rows, 0, 0, "", true},
		{"ikincisini aç", rows, 0, 1, "", true},
		{"ikinci'yi incele", rows, 0, 1, "", true},
		{"üçüncüsüne bak", rows, 0, 2, "", true},
		{"sonuncusu", rows, 0, 2, "", true},
		{"son satırı aç", rows, 0, 2, "", true},
		{"en sondakini göster", rows, 0, 2, "", true},
		{"2. satırı aç", rows, 0, 1, "", true},
		{"#3", rows, 0, 2, "", true},
		{"ikincisinin loglarına bak", rows, 0, 1, "loglarına bak", true},
		// EN
		{"open the first one", rows, 0, 0, "", true},
		{"second", rows, 0, 1, "", true},
		{"the last one please", rows, 0, 2, "", true},
		{"3rd", rows, 0, 2, "", true},
		// zamir: tek satır ya da az önce seçilen satır, çıplak seçimde
		{"onu aç", one, 0, 0, "", true},
		{"bunu incele", rows, 2, 1, "", true},
		{"open that one", one, 0, 0, "", true},
		// çözülmez → bugünkü yol
		{"onu aç", rows, 0, 0, "", false},                      // üç aday, seçim yok
		{"bunu pod bazında göster", one, 0, 0, "", false},      // kapsam devamı, seçim değil
		{"dördüncüsü", rows, 0, 0, "", false},                  // aralık dışı
		{"son 2 saatte ne oldu", rows, 0, 0, "", false},        // pencere
		{"son 1 saat", rows, 0, 0, "", false},                  // pencere
		{"last 24 hours", rows, 0, 0, "", false},               // pencere
		{"ilk 5 servisi göster", rows, 0, 0, "", false},        // sayı
		{"ikinci olarak deploy var mı", rows, 0, 0, "", false}, // söylem
		{"birinci ile ikinciyi karşılaştır", rows, 0, 0, "", false},
		{"hata oranı %2. sırada mı", rows, 0, 0, "", false},
		{"deploy mu vardı", rows, 0, 0, "", false},
		{"ikincisini aç", nil, 0, 0, "", false}, // liste yok
		{strings.Repeat("çok uzun bir soru ", 8) + "ikincisi", rows, 0, 0, "", false},
	}
	for _, c := range cases {
		got, ok := resolveRowOrdinal(c.q, c.rows, c.picked)
		if ok != c.ok || (ok && (got.Index != c.idx || got.Rest != c.rest)) {
			t.Errorf("%q → %+v ok=%v, want idx=%d rest=%q ok=%v", c.q, got, ok, c.idx, c.rest, c.ok)
		}
	}
}

func TestRowPickRequest(t *testing.T) {
	ep := threeRows()[1]
	if rq, ok := rowPickRequest(ep, ""); !ok || rq.Text != "checkout POST /api/pay" || rq.Service != "checkout" || rq.Route != "POST /api/pay" || rq.RangeS != 3600 {
		t.Fatalf("endpoint = kartın İncele turu: %+v", rq)
	}
	if rq, _ := rowPickRequest(ep, "loglarına bak"); rq.Text != "checkout POST /api/pay loglarına bak" {
		t.Fatalf("kalan içerik korunur: %q", rq.Text)
	}
	tr := chatRow{Kind: rowKindTrace, Name: "checkout GET /api/cart", ID: tidB, Service: "checkout"}
	if rq, ok := rowPickRequest(tr, ""); !ok || rq.Trace != tidB || !strings.HasPrefix(rq.Text, tidB) || rq.Service != "" {
		t.Fatalf("trace = trace açma: %+v", rq)
	}
	sv := serviceRows([]string{"payments"})[0]
	if rq, ok := rowPickRequest(sv, ""); !ok || rq.Service != "payments" || rq.Text != "payments nasıl?" {
		t.Fatalf("servis = servis kapsamı: %+v", rq)
	}
	// Kimlik varlık modelinden okunur: ad değil, bozuk kimlik → çözülmez.
	if _, ok := rowPickRequest(chatRow{Kind: rowKindService, Name: "payments", ID: "payments"}, ""); ok {
		t.Fatal("svc: öneksiz kimlik çözülmemeli")
	}
	if _, ok := rowPickRequest(chatRow{Kind: rowKindEndpoint, Name: "GET /x", ID: "checkout|GET /x", Service: "checkout"}, ""); ok {
		t.Fatal("endpoint kimliği servis varlığı taşımalı")
	}
}

func TestRowsFromEvents(t *testing.T) {
	pick := endpointPickPayload{Kind: "endpoint", Service: "checkout", RangeS: 900, Total: 2,
		Items: []endpointPickItem{{Route: "GET /a"}, {Route: "GET /b"}}}
	if rows := rowsFromEvent(chatPickEventKind, pick); len(rows) != 2 || rows[1].ID != "svc:checkout|GET /b" || rows[1].RangeS != 900 {
		t.Fatalf("pick → %+v", rows)
	}
	tl := traceListPayload{Traces: []traceListRow{{TraceID: tidA, ServiceName: "checkout", RootName: "GET /a"}, {TraceID: "kısa"}}}
	if rows := rowsFromEvent("trace_list", tl); len(rows) != 1 || rows[0].ID != tidA || rows[0].Kind != rowKindTrace {
		t.Fatalf("trace_list → %+v", rows)
	}
	ev := mcptools.EvidenceBlock{Type: blocks.TypeTraceList, Payload: mcptools.EvTraceListPayload{Traces: []mcptools.EvTraceRow{{TraceID: tidC}}}}
	if rows := rowsFromEvent("ev_block", ev); len(rows) != 1 || rows[0].ID != tidC {
		t.Fatalf("ev_block → %+v", rows)
	}
	var seq blocks.Sequencer
	if rows := rowsFromEvent("block", seq.Next(blocks.TypeTraceList, tl)); len(rows) != 1 {
		t.Fatalf("block → %+v", rows)
	}
	if rows := rowsFromEvent("block", seq.Next(blocks.TypeChart, map[string]any{})); rows != nil {
		t.Fatal("grafik satır değildir")
	}
	many := make([]string, 30)
	for i := range many {
		many[i] = "svc-" + string(rune('a'+i%26)) + strings.Repeat("x", i)
	}
	if rows := serviceRows(many); len(rows) != chatRowsMax || rows[0].ID != "svc:"+many[0] {
		t.Fatalf("servis satırları tavanlı + entity kimlikli: %d", len(rows))
	}
	// İlk liste kazanır; adım özetleri tavanlı.
	stt := &chatStateTurn{}
	stt.noteEvent("trace_list", tl)
	stt.noteEvent(chatPickEventKind, pick)
	if len(stt.rows) != 1 || stt.rows[0].Kind != rowKindTrace {
		t.Fatalf("ilk liste kazanmalı: %+v", stt.rows)
	}
	stt.noteRouteRows(guidedRoute{Intent: guidedTopErrorServices, TeamServices: []string{"payments"}})
	if stt.rows[0].Kind != rowKindTrace {
		t.Fatal("olay listesi varken rota listesi yazmamalı")
	}
	for i := 0; i < 10; i++ {
		stt.noteEvent("step-result", map[string]any{"tool": "find_traces", "ok": true, "preview": "3 trace\nayrıntı"})
	}
	if len(stt.steps) != chatStepsMax || stt.steps[0] != "find_traces · ok · 3 trace" {
		t.Fatalf("adım özetleri: %v", stt.steps)
	}
	if s := stepSummary(map[string]any{"tool": "query_logs", "ok": false, "error": "zaman aşımı"}); s != "query_logs · hata · zaman aşımı" {
		t.Fatalf("hatalı adım: %q", s)
	}
	other := &chatStateTurn{}
	other.noteRouteRows(guidedRoute{Intent: guidedMyProblems, TeamServices: []string{"payments"}})
	if other.rows != nil {
		t.Fatal("kapsam listesi (problem rotası) satır sayılmaz")
	}
	other.noteRouteRows(guidedRoute{Intent: guidedTeamServices, TeamServices: []string{"payments", "orders"}})
	if len(other.rows) != 2 || other.rows[1].ID != "svc:orders" {
		t.Fatalf("takım servisleri: %+v", other.rows)
	}
}

func TestFinalRowsAndPromptBlock(t *testing.T) {
	rows := threeRows()
	if got := finalRows(rows, nil, 0); got != nil {
		t.Fatal("yeni liste yok + seçim yok → liste düşer")
	}
	if got := finalRows(rows, nil, 2); len(got) != 3 {
		t.Fatal("seçim turunda liste kalır")
	}
	if got := finalRows(rows, rows[:1], 2); len(got) != 1 {
		t.Fatal("yeni liste eskisinin yerine geçer")
	}
	line := chatStateLineTR(ChatContext{Rows: rows, Steps: []string{"find_traces · ok · 3 trace"}})
	for _, want := range []string{"ÖNCEKİ TURUN SATIRLARI", "- 2. endpoint checkout POST /api/pay", "ÖNCEKİ TURUN ADIMLARI", "- find_traces · ok"} {
		if !strings.Contains(line, want) {
			t.Errorf("blokta %q yok:\n%s", want, line)
		}
	}
	// Sınır: 20 uzun satır + 6 uzun adım ≤ chatRowsPromptMax.
	var big []chatRow
	for i := 0; i < chatRowsMax; i++ {
		big = append(big, chatRow{Kind: rowKindTrace, Name: strings.Repeat("n", chatRowNameRunes), ID: tidA})
	}
	steps := []string{strings.Repeat("s", chatStepRunes), strings.Repeat("s", chatStepRunes)}
	if n := utf8.RuneCountInString(chatRowsLineTR(big, steps)); n > chatRowsPromptMax || n == 0 {
		t.Fatalf("blok %d rün (tavan %d)", n, chatRowsPromptMax)
	}
	if chatRowsLineTR(nil, nil) != "" {
		t.Fatal("boşsa blok yok")
	}
}

// TestResolveScopeCarryOverLength — v0.10.2472: devralma uzunluktan bağımsız;
// açık yeni servis / filo / kalıp / açık bırakma devralmayı keser.
func TestResolveScopeCarryOverLength(t *testing.T) {
	prev := ChatContext{Service: "checkout", Env: "prod", RangeS: 3600, RangeExplicit: true}
	long := "peki şunu da merak ediyorum, hata oranı deploy sonrasında arttı mı yoksa öncesinde de aynı seviyede miydi, bir de p99 nasıl gitti"
	if utf8.RuneCountInString(long) <= followUpMaxRunes {
		t.Fatal("test sorusu uzun olmalı")
	}
	cases := []struct {
		q              string
		inherit        bool
		wantService    string
		wantServiceSet bool
	}{
		{"peki dün aynı saatte?", true, "checkout", false},
		{long, true, "checkout", false},
		{long + " — payments için", false, "payments", true},
		{"payments'ta deploy mu vardı", false, "payments", true},
		{"tüm servislerde son bir saatte hata oranı nasıl, deploy sonrası bir artış var mı, hangi servisler etkilendi acaba", false, "checkout", false},
		{"kapsamı kaldır ve hata oranı en yüksek olanları göster, son bir saatte, mümkünse sıralı olarak ve p99 ile beraber", false, "checkout", false},
		{"harvester-* servislerinde hata oranı nasıl, son bir saatte bir artış var mı, varsa hangi servisler etkilendi", false, "checkout", false},
	}
	for _, c := range cases {
		out := resolveQ(c.q, prev)
		if out.InheritService != c.inherit || out.ServiceSet != c.wantServiceSet || out.Next.Service != c.wantService {
			t.Errorf("%q → inherit=%v set=%v svc=%q, want %v %v %q", c.q, out.InheritService, out.ServiceSet, out.Next.Service, c.inherit, c.wantServiceSet, c.wantService)
		}
		if c.inherit && (!out.InheritEnv || !(out.InheritWindow || out.WindowSet)) {
			t.Errorf("%q ortam + pencere de devralınmalı: %+v", c.q, out)
		}
	}
}

// TestChatStateOrdinalPickFlow — gerçek v2 handler: tur 1 trace listesi
// gösterir, tur 2 "ikincisini aç" o trace'e çözülür (guided trace rotası),
// tur 3 sıra sözsüz soru bugünkü yolda kalır ve listeyi düşürür.
func TestChatStateOrdinalPickFlow(t *testing.T) {
	setConversationState(t, true)
	rg := newStateRig(t, referansLoop, nil)
	prevW := guidedWindowSeam
	guidedWindowSeam = func(r guidedRoute, rangeS int64, anchor time.Time, emit func(string, any)) {
		prevW(r, rangeS, anchor, func(string, any) {})
		if r.Intent == guidedSlowTraces {
			emit("trace_list", traceListPayload{Traces: []traceListRow{
				{TraceID: tidA, ServiceName: "checkout", RootName: "GET /api/cart"},
				{TraceID: tidB, ServiceName: "checkout", RootName: "POST /api/pay"},
				{TraceID: tidC, ServiceName: "checkout", RootName: "GET /health"},
			}})
		}
		emit("answer", map[string]any{"text": "tamam"})
	}
	t.Cleanup(func() { guidedWindowSeam = prevW })

	rg.ask("checkout yavaş trace'leri göster", nil)
	g, ok := rg.lastGuided()
	if !ok || g.route.Intent != guidedSlowTraces {
		t.Fatalf("tur 1 yavaş trace rotasına gitmedi: %+v", g.route)
	}
	if st := rg.state(); len(st.Rows) != 3 || st.Rows[1].ID != tidB {
		t.Fatalf("tur 1 satırları saklanmadı: %+v", st.Rows)
	}
	body := rg.ask("ikincisini aç", nil)
	if !hasLabel(v2StepLabels(body), "seçim: önceki listenin 2. satırı") {
		t.Fatalf("seçim çipi yok: %v", v2StepLabels(body))
	}
	st := rg.state()
	if st.Trace != tidB || st.Picked != 2 || len(st.Rows) != 3 {
		t.Fatalf("tur 2 durumu: trace=%q picked=%d rows=%d", st.Trace, st.Picked, len(st.Rows))
	}
	if g, _ := rg.lastGuided(); g.route.TraceID != tidB {
		t.Fatalf("tur 2 seçilen trace'e gitmedi: %+v", g.route)
	}
	// Çözülemeyen soru bugünkü yolda kalır; liste düşer.
	rg.ask("deploy mu vardı", nil)
	if st := rg.state(); st.Rows != nil || st.Picked != 0 {
		t.Fatalf("seçimsiz yeni turda liste düşmeli: %+v", st.Rows)
	}
}

// TestApplyRowPickEndpoint — endpoint satırı kartın İncele turunun AYNI
// isteğini kurar (metin "<servis> <rota>", servis, kart atlanır, kartın penceresi).
func TestApplyRowPickEndpoint(t *testing.T) {
	prev := ChatContext{Service: "checkout", Rows: threeRows()}
	req := chatRequest{Messages: []copilot.ChatMessage{{Role: "user", Text: "checkout ne oluyor"}, {Role: "assistant", Text: "hangisi?"}, {Role: "user", Text: "ikincisini aç"}}}
	res := stateResolveOut{Next: prev}
	stt := &chatStateTurn{}
	label := stt.applyRowPick(&res, prev, &req, true)
	if label == "" || req.Messages[2].Text != "checkout POST /api/pay" || req.Context.Service != "checkout" ||
		!req.Context.SkipEndpointPick || req.Context.RangeS != 3600 || res.Next.Operation != "POST /api/pay" || stt.picked != 2 {
		t.Fatalf("label=%q text=%q ctx=%+v op=%q picked=%d", label, req.Messages[2].Text, req.Context, res.Next.Operation, stt.picked)
	}
	// Açık yeni servis anıldıysa seçim aranmaz.
	req2 := chatRequest{Messages: []copilot.ChatMessage{{Role: "user", Text: "payments ikincisi"}}}
	res2 := stateResolveOut{Next: prev, ServiceSet: true}
	if l := (&chatStateTurn{}).applyRowPick(&res2, prev, &req2, true); l != "" || req2.Messages[0].Text != "payments ikincisi" {
		t.Fatalf("açık servis varken seçim yapılmamalı: %q", l)
	}
}
