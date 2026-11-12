package api

// copilot_service_healthcheck.go — v0.10.2048 (servis sağlık kontrolü, dilim 6;
// spec docs/plans/spec-service-healthcheck-runbook.md, operatör onayı
// 2026-11-12, açık soru 1: YENİ niyet).
//
// "orders-api servisinin sağlık kontrolünü yap" / "orders-api healthcheck" /
// "orders-api için runbook çalıştır" → service_healthcheck. Cevap LLM'SİZ ve
// deterministik: servis sayfası düğmesinin, /runbooks yerleşik satırının ve
// MCP aracının (service_health_sweep checks:"all") ÇAĞIRDIĞI AYNI fonksiyon
// (mcptools.ReadServiceHealthcheckCached; aynı 60 sn önbellek girdisi). Önce
// tek cümle, altında kontrol tablosu (sohbet markdown tablosu), bağlantılar.
//
// Kapsam DAR (operatör: mevcut service_health cevapları değişmez): yalnız
// "sağlık kontrolü" / "healthcheck" / "health check" ifadeleri ya da
// "runbook" + eylem fiili (çalıştır / koş / yap / başlat / run). "X sağlıklı
// mı", "X nasıl", "X'i tara" bugünkü yollarında kalır; "runbook var mı" bilgi
// aramasında kalır (fiil yok). Servis adı yoksa ekran bağlamındaki servis;
// o da yoksa model çağrılmadan "hangi servis?" denir.

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// guidedServiceHealthcheck — servis sağlık kontrolü niyeti.
const guidedServiceHealthcheck guidedIntent = "service_healthcheck"

// hcFoldTR — SAF: Türkçe harfleri ASCII'ye katla (ifade eşleşmesi için;
// "SAĞLIK" ToLower ile "sağlik" olur, "sağlık" ile aynı sayılmalı).
var hcFoldTR = strings.NewReplacer("ğ", "g", "ı", "i", "ş", "s", "ü", "u", "ö", "o", "ç", "c", "â", "a", "î", "i")

// hasHealthcheckSignal — SAF: normalize mesaj sağlık kontrolü istiyor mu.
func hasHealthcheckSignal(msg string) bool {
	m := hcFoldTR.Replace(strings.ToLower(msg))
	for _, p := range []string{"saglik kontrol", "healthcheck", "health check", "health-check"} {
		if strings.Contains(m, p) {
			return true
		}
	}
	if !strings.Contains(m, "runbook") {
		return false
	}
	// Eylem fiili SÖZCÜK başında ("runbook" içindeki "run" sayılmaz).
	words := strings.FieldsFunc(m, func(r rune) bool { return r < 'a' || r > 'z' })
	for _, w := range words {
		switch w {
		case "run", "execute", "yap", "kos", "calis":
			return true
		}
		// "yapılandırma", "koşul" gibi ad kökleri sayılmasın: yalnız fiil gövdeleri.
		for _, v := range []string{"calistir", "kostur", "baslat"} {
			if strings.HasPrefix(w, v) {
				return true
			}
		}
	}
	return false
}

// routeServiceHealthcheck — router kapısı (routeGuidedIntentExt; servis /
// ortam çıkarımından hemen sonra, tüm özel şekillerden ÖNCE: "pod restart"
// ya da "kafka lag" geçse bile açık sağlık kontrolü isteği kendi niyetinde).
func routeServiceHealthcheck(msg, svc, env, ctxService string) (guidedRoute, bool) {
	if !hasHealthcheckSignal(msg) {
		return guidedRoute{}, false
	}
	if svc == "" {
		svc = ctxService
	}
	return guidedRoute{Intent: guidedServiceHealthcheck, Service: svc, Env: env}, true
}

// healthcheckStateTR — durum etiketi (FE HC_STATE_LABEL ile aynı).
func healthcheckStateTR(s string) string {
	switch s {
	case mcptools.HCOK:
		return "İyi"
	case mcptools.HCWarn:
		return "Dikkat"
	case mcptools.HCCrit:
		return "Sorun"
	}
	return "Ölçülemedi"
}

// renderHealthcheckTR — SAF: sonuç → sohbet metni (cümle + tablo + dipnot).
func renderHealthcheckTR(res mcptools.HealthcheckResult) string {
	var b strings.Builder
	b.WriteString(res.Sentence)
	b.WriteString("\n\n| Kontrol | Durum | Ne görüldü | Nereye bakmalı |\n|---|---|---|---|\n")
	for _, r := range res.Rows {
		title := r.Title
		if r.Subject != "" {
			title += " · " + r.Subject
		}
		link := "—"
		if r.Link != "" {
			link = "[" + mdCell(firstNonEmpty(r.LinkLabel, "Aç")) + "](" + r.Link + ")"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", mdCell(title), healthcheckStateTR(r.State), mdCell(r.Seen), link)
	}
	fmt.Fprintf(&b, "\nSon %s", res.Window)
	if res.Partial {
		b.WriteString(" · kısmi: bazı kaynaklar zamanında cevap vermedi")
	}
	if len(res.NotApplicable) > 0 {
		b.WriteString(" · uygulanmadı (bu serviste veri yok): " + strings.Join(res.NotApplicable, ", "))
	}
	b.WriteString(". Salt-okunur kontrol; hüküm kurallardan, model yorum katmadı.")
	return b.String()
}

// guidedHealthcheckAnswer — LLM'siz cevap.
func (s *Server) guidedHealthcheckAnswer(ctx context.Context, emit func(string, any), route guidedRoute, ctxService string) (bool, bool) {
	svc := strings.TrimSpace(firstNonEmpty(route.Service, ctxService))
	if svc == "" {
		emit("answer", map[string]any{
			"text":        "Hangi servisin sağlık kontrolünü yapayım? Servis adını yaz (ör. \"orders-api sağlık kontrolü\") ya da servis sayfasındaki \"Sağlık kontrolü\" düğmesini kullan.",
			"suggestions": []string{},
			"links":       []guidedAnswerLink{{Label: "Runbooks", Href: "/runbooks"}},
		})
		return true, true
	}
	n := emitGuidedStep(emit, mcptools.ServiceHealthcheckName, fmt.Sprintf(`{"service":%q,"env":%q}`, svc, route.Env))
	res, err := mcptools.ReadServiceHealthcheckCached(ctx, s.mcpDeps(), mcptools.HealthcheckRequest{Service: svc, Env: route.Env})
	if err != nil {
		emitGuidedStepResult(emit, n, mcptools.ServiceHealthcheckName, "", err)
		emit("answer", map[string]any{"text": "Sağlık kontrolü koşamadı: " + truncRunes(err.Error(), 200), "suggestions": []string{}})
		return true, true
	}
	text := renderHealthcheckTR(res)
	emitGuidedStepResult(emit, n, mcptools.ServiceHealthcheckName, text, nil)
	emit("answer", map[string]any{
		"text":        text,
		"suggestions": []string{svc + " servisi neden yavaş?", svc + " servisini tara"},
		"links": dedupLinksByHref([]guidedAnswerLink{
			{Label: "Servis sayfasında aç", Href: "/service?name=" + url.QueryEscape(svc) + "&hc=1"},
			{Label: "Runbooks", Href: "/runbooks?hc=" + url.QueryEscape(svc)},
		}),
	})
	return true, true
}
