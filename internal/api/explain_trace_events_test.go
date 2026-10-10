package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/tempo"
)

// explain_trace_events_test.go — v0.10.1147 (operatör-bildirimli, prod):
// tek span'lik ERROR trace'te (Kafka producer "… publish") "CoSRE'ye sor"
// nedeni anmadı; neden span'in EXCEPTION EVENT'indeydi ve kanıt paketi
// SpanRow.Events'i hiç okumuyordu. Pinler: exception tipi/mesajı/ilk
// frame'ler kanıta girer; tavanlar tutar; event yoksa prompt bayt-bayt
// eskisi; takip sohbeti aynı özeti taşır.

const (
	evTopicExType = "org.apache.kafka.common.errors.TopicAuthorizationException"
	evTopicExMsg  = "Not authorized to access topics: [orders.dlq]"
)

// evKafkaStack — sentetik Java stack: başlık, 3+ çerçeve frame'i, uygulama
// frame'leri, JDK kuyruğu ve "... N more".
const evKafkaStack = evTopicExType + ": " + evTopicExMsg + `
	at org.apache.kafka.clients.producer.KafkaProducer$FutureFailure.<init>(KafkaProducer.java:1442)
	at org.apache.kafka.clients.producer.KafkaProducer.doSend(KafkaProducer.java:1084)
	at org.apache.kafka.clients.producer.KafkaProducer.send(KafkaProducer.java:962)
	at org.springframework.kafka.core.KafkaTemplate.doSend(KafkaTemplate.java:790)
	at org.springframework.kafka.core.KafkaTemplate.send(KafkaTemplate.java:500)
	at com.example.orders.dlq.DlqPublisher.publish(DlqPublisher.java:42)
	at com.example.orders.OrderConsumer.onMessage(OrderConsumer.java:88)
	at java.base/java.lang.Thread.run(Thread.java:833)
	... 12 more`

// evExceptionEvent — CH okuma yolunun şekli (stored JSON → interface{}).
func evExceptionEvent(typ, msg, stack string) map[string]any {
	attrs := map[string]any{"exception.type": typ, "exception.message": msg}
	if stack != "" {
		attrs["exception.stacktrace"] = stack
	}
	return map[string]any{"name": "exception", "timeNano": float64(1), "attributes": attrs}
}

func evSpan(id string, start, durMs int64, errStatus bool, events ...map[string]any) chstore.SpanRow {
	sp := chstore.SpanRow{SpanID: id, Name: "orders.dlq publish", ServiceName: "svc-orders", Kind: "producer",
		StartTime: start, EndTime: start + durMs*1_000_000}
	if errStatus {
		sp.StatusCode = "error"
	}
	if len(events) > 0 {
		evs := make([]any, len(events))
		for i, e := range events {
			evs[i] = e
		}
		sp.Events = evs
	}
	return sp
}

func TestSpanEventsOf(t *testing.T) {
	// Tempo yolunun tipli dilimi (tempo.tempoSpanEvent ile aynı JSON etiketleri).
	type typed struct {
		Name       string            `json:"name"`
		TimeNano   uint64            `json:"timeNano"`
		Attributes map[string]string `json:"attributes"`
	}
	ch := []any{evExceptionEvent(evTopicExType, evTopicExMsg, "")}
	cases := map[string]any{
		"CH []any":      ch,
		"Tempo tipli":   []typed{{Name: "exception", Attributes: map[string]string{"exception.type": evTopicExType, "exception.message": evTopicExMsg}}},
		"JSON string":   `[{"name":"exception","attributes":{"exception.type":"` + evTopicExType + `","exception.message":"` + evTopicExMsg + `"}}]`,
		"sayısal değer": []any{map[string]any{"name": "exception", "attributes": map[string]any{"exception.type": evTopicExType, "exception.message": evTopicExMsg, "retry": float64(3)}}},
	}
	for name, in := range cases {
		got := spanEventsOf(in)
		if len(got) != 1 || got[0].Name != "exception" || got[0].Attrs["exception.type"] != evTopicExType || got[0].Attrs["exception.message"] != evTopicExMsg {
			t.Errorf("%s: %+v", name, got)
		}
		if !isExceptionEvent(got[0]) {
			t.Errorf("%s: exception sayılmadı", name)
		}
	}
	for name, in := range map[string]any{"nil": nil, "bozuk JSON": "{", "nesne": map[string]any{"a": 1}, "boş": []any{}} {
		if got := spanEventsOf(in); len(got) != 0 {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestExceptionStackHead(t *testing.T) {
	got := exceptionStackHead(evKafkaStack, evTopicExType, traceStackMaxLines, traceStackMaxRunes)
	want := []string{
		"at org.apache.kafka.clients.producer.KafkaProducer$FutureFailure.<init>(KafkaProducer.java:1442)",
		"at org.apache.kafka.clients.producer.KafkaProducer.doSend(KafkaProducer.java:1084)",
		"at org.apache.kafka.clients.producer.KafkaProducer.send(KafkaProducer.java:962)",
		"… 2 çerçeve satırı atlandı",
		"at com.example.orders.dlq.DlqPublisher.publish(DlqPublisher.java:42)",
		"at com.example.orders.OrderConsumer.onMessage(OrderConsumer.java:88)",
		"… 1 çerçeve satırı atlandı",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("stack başı:\n got=%q\nwant=%q", got, want)
	}

	// "Caused by:" her zaman kalır; segment sayacı sıfırlanır.
	chain := "com.example.WrapperException: wrap\n\tat com.example.A.a(A.java:1)\nCaused by: java.sql.SQLException: ORA-00942: table or view does not exist\n" +
		"\tat oracle.jdbc.X.a(X.java:1)\n\tat oracle.jdbc.X.b(X.java:2)\n\tat oracle.jdbc.X.c(X.java:3)\n\tat oracle.jdbc.X.d(X.java:4)\n\t... 9 more"
	got = exceptionStackHead(chain, "com.example.WrapperException", 12, 1500)
	if len(got) < 2 || got[1] != "Caused by: java.sql.SQLException: ORA-00942: table or view does not exist" {
		t.Fatalf("Caused by satırı: %q", got)
	}
	for _, l := range got {
		if strings.Contains(l, "more") {
			t.Errorf("'... N more' satırı kaldı: %q", l)
		}
	}

	// Tavanlar: satır ve rune.
	var long strings.Builder
	long.WriteString("com.example.Boom: x\n")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&long, "\tat com.example.app.Layer%d.call%s(Layer%d.java:%d)\n", i, strings.Repeat("x", 40), i, i+1)
	}
	for _, c := range []struct{ lines, runes int }{{12, 1500}, {6, 600}, {3, 50}} {
		got = exceptionStackHead(long.String(), "com.example.Boom", c.lines, c.runes)
		n := 0
		for _, l := range got {
			n += utf8.RuneCountInString(l)
		}
		if len(got) == 0 || len(got) > c.lines || n > c.runes {
			t.Errorf("tavan %d/%d aşıldı: %d satır, %d rune", c.lines, c.runes, len(got), n)
		}
	}

	// Python: kök neden SONDA.
	py := "Traceback (most recent call last):\n  File \"/srv/app.py\", line 10, in handler\n    publish()\n  File \"/srv/q.py\", line 3, in publish\n    raise PermissionError(\"topic orders.dlq\")\nPermissionError: topic orders.dlq"
	got = exceptionStackHead(py, "PermissionError", 3, 1500)
	if len(got) != 3 || got[len(got)-1] != "PermissionError: topic orders.dlq" || !strings.HasPrefix(got[0], "… ") {
		t.Fatalf("python kuyruğu: %q", got)
	}

	if got := exceptionStackHead("  \n ", "x", 12, 1500); got != nil {
		t.Fatalf("boş stack: %q", got)
	}
}

// Tek span'lik hata trace'i (prod senaryosu): tip, mesaj, ilk frame'ler ve
// kod yolu girdileri.
func TestBuildTraceEventDigestSingleErrorSpan(t *testing.T) {
	spans := []chstore.SpanRow{evSpan("aaaaaaaaaaaaaaa1", 1_000, 12, true, evExceptionEvent(evTopicExType, evTopicExMsg, evKafkaStack))}
	d := buildTraceEventDigest(spans)
	if len(d.Spans) != 1 || !d.HasException || d.SkippedSpans != 0 {
		t.Fatalf("özet: %+v", d)
	}
	ev := d.Spans[0].Events[0]
	if ev.ExType != evTopicExType || ev.ExMessage != evTopicExMsg || len(ev.Stack) == 0 ||
		!strings.Contains(ev.Stack[0], "KafkaProducer") {
		t.Fatalf("exception satırı: %+v", ev)
	}
	if d.RawStack != evKafkaStack || d.RawStackService != "svc-orders" || d.RawStackSpanID != "aaaaaaaaaaaaaaa1" {
		t.Fatalf("kod yolu girdisi (HAM stack): %q %q %q", d.RawStack, d.RawStackService, d.RawStackSpanID)
	}
	if len(d.ErrTexts) != 1 || d.ErrTexts[0] != evTopicExType+": "+evTopicExMsg {
		t.Fatalf("hata metni: %q", d.ErrTexts)
	}
	b := d.Block()
	for _, want := range []string{traceEventsBlockHeader, "```json\n", `"exType":"` + evTopicExType + `"`,
		`"exMessage":"` + evTopicExMsg + `"`, "KafkaProducer.doSend(KafkaProducer.java:1084)", "DlqPublisher.publish",
		"<init>", traceEventsExceptionDirective} {
		if !strings.Contains(b, want) {
			t.Errorf("blok %q içermiyor:\n%s", want, b)
		}
	}
	if !strings.HasPrefix(b, "\n\n") || strings.Count(b, "```") != 2 {
		t.Fatalf("blok biçimi (log bloğu emsali, tek çit):\n%s", b)
	}
}

// Event yoksa hiçbir şey değişmez: blok boş, prompt bayt-bayt eskisi.
func TestBuildTraceEventDigestNoEvents(t *testing.T) {
	spans := []chstore.SpanRow{evSpan("aaaaaaaaaaaaaaa1", 1, 5, true), evSpan("aaaaaaaaaaaaaaa2", 2, 50, false)}
	spans[0].Events = []any{} // CH: boş dizi
	d := buildTraceEventDigest(spans)
	if len(d.Spans) != 0 || d.HasException || d.RawStack != "" || len(d.ErrTexts) != 0 || d.Block() != "" {
		t.Fatalf("event'siz trace'te özet dolu: %+v", d)
	}
}

// Öncelik: exception'lı hata span'i önce (zamanca sonra olsa da); en yavaş
// span event'leriyle girer; exception'sız, hatasız, yavaş olmayan span girmez.
func TestBuildTraceEventDigestPriority(t *testing.T) {
	other := map[string]any{"name": "message", "attributes": map[string]any{"messaging.message.id": "m-1"}}
	spans := []chstore.SpanRow{
		evSpan("aaaaaaaaaaaaaaa1", 1, 5, false, other),                                            // ne hata ne en yavaş → yok
		evSpan("aaaaaaaaaaaaaaa2", 2, 900, false, other),                                          // en yavaş → 3.
		evSpan("aaaaaaaaaaaaaaa3", 3, 5, true, other),                                             // hata, exception'sız → 2.
		evSpan("aaaaaaaaaaaaaaa4", 4, 5, true, evExceptionEvent(evTopicExType, evTopicExMsg, "")), // → 1.
	}
	d := buildTraceEventDigest(spans)
	var ids []string
	for _, s := range d.Spans {
		ids = append(ids, s.SpanID)
	}
	if strings.Join(ids, ",") != "aaaaaaaaaaaaaaa4,aaaaaaaaaaaaaaa3,aaaaaaaaaaaaaaa2" {
		t.Fatalf("sıra: %v", ids)
	}
}

// Tavanlar: span sayısı, span başına event/exception, mesaj ve stack
// boyları, toplam blok yükü; aynı exception'ın stack'i bir kez.
func TestBuildTraceEventDigestCaps(t *testing.T) {
	var deep strings.Builder
	deep.WriteString("com.example.Boom: x\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&deep, "\tat com.example.app.Layer%d.call%s(Layer%d.java:%d)\n", i, strings.Repeat("y", 60), i, i+1)
	}
	longMsg := strings.Repeat("ş", 2000)
	noisy := func(i int) map[string]any {
		return map[string]any{"name": fmt.Sprintf("retry-%d", i), "attributes": map[string]any{
			"a": strings.Repeat("v", 500), "b": "2", "c": "3", "d": "4", "e": "5"}}
	}
	var spans []chstore.SpanRow
	for i := 0; i < 8; i++ {
		spans = append(spans, evSpan(fmt.Sprintf("bbbbbbbbbbbbbb%02d", i), int64(i), 5, true,
			evExceptionEvent(fmt.Sprintf("com.example.Boom%d", i), longMsg, deep.String()),
			evExceptionEvent(fmt.Sprintf("com.example.Second%d", i), "ikinci", deep.String()),
			evExceptionEvent(fmt.Sprintf("com.example.Third%d", i), "üçüncü", ""),
			noisy(1), noisy(2), noisy(3), noisy(4)))
	}
	d := buildTraceEventDigest(spans)
	if len(d.Spans) == 0 || len(d.Spans) > traceEventSpansMax {
		t.Fatalf("span tavanı: %d", len(d.Spans))
	}
	if d.SkippedSpans != len(spans)-len(d.Spans) {
		t.Errorf("atlanan span sayısı dürüst değil: %d (yazılan %d / %d)", d.SkippedSpans, len(d.Spans), len(spans))
	}
	payload := marshalTraceEvents(d.Spans)
	if n := utf8.RuneCountInString(payload); n > traceEventsBlockMaxRunes {
		t.Fatalf("blok yükü %d rune > %d", n, traceEventsBlockMaxRunes)
	}
	for _, s := range d.Spans {
		exN := 0
		if len(s.Events) > traceEventsPerSpanMax {
			t.Errorf("%s: %d event", s.SpanID, len(s.Events))
		}
		if s.Omitted == 0 {
			t.Errorf("%s: kırpılan event sayısı söylenmedi", s.SpanID)
		}
		for _, e := range s.Events {
			if e.exKey != "" {
				exN++
			}
			if utf8.RuneCountInString(e.ExMessage) > traceExMsgMaxRunes+1 {
				t.Errorf("mesaj tavanı: %d", utf8.RuneCountInString(e.ExMessage))
			}
			if len(e.Stack) > traceStackMaxLines {
				t.Errorf("stack satır tavanı: %d", len(e.Stack))
			}
			n := 0
			for _, l := range e.Stack {
				n += utf8.RuneCountInString(l)
			}
			if n > traceStackMaxRunes {
				t.Errorf("stack rune tavanı: %d", n)
			}
			if len(e.Attrs) > traceEventAttrsMax {
				t.Errorf("attr tavanı: %d", len(e.Attrs))
			}
			for k, v := range e.Attrs {
				if utf8.RuneCountInString(v) > traceEventAttrMaxRunes+1 || utf8.RuneCountInString(k) > traceEventKeyMaxRunes+1 {
					t.Errorf("attr boyu: %q", k)
				}
			}
		}
		if exN > traceExceptionsPerSpan {
			t.Errorf("%s: %d exception", s.SpanID, exN)
		}
	}
	// Yalnız İLK yazılan stack büyük bütçeyi alır.
	big := 0
	for _, s := range d.Spans {
		for _, e := range s.Events {
			if len(e.Stack) > traceStackRestLines {
				big++
			}
		}
	}
	if big > 1 {
		t.Errorf("büyük stack bütçesi %d kez kullanıldı", big)
	}

	// Aynı exception (retry fırtınası): stack bir kez, sonra referans.
	same := []chstore.SpanRow{
		evSpan("cccccccccccccc01", 1, 5, true, evExceptionEvent(evTopicExType, evTopicExMsg, evKafkaStack)),
		evSpan("cccccccccccccc02", 2, 5, true, evExceptionEvent(evTopicExType, evTopicExMsg, evKafkaStack)),
	}
	d = buildTraceEventDigest(same)
	if len(d.Spans) != 2 || len(d.Spans[1].Events[0].Stack) != 0 || d.Spans[1].Events[0].StackSameAs != "cccccccccccccc01" {
		t.Fatalf("tekrar eden stack katlanmadı: %+v", d.Spans)
	}
}

// Çekmece kırpması: span listesi bütçeyi aşsa da exception bloğu korunur
// (blok kuyruğun başında, LogsBlock = kuyruk).
func TestDrawerClampKeepsSpanEventsBlock(t *testing.T) {
	d := buildTraceEventDigest([]chstore.SpanRow{evSpan("aaaaaaaaaaaaaaa1", 1, 12, true, evExceptionEvent(evTopicExType, evTopicExMsg, evKafkaStack))})
	tail := d.Block() + "\n\nLOGLAR:\nboom"
	full := traceExplainUser(defTestTrace, 100, 100, "["+strings.Repeat(`{"name":"GET /x","service":"svc-orders"},`, 400)+"{}]", tail)
	got := clampDrawerEvidence(full, tail)
	if !strings.Contains(got, evTopicExMsg) || !strings.Contains(got, evTopicExType) || !strings.Contains(got, "LOGLAR") {
		t.Fatalf("kırpma exception bloğunu düşürdü:\n%s", got)
	}
}

// ── uçtan uca: explain + takip ───────────────────────────────────────────

// evTempoTrace — tek span'lik sahte Tempo trace'i: svc-orders "orders.dlq
// publish" (producer, status error, mesajsız) + exception event'i.
func evTempoTrace(t *testing.T, traceID string) *httptest.Server {
	t.Helper()
	ns := time.Now().Add(-2 * time.Hour).Truncate(time.Second).UnixNano()
	str := func(k, v string) map[string]any {
		return map[string]any{"key": k, "value": map[string]any{"stringValue": v}}
	}
	body, _ := json.Marshal(map[string]any{"batches": []any{map[string]any{
		"resource": map[string]any{"attributes": []any{str("service.name", "svc-orders")}},
		"scopeSpans": []any{map[string]any{"spans": []any{map[string]any{
			"traceId": traceID, "spanId": "aaaaaaaaaaaaaaa1", "name": "orders.dlq publish", "kind": 4,
			"startTimeUnixNano": fmt.Sprint(ns), "endTimeUnixNano": fmt.Sprint(ns + 12_000_000),
			"status": map[string]any{"code": 2},
			"events": []any{map[string]any{"timeUnixNano": fmt.Sprint(ns + 11_000_000), "name": "exception",
				"attributes": []any{str("exception.type", evTopicExType), str("exception.message", evTopicExMsg),
					str("exception.stacktrace", evKafkaStack)}}},
		}}}},
	}}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/traces/"+traceID) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// "CoSRE'ye sor" (kodsuz varsayılan): modele giden kanıt exception'ı taşır,
// sistem istemi exception kuralını taşır.
func TestExplainTraceCarriesSpanExceptionEvent(t *testing.T) {
	p := newDefProvider(t, defAnswerA, defAnswerB)
	s := defServer(t, p)
	s.tempo = tempo.New()
	s.tempo.Configure(tempo.Settings{Enabled: true, BaseURL: evTempoTrace(t, defTestTrace).URL})
	w := defCall(s, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	sys, user := p.lastMessages(t)
	for _, want := range []string{"SPAN EVENT'LERİ", evTopicExType, evTopicExMsg,
		"KafkaProducer.doSend(KafkaProducer.java:1084)", "DlqPublisher.publish(DlqPublisher.java:42)"} {
		if !strings.Contains(user, want) {
			t.Errorf("explain kanıtı %q içermiyor:\n%s", want, user)
		}
	}
	if !strings.Contains(sys, "EXCEPTION EVENTS COME FIRST") {
		t.Error("trace sistem istemi exception kuralını taşımıyor")
	}
}

// Event'siz trace (mevcut sahte Tempo): kanıtta blok YOK — davranış aynen.
func TestExplainTraceWithoutEventsUnchanged(t *testing.T) {
	p := newDefProvider(t, defAnswerA, defAnswerB)
	s := defServer(t, p)
	if w := defCall(s, "", ""); w.Code != http.StatusOK {
		t.Fatalf("HTTP %d", w.Code)
	}
	_, user := p.lastMessages(t)
	if strings.Contains(user, "SPAN EVENT'LERİ") || strings.Contains(user, traceEventsExceptionDirective) {
		t.Fatalf("event'siz trace'e blok girdi:\n%s", user)
	}
	if strings.Count(user, "```") != 2 {
		t.Fatalf("event'siz kanıt tek çit olmalı (span listesi):\n%s", user)
	}
}

// Sohbetteki trace sorusu (guided trace_by_id odaklı yol) AYNI kanıtı görür.
func TestGuidedTraceExplainCarriesSpanExceptionEvent(t *testing.T) {
	p := newDefProvider(t, "TopicAuthorizationException")
	s := defServer(t, p)
	s.tempo = tempo.New()
	s.tempo.Configure(tempo.Settings{Enabled: true, BaseURL: evTempoTrace(t, defTestTrace).URL})
	emit := func(string, any) {}
	to := time.Now()
	handled, ok := s.guidedTraceExplain(context.Background(), emit, guidedRoute{Intent: guidedTraceByID, TraceID: defTestTrace},
		"bu trace neden hata verdi", to.Add(-time.Hour), to, "")
	if !handled || !ok {
		t.Fatalf("guided trace yolu: handled=%v ok=%v", handled, ok)
	}
	_, user := p.lastMessages(t)
	if !strings.Contains(user, evTopicExType) || !strings.Contains(user, evTopicExMsg) {
		t.Fatalf("guided kanıt exception'ı taşımıyor:\n%s", user)
	}
}

// Takip ("Bu neden oluyor?"): sistem mesajı ilk cevabın exception özetini
// taşır; çip bunu söyler.
func TestTraceFollowUpLoopCarriesSpanExceptions(t *testing.T) {
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		return answerMsg("TopicAuthorizationException: orders.dlq için ACL eksik.")
	})
	s, _ := newLoopTestServer(t, llm, "u-a")
	s.tempo = tempo.New()
	s.tempo.Configure(tempo.Settings{Enabled: true, BaseURL: evTempoTrace(t, tfTrace).URL})
	frames := postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Bu neden oluyor?", anchoredToMs(), "conv-ev"))
	calls := llm.calls()
	if len(calls) == 0 {
		t.Fatal("model çağrılmadı")
	}
	sys := llmSystem(calls[0])
	for _, want := range []string{traceEventsBlockHeader, evTopicExType, evTopicExMsg, "KafkaProducer.doSend", "TRACE İNCELEMESİ SÜRÜYOR"} {
		if !strings.Contains(sys, want) {
			t.Errorf("takip sistem mesajı %q içermiyor", want)
		}
	}
	// Sıra: takip talimatı → span event'leri → önceki açıklama.
	iAdd, iEv, iEx := strings.Index(sys, "TRACE İNCELEMESİ SÜRÜYOR"), strings.Index(sys, traceEventsBlockHeader), strings.Index(sys, traceFollowUpExplainHeader)
	if !(iAdd >= 0 && iAdd < iEv && iEv < iEx) {
		t.Errorf("sıra: talimat=%d event=%d açıklama=%d", iAdd, iEv, iEx)
	}
	if !strings.Contains(strings.Join(stepLabels(frames), "\n"), "span event'leri") {
		t.Errorf("bağlam çipi span event'lerini söylemiyor: %v", stepLabels(frames))
	}
}

// Takip bağlamı: Events boşsa bölüm bayt-bayt eskisi; doluysa talimattan
// sonra, açıklamadan önce ve sayı tohumunda.
func TestTraceFollowUpPromptEvents(t *testing.T) {
	base := traceFollowUp{TraceID: tfTrace, Env: "prod"}
	withEv := base
	withEv.Events = buildTraceEventDigest([]chstore.SpanRow{evSpan("aaaaaaaaaaaaaaa1", 1, 12, true,
		evExceptionEvent(evTopicExType, evTopicExMsg, evKafkaStack))}).Block()
	plain, rich := traceFollowUpPromptTR(&base, "önceki"), traceFollowUpPromptTR(&withEv, "önceki")
	if strings.Contains(plain, traceEventsBlockHeader) {
		t.Fatal("Events boşken blok yazıldı")
	}
	if strings.Replace(rich, withEv.Events, "", 1) != plain {
		t.Fatal("Events yalnız kendi bloğunu eklemeli")
	}
	if i, j := strings.Index(rich, withEv.Events), strings.Index(rich, traceFollowUpExplainHeader); i < 0 || i > j {
		t.Fatalf("blok açıklamadan önce değil: %d %d", i, j)
	}
	if seed := traceFollowUpEvidenceSeed([]string{traceFollowUpPromptTR(&withEv, "")}, nil); !strings.Contains(seed, evTopicExMsg) {
		t.Error("sayı tohumu exception mesajını taşımıyor")
	}
	if got := traceFollowUpChipTR(base); strings.Contains(got, "span event") {
		t.Errorf("event'siz çip değişti: %q", got)
	}
}

// Sistem istemleri: takip ve çekmece istemleri aynı kuralı taşır (prompt
// pinleri copilot paketinde; burada çağrılan yüzeyin metni).
func TestFollowUpPromptsNameSpanEvents(t *testing.T) {
	for name, p := range map[string]string{"followup": copilot.TraceFollowUpAddendum(), "drawer": copilot.SystemPromptDrawerChat()} {
		if !strings.Contains(p, "SPAN EVENT'LERİ") || !strings.Contains(p, "birincil") {
			t.Errorf("%s istemi span exception kuralını taşımıyor", name)
		}
	}
}
