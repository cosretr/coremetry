package chstore

// incident_priority.go — ilan edilen incident'ın P1/P2/P3 önceliği, TEK
// yerde (v0.10.796; dış skill denetimi 2026-09-19 I12).
//
// Öncesi: eşleme yalnız Inbox'ın incidentToInbox'ında yaşıyordu; Incidents
// listesi ve Incident detayı yalnız şiddet pili çiziyordu, P rozeti yoktu —
// v0.10.364 dersi: "kural görünür değilse yoktur". Şimdi üç yüzey aynı saf
// işlevi okur; gerekçe cümlesi her satırla gider (CLAUDE.md triage).
//
// Kural: ilan edilen şiddet → critical P1, warning P2, info P3. İnsan
// kararıdır (eşikten yeniden türetilmez). Acknowledged incident ÜZERİNDE
// ÇALIŞILIYORDUR: kuyruktan düşmez ama bir basamak iner (P1→P2, P2→P3);
// resolved için öncelik yine ilan edilen (tarihçe okunurken anlamlı kalsın).
func IncidentPriority(inc Incident) (prio, reason string) {
	prio, reason = "P3", "Declared incident"
	switch inc.Severity {
	case "critical":
		prio, reason = "P1", "Declared incident, critical"
	case "warning":
		prio, reason = "P2", "Declared incident, warning"
	}
	if inc.Status == "acknowledged" {
		switch prio {
		case "P1":
			prio, reason = "P2", "Declared incident, critical — acknowledged"
		case "P2":
			prio, reason = "P3", "Declared incident, warning — acknowledged"
		}
	}
	return prio, reason
}

// EnrichIncidentsWithPriority — okuma-anı zenginleştirme (liste + detay,
// EnrichIncidentsWithRootCause ile aynı desen): Priority/PriorityReason
// alanları DB'de yok, yalnız JSON'da.
func EnrichIncidentsWithPriority(rows []Incident) []Incident {
	for i := range rows {
		rows[i].Priority, rows[i].PriorityReason = IncidentPriority(rows[i])
	}
	return rows
}
