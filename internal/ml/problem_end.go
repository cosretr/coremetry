package ml

// problem_end.go — v0.10.2476 (operatör: "problemler kapanınca ayrıca
// gözüksün, aktifler arasında kalmasın"; prod ekranı: olay çizelgesindeki
// her satır "(bitti)" iken P1 Problem hâlâ açık).
//
// KÖK NEDEN: ML Problem'inin "grup bitti" kararı (DecideGroupPromotion →
// Finished) süreç-içi koşu durumuna bakar (tracker.openIDs). Ailesi veri
// değerlendiremeyen koşu (no-data / runBlind) kaçırma saymaz, noDataHoldMax
// (3 sa) boyunca AÇIK tutulur. O süre boyunca grup "bitmedi" sayılır, plan
// Problem satırını her tik tazeler (updated_at taze → evaluator'ın bayat
// süpürmesi de dokunmaz), oysa olayın kendisi (ml_events last_seen) çoktan
// donmuştur: çizelge ve /anomalies aynı olayı MLEventOpen kuralıyla
// "bitti" gösterir. Sonuç: tüm olayları bitmiş Problem 3 saate kadar Açık
// görünümünde kalır.
//
// KURAL: Problem kararı için bir koşu, olayı MLEventOpen'a göre bitmiş VE
// son görülmesinden bu yana problemEndedGrace (15 dk) geçmişse açık
// SAYILMAZ. Grup böylece "bitti" olur ve mevcut kapanış yolu (lider
// sürekliliği, HoldResolve, dropNoDataResolves, kapanış gerekçesi, mevcut
// bildirim davranışı) aynen çalışır — yeni kapanış yolu, yeni bildirim yok.
// Olay yeniden görülürse (tracker.observe) koşu canlanır, grup yeniden
// "açardı" der ve aynı kimlikli Problem mevcut açılış yolundan yeniden açılır.

import (
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// problemEndedGrace — tüm olayları bitmiş Problem'in kapanmadan önce
// beklediği süre (operatör kuralı: "tüm üyeler ≥ 15 dk bitmiş → kapanır").
const problemEndedGrace = 15 * time.Minute

// problemOpenIDs — SAF: Problem kararının "açık koşu" kümesi. open: koşu
// durumu (tracker.openIDs); running: koşuların son olayları; kept: bu tikte
// görülen olaylar (her zaman açık). Olayı bitmiş ve son görülmesi grace'ten
// eski kimlikler düşer; olayı bilinmeyen kimlik (yalnız open'da) olduğu gibi
// kalır. Girdi değiştirilmez.
func problemOpenIDs(open map[string]bool, running, kept []chstore.MLEvent, now time.Time) map[string]bool {
	if len(open) == 0 {
		return open
	}
	seen := make(map[string]bool, len(kept))
	for _, e := range kept {
		seen[e.ID] = true
	}
	nowNs := now.UnixNano()
	var ended map[string]bool
	for _, e := range running {
		if !open[e.ID] || seen[e.ID] || chstore.MLEventOpen(e, nowNs) || nowNs-e.LastSeen < int64(problemEndedGrace) {
			continue
		}
		if ended == nil {
			ended = map[string]bool{}
		}
		ended[e.ID] = true
	}
	if len(ended) == 0 {
		return open
	}
	out := make(map[string]bool, len(open))
	for id, v := range open {
		if !ended[id] {
			out[id] = v
		}
	}
	return out
}
