package evaluator

import (
	"context"
	"log"
)

// selfhealth_skipmat.go — v0.10.2424: self-skipidx-materialize.
//
// Arka plan MATERIALIZE INDEX|PROJECTION işi (chstore/skipidx_materialize.go)
// bir partition'da üst üste ≥ 3 kez başarısız olduysa ya da iş "failed"e
// düştüyse (deneme hakkı biten partition) not açılır; operatör Admin →
// ClickHouse'ta "yeniden dene" / "iptal" deyince ya da iş ilerleyince kapanır.
// Durum system_settings'ten okunur; okunamazsa kural ölçülmemiş sayılır
// (açık satır kapanmaz, v0.9.984).

const selfSkipMatRuleID = "self-skipidx-materialize"

func (e *Evaluator) selfSkipMat(ctx context.Context) ([]selfProblem, bool) {
	bad, note, err := e.store.SkipMatHealth(ctx)
	if err != nil {
		log.Printf("[evaluator] self-health materialize işi okunamadı: %v", err)
		return nil, false
	}
	if !bad {
		return nil, true
	}
	return []selfProblem{{
		id:         selfSkipMatRuleID,
		ruleID:     selfSkipMatRuleID,
		ruleName:   "Coremetry · arka plan MATERIALIZE işi başarısız",
		metric:     "self.skipidx_materialize_failures",
		comparator: ">",
		severity:   "warning",
		value:      1,
		threshold:  0,
		description: "Skip indeks / projection materialize işi ilerlemiyor: " + note +
			". Yeni part'lar indeksle yazılmaya devam eder; yalnız eski partition'lar budanmaz. " +
			"Müdahale: Admin → ClickHouse → Arka plan MATERIALIZE işleri (yeniden dene / iptal); " +
			"system.mutations'ta latest_fail_reason.",
	}}, true
}
