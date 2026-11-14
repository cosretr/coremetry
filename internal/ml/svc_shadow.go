package ml

// svc_shadow.go — v0.10.2077 (spec docs/plans/spec-ml-service-level.md
// dilim 3, operatör onayı 2026-11-13): servis düzeyi ML olaylarının
// (svc_error_rate, svc_breadth) GÖLGE Problem kapısı. Karar her tik SAF
// işlevden çıkar ve olayın kanıtına `svcShadow` anahtarıyla yazılır.
// v0.10.2130 (spec dilim 4, operatör kararı 2026-11-14 "Yenileri canlıya
// al"; 14 günlük gölge beklenmedi): AYNI karar canlı yolu sürer — ML
// Problem anahtarı (ml.groupProblems, GroupProblemsOn) açıkken kaydedici
// `anomaly-auto:svc:<svc>` Problem'ini açar / tazeler / kapatır
// (group_live.go planMLProblems; grup Problem'iyle ortak servis anahtarı).
// Anahtar kapalıyken yalnız gölge kaydı (eski davranış).
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
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// MLSvcRulePrefix — servis düzeyi Problem ailesinin öneki (tek kaynak
// chstore; evaluator süpürmeleri ve inbox keep listesi oradan okur).
const MLSvcRulePrefix = chstore.MLSvcRulePrefix

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
	// Live / ProblemID — v0.10.2130: canlı kip bu kararla Problem açtı ya da
	// tazeledi (servisin ML Problem'i; grup sahipliğinde grup Problem'i).
	Live      bool   `json:"live,omitempty"`
	ProblemID string `json:"problemId,omitempty"`
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
				d.Reason = fmt.Sprintf("servisin grup Problem'i (%s) var — ona üye, öncelik %s", groupRule[svc], best)
			case i == 0:
				d.Reason = fmt.Sprintf("kapıdan geçti: %s, öncelik %s (%s)", rule, best, bestReason)
			default:
				d.Reason = fmt.Sprintf("aynı servisin svc Problem'ine (%s) üye, öncelik %s", rule, best)
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

// --- v0.10.2130 canlı kip (spec dilim 4) ---------------------------------

// svcProblemRuleName — "ML · " öneki manşeti açıklamadan alır
// (chstore.MLPromotedHeadline): Problem detayının başlığı düz cümle.
const svcProblemRuleName = chstore.MLPromotedRuleNamePrefix + "servis anomalisi"

// svcProblemIn — SAF girdi: servis başına canlı servis düzeyi karar
// (planMLProblems). Value / Threshold en büyük oranlı olaydan (öncelik
// yalnız yükselir), StartedAt en erken olaydan.
type svcProblemIn struct {
	Service          string
	Value, Threshold float64
	Metric           string
	StartedAt        int64
	Desc             string
	Events           []string // kararın olay kimlikleri (Live işareti)
}

// pctTR — SAF: oran → "2,5" (yüzde; işaret çağıranda).
func pctTR(v float64) string { return trDec(v*100, 1) }

// svcEventSentence — SAF: Problem detayındaki düz cümle (spec Karar 5).
func svcEventSentence(e chstore.MLEvent) string {
	switch e.Detector {
	case detSvcErrorRate:
		return fmt.Sprintf("%s servisinde hata oranı %%%s, olağanı %%%s.", e.Service, pctTR(e.Observed), pctTR(e.Baseline))
	case detSvcBreadth:
		_, n := breadthOpsOf(e.Evidence)
		cnt := "birden çok"
		if n > 0 {
			cnt = trInt(float64(n))
		}
		s := fmt.Sprintf("%s servisinde %s operasyon birden yavaşladı", e.Service, cnt)
		if e.Baseline > 0 {
			s += fmt.Sprintf(" (p95 olağanın %s katı)", trDec(e.Observed/e.Baseline, 1))
		}
		return s + "."
	}
	return e.Service + ": servis düzeyi anomali."
}

// svcMetric — SAF: Problem ölçüsü (problem standardı ailesi + kategori).
func svcMetric(det string) string {
	if det == detSvcBreadth {
		return "p95_ms"
	}
	return "error_rate"
}

// mlProblemRatio — SAF: Value/Threshold (Threshold ≤ 0 → 1; grup satırı 1).
func mlProblemRatio(v, th float64) float64 {
	if th <= 0 {
		return 1
	}
	return v / th
}

// legacyTwinNote — SAF: aynı serviste eski kural (svc-slowdown /
// anomaly:<svc>:error_rate) AÇIKSA açıklama eki; yoksa "". Eski dedektörler
// emekli edilene dek (spec Karar 5 madde 5) aynı olay iki Problem
// açabilir — operatör kabul etti; not çift kaydı görünür kılar.
func legacyTwinNote(svc string, open []chstore.Problem) string {
	var rules []string
	for _, p := range open {
		if p.Service != svc || p.Status == "resolved" {
			continue
		}
		if strings.HasPrefix(p.RuleID, "svc-slowdown:") || (strings.HasPrefix(p.RuleID, "anomaly:") && strings.HasSuffix(p.RuleID, ":error_rate")) {
			rules = append(rules, p.RuleID)
		}
	}
	if len(rules) == 0 {
		return ""
	}
	sort.Strings(rules)
	return " Not: aynı servis için eski kural da açık (" + strings.Join(rules, ", ") + ") — aynı olayın çift kaydı olabilir."
}

// svcProblemInputs — SAF: bu tikin servis düzeyi kararları → servis başına
// canlı girdi (servis sıralı). Yalnız açık (karar verilmiş) olaylar;
// note: servis → açıklama eki (eski kural ikizi).
func svcProblemInputs(kept []chstore.MLEvent, ds map[string]SvcShadow, note func(string) string) []svcProblemIn {
	bySvc := map[string][]chstore.MLEvent{}
	for _, e := range kept {
		if d, ok := ds[e.ID]; ok && d.WouldOpen {
			bySvc[e.Service] = append(bySvc[e.Service], e)
		}
	}
	out := make([]svcProblemIn, 0, len(bySvc))
	for svc, evs := range bySvc {
		// cümle sırası sabit: hata oranı önce, sonra yavaşlama
		sort.Slice(evs, func(i, j int) bool {
			if evs[i].Detector != evs[j].Detector {
				return evs[i].Detector == detSvcErrorRate
			}
			return evs[i].ID < evs[j].ID
		})
		in := svcProblemIn{Service: svc}
		best := -1.0
		var parts []string
		for _, e := range evs {
			v, th := svcValueThreshold(e)
			if r := mlProblemRatio(v, th); r > best {
				best, in.Value, in.Threshold, in.Metric = r, v, th, svcMetric(e.Detector)
			}
			if in.StartedAt == 0 || e.StartedAt < in.StartedAt {
				in.StartedAt = e.StartedAt
			}
			parts = append(parts, svcEventSentence(e))
			in.Events = append(in.Events, e.ID)
		}
		desc := strings.Join(parts, " ")
		if note != nil {
			desc += note(svc)
		}
		in.Desc = truncRunes(desc, 600)
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}

// newSvcProblem — SAF: açılacak servis düzeyi Problem satırı (critical;
// öncelik computePriority'den: oran ≥ BigBreachRatio → P1, değilse P2).
func newSvcProblem(in svcProblemIn) chstore.Problem {
	rule := MLSvcRulePrefix + in.Service
	return chstore.Problem{
		ID: rule, RuleID: rule, RuleName: svcProblemRuleName, Severity: "critical", Service: in.Service,
		Metric: in.Metric, Comparator: ">=", Value: in.Value, Threshold: in.Threshold, Status: "open",
		Description: in.Desc, StartedAt: in.StartedAt,
	}
}

// markLiveSvc — SAF: yazımı başarılı Problem'e bağlanan kararlara Live +
// ProblemID işlenir (olay kanıtına damgalanmadan ÖNCE çağrılır).
func markLiveSvc(ds map[string]SvcShadow, live map[string]string, failed map[string]bool) {
	for id, pid := range live {
		if d, ok := ds[id]; ok && !failed[pid] {
			d.Live, d.ProblemID = true, pid
			ds[id] = d
		}
	}
}
