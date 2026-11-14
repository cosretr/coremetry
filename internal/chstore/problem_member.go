package chstore

// problem_member.go — v0.10.2156 (Davis tarzı tek Problem D2, spec
// docs/plans/spec-davis-style-problem-merge.md): ÜYE OLAYI. Açık bir sahibe
// katılan eski dedektör (svc-slowdown, anomaly:<svc>:<metrik>, anomaly-auto
// terfisi) Problem satırı açmaz; üyeliği bir `ml_events` satırıdır
// (detector = svc_slowdown | metric_anomaly | anomaly_promotion,
// stage = live). Şema değişmez: üyelik kanıtta (`problem_id`), ML'in bugünkü
// damgasıyla (groupShadow / svcShadow problemId) AYNI yerde okunur.
//
// Kanıt, dedektörün AÇACAĞI satırı da taşır (`row`): dedektör üyeliğini kendi
// açık satırı gibi görür (histerezis, tazeleme, kapanış kuralları aynen),
// yazımları üye olayına gider (internal/anomaly/problem_member.go).
//
// GÖRÜNÜRLÜK: bu dedektörler Faz 1'de anomali yüzeylerinde (Anomalies
// listesi, birleşik akış, karne, gruplama) GİZLİDİR — ListMLEvents ve
// kardeşleri varsayılan olarak dışlar (MLEventsFilter.Members). Yalnız
// Problem'in olay çizelgesi ve üyelik okumaları görür.

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Üye dedektör adları (ml_events.detector). ML ayarı / karne anahtarı DEĞİL.
const (
	MLDetMemberSvcSlowdown   = "svc_slowdown"
	MLDetMemberMetricAnomaly = "metric_anomaly"
	MLDetMemberPromotion     = "anomaly_promotion"
	// MLStageLive — üye olayının aşaması (kapanınca MLStageClosed).
	MLStageLive = "live"
	// MemberEventWindowSec — üye olayının penceresi. MLEventOpen "son görülme
	// ≤ 4 × pencere" der: dedektör durursa üyelik 20 dk sonra kendiliğinden
	// biter (emniyet kapanışı; olağan kapanış dedektörün kendi kararıdır).
	MemberEventWindowSec = 300
)

// MLMemberDetectors — üye dedektörleri (SQL dışlama listesi; sıra sabit).
var MLMemberDetectors = []string{MLDetMemberSvcSlowdown, MLDetMemberMetricAnomaly, MLDetMemberPromotion}

// IsMLMemberDetector — SAF.
func IsMLMemberDetector(d string) bool {
	for _, m := range MLMemberDetectors {
		if m == d {
			return true
		}
	}
	return false
}

// mlMemberDetectorsSQL — SAF: `'a','b','c'` (sabit liste; kullanıcı girdisi yok).
func mlMemberDetectorsSQL() string {
	return "'" + strings.Join(MLMemberDetectors, "','") + "'"
}

// MemberEvidence — üye olayının kanıtı.
type MemberEvidence struct {
	ProblemID string  `json:"problem_id"` // sahip Problem
	RuleID    string  `json:"rule_id"`    // üyenin kendi kuralı
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	// Row — dedektörün açacağı satır (sanal açık satır; yalnız saklanan alanlar).
	Row *MemberRow `json:"row,omitempty"`
}

// MemberRow — sanal satırın SAKLANAN Problem alanları. Okuma-anı
// zenginleştirmeleri (öncelik, takım, deploy, kök neden …) ve AI özeti
// bilinçli olarak yok: sanal satır bir Problem değildir, dedektörün kendi
// durumudur.
type MemberRow struct {
	ID          string  `json:"id"`
	RuleID      string  `json:"ruleId"`
	RuleName    string  `json:"ruleName"`
	Severity    string  `json:"severity"`
	Service     string  `json:"service"`
	Kind        string  `json:"kind,omitempty"`
	Metric      string  `json:"metric"`
	Value       float64 `json:"value"`
	Threshold   float64 `json:"threshold"`
	Comparator  string  `json:"comparator,omitempty"`
	Pod         string  `json:"pod,omitempty"`
	Description string  `json:"description"`
	StartedAt   int64   `json:"startedAt"`
}

func memberRowOf(p Problem) *MemberRow {
	return &MemberRow{ID: p.ID, RuleID: p.RuleID, RuleName: p.RuleName, Severity: p.Severity, Service: p.Service, Kind: p.Kind, Metric: p.Metric,
		Value: p.Value, Threshold: p.Threshold, Comparator: p.Comparator, Pod: p.Pod, Description: p.Description, StartedAt: p.StartedAt}
}

// Problem — sanal AÇIK satır (dedektörün gördüğü hal).
func (r MemberRow) Problem() Problem {
	return Problem{ID: r.ID, RuleID: r.RuleID, RuleName: r.RuleName, Severity: r.Severity, Service: r.Service, Kind: r.Kind, Metric: r.Metric,
		Value: r.Value, Threshold: r.Threshold, Comparator: r.Comparator, Pod: r.Pod, Description: r.Description, StartedAt: r.StartedAt, Status: "open"}
}

// MemberDetectorForRule — SAF: eski ailenin üye dedektörü ("" = üye olayı
// yazılmaz: ML ailesinin üyeliği kendi olaylarıdır; ayrı aileler katılmaz).
func MemberDetectorForRule(ruleID string) string {
	switch {
	case ProblemMergeClass(ruleID) == MergeSeparate, IsMLLiveProblem(ruleID):
		return ""
	case strings.HasPrefix(ruleID, RuleSvcSlowdownPrefix):
		return MLDetMemberSvcSlowdown
	case strings.HasPrefix(ruleID, PromotedAnomalyRulePrefix):
		return MLDetMemberPromotion
	}
	return MLDetMemberMetricAnomaly
}

// MemberEventID — SAF: (dedektör, servis, kural, sahip bölümü) başına TEK
// satır. Deterministik: aynı üyelik her tikte aynı kimliğe yazar, yeniden
// katılma (okuma gecikmesi / lider değişimi) ikinci satır üretmez; sahibin
// yeni bölümü (aynı kimlikle yeniden açılan svc-slowdown) ayrı satırdır.
func MemberEventID(detector, ruleID, service string, owner Problem) string {
	return MLEpisodeID(MLEventID(detector, service, ruleID+"|"+owner.ID), time.Unix(0, owner.StartedAt))
}

// NewMemberEvent — SAF: üyenin ml_events satırı. row = dedektörün açacağı /
// tazeleyeceği satır; startNs = üyeliğin başlangıcı (taşınır), nowNs = son
// görülme. closed → stage=closed (üye ayrıldı).
func NewMemberEvent(detector string, row, owner Problem, startNs, nowNs int64, closed bool) MLEvent {
	ev, _ := json.Marshal(MemberEvidence{ProblemID: owner.ID, RuleID: row.RuleID, Value: row.Value, Threshold: row.Threshold, Row: memberRowOf(row)})
	dir := "up"
	if isBelowRule(row.Comparator) {
		dir = "down"
	}
	stage := MLStageLive
	if closed {
		stage = MLStageClosed
	}
	score := 0.0
	if row.Threshold > 0 {
		score = row.Value / row.Threshold
	}
	if startNs <= 0 || startNs > nowNs {
		startNs = nowNs
	}
	return MLEvent{
		ID: MemberEventID(detector, row.RuleID, row.Service, owner), Detector: detector, Signal: MLDetectorSignal(detector),
		Stage: stage, Service: row.Service, StartedAt: startNs, LastSeen: nowNs, WindowSec: MemberEventWindowSec,
		Baseline: row.Threshold, Observed: row.Value, Score: score, Direction: dir, Evidence: string(ev),
	}
}

// MemberEvidenceOf — SAF: üye olayının kanıtı (üye dedektörü değilse ok=false).
func MemberEvidenceOf(e MLEvent) (MemberEvidence, bool) {
	if !IsMLMemberDetector(e.Detector) || e.Evidence == "" {
		return MemberEvidence{}, false
	}
	var m MemberEvidence
	if json.Unmarshal([]byte(e.Evidence), &m) != nil || m.ProblemID == "" || m.RuleID == "" {
		return MemberEvidence{}, false
	}
	return m, true
}

// mlProblemStamp — olay kanıtındaki Problem bağı: üye olayı (problem_id) ya
// da ML'in canlı damgası (groupShadow / svcShadow, live + problemId).
type mlProblemStamp struct {
	ProblemID string `json:"problem_id"`
	RuleID    string `json:"rule_id"`
	Group     *struct {
		Live      bool   `json:"live"`
		ProblemID string `json:"problemId"`
	} `json:"groupShadow"`
	Svc *struct {
		Live      bool   `json:"live"`
		ProblemID string `json:"problemId"`
	} `json:"svcShadow"`
}

// MLEventProblemID — SAF: olayın bağlı olduğu Problem ("" = bağlı değil).
// Üye olayında kanıtın problem_id'si; ML olayında canlı kararın damgası
// (servis düzeyi karar önce — aynı olay iki damga taşıyorsa ikisi de aynı
// Problem'i gösterir, group_live.go).
func MLEventProblemID(e MLEvent) string {
	if e.Evidence == "" {
		return ""
	}
	var s mlProblemStamp
	if json.Unmarshal([]byte(e.Evidence), &s) != nil {
		return ""
	}
	switch {
	case s.ProblemID != "":
		return s.ProblemID
	case s.Svc != nil && s.Svc.Live && s.Svc.ProblemID != "":
		return s.Svc.ProblemID
	case s.Group != nil && s.Group.Live && s.Group.ProblemID != "":
		return s.Group.ProblemID
	}
	return ""
}

// ProblemEventsOf — SAF: bir Problem'e bağlı olaylar (zaman sıralı: başlangıç,
// eşitse kimlik). Problem'in bölümüne ait olmayan eski olaylar (aynı kimlikle
// yeniden açılan satır) başlangıç penceresiyle elenir: olay, Problem
// başlamadan en çok `slack` önce başlamış ve — Problem kapandıysa — kapanıştan
// önce başlamış olmalı.
func ProblemEventsOf(evs []MLEvent, p Problem, slack time.Duration) []MLEvent {
	var out []MLEvent
	for _, e := range evs {
		if e.Service != p.Service || MLEventProblemID(e) != p.ID {
			continue
		}
		if e.LastSeen < p.StartedAt-int64(slack) {
			continue
		}
		if p.ResolvedAt != nil && e.StartedAt > *p.ResolvedAt {
			continue
		}
		out = append(out, e)
	}
	sortMLEventsByStart(out)
	return out
}

func sortMLEventsByStart(evs []MLEvent) {
	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].StartedAt != evs[j].StartedAt {
			return evs[i].StartedAt < evs[j].StartedAt
		}
		return evs[i].ID < evs[j].ID
	})
}

// LiveMemberEvents — SAF: Problem'in CANLI üyeleri (MLEventOpen kuralı:
// kapanmamış ve son görülmesi taze). Sahip ML ailesindense yalnız üye
// dedektörleri sayılır (ML'in kendi olayları sahibin KENDİ sinyalidir, onun
// kapanış kuralını ML kaydedicisi yürütür).
func LiveMemberEvents(evs []MLEvent, owner Problem, nowNs int64) []MLEvent {
	mlOwner := IsMLLiveProblem(owner.RuleID)
	var out []MLEvent
	for _, e := range evs {
		if e.Service != owner.Service || !MLEventOpen(e, nowNs) || MLEventProblemID(e) != owner.ID {
			continue
		}
		if mlOwner && !IsMLMemberDetector(e.Detector) {
			continue
		}
		out = append(out, e)
	}
	return out
}
