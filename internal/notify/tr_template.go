package notify

// tr_template.go — v0.10.1532 (Problem standardı R4; D9 "bildirimler tamamen
// Türkçe"). Bildirimler ekranla AYNI standarttan çizilir (chstore.
// ProblemStandard — Inbox satırı ve detay başlığının kaynağı): Ne bozuk ·
// Etki · Ne zamandan beri · Güven · Kaynak · Neden · Bağlam · Sonraki adım.
// Mesajın başında yaşam döngüsü etiketi (lifecycle.go), e-posta konusu konu
// boyunca SABİT.
//
// Görünüm SendProblemAlert'te bir kez kurulur ve ctx ile taşınır
// (withAlertView): kanal göndericileri imza değiştirmeden okur; görünüm yoksa
// (Test gönder, runbook tamamlandı, `template: legacy`) eski biçim bayt bayt.
//
// Webhook kanalları DEĞİŞMEZ: gövdeleri operatörün kendi şablonu (sözleşme).

import (
	"context"
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

// alertView — bir bildirimin Türkçe görünümü.
type alertView struct {
	Life   string
	Thread chstore.NotifyThread
	Std    chstore.ProblemStandard
	Now    time.Time
}

type alertViewKey struct{}

func withAlertView(ctx context.Context, v *alertView) context.Context {
	return context.WithValue(ctx, alertViewKey{}, v)
}

// alertViewOf — ctx'teki görünüm (yoksa nil → eski biçim).
func alertViewOf(ctx context.Context) *alertView {
	v, _ := ctx.Value(alertViewKey{}).(*alertView)
	return v
}

// prepareAlertView — politika `legacy` değilse görünümü kurar, konuyu ilerletir
// ve ctx'e koyar. Trafik (G1) yalnız konu geçişinde okunur (açılış / yeniden
// başlama / çözülme): her tikte yeniden çağrılan açık problem CH okumaz.
func (n *Notifier) prepareAlertView(ctx context.Context, p chstore.Problem) context.Context {
	if chstore.CurrentNotifyPolicy().Template == chstore.NotifyTemplateLegacy {
		return ctx
	}
	now := time.Now()
	sens := n.shadowSens()
	facts := chstore.StdFacts{Now: now, CalendarKind: sens.Calendar.SpecialKindOn(now), Batch: sens.IsBatchService(p.Service)}
	if p.RecentDeploy != nil {
		facts.RecentDeploy = p.RecentDeploy.Version
	}
	std := chstore.StandardizeProblem(p, facts)
	life, th := n.advanceThread(ctx, p, std.Headline, now)
	if life != LifeUpdate && p.StartedAt > 0 {
		facts.Traffic = n.shadowTraffic(ctx, p, time.Unix(0, p.StartedAt))
		std = chstore.StandardizeProblem(p, facts)
	}
	return withAlertView(ctx, &alertView{Life: life, Thread: th, Std: std, Now: now})
}

// ── Saf metin yardımcıları (FE lib/problemStandardView.ts ikizi) ──────────

var confLabelTR = map[string]string{
	chstore.ConfidenceHigh: "YÜKSEK", chstore.ConfidenceMedium: "ORTA", chstore.ConfidenceLow: "DÜŞÜK",
}

var sourceLabelTR = map[string]string{
	chstore.StdSourceVM: "VictoriaMetrics", chstore.StdSourceSpan: "span", chstore.StdSourceDBMV: "DB özeti",
	chstore.StdSourceProbe: "sentetik kontrol", chstore.StdSourceException: "exception grubu",
	chstore.StdSourceOracle: "Oracle", chstore.StdSourceSelf: "Coremetry", chstore.StdSourceExternal: "dış kaynak",
	chstore.StdSourceRule: "kural",
	chstore.StdSourceLogs: "log kalıpları", // v0.10.2383 — ML log dedektörü liderliğindeki Problem (ekran etiketiyle aynı)
}

// trDuration — SAF: "45 sn" · "35 dk" · "5 sa 40 dk" · "2 g 3 sa".
func trDuration(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	if sec < 60 {
		return strconv.FormatInt(sec, 10) + " sn"
	}
	m := sec / 60
	if m < 60 {
		return strconv.FormatInt(m, 10) + " dk"
	}
	h := m / 60
	if h < 24 {
		if m%60 != 0 {
			return fmt.Sprintf("%d sa %d dk", h, m%60)
		}
		return fmt.Sprintf("%d sa", h)
	}
	d := h / 24
	if h%24 != 0 {
		return fmt.Sprintf("%d g %d sa", d, h%24)
	}
	return fmt.Sprintf("%d g", d)
}

var onesSuffixTR = []string{"'dan", "'den", "'den", "'ten", "'ten", "'ten", "'dan", "'den", "'den", "'dan"}
var tensSuffixTR = []string{"", "'dan", "'den", "'dan", "'tan", "'den"}

func numSuffixTR(n int) string {
	if n%10 != 0 {
		return onesSuffixTR[n%10]
	}
	if n == 0 {
		return "'dan"
	}
	if i := n / 10; i < len(tensSuffixTR) {
		return tensSuffixTR[i]
	}
	return "'den"
}

// trSinceLine — SAF: "06:05'ten beri" (bugün değilse "24.10 06:05'ten beri").
func trSinceLine(startNs int64, now time.Time, loc *time.Location) string {
	if startNs <= 0 {
		return "—"
	}
	if loc == nil {
		loc = time.UTC
	}
	t := time.Unix(0, startNs).In(loc)
	nl := now.In(loc)
	clock := t.Format("15:04")
	if t.Year() != nl.Year() || t.YearDay() != nl.YearDay() {
		clock = t.Format("02.01 15:04")
	}
	suf := numSuffixTR(t.Hour())
	if t.Minute() > 0 {
		suf = numSuffixTR(t.Minute())
	}
	return clock + suf + " beri"
}

// trDurationLine — SAF: "5 sa 40 dk · sürüyor · 3. kez".
func trDurationLine(s chstore.StdSince) string {
	parts := []string{trDuration(s.DurationSec)}
	if s.Ongoing {
		parts = append(parts, "sürüyor")
	} else {
		parts = append(parts, "bitti")
	}
	if s.Episode > 1 {
		parts = append(parts, fmt.Sprintf("%d. kez", s.Episode))
	}
	return strings.Join(parts, " · ")
}

// trImpactLine — SAF: birincil etki + ölçülmüş dakikalık hata + doğrulanmış çağıran.
func trImpactLine(im chstore.StdImpact) string {
	parts := []string{}
	if t := strings.TrimSpace(im.Primary.Text); t != "" {
		parts = append(parts, t)
	}
	if im.FailedPerMin > 0 {
		parts = append(parts, "~"+strconv.Itoa(int(math.Round(im.FailedPerMin)))+" hatalı istek/dk")
	}
	if n := len(im.Callers); n > 0 {
		parts = append(parts, fmt.Sprintf("%d çağıran (doğrulanmış)", n))
	}
	if len(parts) == 0 {
		return "etki bilinmiyor"
	}
	return strings.Join(parts, " · ")
}

var gateMarkTR = map[string]string{chstore.GatePass: "✓", chstore.GateFail: "✗", chstore.GateUnknown: "?", chstore.GateFlag: "!"}

// trGateSummary — SAF: "trafik ✓ sürdürme ? takvim !" (na / bağlam yazılmaz).
func trGateSummary(gates []chstore.SignalGate) string {
	var out []string
	for _, g := range gates {
		if g.Status == chstore.GateNA || g.Status == chstore.GateContext {
			continue
		}
		mark := gateMarkTR[g.Status]
		if mark == "" {
			mark = "?"
		}
		out = append(out, g.Name+" "+mark)
	}
	return strings.Join(out, " ")
}

// trConfidenceLine — SAF: "YÜKSEK (trafik ✓ …)".
func trConfidenceLine(c chstore.StdConfidence) string {
	l := confLabelTR[c.Level]
	if l == "" {
		l = "ORTA"
	}
	if s := trGateSummary(c.Gates); s != "" {
		return l + " (" + s + ")"
	}
	return l
}

// trSourceLine — SAF: "VictoriaMetrics · örneklenmemiş".
func trSourceLine(e chstore.StdEvidence) string {
	name := sourceLabelTR[e.Source]
	if name == "" {
		name = e.Source
	}
	if e.Sampled {
		return name + " · örneklenmiş"
	}
	return name + " · örneklenmemiş"
}

// trHeadLine — SAF: mesajın İLK satırı (önizlemede görünen): durum · öncelik ·
// güven. E-posta konusu sabit olduğu için aciliyet buradan okunur.
func trHeadLine(v *alertView, p chstore.Problem) string {
	parts := []string{v.Life}
	if pr := strings.TrimSpace(p.Priority); pr != "" {
		parts = append(parts, pr)
	}
	if l := confLabelTR[v.Std.Confidence.Level]; l != "" {
		parts = append(parts, "güven "+l)
	}
	return strings.Join(parts, " · ")
}

// trTopic — konunun manşeti (konu açılışındaki; yoksa güncel).
func (v *alertView) trTopic() string {
	if t := strings.TrimSpace(v.Thread.Topic); t != "" {
		return t
	}
	return v.Std.Headline
}

// trEmailSubject — SAF: SABİT e-posta konusu ("Coremetry · <manşet>").
// Durum ve öncelik konuda DEĞİL (Outlook konuşmayı konuya göre gruplar);
// önizleme satırında ve P1'de Importance başlığında.
func trEmailSubject(v *alertView) string {
	return "Coremetry · " + v.trTopic()
}

// trChatTitle — SAF: sohbet kanallarının başlığı: "AÇILDI · [P1] <manşet>".
func trChatTitle(v *alertView, p chstore.Problem) string {
	return strings.TrimSpace(v.Life + " · " + priorityTag(p) + " " + v.trTopic())
}

// alertTitleCtx — görünüm varsa Türkçe sohbet başlığı, yoksa eski başlık.
func alertTitleCtx(ctx context.Context, p chstore.Problem) string {
	if v := alertViewOf(ctx); v != nil {
		return trChatTitle(v, p)
	}
	return alertTitle(p)
}

// trField — etiket/değer çifti (boş değer yazılmaz).
type trField struct{ Label, Value, Href string }

// trFields — SAF: şablonların ortak alan listesi (sıra = mockup).
func (n *Notifier) trFields(v *alertView, p chstore.Problem) []trField {
	s := v.Std
	fs := []trField{
		{Label: "Etki", Value: trImpactLine(s.Impact)},
		{Label: "Ne zamandan beri", Value: trSinceLine(s.Since.StartedAt, v.Now, tzdefault.Location()) + " · " + trDurationLine(s.Since)},
		{Label: "Güven", Value: trConfidenceLine(s.Confidence)},
		{Label: "Kaynak", Value: trSourceLine(s.Evidence)},
		{Label: "Neden", Value: s.Evidence.Sentence},
	}
	if len(s.Context) > 0 {
		fs = append(fs, trField{Label: "Bağlam", Value: strings.Join(s.Context, " · ") + " (önceliği etkilemez)"})
	}
	next := trField{Label: "Sonraki adım", Value: s.NextStep.Text}
	if h := s.NextStep.Href; strings.HasPrefix(h, "/") && !strings.HasPrefix(h, "//") {
		if base := n.PublicURL(); base != "" {
			next.Href = strings.TrimRight(base, "/") + h
		}
	}
	fs = append(fs, next)
	if p.Priority != "" {
		pv := p.Priority
		if p.PriorityReason != "" {
			pv += " — " + p.PriorityReason
		}
		fs = append(fs, trField{Label: "Öncelik", Value: pv})
	}
	if p.RunbookURL != "" {
		fs = append(fs, trField{Label: "Runbook", Value: p.RunbookURL, Href: p.RunbookURL})
	}
	if p.OncallURL != "" {
		fs = append(fs, trField{Label: "Nöbet", Value: p.OncallURL, Href: p.OncallURL})
	}
	return fs
}

// trTechLine — SAF: teknik ayrıntı satırı (kural · metrik · değer/eşik) —
// standart cümleler insan için; nöbetçi kuralın kendisini de görebilmeli.
func trTechLine(p chstore.Problem) string {
	parts := []string{}
	if p.RuleName != "" {
		parts = append(parts, "kural: "+p.RuleName)
	}
	if p.Metric != "" {
		parts = append(parts, "metrik: "+p.Metric)
	}
	if p.Value != 0 || p.Threshold != 0 {
		parts = append(parts, fmt.Sprintf("değer %.2f (eşik %.2f)", p.Value, p.Threshold))
	}
	parts = append(parts, "ciddiyet: "+strings.ToLower(p.Severity))
	return strings.Join(parts, " · ")
}

// buildEmailBodyTR — düz metin parça (Türkçe standart).
func (n *Notifier) buildEmailBodyTR(v *alertView, p chstore.Problem, rc *chstore.RootCauseHypothesis, ignore string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n\n", trHeadLine(v, p), v.Std.Headline)
	for _, f := range n.trFields(v, p) {
		if strings.TrimSpace(f.Value) == "" {
			continue
		}
		val := f.Value
		if f.Href != "" && f.Href != f.Value {
			val += " — " + f.Href
		}
		fmt.Fprintf(&b, "%-17s %s\n", f.Label+":", val)
	}
	if u := n.subjectURL(p); u != "" {
		fmt.Fprintf(&b, "%-17s %s\n", "Coremetry'de aç:", u)
	}
	if ignore != "" {
		fmt.Fprintf(&b, "%-17s %s\n", "Sustur:", ignore)
	}
	fmt.Fprintf(&b, "\nTeknik: %s\n", trTechLine(p))
	if d := strings.TrimSpace(p.Description); d != "" {
		fmt.Fprintf(&b, "Dedektör notu: %s\n", d)
	}
	b.WriteString(n.kindFooterText(chstore.ProblemNotifyKind(p)))
	if sum := strings.TrimSpace(p.AISummary); sum != "" {
		fmt.Fprintf(&b, "\nAI kök-sebep yorumu:\n%s\n", sum)
	}
	if rc != nil {
		fmt.Fprintf(&b, "\nOlası kök neden (deterministik): %s — güven %%%.0f\n", rc.TopSuspect, rc.Confidence*100)
		if reason := hypothesisReason(rc); reason != "" {
			fmt.Fprintf(&b, "  %s\n", reason)
		}
		if rc.ExemplarTraceID != "" {
			if u := n.traceURL(rc.ExemplarTraceID); u != "" {
				fmt.Fprintf(&b, "Kanıt trace: %s\n", u)
			}
		}
	}
	return b.String()
}

// lifeColor — durum şeridinin rengi: çözülme yeşil, diğerleri ciddiyetin rengi.
func lifeColor(v *alertView, p chstore.Problem) string {
	if v.Life == LifeResolved {
		return "#16a34a"
	}
	return severityColor(p.Severity)
}

// buildEmailHTMLTR — HTML parça; Outlook (Word motoru) kısıtları buildEmailHTMLWith
// ile aynı: iç içe tablolar, dolgu <td>'de, <a> yalnız renk taşır, her dinamik
// alan kaçışlı.
func (n *Notifier) buildEmailHTMLTR(v *alertView, p chstore.Problem, rc *chstore.RootCauseHypothesis, ignore string) string {
	esc := html.EscapeString
	const font = `font-family:Segoe UI,Roboto,Helvetica,Arial,sans-serif`
	row := func(label, value string) string {
		return `<tr><td style="` + font + `;padding:4px 14px 4px 0;color:#6b7280;font-size:13px;white-space:nowrap;vertical-align:top">` +
			esc(label) + `</td><td style="` + font + `;padding:4px 0;font-size:13px;color:#111827">` + value + `</td></tr>`
	}
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="tr"><body style="margin:0;padding:0" bgcolor="#f3f4f6">`)
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="#f3f4f6"><tr><td align="center" style="padding:24px 16px">`)
	b.WriteString(`<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" bgcolor="#ffffff" style="border:1px solid #e5e7eb">`)
	b.WriteString(`<tr><td height="4" bgcolor="` + lifeColor(v, p) + `" style="height:4px;line-height:4px;font-size:0">&nbsp;</td></tr>`)
	b.WriteString(`<tr><td style="padding:20px">`)
	// Durum rozeti: durum · öncelik · güven (önizlemede okunan satırın aynısı).
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr><td bgcolor="` + lifeColor(v, p) +
		`" style="` + font + `;padding:2px 10px;color:#ffffff;font-size:11px;font-weight:700;letter-spacing:.5px">` +
		esc(trHeadLine(v, p)) + `</td></tr></table>`)
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%"><tr><td style="` + font +
		`;padding:12px 0 0;font-size:17px;font-weight:600;color:#111827">` + esc(v.Std.Headline) + `</td></tr></table>`)
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="border-collapse:collapse;margin:14px 0 0">`)
	for _, f := range n.trFields(v, p) {
		if strings.TrimSpace(f.Value) == "" {
			continue
		}
		val := esc(f.Value)
		if f.Href != "" {
			val = `<a href="` + esc(f.Href) + `" style="color:#2563eb">` + esc(f.Value) + `</a>`
		}
		b.WriteString(row(f.Label, val))
	}
	b.WriteString(row("Teknik", `<span style="color:#6b7280">`+esc(trTechLine(p))+`</span>`))
	b.WriteString(`</table>`)
	if d := strings.TrimSpace(p.Description); d != "" {
		b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%"><tr><td style="` + font +
			`;padding:10px 0 0;font-size:12px;color:#6b7280;line-height:1.5"><b>Dedektör notu:</b> ` +
			strings.ReplaceAll(esc(d), "\n", "<br>") + `</td></tr></table>`)
	}
	if sum := strings.TrimSpace(p.AISummary); sum != "" {
		b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="margin:16px 0 0"><tr>` +
			`<td bgcolor="#f9fafb" style="` + font + `;padding:12px 14px;border-left:3px solid #6b7280">` +
			`<div style="` + font + `;font-size:11px;font-weight:700;color:#6b7280;letter-spacing:.5px;padding-bottom:6px">AI KÖK-SEBEP YORUMU</div>` +
			`<div style="` + font + `;font-size:13px;color:#374151;line-height:1.5">` +
			strings.ReplaceAll(esc(sum), "\n", "<br>") + `</div></td></tr></table>`)
	}
	if rc != nil {
		b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="margin:16px 0 0"><tr>` +
			`<td bgcolor="#eff6ff" style="` + font + `;padding:12px 14px;border-left:3px solid #2563eb">` +
			`<div style="` + font + `;font-size:11px;font-weight:700;color:#1d4ed8;letter-spacing:.5px;padding-bottom:6px">OLASI KÖK NEDEN (DETERMİNİSTİK)</div>` +
			`<div style="` + font + `;font-size:13px;color:#111827;line-height:1.5"><b>` + esc(rc.TopSuspect) + `</b>` +
			fmt.Sprintf(` <span style="color:#6b7280">— güven %%%.0f</span>`, rc.Confidence*100))
		if reason := hypothesisReason(rc); reason != "" {
			b.WriteString(`<br>` + esc(reason))
		}
		if u := n.traceURL(rc.ExemplarTraceID); u != "" {
			b.WriteString(`<br><a href="` + esc(u) + `" style="color:#2563eb">Kanıt trace →</a>`)
		}
		b.WriteString(`</div></td></tr></table>`)
	}
	if u, ig := n.subjectURL(p), ignore; u != "" || ig != "" {
		btn := func(bg, href, label string) string {
			return `<td bgcolor="` + bg + `" style="padding:9px 18px"><a href="` + esc(href) +
				`" style="` + font + `;color:#ffffff;text-decoration:none;font-size:13px;font-weight:600">` + label + `</a></td>`
		}
		b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:16px 0 0"><tr>`)
		if u != "" {
			b.WriteString(btn("#111827", u, "Coremetry'de aç"))
		}
		if u != "" && ig != "" {
			b.WriteString(`<td width="10" style="width:10px;font-size:0;line-height:0">&nbsp;</td>`)
		}
		if ig != "" {
			b.WriteString(btn("#6b7280", ig, "Bu alarmı sustur"))
		}
		b.WriteString(`</tr></table>`)
	}
	b.WriteString(`</td></tr></table>`)
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr><td style="` + font + `;padding:12px 0 0;font-size:11px;color:#9ca3af" align="center">Coremetry problem bildirimi · öneriler bilgi içindir, sistem hiçbir şeyi değiştirmez</td></tr>` +
		`<tr><td style="` + font + `;padding:4px 0 0;font-size:11px;color:#9ca3af" align="center">` + n.kindFooterHTML(chstore.ProblemNotifyKind(p), font) + `</td></tr></table>`)
	b.WriteString(`</td></tr></table></body></html>`)
	return b.String()
}

// trThreadHeaders — SAF: konu başlıkları (RFC 5322 Message-ID / In-Reply-To /
// References + Outlook Thread-Topic; P1'de Importance: high).
func trThreadHeaders(v *alertView, p chstore.Problem) []string {
	root := threadMessageRoot(v.Thread)
	h := []string{"Message-ID: " + threadMessageID(v.Thread, v.Life, v.Now)}
	if v.Life != LifeOpened {
		h = append(h, "In-Reply-To: "+root, "References: "+root)
	}
	h = append(h, "Thread-Topic: "+encodeSubject(v.trTopic()))
	if strings.EqualFold(strings.TrimSpace(p.Priority), "P1") && v.Life != LifeResolved {
		h = append(h, "Importance: high", "X-Priority: 1")
	}
	return h
}

// trChatFields — SAF: sohbet kanallarının (Slack / Teams / Zoom) alanları.
func (n *Notifier) trChatFields(v *alertView, p chstore.Problem) []trField {
	fs := n.trFields(v, p)
	if u := n.subjectURL(p); u != "" {
		fs = append(fs, trField{Label: "Coremetry", Value: "Coremetry'de aç", Href: u})
	}
	return fs
}

// buildWhatsAppTextTR — SAF metin.
func (n *Notifier) buildWhatsAppTextTR(v *alertView, p chstore.Problem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%s*\n%s", trChatTitle(v, p), trHeadLine(v, p))
	for _, f := range n.trFields(v, p) {
		if strings.TrimSpace(f.Value) == "" || f.Label == "Bağlam" || f.Label == "Öncelik" {
			continue
		}
		fmt.Fprintf(&b, "\n%s: %s", f.Label, f.Value)
	}
	return b.String()
}

// slackBodyTR — Slack / Mattermost gövdesi (Türkçe standart).
func (n *Notifier) slackBodyTR(v *alertView, c chstore.NotificationChannel, p chstore.Problem) map[string]any {
	var fields []map[string]any
	for _, f := range n.trChatFields(v, p) {
		if strings.TrimSpace(f.Value) == "" {
			continue
		}
		val := f.Value
		if f.Href != "" {
			val = fmt.Sprintf("<%s|%s ↗>", f.Href, f.Value)
		}
		fields = append(fields, map[string]any{"title": f.Label, "value": val, "short": false})
	}
	if u := n.ignoreURL(p, channelWho(c)); u != "" {
		fields = append(fields, map[string]any{"title": "Sustur", "value": fmt.Sprintf("<%s|Bu alarmı sustur>", u), "short": false})
	}
	return map[string]any{
		"text": trChatTitle(v, p),
		"attachments": []map[string]any{{
			"color":  lifeColor(v, p),
			"text":   trHeadLine(v, p),
			"fields": fields,
			"footer": "Coremetry · " + trTechLine(p),
		}},
	}
}

// teamsBodyTR — Teams MessageCard (Türkçe standart).
func (n *Notifier) teamsBodyTR(v *alertView, c chstore.NotificationChannel, p chstore.Problem) map[string]any {
	var facts []map[string]string
	var actions []map[string]any
	for _, f := range n.trChatFields(v, p) {
		if strings.TrimSpace(f.Value) == "" {
			continue
		}
		if f.Href != "" {
			actions = append(actions, map[string]any{"@type": "OpenUri", "name": f.Label,
				"targets": []map[string]string{{"os": "default", "uri": f.Href}}})
			if f.Label == "Coremetry" {
				continue
			}
		}
		facts = append(facts, map[string]string{"name": f.Label, "value": f.Value})
	}
	if u := n.ignoreURL(p, channelWho(c)); u != "" {
		actions = append(actions, map[string]any{"@type": "OpenUri", "name": "Bu alarmı sustur",
			"targets": []map[string]string{{"os": "default", "uri": u}}})
	}
	body := map[string]any{
		"@type":      "MessageCard",
		"@context":   "https://schema.org/extensions",
		"summary":    trChatTitle(v, p),
		"themeColor": strings.TrimPrefix(lifeColor(v, p), "#"),
		"title":      trChatTitle(v, p),
		"text":       trHeadLine(v, p),
		"sections":   []map[string]any{{"facts": facts}},
	}
	if len(actions) > 0 {
		body["potentialAction"] = actions
	}
	return body
}

// zoomTextTR — Zoom Chat metni (Türkçe standart).
func (n *Notifier) zoomTextTR(v *alertView, c chstore.NotificationChannel, p chstore.Problem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n", trChatTitle(v, p), trHeadLine(v, p))
	for _, f := range n.trChatFields(v, p) {
		if strings.TrimSpace(f.Value) == "" {
			continue
		}
		val := f.Value
		if f.Href != "" && f.Href != f.Value {
			val += ": " + f.Href
		}
		fmt.Fprintf(&b, "\n• %s: %s", f.Label, val)
	}
	if u := n.ignoreURL(p, channelWho(c)); u != "" {
		fmt.Fprintf(&b, "\n• Sustur: %s", u)
	}
	return b.String()
}
