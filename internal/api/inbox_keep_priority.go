package api

// inbox_keep_priority.go — v0.10.1072: inbox görünüm kuralının (v0.9.487,
// "exception dışı türler HEP P3") DAR istisnası.
//
// Operatör (prod, 2026-10-02 akşamı): "Dün akşam CRM database'inde sorun
// oldu ama problemlerde P1 gelmedi." Kritik hata oranı anomalisi kaynağında
// P1'di (critical + 14.9× ≥ 2×); inbox onu P3'e çivilemişti. Operatör onayı:
// kaynağı istisna listesindeki (problem_priority.inboxKeepSourcePriority)
// satır kendi P1/P2'sini korur, gerisi bugünkü gibi P3.
//
// Kalıp sözdizimi ve varsayılanlar chstore/problem_priority_inbox.go'da;
// burada yalnız satır → kimlik eşlemesi ve gerekçe metni var.

import "github.com/cilcenk/coremetry/internal/chstore"

// forceNonExceptionP3With — SAF çekirdek (liste dışarıdan). Sıra:
//
//	exception / httperror      → dokunulmaz (kendi merdiveni)
//	zaten P3                   → dokunulmaz (kaynağın gerekçesi daha değerli)
//	kimlik istisna listesinde  → kaynak önceliği KORUNUR, gerekçe söyler
//	diğer her şey              → P3 (v0.9.487)
func forceNonExceptionP3With(items []InboxItem, keep []string) {
	for i := range items {
		it := &items[i]
		if it.Kind == "exception" || it.Kind == "httperror" {
			continue
		}
		if it.Priority == "P3" {
			continue
		}
		orig := it.Priority
		if p, ok := chstore.MatchInboxKeepSourcePriority(keep, inboxKeepSourceID(*it)); ok && (orig == "P1" || orig == "P2") {
			reason := "kaynak önceliği korundu (" + inboxKeepLabel(p, it.Severity) + ")"
			if it.PriorityReason != "" {
				reason += " · " + it.PriorityReason
			}
			it.PriorityReason = reason
			continue
		}
		it.Priority = "P3"
		it.PriorityReason = "tür kuralı: exception/HTTP error dışı kalemler inbox'ta P3 (kaynak önceliği " + orig + ")"
	}
}

// inboxKeepSourceID — satırın istisna listesinde aranan kimliği.
//
//	problem  → Problem.RuleID ("anomaly:<svc>:error_rate", "builtin-…", "db-health:…")
//	incident → "incident:<severity>" (incident'in kural kimliği yok; sınıfı önem)
//	diğer    → "" (anomali olay satırları — trace_op / log_* / behavior_change —
//	            kimliği bilinçli olarak YOK: hiçbir kalıba uyamaz)
func inboxKeepSourceID(it InboxItem) string {
	switch it.Kind {
	case "problem":
		if it.Problem != nil {
			return it.Problem.RuleID
		}
	case "incident":
		if it.Severity != "" {
			return "incident:" + it.Severity
		}
	}
	return ""
}

// inboxKeepLabel — gerekçedeki "neden": varsayılan kalıplar için insan
// cümlesi, operatörün eklediği kalıp için kalıbın kendisi (vidayı görsün).
func inboxKeepLabel(pattern, severity string) string {
	switch pattern {
	case chstore.InboxKeepErrorRateAnomaly:
		if severity == "critical" {
			return "kritik hata oranı"
		}
		return "hata oranı anomalisi"
	case chstore.InboxKeepBuiltin:
		return "yerleşik kural"
	case chstore.InboxKeepDBHealth:
		return "DB sağlık kuralı"
	case chstore.InboxKeepCriticalIncident:
		return "kritik incident"
	case chstore.InboxKeepExtErrorCount: // v0.10.1083
		return "dış kaynak hata serisi"
	case chstore.InboxKeepExtCluster:
		return "dış kaynak hata kümesi"
	case chstore.InboxKeepExtCap:
		return "dış kaynak tavan özeti"
	}
	return "istisna listesi: " + pattern
}
