package api

// cosre_briefing_ask.go — v0.10.1716 (CoSRE Login Briefing dilim 7, briefing-
// audit "Tasarım önerisi" UI kartı): brifing satırının "Sor"u composer'ı
// doldurur ve isteğe context.briefingId + briefingLine taşır. v2 döngüsü:
//   • kart (cosre_briefings FINAL, nokta okuması) yeniden okunur; kimlik
//     kartınkiyle eşleşirse satırın SAYISIZ bağlamı (kalem + ≤3 özne adı)
//     döngü ekine girer (briefing.LineAskRef → copilot.ChatBriefingLineAddendum).
//     Sayılar LLM'e ek olarak verilmez; model aynı pencereyi araçla okur.
//   • sistem sinyali briefing_ask: exchange_id = briefing ULID (tasarım §8,
//     cosre_briefings.briefing_id ile birleşir), ayrıntı sohbet alışverişinin
//     kimliğini taşır → sohbetin kendi sinyalleri o satıra bağlanır.
// Kimlik yok / geçersiz → hiçbir şey (eski davranış bayt bayt).

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosre/briefing"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

const briefingAskReadTimeout = 2 * time.Second

// briefingAskFromPayload — SAF: kart yükü + istemci kimliği/satırı → ek + kalem.
// Eşleşmeyen kimlik, satır dışı sıra ya da bozuk yük → ("", "", false).
func briefingAskFromPayload(payload, id string, line int) (addendum, item string, ok bool) {
	var b briefing.Briefing
	if json.Unmarshal([]byte(payload), &b) != nil {
		return "", "", false
	}
	ref, ok := briefing.LineAskRef(b, id, line)
	if !ok {
		return "", "", false
	}
	return copilot.ChatBriefingLineAddendum(ref.Item, ref.Subjects), ref.Item, true
}

// briefingAskSignal — SAF: birleşim satırı (exchange_id = briefing ULID).
func briefingAskSignal(id, xid string, line int, item string, matched bool) chstore.CosreSignal {
	m := 0
	if matched {
		m = 1
	}
	return chstore.CosreSignal{
		ExchangeID: id, Kind: cosrelearn.SigBriefingAsk, UserEmail: cosrelearn.SystemSignalActor,
		Detail: fmt.Sprintf("exchange=%s line=%d item=%s matched=%d", xid, line, item, m),
	}
}

// briefingAskAddendum — v2 döngü planının brifing eki ("" = ek yok).
func (s *Server) briefingAskAddendum(ctx context.Context, us userScope, env, id string, line *int) string {
	if line == nil || !briefing.ValidID(id) {
		return ""
	}
	addendum, item, matched := "", "", false
	if !us.Unscoped && s.store != nil {
		rctx, cancel := context.WithTimeout(ctx, briefingAskReadTimeout)
		teamKey := s.teamAliasesCtx(rctx).CanonTeam(us.Team)
		row, ok, err := s.store.GetCosreBriefing(rctx, teamKey, env, time.Now().Add(-briefing.MaxTTLS*time.Second))
		cancel()
		if err == nil && ok {
			addendum, item, matched = briefingAskFromPayload(row.Payload, id, *line)
		}
	}
	s.writeSystemSignals(ctx, id, []chstore.CosreSignal{
		briefingAskSignal(id, copilot.MetaFromContext(ctx).ExchangeID, *line, item, matched),
	})
	return addendum
}
