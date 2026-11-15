package evaluator

// problem_member.go — v0.10.2156 (Davis tarzı tek Problem D2, spec
// docs/plans/spec-davis-style-problem-merge.md): evaluator'daki eski
// yazarların (yaygın yavaşlama, anomali terfisi, özne terfisi) ortak üyelik
// görünümü. Kural ve yumuşak-hata yönleri internal/anomaly/problem_member.go.

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// members — bir pasın üyelik görünümü (tik başına, pas başına bir okuma).
// v0.10.2164 (D5): üye sahibi P1'e çıkarırsa tek "yükseldi" bildirimi
// (anomaly.EscalationAlert; yaş eskalasyonuyla aynı hat).
func (e *Evaluator) members(ctx context.Context, snap *chstore.OpenProblems, detector string, now time.Time) *anomaly.Members {
	var send func(context.Context, chstore.Problem)
	if e.notifier != nil {
		send = e.notifier.SendProblemAlert
	}
	return anomaly.LoadMembers(ctx, e.store, snap, detector, now).OnEscalate(anomaly.EscalationAlert("evaluator", e.stamps, send))
}
