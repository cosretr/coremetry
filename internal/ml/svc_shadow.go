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
// P2; 4 sa açık critical → P1). svc_error_rate'te Threshold en az %0,5
// (svcErrThresholdFloor = etki kuralının %1 mutlak tabanı / 2): "normalde
// hiç yokken ≥ %1" büyük sapmadır (eventRatio'nun taban-0 kuralıyla aynı yön).
//
// TETİK (v0.10.2373, etki olayı programı I3): svc_error_rate olayı artık etki
// kuralıyla (1 dk örnek, 5'te 3, eşik max(%1, taban × 1,5, taban + 1 puan))
// açılır ve TEK BAŞINA Problem açar. Eski kural ≥ 2 × taban istediği için
// olay hep P1 doğardı; yeni kuralda taban × 1,5 – 2 arası olay P2 doğar,
// ≥ 2 × taban (ya da taban < %0,5 iken ≥ %1) yine P1. Öncelik mantığı aynı.
//
// DEDUPE (spec Karar 4): servis başına TEK ML Problem'i. Aynı servisin iki
// svc olayından en erken başlayan sahip, diğeri üye; öncelik en yükseği.
// Aynı servisin grup Problem'i (anomaly-auto:group, canlı ya da "açardı")
// varsa svc kararı ona ÜYE olurdu (JoinsRule dolu) — yine "Problem var"
// sayılır (WouldOpen), sayaç yalnız sahip kararları sayar.
//
// YANIT SÜRESİ (v0.10.2385, etki olayı programı I4): svc_response_time olayı
// aynı kapıdan TEK BAŞINA Problem açar (Value = ateşleyen ölçünün gözleneni,
// Threshold = o ölçünün mevsimsel tabanı, ms). En yavaş %10 kuralı ≥ 2 × taban
// istediği için o ölçüyle açılan olay P1 doğar; medyan kuralında taban × 1,5 –
// 2 arası olay P2, ≥ 2 × taban (taban ≤ 100 ms iken her ateşleme) P1.
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
func isSvcDetector(d string) bool {
	return d == detSvcErrorRate || d == detSvcBreadth || d == detSvcResponseTime || // yanıt süresi: v0.10.2385 (I4)
		d == detEndpointImpact || // v0.10.2474: uç nokta etki olayı servisin TEK Problem'ine üye
		d == detPodOOM // v0.10.2476: OOMKilled pod (iş yükü) da
}

// svcSecondary — SAF (v0.10.2476): servis düzeyi olay varken yalnız üye olan,
// yokken Problem'i tek başına açabilen olaylar (uç nokta, OOMKilled iş yükü).
func svcSecondary(d string) bool { return d == detEndpointImpact || d == detPodOOM }

// svcErrThresholdFloor — hata oranı Problem'inin Threshold tabanı: etki
// kuralının mutlak tabanının yarısı (%1 / 2 = %0,5). Taban bunun altındayken
// kuralın açabildiği en küçük oran (%1) BigBreachRatio'yu (2) karşılar.
var svcErrThresholdFloor = impactRules[impactFailureRate].Floor / 2

// svcValueThreshold — SAF: Problem'in Value / Threshold'u.
func svcValueThreshold(e chstore.MLEvent) (float64, float64) {
	if e.Detector == detPodOOM { // v0.10.2476: öncelik kodu (pod_oom.go oomValueThreshold)
		return oomValueThreshold(e)
	}
	th := e.Baseline
	det := e.Detector
	if det == detEndpointImpact { // v0.10.2474: ölçüsüne göre servis karşılığının kuralı
		det = detSvcResponseTime
		if epMeasureOfEvent(e).key == "" {
			det = detSvcErrorRate
		}
	}
	if det == detSvcErrorRate && th < svcErrThresholdFloor {
		th = svcErrThresholdFloor
	}
	if det == detSvcResponseTime && th < 1 { // ms; taban 0 olamaz ama oran sonsuza gitmesin
		th = 1
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
		for _, e := range svcLeadEvents(evs) { // v0.10.2474: uç nokta olayı servis olayı varken yalnız üye
			v, th := svcValueThreshold(e)
			// v0.10.2476: ölçü + cümle de (OOM önceliği kendi dalında, manşetten)
			p, why := chstore.PreviewProblemPriority(chstore.Problem{RuleID: rule, Severity: "critical", Service: svc, Value: v, Threshold: th,
				Metric: svcMetric(e), Description: svcEventSentence(e), Status: "open", StartedAt: evs[0].StartedAt}, now.UnixNano(), prio)
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

// svcEventSentence — SAF: Problem detayındaki düz cümle (spec Karar 5).
func svcEventSentence(e chstore.MLEvent) string {
	switch e.Detector {
	case detSvcErrorRate:
		// v0.10.2383 — "normalde X, şimdi Y (N kat)": cümlenin tek tanımı
		// chstore (manşet oradan geri okunur); kat OLAYIN tabanından, Problem'in
		// %0,5 tabanlı Threshold'undan değil.
		s := chstore.MLErrRateSentence(e.Service, e.Observed, e.Baseline)
		// v0.10.2380 — istek sayısıyla etki (chstore işaret cümlesi; Problem
		// standardı buradan geri okur). Kanıtta bölüm yoksa cümle eklenmez.
		if rs := chstore.MLRequestSplitOf(e.Evidence); rs != nil {
			s += " " + chstore.RequestSplitSentence(*rs)
		}
		return s
	case detSvcResponseTime:
		// v0.10.2385 (I4) — "yanıt süresi (medyan) normalde X, şimdi Y (N kat)":
		// cümlenin tek tanımı chstore; ölçü ve değerler olayın ateşleyen ölçüsü.
		return chstore.MLResponseTimeSentence(e.Service, rtMeasureOfEvent(e).key, e.Observed, e.Baseline)
	case detEndpointImpact: // v0.10.2474: "checkout · POST /pay: hata oranı normalde %0,2, şimdi %14 (70 kat)."
		return epSentence(e)
	case detPodOOM: // v0.10.2476: "checkout · pod bellek sınırında sonlandırıldı (OOMKilled): 2 pod, son 15 dk'da 3 kez — limit 512 Mi."
		return oomSentence(e)
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
// Yanıt süresi (v0.10.2385): olayın ölçüsü — medyan p50_ms, en yavaş %10
// p90_ms (ikisi de gecikme ailesi / SLOWDOWN).
func svcMetric(e chstore.MLEvent) string {
	switch e.Detector {
	case detSvcBreadth:
		return "p95_ms"
	case detSvcResponseTime:
		return rtMeasureOfEvent(e).agg + "_ms"
	case detPodOOM: // v0.10.2476
		return chstore.MLOOMMetric
	case detEndpointImpact: // v0.10.2474: servis karşılığının ölçüsü (problem standardı ailesi aynı)
		switch epMeasureOfEvent(e).key {
		case chstore.MLRespMedian:
			return rtMeasures[0].agg + "_ms"
		case chstore.MLRespSlow:
			return rtMeasures[1].agg + "_ms"
		}
	}
	return "error_rate"
}

// svcSentenceRank — Problem açıklamasında cümle sırası: hata oranı, yanıt
// süresi, yaygın yavaşlama.
var svcSentenceRank = map[string]int{detSvcErrorRate: 0, detSvcResponseTime: 1, detSvcBreadth: 2, detEndpointImpact: 3, detPodOOM: 4}

// mlProblemRatio — SAF: Value/Threshold (Threshold ≤ 0 → 1; grup satırı 1).
func mlProblemRatio(v, th float64) float64 {
	if th <= 0 {
		return 1
	}
	return v / th
}

// svcLeadEvents — SAF (v0.10.2474): Problem'in değerini, önceliğini ve
// cümlesini veren olaylar. Servis düzeyi olay varsa yalnız onlar (uç nokta
// olayları yalnız üye; davranış v0.10.2473 ile aynı); yoksa uç nokta
// olaylarından oranı (Value / Threshold) en büyük TEK olay — manşet tek uç
// noktayı adlandırır, kalan uç noktalar üye. Sıra korunur.
func svcLeadEvents(evs []chstore.MLEvent) []chstore.MLEvent {
	var svcEvs []chstore.MLEvent
	bestI, best := -1, -1.0
	for i, e := range evs {
		if !svcSecondary(e.Detector) { // v0.10.2476: OOMKilled iş yükü de uç nokta gibi
			svcEvs = append(svcEvs, e)
			continue
		}
		if r := mlProblemRatio(svcValueThreshold(e)); bestI < 0 || r > best || (r == best && e.ID < evs[bestI].ID) {
			bestI, best = i, r
		}
	}
	if len(svcEvs) > 0 || bestI < 0 {
		return svcEvs
	}
	return evs[bestI : bestI+1]
}

// svcProblemInputs — SAF: bu tikin servis düzeyi kararları → servis başına
// canlı girdi (servis sıralı). Yalnız açık (karar verilmiş) olaylar.
// v0.10.2155: eski kural ikizi notu kalktı — aynı serviste eski bir sahip
// açıksa ML kararı ona üye olur (group_live.go), çift satır açılmaz.
func svcProblemInputs(kept []chstore.MLEvent, ds map[string]SvcShadow) []svcProblemIn {
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
				return svcSentenceRank[evs[i].Detector] < svcSentenceRank[evs[j].Detector]
			}
			return evs[i].ID < evs[j].ID
		})
		in := svcProblemIn{Service: svc}
		best := -1.0
		var parts []string
		var lead chstore.MLEvent // Problem'in ölçüsünü veren olay (kaynak işareti)
		// v0.10.2474: değer + cümle yalnız svcLeadEvents'ten (uç nokta olayı
		// servis olayı varken yalnız üye; yoksa tek uç nokta manşette).
		for _, e := range svcLeadEvents(evs) {
			v, th := svcValueThreshold(e)
			if r := mlProblemRatio(v, th); r > best {
				best, in.Value, in.Threshold, in.Metric = r, v, th, svcMetric(e)
				lead = e
			}
			parts = append(parts, svcEventSentence(e))
		}
		for _, e := range evs {
			if in.StartedAt == 0 || e.StartedAt < in.StartedAt {
				in.StartedAt = e.StartedAt
			}
			in.Events = append(in.Events, e.ID)
		}
		in.Desc = withSourceMarker(strings.Join(parts, " "), lead) // v0.10.2383
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
