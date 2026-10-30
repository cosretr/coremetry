package api

// chat_approval.go — agent-core S4 (docs/cosre/agent-core-ideas.md §4):
// CoSRE araç onayının SAF çekirdeği. S4'te hiçbir araç onay istemez
// (toolsForRole Approval araçlarını sunmaz, Executor Approve kancası
// sohbette nil); bu dosya S5'in devam yolunun kararlarını taşır:
//
//   - approvalArgsDigest: jetonun bağlandığı argüman digest'i (çalışacak cargs);
//   - decideApproval: jeton + operatörün cevabı + çağıran → karar
//     (bağlama x,s,t,h,u; rol yeniden okunur; zaman aşımı = RET);
//   - claimApprovalOnce: Redis SETNX tek-kullanım (ikinci kullanım RET;
//     Redis yok/hata → RET);
//   - approvalGatePlan: v1'de ya da onaylayamayan rolde onay-kapılı aracı
//     spec'ten düşüren prepareStep (yalnız daraltır).
//   - approvalStepResult: `step-result{skipped, reason:approval_required,
//     approval{token, argsDigest, expiresAt}}` gövdesi (çevirici data-approval üretir).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// skipReasonApproval — step-result `reason` (Executor KindApproval ile aynı).
const skipReasonApproval = agenttools.KindApproval

// stopApprovalPending — onay bekleniyor: model yeniden çağrılmaz (chat_stop.go).
const stopApprovalPending stopReason = "approval_pending"

// stopOnApprovalPending — tur onay bekleyen bir çağrıyla bitti.
func stopOnApprovalPending() stopCond {
	return func(s loopState) stopReason {
		if s.ApprovalPending {
			return stopApprovalPending
		}
		return stopNone
	}
}

// approvalDecision — devam isteğindeki onayın sonucu (audit `decision`).
type approvalDecision string

const (
	approvalApproved  approvalDecision = "approved"
	approvalDenied    approvalDecision = "denied"
	approvalExpired   approvalDecision = "expired"
	approvalReplayed  approvalDecision = "replayed"
	approvalForbidden approvalDecision = "forbidden"
	approvalMismatch  approvalDecision = "mismatch"
	approvalInvalid   approvalDecision = "invalid"
)

// executes — yalnız approved çağrıyı yürütür; diğer her karar RET.
func (d approvalDecision) executes() bool { return d == approvalApproved }

// approvalArgsDigest — kanonik argüman digest'i (anahtar sırası farkı aynı
// digest; çözülemeyen argüman ham bayttan).
func approvalArgsDigest(args json.RawMessage) string {
	b := []byte(args)
	var v any
	if err := json.Unmarshal(args, &v); err == nil {
		if c, err := json.Marshal(v); err == nil {
			b = c
		}
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// approvalAnswer — operatörün devam isteğindeki cevabı (approvals[]).
type approvalAnswer struct {
	StepID   string          `json:"stepId"`
	Tool     string          `json:"tool"`
	Args     json.RawMessage `json:"args"`
	Token    string          `json:"token"`
	Approved bool            `json:"approved"`
}

// approvalCaller — devam isteğini yapan (rol İSTEK anında yeniden okunur).
type approvalCaller struct {
	UserID string
	Role   string
}

// decideApproval — SAF: doğrulanmış jeton hatası + claims + cevap + çağıran
// → karar. Sıra: jeton geçersiz → süre (zaman aşımı = ret) → bağlama
// (exchange, adım, araç, argüman digest'i) → kullanıcı + rol → operatör
// cevabı. Tek-kullanım kontrolü ayrı (claimApprovalOnce), approved'dan sonra.
func decideApproval(verifyErr error, c auth.ApprovalClaims, a approvalAnswer, exchangeID string, who approvalCaller, approverMinRole string) approvalDecision {
	switch {
	case errors.Is(verifyErr, auth.ErrActionExpired):
		return approvalExpired
	case verifyErr != nil:
		return approvalInvalid
	}
	if c.Exchange != exchangeID || c.Step != a.StepID || c.Tool != a.Tool || c.ArgsDigest != approvalArgsDigest(a.Args) {
		return approvalMismatch
	}
	if who.UserID == "" || c.User != who.UserID || !roleSatisfies(who.Role, approverMinRole) {
		return approvalForbidden
	}
	if !a.Approved {
		return approvalDenied
	}
	return approvalApproved
}

// approvalReplayKey — tek-kullanım anahtarı: jetonun sha256'sı (jeton
// Redis'e yazılmaz).
func approvalReplayKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "cosre:approval:" + hex.EncodeToString(sum[:])
}

// claimApprovalOnce — Redis SETNX ile jetonu bir kez harcar. İkinci kullanım
// replayed; Redis yok ya da hata → RET (invalid): tekrar oynatma koruması
// olmadan yazma yürütülmez (Q7). haveRedis: no-op önbellek SETNX'i her
// zaman kazanır — Redis'siz kurulumda çağıran false verir.
func claimApprovalOnce(ctx context.Context, c cache.Cache, haveRedis bool, token string, ttl time.Duration) approvalDecision {
	if c == nil || !haveRedis || ttl <= 0 {
		return approvalInvalid
	}
	ok, err := c.SetNX(ctx, approvalReplayKey(token), []byte("1"), ttl)
	switch {
	case err != nil:
		return approvalInvalid
	case !ok:
		return approvalReplayed
	}
	return approvalApproved
}

// approvalGatePlan — prepareStep: v1 akışında ya da onaylayamayan rolde
// onay-kapılı araçları spec'ten düşürür. Hiçbiri yoksa plan aynen (bayt aynı).
func approvalGatePlan(gated map[string]bool, v2 bool, approverMinRole string) prepareStep {
	return func(in stepInput, p stepPlan) stepPlan {
		if len(gated) == 0 || (v2 && roleSatisfies(in.Role, approverMinRole)) {
			return p
		}
		base := in.Specs
		if p.Specs != nil {
			base = p.Specs
		}
		keep := make([]copilot.ToolSpec, 0, len(base))
		dropped := false
		for _, s := range base {
			if gated[s.Name] {
				dropped = true
				continue
			}
			keep = append(keep, s)
		}
		if dropped {
			p.Specs = keep
		}
		return p
	}
}

// approvalStepResult — onay bekleyen çağrının step-result gövdesi (SAF):
// skippedStepResult + approval{token, argsDigest, expiresAt(ms)}.
func approvalStepResult(i int, tool, content, token, argsDigest string, exp time.Time) map[string]any {
	m := skippedStepResult(i, tool, content, skipReasonApproval)
	m["approval"] = map[string]any{"token": token, "argsDigest": argsDigest, "expiresAt": exp.UnixMilli()}
	return m
}
