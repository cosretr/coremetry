package api

// v0.10.987 — "Hızlı açıkla" (operatör "3 seçenek"): yol seçimi saf.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTraceExplainPath(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"gövde yok → inceleme (v0.10.948 varsayılanı)", "", traceExplainInvestigation},
		{"quick → klasik kodsuz", `{"quick":true}`, traceExplainQuick},
		{"includeCode → klasik kodlu", `{"includeCode":true}`, traceExplainCode},
		{"ikisi de → kod dalı kazanır", `{"includeCode":true,"quick":true}`, traceExplainCode},
		{"quick:false → inceleme", `{"quick":false}`, traceExplainInvestigation},
		{"bozuk JSON → inceleme", `{"quick":`, traceExplainInvestigation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/copilot/explain-trace/t1", strings.NewReader(tc.body))
			if got := traceExplainPath(decodeExplainOptions(r)); got != tc.want {
				t.Fatalf("yol=%s, istenen %s", got, tc.want)
			}
		})
	}
}

// Hızlı yol inceleme koşucusunu HİÇ çağırmaz: sahte koşucu sayacı sıfır
// kalır (trace mağazası olmadığından cevap 500'e düşer; burada ölçülen
// yalnız hangi yola girildiği).
func TestExplainTraceQuickSkipsInvestigation(t *testing.T) {
	p := newInvCaptureProvider(t, "**Kök Neden ve Sonraki Adım**\n- a. b.")
	f := newFakeInvRunner(invTestT0)
	s := invHandlerServer(t, p, f)
	r := httptest.NewRequest(http.MethodPost, "/api/copilot/explain-trace/"+invTestTrace+"?stream=1", strings.NewReader(`{"quick":true}`))
	r.SetPathValue("id", invTestTrace)
	w := httptest.NewRecorder()
	func() {
		defer func() { _ = recover() }() // mağazasız Server: klasik paket kurulamaz; yol seçimi ölçülür
		s.copilotExplainTrace(w, r)
	}()
	if f.callCount() != 0 {
		t.Fatalf("hızlı yol inceleme araçlarını çalıştırdı: %d çağrı", f.callCount())
	}
}
