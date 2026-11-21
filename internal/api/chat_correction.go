package api

// chat_correction.go — CoSRE Faz E (hafıza): sohbette AÇIK düzeltme.
//
// Mesaj "Düzeltme: …" / "Düzelt: …" / "Correction: …" ile başlıyorsa
// (cosrememory.DetectCorrection; iki nokta zorunlu) hafıza kademesi onu
// ekip notu ÖNERİSİ olarak karşılar: cevap bir `action` bloğu taşır (kind
// cosre_memory), istemci kartı metni + kapsamı düzenletir, yalnız "Kaydet"
// tıkı POST /api/ai/memory çağırır (editor+, audit). Sunucu HİÇBİR ŞEY
// kaydetmez; model çağrılmaz. Örtük itiraz ("bu yanlış"), 👍/👎 ve triage
// düğmeleri düzeltme DEĞİLDİR — not yalnız açık işaretle önerilir.

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/ai/cosrememory"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
)

const correctionHowToTR = "Düzeltmeyi tek cümleyle yaz: `Düzeltme: <doğru bilgi>` — ör. `Düzeltme: checkout'un veritabanı orders-db değil, pay-db.`"

// cosreMemoryAction — SAF: onay kartının aksiyon yükü.
func cosreMemoryAction(stmt string, def cosrememory.Scope, opts []ruleScopeOption, canSave bool, exchangeID string) map[string]any {
	act := map[string]any{
		"kind": "cosre_memory", "statement": stmt, "scope": def, "scopeOptions": opts,
		"canSave": canSave, "label": "Notu kaydet", "source": cosrememory.SourceCorrection,
	}
	if exchangeID != "" {
		act["exchangeId"] = exchangeID
	}
	if !canSave {
		act["reason"] = "Ekip notu kaydetmek editör ya da admin rolü ister."
	}
	return act
}

// chatCorrectionTier — açık düzeltme mesajı → not önerisi kartı.
func (s *Server) chatCorrectionTier(ctx context.Context, emit func(string, any), seq *blocks.Sequencer, rq memoryTierReq, stmt string) (bool, bool) {
	stmt = cosrememory.ClipStatement(stmt)
	if utf8.RuneCountInString(stmt) < cosrememory.MinStatementRunes {
		emit("answer", map[string]any{"text": "Düzeltme çok kısa (en az " + strconv.Itoa(cosrememory.MinStatementRunes) + " karakter). " + correctionHowToTR})
		return true, true
	}
	h := ruleScopeHints{CtxService: strings.TrimSpace(rq.CtxService)}
	if sc := chatScopeFromCtx(ctx); sc != nil {
		h.ScopeServices, h.ScopeTeam = sc.Services, sc.Team
	}
	h.OwnerTeam = s.serviceOwnerTeam(ctx, h.CtxService)
	c := auth.FromContext(ctx)
	if c != nil {
		if u := s.userByID(ctx, c.UserID); u != nil {
			h.UserTeam = strings.TrimSpace(u.Team)
		}
	}
	def, opts := inferRuleScope(memoryIntent{Text: stmt}, h)
	canSave := canEditRules(c)
	emit("block", seq.Next(blocks.TypeAction, cosreMemoryAction(stmt, def, opts, canSave, copilot.MetaFromContext(ctx).ExchangeID)))
	ans := "Bunu **ekip notu** olarak kaydedebilirim; aşağıda metni ve kapsamı düzenleyebilirsin. **Kaydet**'e basmadan hiçbir şey saklanmaz. " +
		"Kaydedilen not bu kapsamdaki sonraki cevaplara veri olarak eklenir; bugünkü telemetriyle çelişirse telemetri esas alınır ve çelişki söylenir."
	if !canSave {
		ans += " Not kaydetmek editör ya da admin rolü ister; kartı bir editöre iletebilirsin."
	}
	emit("answer", map[string]any{"text": ans})
	return true, true
}
