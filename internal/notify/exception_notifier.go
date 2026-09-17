package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// exception_notifier.go — P1 exception anonsu (v0.9.437, öneri #2).
// Operatör diliyle "anonslu exception'lar" şimdiye dek yalnız inbox'ta
// yaşıyordu — notify paketinde exception hook'u HİÇ yoktu; ekip sayfaya
// bakmadan haberdar olamıyordu. Bu işçi, inbox'ın P1 formülünü
// (exceptionPriority, internal/api/inbox.go: last_seen ≤5dk VE
// occurrences ≥500) geçen grupları takımlara mailler.
//
// Desen team-routing'in (v0.8.429) ikizi:
//   - Alıcılar servis kataloğunun owner+SRE takımlarından
//     (resolveTeamRecipients + team_contacts blob'u); tc.Enabled ve
//     MinSeverity kapıları AYNEN geçerli — ayrı bir toggle yok, team
//     routing kapalıysa bu anons da kapalıdır (bilinçli).
//   - Gönderim sentetik e-mail kanalı + sendOne — şablon, SMTP ve
//     notification_log kaydı elle kurulmuş kanalla bayt-bayt aynı
//     (SendRunbookComplete'in sentetik-Problem emsali).
//   - Dedup: notification_log related_kind="exception", related_id=
//     fingerprint — grup ömrü başına TEK anons (90g pencere).
//     Regresyon bilinçli olarak YENİDEN anons ETMEZ (v1; spam riski >
//     değer — gerekirse dedup anahtarına dönem eklenir).
//   - Leader-lock'lu 60s tik; her şey soft-fail, evaluator/ingest
//     yolunu asla bloklamaz.
const exceptionNotifierLockKey = "exception-notifier:lock"

// P1 eşiği — inbox.go exceptionPriority ile BİREBİR (oradaki formül
// değişirse burası da değişmeli; pin: exception_notifier_test.go).
const exNotifyFreshWindow = 5 * time.Minute
const exNotifyMinOccurrences = 500

// ExceptionPriorityFn — inbox merdiveni (api/inbox.go exceptionPriorityAt);
// api paketi notify'ı içe aktardığı için işlev main.go'dan enjekte edilir.
type ExceptionPriorityFn func(g chstore.ExceptionGroup) (priority, reason string)

type ExceptionNotifier struct {
	n        *Notifier
	store    *chstore.Store
	leader   *cache.LeaderHolder
	interval time.Duration
	// prio (v0.10.782) — kanal yolu için grup önceliği; nil = kanal yolu kapalı
	// (yalnız P1 anons maili).
	prio ExceptionPriorityFn
	// sent — lider-yerel "bu grup/durum gönderildi" defteri; notification_log
	// (HasAnyNotification) restart sonrası aynı görevi görür.
	sent map[string]int64
}

func NewExceptionNotifier(store *chstore.Store, n *Notifier, lock cache.Lock, prio ExceptionPriorityFn) *ExceptionNotifier {
	interval := 60 * time.Second
	return &ExceptionNotifier{
		n: n, store: store,
		leader:   cache.NewLeaderHolder(lock, exceptionNotifierLockKey, cache.LeaderTTL(interval)),
		interval: interval,
		prio:     prio,
		sent:     map[string]int64{},
	}
}

func (e *ExceptionNotifier) Start(ctx context.Context) {
	if e == nil || e.n == nil {
		return
	}
	e.leader.Start(ctx)
	t := time.NewTicker(e.interval)
	defer t.Stop()
	e.tickIfLeader(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.tickIfLeader(ctx)
		}
	}
}

func (e *ExceptionNotifier) tickIfLeader(ctx context.Context) {
	if !e.leader.IsLeader() {
		return
	}
	// v0.10.782 — kanal yolu ekip-yönlendirmeden BAĞIMSIZ (kanal modalında
	// "Exception" türü seçili kanallar); anons maili eski kapısında.
	e.routeGroups(ctx, time.Now())
	tc, err := e.store.GetTeamContacts(ctx)
	if err != nil || !tc.Enabled {
		return // team routing kapalı → anons kapalı (bilinçli tek kapı)
	}
	e.run(ctx, tc)
}

// ── v0.10.782 — exception / HTTP-hata grupları → bildirim kanalları ──────
//
// Operatör (2026-09-17, prod): "HTTP hataları olarak yansımış ama
// notification gelmedi." Gruplar kanallara hiç girmiyordu; tek yol ≥500
// oluşumlu P1 anons mailiydi. Şimdi: inbox merdiveninde P1/P2 olan TAZE
// gruplar (new: ilk görülme ≤15 dk ve son görülme ≤10 dk; regressed: son
// görülme ≤10 dk) sentetik bir Problem olarak SendProblemAlert hunisine
// girer — kanal eşleşmesi Kind=exception (kanal başına opt-in), öncelik
// merdivenden (computePriority ezmez), ekip maili YOK (anons ayrı), grup
// ömrü + durum başına BİR kez (notification_log + lider-yerel defter), tik
// başına tavan exChannelMaxPerTick. Eski yapışkan P1 yığını (ilk görülme
// saatler önce) tetiklenmez: tazelik kapısı bunun için.

const (
	exChannelNewMaxAge  = 15 * time.Minute
	exChannelActiveWin  = 10 * time.Minute
	exChannelMaxPerTick = 20
	exChannelMinOccur   = 2
)

// exceptionGroupID — SAF: bildirim kimliği; regressed ayrı kimlik (bir kez
// daha bildirilir), Sustur bağlantısı da bu kimlikle çalışır.
func exceptionGroupID(fp, state string) string {
	if state == chstore.ExStateRegressed {
		return chstore.ExceptionGroupRulePrefix + fp + ":regressed"
	}
	return chstore.ExceptionGroupRulePrefix + fp
}

// exceptionGroupFingerprint — SAF: kimlikten parmak izi ("" = bu tür değil).
func exceptionGroupFingerprint(id string) string {
	if !strings.HasPrefix(id, chstore.ExceptionGroupRulePrefix) {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(id, chstore.ExceptionGroupRulePrefix), ":regressed")
}

// isChannelCandidate — SAF: tazelik kapısı (state'e göre) + oluşum tabanı.
func isChannelCandidate(g chstore.ExceptionGroup, state string, now time.Time) bool {
	if g.Occurrences < exChannelMinOccur {
		return false
	}
	sinceLast := time.Duration(now.UnixNano() - g.LastSeen)
	if sinceLast > exChannelActiveWin || sinceLast < 0 {
		return false
	}
	if state == chstore.ExStateNew {
		return time.Duration(now.UnixNano()-g.FirstSeen) <= exChannelNewMaxAge
	}
	return state == chstore.ExStateRegressed
}

var httpErrorTypeRe = regexp.MustCompile(chstore.HTTPErrorTypeRe)

// exceptionGroupProblem — SAF: kanal hunisine giren sentetik Problem.
// Öncelik/gerekçe merdivenden; severity kanal ciddiyet süzgeci için
// (P1 → critical, P2/P3 → warning). Saklanmaz.
func exceptionGroupProblem(g chstore.ExceptionGroup, state, prio, reason string) chstore.Problem {
	kind := "Exception"
	if httpErrorTypeRe.MatchString(g.Type) {
		kind = "HTTP hatası"
	}
	sev := "warning"
	if prio == "P1" {
		sev = "critical"
	}
	label := "yeni grup"
	if state == chstore.ExStateRegressed {
		label = "geri döndü (regressed)"
	}
	return chstore.Problem{
		ID:             exceptionGroupID(g.Fingerprint, state),
		RuleID:         chstore.ExceptionGroupRulePrefix + state,
		RuleName:       kind + " · " + g.Type,
		Service:        g.Service,
		Severity:       sev,
		Status:         "open",
		Metric:         "exception",
		Priority:       prio,
		PriorityReason: reason,
		Description:    fmt.Sprintf("%s — %d occurrence · %s. %s", g.Type, g.Occurrences, label, g.Message),
		StartedAt:      g.FirstSeen,
	}
}

// routeGroups — kanal yolu; her tik, lider.
func (e *ExceptionNotifier) routeGroups(ctx context.Context, now time.Time) {
	if e.prio == nil {
		return
	}
	sent := 0
	for _, state := range []string{chstore.ExStateNew, chstore.ExStateRegressed} {
		groups, err := e.store.ListExceptionGroups(ctx, chstore.ExceptionGroupFilter{
			State: state, Limit: 300, MinOccurrences: exChannelMinOccur,
		})
		if err != nil {
			log.Printf("[exception-notifier] kanal yolu list %s: %v", state, err)
			continue
		}
		for _, g := range groups {
			if sent >= exChannelMaxPerTick {
				log.Printf("[exception-notifier] tik tavanı (%d) — kalan gruplar sonraki tikte", exChannelMaxPerTick)
				return
			}
			if !isChannelCandidate(g, state, now) {
				continue
			}
			prio, reason := e.prio(g)
			if prio != "P1" && prio != "P2" {
				continue
			}
			id := exceptionGroupID(g.Fingerprint, state)
			if _, done := e.sent[id]; done {
				continue
			}
			if seen, herr := e.store.HasAnyNotification(ctx, "exception", id); herr == nil && seen {
				e.sent[id] = now.UnixNano()
				continue
			}
			e.sent[id] = now.UnixNano()
			e.n.SendProblemAlert(ctx, exceptionGroupProblem(g, state, prio, reason))
			sent++
		}
	}
	if sent > 0 {
		log.Printf("[exception-notifier] %d exception/HTTP-hata grubu kanallara yönlendirildi", sent)
	}
	for id, at := range e.sent { // defter büyümesin; dedup notification_log'da
		if now.UnixNano()-at > int64(24*time.Hour) {
			delete(e.sent, id)
		}
	}
}

func (e *ExceptionNotifier) run(ctx context.Context, tc chstore.TeamContacts) {
	now := time.Now()
	sent := 0
	for _, state := range []string{chstore.ExStateNew, chstore.ExStateRegressed} {
		groups, err := e.store.ListExceptionGroups(ctx, chstore.ExceptionGroupFilter{
			State: state, Limit: 100, MinOccurrences: exNotifyMinOccurrences,
		})
		if err != nil {
			log.Printf("[exception-notifier] list %s: %v", state, err)
			continue
		}
		for _, g := range groups {
			if !isP1ExceptionCandidate(g, now) {
				continue
			}
			if seen, herr := e.store.HasNotification(ctx, "exception", g.Fingerprint, teamRoutingChannelName); herr != nil || seen {
				continue
			}
			if e.sendExceptionMail(ctx, tc, g) {
				sent++
			}
		}
	}
	if sent > 0 {
		log.Printf("[exception-notifier] %d P1 exception anonsu gönderildi", sent)
	}
}

// isP1ExceptionCandidate — inbox P1 formülünün saf ikizi (tablo-testli).
func isP1ExceptionCandidate(g chstore.ExceptionGroup, now time.Time) bool {
	age := now.UnixNano() - g.LastSeen
	return time.Duration(age) <= exNotifyFreshWindow && g.Occurrences >= exNotifyMinOccurrences
}

// exceptionAsProblem — sentetik Problem: kanal şablonu Severity/Service/
// RuleName/Description alanlarını basar (SendRunbookComplete emsali).
// Saf — tablo-testli.
func exceptionAsProblem(g chstore.ExceptionGroup) chstore.Problem {
	return chstore.Problem{
		ID:          "exception:" + g.Fingerprint,
		Service:     g.Service,
		RuleName:    "Exception · " + g.Type,
		Severity:    "warning", // inbox exception satırlarıyla aynı (severity alanı yok)
		Metric:      "exception",
		Description: fmt.Sprintf("%s — %d occurrence (P1: son 5dk içinde aktif). %s", g.Type, g.Occurrences, g.Message),
		StartedAt:   g.FirstSeen,
	}
}

func (e *ExceptionNotifier) sendExceptionMail(ctx context.Context, tc chstore.TeamContacts, g chstore.ExceptionGroup) bool {
	if !tc.SeverityAllows("warning") {
		return false
	}
	var md *chstore.ServiceMetadata
	if md2, err := e.store.GetServiceMetadata(ctx, g.Service); err == nil {
		md = md2
	}
	to := resolveTeamRecipients(md, tc)
	if len(to) == 0 {
		return false
	}
	cfg, err := json.Marshal(EmailChannelConfig{Recipients: to})
	if err != nil {
		return false
	}
	ch := chstore.NotificationChannel{
		Name:   teamRoutingChannelName,
		Type:   "email",
		Config: cfg,
	}
	if err := e.n.sendOne(ctx, ch, exceptionAsProblem(g), "exception", g.Fingerprint); err != nil {
		log.Printf("[exception-notifier] %s (%s → %d rcpt): %v", g.Type, g.Service, len(to), err)
		return false
	}
	return true
}
