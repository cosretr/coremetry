package api

// anomalies_unified_attached.go — v0.10.2481 (Problems tek sayfa, Sinyaller
// sekmesi): "Bağlanmamış anomaliler" = açık bir Problem'in ÜYESİ olmayan
// olaylar. /api/anomalies/unified?unattached=1 (yalnız servis görünümü)
// birleşik satırı, üyelerinden biri açık bir Problem'e bağlıysa düşürür.
//
// Üyelik bugün nasıl biliniyor (yeni bağ UYDURULMAZ, mevcut üç iz okunur):
//   - ML olayı: kanıttaki Problem damgası (chstore.MLEventProblemID — üye
//     olayının problem_id'si ya da canlı groupShadow / svcShadow damgası;
//     Problem detayının olay çizelgesi ve HasLiveMembers aynı işlevi okur).
//   - Eski dedektör olayı, kendi Problem'i: terfi kuralı
//     "anomaly-auto:<olay id>" ile (servis) açık satır (evaluator
//     promoteStrongAnomalies).
//   - Eski dedektör olayı, başka sahibe katılmış: anomaly_promotion üye
//     olayı (kural "anomaly-auto:<olay id>", problem_id sahip; stage closed =
//     üyelikten ayrıldı).
// "Açık" = chstore.OpenProblemsSnapshot (open | acknowledged; 5 sn memo,
// LIMIT 50000). Bağ kurulamayan tek aile: özne grubu terfisi
// ("anomaly-auto:subject:<anahtar>") — kural olay kimliği taşımaz; o olaylar
// bağlanmamış sayılır (DECISIONS 2026-11-26).
//
// Sınır: süzgeç birleşik satırların TAMAMINDA, sayfalamadan ÖNCE çalışır
// (toplamlar süzgeç sonrası kesin); ek okuma tek, sınırlı: üye olayları
// (detector = anomaly_promotion, son 24 sa, MLUnifiedCap).

import (
	"context"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

// openProblemIndex — açık Problem araması (*chstore.OpenProblems karşılar).
type openProblemIndex interface {
	ByID(id string) *chstore.Problem
	ByKey(ruleID, service string) *chstore.Problem
}

// promotedLegacyMembers — SAF: açık bir sahibe katılmış eski olayların
// kimlikleri (anomaly_promotion üye olaylarından; kapanmış üyelik sayılmaz).
func promotedLegacyMembers(members []chstore.MLEvent, open openProblemIndex) map[string]bool {
	out := map[string]bool{}
	for _, m := range members {
		if m.Detector != chstore.MLDetMemberPromotion || m.Stage == chstore.MLStageClosed {
			continue
		}
		ev, ok := chstore.MemberEvidenceOf(m)
		if !ok || open.ByID(ev.ProblemID) == nil {
			continue
		}
		if id, ok := strings.CutPrefix(ev.RuleID, chstore.PromotedAnomalyRulePrefix); ok && id != "" {
			out[id] = true
		}
	}
	return out
}

// mlAttached — SAF: ML olayı açık bir Problem'e bağlı mı.
func mlAttached(e chstore.MLEvent, open openProblemIndex) bool {
	id := chstore.MLEventProblemID(e)
	return id != "" && open.ByID(id) != nil
}

// legacyAttached — SAF: eski olay açık bir Problem'e bağlı mı (kendi terfi
// satırı ya da başka sahibe üyelik).
func legacyAttached(e chstore.AnomalyEvent, open openProblemIndex, promoted map[string]bool) bool {
	if promoted[e.ID] {
		return true
	}
	return open.ByKey(chstore.PromotedAnomalyRulePrefix+e.ID, e.Service) != nil
}

// dropAttachedUnified — SAF: üyelerinden biri açık bir Problem'e bağlı olan
// birleşik satırları düşürür (satır bölünmez: grup ya hep görünür ya hiç).
func dropAttachedUnified(rows []ml.UnifiedRow, items []chstore.MLEvent, legacy []chstore.AnomalyEvent, open openProblemIndex, promoted map[string]bool) []ml.UnifiedRow {
	out := rows[:0:0]
	for _, r := range rows {
		if !unifiedRowAttached(r, items, legacy, open, promoted) {
			out = append(out, r)
		}
	}
	return out
}

func unifiedRowAttached(r ml.UnifiedRow, items []chstore.MLEvent, legacy []chstore.AnomalyEvent, open openProblemIndex, promoted map[string]bool) bool {
	for _, j := range r.ML {
		if j >= 0 && j < len(items) && mlAttached(items[j], open) {
			return true
		}
	}
	for _, j := range r.Legacy {
		if j >= 0 && j < len(legacy) && legacyAttached(legacy[j], open, promoted) {
			return true
		}
	}
	return false
}

// unattachedUnified — açık Problem anlık görüntüsü + promotion üye olayları
// okunur ve bağlı satırlar düşer. Okunamazsa süzgeç UYGULANMAZ (liste tam
// gelir) ve yanıt partial="problems" taşır — eksik süzgeç sessiz kalmaz ve
// önbelleğe girmez (partial yanıt uncacheable).
func (s *Server) unattachedUnified(ctx context.Context, rows []ml.UnifiedRow, items []chstore.MLEvent, legacy []chstore.AnomalyEvent, since time.Time, resp *unifiedAnomaliesResponse) []ml.UnifiedRow {
	snap, err := s.store.OpenProblemsSnapshot(ctx)
	if err != nil {
		resp.Partial = "problems"
		return rows
	}
	var promoted map[string]bool
	if len(legacy) > 0 {
		members, err := s.store.ListMLEvents(ctx, chstore.MLEventsFilter{
			Since: since, Detector: chstore.MLDetMemberPromotion, Members: true, Limit: chstore.MLUnifiedCap})
		if err != nil {
			resp.Partial = "problems"
			return rows
		}
		promoted = promotedLegacyMembers(members, snap)
	}
	return dropAttachedUnified(rows, items, legacy, snap, promoted)
}
