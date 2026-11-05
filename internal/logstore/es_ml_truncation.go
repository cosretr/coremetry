package logstore

// es_ml_truncation.go — v0.10.1812 (öğrenme döngüsü audit §13 D3): ML log
// agregasyonlarındaki servis `terms`'i MaxServices ile kesiliyor; aşan
// servisler doc_count sırasıyla SESSİZ düşüyordu. Tik ölçümü artık kaç
// servisin tavan dışında kaldığını söyler.
//
// SİNYAL SEÇİMİ: `sum_other_doc_count` (terms yanıtında zaten var, bedava)
// kesilme olup olmadığını KESİN söyler (> 0 = en az bir servis dışarıda) ama
// kaç servis olduğunu söylemez. Sayı için aynı istekte kardeş `cardinality`
// agregasyonu (HLL; servis alanı keyword → global ordinals, binlerce değerde
// ucuz; precision_threshold altındaki sayımlar neredeyse kesin). Kardinalite
// yalnız tik isteklerinde istenir (CountServices) — 21/56 günlük backfill
// ve 14 günlük kod geçmişi bu bedeli taşımaz.

// mlCardinalityPrecision — tavanın iki katı (en az 1000, ES üst sınırı 40000).
func mlCardinalityPrecision(maxServices int) int {
	p := 2 * maxServices
	if p < 1000 {
		p = 1000
	}
	if p > 40000 {
		p = 40000
	}
	return p
}

// mlServiceCardinalityAgg — SAF: kardeş agregasyon gövdesi.
func mlServiceCardinalityAgg(svcField string, maxServices int) map[string]any {
	return map[string]any{"cardinality": map[string]any{"field": svcField, "precision_threshold": mlCardinalityPrecision(maxServices)}}
}

// MLDroppedServices — SAF: tavan dışında kalan servis sayısı.
//
//	sumOtherDocs ≤ 0                  → 0 (kesilme yok; HLL hatası sayılmaz)
//	sayım var ve distinct > returned  → distinct − returned
//	aksi (sayım yok / HLL eksik saydı) → 1 ("en az bir")
func MLDroppedServices(returned int, sumOtherDocs int64, distinct int64, counted bool) int {
	if sumOtherDocs <= 0 {
		return 0
	}
	if counted && distinct > int64(returned) {
		return int(distinct - int64(returned))
	}
	return 1
}
