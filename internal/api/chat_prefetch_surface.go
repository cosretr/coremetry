package api

// chat_prefetch_surface.go — v0.10.2134 (CoSRE router boşlukları D1,
// docs/cosre/router-gaps-audit.md A2/M).
//
// Operasyon / endpoint ön-getirmesi (chatOperationPrefetch) ya da v2 rota
// planı (planRouteTurn) eşleştiğinde serbest döngü veriyle TOHUMLANIR: router
// soruyu yakalamıştır. Döngünün ai_calls satırı yine de "chat" yazıyordu ve
// /ai › Router boşlukları onu boşluk sayıyordu (7 günde ~4/54). Artık satır
// "chat-prefetch" etiketiyle yazılır (RouterGap değil). Döngü, istem ve
// profil AYNI — yalnız kayıt etiketi (copilot.Service.RecordAs profili
// sabitler). Değerlendirme kipi etiketi (evalset-*) dokunulmaz.

import (
	"context"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// prefetchSeededLoop — SAF: döngü satırı "chat-prefetch" mi yazmalı? Yalnız
// üretim "chat" etiketi + tohumlanmış tur; tohumsuz tur ve değerlendirme
// etiketi (evalset-*) aynen.
func prefetchSeededLoop(cur string, seeded bool) bool {
	return seeded && cur == aisurface.Chat
}

// loopRecordCtx — döngünün RecordUsage ctx'i: tohumlu turda etiket
// chat-prefetch (profil sabit), aksi hâlde ctx aynen.
func (s *Server) loopRecordCtx(ctx context.Context, seeded bool) context.Context {
	if s == nil || s.copilot == nil || !prefetchSeededLoop(copilot.MetaFromContext(ctx).Surface, seeded) {
		return ctx
	}
	return s.copilot.RecordAs(ctx, aisurface.ChatPrefetch)
}
