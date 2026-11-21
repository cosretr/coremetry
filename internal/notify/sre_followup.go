package notify

// sre_followup.go — CoSRE Faz C1: bildirim giderken bitmemiş araştırmanın
// sonradan bitince aynı mesaja işlenmesi.
//
// Lider kilitli 20 sn tik bekleyen künyeleri (chstore.NotifySREFollowUp)
// okur; araştırma bittiyse:
//   - Zoom Chat: gönderilen mesaj DÜZENLENİR (PUT /v2/chat/users/me/
//     messages/{id}, ilk metin + özet bloğu). Düzenleme reddedilirse aynı
//     mesaja konuda tek yanıt (reply_main_message_id).
//   - E-posta: aynı konuya (In-Reply-To / References kök) tek kısa yanıt.
//   - Slack / Mattermost / Teams (gelen webhook — düzenleme ve konu yok):
//     tek kısa takip mesajı.
//   - webhook / whatsapp: künye tutulmaz (sre_summary.go followUpChannel).
//
// Satır teslimden ÖNCE silinir (en çok bir kez; silme başarısızsa teslim
// yok — çift takip mesajı bir kayıp özetten kötü). Problem artık açık değil,
// bildirimi susturulmuş ya da bakım penceresinde ise yalnız Zoom düzenlemesi
// yapılır (yeni mesaj yok). Araştırma 45 dk içinde bitmezse künye düşer.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	followUpLockKey = "notify:sre-followup"
	followUpEvery   = 20 * time.Second
	// followUpMaxAge — künyenin en uzun bekleme süresi.
	followUpMaxAge = 45 * time.Minute
)

// RunInvestigationFollowUpLoop — worker rolünde (RunDigestLoop yanında).
func (n *Notifier) RunInvestigationFollowUpLoop(ctx context.Context, lock cache.Lock) {
	leader := cache.NewLeaderHolder(lock, followUpLockKey, cache.LeaderTTL(time.Minute))
	leader.Start(ctx)
	t := time.NewTicker(followUpEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if leader.IsLeader() {
				tctx, cancel := context.WithTimeout(ctx, followUpEvery)
				n.followUpTick(tctx, time.Now())
				cancel()
			}
		}
	}
}

// followUpDecision — SAF: bekleyen künyeyle ne yapılır.
type followUpDecision int

const (
	followUpWait followUpDecision = iota
	followUpDrop
	followUpDeliver
)

func decideFollowUp(rec chstore.NotifySREFollowUp, info InvestigationInfo, now time.Time) followUpDecision {
	if !info.Enabled || now.Sub(time.Unix(0, rec.CreatedAt)) > followUpMaxAge {
		return followUpDrop
	}
	d := info.Digest
	switch {
	case d == nil:
		return followUpWait
	case d.Finished:
		return followUpDeliver
	case d.State == "queued" || d.State == "running":
		return followUpWait
	}
	return followUpDrop // error / skipped / shared-çözülemedi
}

func (n *Notifier) followUpTick(ctx context.Context, now time.Time) {
	lookup := n.investigationLookup()
	if lookup == nil || n.store == nil {
		return
	}
	recs, err := n.store.ListNotifySREFollowUps(ctx)
	if err != nil {
		log.Printf("[notify] CoSRE özet künyeleri okunamadı: %v", err)
		return
	}
	for _, rec := range recs {
		info, err := lookup(ctx, rec.ProblemID)
		if err != nil {
			continue
		}
		dec := decideFollowUp(rec, info, now)
		if dec == followUpWait {
			continue
		}
		if err := n.store.DeleteNotifySREFollowUp(ctx, rec.ProblemID); err != nil {
			log.Printf("[notify] CoSRE özet künyesi silinemedi (%s): %v", rec.ProblemID, err)
			continue
		}
		if dec == followUpDeliver {
			n.deliverFollowUp(ctx, rec, info.Digest)
		}
	}
}

// followUpAllowsNew — yeni (takip) mesajına izin var mı: problem hâlâ açık,
// bildirim bağlantısından susturulmamış, bakım penceresinde değil.
func (n *Notifier) followUpAllowsNew(ctx context.Context, p *chstore.Problem) bool {
	if p == nil || p.Status != "open" {
		return false
	}
	if ok, err := n.store.NotificationIgnored(ctx, p.ID); err == nil && ok {
		return false
	}
	return !n.maintenanceSilenced(ctx, p.Service, p.Severity)
}

func (n *Notifier) deliverFollowUp(ctx context.Context, rec chstore.NotifySREFollowUp, d *InvestigationDigest) {
	tr := rec.Lang == "tr"
	lines := sreBlockLines(d, tr, n.reportURL(d))
	if len(lines) == 0 {
		return
	}
	p, _ := n.store.GetProblem(ctx, rec.ProblemID)
	allowNew := n.followUpAllowsNew(ctx, p)
	for _, it := range rec.Items {
		c, err := n.store.GetChannel(ctx, it.ChannelID)
		if err != nil || c == nil || !c.Enabled {
			continue
		}
		var serr error
		switch c.Type {
		case "zoomchat":
			serr = n.zoomFollowUp(ctx, *c, it, lines, allowNew)
		case "slack", "mattermost":
			if allowNew {
				serr = n.slackFollowUp(ctx, *c, rec, lines)
			}
		case "teams":
			if allowNew {
				serr = n.teamsFollowUp(ctx, *c, rec, lines)
			}
		case "email":
			if allowNew {
				serr = n.emailFollowUp(ctx, *c, rec, lines)
			}
		}
		if serr != nil {
			log.Printf("[notify] CoSRE özeti işlenemedi — %s (%s) · %s: %v", c.Name, c.Type, rec.ProblemID, serr)
		}
	}
}

// followUpTitle — SAF: takip mesajının başlığı.
func followUpTitle(rec chstore.NotifySREFollowUp) string {
	return trEN(rec.Lang == "tr", "CoSRE araştırması tamamlandı — ", "CoSRE investigation finished — ") + rec.Title
}

// zoomFollowUp — mesajı düzenler; olmazsa konuda tek yanıt (izin varsa).
func (n *Notifier) zoomFollowUp(ctx context.Context, c chstore.NotificationChannel, it chstore.NotifySREFollowUpItem, lines []string, allowNew bool) error {
	var zc ZoomChatChannelConfig
	if err := json.Unmarshal(c.Config, &zc); err != nil {
		return fmt.Errorf("decode zoom chat config: %w", err)
	}
	if zc.AccountID == "" || zc.ClientID == "" || zc.ClientSecret == "" || (zc.ChannelID == "" && zc.ToContact == "") {
		return errors.New("zoom chat kanalı eksik yapılandırılmış")
	}
	target := func(m map[string]any) map[string]any {
		if zc.ChannelID != "" {
			m["to_channel"] = zc.ChannelID
		} else {
			m["to_contact"] = zc.ToContact
		}
		return m
	}
	var editErr error
	if it.MessageID != "" && it.Text != "" {
		editErr = n.zoomCall(ctx, zc, func(token string) error {
			_, err := zoomChatRequest(ctx, http.MethodPut, it.MessageID, token,
				target(map[string]any{"message": it.Text + sreBlockText(lines)}), zc.APIBaseURL, zc.InsecureSkipVerify)
			return err
		})
		if editErr == nil {
			return nil
		}
	}
	if !allowNew {
		return editErr
	}
	payload := target(map[string]any{"message": lines[0] + "\n• " + strings.Join(lines[1:], "\n• ")})
	if it.MessageID != "" {
		payload["reply_main_message_id"] = it.MessageID
	}
	err := n.zoomCall(ctx, zc, func(token string) error {
		_, err := postZoomChatMessageID(ctx, token, payload, zc.APIBaseURL, zc.InsecureSkipVerify)
		return err
	})
	if err != nil {
		return errors.Join(editErr, err)
	}
	return nil
}

// zoomCall — önbellekli jetonla çağırır; yetki hatasında jetonu düşürüp bir
// kez yeniden dener (sendZoomChat ile aynı akış).
func (n *Notifier) zoomCall(ctx context.Context, zc ZoomChatChannelConfig, f func(token string) error) error {
	token, err := n.zoomAccessToken(ctx, zc.AccountID, zc.ClientID, zc.ClientSecret, zc.OAuthBaseURL, zc.InsecureSkipVerify)
	if err != nil {
		return fmt.Errorf("zoom oauth: %w", err)
	}
	err = f(token)
	if err != nil && isAuthErr(err) {
		n.zoomInvalidate(zc.AccountID, zc.ClientID)
		token2, err2 := n.zoomAccessToken(ctx, zc.AccountID, zc.ClientID, zc.ClientSecret, zc.OAuthBaseURL, zc.InsecureSkipVerify)
		if err2 != nil {
			return fmt.Errorf("zoom oauth retry: %w", err2)
		}
		return f(token2)
	}
	return err
}

func (n *Notifier) slackFollowUp(ctx context.Context, c chstore.NotificationChannel, rec chstore.NotifySREFollowUp, lines []string) error {
	var sc SlackChannelConfig
	if err := json.Unmarshal(c.Config, &sc); err != nil || sc.WebhookURL == "" {
		return errors.New("slack kanalı eksik yapılandırılmış")
	}
	return postJSON(ctx, sc.WebhookURL, map[string]any{
		"text": followUpTitle(rec),
		"attachments": []map[string]any{{
			"color": "#6366f1", "title": lines[0], "text": "• " + strings.Join(lines[1:], "\n• "),
		}},
	})
}

func (n *Notifier) teamsFollowUp(ctx context.Context, c chstore.NotificationChannel, rec chstore.NotifySREFollowUp, lines []string) error {
	var tc TeamsChannelConfig
	if err := json.Unmarshal(c.Config, &tc); err != nil || tc.WebhookURL == "" {
		return errors.New("teams kanalı eksik yapılandırılmış")
	}
	return postJSON(ctx, tc.WebhookURL, map[string]any{
		"@type": "MessageCard", "@context": "https://schema.org/extensions",
		"summary": followUpTitle(rec), "title": followUpTitle(rec), "themeColor": "6366f1",
		"text": "- " + strings.Join(lines[1:], "\n\n- "),
	})
}

// emailFollowUp — aynı konuya tek yanıt (Türkçe şablonda konu kökü var).
func (n *Notifier) emailFollowUp(ctx context.Context, c chstore.NotificationChannel, rec chstore.NotifySREFollowUp, lines []string) error {
	smtpCfg := n.SMTP(ctx)
	if !smtpCfg.Configured() {
		return errors.New("SMTP yapılandırılmamış")
	}
	var ec EmailChannelConfig
	if err := json.Unmarshal(c.Config, &ec); err != nil || len(ec.Recipients) == 0 {
		return errors.New("e-posta kanalı alıcısız")
	}
	fromHeader := smtpCfg.From
	if smtpCfg.FromName != "" {
		fromHeader = (&mail.Address{Name: smtpCfg.FromName, Address: smtpCfg.From}).String()
	}
	subject := rec.Subject
	if subject == "" {
		subject = rec.Title
	}
	hdrs := followUpMailHeaders(rec, time.Now())
	plain := followUpTitle(rec) + sreBlockText(lines)
	htm := `<!DOCTYPE html><html><body>` + sreBlockHTML(lines) + `</body></html>`
	msg, err := composeAltEmailH(fromHeader, ec.Recipients, subject, plain, htm, hdrs)
	if err != nil {
		return fmt.Errorf("compose email: %w", err)
	}
	addr := net.JoinHostPort(smtpCfg.Host, strconv.Itoa(smtpCfg.Port))
	return sendSMTP(addr, smtpCfg, smtpCfg.From, ec.Recipients, msg)
}

// followUpMailHeaders — SAF: konu kökü varsa yanıt başlıkları.
func followUpMailHeaders(rec chstore.NotifySREFollowUp, now time.Time) []string {
	if rec.ThreadRoot == "" || !strings.HasPrefix(rec.ThreadRoot, "<") {
		return nil
	}
	return []string{
		fmt.Sprintf("Message-ID: <cm-sre-%d-%s", now.UnixNano(), rec.ThreadRoot[1:]),
		"In-Reply-To: " + rec.ThreadRoot,
		"References: " + rec.ThreadRoot,
	}
}
