package api

// v0.10.1327 (operator-reported): "bsa-loan-legalproceeding-prod neden yavaş"
// SocketTimeoutException dedi ama NEREDE demedi; takip "nereye giderken
// socketimeoutexception almış" Log kademesine düşüp "son 500 hata logunda yok"
// dedi. En yavaş trace anında gösteriyordu: kök SERVER 50.02 s, çocuk CLIENT
// "POST <host>" 50015 ms öz süre %100, HTTP 500, url.full …/DosyaListesiSorgulama
// ?wsdl, 24 sa p50 13 ms → ×3825, kaynak Tempo. Saf çekirdeğin pinleri; CH /
// Tempo / LLM yok.

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	tdT0      = int64(1_790_000_000_000_000_000)
	tdTrace1  = "4bf92f3577b34da6a3ce929d0e0e4736"
	tdTrace2  = "0af7651916cd43dd8448eb211c80319c"
	tdSvc     = "bsa-loan-legalproceeding-prod"
	tdHost    = "osbprod.bank.example"
	tdURLFull = "http://osbprod.bank.example/TibcoESB/ExternalXServices/Uyap/DosyaListesiSorgulama?wsdl"
	tdPath    = "/TibcoESB/ExternalXServices/Uyap/DosyaListesiSorgulama"
)

func tdMs(ms float64) int64 { return int64(ms * 1e6) }

func tdSpan(id, parent, name, kind string, startMs, durMs float64) chstore.SpanRow {
	return chstore.SpanRow{TraceID: tdTrace1, SpanID: id, ParentSpanID: parent, Name: name, Kind: kind, ServiceName: tdSvc,
		StartTime: tdT0 + tdMs(startMs), EndTime: tdT0 + tdMs(startMs+durMs), DurationMs: durMs, StatusCode: "unset"}
}

// tdProdSpans — prod vakası: kök SERVER 50.02 s, çocuk CLIENT 50015 ms.
func tdProdSpans() []chstore.SpanRow {
	root := tdSpan("a1", "", "POST /BSAWEB/legalproceeding/dosya", "server", 0, 50020)
	root.StatusCode = "error"
	cl := tdSpan("b2", "a1", "POST", "client", 2, 50015)
	cl.StatusCode = "error"
	cl.ScopeName = "io.opentelemetry.http-url-connection"
	cl.Attributes = map[string]string{"http.response.status_code": "500", "server.address": tdHost, "url.full": tdURLFull}
	cl.Events = []any{map[string]any{"name": "exception", "attributes": map[string]any{
		"exception.type": "java.net.SocketTimeoutException", "exception.message": "Read timed out"}}}
	return []chstore.SpanRow{root, cl}
}

func TestDominantSpan(t *testing.T) {
	t.Run("prod: CLIENT çocuk öz sürenin tamamı", func(t *testing.T) {
		sp := tdProdSpans()
		p := dominantSpan(sp, chstore.BuildTraceAnalysis(sp, false))
		if !p.OK || p.Span.SpanID != "b2" {
			t.Fatalf("baskın span b2 olmalı: %+v", p)
		}
		if p.SelfNs != tdMs(50015) || p.WallNs != tdMs(50020) || p.Depth != 1 {
			t.Errorf("öz/duvar/derinlik: %d %d %d", p.SelfNs, p.WallNs, p.Depth)
		}
		if p.Share < 0.999 || p.Share > 1 {
			t.Errorf("pay ≈1 olmalı: %v", p.Share)
		}
	})
	t.Run("paralel çocuklar: yalnız kritik yol aday", func(t *testing.T) {
		// Kök 100 ms; A 0-90 (kritik), B 5-65 paralel (yolda değil).
		sp := []chstore.SpanRow{
			tdSpan("r", "", "GET /x", "server", 0, 100),
			tdSpan("a", "r", "GET a", "client", 0, 90),
			tdSpan("b", "r", "GET b", "client", 5, 60),
		}
		p := dominantSpan(sp, chstore.BuildTraceAnalysis(sp, false))
		if p.Span.SpanID != "a" || p.SelfNs != tdMs(90) {
			t.Fatalf("a (90 ms) olmalı: %s %d", p.Span.SpanID, p.SelfNs)
		}
	})
	t.Run("eşit öz süre: hata durumu kazanır", func(t *testing.T) {
		sp := []chstore.SpanRow{tdSpan("r", "", "GET /x", "server", 0, 100), tdSpan("c", "r", "work", "internal", 0, 50)}
		sp[1].StatusCode = "error"
		if p := dominantSpan(sp, chstore.BuildTraceAnalysis(sp, false)); p.Span.SpanID != "c" {
			t.Fatalf("hatalı c kazanmalı: %s", p.Span.SpanID)
		}
	})
	t.Run("eşit öz süre, hata yok: CLIENT kazanır", func(t *testing.T) {
		sp := []chstore.SpanRow{tdSpan("r", "", "GET /x", "server", 0, 100), tdSpan("c", "r", "GET y", "client", 0, 50)}
		if p := dominantSpan(sp, chstore.BuildTraceAnalysis(sp, false)); p.Span.SpanID != "c" {
			t.Fatalf("CLIENT c kazanmalı: %s", p.Span.SpanID)
		}
	})
	t.Run("tek span'li trace", func(t *testing.T) {
		sp := []chstore.SpanRow{tdSpan("r", "", "GET /x", "server", 0, 40)}
		p := dominantSpan(sp, chstore.BuildTraceAnalysis(sp, false))
		if !p.OK || p.Span.SpanID != "r" || p.Share != 1 {
			t.Fatalf("kök kendisi, pay 1: %+v", p)
		}
	})
	t.Run("boş trace", func(t *testing.T) {
		if p := dominantSpan(nil, chstore.BuildTraceAnalysis(nil, false)); p.OK {
			t.Fatal("boş trace OK=false olmalı")
		}
	})
}

func TestPeerOfPrecedence(t *testing.T) {
	sp := func(name string, attrs map[string]string) chstore.SpanRow {
		return chstore.SpanRow{Name: name, Attributes: attrs}
	}
	withPeer := sp("POST", map[string]string{"server.address": tdHost})
	withPeer.PeerService = "ledger"
	withDB := sp("SELECT orders", map[string]string{"db.name": "orders"})
	withDB.DBSystem = "postgresql"
	cases := []struct {
		name string
		in   chstore.SpanRow
		want peerRef
	}{
		{"server.address + url.full yolu (sorgu atılır)", sp("POST", map[string]string{"server.address": tdHost, "url.full": tdURLFull}),
			peerRef{Kind: "http", Target: tdHost, Path: tdPath}},
		{"peer.service kolonu her şeyden önce", withPeer, peerRef{Kind: "peer.service", Target: "ledger"}},
		{"peer.service attribute", sp("GET", map[string]string{"peer.service": "auth", "net.peer.name": "h1"}), peerRef{Kind: "peer.service", Target: "auth"}},
		{"net.peer.name (eski semconv)", sp("GET", map[string]string{"net.peer.name": "legacy.host"}), peerRef{Kind: "http", Target: "legacy.host"}},
		{"yalnız url.full → host url'den, fragman atılır", sp("GET", map[string]string{"url.full": "https://api.example:8443/v1/x?q=1#f"}),
			peerRef{Kind: "http", Target: "api.example", Path: "/v1/x"}},
		{"db.system + db.name (kolon)", withDB, peerRef{Kind: "db", Target: "postgresql/orders"}},
		{"db.system.name + db.namespace (yeni semconv)", sp("q", map[string]string{"db.system.name": "oracle", "db.namespace": "CORE"}),
			peerRef{Kind: "db", Target: "oracle/CORE"}},
		{"messaging.system + hedef", sp("publish", map[string]string{"messaging.system": "kafka", "messaging.destination.name": "loans"}),
			peerRef{Kind: "messaging", Target: "kafka/loans"}},
		{"hiçbiri → span adı", sp("compute", nil), peerRef{Kind: "name", Target: "compute"}},
	}
	for _, c := range cases {
		if got := peerOf(c.in); got != c.want {
			t.Errorf("%s: %+v, beklenen %+v", c.name, got, c.want)
		}
	}
}

func TestSpanFailure(t *testing.T) {
	prod := tdProdSpans()[1]
	if f := spanFailure(prod); f.HTTPCode != 500 || f.ExType != "java.net.SocketTimeoutException" || f.ExMsg != "Read timed out" || f.Status != "error" {
		t.Errorf("prod: %+v", f)
	}
	col := chstore.SpanRow{StatusCode: "error", HTTPStatus: 503, StatusMessage: "upstream reset"}
	if f := spanFailure(col); f.HTTPCode != 503 || f.ExType != "" || f.ExMsg != "upstream reset" {
		t.Errorf("kolon + status mesajı: %+v", f)
	}
	legacy := chstore.SpanRow{StatusCode: "unset", Attributes: map[string]string{"http.status_code": "404"}, StatusMessage: "ignored"}
	if f := spanFailure(legacy); f.HTTPCode != 404 || f.ExMsg != "" {
		t.Errorf("eski semconv kodu, hata değil → mesaj yok: %+v", f)
	}
	str := chstore.SpanRow{Events: `[{"name":"exception","attributes":{"exception.type":"X","exception.message":"m"}}]`}
	if f := spanFailure(str); f.ExType != "X" || f.ExMsg != "m" {
		t.Errorf("ham JSON event: %+v", f)
	}
}

func tdProdDrill(id, label string) traceDrill {
	sp := tdProdSpans()
	for i := range sp {
		sp[i].TraceID = id
	}
	p := dominantSpan(sp, chstore.BuildTraceAnalysis(sp, false))
	return traceDrill{TraceID: id, Label: label, Source: traceSourceTempo, Pick: p, Peer: peerOf(p.Span), Fail: spanFailure(p.Span),
		BaseP50Ms: 13.0758, BaseOK: true, RootSvc: tdSvc}
}

func TestRenderTraceDrillTRGolden(t *testing.T) {
	d := tdProdDrill(tdTrace1, "en yavaş")
	want := "50.02 s'nin 50.0 s'i (%100) CLIENT çağrısında: POST " + tdHost + " → " + tdPath +
		" (HTTP 500, java.net.SocketTimeoutException: Read timed out); bu operasyonun 24 sa p50'si 13 ms (×3825). Trace: " + tdTrace1 + " (kaynak: Tempo)"
	if got := renderTraceDrillTR(d); got != want {
		t.Fatalf("golden:\n got %s\nwant %s", got, want)
	}
	// Span adı host'u zaten taşıyorsa tekrar yazılmaz.
	d2 := d
	d2.Pick.Span.Name = "POST " + tdHost
	if got := renderTraceDrillTR(d2); !strings.Contains(got, "çağrısında: POST "+tdHost+" → "+tdPath+" (") {
		t.Errorf("host çift yazılmamalı: %s", got)
	}
	// Taban yoksa ×N yok; okunamadıysa söylenir.
	d3 := d
	d3.BaseOK, d3.BaseP50Ms = false, 0
	if got := renderTraceDrillTR(d3); strings.Contains(got, "×") || strings.Contains(got, "p50") {
		t.Errorf("taban yokken ×N/p50 yazılmamalı: %s", got)
	}
	d4 := d
	d4.BaseOK, d4.BaseErr = false, errors.New("code: 159, timeout")
	if got := renderTraceDrillTR(d4); !strings.Contains(got, "p50'si OKUNAMADI") || strings.Contains(got, "×") {
		t.Errorf("taban hatası söylenmeli: %s", got)
	}
	// Okuma hatası ≠ bulunamadı.
	if got := renderTraceDrillTR(traceDrill{TraceID: tdTrace1, Err: errors.New("dial tcp: connection refused")}); !strings.Contains(got, "OKUNAMADI") {
		t.Errorf("okuma hatası: %s", got)
	}
	if got := renderTraceDrillTR(traceDrill{TraceID: tdTrace1, NotFound: true}); !strings.Contains(got, "BULUNAMADI") {
		t.Errorf("bulunamadı: %s", got)
	}
	// Baskın span başka servisteyse adı yazılır.
	d5 := d
	d5.Pick.Span.ServiceName = "osb-gateway"
	if got := renderTraceDrillTR(d5); !strings.Contains(got, "[servis: osb-gateway]") {
		t.Errorf("başka servis adı yazılmalı: %s", got)
	}
	// v0.10.1373 — şekil doluysa satırın sonuna eklenir (Spans==0 → bayt bayt eski).
	d6 := d
	d6.Shape = traceShapeStats{Spans: 148, WallNs: 2_300_000_000, DB: shapeShare{Count: 92, SumNs: 1_800_000_000, Share: 0.78},
		Repeats: []shapeRepeat{{Name: "SELECT orders.order_item", Count: 46, AvgNs: 19_000_000}, {Name: "GET /x", Count: 12, AvgNs: 35_000_000}}}
	if got := renderTraceDrillTR(d6); got != want+". Şekil: 148 span · DB 92 span / 1,8 s (%78) · tekrar: SELECT orders.order_item ×46 (ort 19 ms), GET /x ×12 (ort 35 ms)" {
		t.Errorf("şekil golden:\n got %s", got)
	}
}

func TestRenderTraceDrillsTRSharedTarget(t *testing.T) {
	out := renderTraceDrillsTR([]traceDrill{tdProdDrill(tdTrace1, "en yavaş"), tdProdDrill(tdTrace2, "en yavaş hatalı")})
	for _, w := range []string{
		"- en yavaş: 50.02 s'nin 50.0 s'i (%100) CLIENT çağrısında",
		"- en yavaş hatalı: 50.02 s'nin",
		"ortak hedef: " + tdHost + " (2/2)",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("%q yok:\n%s", w, out)
		}
	}
	one := renderTraceDrillsTR([]traceDrill{tdProdDrill(tdTrace1, "en yavaş"), {TraceID: tdTrace2, Label: "2. en yavaş", NotFound: true}})
	if strings.Contains(one, "ortak hedef") {
		t.Errorf("tek başarılı kırılımda ortak hedef yazılmamalı:\n%s", one)
	}
	if renderTraceDrillsTR(nil) != "" {
		t.Error("boş girdi boş metin")
	}
}

func TestPickDrillTraces(t *testing.T) {
	r := func(id string, ms float64, errored bool) chstore.TraceRow {
		return chstore.TraceRow{TraceID: id, DurationMs: ms, HasError: errored}
	}
	cases := []struct {
		name string
		rows []chstore.TraceRow
		want []string
	}{
		{"boş", nil, nil},
		{"tek", []chstore.TraceRow{r("a", 10, false)}, []string{"a:en yavaş"}},
		{"en yavaş + en yavaş hatalı", []chstore.TraceRow{r("a", 900, false), r("b", 800, false), r("c", 700, true)}, []string{"a:en yavaş", "c:en yavaş hatalı"}},
		{"en yavaş zaten hatalı → 2. en yavaş", []chstore.TraceRow{r("a", 900, true), r("b", 800, true)}, []string{"a:en yavaş", "b:2. en yavaş"}},
		{"sırasız girdi sıralanır", []chstore.TraceRow{r("b", 100, false), r("a", 900, false)}, []string{"a:en yavaş", "b:2. en yavaş"}},
		{"aynı id tekrarlanmaz", []chstore.TraceRow{r("a", 900, false), r("a", 900, true)}, []string{"a:en yavaş"}},
	}
	for _, c := range cases {
		var got []string
		for _, p := range pickDrillTraces(c.rows) {
			got = append(got, p.Row.TraceID+":"+p.Label)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: %v, beklenen %v", c.name, got, c.want)
		}
	}
}

func TestGroupTraceLogsCapsAndOrder(t *testing.T) {
	var logs []*logstore.LogRecord
	for i := 0; i < 12; i++ {
		sev := uint8(9)
		if i%4 == 0 {
			sev = 17
		}
		logs = append(logs, &logstore.LogRecord{TraceID: tdTrace1, Severity: sev, Timestamp: tdT0 + int64(12-i)*1e9, Body: "l"})
	}
	logs = append(logs,
		&logstore.LogRecord{TraceID: strings.ToUpper(tdTrace2), Severity: 13, Timestamp: tdT0},
		&logstore.LogRecord{TraceID: "ffffffffffffffffffffffffffffffff", Severity: 21, Timestamp: tdT0},
		nil)
	g := groupTraceLogs(logs, []string{tdTrace1, tdTrace2}, 8)
	if len(g) != 2 || len(g[0]) != 8 || len(g[1]) != 1 {
		t.Fatalf("grup boyları: %d %d %d", len(g), len(g[0]), len(g[1]))
	}
	for i := 1; i < len(g[0]); i++ {
		a, b := g[0][i-1], g[0][i]
		if a.Severity < b.Severity || (a.Severity == b.Severity && a.Timestamp > b.Timestamp) {
			t.Fatalf("sıra şiddet desc, sonra zaman asc olmalı: %d", i)
		}
	}
	if g[0][0].Severity != 17 || g[0][3].Severity != 9 {
		t.Errorf("üç ERROR önce: %d %d", g[0][0].Severity, g[0][3].Severity)
	}
}

func TestRenderTraceDrillLogsTR(t *testing.T) {
	ids := []string{tdTrace1, tdTrace2}
	if got := renderTraceDrillLogsTR(true, nil, nil, ids); !strings.Contains(got, "log deposu yapılandırılmamış") {
		t.Errorf("depo yok: %s", got)
	}
	if got := renderTraceDrillLogsTR(false, nil, logstore.ErrBackendSlow, ids); !strings.Contains(got, "Loglar OKUNAMADI (") || strings.Contains(got, "YOK.") {
		t.Errorf("okunamadı ≠ yok: %s", got)
	}
	page := &logstore.Page{Logs: []*logstore.LogRecord{{
		TraceID: tdTrace1, Severity: 17, SeverityText: "error", ServiceName: tdSvc, Timestamp: time.Date(2026, 10, 17, 10, 42, 1, 0, time.UTC).UnixNano(),
		Body: "UYAP call failed\n  after 50000 ms", Attributes: map[string]string{"exception.type": "java.net.SocketTimeoutException"}}}}
	got := renderTraceDrillLogsTR(false, page, nil, ids)
	wantLine := "KANIT: log 10:42:01 ERROR " + tdSvc + " UYAP call failed after 50000 ms [exception.type=java.net.SocketTimeoutException] (trace 4bf92f3577b3…)"
	if !strings.Contains(got, wantLine) {
		t.Errorf("log satırı:\n%s\nbeklenen satır:\n%s", got, wantLine)
	}
	if !strings.Contains(got, "- trace 0af7651916cd…: bu trace'e bağlı log kaydı YOK.") {
		t.Errorf("logsuz trace söylenmeli:\n%s", got)
	}
	long := &logstore.LogRecord{TraceID: tdTrace1, Body: strings.Repeat("ş", 300)}
	if line := renderTraceLogLineTR(long); strings.Count(line, "ş") != traceDrillLogBodyRunes {
		t.Errorf("gövde 200 rune'a kesilmeli: %d", strings.Count(line, "ş"))
	}
}

func TestHasPeerSignal(t *testing.T) {
	cases := map[string]bool{
		"nereye giderken socketimeoutexception almış": true,
		"hangi servise giderken timeout":              true,
		"hangi hedefe gidiyor":                        true,
		"downstream hangisi":                          true,
		"kime gidiyor":                                true,
		"hangi url'de takılıyor":                      true,
		"hangi endpoint'e istek atıyor":               true,
		"loglarda hata var mı":                        false,
		"hangi servisler yavaş":                       false,
		"payments sağlığı nasıl":                      false,
	}
	for q, want := range cases {
		if got := hasPeerSignal(guidedTokens(normalizeGuidedMsg(q))); got != want {
			t.Errorf("%q: %v, beklenen %v", q, got, want)
		}
	}
}

func TestPrevTurnMentionsLatencyFailure(t *testing.T) {
	u := func(s string) copilot.ChatMessage { return copilot.ChatMessage{Role: "user", Text: s} }
	a := func(s string) copilot.ChatMessage { return copilot.ChatMessage{Role: "assistant", Text: s} }
	cur := u("nereye giderken socketimeoutexception almış")
	cases := []struct {
		name string
		msgs []copilot.ChatMessage
		last guidedIntent
		want bool
	}{
		{"geçmiş yok", []copilot.ChatMessage{cur}, "", false},
		{"asistan SocketTimeoutException dedi", []copilot.ChatMessage{u("x"), a("Kök neden: java.net.SocketTimeoutException artışı"), cur}, "", true},
		{"önceki soru 'neden yavaş'", []copilot.ChatMessage{u(tdSvc + " neden yavaş"), a("Özet hazır."), cur}, "", true},
		{"önceki tur alakasız", []copilot.ChatMessage{u("takımımın servisleri"), a("3 servis listelendi."), cur}, "", false},
		{"yalnız EN YAKIN tur sayılır", []copilot.ChatMessage{u("neden yavaş"), a("timeout"), u("deploy oldu mu"), a("Son 1 saatte deploy yok."), cur}, "", false},
		{"önceki rota kök-neden", []copilot.ChatMessage{u("x"), a("y"), cur}, guidedRootCause, true},
		{"önceki rota yavaş trace", []copilot.ChatMessage{cur}, guidedSlowTraces, true},
		{"aktif soru kendisi sayılmaz", []copilot.ChatMessage{u("takımım"), a("liste"), u("nereye giderken timeout almış")}, "", false},
	}
	for _, c := range cases {
		if got := prevTurnMentionsLatencyFailure(c.msgs, c.last); got != c.want {
			t.Errorf("%s: %v, beklenen %v", c.name, got, c.want)
		}
	}
}

// tdRouteWithHistory — copilotChatGuided'ın saf eşleniği: router → geçmişli
// trace_drill ezmesi (servis sorudan ya da önceki sorudan).
func tdRouteWithHistory(q string, msgs []copilot.ChatMessage, svcs []string) guidedRoute {
	envs := []string{"prod"}
	r := routeGuidedIntent(q, svcs, envs, nil, "")
	if dr, ok := traceDrillRoute(r, normalizeGuidedMsg(q), latestPriorService(priorUserTexts(msgs), svcs, envs), prevTurnMentionsLatencyFailure(msgs, "")); ok {
		return dr
	}
	return r
}

func TestTraceDrillRouterTable(t *testing.T) {
	svcs := []string{tdSvc, "payments", "checkout-service"}
	u := func(s string) copilot.ChatMessage { return copilot.ChatMessage{Role: "user", Text: s} }
	a := func(s string) copilot.ChatMessage { return copilot.ChatMessage{Role: "assistant", Text: s} }
	hist := []copilot.ChatMessage{u(tdSvc + " neden yavaş"), a("Yavaşlık SocketTimeoutException kaynaklı görünüyor.")}
	with := func(q string) []copilot.ChatMessage { return append(append([]copilot.ChatMessage(nil), hist...), u(q)) }
	cases := []struct {
		name, q string
		msgs    []copilot.ChatMessage
		intent  guidedIntent
		service string
	}{
		{"prod takip + geçmiş → trace_drill (servis devralınır)", "nereye giderken socketimeoutexception almış", with("nereye giderken socketimeoutexception almış"), guidedTraceDrill, tdSvc},
		{"aynı soru geçmişsiz → bugünkü rota", "nereye giderken socketimeoutexception almış", []copilot.ChatMessage{u("nereye giderken socketimeoutexception almış")},
			routeGuidedIntent("nereye giderken socketimeoutexception almış", svcs, []string{"prod"}, nil, "").Intent, ""},
		{"log hata sorusu geçmişle de değişmez", "loglarda hata var mı", with("loglarda hata var mı"), guidedLogErrors, ""},
		{"servisli log hedef sorusu + geçmiş → trace_drill", "payments hangi servise giderken hata alıyor loglarda", with("payments hangi servise giderken hata alıyor loglarda"), guidedTraceDrill, "payments"},
		{"yavaş-trace rotası ezilmez (zaten kırılım taşır)", tdSvc + " en yavaş trace'i nereye gidiyor?", with(tdSvc + " en yavaş trace'i nereye gidiyor?"), guidedSlowTraces, tdSvc},
	}
	for _, c := range cases {
		got := tdRouteWithHistory(c.q, c.msgs, svcs)
		if got.Intent != c.intent || got.Service != c.service {
			t.Errorf("%s: %s/%q, beklenen %s/%q", c.name, got.Intent, got.Service, c.intent, c.service)
		}
	}
	// Servis hiçbir yerden gelmiyorsa ezme yok.
	if _, ok := traceDrillRoute(guidedRoute{}, normalizeGuidedMsg("nereye giderken timeout"), "", true); ok {
		t.Error("servissiz ezme olmamalı")
	}
}

func TestIntentTraceDrillUpgrade(t *testing.T) {
	r := intentTraceDrillUpgrade(guidedRoute{Intent: guidedLogErrors, Service: "payments", Env: "prod"}, "payments hangi servise giderken hata alıyor")
	if r.Intent != guidedTraceDrill || r.Service != "payments" || r.Env != "prod" {
		t.Errorf("yükseltme: %+v", r)
	}
	if r := intentTraceDrillUpgrade(guidedRoute{Intent: guidedLogErrors, Service: "payments"}, "payments loglarında hata var mı"); r.Intent != guidedLogErrors {
		t.Errorf("hedef sinyalsiz değişmemeli: %s", r.Intent)
	}
	if r := intentTraceDrillUpgrade(guidedRoute{Intent: guidedLogErrors}, "nereye giderken hata"); r.Intent != guidedLogErrors {
		t.Errorf("servissiz değişmemeli: %s", r.Intent)
	}
	if r := intentTraceDrillUpgrade(guidedRoute{Intent: guidedServiceHealth, Service: "payments"}, "payments nereye gidiyor"); r.Intent != guidedServiceHealth {
		t.Errorf("yalnız log_errors yükselir: %s", r.Intent)
	}
}

func TestRootCauseWantsTraceDrill(t *testing.T) {
	cases := []struct {
		name      string
		cur, base aiRED
		err       error
		slow      bool
		want      bool
	}{
		{"soru yavaşlık soruyor", aiRED{}, aiRED{}, nil, true, true},
		{"p99 ×2 yükseldi", aiRED{Spans: 10, P99Ms: 400}, aiRED{P99Ms: 200}, nil, false, true},
		{"p99 sakin", aiRED{Spans: 10, P99Ms: 210}, aiRED{P99Ms: 200}, nil, false, false},
		{"taban yok", aiRED{Spans: 10, P99Ms: 900}, aiRED{}, nil, false, false},
		{"RED okunamadı", aiRED{Spans: 10, P99Ms: 900}, aiRED{P99Ms: 10}, errors.New("x"), false, false},
	}
	for _, c := range cases {
		if got := rootCauseWantsTraceDrill(c.cur, c.base, c.err, c.slow); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	for q, want := range map[string]bool{
		tdSvc + " neden yavaş":              true,
		"neden timeout alıyoruz":            true,
		"neden socketimeoutexception almış": true,
		"zaman aşımı neden oluyor":          true,
		"payments neden patladı":            false,
	} {
		if got := hasLatencyAsk(normalizeGuidedMsg(q)); got != want {
			t.Errorf("hasLatencyAsk(%q)=%v", q, got)
		}
	}
}

// Kablolama pinleri: yavaş-trace ve kök-neden demetleri kırılımı taşır, kök-neden
// kaynak etiketi kırılımı söyler, sıfır-maliyet kapısı hedef sorusunu geçirir,
// sınıflandırıcı yolu yükseltmeyi uygular.
func TestTraceDrillWiring(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	g := read("copilot_guided.go")
	fn := func(name string) string {
		i := strings.Index(g, "func (s *Server) "+name+"(")
		if i < 0 {
			t.Fatalf("%s yok", name)
		}
		return g[i : i+strings.Index(g[i:], "\nfunc ")]
	}
	if !strings.Contains(fn("guidedSlowTracesBundle"), "s.traceDrillEvidence(ctx, emit, pickDrillTraces(rows), service, from, to)") {
		t.Error("yavaş-trace demeti kırılımı aynı listeden kurmalı")
	}
	rc := fn("guidedRootCauseBundle")
	for _, w := range []string{"rootCauseWantsTraceDrill(cx.Current, cx.Baseline, cx.curErr, slowAsk)", "s.slowTraceDrill(ctx, emit, service, env, from, to)", `" + en yavaş trace kritik yolu + o trace'lerin logları"`} {
		if !strings.Contains(rc, w) {
			t.Errorf("kök-neden demeti %q taşımalı", w)
		}
	}
	if a, d := strings.Index(rc, `emitGuidedStep(emit, "log_patterns"`), strings.Index(rc, "rootCauseWantsTraceDrill("); a < 0 || d < a {
		t.Error("kırılım log desenlerinden SONRA olmalı")
	}
	if !strings.Contains(g, "case guidedTraceDrill:") || !strings.Contains(g, "s.guidedTraceDrillOverride(ctx, route, norm, msgs, prior, ctxService, svcNames, envNames)") {
		t.Error("trace_drill dispatch + geçmişli ezme kablolu olmalı")
	}
	if !strings.Contains(read("copilot_intent.go"), "route = intentTraceDrillUpgrade(route, question)") {
		t.Error("sınıflandırıcı yolu log_errors+hedef yükseltmesini uygulamalı")
	}
	d := read("chat_trace_drill.go")
	for _, w := range []string{"s.resolveTraceSpans(ctx, c.Row.TraceID)", "logstore.SearchWithTimeout(dctx, s.logs, logstore.Filter{TraceIDs: ids", "context.WithTimeout(ctx, traceDrillBudget)", "s.store.SpanOperationRED("} {
		if !strings.Contains(d, w) {
			t.Errorf("kırılım %q taşımalı", w)
		}
	}
	if strings.Contains(d, "COREMETRY_LOGS_BACKEND") || strings.Contains(d, "os.Getenv") {
		t.Error("log arka ucu env'den tahmin edilmemeli (s.logs yapılandırılmış depo)")
	}
	// Goroutine emit etmez: go func gövdesinde emit yok.
	gi := strings.Index(d, "go func(i int, c drillCandidate) {")
	if gi < 0 {
		t.Fatal("paralel kırılım goroutine'i yok")
	}
	if body := d[gi : gi+strings.Index(d[gi:], "}(i, c)")]; strings.Contains(body, "emit") {
		t.Error("goroutine emit etmemeli")
	}
}

// Takip çipi: yavaş-trace ve kök-neden sonrası "nereye gidiyor" çipi var ve
// router'da KENDİ BAŞINA yönlenir (geçmişsiz: slow_traces, kırılım taşır).
func TestTraceDrillFollowupChip(t *testing.T) {
	for _, r := range []guidedRoute{{Intent: guidedSlowTraces, Service: "payments"}, {Intent: guidedRootCause, Service: "payments"}} {
		var chip string
		for _, s := range guidedSuggestions(r) {
			if strings.Contains(s, "nereye gidiyor") {
				chip = s
			}
		}
		if chip != "payments en yavaş trace'i nereye gidiyor?" {
			t.Fatalf("%s: çip yok: %v", r.Intent, guidedSuggestions(r))
		}
		if got := routeGuidedIntent(chip, []string{"payments"}, nil, nil, ""); got.Intent != guidedSlowTraces || got.Service != "payments" {
			t.Errorf("çip %q slow_traces/payments'a yönlenmeli: %s/%s", chip, got.Intent, got.Service)
		}
	}
	for _, s := range guidedSuggestions(guidedRoute{Intent: guidedTraceDrill, Service: "payments"}) {
		if got := routeGuidedIntent(s, []string{"payments"}, nil, nil, ""); got.Intent == guidedNone {
			t.Errorf("trace_drill çipi %q yönlenemiyor", s)
		}
	}
	if links := guidedAnswerLinks(guidedRoute{Intent: guidedTraceDrill, Service: "payments"}, noLinkWindow()); len(links) == 0 {
		t.Error("trace_drill rotası derin link taşımalı")
	}
}

// v0.10.1988 — traceRecords gövdeli kayıt CoSRE kanıt satırında 200 karakterde kesilmez.
func TestTraceDrillLineTraceRecordsBudget(t *testing.T) {
	body := strings.Repeat("Request: {x} ", 60) // ~780 karakter
	lg := &logstore.LogRecord{Body: body, Attributes: map[string]string{"traceRecords": "[...]"}}
	if got := renderTraceLogLineTR(lg); len([]rune(got)) < 700 {
		t.Fatalf("traceRecords kaydı kısa kesildi: %d", len([]rune(got)))
	}
	plain := &logstore.LogRecord{Body: body, Attributes: map[string]string{}}
	if got := renderTraceLogLineTR(plain); len([]rune(got)) > 400 {
		t.Fatalf("düz kayıt tavanı değişmemeli: %d", len([]rune(got)))
	}
}
