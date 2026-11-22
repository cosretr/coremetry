package api

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// v0.10.2449 — router fikstürleri: operatörün 2026-11-22 prod soruları
// (adlar sentetik: orders-api, example.com, OP_ORDER_QUERY_REST) + komşular
// (aynı sözcüğü taşıyıp guided'da kalması gerekenler).
const lkPastedLog = `2026-11-22 10:41:07.123 ERROR [orders-api,4bf92f3577b34da6a3ce929d0e0e4736,00f067aa0ba902b7] c.e.o.OrderClient - call failed
org.springframework.web.client.ResourceAccessException: I/O error on POST request for "https://partner-gw.example.com/v1/orders": Read timed out
	at org.springframework.web.client.RestTemplate.doExecute(RestTemplate.java:785)
Caused by: java.net.SocketTimeoutException: Read timed out`

func TestLookupAskForOperatorQuestions(t *testing.T) {
	cases := []struct {
		q    string
		tool string
		args map[string]string
	}{
		{"bu servisin url'i nedir", mcptools.EndpointLookupName, nil},
		{"orders-api endpoint'i nedir", mcptools.EndpointLookupName, nil},
		{"OP_ORDER_QUERY_REST what is this service exact endpoint url", mcptools.EndpointLookupName, map[string]string{"operation": "OP_ORDER_QUERY_REST"}},
		{"api.example.com bu domainin çözdüğü podu soruyorum 3 cluster var", mcptools.EndpointLookupName, map[string]string{"domain": "api.example.com"}},
		{"timeout'un hangi poddan geldiğini biliyor musun", mcptools.TraceWhereName, map[string]string{"mode": "errors"}},
		{"ocpka veya ocpkb'deki podlardan birine düştüğünü biliyorum hangisi", mcptools.TraceWhereName, nil},
		{"hangi mq'da uzamış", mcptools.MessagingFlowName, nil},
		{"hangi topic'te gecikme var", mcptools.MessagingFlowName, nil},
		{"payments-topic mesajını kim tüketiyor", mcptools.MessagingFlowName, map[string]string{"topic": "payments-topic"}},
		{"kafka consumer lag var mı", mcptools.MessagingFlowName, nil},
		{"orders-api kimi çağırıyor, hangisi yavaş", mcptools.DownstreamCallsName, nil},
		{"orders-api downstream servislerinden hangisi hata veriyor", mcptools.DownstreamCallsName, nil},
		{"orders-api pod içerisinde geçen sürelere baktığımda 200ms göremiyorum", mcptools.TraceWhereName, map[string]string{"mode": "slow"}},
		{"reponun linkini veremez misin", mcptools.RepoLookupName, nil},
		{"şu söylediğim podun reposunu getirir misin", mcptools.RepoLookupName, nil},
		{"yani hata osb'den mi dönmüş [kod: ERR-42]", mcptools.TraceWhereName, map[string]string{"peer": "osb"}},
		{"bir log attım, external call var mı\n" + lkPastedLog, mcptools.TraceWhereName, map[string]string{"trace_id": "4bf92f3577b34da6a3ce929d0e0e4736"}},
		{lkPastedLog, mcptools.TraceWhereName, map[string]string{"trace_id": "4bf92f3577b34da6a3ce929d0e0e4736"}},
		// v0.10.2453 — dependency_path (operatör onaylı ifadeler; adlar sentetik)
		{"orders-api neden yavaş, zincirde hangi bağımlılık", mcptools.DependencyPathName, map[string]string{"direction": "down"}},
		{"orders-api hangi downstream yüzünden yavaşladı", mcptools.DependencyPathName, map[string]string{"direction": "down"}},
		{"orders-api'yi kim çağırıyor", mcptools.DependencyPathName, map[string]string{"direction": "up"}},
		{"orders-api çökerse etki alanı ne olur", mcptools.DependencyPathName, map[string]string{"direction": "up"}},
		{"orders-api bağımlılık zinciri ve etki alanı", mcptools.DependencyPathName, map[string]string{"direction": "both"}},
		{"who calls orders-api", mcptools.DependencyPathName, map[string]string{"direction": "up"}},
	}
	for _, c := range cases {
		got := lookupAskFor(c.q)
		if got.Tool != c.tool {
			t.Errorf("%q → %q, want %q", c.q, got.Tool, c.tool)
			continue
		}
		for k, v := range c.args {
			if got.Args[k] != v {
				t.Errorf("%q: arg %s=%q, want %q (all %v)", c.q, k, got.Args[k], v, got.Args)
			}
		}
	}
}

// Komşular: guided demetlerinin sorusu — eşleşmemeli (akış bayt bayt eski).
func TestLookupAskForNeighboursStayGuided(t *testing.T) {
	for _, q := range []string{
		"orders-api sağlığı nasıl?",
		"orders-api endpoint'i neden yavaş",
		"orders-api endpointlerinde hata var mı",
		"mainframe cevap sürelerimde artış var mı?",
		"orders-api son 1 saatte hata oranı",
		"orders-api nereden çağrılıyor",
		"orders-api yavaşlamasının sebebi hangi downstream?",
		"orders-api neden yavaşladı, hangi bağımlılık",
		"orders-api bağımlılıkları neler",
		"orders-api-7d9f pod sürekli restart oluyor",
		"orders-api podları restart ediyor mu",
		"siparis no 73066 için trace ara",
		"Caused by: java.lang.NullPointerException at com.example.Foo.bar(Foo.java:12)",
		"",
	} {
		if got := lookupAskFor(q); got.Tool != "" {
			t.Errorf("%q → %q (%s); guided'da kalmalıydı", q, got.Tool, got.Why)
		}
	}
}

func TestLookupLoopPromptTR(t *testing.T) {
	if lookupLoopPromptTR(lookupAsk{}) != "" {
		t.Fatal("eşleşme yokken istem eki boş olmalı (bayt bayt eski)")
	}
	p := lookupLoopPromptTR(lookupAskFor("bir log attım, external call var mı\n" + lkPastedLog))
	for _, want := range []string{"ÖNCE trace_where(trace_id=4bf92f3577b34da6a3ce929d0e0e4736)", "partner-gw.example.com",
		"SocketTimeoutException", "endpoint_lookup(domain=partner-gw.example.com)", "uydurma"} {
		if !strings.Contains(p, want) {
			t.Errorf("ipucunda %q yok:\n%s", want, p)
		}
	}
	// Model ayrımı yok: aynı soru → aynı metin (Qwen / gemma).
	if p != lookupLoopPromptTR(lookupAskFor("bir log attım, external call var mı\n"+lkPastedLog)) {
		t.Error("ipucu deterministik değil")
	}
}

func TestLookupDomainAndPeerTokens(t *testing.T) {
	if got := lookupDomainToken(guidedTokens("api.example.com bu domain")); got != "api.example.com" {
		t.Errorf("domain %q", got)
	}
	if got := lookupDomainToken(guidedTokens("orders-api.prod servisi")); got != "" {
		t.Errorf("tek noktalı servis adı domain sayılmamalı: %q", got)
	}
	if got := lookupPeerToken(guidedTokens("hata osb'den mi dönmüş")); got != "osb" {
		t.Errorf("peer %q", got)
	}
}
