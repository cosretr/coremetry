package api

// inbox_incidents_retired.go — v0.10.1827 (Incidents emekliliği, audit
// §16.1 E2; operatör kararı 2026-11-05: "Incident sekmesi yerine Anomalies
// olsun; eski incident'lar devre dışı kalsın"). Ana anahtar
// (chstore.IncidentsEnabled) KAPALIYKEN Problems listesi (/inbox):
//   - incident kaynağını okumaz, incident satırı üretmez;
//   - Problem'leri incident altına katlamaz (her Problem kendi satırı);
//   - `incident` tür sözlüğünden ve varsayılandan çıkar; yalnız
//     `?kind=incident` taşıyan eski bağlantı hatasız varsayılan görünüme
//     düşer;
//   - rozet ve liste anahtarı anahtar durumunu taşır (açık/kapalı iki
//     görünüm aynı önbellek girdisini paylaşmaz).
// Açıkken davranış aynen (v0.10.1796 varsayılanı problem+anomaly+incident).

var inboxKindsAllRetired = []string{"problem", "exception", "httperror", "anomaly"}

// inboxKindsDefaultRetired — anahtar kapalıyken parametresiz açılış ve rozet.
var inboxKindsDefaultRetired = []string{"problem", "anomaly"}

// inboxKindsAllFor — SAF: tür sözlüğü.
func inboxKindsAllFor(incidentsOn bool) []string {
	if incidentsOn {
		return inboxKindsAll
	}
	return inboxKindsAllRetired
}

// inboxKindsDefaultFor — SAF: varsayılan tür kümesi (sayfa + rozet).
func inboxKindsDefaultFor(incidentsOn bool) []string {
	if incidentsOn {
		return inboxKindsDefault
	}
	return inboxKindsDefaultRetired
}

// inboxKindsFromQuery — SAF: `?kind=` çözümü. Anahtar kapalıyken yalnız
// emekli türleri (incident) taşıyan bağlantı tüm türlere değil VARSAYILAN
// görünüme düşer (normalizeInboxSet'in "geçerli token yok = hepsi" kuralı
// eski /inbox?kind=incident linkini exception kalabalığına açardı). Boş
// ya da tamamen anlamsız `kind` bugünkü gibi tüm sözlük.
func inboxKindsFromQuery(raw string, incidentsOn bool) []string {
	if !incidentsOn {
		legacy := normalizeInboxSet(raw, inboxKindsAll)
		if len(legacy) == 1 && legacy[0] == "incident" {
			return append([]string(nil), inboxKindsDefaultRetired...)
		}
	}
	return normalizeInboxSet(raw, inboxKindsAllFor(incidentsOn))
}

// inboxReadsIncidents — SAF: incident kaynağı okunur mu ve katlama koşar mı.
// Bugünkü koşullar (ignored pivotu ve db şeridi incident çekmez) + ana anahtar.
func inboxReadsIncidents(incidentsOn bool, statusFilter, subject string) bool {
	return incidentsOn && statusFilter != "ignored" && subject == inboxSubjectService
}
