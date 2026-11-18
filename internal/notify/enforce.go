package notify

// enforce.go — v0.10.1533 (Problem standardı R5). notify_policy.mode=enforce
// iken yeni bildirim barı GÖNDERİMİ yönetir: gölgenin ölçtüğü kararın aynısı
// (chstore.NotifiableAt — D2 görünüm önceliği, D3 tekrar + baypas, D5 süreli
// "problem değil", D6 etki tabanları, D10 düşük güven bildirilmez) burada
// senkron uygulanır.
//
// VARSAYILAN KİP shadow (D8): enforce'a geçiş yöneticinin kararı — 7 günlük
// gölge ve replay'de "Gerçek problem" işaretleri + onaylı vakalarda %100
// recall görüldükten sonra (Settings → Bildirim politikası). shadow / off
// kipinde bu dosya hiçbir şeyi değiştirmez.
//
// Sıra (SendProblemAlert): mevcut kapılar → gölge → enforceGate →
// prepareAlertView (konu) → fan-out. Kapı konudan ÖNCE: tutulan açılış konu
// açmaz; dolayısıyla çözülmesi de gitmez ("açılışı gönderilmemiş problemin
// ÇÖZÜLDÜ'sü" gürültüdür).
//
// P3 (D7): tutulmaz, ÖZETE düşer — chstore.EnqueueDigest; digest.go günün
// özet saatlerinde tek mesajda gönderir.
//
// Güvenli yön: kararın girdilerinden biri okunamazsa (trafik, karar listesi)
// "bilinmiyor" sayılır ve bilinmiyor bildirimi TUTMAZ (NotifiableAt sözleşmesi).
// Özet kuyruğuna yazılamazsa bildirim ANINDA gider (kayıp yok).

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// enforceBook — enforce kipinin süreç-içi durumu (Notifier alanı).
type enforceBook struct {
	mu   sync.Mutex
	last map[string]chstore.NotifyRecord // imza → son GERÇEK gönderim (D3 tekrar penceresi)
	sent map[string]bool                 // imza → açılışı gönderildi (çözülme kapısı; legacy şablonda konu yok)
	held map[string]string               // v0.10.2341 (S4): Problem → tutulduğu görünüm önceliği (merge_hold.go)
}

// enforceGate — true = fan-out devam. hold = tutma kodu (log / test).
func (n *Notifier) enforceGate(ctx context.Context, p chstore.Problem) (bool, string) {
	pol := chstore.CurrentNotifyPolicy()
	if pol.Mode != chstore.NotifyModeEnforce || n == nil {
		return true, ""
	}
	sig := verdictSignature(p)
	if sig == "" {
		return true, "" // imzasız (olay): politika dışı, eski davranış
	}
	if p.Status == "resolved" {
		if n.enforceOpened(ctx, p, sig) {
			return true, ""
		}
		return false, "resolve:not_opened"
	}
	if p.Status != "open" {
		return true, ""
	}
	now := time.Now()
	sens := n.shadowSens()
	rctx, cancel := context.WithTimeout(ctx, shadowReadTimeout)
	defer cancel()
	facts := chstore.StdFacts{Now: now, CalendarKind: sens.Calendar.SpecialKindOn(now), Batch: sens.IsBatchService(p.Service)}
	if p.StartedAt > 0 {
		facts.Traffic = n.shadowTraffic(rctx, p, time.Unix(0, p.StartedAt))
	}
	if p.RecentDeploy != nil {
		facts.RecentDeploy = p.RecentDeploy.Version
	}
	std := chstore.StandardizeProblem(p, facts)
	prio := chstore.NotifyViewPriority(p, chstore.CurrentProblemPriority().InboxKeepSourcePriorityList())
	hist := chstore.NotifyHistory{Noise: n.shadowNoise(rctx, sig)}
	n.enf.mu.Lock()
	if r, ok := n.enf.last[sig]; ok {
		rr := r
		hist.LastSend = &rr
	}
	n.enf.mu.Unlock()
	n.mergeHistory(rctx, p, &hist, true)    // v0.10.2341 (S4): kapalıyken no-op
	n.frequentHistory(rctx, p, &hist, true) // v0.10.2342 (S5): kapalıyken no-op
	dec := chstore.NotifiableAt(std, prio, hist, pol, now.In(sens.Calendar.Location()))
	if dec.Notifiable {
		n.enf.mu.Lock()
		if n.enf.last == nil {
			n.enf.last, n.enf.sent = map[string]chstore.NotifyRecord{}, map[string]bool{}
		}
		n.enf.last[sig] = chstore.NotifyRecord{At: now.UnixNano(), Priority: prio, Magnitude: dec.Magnitude}
		n.enf.sent[sig] = true
		n.enf.mu.Unlock()
		return true, ""
	}
	if dec.Digest {
		if n.store == nil {
			return true, ""
		}
		item := digestItemOf(p, std, prio, sig, now)
		if err := n.store.EnqueueDigest(context.WithoutCancel(rctx), item); err != nil {
			log.Printf("[notify] P3 özet kuyruğu yazılamadı %s: %v — anında gönderiliyor", sig, err)
			return true, ""
		}
		return false, chstore.NotifyHoldP3Digest
	}
	n.rememberHeld(p.ID, dec.Hold, prio)
	return false, dec.Hold
}

// enforceOpened — bu imzanın açılışı (enforce altında ya da öncesinde)
// gönderildi mi: süreç-içi defter, yoksa kalıcı konu kaydı (R4). İkisi de
// bilinmiyorsa (konu okunamadı) GÖNDER — çözülme bildirimi kaybetmek
// fazladan bir mailden kötü.
func (n *Notifier) enforceOpened(ctx context.Context, p chstore.Problem, sig string) bool {
	n.enf.mu.Lock()
	sent := n.enf.sent[sig]
	n.enf.mu.Unlock()
	if sent {
		return true
	}
	key := threadKey(p)
	n.threads.mu.Lock()
	t, ok := n.threads.m[key]
	n.threads.mu.Unlock()
	if ok {
		return t != nil && t.State == chstore.NotifyThreadOpen
	}
	if n.store == nil {
		return true
	}
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	th, err := n.store.GetNotifyThread(rctx, key)
	if err != nil {
		return true
	}
	return th != nil && th.State == chstore.NotifyThreadOpen
}

// digestItemOf — SAF: problem → özet kalemi.
func digestItemOf(p chstore.Problem, std chstore.ProblemStandard, prio, sig string, now time.Time) chstore.DigestItem {
	return chstore.DigestItem{
		Signature: sig, ProblemID: p.ID, Service: p.Service, Severity: p.Severity, Priority: prio,
		Kind: chstore.ProblemNotifyKind(p), Headline: std.Headline, Impact: trImpactLine(std.Impact),
		Confidence: std.Confidence.Level, StartedAt: p.StartedAt, QueuedAt: now.UnixNano(),
	}
}
