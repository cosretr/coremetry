package api

// v0.10.2454 — sağlık sorusu yönlendirme fikstürleri (adlar sentetik) +
// anlatım eki kapısı.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestHealthQuestionsRouteToServiceHealth(t *testing.T) {
	services := []string{"orders-api", "billing-worker", "stock-svc"}
	for _, c := range []struct{ q, svc string }{
		{"orders-api servisi nasıl", "orders-api"},
		{"orders-api nasıl", "orders-api"},
		{"billing-worker sağlığı nasıl", "billing-worker"},
		{"billing-worker'ın sağlığı", "billing-worker"},
		{"stock-svc ne durumda", "stock-svc"},
		{"stock-svc servisinin durumu nedir", "stock-svc"},
		{"orders-api sağlıklı mı", "orders-api"},
	} {
		r := routeGuidedIntent(c.q, services, nil, nil, "")
		if r.Intent != guidedServiceHealth || r.Service != c.svc {
			t.Errorf("%q → %s / %q (service_health / %q bekleniyor)", c.q, r.Intent, r.Service, c.svc)
		}
		if a := lookupAskFor(c.q); a.Tool != "" {
			t.Errorf("%q lookup yönlendiricisine düşmemeli (%s)", c.q, a.Tool)
		}
	}
	// Ekran bağlamındaki servis ("bu servis nasıl").
	if r := routeGuidedIntent("bu servis ne durumda", services, nil, nil, "orders-api"); r.Intent != guidedServiceHealth || r.Service != "orders-api" {
		t.Errorf("bağlam servisi: %s / %q", r.Intent, r.Service)
	}
}

func TestHealthReportPromptGate(t *testing.T) {
	ev := "Hüküm: ...\n" + mcptools.HealthReportMarker + " (get_service_health; ...):\n1) Metrikler ..."
	if got := healthReportPromptFor(guidedServiceHealth, ev); got != copilot.ServiceHealthAnswerOrder() {
		t.Fatal("rapor varken sıra eki eklenmeli")
	}
	if healthReportPromptFor(guidedServiceHealth, "rapor yok") != "" || healthReportPromptFor(guidedServiceReport, ev) != "" {
		t.Fatal("rapor yoksa / başka niyette ek olmamalı (istem bayt bayt eski)")
	}
	order := copilot.ServiceHealthAnswerOrder()
	last := -1
	for _, h := range []string{"1. Metrikler", "2. Trace hataları", "3. En çok hata", "4. Teknik hatalar", "5. Exception", "6. Messaging", "Son satır"} {
		i := strings.Index(order, h)
		if i <= last {
			t.Fatalf("sıra: %q", h)
		}
		last = i
	}
	if !strings.Contains(order, "örneklenmiş") || !strings.Contains(order, "ölçülmüyor") {
		t.Fatal("örneklenmiş / ölçülmüyor kuralı eksik")
	}
}
