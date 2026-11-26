package chstore

// subject_group_rule.go — v0.10.2482: özne grubu terfisinin (evaluator
// anomaly_subject_group.go) anahtar → kural kimliği eşlemesi TEK kaynak.
// Grup Problem'i `anomaly-auto:subject:<servis>:<metrik>:<yön>` olay kimliği
// taşımaz; üyeleri aynı servisin, aynı metrikte aynı yönde kötüleşen endpoint
// özne olaylarıdır (Problem başlığı / açıklaması rotaları bu kümeden yazar).
// Sinyaller'in bağ süzgeci (api/anomalies_unified_attached.go) olayın grup
// kuralını buradan kurar; evaluator aynı işlevleri kullanır.

import "strings"

// PromotedSubjectRulePrefix — özne grubu Problem'inin kural öneki.
const PromotedSubjectRulePrefix = PromotedAnomalyRulePrefix + "subject:"

// SubjectEventMetric — SAF: özne olayının metriği. Endpoint deseni
// "metrik @ rota", db/queue deseni yalnız metrik.
func SubjectEventMetric(ev AnomalyEvent) string {
	if i := strings.Index(ev.Pattern, " @ "); i >= 0 {
		return ev.Pattern[:i]
	}
	return ev.Pattern
}

// SubjectGroupRuleID — SAF: (servis, metrik, yön) anahtarının kural kimliği.
func SubjectGroupRuleID(service, metric, direction string) string {
	return PromotedSubjectRulePrefix + service + ":" + metric + ":" + direction
}

// SubjectGroupRuleOf — SAF: endpoint özne olayının üyesi olabileceği grup
// Problem'inin kural kimliği (diğer olaylar ok=false).
func SubjectGroupRuleOf(ev AnomalyEvent) (string, bool) {
	if !IsSubjectAnomaly(ev) || ev.SubjectKind != AnomalySubjectEndpoint {
		return "", false
	}
	return SubjectGroupRuleID(ev.Service, SubjectEventMetric(ev), ev.Direction), true
}
