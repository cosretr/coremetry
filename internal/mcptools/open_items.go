package mcptools

// v0.10.1694 (operator-reported) — "Açık konular": servis sayfasının üst
// şeridiyle (ServiceAttentionStrip → /api/inbox?service=&status=open&limit=20)
// AYNI küme. service_health_sweep'in açık-problem bölümü yalnız problems
// tablosunu okuyordu (alarm kuralı problemleri); exception / httperror grupları
// Inbox'ta olduğu hâlde sohbet "veri yok" derken sayfa 13 açık konu
// gösteriyordu. Okuma Deps.InboxOpen (api/cosre_inbox_open.go) +
// InboxOpenQuery.Attention (şeridin süzgeci).

import "net/url"

// ServiceAttentionLimit — şeridin api.inbox({limit: 20}) değeri.
const ServiceAttentionLimit = 20

// ServiceInboxPrio — şerit bağlantısının öncelik kapsamı (FE SERVICE_INBOX_PRIO).
const ServiceInboxPrio = "P1,P2,P3"

// ServiceAttentionQuery — SAF: şeridin sorgusu (ortamsız: şerit env taşımaz).
func ServiceAttentionQuery(service string) InboxOpenQuery {
	return InboxOpenQuery{Service: service, Limit: ServiceAttentionLimit, Attention: true}
}

// ServiceInboxHref — SAF: FE lib/inboxHref.ts serviceInboxHref'in ikizi
// (v0.10.1508): tüm öncelikler + açık-boş env= (yapışkan seçim dirilmez).
func ServiceInboxHref(service string) string {
	return "/inbox?service=" + url.QueryEscape(service) + "&prio=" + ServiceInboxPrio + "&env="
}
