package api

import (
	"strings"
	"testing"
)

// v0.10.819 — yapıştırılan hata metni: şekil dedektörü + terim merdiveni →
// log_field (message alanında terim geçen loglar) + Inbox exception bağlantısı.

const pastedDotNet = "Server Error in '/' Application.\nCould not load file or assembly 'log4net, Version=1.2.10.0, Culture=neutral, PublicKeyToken=669e0ddf0bb1aa2a' or one of its dependencies. The system cannot find the file specified."
const pastedJava = "Exception in thread \"main\" java.lang.RuntimeException: boom\nCaused by: java.lang.NullPointerException: Cannot invoke \"String.length()\"\n\tat com.shop.Pay.run(Pay.java:12)"
const pastedPython = "Traceback (most recent call last):\n  File \"app.py\", line 3, in <module>\n    main()\nKeyError: 'x'"

func TestLooksLikePastedError(t *testing.T) {
	yes := []string{
		pastedDotNet, pastedJava, pastedPython,
		"com.shop.payment.TimeoutException: read timed out",                                     // FQCN tek satır
		"System.NullReferenceException: Object reference not set",                               // .NET FQCN
		"'Newtonsoft.Json, Version=13.0.0.0, Culture=neutral, PublicKeyToken=30ad4fe6b2a6aeed'", // assembly kimliği, "error" sözcüğü yok
		"panic: runtime error: index out of range [3] with length 3\n\ngoroutine 1 [running]:",
		"Unhandled exception. System.IO.IOException: disk full",
		"ORA-00060: deadlock detected\nORA-06512: at line 1",      // iki zayıf
		"şu hatayı alıyorum: NullPointerException\nne yapmalıyım", // zayıf + çok satır
	}
	for _, q := range yes {
		if !looksLikePastedError(q) {
			t.Errorf("yapıştırılan hata sayılmalı: %q", q)
		}
	}
	no := []string{
		"checkout-service hata alıyor mu?",
		"payments'ta 500 artıyor, neden?",
		"TimeoutException var mı?", // tek zayıf, kısa, tek satır
		"07544915dcf643aead8a61070780e6f7",
		"8a61070780e6f7ab",
		"message alanında 'timeout' geçen loglar",
		"hatalı trace'lere nasıl ulaşırım",
		"bugün hava nasıl?",
		"HTTP 500 alıyorum",
		// inceleme 2026-09-19: düzyazı kalıpları tek satırlık soruda şekil değildir.
		"Unhandled exception alan servisler hangileri?",
		"Server Error in '/' Application nedir?",
		"Could not load file or assembly hatası ne demek",
		"", "   ",
	}
	for _, q := range no {
		if looksLikePastedError(q) {
			t.Errorf("yapıştırılan hata sayılMAMALI: %q", q)
		}
	}
}

func TestPastedErrorTerm(t *testing.T) {
	cases := []struct {
		in, want string
		typed    bool
	}{
		{pastedDotNet, "log4net", true},
		{pastedJava, "NullPointerException", true}, // SON Caused by (kök neden), RuntimeException değil
		{pastedPython, "KeyError", true},
		{"com.shop.payment.TimeoutException: read timed out", "TimeoutException", true},
		{"System.NullReferenceException: Object reference not set", "NullReferenceException", true},
		{"'Newtonsoft.Json, Version=13.0.0.0, Culture=neutral, PublicKeyToken=30ad4fe6b2a6aeed'", "Newtonsoft.Json", true},
		{"ORA-00060: deadlock detected while waiting for resource", "ORA-00060", true},
		{"panic: runtime error: index out of range [3] with length 3\n\ngoroutine 1 [running]:", "runtime error: index out of range [3] with length 3", true},
		// İlk-satır yedeği: hata sözcüklü satır, tür-şekilli DEĞİL (Inbox bağlantısı yok).
		{"Server Error in '/' Application.\nThe system cannot find the file specified.", "The system cannot find the file specified", false},
		// inceleme: geçersiz Caused-by çıktısı merdiveni KESMEZ; sonraki basamaklar denenir.
		{"Caused by: 2 errors occurred:\n\tat com.shop.Pay.run(Pay.java:12)", "Caused by: 2 errors occurred", false},
		// Soru cümlesi ya da hata sözcüğü olmayan satır terim değildir.
		{"Server Error in '/' Application.\nsome other line here.", "", false},
		{"Unhandled exception alan servisler hangileri?", "", false},
	}
	for _, c := range cases {
		got, typed := pastedErrorTerm(c.in)
		if got != c.want || typed != c.typed {
			t.Errorf("term(%q) = (%q, %v), want (%q, %v)", c.in, got, typed, c.want, c.typed)
		}
	}
}

func route(raw string, services []string) (guidedRoute, bool) {
	msg := normalizeGuidedMsg(raw)
	return routePastedError(raw, msg, guidedTokens(msg), services)
}

func TestRoutePastedError(t *testing.T) {
	r, ok := route(pastedDotNet, guidedTestServices)
	if !ok || r.Intent != guidedLogField || r.LogField != "message" || r.LogValue != "log4net" || !r.LogContains || r.ExceptionTerm != "log4net" || r.Service != "" || r.Env != "" {
		t.Fatalf("dotnet rota: ok=%v %+v", ok, r)
	}
	// Servis yalnız sınırlı TAM ad: "not"/"file"/"load" bir öneke çözülmez.
	trap := []string{"notification-service", "file-service", "loader", "checkout-service"}
	if r, ok := route(pastedDotNet, trap); !ok || r.Service != "" {
		t.Errorf("önek tuzağı: %+v", r)
	}
	named := "checkout-service loglarında şu hata var:\n" + pastedJava
	if r, ok := route(named, trap); !ok || r.Service != "checkout-service" || r.LogValue != "NullPointerException" {
		t.Errorf("adlı servis: ok=%v %+v", ok, r)
	}
	// Düzyazı terim: LogValue dolu, ExceptionTerm BOŞ (Inbox bağlantısı yalnız tür-şekilli terime).
	prose := "Server Error in '/' Application.\nThe system cannot find the file specified."
	if r, ok := route(prose, trap); !ok || r.LogValue == "" || r.ExceptionTerm != "" {
		t.Errorf("düzyazı terim: ok=%v %+v", ok, r)
	}
	// ÇEKİLME (inceleme 2026-09-19): açık alan cümlesi, trace araması, tür adı hakkında soru, sözcük düzeyi hata.
	for _, q := range []string{
		`exception.type alanında "java.lang.NullPointerException" eşit olan loglar`,
		"checkout-service servisinden içinde com.shop.TimeoutException geçen trace'leri getir",
		"com.shop.payment.TimeoutException kaç kez oldu?",
		"checkout-service hata alıyor mu?",
		"Unhandled exception alan servisler hangileri?",
	} {
		if r, ok := route(q, trap); ok {
			t.Errorf("çekilmeli: %q → %+v", q, r)
		}
	}
}

// Router sırası: .NET PublicKeyToken (16-hex) span sanılmaz; 32-hex trace id
// ve yapılandırılmış istek kimliği yine kazanır.
func TestRouteGuidedIntentPastedErrorOrder(t *testing.T) {
	r := routeGuidedIntent(pastedDotNet, guidedTestServices, guidedTestEnvs, nil, "")
	if r.Intent != guidedLogField || r.LogValue != "log4net" || r.SpanID != "" {
		t.Fatalf("dotnet: %+v", r)
	}
	const trace = "07544915dcf643aead8a61070780e6f7"
	if r := routeGuidedIntent("trace "+trace+"\n"+pastedJava, guidedTestServices, guidedTestEnvs, nil, ""); r.Intent != guidedTraceByID || r.TraceID != trace {
		t.Errorf("32-hex trace yine kazanmalı: %+v", r)
	}
	for _, q := range []string{pastedJava, pastedPython} {
		if r := routeGuidedIntent(q, guidedTestServices, guidedTestEnvs, nil, ""); r.Intent != guidedLogField {
			t.Errorf("%q → %q, beklenen log_field", q[:20], r.Intent)
		}
	}
	// Java yapıştırması "error"/"exception" jetonu taşımaz (FQCN tek jeton):
	// sıfır-maliyet kapısı şekle bakmalı.
	if hasGuidedSignal(normalizeGuidedMsg(pastedPython)) {
		t.Skip("python paste artık guided sinyal taşıyor; kapı testi anlamsız")
	}
	if !looksLikePastedError(pastedPython) {
		t.Error("kapı: python paste şekli geçmeli")
	}
}

func TestPastedErrorLinksIncludeInbox(t *testing.T) {
	rt := guidedRoute{Intent: guidedLogField, Service: "shop", LogField: "message", LogValue: "log4net", LogContains: true, ExceptionTerm: "log4net"}
	links := guidedAnswerLinks(rt, noLinkWindow())
	var hrefs []string
	for _, l := range links {
		hrefs = append(hrefs, l.Href)
	}
	joined := strings.Join(hrefs, " ")
	if !strings.Contains(joined, "/logs?q=") || !strings.Contains(joined, "/inbox?kind=exception&q=log4net&service=shop") {
		t.Errorf("bağlantılar (servis kapsamı dahil): %v", hrefs)
	}
	plain := guidedAnswerLinks(guidedRoute{Intent: guidedLogField, LogField: "message", LogValue: "x", LogContains: true}, noLinkWindow())
	if len(plain) != 1 {
		t.Errorf("terimsiz log_field tek bağlantı taşır: %v", plain)
	}
}
