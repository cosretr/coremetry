package notify

// frequent_hold.go — v0.10.2342 (entity grafı ile Problem birleştirme S5,
// docs/problems/entity-graph-merge-design.md §6, §9): "sık tekrar eden"
// açılış bildirimi MEVCUT politika yolundan tutulur — kaynak S3'ün gölge
// kararıdır (FrequentShadowSource, evaluator), karar chstore.NotifiableAt'ın
// `frequent` adımı (etki tabanından sonra, "problem değil" ve tekrar
// penceresinden önce). Gönderimi yalnız notify_policy.mode=enforce değiştirir;
// gölge kipte politika satırı "hold:frequent" yazar. Şiddet artışı ve
// tutulduktan sonra öncelik yükselişi baypas eder.
//
// KAPALI GELİR: chstore.ProblemGraphFrequentEnforced() false iken kaynak bu
// yoldan sorulmaz, NotifyHistory.Frequent boş → karar bayt bayt aynı. S3'ün
// ayrı gölge satırı iki kipte de yazılır.

import (
	"context"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// frequentHistory — S5 açıkken hist'e sık tekrar kararını ekler.
func (n *Notifier) frequentHistory(ctx context.Context, p chstore.Problem, hist *chstore.NotifyHistory, book bool) {
	if !chstore.ProblemGraphFrequentEnforced() {
		return
	}
	src, _ := n.shadow.freq.Load().(FrequentShadowSource)
	if src == nil {
		return
	}
	v, ok := src.FrequentShadowVerdict(ctx, p)
	if !ok || !v.Frequent {
		return
	}
	hist.Frequent = &v
	if book {
		hist.HeldPriority = n.heldPriority(p.ID)
	}
}
