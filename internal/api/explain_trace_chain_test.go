package api

// explain_trace_chain_test.go — v0.10.1371 — operator-reported: KPS/OSB 500 →
// null → NPE; explain led with the null-check.
//
// Prod trace: KPS/OSB'ye giden CLIENT span'i POST HTTP 500 döndü (24 sa p50'nin
// ×7.6'sı); çağıran ardından NullPointerException logladı
// (KpsApsV2Util.java:760). CoSRE trace açıklaması "null-check ekleyin" ile
// başladı. Operatör: "KPS sorgusundan 500 almış ama null check gibi söylüyor.
// Haklı ancak KPS'den 500 aldığını da söylese iyi olur."
//
// Pinler: hata kaynağı = en derin hatalı yaprak (eşitlikte CLIENT, sonra en
// erken), zincir kaynaktan köke; blok düz metin (çitsiz), sağlıklı trace'te "";
// explain User'ı blokla BAŞLAR, kanıt listesi kaynakla başlar; takip turu aynı
// zinciri (tabansız) taşır. Hostlar example.test.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/stackparse"
	"github.com/cilcenk/coremetry/internal/tempo"
)

const (
	chRootID   = "c0000000000000a1"
	chUtilID   = "c0000000000000a2"
	chClientID = "c0000000000000a3"
	chOSBURL   = "https://osb.example.test/TibcoESB/KPS/KimlikSorgula?wsdl"
	chNPEMsg   = `Cannot invoke "String.trim()" because "resp.getAd()" is null`
)

// chSpan — msec ofsetli test span'i.
func chSpan(id, parent, svc, name, kind string, startMs, durMs int64, errStatus bool) chstore.SpanRow {
	base := defTestT0.UnixNano()
	sp := chstore.SpanRow{SpanID: id, ParentSpanID: parent, ServiceName: svc, Name: name, Kind: kind,
		StartTime: base + startMs*1_000_000, EndTime: base + (startMs+durMs)*1_000_000}
	if errStatus {
		sp.StatusCode = "error"
	}
	return sp
}

// chOperatorTrace — operatörün trace'i: kök SERVER (hata) → INTERNAL util
// (hata) → CLIENT POST osb.example.test HTTP 500 (50 ms).
func chOperatorTrace() []chstore.SpanRow {
	root := chSpan(chRootID, "", "bsa-kps-prod", "POST /BSAWEB/kps/kimlik", "server", 0, 120, true)
	util := chSpan(chUtilID, chRootID, "bsa-kps-prod", "KpsApsV2Util.sorgula", "internal", 5, 100, true)
	cl := chSpan(chClientID, chUtilID, "bsa-kps-prod", "POST", "client", 10, 50, true)
	cl.HTTPStatus = 500
	cl.Attributes = map[string]string{"url.full": chOSBURL, "server.address": "osb.example.test", "http.request.method": "POST"}
	return []chstore.SpanRow{root, util, cl}
}

func TestTraceErrorChainOrigin(t *testing.T) {
	t.Run("operatör şekli", func(t *testing.T) {
		c := traceErrorChainOf(chOperatorTrace())
		if !c.OK || c.Origin.Span.SpanID != chClientID {
			t.Fatalf("kaynak = %q (OK=%v); %q", c.Origin.Span.SpanID, c.OK, chClientID)
		}
		var ids []string
		for _, h := range c.Hops {
			ids = append(ids, h.Span.SpanID)
		}
		if got := strings.Join(ids, ","); got != chClientID+","+chUtilID+","+chRootID {
			t.Errorf("halkalar = %s; kaynak → köke", got)
		}
		if c.Origin.Fail.HTTPCode != 500 || c.Origin.Depth != 2 || c.OtherLeaf != 0 {
			t.Errorf("kaynak: http=%d depth=%d other=%d", c.Origin.Fail.HTTPCode, c.Origin.Depth, c.OtherLeaf)
		}
	})
	t.Run("tek span", func(t *testing.T) {
		c := traceErrorChainOf([]chstore.SpanRow{chSpan("s1", "", "svc", "op", "producer", 0, 5, true)})
		if !c.OK || len(c.Hops) != 1 || c.Origin.Span.SpanID != "s1" {
			t.Fatalf("tek span: %+v", c)
		}
		if b := c.Block(traceChainException{}); !strings.Contains(b, "[hata kaynağı · kök]") {
			t.Errorf("tek halka hem kaynak hem kök olmalı:\n%s", b)
		}
	})
	t.Run("sağlıklı", func(t *testing.T) {
		spans := chOperatorTrace()
		for i := range spans {
			spans[i].StatusCode = "ok"
		}
		c := traceErrorChainOf(spans)
		if c.OK || c.Block(traceChainException{Type: "X", Msg: "y", Source: "log"}) != "" {
			t.Fatalf("hatasız trace'te zincir olmamalı: %+v", c)
		}
		if traceErrorChainOf(nil).OK {
			t.Fatal("boş trace")
		}
	})
	t.Run("iki hatalı yaprak → en derin", func(t *testing.T) {
		spans := append(chOperatorTrace(),
			chSpan("shallow-err", chRootID, "bsa-kps-prod", "SELECT", "client", 2, 3, true))
		c := traceErrorChainOf(spans)
		if c.Origin.Span.SpanID != chClientID || c.OtherLeaf != 1 || len(c.OtherIDs) != 1 || c.OtherIDs[0] != "shallow-err" {
			t.Fatalf("kaynak=%s other=%d ids=%v", c.Origin.Span.SpanID, c.OtherLeaf, c.OtherIDs)
		}
		if b := c.Block(traceChainException{}); !strings.Contains(b, "Zincir dışında 1 başka hata ucu (span: shallow-err).") {
			t.Errorf("zincir dışı satırı yok:\n%s", b)
		}
	})
	t.Run("eşit derinlik → CLIENT, sonra en erken", func(t *testing.T) {
		root := chSpan("r", "", "a", "GET /x", "server", 0, 100, true)
		in := chSpan("i", "r", "a", "work", "internal", 1, 10, true)
		cl := chSpan("c", "r", "a", "GET", "client", 50, 10, true)
		cl2 := chSpan("c2", "r", "a", "GET", "client", 60, 10, true)
		if got := traceErrorChainOf([]chstore.SpanRow{root, in, cl2, cl}).Origin.Span.SpanID; got != "c" {
			t.Errorf("kaynak = %s; eşitlikte CLIENT + en erken (c)", got)
		}
	})
	t.Run("ebeveyni eksik", func(t *testing.T) {
		orphan := chSpan("o1", "missing0000000", "svc", "POST", "client", 0, 5, true)
		c := traceErrorChainOf([]chstore.SpanRow{orphan})
		if !c.OK || len(c.Hops) != 1 {
			t.Fatalf("yetim kaynak: %+v", c)
		}
		b := c.Block(traceChainException{})
		if strings.Contains(b, "kök]") || !strings.Contains(b, "ebeveyn span trace'te yok") {
			t.Errorf("yetim tepe kök diye etiketlendi:\n%s", b)
		}
	})
	t.Run("HTTP kodu kolondan ya da attribute'tan", func(t *testing.T) {
		col := chSpan("a", "", "svc", "POST", "client", 0, 5, true)
		col.HTTPStatus = 502
		attr := chSpan("b", "", "svc", "POST", "client", 0, 5, true)
		attr.Attributes = map[string]string{"http.response.status_code": "503"}
		if got := traceErrorChainOf([]chstore.SpanRow{col}).Origin.Fail.HTTPCode; got != 502 {
			t.Errorf("kolon kodu = %d", got)
		}
		if got := traceErrorChainOf([]chstore.SpanRow{attr}).Origin.Fail.HTTPCode; got != 503 {
			t.Errorf("attribute kodu = %d", got)
		}
	})
	t.Run("kaynak ilk 100 span'in dışında", func(t *testing.T) {
		spans := []chstore.SpanRow{chSpan("root", "", "svc", "GET /", "server", 0, 10_000, false)}
		for i := 0; i < 120; i++ { // 120 sığ hatalı yaprak (pick'in hata kotası 50)
			spans = append(spans, chSpan(fmt.Sprintf("leaf%03d", i), "root", "svc", "q", "internal", int64(i), 2, true))
		}
		spans = append(spans,
			chSpan("mid", "root", "svc", "call", "internal", 500, 1, false),
			chSpan("deep", "mid", "svc", "POST", "client", 500, 1, true))
		c := traceErrorChainOf(spans)
		if c.Origin.Span.SpanID != "deep" || c.OtherLeaf != 120 {
			t.Fatalf("kaynak = %s other=%d; derin yaprak (deep)", c.Origin.Span.SpanID, c.OtherLeaf)
		}
		for _, sp := range pickExplainSpans(spans, traceExplainSpanCap) {
			if sp.SpanID == "deep" {
				t.Fatal("test kurgusu: deep 100'lük seçimde olmamalı")
			}
		}
	})
}

func TestTraceErrorChainBlockTR(t *testing.T) {
	c := traceErrorChainOf(chOperatorTrace())
	c.BaseP50Ms, c.BaseOK = 6.6, true
	npe := &logstore.LogRecord{ServiceName: "bsa-kps-prod", Body: "KPS sorgusu işlenemedi\njava.lang.NullPointerException: " + chNPEMsg +
		"\n\tat com.example.kps.KpsApsV2Util.map(KpsApsV2Util.java:760)\n\tat com.example.kps.KpsService.call(KpsService.java:42)"}
	stack, _ := stackFromLogForTest(npe)
	b := c.Block(traceChainLogException(npe, stack))
	lines := strings.Split(b, "\n")
	if !strings.HasPrefix(b, traceChainHeader) || len(lines) < 5 {
		t.Fatalf("blok başlığı/satırları:\n%s", b)
	}
	wantHop1 := "1. [hata kaynağı] bsa-kps-prod · CLIENT · POST osb.example.test → /TibcoESB/KPS/KimlikSorgula · HTTP 500 · 50 ms (24 sa p50 6.6 ms, ×7.6)"
	if lines[1] != wantHop1 {
		t.Errorf("1. halka\n got: %s\nwant: %s", lines[1], wantHop1)
	}
	if !strings.HasPrefix(lines[3], "3. [kök] bsa-kps-prod · SERVER · POST /BSAWEB/kps/kimlik · status error · 120 ms") {
		t.Errorf("kök halka: %s", lines[3])
	}
	wantEx := "Zincirin ardından kaydedilen exception: bsa-kps-prod · java.lang.NullPointerException: " + chNPEMsg + " (log)"
	if lines[4] != wantEx {
		t.Errorf("exception satırı\n got: %s\nwant: %s", lines[4], wantEx)
	}
	if strings.Contains(b, "```") {
		t.Error("blok çitli olmamalı")
	}
	if strings.HasPrefix(b, "\n") || strings.HasSuffix(b, "\n") {
		t.Error("blok baş/son satır sonu taşımamalı (ayırıcıyı çağıran koyar)")
	}

	// Taban yok → ×N yok.
	c.BaseOK = false
	if nb := c.Block(traceChainException{}); strings.Contains(nb, "×") || strings.Contains(nb, "p50") {
		t.Errorf("tabansız blokta ×N:\n%s", nb)
	}

	// Halkanın kendi exception event'i log yedeğini yener.
	spans := chOperatorTrace()
	spans[1].Events = []any{evExceptionEvent("java.lang.IllegalStateException", "KPS yanıtı boş", "")}
	eb := traceErrorChainOf(spans).Block(traceChainLogException(npe, stack))
	if !strings.Contains(eb, "Zincirin ardından kaydedilen exception: bsa-kps-prod · java.lang.IllegalStateException: KPS yanıtı boş (span event)") {
		t.Errorf("span event exception satırı yok:\n%s", eb)
	}

	// Uzun zincir: orta atlanır, gerçek numaralar korunur.
	long := []chstore.SpanRow{chSpan("h00", "", "svc", "root", "server", 0, 1000, true)}
	for i := 1; i < 12; i++ {
		long = append(long, chSpan(fmt.Sprintf("h%02d", i), fmt.Sprintf("h%02d", i-1), "svc", fmt.Sprintf("op%d", i), "internal", int64(i), 900, true))
	}
	lb := traceErrorChainOf(long).Block(traceChainException{})
	if !strings.Contains(lb, "… 4 ara halka atlandı") || !strings.Contains(lb, "12. [kök] svc · SERVER · root") || strings.Count(lb, "\n") != 9 {
		t.Errorf("uzun zincir kısaltması:\n%s", lb)
	}
	if n := len([]rune(lb)); n > traceChainBlockMaxRunes+1 {
		t.Errorf("blok tavanı aşıldı: %d", n)
	}
}

// stackFromLogForTest — üretim yolundaki stack araması (stackparse.FromLog).
func stackFromLogForTest(lg *logstore.LogRecord) (string, bool) {
	return stackparse.FromLog(lg.Attributes, lg.Body)
}

func TestTraceChainLogException(t *testing.T) {
	for _, tc := range []struct {
		name         string
		attrs        map[string]string
		body, stack  string
		wantT, wantM string
	}{
		{"attr öncelikli", map[string]string{"exception.type": "a.B", "exception.message": "m1"}, "gövde", "a.B: m2\n\tat x.y(Z.java:1)", "a.B", "m1"},
		{"stack ilk satırı", nil, "gövde", "java.lang.NullPointerException: boş\n\tat x.y(Z.java:1)", "java.lang.NullPointerException", "boş"},
		{"mesajsız stack → gövde başı", nil, "İşlem hatası", "java.lang.NullPointerException\n\tat x.y(Z.java:1)", "java.lang.NullPointerException", "İşlem hatası"},
		{"serbest ilk satır tip değildir", nil, "gövde", "bir şey ters gitti: x", "", "gövde"},
	} {
		ex := traceChainLogException(&logstore.LogRecord{ServiceName: "svc", Attributes: tc.attrs, Body: tc.body}, tc.stack)
		if ex.Type != tc.wantT || ex.Msg != tc.wantM || ex.Source != "log" || ex.Service != "svc" {
			t.Errorf("%s: %+v; tip=%q mesaj=%q", tc.name, ex, tc.wantT, tc.wantM)
		}
	}
	if (traceChainLogException(nil, "x") != traceChainException{}) {
		t.Error("nil log boş dönmeli")
	}
}

func TestPrependUnique(t *testing.T) {
	if got := strings.Join(prependUnique("b", []string{"a", "b", "c"}), ","); got != "b,a,c" {
		t.Errorf("got %s", got)
	}
	if got := strings.Join(prependUnique("", []string{"a"}), ","); got != "a" {
		t.Errorf("boş id: %s", got)
	}
}

// chTempoOperatorTrace — operatör şekli sahte Tempo'dan (http.response.
// status_code attribute'u intValue; Tempo yalnız eski http.status_code'u
// kolona koyar — kod attribute'tan okunmalı).
func chTempoOperatorTrace(t *testing.T) *httptest.Server {
	t.Helper()
	ns := defTestT0.UnixNano()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/traces/"+defTestTrace) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"batches":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"bsa-kps-prod"}}]},
 "scopeSpans":[{"spans":[
  {"traceId":%[1]q,"spanId":%[2]q,"name":"POST /BSAWEB/kps/kimlik","kind":2,"startTimeUnixNano":"%[5]d","endTimeUnixNano":"%[6]d","status":{"code":2}},
  {"traceId":%[1]q,"spanId":%[3]q,"parentSpanId":%[2]q,"name":"KpsApsV2Util.sorgula","kind":1,"startTimeUnixNano":"%[7]d","endTimeUnixNano":"%[8]d","status":{"code":2}},
  {"traceId":%[1]q,"spanId":%[4]q,"parentSpanId":%[3]q,"name":"POST","kind":3,"startTimeUnixNano":"%[9]d","endTimeUnixNano":"%[10]d","status":{"code":2},
   "attributes":[{"key":"url.full","value":{"stringValue":%[11]q}},{"key":"http.response.status_code","value":{"intValue":"500"}}]}
 ]}]}]}`,
			defTestTrace, chRootID, chUtilID, chClientID,
			ns, ns+120_000_000, ns+5_000_000, ns+105_000_000, ns+10_000_000, ns+60_000_000, chOSBURL)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestExplainTraceCarriesErrorChain(t *testing.T) {
	p := newDefProvider(t, defAnswerA, defAnswerB)
	s := defServer(t, p)
	s.tempo = tempo.New()
	s.tempo.Configure(tempo.Settings{Enabled: true, BaseURL: chTempoOperatorTrace(t).URL})
	w := defCall(s, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var first map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatalf("yanıt JSON değil: %v", err)
	}
	sys, user := p.lastMessages(t)
	if !strings.HasPrefix(user, "HATA ZİNCİRİ") {
		t.Errorf("user istemi HATA ZİNCİRİ ile başlamıyor:\n%.300s", user)
	}
	for _, want := range []string{"HTTP 500", "POST osb.example.test → /TibcoESB/KPS/KimlikSorgula", "[hata kaynağı]", "Trace " + defTestTrace + " with 3 spans"} {
		if !strings.Contains(user, want) {
			t.Errorf("kanıt %q içermiyor:\n%s", want, user)
		}
	}
	if !strings.Contains(sys, "CAUSAL CHAIN OUTRANKS THE SYMPTOM") {
		t.Error("sistem istemi zincir kuralını taşımıyor")
	}
	ev, _ := first["evidenceSpanIds"].([]any)
	if len(ev) == 0 || ev[0] != chClientID {
		t.Errorf("evidenceSpanIds = %v; hata kaynağı (%s) önce kutulanmalı", ev, chClientID)
	}
	in, err := s.buildTraceExplainInput(context.Background(), defTestTrace)
	if err != nil {
		t.Fatal(err)
	}
	if in.ErrorOrigin != chClientID || len(in.Evidence) == 0 || in.Evidence[0] != chClientID {
		t.Errorf("ErrorOrigin=%q Evidence=%v", in.ErrorOrigin, in.Evidence)
	}
}

func TestTraceFollowUpPromptCarriesChain(t *testing.T) {
	s := &Server{}
	s.tempo = tempo.New()
	s.tempo.Configure(tempo.Settings{Enabled: true, BaseURL: chTempoOperatorTrace(t).URL})
	events, chain, _ := s.traceFollowUpEvents(context.Background(), defTestTrace)
	if events != "" {
		t.Errorf("event'siz trace'te events bloğu: %q", events)
	}
	if !strings.HasPrefix(chain, "\n\n"+traceChainHeader) || !strings.Contains(chain, "HTTP 500") {
		t.Fatalf("takip zinciri:\n%q", chain)
	}
	if strings.Contains(chain, "p50") || strings.Contains(chain, "×") {
		t.Errorf("takip turu taban okumaz:\n%s", chain)
	}

	base := traceFollowUp{TraceID: tfTrace, Env: "prod"}
	withCh := base
	withCh.Chain = chain
	withCh.Events = buildTraceEventDigest([]chstore.SpanRow{evSpan("aaaaaaaaaaaaaaa1", 1, 12, true,
		evExceptionEvent(evTopicExType, evTopicExMsg, evKafkaStack))}).Block()
	plain, rich := traceFollowUpPromptTR(&base, "önceki"), traceFollowUpPromptTR(&withCh, "önceki")
	if strings.Contains(plain, "HATA ZİNCİRİ —") {
		t.Fatal("Chain boşken blok yazıldı")
	}
	iAdd, iCh, iEv, iEx := strings.Index(rich, "TRACE İNCELEMESİ SÜRÜYOR"), strings.Index(rich, traceChainHeader), strings.Index(rich, traceEventsBlockHeader), strings.Index(rich, traceFollowUpExplainHeader)
	if !(iAdd >= 0 && iAdd < iCh && iCh < iEv && iEv < iEx) {
		t.Errorf("sıra: talimat=%d zincir=%d event=%d açıklama=%d", iAdd, iCh, iEv, iEx)
	}
	if seed := traceFollowUpEvidenceSeed([]string{traceFollowUpPromptTR(&withCh, "")}, nil); !strings.Contains(seed, "HTTP 500") {
		t.Error("sayı tohumu zinciri taşımıyor")
	}
	if got := traceFollowUpChipTR(withCh); !strings.Contains(got, "hata zinciri") {
		t.Errorf("çip zinciri söylemiyor: %q", got)
	}
	if got := traceFollowUpChipTR(base); strings.Contains(got, "hata zinciri") {
		t.Errorf("zincirsiz çip değişti: %q", got)
	}
}
