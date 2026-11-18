package notify

// merge_hold.go — v0.10.2341 (entity grafı ile Problem birleştirme S4,
// docs/problems/entity-graph-merge-design.md §2, §9): sonradan bağlanan küme
// üyesinin açılış bildirimi MEVCUT politika yolundan tutulur — ikinci bir
// susturma yolu yok. Kaynak (evaluator) üyeliği söyler; karar
// chstore.NotifiableAt'ın `merged` adımıdır (enforce kipinde gönderimi
// yönetir, gölge kipinde politika satırına "hold:merged" yazar).
//
// KAPALI GELİR: chstore.ProblemGraphMergeEnforced() false iken bu dosyadaki
// hiçbir şey çağrılmaz (NotifyHistory.Merged ve HeldPriority boş kalır →
// karar bugünküyle bayt bayt aynı).
//
// Güvenli yön: sahibin açılışının GERÇEKTEN gittiği bilinmiyorsa (süreç içi
// defter yok, konu kaydı yok / okunamadı) üye tutulmaz — kümeden kimse
// bildirilmemiş olmasın. Kaynak karar veremezse (okuma hatası, graf yok)
// tutma yok.

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// MergeHoldSource — S4: Problem sonradan bağlanan bir küme üyesi mi.
// ok=false = üye değil ya da karar verilemedi.
type MergeHoldSource interface {
	MergeHold(ctx context.Context, p chstore.Problem) (chstore.MergeHold, bool)
}

// SetMergeHold — main bağlar (evaluator). Yalnız S4 açıkken sorulur.
func (n *Notifier) SetMergeHold(src MergeHoldSource) {
	if n != nil && src != nil {
		n.shadow.merge.Store(src)
	}
}

// mergeHistory — S4 açıkken hist'e küme üyeliğini ve tutulmuş önceliği
// ekler. book = enforce defterindeki tutulmuş önceliği oku (gölgede false).
func (n *Notifier) mergeHistory(ctx context.Context, p chstore.Problem, hist *chstore.NotifyHistory, book bool) {
	if !chstore.ProblemGraphMergeEnforced() {
		return
	}
	src, _ := n.shadow.merge.Load().(MergeHoldSource)
	if src == nil {
		return
	}
	m, ok := src.MergeHold(ctx, p)
	if !ok || m.Owner == nil || !n.mergeOwnerNotified(ctx, *m.Owner) {
		return
	}
	hist.Merged = &m
	if book {
		hist.HeldPriority = n.heldPriority(p.ID)
	}
}

// mergeOwnerNotified — sahibin açılışı gönderildi mi: süreç içi enforce
// defteri ya da kalıcı konu kaydı (R4) açık. Bilinmiyorsa false (tutma yok).
func (n *Notifier) mergeOwnerNotified(ctx context.Context, owner chstore.Problem) bool {
	sig := verdictSignature(owner)
	if sig != "" {
		n.enf.mu.Lock()
		sent := n.enf.sent[sig]
		n.enf.mu.Unlock()
		if sent {
			return true
		}
	}
	key := threadKey(owner)
	n.threads.mu.Lock()
	t, ok := n.threads.m[key]
	n.threads.mu.Unlock()
	if ok {
		return t != nil && t.State == chstore.NotifyThreadOpen
	}
	if n.store == nil {
		return false
	}
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	th, err := n.store.GetNotifyThread(rctx, key)
	if err != nil {
		return false
	}
	return th != nil && th.State == chstore.NotifyThreadOpen
}

// heldPriority / rememberHeld — enforce defterinde S4/S5 kuralıyla tutulan
// açılışın önceliği (yükselirse baypas). Yalnız S4/S5 kodlarında yazılır.
func (n *Notifier) heldPriority(id string) string {
	n.enf.mu.Lock()
	defer n.enf.mu.Unlock()
	return n.enf.held[id]
}

func (n *Notifier) rememberHeld(id, hold, prio string) {
	if hold != chstore.NotifyHoldMerged {
		return
	}
	n.enf.mu.Lock()
	defer n.enf.mu.Unlock()
	if n.enf.held == nil {
		n.enf.held = map[string]string{}
	}
	if len(n.enf.held) >= shadowMemoMax {
		n.enf.held = map[string]string{}
	}
	if _, ok := n.enf.held[id]; !ok {
		n.enf.held[id] = prio
	}
}
