package notify

// ignore_link.go — v0.10.749: bildirimdeki "Sustur" bağlantısı.
//
// Bağlantı kimliksiz ve imzalı (auth.SignAction); kimlik jetonun
// içinde: e-posta kanalında ALICI adresi (sendEmail alıcı başına ayrı
// mail atar), paylaşımlı kanallarda (Slack/Teams/Zoom/webhook)
// "channel:<tür>/<ad>". İmzalayıcı ya da public URL yoksa bağlantı yok
// ve şablonlar bayt-bayt eski (kapalı ortam / test).

import (
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ActionSigner — auth.Service.SignAction imzası; boot'ta bağlanır.
type ActionSigner func(kind, id, who string, ttl time.Duration) (string, error)

// SetActionSigner — main.go: notifier.SetActionSigner(authSvc.SignAction).
func (n *Notifier) SetActionSigner(f ActionSigner) {
	n.actionSigner = f
}

// ignoreLinkTTL — 7 gün: bir problem/incident bundan uzun açık kalmaz;
// eski maildeki bağlantı "süresi dolmuş" sayfasına düşer.
const ignoreLinkTTL = 7 * 24 * time.Hour

// channelWho — paylaşımlı kanalın jeton kimliği.
func channelWho(c chstore.NotificationChannel) string {
	return "channel:" + c.Type + "/" + c.Name
}

// ignoreURL — boş = bağlantı basılmaz.
func (n *Notifier) ignoreURL(p chstore.Problem, who string) string {
	if n == nil || n.actionSigner == nil || p.ID == "" {
		return ""
	}
	base := n.PublicURL()
	if base == "" {
		return ""
	}
	kind := chstore.NotifyKindProblem
	if p.Kind == chstore.NotifyKindIncident {
		kind = chstore.NotifyKindIncident
	}
	tok, err := n.actionSigner(kind, p.ID, who, ignoreLinkTTL)
	if err != nil || tok == "" {
		return ""
	}
	return base + "/api/public/notify/ignore/" + tok
}
