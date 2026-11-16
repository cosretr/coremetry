package notify

// policy_shadow.go — v0.10.1523 (Problem standardı R1): yeni bildirim barının
// GÖLGE ölçümü.
//
// SendProblemAlert'in mevcut karar kapılarından (bakım penceresi, ack,
// Sustur bağlantısı, "problem değil" politikası) SONRA, kanal fan-out'undan
// ÖNCE çağrılır ve yeni politikanın (chstore.NotifiableAt) bu açılış için
// ne derdi olduğunu notification_log'a yazar: channel_kind='policy',
// related_kind='policy', related_id=problem kimliği, error = "would_send"
// ya da "hold:<kod>".
//
// GÖNDERİMİ ASLA DEĞİŞTİRMEZ (TestShadowNeverChangesSends):
//   - dönüş değeri yok; çağrı satırı çıplak ifade (AST pini);
//   - iş ayrı goroutine'de, kopyalanmış Problem değeriyle — gönderim
//     gecikmez, Problem'in alanlarına yazılmaz;
//   - dedup defterlerine (dedupSeen, awaiting) dokunmaz; kendi defteri var;
//   - panik yakalanır; her hata log + devam.
//
// `enforce` kipi R1'de shadow ile AYNI davranır (gönderimi değiştiren kablo
// R5). `off` → hiçbir şey yazılmaz.
//
// Satır seli olmasın: aynı (problem, durum, başlangıç, ciddiyet, öncelik)
// bir saatte bir kez yazılır — dedup katman 2'nin anahtarıyla aynı biçim.
// Gölge satırı okuyucuları yanıltmaz: /events listesi, problem bildirim
// geçmişi, watchers çekmecesi ve kanal sağlığı channel_kind='policy'yi
// SQL'de dışlar (chstore/notification_log.go); dedup kapıları related_kind
// ile süzdüğü için 'policy' türünü hiç görmez.

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	shadowMemoTTL     = time.Hour
	shadowMemoMax     = 5000
	shadowVerdictTTL  = 30 * time.Second
	shadowReadTimeout = 5 * time.Second
)

// policyShadow — gölge ölçümün süreç-içi durumu (Notifier alanı).
type policyShadow struct {
	mu      sync.Mutex
	logged  map[string]int64                // memo anahtarı → yazım (unix ns)
	last    map[string]chstore.NotifyRecord // imza → yeni politikanın son "gönderilirdi" kaydı
	vAt     time.Time
	noise   map[string]chstore.ProblemVerdict
	wg      sync.WaitGroup
	traffic func(ctx context.Context, p chstore.Problem, now time.Time) *chstore.StdTraffic // test; nil = store
	verdict func(ctx context.Context) ([]chstore.ProblemVerdict, error)                     // test; nil = store
	sink    func(ctx context.Context, e chstore.NotificationLog) error                      // test; nil = store
	sens    func() chstore.AnomalySensitivityConfig                                         // test; nil = store
	// freq — v0.10.2280 (S3): "sık tekrar eden" gölge kararı kaynağı
	// (evaluator); nil = ölçülmez. Yalnız AYRI bir gölge satırı yazar.
	freq atomic.Value // FrequentShadowSource
}

// FrequentShadowSource — v0.10.2280 (entity grafı S3): Problem için "sık
// tekrar eden" kuralının gölge kararı. ok=false = karar yok.
type FrequentShadowSource interface {
	FrequentShadowVerdict(ctx context.Context, p chstore.Problem) (chstore.FrequentVerdict, bool)
}

// SetFrequentShadow — main bağlar (evaluator). Gönderimi ETKİLEMEZ: kaynak
// yalnız recordShadowDecision'ın goroutine'inde, politika satırı yazıldıktan
// SONRA sorulur; NotifiableAt ve enforceGate onu hiç görmez.
func (n *Notifier) SetFrequentShadow(src FrequentShadowSource) {
	if n != nil && src != nil {
		n.shadow.freq.Store(src)
	}
}

// shadowMemoKey — SAF.
func shadowMemoKey(p chstore.Problem) string {
	return fmt.Sprintf("%s|%s|%d|%s|%s", p.ID, p.Status, p.StartedAt, p.Severity, p.Priority)
}

// claim — bu anahtar son saatte yazılmadıysa talep et.
func (s *policyShadow) claim(key string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logged == nil {
		s.logged = map[string]int64{}
	}
	if at, ok := s.logged[key]; ok && now.UnixNano()-at < int64(shadowMemoTTL) {
		return false
	}
	if len(s.logged) >= shadowMemoMax {
		for k, at := range s.logged {
			if now.UnixNano()-at >= int64(shadowMemoTTL) {
				delete(s.logged, k)
			}
		}
	}
	s.logged[key] = now.UnixNano()
	return true
}

// shadowRow — SAF: karar → notification_log satırı.
func shadowRow(p chstore.Problem, std chstore.ProblemStandard, dec chstore.NotifyDecision, mode string) chstore.NotificationLog {
	code := chstore.NotifyShadowWouldSend
	if !dec.Notifiable {
		code = "hold:" + dec.Hold
	}
	body := std.Headline + " · güven " + dec.Confidence + " · öncelik " + dec.Priority
	if dec.Bypass != "" {
		body += " · baypas " + dec.Bypass
	}
	return chstore.NotificationLog{
		ChannelKind: chstore.NotifyShadowChannelKind,
		ChannelName: mode,
		Subject:     alertTitle(p),
		BodyPreview: preview(body, 200),
		RelatedKind: chstore.NotifyShadowRelatedKind,
		RelatedID:   p.ID,
		OK:          false,
		Error:       code,
	}
}

// shadowNotifyPolicy — funnel kancası. Dönüş yok; iş arka planda.
func (n *Notifier) shadowNotifyPolicy(ctx context.Context, p chstore.Problem) {
	if n == nil || p.Status != "open" {
		return
	}
	pol := chstore.CurrentNotifyPolicy()
	if pol.Mode == chstore.NotifyModeOff {
		return
	}
	if n.store == nil && n.shadow.sink == nil {
		return
	}
	now := time.Now()
	if !n.shadow.claim(shadowMemoKey(p), now) {
		return
	}
	n.shadow.wg.Add(1)
	go func(p chstore.Problem) {
		defer n.shadow.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[notify] policy shadow panic (gönderim etkilenmedi): %v", r)
			}
		}()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shadowReadTimeout)
		defer cancel()
		n.recordShadowDecision(sctx, p, pol, now)
	}(p)
}

func (n *Notifier) shadowSens() chstore.AnomalySensitivityConfig {
	if n.shadow.sens != nil {
		return n.shadow.sens()
	}
	if n.store == nil {
		return chstore.AnomalySensitivityConfig{}
	}
	return n.store.AnomalySensitivityForDetectors()
}

func (n *Notifier) shadowTraffic(ctx context.Context, p chstore.Problem, now time.Time) *chstore.StdTraffic {
	if n.shadow.traffic != nil {
		return n.shadow.traffic(ctx, p, now)
	}
	if n.store == nil || p.Service == "" || chstore.ProblemSubjectKind(p.Kind) != chstore.ProblemKindService {
		return nil
	}
	cur, base := chstore.TrafficPairsFor(p.Service, now)
	m, _, err := n.store.ServiceBucketTraffic(ctx, []chstore.ServiceBucket{cur, base})
	if err != nil {
		log.Printf("[notify] policy shadow traffic %s: %v", p.Service, err)
		return nil // bilinmiyor — tutmaz
	}
	return chstore.StdTrafficFrom(m, cur, base)
}

func (n *Notifier) shadowNoise(ctx context.Context, sig string) *chstore.NoiseMark {
	if sig == "" {
		return nil
	}
	n.shadow.mu.Lock()
	stale := time.Since(n.shadow.vAt) > shadowVerdictTTL
	n.shadow.mu.Unlock()
	if stale {
		var list []chstore.ProblemVerdict
		var err error
		switch {
		case n.shadow.verdict != nil:
			list, err = n.shadow.verdict(ctx)
		case n.store != nil:
			list, err = n.store.ListProblemVerdicts(ctx)
		}
		n.shadow.mu.Lock()
		if err == nil {
			m := map[string]chstore.ProblemVerdict{}
			for _, v := range list {
				if v.Verdict == chstore.ProblemVerdictNoise {
					m[v.Signature] = v
				}
			}
			n.shadow.noise = m
		} else {
			log.Printf("[notify] policy shadow verdicts: %v", err)
		}
		n.shadow.vAt = time.Now()
		n.shadow.mu.Unlock()
	}
	n.shadow.mu.Lock()
	defer n.shadow.mu.Unlock()
	if v, ok := n.shadow.noise[sig]; ok {
		vv := v
		return chstore.NoiseMarkOf(&vv)
	}
	return nil
}

// recordShadowDecision — kararı hesaplar ve yazar.
func (n *Notifier) recordShadowDecision(ctx context.Context, p chstore.Problem, pol chstore.NotifyPolicy, now time.Time) {
	sens := n.shadowSens()
	facts := chstore.StdFacts{
		Now:          now,
		Traffic:      n.shadowTraffic(ctx, p, now),
		CalendarKind: sens.Calendar.SpecialKindOn(now),
		Batch:        sens.IsBatchService(p.Service),
	}
	if p.RecentDeploy != nil {
		facts.RecentDeploy = p.RecentDeploy.Version
	}
	std := chstore.StandardizeProblem(p, facts)
	prio := chstore.NotifyViewPriority(p, chstore.CurrentProblemPriority().InboxKeepSourcePriorityList())
	sig := verdictSignature(p)
	hist := chstore.NotifyHistory{Noise: n.shadowNoise(ctx, sig)}
	n.shadow.mu.Lock()
	if r, ok := n.shadow.last[sig]; ok && sig != "" {
		rr := r
		hist.LastSend = &rr
	}
	n.shadow.mu.Unlock()
	dec := chstore.NotifiableAt(std, prio, hist, pol, now.In(sens.Calendar.Location()))
	if dec.Notifiable && sig != "" {
		n.shadow.mu.Lock()
		if n.shadow.last == nil {
			n.shadow.last = map[string]chstore.NotifyRecord{}
		}
		n.shadow.last[sig] = chstore.NotifyRecord{At: now.UnixNano(), Priority: prio, Magnitude: dec.Magnitude}
		n.shadow.mu.Unlock()
	}
	row := shadowRow(p, std, dec, pol.Mode)
	var err error
	if n.shadow.sink != nil {
		err = n.shadow.sink(ctx, row)
	} else {
		err = n.store.InsertNotificationLog(ctx, row)
	}
	if err != nil {
		log.Printf("[notify] policy shadow write %s: %v", p.ID, err)
	}
	n.recordFrequentShadow(ctx, p, dec, pol.Mode)
}

// frequentShadowRow — SAF: S3 gölge kararı → AYRI notification_log satırı
// (channel_kind='policy'; error = chstore.FrequentShadow* kodu). Politika
// satırı bayt bayt aynı kalır; "" = satır yok (sık değil).
func frequentShadowRow(p chstore.Problem, dec chstore.NotifyDecision, v chstore.FrequentVerdict, mode string) (chstore.NotificationLog, bool) {
	code := chstore.FrequentShadowCode(dec, v)
	if code == "" {
		return chstore.NotificationLog{}, false
	}
	body := fmt.Sprintf("imza %s · 24 sa %d bölüm · 7 gün %d gün · öncelik %s (pencerede en yüksek %s)",
		v.Signature, v.Prior24h, v.Days7d, v.Priority, v.MaxPriority)
	if v.Bypass != "" {
		body += " · şiddet arttı " + v.Bypass
	}
	if dec.Hold != "" {
		body += " · politika " + dec.Hold
	}
	return chstore.NotificationLog{
		ChannelKind: chstore.NotifyShadowChannelKind,
		ChannelName: mode,
		Subject:     alertTitle(p),
		BodyPreview: preview(body, 200),
		RelatedKind: chstore.NotifyShadowRelatedKind,
		RelatedID:   p.ID,
		OK:          false,
		Error:       code,
	}, true
}

// recordFrequentShadow — S3 gölge satırı (varsa). Hata yalnız log.
func (n *Notifier) recordFrequentShadow(ctx context.Context, p chstore.Problem, dec chstore.NotifyDecision, mode string) {
	src, _ := n.shadow.freq.Load().(FrequentShadowSource)
	if src == nil {
		return
	}
	v, ok := src.FrequentShadowVerdict(ctx, p)
	if !ok {
		return
	}
	row, ok := frequentShadowRow(p, dec, v, mode)
	if !ok {
		return
	}
	var err error
	if n.shadow.sink != nil {
		err = n.shadow.sink(ctx, row)
	} else if n.store != nil {
		err = n.store.InsertNotificationLog(ctx, row)
	}
	if err != nil {
		log.Printf("[notify] frequent shadow write %s: %v", p.ID, err)
	}
}
