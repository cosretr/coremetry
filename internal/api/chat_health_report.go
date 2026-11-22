package api

// chat_health_report.go — v0.10.2454: guided servis sağlığı anlatımı, kanıtta
// get_service_health raporu (mcptools.HealthReportMarker) varken bölüm sırası
// ekini taşır (copilot.ServiceHealthAnswerOrder). SAF; model dalı yok. Rapor
// yoksa (operasyon kapsamı, okuma hatası) ek "" → istem bayt bayt eski.

import (
	"strings"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func healthReportPromptFor(intent guidedIntent, evidence string) string {
	if intent != guidedServiceHealth || !strings.Contains(evidence, mcptools.HealthReportMarker) {
		return ""
	}
	return copilot.ServiceHealthAnswerOrder()
}
