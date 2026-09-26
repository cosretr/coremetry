package chstore

import "strings"

// ProblemMetricUnit — problem satırındaki sayının BİRİMİ (v0.10.405, CoSRE
// denetimi P4). api'den buraya taşındı ki problemi modele anlatan HER yol
// (tık anında explain, arka plan açıklayıcısı, olay ve runbook satırları)
// aynı birimi yazsın: error_rate yüzde puanı (evaluator `* 100`), gecikme
// aileleri ms, throughput req/s; tanınmayan metrik birimsiz kalır (tahmin
// yok — yanlış birim birimsizden kötü).
func ProblemMetricUnit(metric string) string {
	m := strings.ToLower(metric)
	switch {
	case strings.Contains(m, "error_rate") || strings.HasSuffix(m, "_pct") || strings.Contains(m, "percent"):
		return "%"
	case strings.Contains(m, "p50") || strings.Contains(m, "p95") || strings.Contains(m, "p99") ||
		strings.Contains(m, "latency") || strings.Contains(m, "duration"):
		return " ms"
	case m == "rps" || strings.Contains(m, "throughput") || strings.Contains(m, "req_per_s"):
		return " req/s"
	}
	return ""
}
