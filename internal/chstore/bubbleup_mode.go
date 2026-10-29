package chstore

// bubbleup_mode.go — v0.10.1577 (dış skill denetimi V1 dilim 2): BubbleUp
// kıyas türünün SAF kararı api paketinden buraya taşındı. Neden: kararı artık
// iki paket okuyor — api (/rootcause paneli, v0.10.1495) ve anomaly
// (sentezleyicinin derin kanıtı); api anomaly'yi import ettiği için karar
// ikisinin ortak bağımlılığında yaşamalı (tersi import döngüsü). Gövde
// v0.10.1495'teki rootCauseBubbleMode'un AYNISI; api ince sarmalayıcı tutar.
//
//   - errors — hata problemi: aynı pencerede hatalı span'ler tüm span'lere karşı.
//   - window — gecikme / trafik / diğer servis metrikleri: pencere, ÖNCEKİ
//     eş-boy pencereye karşı. Hata dili YOK.
//   - none   — span attribute'larının açıklayamadığı türler (runtime, db
//     kapasite/sağlık, monitor, self-health, servis olmayan özne). Ham spans
//     taraması HİÇ koşmaz.

import "strings"

// BubbleMode — BubbleUp kıyasının türü.
type BubbleMode string

const (
	BubbleModeErrors BubbleMode = "errors"
	BubbleModeWindow BubbleMode = "window"
	BubbleModeNone   BubbleMode = "none"
)

// bubbleNonePrefixes — span attribute kıyası anlamsız olan kural/problem id
// önekleri (aiops §3 aileleri).
var bubbleNonePrefixes = []string{"runtime:", "db-capacity:", "db-health:", "monitor:", "self-"}

// ProblemBubbleMode — SAF (bubbleup_mode_test.go tablo).
func ProblemBubbleMode(p *Problem) BubbleMode {
	if p == nil || p.Service == "" || (p.Kind != "" && p.Kind != ProblemKindService) {
		return BubbleModeNone
	}
	for _, id := range []string{p.ID, p.RuleID} {
		for _, pre := range bubbleNonePrefixes {
			if strings.HasPrefix(id, pre) {
				return BubbleModeNone
			}
		}
	}
	if ExemplarKindForMetric(p.Metric) == ExemplarError {
		return BubbleModeErrors
	}
	return BubbleModeWindow
}

// ExemplarKindForMetric maps a problem's breached metric to the exemplar trace
// that best illustrates it: error_rate → an erroring trace, latency → a slow
// trace, everything else → any representative trace.
func ExemplarKindForMetric(metric string) ExemplarKind {
	m := strings.ToLower(metric)
	switch {
	case strings.Contains(m, "error"):
		return ExemplarError
	case strings.Contains(m, "p99") || strings.Contains(m, "p95") ||
		strings.Contains(m, "latency") || strings.Contains(m, "duration") || strings.Contains(m, "ms"):
		return ExemplarSlow
	default:
		return ExemplarAny
	}
}
