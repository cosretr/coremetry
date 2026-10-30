package api

// explain_trace_critpath_test.go — v0.10.1627 — CoSRE kritik yol planı R2.
//
// Sözleşme:
//   - Sağlıklı ama yavaş trace'in kanıtı (buildTraceExplainInput) TÜM
//     span'lerden hesaplanmış KRİTİK YOL bloğuyla BAŞLAR (HATA ZİNCİRİ yoksa);
//     zincir varsa sıra zincir → kritik yol → span JSON'u.
//   - Blok altın: 12.21 s dış gateway CLIENT'ı en büyük pay, hedef url.full.
//   - traceExplainUser'a dokunulmaz: User = blok + "\n\n" + traceExplainUser.
//   - Evidence: gecikme sahibi (en büyük pay) kutulanır; hata kaynağı varsa o
//     önce.
//   - Tek adımlı yolda blok YOK (kanıt bayt bayt eskisi gibi).
//   - Çekmece kırpması (6000 rune) bloğu korur.
//   - Takip turu: zincir → kritik yol → event sırası; çip "kritik yol" der.
// Fikstür sentetik (orders-api, gateway.ext.example, com.acme.*).

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/tempo"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

const (
	cpRootID = "d0000000000000b1"
	cpIntID  = "d0000000000000b2"
	cpCliID  = "d0000000000000b3"
	cpDBID   = "d0000000000000b4"
	cpProdID = "d0000000000000b5"
)

// cpGatewayBlock — testdata/critpath/external-gateway-12s.json'un bloğu.
const cpGatewayBlock = tracechain.CritHeader + " Kök duvar saati 12.46 s; adımlar kök → yaprak; öz = adımın yolda tek başına geçen süresi, pay = kök süresine oranı; süreler TOPLANMAZ:\n" +
	"1. [kök] orders-api · SERVER · POST /orders/score · süre 12.46 s · öz 160 ms (%1) · HTTP 200\n" +
	"2. orders-api · INTERNAL · com.acme.orders.ScoreService.evaluate · süre 12.30 s · öz 90 ms (%1)\n" +
	"3. [en büyük pay] orders-api · CLIENT · POST gateway.ext.example · süre 12.21 s · öz 12.21 s (%98) · hedef https://gateway.ext.example/v1/score · HTTP 200\n" +
	"En büyük pay: orders-api · CLIENT · hedef https://gateway.ext.example/v1/score · 12.21 s (%98) — dış bağımlılık beklemesi (çağıranın altında izlenen çocuk span yok)"

// cpTempoGatewayTrace — sağlıklı 12.46 s trace'i sahte Tempo'dan.
func cpTempoGatewayTrace(t *testing.T) *httptest.Server {
	t.Helper()
	ns := defTestT0.UnixNano()
	ms := func(v int64) int64 { return ns + v*1_000_000 }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/traces/"+defTestTrace) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sv := func(k, v string) string { return fmt.Sprintf(`{"key":%q,"value":{"stringValue":%q}}`, k, v) }
		span := func(id, parent, name string, kind int, start, dur int64, attrs ...string) string {
			p := ""
			if parent != "" {
				p = fmt.Sprintf(`"parentSpanId":%q,`, parent)
			}
			return fmt.Sprintf(`{"traceId":%q,"spanId":%q,%s"name":%q,"kind":%d,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[%s]}`,
				defTestTrace, id, p, name, kind, ms(start), ms(start+dur), strings.Join(attrs, ","))
		}
		spans := []string{
			span(cpRootID, "", "POST /orders/score", 2, 0, 12460, sv("http.request.method", "POST"), sv("http.route", "/orders/score"), `{"key":"http.response.status_code","value":{"intValue":"200"}}`),
			span(cpIntID, cpRootID, "com.acme.orders.ScoreService.evaluate", 1, 100, 12300),
			span(cpCliID, cpIntID, "POST gateway.ext.example", 3, 150, 12210, sv("url.full", "https://gateway.ext.example/v1/score"), sv("server.address", "gateway.ext.example"),
				`{"key":"server.port","value":{"intValue":"443"}}`, sv("http.request.method", "POST"), `{"key":"http.response.status_code","value":{"intValue":"200"}}`),
			span(cpDBID, cpIntID, "SELECT orders", 3, 110, 40, sv("db.system", "postgresql"), sv("db.name", "ordersdb"), sv("server.address", "pg.internal.example")),
			span(cpProdID, cpRootID, "orders.audit publish", 4, 12400, 200, sv("messaging.system", "kafka"), sv("messaging.destination.name", "orders.audit")),
		}
		fmt.Fprintf(w, `{"batches":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"orders-api"}}]},"scopeSpans":[{"spans":[%s]}]}]}`, strings.Join(spans, ","))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cpServer(t *testing.T, srv *httptest.Server) *Server {
	t.Helper()
	s := &Server{}
	s.tempo = tempo.New()
	s.tempo.Configure(tempo.Settings{Enabled: true, BaseURL: srv.URL})
	return s
}

func TestBuildTraceExplainInputCriticalBlock(t *testing.T) {
	s := cpServer(t, cpTempoGatewayTrace(t))
	in, err := s.buildTraceExplainInput(context.Background(), defTestTrace)
	if err != nil {
		t.Fatal(err)
	}
	if in.CriticalBlock != cpGatewayBlock {
		t.Errorf("KRİTİK YOL bloğu:\n%s\n--- beklenen ---\n%s", in.CriticalBlock, cpGatewayBlock)
	}
	if !strings.HasPrefix(in.User, cpGatewayBlock+"\n\nTrace "+defTestTrace+" with 5 spans:\n```json\n") {
		t.Errorf("User blokla başlayıp traceExplainUser'a devretmeli:\n%.600s", in.User)
	}
	if in.ErrorOrigin != "" || in.ChainBlock != "" {
		t.Errorf("sağlıklı trace'te zincir: %q / %q", in.ErrorOrigin, in.ChainBlock)
	}
	if in.TopStep == nil || in.TopStep.SpanID != cpCliID || !in.TopExternalWait {
		t.Fatalf("TopStep=%+v TopExternalWait=%v", in.TopStep, in.TopExternalWait)
	}
	if len(in.Evidence) == 0 || in.Evidence[0] != cpCliID {
		t.Errorf("Evidence = %v; gecikme sahibi (%s) önce kutulanmalı", in.Evidence, cpCliID)
	}
	if got := clampDrawerEvidence(in.User+strings.Repeat("x", 9000), in.LogsBlock); !strings.HasPrefix(got, cpGatewayBlock) {
		t.Errorf("çekmece kırpması bloğu korumadı:\n%.300s", got)
	}
}

func TestBuildTraceExplainInputChainThenCritical(t *testing.T) {
	s := cpServer(t, chTempoOperatorTrace(t))
	in, err := s.buildTraceExplainInput(context.Background(), defTestTrace)
	if err != nil {
		t.Fatal(err)
	}
	iCh, iCp, iTr := strings.Index(in.User, traceChainHeader), strings.Index(in.User, tracechain.CritHeader), strings.Index(in.User, "Trace "+defTestTrace)
	if !(iCh == 0 && iCh < iCp && iCp < iTr) {
		t.Errorf("sıra: zincir=%d kritik=%d trace=%d\n%.900s", iCh, iCp, iTr, in.User)
	}
	if in.User != in.ChainBlock+"\n\n"+in.CriticalBlock+"\n\n"+in.User[iTr:] {
		t.Error("User = zincir + kritik yol + traceExplainUser olmalı")
	}
	if len(in.Evidence) < 1 || in.Evidence[0] != chClientID || in.ErrorOrigin != chClientID {
		t.Errorf("hata kaynağı önce: Evidence=%v", in.Evidence)
	}
	if in.TopExternalWait != (in.TopStep != nil && in.TopStep.Kind == "client") {
		t.Errorf("TopStep=%+v ext=%v", in.TopStep, in.TopExternalWait)
	}
}

func TestTraceCriticalHeadSingleSpan(t *testing.T) {
	one := []chstore.SpanRow{{SpanID: "a1", Name: "GET /", Kind: "server", ServiceName: "orders-api", StartTime: 1, EndTime: 5_000_001}}
	if head, top := traceCriticalHead(traceCriticalPathOf(one)); head != "" || top != nil {
		t.Errorf("tek adımda blok yazıldı: %q %+v", head, top)
	}
	if got := traceCriticalFollowUp(one); got != "" {
		t.Errorf("tek adımda takip bloğu: %q", got)
	}
	if head, top := traceCriticalHead(traceCriticalPathOf(nil)); head != "" || top != nil {
		t.Error("boş girdi")
	}
}

func TestTraceFollowUpCarriesCriticalPath(t *testing.T) {
	s := cpServer(t, cpTempoGatewayTrace(t))
	events, chain, crit := s.traceFollowUpEvents(context.Background(), defTestTrace)
	if events != "" || chain != "" {
		t.Errorf("sağlıklı trace: events=%q chain=%q", events, chain)
	}
	if crit != "\n\n"+cpGatewayBlock {
		t.Fatalf("takip kritik yol bloğu:\n%q", crit)
	}
	_, chCh, _ := cpServer(t, chTempoOperatorTrace(t)).traceFollowUpEvents(context.Background(), defTestTrace)
	tf := traceFollowUp{TraceID: tfTrace, Env: "prod", Chain: chCh, Critical: crit,
		Events: buildTraceEventDigest([]chstore.SpanRow{evSpan("aaaaaaaaaaaaaaa1", 1, 12, true, evExceptionEvent(evTopicExType, evTopicExMsg, evKafkaStack))}).Block()}
	p := traceFollowUpPromptTR(&tf, "önceki")
	iCh, iCp, iEv := strings.Index(p, traceChainHeader), strings.Index(p, tracechain.CritHeader), strings.Index(p, traceEventsBlockHeader)
	if !(iCh >= 0 && iCh < iCp && iCp < iEv) {
		t.Errorf("sıra: zincir=%d kritik=%d event=%d", iCh, iCp, iEv)
	}
	if got := traceFollowUpChipTR(tf); !strings.Contains(got, "kritik yol") {
		t.Errorf("çip kritik yolu söylemiyor: %q", got)
	}
	base := traceFollowUp{TraceID: tfTrace, Env: "prod"}
	if strings.Contains(traceFollowUpPromptTR(&base, ""), tracechain.CritHeader) || strings.Contains(traceFollowUpChipTR(base), "kritik yol") {
		t.Error("Critical boşken blok/çip yazıldı")
	}
}
