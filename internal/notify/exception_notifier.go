package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
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
	// logged — kalıcı defter sorusu (v0.10.1078; testte sahte).
	logged func(ctx context.Context, id string) (bool, error)
	// lastSent / ignored — v0.10.1109 Oracle P1 olayının soğuması (son `within`
	// içindeki son başarılı gönderim) ve Sustur kapısı (testte sahte; nil = kayıt
	// yok / susturulmamış). oracleRef — grubun kaynağı + son 1 sa (main.go;
	// nil/false = kaynak bilinmiyor → tek tek). send — testte sahte; nil =
	// Notifier.SendProblemAlert. Ayrıntı: exception_oracle_p1.go.
	lastSent  func(ctx context.Context, id string, within time.Duration) (time.Time, error)
	ignored   func(ctx context.Context, id string) (bool, error)
	oracleRef func(g chstore.ExceptionGroup) (OracleGroupRef, bool)
	send      func(ctx context.Context, p chstore.Problem)
	// gate — v0.10.1092: grubun bildirim hattına girip girmeyeceği + tazelik
	// ölçüsünün kayması (nil = hepsi, kayma 0). main.go Oracle kaynak kipini
	// bağlar: `ora:` grubu yalnız kaynağı problemMode=live iken bildirilir
	// (shadow = grup var, bildirim yok). Kayma: Oracle grubu yalnız KAPANMIŞ
	// dakikaları sayar, last_seen'i duvar saatinin ~WindowMin gerisindedir;
	// tazelik kapıları (≤10 dk / ≤5 dk) o kadar geriden ölçülmezse canlı bir
	// Oracle grubu HİÇBİR ZAMAN aday olmazdı.
	gate GroupGate
}

// GroupGate — (bildirilsin mi, tazelik kayması).
type GroupGate func(g chstore.ExceptionGroup) (ok bool, lag time.Duration)

// SetGroupGate — v0.10.1092: Start'tan ÖNCE çağrılır.
func (e *ExceptionNotifier) SetGroupGate(f GroupGate) { e.gate = f }

// admit — SAF yardımcı: kapı yoksa her grup, kayma 0. Dönen zaman tazelik
// kapılarının ölçüleceği "şimdi".
func (e *ExceptionNotifier) admit(g chstore.ExceptionGroup, now time.Time) (bool, time.Time) {
	if e.gate == nil {
		return true, now
	}
	ok, lag := e.gate(g)
	return ok, now.Add(-lag)
}

func NewExceptionNotifier(store *chstore.Store, n *Notifier, lock cache.Lock, prio ExceptionPriorityFn) *ExceptionNotifier {
	interval := 60 * time.Second
	return &ExceptionNotifier{
		n: n, store: store,
		leader:   cache.NewLeaderHolder(lock, exceptionNotifierLockKey, cache.LeaderTTL(interval)),
		interval: interval,
		prio:     prio,
		sent:     map[string]int64{},
		logged: func(ctx context.Context, id string) (bool, error) {
			return store.HasAnyNotification(ctx, "exception", id)
		},
		lastSent: func(ctx context.Context, id string, within time.Duration) (time.Time, error) {
			return store.LastNotificationAt(ctx, "exception", id, within)
		},
		ignored: store.NotificationIgnored,
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
//
// v0.10.1078 — regressed grup P1'e yükselince (v0.10.1072 hacim kapısı)
// `<fp>:regressed:p1:<epoch>` BİR kez daha — yalnız-P1 kanallar o ana dek
// hiçbir şey almamıştı. Damga resolve anı: sonraki regresyonda 500'ü yeniden
// aşan grup gerçek bir P1'dir. Taban `<fp>:regressed` DEĞİŞMEDİ (90 gün,
// damgasız): her gün resolve/regress olan kronik grup her seferinde bildirmez.

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

// exceptionRegressionP1ID — SAF (v0.10.1078): regresyonun P1 yükseltme
// anahtarı; resolve damgası (sn) ekli. Damgasız (eski) satır: `…:p1`.
func exceptionRegressionP1ID(fp string, resolvedAt *int64) string {
	id := exceptionGroupID(fp, chstore.ExStateRegressed) + ":p1"
	if resolvedAt != nil && *resolvedAt > 0 {
		id += ":" + strconv.FormatInt(*resolvedAt/int64(time.Second), 10)
	}
	return id
}

// exceptionGroupFingerprint — SAF: kimlikten parmak izi ("" = bu tür değil).
// Parmak izi onaltılık (':' içermez); sonrasındaki durum/damga/p1 ekleri atılır.
// v0.10.1092 — Oracle grubunun parmak izi `ora:<hex>` (tek ':' içerir):
// önek korunur, ilk ':' kesimi ondan SONRA aranır.
func exceptionGroupFingerprint(id string) string {
	if !strings.HasPrefix(id, chstore.ExceptionGroupRulePrefix) {
		return ""
	}
	rest := strings.TrimPrefix(id, chstore.ExceptionGroupRulePrefix)
	if strings.HasPrefix(rest, oracleSourceRollupTag) {
		return "" // v0.10.1109 — kaynak özeti tek bir grup değil
	}
	if strings.HasPrefix(rest, chstore.OracleGroupPrefix) {
		fp, _, _ := strings.Cut(strings.TrimPrefix(rest, chstore.OracleGroupPrefix), ":")
		return chstore.OracleGroupPrefix + fp
	}
	fp, _, _ := strings.Cut(rest, ":")
	return fp
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

// exceptionChannelKey — SAF: grubun bu tikte kanal yoluna gireceği kimlik
// ("" = aday değil). gnow: kapının kaydırdığı "şimdi". Regressed span grubunda
// gerçek kimliği claimRegressed seçer (taban ya da `:p1:<epoch>`).
func exceptionChannelKey(g chstore.ExceptionGroup, state, prio string, gnow time.Time) string {
	if chstore.IsOracleGroup(g.Fingerprint) && prio == "P1" {
		sinceLast := time.Duration(gnow.UnixNano() - g.LastSeen)
		if g.Occurrences < exChannelMinOccur || sinceLast < 0 || sinceLast > exChannelActiveWin {
			return ""
		}
		return exceptionOracleP1ID(g.Fingerprint)
	}
	if !isChannelCandidate(g, state, gnow) || (prio != "P1" && prio != "P2") {
		return ""
	}
	return exceptionGroupID(g.Fingerprint, state)
}

// exceptionChannelPasses — v0.10.1109: Oracle grupları AYRI taramada. Son
// görülmeleri kapanmış dakika gecikmesi kadar (≥ pencere + 1 dk) geride;
// last_seen DESC LIMIT 300 penceresini canlı span gruplarıyla paylaşınca
// büyük filoda pencerenin dışında kalabiliyorlardı. Oracle geçişleri son
// görülmeyle sınırlı (exOracleScanLookback) — FINAL tüm tabloyu taramasın;
// span geçişleri v0.10.1116'ten beri exSpanScanLookback ile sınırlı.
var exceptionChannelPasses = []struct{ state, oracle string }{
	{chstore.ExStateNew, "exclude"}, {chstore.ExStateRegressed, "exclude"},
	{chstore.ExStateNew, "only"}, {chstore.ExStateRegressed, "only"},
}

// ── v0.10.1116 — span geçişlerine zaman sınırı ─────────────────────────
//
// Span geçişleri (oracle "exclude") `exception_groups FINAL … ORDER BY
// last_seen DESC LIMIT 300` okumasını alt sınırsız yapıyordu. Sınır KANITLA
// güvenli: span grubunun kapısı gecikmesizdir (GroupNotifyGate span grubunda
// (true, 0) döner → gnow = now) ve isChannelCandidate HEM new HEM regressed
// için `0 ≤ now − last_seen ≤ exChannelActiveWin` (10 dk) ister; new ayrıca
// first_seen ≤ 15 dk. exceptionChannelKey ve claimRegressed aynı kapının
// arkasında — regressed dalında da tazelik ŞART. Yani her aday
// last_seen ≥ now − 10 dk taşır; sınır max(15 dk, 10 dk) + 2 sa pay =
// now − 2 sa 15 dk. Pay, ileride span grubuna gecikme veren bir kapıyı
// (≤ 2 sa) da kapsar. LIMIT eşdeğerliği: sıralama last_seen DESC olduğu
// için sınırın üstündeki satırlar sıralı listenin ÖNEKİDİR → sınırlı
// sorgunun ilk 300'ü = sınırsız sorgunun ilk 300'ünün sınır üstü kısmı;
// düşen her satır zaten aday olamazdı.
//
// DÜRÜST ETKİ: okuma IO'su KÜÇÜLMEZ. Tablo ORDER BY fingerprint, partition
// ve skip index yok; FINAL'de anahtar dışı kolon koşulu birleştirmeden SONRA
// uygulanır. clickhouse local ölçümü (200k grup): sınırlı/sınırsız ikisi de
// 26 mark / 250.552 satır okudu; minmax(last_seen) + use_skip_indexes_if_final
// = 1 ile de AYNI — fingerprint (hash) sıralı granüllerde her granül taze bir
// grup içerir, minmax hiçbirini eleyemez. Ayrıca exact-mode'suz (CH 24.8)
// use_skip_indexes_if_final RMT'de yanlış sonuç verebilir: yazıcılar
// oku-değiştir-yaz yapar, last_seen sürümler arasında geri gidebilir. Bu
// yüzden indeks EKLENMEDİ. Küçülen: FINAL sonrası satır kümesi (sıralama,
// LIMIT, aktarım, Go tarafında satır çözme) — tablo büyüdükçe 300'lük dolu
// cevap yerine yalnız son 2 sa 15 dk'da aktif gruplar.
const (
	exSpanScanMargin   = 2 * time.Hour
	exSpanScanLookback = max(exChannelNewMaxAge, exChannelActiveWin) + exSpanScanMargin
)

// exceptionChannelFilter — SAF: geçişin liste süzgeci. Oracle geçişi
// `last_seen ≥ now − (10 dk tazelik + 30 dk en çok gecikme)`; span geçişi
// (v0.10.1116) `last_seen ≥ now − exSpanScanLookback`.
func exceptionChannelFilter(state, oracle string, now time.Time) chstore.ExceptionGroupFilter {
	f := chstore.ExceptionGroupFilter{State: state, Limit: 300, MinOccurrences: exChannelMinOccur, Oracle: oracle}
	switch oracle {
	case "only":
		f.ActiveFromNs = now.Add(-exOracleScanLookback).UnixNano()
	case "exclude":
		f.ActiveFromNs = now.Add(-exSpanScanLookback).UnixNano()
	}
	return f
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
	name := kind + " · " + g.Type
	if chstore.IsOracleGroup(g.Fingerprint) {
		// v0.10.1109 — Oracle grubu kullanıcıya görünen adıyla (v0.10.1108
		// branding etiketi, varsayılan "Teknik hata") + operasyon; P1'i bir olay.
		name = chstore.CurrentOracleGroupLabel() + " · " + g.Type
		if op := strings.TrimSpace(g.Message); op != "" {
			name += " · " + op
		}
		if prio == "P1" {
			label = "P1 olayı"
		}
	}
	return chstore.Problem{
		ID:             exceptionGroupID(g.Fingerprint, state),
		RuleID:         chstore.ExceptionGroupRulePrefix + state,
		RuleName:       name,
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
	// v0.10.1109 — Oracle P1 olay adayları toplanır, taramalardan SONRA kaynak
	// başına özetlenir ya da tek tek gider (routeOracleP1).
	var oraP1 []oracleP1Cand
passes:
	for _, pass := range exceptionChannelPasses {
		state := pass.state
		groups, err := e.store.ListExceptionGroups(ctx, exceptionChannelFilter(state, pass.oracle, now))
		if err != nil {
			log.Printf("[exception-notifier] kanal yolu list %s/%s: %v", state, pass.oracle, err)
			continue
		}
		for _, g := range groups {
			if sent >= exChannelMaxPerTick {
				log.Printf("[exception-notifier] tik tavanı (%d) — kalan gruplar sonraki tikte", exChannelMaxPerTick)
				break passes
			}
			ok, gnow := e.admit(g, now)
			if !ok {
				continue
			}
			oracleGroup := chstore.IsOracleGroup(g.Fingerprint)
			if !oracleGroup && !isChannelCandidate(g, state, gnow) {
				continue // ucuz ön eleme: merdiven yalnız adayda (eski sıra)
			}
			prio, reason := e.prio(g)
			key := exceptionChannelKey(g, state, prio, gnow)
			if key == "" {
				continue
			}
			// v0.10.1016 — "problem değil" grubu tik tavanını yemesin
			// (SendProblemAlert zaten susturur; burada defter de kirlenmez).
			if e.n.verdictSilenced(ctx, exceptionVerdictSignature(g.Fingerprint)) {
				continue
			}
			if oracleGroup && prio == "P1" {
				oraP1 = append(oraP1, oracleP1Cand{g: g, state: state, reason: reason})
				continue
			}
			var id string
			if state == chstore.ExStateRegressed {
				id = e.claimRegressed(ctx, g, prio, now)
				if id == "" {
					continue
				}
				// Yükseltme, aynı regresyonun Sustur'unu da dinler (kimliği ayrı).
				if base := exceptionGroupID(g.Fingerprint, state); id != base {
					if ok, ierr := e.store.NotificationIgnored(ctx, base); ierr == nil && ok {
						continue
					}
				}
			} else {
				id = exceptionGroupID(g.Fingerprint, state)
				if e.alreadySent(ctx, id, now) {
					continue
				}
				e.sent[id] = now.UnixNano()
			}
			// v0.10.1109 — aynı bölümün P1'i az önce gittiyse Oracle P2'si çalmaz.
			// Talepten SONRA: kimlik defterde, sonraki tiklerde CH okuması yok.
			if oracleGroup && prio != "P1" && e.sentWithin(ctx, exceptionOracleP1ID(g.Fingerprint), now, exOracleP1Cooldown) {
				continue
			}
			p := exceptionGroupProblem(g, state, prio, reason)
			p.ID = id
			e.n.SendProblemAlert(ctx, p)
			sent++
		}
	}
	sent = e.routeOracleP1(ctx, now, oraP1, sent)
	if sent > 0 {
		log.Printf("[exception-notifier] %d exception/HTTP-hata grubu kanallara yönlendirildi", sent)
	}
	for id, at := range e.sent { // defter büyümesin; dedup notification_log'da
		if now.UnixNano()-at > int64(24*time.Hour) {
			delete(e.sent, id)
		}
	}
}

// alreadySent — önce lider-yerel defter, sonra notification_log (restart /
// lider değişimi). Log okunamadı = gönderilmemiş say (v0.10.782 yönü).
func (e *ExceptionNotifier) alreadySent(ctx context.Context, id string, now time.Time) bool {
	if _, done := e.sent[id]; done {
		return true
	}
	if seen, err := e.logged(ctx, id); err == nil && seen {
		e.sent[id] = now.UnixNano()
		return true
	}
	return false
}

// claimRegressed — v0.10.1078: regressed grup için bu tikte gidecek kimlik
// ("" = yok) ve defter kaydı:
//   - P2 → taban `<fp>:regressed` (eski davranış: 90 günde bir, damgasız);
//   - grup bu regresyonda P1 olur → `:p1:<epoch>` BİR kez (P1 kanallar ilk kez alır);
//   - regresyon zaten P1 başlarsa yalnız `:p1` gider ve taban da gönderilmiş
//     sayılır (P1 kanala çift gitmez).
//
// P2 tikinde `:p1` defteri yalnız taban gönderilmemişse sorulur — her tikte
// fazladan CH okuması yok.
func (e *ExceptionNotifier) claimRegressed(ctx context.Context, g chstore.ExceptionGroup, prio string, now time.Time) string {
	base := exceptionGroupID(g.Fingerprint, chstore.ExStateRegressed)
	up := exceptionRegressionP1ID(g.Fingerprint, g.ResolvedAt)
	if prio != "P1" {
		if e.alreadySent(ctx, base, now) || e.alreadySent(ctx, up, now) {
			e.sent[base] = now.UnixNano() // P1 doğmuş regresyon: sonraki tikler sorgusuz
			return ""
		}
		e.sent[base] = now.UnixNano()
		return base
	}
	if e.alreadySent(ctx, up, now) {
		return ""
	}
	e.sent[up] = now.UnixNano()
	e.sent[base] = now.UnixNano() // P1 doğan regresyonda taban da kapanır
	return up
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
			ok, gnow := e.admit(g, now)
			if !ok {
				continue
			}
			if chstore.IsOracleGroup(g.Fingerprint) {
				// v0.10.1092 — Oracle grubu: 500'lük span hacim eşiği DEĞİL, grubun
				// kendi merdiveni P1 demeli (patlama / yeni; api.oraclePriorityAt).
				prio := ""
				if e.prio != nil {
					prio, _ = e.prio(g)
				}
				if !oracleAnnounceCandidate(g, prio, gnow) {
					continue
				}
			} else if !isP1ExceptionCandidate(g, gnow) {
				continue
			}
			if e.n.verdictSilenced(ctx, exceptionVerdictSignature(g.Fingerprint)) {
				continue // v0.10.1016 — "problem değil": P1 anonsu da susar
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

// oracleAnnounceCandidate — SAF (v0.10.1092): Oracle grubunun takım anonsu
// yalnız merdiven P1 derken ve grup (gecikme kaydırılmış) taze iken.
func oracleAnnounceCandidate(g chstore.ExceptionGroup, prio string, gnow time.Time) bool {
	return prio == "P1" && time.Duration(gnow.UnixNano()-g.LastSeen) <= exNotifyFreshWindow
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
