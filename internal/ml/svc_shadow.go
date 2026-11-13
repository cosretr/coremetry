package ml

// svc_shadow.go — v0.10.2077 (spec docs/plans/spec-ml-service-level.md
// dilim 3, operatör onayı 2026-11-13): servis düzeyi ML olaylarının
// (svc_error_rate, svc_breadth) GÖLGE Problem kapısı. Karar her tik SAF
// işlevden çıkar ve olayın kanıtına `svcShadow` anahtarıyla yazılır.
// Problem AÇILMAZ, bildirim GİTMEZ — canlı kip spec dilim 4, 14 günlük
// gölgeden sonra ayrı operatör kararı.
//
// KAPI (spec seçenek a): açık, servis düzeyi olay tek başına Problem açardı;
// yeni aile `anomaly-auto:svc:<svc>`, şiddet critical, Value = gözlenen,
// Threshold = mevsimsel taban → öncelik MEVCUT computePriority'den
// (chstore.PreviewProblemPriority; oran ≥ BigBreachRatio (2) → P1, değilse
// P2; 4 sa açık critical → P1). svc_error_rate'te taban 0 ise Threshold =
// %0,5 (= %1 tabanı / 2 kat): "normalde hiç yokken ≥ %1" büyük sapmadır
// (eventRatio'nun taban-0 kuralıyla aynı yön).
//
// DEDUPE (spec Karar 4): servis başına TEK ML Problem'i. Aynı servisin iki
// svc olayından en erken başlayan sahip, diğeri üye; öncelik en yükseği.
// Aynı servisin grup Problem'i (anomaly-auto:group, canlı ya da "açardı")
// varsa svc kararı ona ÜYE olurdu (JoinsRule dolu) — yine "Problem var"
// sayılır (WouldOpen), sayaç yalnız sahip kararları sayar.
//
// GRUP KURALINA ETKİSİ: svc olayları grupta trace sinyali üyesidir (başka
// değişiklik yok); tek-sinyal gölge kolu (v0.10.2058) svc olaylarını SAYMAZ
// (singleWorsening) — canlı karşılıkları bu kapı olduğu için çift sayım olmaz.

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// MLSvcRulePrefix — servis düzeyi Problem ailesinin öneki (yalnız gölge).
const MLSvcRulePrefix = "anomaly-auto:svc:"

const svcShadowKey = "svcShadow"

// SvcShadow — servis düzeyi gölge kararı (olay kanıtında).
type SvcShadow struct {
	WouldOpen      bool    `json:"wouldOpen"`
	Owner          bool    `json:"owner,omitempty"` // servisin svc kararlarının sahibi (sayaç bunu sayar)
	RuleID         string  `json:"rule"`
	JoinsRule      string  `json:"joinsRule,omitempty"` // grup Problem'ine üye olurdu
	Priority       string  `json:"priority,omitempty"`
	PriorityReason string  `json:"priorityReason,omitempty"`
	Value          float64 `json:"value"`
	Threshold      float64 `json:"threshold"`
	Reason         string  `json:"reason"`
	At             int64   `json:"at"`
}

// isSvcDetector — SAF.
func isSvcDetector(d string) bool { return d == detSvcErrorRate || d == detSvcBreadth }

// svcValueThreshold — SAF: Problem'in Value / Threshold'u.
func svcValueThreshold(e chstore.MLEvent) (float64, float64) {
	th := e.Baseline
	if e.Detector == detSvcErrorRate && th < svcErrFloor/svcErrMinRatio {
		th = svcErrFloor / svcErrMinRatio
	}
	return e.Observed, th
}

var prioRank = map[string]int{"P1": 3, "P2": 2, "P3": 1}

// decideSvcShadow — SAF: bu tikin olayları (+ açık koşu kimlikleri) + aynı
// tikin grup kararları → servis düzeyi olay kimliği → karar. open nil =
// stage'e bak (replay).
func decideSvcShadow(kept []chstore.MLEvent, open map[string]bool, groups []decidedGroup, prio chstore.ProblemPriorityConfig, now time.Time) map[string]SvcShadow {
	groupRule := map[string]string{} // servis → canlı / açardı grup kuralı
	for _, g := range groups {
		if (g.D.WouldOpen || g.D.Live) && !g.D.Finished && len(g.Members) > 0 {
			if _, ok := groupRule[g.Members[0].Service]; !ok {
				groupRule[g.Members[0].Service] = g.D.RuleID
			}
		}
	}
	bySvc := map[string][]chstore.MLEvent{}
	for _, e := range kept {
		if !isSvcDetector(e.Detector) || e.Stage == chstore.MLStageChronic || !groupMemberOpen(e, open) {
			continue
		}
		bySvc[e.Service] = append(bySvc[e.Service], e)
	}
	out := map[string]SvcShadow{}
	for svc, evs := range bySvc {
		sort.Slice(evs, func(i, j int) bool {
			if evs[i].StartedAt != evs[j].StartedAt {
				return evs[i].StartedAt < evs[j].StartedAt
			}
			return evs[i].ID < evs[j].ID
		})
		rule := MLSvcRulePrefix + svc
		// servisin önceliği = üyelerin en yükseği (yalnız yükselir)
		best, bestReason, bestV, bestT := "", "", 0.0, 0.0
		for _, e := range evs {
			v, th := svcValueThreshold(e)
			p, why := chstore.PreviewProblemPriority(chstore.Problem{RuleID: rule, Severity: "critical", Service: svc, Value: v, Threshold: th,
				Status: "open", StartedAt: evs[0].StartedAt}, now.UnixNano(), prio)
			if prioRank[p] > prioRank[best] {
				best, bestReason, bestV, bestT = p, why, v, th
			}
		}
		for i, e := range evs {
			d := SvcShadow{WouldOpen: true, Owner: i == 0, RuleID: rule, Priority: best, PriorityReason: bestReason, Value: bestV, Threshold: bestT, At: now.Unix()}
			switch {
			case groupRule[svc] != "":
				d.Owner = false
				d.JoinsRule = groupRule[svc]
				d.Reason = fmt.Sprintf("servisin grup Problem'i (%s) zaten var — ona üye olurdu (gölge), öncelik %s", groupRule[svc], best)
			case i == 0:
				d.Reason = fmt.Sprintf("açardı (gölge): %s, öncelik %s (%s)", rule, best, bestReason)
			default:
				d.Reason = fmt.Sprintf("aynı servisin svc Problem'ine (%s) üye olurdu, öncelik %s", rule, best)
			}
			out[e.ID] = d
		}
	}
	return out
}

// svcShadowCounts — SAF: sahip karar sayısı + P1 olanlar (tik sayacı).
func svcShadowCounts(ds map[string]SvcShadow) (wouldOpen, p1 int64) {
	for _, d := range ds {
		if d.WouldOpen && d.Owner {
			wouldOpen++
			if d.Priority == "P1" {
				p1++
			}
		}
	}
	return
}

// stampSvcShadow — SAF: kararı yazılacak olayların kanıtına damgalar.
func stampSvcShadow(kept []chstore.MLEvent, ds map[string]SvcShadow) {
	for i := range kept {
		if d, ok := ds[kept[i].ID]; ok {
			kept[i].Evidence = mergeEvidence(kept[i].Evidence, map[string]any{svcShadowKey: d})
		}
	}
}

// SvcShadowOf — SAF: kanıttan servis düzeyi gölge kararı.
func SvcShadowOf(evidence string) (SvcShadow, bool) {
	if evidence == "" {
		return SvcShadow{}, false
	}
	var m struct {
		S *SvcShadow `json:"svcShadow"`
	}
	if json.Unmarshal([]byte(evidence), &m) != nil || m.S == nil || m.S.RuleID == "" {
		return SvcShadow{}, false
	}
	return *m.S, true
}
