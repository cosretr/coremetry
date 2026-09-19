package chstore

import "time"

// slo_burn_policy.go — SLO burn-rate pencereleri ve eşikleri, TEK KAYNAK
// (v0.10.794; dış skill denetimi 2026-09-19 V5).
//
// Öncesi: evaluator kendi `burnPolicies` dizisini (1 sa/6 sa · 6 sa/24 sa)
// taşıyor, /api/copilot/explain-slo ise 5 dk / 1 sa ölçüyordu ve SLO
// modalı "fast/slow burn"ü etiketsiz basıyordu — operatör problemde ve
// modalda farklı sayılar görüyordu. Pencereler burada yaşar; evaluator
// alarmı, açıklama ucu ve FE etiketi aynı çifti okur.
//
// Değerler Google SRE Workbook §5 (30 günlük pencere):
//   - critical: 1 sa burn > 14.4 (bütçe ~2 günde biter) VE 6 sa > 6 (~5 gün)
//   - warning:  6 sa burn > 6 VE 24 sa > 3 — yavaş damlama
//
// İki pencere birlikte ateşlemeli: tek kovalık sapmayı bastırmanın yolu bu.
type BurnPolicy struct {
	Severity   string
	FastWindow time.Duration
	FastRate   float64
	SlowWindow time.Duration
	SlowRate   float64
}

// BurnPolicies — sırası şiddet sırasıdır (critical önce); evaluator her SLO
// için ikisini de değerlendirir, her biri kendi `slo:<id>:<severity>` kural
// kimliğini açar.
var BurnPolicies = []BurnPolicy{
	{Severity: "critical", FastWindow: 1 * time.Hour, FastRate: 14.4, SlowWindow: 6 * time.Hour, SlowRate: 6.0},
	{Severity: "warning", FastWindow: 6 * time.Hour, FastRate: 6.0, SlowWindow: 24 * time.Hour, SlowRate: 3.0},
}

// BurnExplainPolicy — açıklama modalı ve explain-slo ucunun ölçtüğü çift:
// critical bandı (operatörün problemde gördüğü "burn_rate_60m" ile aynı).
func BurnExplainPolicy() BurnPolicy { return BurnPolicies[0] }
