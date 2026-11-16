package chstore

// incident_priority.go — ilan edilen incident'ın P1/P2/P3 önceliği, TEK
// yerde (v0.10.796; dış skill denetimi 2026-09-19 I12).
//
// Öncesi: eşleme yalnız Inbox'ın incidentToInbox'ında yaşıyordu; Incidents
// listesi ve Incident detayı yalnız şiddet pili çiziyordu, P rozeti yoktu —
// v0.10.364 dersi: "kural görünür değilse yoktur". Liste + detay
// yüzeyleri Incidents emekliliğiyle silindi; okuyan yalnız Inbox
// (incidentToInbox).
//
// Kural: ilan edilen şiddet → critical P1, warning P2, info P3. İnsan
// kararıdır (eşikten yeniden türetilmez). Acknowledged incident ÜZERİNDE
// ÇALIŞILIYORDUR: kuyruktan düşmez ama bir basamak iner (P1→P2, P2→P3);
// resolved için öncelik yine ilan edilen (tarihçe okunurken anlamlı kalsın).
func IncidentPriority(inc Incident) (prio, reason string) {
	prio, reason, _ = incidentPriorityMsg(inc)
	return prio, reason
}

// IncidentPriorityMsg — v0.10.2300 (sunucu metinleri S3): IncidentPriority
// gerekçesinin metin anahtarı (aynı dallar, tek çekirdek).
func IncidentPriorityMsg(inc Incident) *StdMsg {
	_, _, m := incidentPriorityMsg(inc)
	return m
}

func incidentPriorityMsg(inc Incident) (prio, reason string, msg *StdMsg) {
	prio, reason, msg = "P3", "Declared incident", stdM("prio.inc.declared")
	switch inc.Severity {
	case "critical":
		prio, reason, msg = "P1", "Declared incident, critical", stdM("prio.inc.critical")
	case "warning":
		prio, reason, msg = "P2", "Declared incident, warning", stdM("prio.inc.warning")
	}
	if inc.Status == "acknowledged" {
		switch prio {
		case "P1":
			prio, reason, msg = "P2", "Declared incident, critical — acknowledged", stdM("prio.inc.criticalAck")
		case "P2":
			prio, reason, msg = "P3", "Declared incident, warning — acknowledged", stdM("prio.inc.warningAck")
		}
	}
	return prio, reason, msg
}
