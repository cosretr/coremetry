package notify

import (
	"html"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// kind_footer.go — v0.10.814 (operatör: "anomali eventleri de mail olarak
// gidiyor, bunu disable edebilmeliyim; çok false pozitif"). Anahtar v0.10.747'den
// beri vardı (kanal → Türler) ama (a) ekip maili kanal süzgecinden bağımsız
// gidiyordu (settings.go TeamContacts.Kinds), (b) mail hangi türde olduğunu
// ve nereden kapatılacağını söylemiyordu. Bu dosya (b): her problem mailinin
// altbilgisinde tür + iki ayar bağlantısı.

// NotifyKindLabelTR — SAF: türün operatör etiketi (frontend notifyKinds ile aynı).
func NotifyKindLabelTR(kind string) string {
	switch kind {
	case chstore.NotifyKindAnomaly:
		return "Anomali"
	case chstore.NotifyKindIncident:
		return "Incident"
	case chstore.NotifyKindException:
		return "Exception"
	default:
		return "Problem (kural)"
	}
}

// settingsURL — PublicURL + ayar sekmesi; PublicURL yoksa "". Yollar
// LİTERAL (birleştirme değil): api/frontend_routes_test.go'nun rota kapısı
// PublicURL() taşıyan gövdelerdeki `base + "/…"` parçalarını App.tsx
// rotalarıyla (/settings/:section) eşler — "/settings/" + slug kayıtsız
// görünürdü (v0.10.816 düzeltmesi).
func (n *Notifier) settingsURL(slug string) string {
	base := n.PublicURL()
	if base == "" {
		return ""
	}
	switch slug {
	case "channels":
		return base + "/settings/channels"
	case "team-routing":
		return base + "/settings/team-routing"
	}
	return base + "/settings"
}

// kindFooterText — düz metin: tür + kapatma adresleri (bağlantı yoksa yol tarifi).
func (n *Notifier) kindFooterText(kind string) string {
	line := "Olay türü: " + NotifyKindLabelTR(kind)
	if ch, tr := n.settingsURL("channels"), n.settingsURL("team-routing"); ch != "" {
		line += " · bu türü kapatmak: kanal Türler " + ch + " · ekip yönlendirmesi Türler " + tr
	} else {
		line += " · bu türü kapatmak: Settings → Bildirim kanalları → kanal → Türler; Settings → Team routing → Türler"
	}
	return line + "\n"
}

// kindFooterHTML — HTML altbilgi hücresinin İÇİ (td çağıranda; Outlook-güvenli:
// yalnız metin ve stil'inde padding/background olmayan <a>).
func (n *Notifier) kindFooterHTML(kind, font string) string {
	label := "Olay türü: " + html.EscapeString(NotifyKindLabelTR(kind))
	ch, tr := n.settingsURL("channels"), n.settingsURL("team-routing")
	if ch == "" {
		return label + " · kapatmak: Settings → Bildirim kanalları → Türler / Team routing → Türler"
	}
	a := func(href, text string) string {
		return `<a href="` + html.EscapeString(href) + `" style="` + font + `;color:#6b7280;text-decoration:underline">` + text + `</a>`
	}
	return label + " · bu türü kapatmak: " + a(ch, "kanal Türler") + " · " + a(tr, "ekip yönlendirmesi Türler")
}
