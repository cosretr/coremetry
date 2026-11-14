package chstore

// ml_group_problem.go — v0.10.2052 (anomali tekilleştirme D4): ML grubunun
// açtığı Problem'in (`anomaly-auto:group:`) evaluator süpürmeleriyle sınırı.
//
// Yaşam döngüsünün sahibi ML kaydedicisi (internal/ml/group_live.go): her ML
// tikinde (varsayılan 300 sn) tazeler, grup bitince kapatır. Evaluator'ın
// bayat süpürmesi 3 × evaluator aralığında (≈ 3 dk) tazelenmeyeni "source
// silent" diye kapatır — ML tiki bundan seyrek olduğu için muafiyet olmasa
// her tik kapanıp yeniden açılır ve her açılış bildirim olurdu (/aiops §2
// soru 1). Muafiyet YAŞAYAN kaynağa bağlı: ML tikinin 3 katı + 5 dk
// tazelenmeyen satır yine süpürülür (ML durdu / anahtar kapandı).

import "time"

// MLGroupStaleAfter — SAF: grup Problem'inin bayat sayılma süresi.
func MLGroupStaleAfter(tickSec int) time.Duration {
	if tickSec <= 0 {
		tickSec = DefaultMLConfig().TickSec
	}
	return 3*time.Duration(tickSec)*time.Second + 5*time.Minute
}

// IsMLGroupProblem — SAF: kural kimliği ML grup Problem'i mi.
func IsMLGroupProblem(ruleID string) bool {
	return len(ruleID) > len(MLGroupRulePrefix) && ruleID[:len(MLGroupRulePrefix)] == MLGroupRulePrefix
}

// IsMLSvcProblem — SAF (v0.10.2130): kural kimliği servis düzeyi ML
// Problem'i mi (`anomaly-auto:svc:<servis>`).
func IsMLSvcProblem(ruleID string) bool {
	return len(ruleID) > len(MLSvcRulePrefix) && ruleID[:len(MLSvcRulePrefix)] == MLSvcRulePrefix
}

// IsMLLiveProblem — SAF (v0.10.2130): yaşam döngüsü ML kaydedicisinde olan
// Problem (grup VEYA servis düzeyi). Evaluator süpürmeleri (bayat +
// terfi kapanışı) bu yüklemle muaf tutar — tek karar noktası.
func IsMLLiveProblem(ruleID string) bool { return IsMLGroupProblem(ruleID) || IsMLSvcProblem(ruleID) }

// SplitMLGroupStale — SAF: bayat listesinden ML grup Problem'lerini ayırır.
// veryStale: ML eşiğini (MLGroupStaleAfter) de aşmış kimlikler; nil =
// bilinmiyor → hiçbir grup Problem'i süpürülmez (kör kapanış, bir tik
// gecikmeden kötü). Grup dışı satırlar sırasıyla aynen döner.
func SplitMLGroupStale(stale []Problem, veryStale map[string]bool) (sweep, exempt []Problem) {
	for _, p := range stale {
		if IsMLLiveProblem(p.RuleID) && !veryStale[p.ID] {
			exempt = append(exempt, p)
			continue
		}
		sweep = append(sweep, p)
	}
	return sweep, exempt
}
