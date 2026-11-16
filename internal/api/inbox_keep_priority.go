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
			label := inboxKeepLabelMsg(p, it.Severity)
			if it.PriorityReason != "" {
				reason += " · " + it.PriorityReason
				it.PriorityReasonMsg = nestedReasonMsg("prio.keepR", label, it.PriorityReasonMsg)
			} else {
				it.PriorityReasonMsg = chstore.NewStdMsg("prio.keep", "labelM", label)
			}
			it.PriorityReason = reason
			continue
		}
		it.Priority = "P3"
		it.PriorityReason = "tür kuralı: exception/HTTP error dışı kalemler inbox'ta P3 (kaynak önceliği " + orig + ")"
		it.PriorityReasonMsg = chstore.NewStdMsg("prio.forceP3", "orig", orig)
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

// nestedReasonMsg — v0.10.2300 (S3): "önek · iç gerekçe" anahtarı. İç
// gerekçenin anahtarı yoksa nil: EN arayüz bütün cümleyi sunucu metniyle
// çizer (yarım çeviri yok). label nil değilse "labelM" olarak taşınır.
func nestedReasonMsg(k string, label, inner *chstore.StdMsg) *chstore.StdMsg {
	if inner == nil {
		return nil
	}
	if label != nil {
		return chstore.NewStdMsg(k, "labelM", label, "rM", inner)
	}
	return chstore.NewStdMsg(k, "rM", inner)
}

// inboxKeepLabelMsg — inboxKeepLabel'ın anahtar ikizi (aynı dallar).
func inboxKeepLabelMsg(pattern, severity string) *chstore.StdMsg {
	switch pattern {
	case chstore.InboxKeepErrorRateAnomaly:
		if severity == "critical" {
			return chstore.NewStdMsg("keep.errRateCritical")
		}
		return chstore.NewStdMsg("keep.errRate")
	case chstore.InboxKeepBuiltin:
		return chstore.NewStdMsg("keep.builtin")
	case chstore.InboxKeepDBHealth:
		return chstore.NewStdMsg("keep.dbHealth")
	case chstore.InboxKeepCriticalIncident:
		return chstore.NewStdMsg("keep.critIncident")
	case chstore.InboxKeepExtErrorCount:
		return chstore.NewStdMsg("keep.extErrors")
	case chstore.InboxKeepExtCluster:
		return chstore.NewStdMsg("keep.extCluster")
	case chstore.InboxKeepExtCap:
		return chstore.NewStdMsg("keep.extCap")
	case chstore.InboxKeepSvcSlowdown:
		return chstore.NewStdMsg("keep.svcSlowdown")
	case chstore.InboxKeepMLGroup:
		return chstore.NewStdMsg("keep.mlGroup")
	case chstore.InboxKeepMLSvc:
		return chstore.NewStdMsg("keep.mlSvc")
	}
	return chstore.NewStdMsg("keep.list", "pattern", pattern)
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
	case chstore.InboxKeepSvcSlowdown: // v0.10.1091
		return "yaygın yavaşlama"
	case chstore.InboxKeepMLGroup: // v0.10.2056
		return "anomali grubu: birden çok sinyal"
	case chstore.InboxKeepMLSvc: // v0.10.2130
		return "ML servis düzeyi anomali"
	}
	return "istisna listesi: " + pattern
}
