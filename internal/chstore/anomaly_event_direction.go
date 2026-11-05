package chstore

// anomaly_event_direction.go — v0.10.1792 (Davis paritesi FAZ 1.4): yönlü
// olayın okuyucu tarafı. "Taban yok"u ratio=0 ile kodlamak (1587 tuzağı) iki
// farklı durumu tek değere sıkıştırıyordu: "taban yok (yeni)" ve "tabana göre
// SIFIRA düştü". Yeni satırlar baseline_kind'i açık taşır; eski satırlar
// (baseline_kind '') eski yorumla okunur — ratio=0 = taban yok.
//
// Bu dosya ikisinin TEK karar noktası: öncelik (inbox), terfi
// (IsCountOnlyAnomaly), standart başlık ve SRE okuyucusu buradan okur.

import "math"

// SubjectAnomalyKind — v0.10.1792: özne (endpoint / db / queue) tabanının
// olay türü. Pattern = metrik (endpoint'te "metrik @ rota"), subject_kind /
// subject_id kolonları özneyi taşır. Karar 8 (operatör 2026-10-29): ham
// anomali iç sinyal katmanıdır — bu tür FAZ 1'de Problem'e TERFİ ETMEZ
// (IsSubjectAnomaly, evaluator promotionGate).
const SubjectAnomalyKind = "subject_red"

// IsSubjectAnomaly — olay özne tabanından mı.
func IsSubjectAnomaly(ev AnomalyEvent) bool { return ev.Kind == SubjectAnomalyKind }

// AnomalyBaselineless — SAF: olayın tabanı YOK mu (yalnız sayım / ilk
// görülme). Yeni satır: baseline_kind == "none". Eski satır (baseline_kind
// ” — kolondan önce ya da kolonu görmeyen pod): eski yorum, peak_ratio == 0.
// Tabanı bilinen (seasonal / consecutive) satırda ratio 0 "sıfıra düştü"
// demektir, tabansızlık DEĞİL.
func AnomalyBaselineless(ev AnomalyEvent) bool {
	switch ev.BaselineKind {
	case AnomalyBaselineNone:
		return true
	case "":
		return ev.PeakRatio == CountOnlyRatio
	}
	return false
}

// AnomalyRatioKnown — SAF: peak_ratio anlamlı bir "normalin N katı" mı.
func AnomalyRatioKnown(ev AnomalyEvent) bool {
	return !AnomalyBaselineless(ev) && ev.PeakRatio > 0 && !math.IsNaN(ev.PeakRatio) && !math.IsInf(ev.PeakRatio, 0)
}

// AnomalyDirectionOfRatio — SAF: oranın yönü (≥ 1 artış, < 1 düşüş); oran
// yok / bozuk → "" (bilinmiyor).
func AnomalyDirectionOfRatio(ratio float64) string {
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return ""
	}
	if ratio < 1 {
		return AnomalyDirectionDown
	}
	return AnomalyDirectionUp
}
