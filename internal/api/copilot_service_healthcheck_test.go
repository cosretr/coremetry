package api

// copilot_service_healthcheck_test.go — v0.10.2048 (servis sağlık kontrolü
// dilim 6). Niyet DAR: yalnız açık "sağlık kontrolü / healthcheck / runbook
// çalıştır" isteği service_healthcheck'e gider; "sağlıklı mı / nasıl / tara"
// ve fiilsiz "runbook var mı" bugünkü yollarında kalır. Cevap metni
// deterministik: cümle önce, sonra 4 sütunlu tablo, Ölçülemedi etiketi.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestHasHealthcheckSignal(t *testing.T) {
	yes := []string{
		"orders-api servisinin sağlık kontrolünü yap",
		"orders-api SAĞLIK KONTROLÜ",
		"orders-api saglik kontrolu",
		"orders-api healthcheck",
		"orders-api health check please",
		"orders-api için runbook çalıştır",
		"orders-api runbook'unu çalıştırır mısın",
		"orders-api için runbook koş",
		"run the runbook for orders-api",
	}
	no := []string{
		"orders-api sağlıklı mı",
		"orders-api sağlığı nasıl",
		"orders-api servisini tara",
		"orders-api için runbook var mı",
		"runbook yapılandırması nasıl",
		"runbook koşulları neler",
		"orders-api neden yavaş",
	}
	for _, q := range yes {
		if !hasHealthcheckSignal(normalizeGuidedMsg(q)) {
			t.Errorf("%q sağlık kontrolü sayılmalı", q)
		}
	}
	for _, q := range no {
		if hasHealthcheckSignal(normalizeGuidedMsg(q)) {
			t.Errorf("%q sağlık kontrolü SAYILMAMALI", q)
		}
	}
}

func TestRouteServiceHealthcheck(t *testing.T) {
	services := []string{"orders-api", "billing-api"}
	cases := []struct {
		q, ctx      string
		wantIntent  guidedIntent
		wantService string
	}{
		{"orders-api servisinin sağlık kontrolünü yap", "", guidedServiceHealthcheck, "orders-api"},
		{"billing-api healthcheck, pod restart ve kafka lag dahil", "", guidedServiceHealthcheck, "billing-api"},
		{"sağlık kontrolü yap", "orders-api", guidedServiceHealthcheck, "orders-api"},
		{"sağlık kontrolü yap", "", guidedServiceHealthcheck, ""},
		// komşular: bugünkü yollarında kalır
		{"orders-api sağlığı nasıl?", "", guidedServiceHealth, "orders-api"},
	}
	for _, c := range cases {
		r := routeGuidedIntent(c.q, services, nil, nil, c.ctx)
		if r.Intent != c.wantIntent || r.Service != c.wantService {
			t.Errorf("%q (ctx %q) → %s/%q, want %s/%q", c.q, c.ctx, r.Intent, r.Service, c.wantIntent, c.wantService)
		}
	}
	if intentAllowed["service_healthcheck"] != guidedServiceHealthcheck || !intentNeedsService[guidedServiceHealthcheck] {
		t.Fatal("sınıflandırıcı beyaz listesi / servis şartı eksik")
	}
}

func TestRenderHealthcheckTR(t *testing.T) {
	res := mcptools.HealthcheckResult{Service: "orders-api", Window: "15m", Verdict: mcptools.HCCrit,
		Sentence: "orders-api: 1 sorun var: pod restart (cluster-a).",
		Rows: []mcptools.HealthcheckRow{
			{Check: "red", Title: "Hata oranı ve gecikme", State: mcptools.HCOK, Seen: "hata oranı %0.40", Link: "/service?name=orders-api", LinkLabel: "Servis özeti"},
			{Check: "pod_restarts", Title: "Pod restart ve OOM", Subject: "cluster-a", State: mcptools.HCCrit, Seen: "a|b pod'da 4 restart"},
			{Check: "datasource", Title: "JBoss datasource havuzu", Subject: "cluster-a", State: mcptools.HCUnknown, Seen: "datasource metriği bulunamadı"},
		},
		NotApplicable: []string{"Kafka consumer lag"}}
	out := renderHealthcheckTR(res)
	if !strings.HasPrefix(out, res.Sentence+"\n\n| Kontrol | Durum | Ne görüldü | Nereye bakmalı |") {
		t.Fatalf("cümle önce, tablo sonra olmalı:\n%s", out)
	}
	for _, want := range []string{
		"| Hata oranı ve gecikme | İyi | hata oranı %0.40 | [Servis özeti](/service?name=orders-api) |",
		"| Pod restart ve OOM · cluster-a | Sorun | a\\|b pod'da 4 restart | — |",
		"| Ölçülemedi |",
		"uygulanmadı (bu serviste veri yok): Kafka consumer lag",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("çıktı %q içermeli:\n%s", want, out)
		}
	}
}
