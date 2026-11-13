package ml

// group_gate.go — v0.10.2015 (anomali tekilleştirme D3,
// docs/anomaly/anomaly-reassessment-audit.md §6 B, §9): grup terfi kararı.
// v0.10.2015'te yalnız GÖLGE ("açardı" kaydı); v0.10.2052 (D4, operatör
// onayı 2026-11-12) aynı karar canlı yolu (group_live.go) da sürer.
//
// KAPI (operatör kuralı, tek cümle): aynı servisin grubunda en az 2 FARKLI
// sinyalden (trace / log / metric) anomali varsa grup Problem açar; tek
// sinyalli grup yalnız anomali kalır.
//   - batch kuralı (batch.go batchKeep) üyelere yeniden uygulanır: `-batch`
//     serviste yük sinyali sayılmaz, gerçek hata oranı sinyali sayılır;
//   - aynı sinyalin iki olayı (iki trace dedektörü dahil) teyit değildir;
//   - bitmiş grup (koşusu açık üyesi kalmamış) yeni Problem açmaz.
//   v0.10.2015 gölgesinden farklar: "≥ 2 farklı DEDEKTÖR" → "≥ 2 farklı
//   SİNYAL"; takvim dilimi ön koşulu kalktı (varsayılan dilim boş = UTC
//   olduğundan kural hiçbir grubu açmazdı). Karne kapı DEĞİL (N2).
//
// ÖNCELİK: şiddet critical; canlı Problem'de Value = Threshold = sinyal
// sayısı (oran 1, büyük sapma yok) → computePriority P2. Audit D4 öncelik
// söylemiyor; brif varsayılanı P2. 4 sa açık kalan critical'ı mevcut
// staleCrit kuralı P1 yapar (merdiven aynen).
//
// GÖLGE = CANLI: karar her tik bu işlevden çıkar, üyenin kanıtına
// (groupShadow) damgalanır; canlı yol Problem'i YALNIZ bu kararın
// WouldOpen'ına göre açar — "açardı" ile "açtı" ayrışamaz.
//
// KAYIT YERİ: şemasız — kararı üyenin ml_events.evidence'ına `groupShadow`
// anahtarıyla yazar (tek yazıcı ML lideri; satır zaten her tik tam
// yazılıyor). Liste ucu kararı satır kanıtından okur (ek sorgu yok).

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// MLGroupRulePrefix — grup Problem'inin kural kimliği öneki (tek kaynak
// chstore; evaluator süpürmeleri de oradan okur).
const MLGroupRulePrefix = chstore.MLGroupRulePrefix

const (
	groupMinSignals = 2
	bigBreachRatio  = 2.0
	groupShadowKey  = "groupShadow"
)

// GroupGateInput — kapının dış girdileri (tik başına bir kez).
type GroupGateInput struct {
	IsBatch   func(string) bool // nil = batch kuralı kapalı
	LoadParts map[string]bool   // Load işaretli metrik serileri (metricLoadParts)
	// Open — koşusu açık olay kimlikleri (tracker + bu tikin olayları).
	// nil = bilinmiyor (replay): stage=closed olmayan üye açık sayılır.
	Open map[string]bool
}

// GroupDecision — grup terfi kararı (gölge kaydı; canlıda Live/ProblemID dolu).
type GroupDecision struct {
	Group     string   `json:"group"`
	WouldOpen bool     `json:"wouldOpen"`
	RuleID    string   `json:"rule"`
	Severity  string   `json:"severity,omitempty"`
	Priority  string   `json:"priority,omitempty"`
	Reason    string   `json:"reason"`
	Detectors []string `json:"detectors"`
	Signals   []string `json:"signals,omitempty"` // v0.10.2052: batch sonrası farklı sinyaller
	At        int64    `json:"at"`                // unix sn
	// Finished — koşusu açık üyesi kalmadı (canlı Problem bu grupla kapanır).
	Finished bool `json:"finished,omitempty"`
	// Live — v0.10.2052 (D4): canlı kip bu grubun Problem'ini açtı ya da
	// tazeledi; ProblemID o Problem (detay bağlantısı). Gölgede boş.
	Live      bool   `json:"live,omitempty"`
	ProblemID string `json:"problemId,omitempty"`
	// SingleWouldOpen — v0.10.2058 (Davis tek-olay kolu, YALNIZ GÖLGE): tek
	// sinyalli grubun güçlü + uzun süren kötüleşmesi "tek sinyal: Problem
	// açardı". WouldOpen false kalır → canlı yol (group_live.go) bu kararla
	// Problem AÇMAZ, bildirim gitmez. Canlıya alma ayrı operatör kararı.
	SingleWouldOpen bool `json:"singleWouldOpen,omitempty"`
}

// eventRatio — SAF: sapma oranı (≥ 1). Yukarı: gözlenen/taban; aşağı:
// taban/gözlenen. Taban ya da gözlenen 0 → büyük sapma.
func eventRatio(e chstore.MLEvent) float64 {
	num, den := e.Observed, e.Baseline
	if e.Direction == "down" {
		num, den = e.Baseline, e.Observed
	}
	if den <= 0 {
		return bigBreachRatio
	}
	return math.Max(num/den, 1)
}

// groupMemberOpen — SAF: üyenin koşusu açık mı (Open nil → stage'e bak).
func groupMemberOpen(e chstore.MLEvent, open map[string]bool) bool {
	if open == nil {
		return e.Stage != chstore.MLStageClosed
	}
	return open[e.ID]
}

// GroupRuleID — SAF: grup Problem'inin kural kimliği = önek + grubun ilk
// üyesinin olay kimliği (grup kimliği de ondan türer; detay bağlantısı
// /anomalies/ml/<olay> bu kimlikten kurulur).
func GroupRuleID(g MLGroup) string {
	if len(g.Members) == 0 {
		return MLGroupRulePrefix + g.ID
	}
	return MLGroupRulePrefix + g.Members[0]
}

// GroupEventOfRule — SAF: kural kimliğinden detay olayının kimliği.
func GroupEventOfRule(ruleID string) (string, bool) {
	id, ok := strings.CutPrefix(ruleID, MLGroupRulePrefix)
	return id, ok && id != ""
}

// DecideGroupPromotion — SAF: grubun terfi kararı (gölge ve canlı AYNI
// işlev). members: grubun üye satırları (fazlası zararsız; kimliğe göre süzülür).
func DecideGroupPromotion(g MLGroup, members []chstore.MLEvent, in GroupGateInput, now time.Time) GroupDecision {
	d := GroupDecision{Group: g.ID, RuleID: GroupRuleID(g), At: now.Unix()}
	inGroup := make(map[string]bool, len(g.Members))
	for _, id := range g.Members {
		inGroup[id] = true
	}
	dets, sigs := map[string]bool{}, map[string]bool{}
	peak, kept, cut, open := 1.0, 0, 0, 0
	for _, e := range members {
		if !inGroup[e.ID] {
			continue
		}
		if groupMemberOpen(e, in.Open) {
			open++
		}
		if !batchKeep(e, in.IsBatch, in.LoadParts) {
			cut++
			continue
		}
		kept++
		dets[e.Detector] = true
		sigs[memberSignal(e)] = true
		peak = math.Max(peak, eventRatio(e))
	}
	d.Detectors, d.Signals = sortedKeys(dets), sortedKeys(sigs)
	switch {
	case open == 0:
		d.Finished = true
		d.Reason = "açmazdı: grup bitti (açık olay kalmadı)"
	case kept == 0 && cut > 0:
		d.Reason = "açmazdı: batch servisi, yalnız yük sinyali (batch kuralı)"
	case len(sigs) < groupMinSignals:
		d.Reason = fmt.Sprintf("açmazdı: tek sinyal (%s), başka sinyal teyit etmedi", strings.Join(d.Signals, ", "))
		if why, ok := singleSignalShadow(members, inGroup, in, now); ok {
			d.SingleWouldOpen = true
			d.Reason += "; tek sinyal: Problem açardı (gölge) — " + why
		}
	default:
		d.WouldOpen = true
		d.Severity = "critical"
		d.Priority = "P2"
		d.Reason = fmt.Sprintf("açardı: %d farklı sinyal teyit etti (%s), en büyük sapma %.1f kat", len(sigs), strings.Join(d.Signals, ", "), peak)
	}
	return d
}

// Tek sinyal kolu eşikleri — Davis özne kapısının (evaluator
// subjectPromotionGate) VARSAYILAN değerleri; yeni ayar yok: 600 sn
// sürdürme, tabanın 2 katı, taban 0 iken sayım ≥ 20 (countOnlyMinCount).
var (
	singleMinSustained = time.Duration(chstore.DefaultAnomalySubjects().PromotionMinSustainedSec) * time.Second
	singleMinRatio     = chstore.DefaultAnomalySubjects().PromotionMinRatio
	singleCountOnlyMin = float64(chstore.DefaultAnomalyPromotion().CountOnlyMinCount)
)

// singleWorsening — SAF: olay kötüleşme yönünde mi; count = değeri bir SAYI
// mı (taban 0 iken sayım kolu yalnız sayılarda anlamlı). Davis emsali:
// yalnız hata / gecikme / consumer lag ARTIŞI; yük (hacim, trafik düşüşü,
// sessizlik), yapı (pod aykırısı, nadir şablon) ve diğer metrik serileri
// kötüleşme sayılmaz.
func singleWorsening(e chstore.MLEvent) (worse, count bool) {
	if e.Direction != "up" {
		return false, false
	}
	switch e.Detector {
	case detErrorRate, detTraceErrorCount:
		return true, true
	case detChangePoint:
		return e.Level == "error", true
	case detOpErrorRate:
		return true, false
	case detOpLatency, detDependencyLatency, detTraceChangePoint:
		return true, false
	case "metric_kafka":
		_, id, _ := splitThanosPart(e.Code)
		return id == "consumer_lag", true
	case detSvcErrorRate, detSvcBreadth:
		// v0.10.2089: servis düzeyi olaylar kendi gölge kapısını izler
		// (svc_shadow.go); tek sinyal kolu onları saymaz (çift sayım olmaz).
		return false, false
	}
	return false, false
}

// singleSignalShadow — SAF: tek sinyalli grubun Davis tek-olay kolu. Açık,
// batch kuralından geçen bir üye ≥ 600 sn sürmüş, kötüleşme yönünde ve
// tabanın ≥ 2 katıysa (taban 0: sayım ≥ 20) gölgede "açardı". En güçlü
// üyenin cümlesi döner.
func singleSignalShadow(members []chstore.MLEvent, inGroup map[string]bool, in GroupGateInput, now time.Time) (string, bool) {
	best, bestR := "", 0.0
	for _, e := range members {
		if !inGroup[e.ID] || !groupMemberOpen(e, in.Open) || !batchKeep(e, in.IsBatch, in.LoadParts) {
			continue
		}
		worse, count := singleWorsening(e)
		if !worse {
			continue
		}
		dur := now.Sub(time.Unix(0, e.StartedAt))
		if dur < singleMinSustained {
			continue
		}
		var why string
		var r float64
		if e.Baseline <= 0 {
			if !count || e.Observed < singleCountOnlyMin {
				continue
			}
			r = math.Inf(1)
			why = fmt.Sprintf("%s %d dk sürdü, normalde hiç yokken %.0f", e.Detector, int(dur.Minutes()), e.Observed)
		} else {
			r = e.Observed / e.Baseline
			if r < singleMinRatio {
				continue
			}
			why = fmt.Sprintf("%s %d dk sürdü, normalin %.1f katı (kötüleşme)", e.Detector, int(dur.Minutes()), r)
		}
		if best == "" || r > bestR {
			best, bestR = why, r
		}
	}
	return best, best != ""
}

// memberSignal — SAF: olayın sinyali (eski satırda dedektörden).
func memberSignal(e chstore.MLEvent) string {
	if e.Signal != "" {
		return e.Signal
	}
	return chstore.MLDetectorSignal(e.Detector)
}

// stampGroupShadow — SAF: kararı kanıt JSON'una yazar (mevcut alanlar korunur).
func stampGroupShadow(evidence string, d GroupDecision) string {
	return mergeEvidence(evidence, map[string]any{groupShadowKey: d})
}

// GroupShadowOf — SAF: kanıttan gölge kararı (yoksa ok=false).
func GroupShadowOf(evidence string) (GroupDecision, bool) {
	if evidence == "" {
		return GroupDecision{}, false
	}
	var m struct {
		G *GroupDecision `json:"groupShadow"`
	}
	if json.Unmarshal([]byte(evidence), &m) != nil || m.G == nil || m.G.Group == "" {
		return GroupDecision{}, false
	}
	return *m.G, true
}

// ReplayGroupDecisions — SAF replay kancası: geçmiş ml_events satırları
// üzerinde gruplama + kapı. S1 örnekleri gelince backtest/replay
// karşılaştırması bunu çağırır (grup sırası GroupMLEvents'inki).
func ReplayGroupDecisions(evs []chstore.MLEvent, slack time.Duration, in GroupGateInput, now time.Time) []GroupDecision {
	gs := GroupMLEvents(evs, slack)
	out := make([]GroupDecision, 0, len(gs))
	for _, g := range gs {
		out = append(out, DecideGroupPromotion(g, evs, in, now))
	}
	return out
}

// shadowCounts — tik sayaçları.
type shadowCounts struct{ Groups, WouldOpen, SingleWouldOpen int64 }

// decidedGroup — bu tikte karar verilen grup + üye satırları.
type decidedGroup struct {
	G       MLGroup
	D       GroupDecision
	Members []chstore.MLEvent
}

// decideGroups — recorder tik adımı (SAF çekirdek): bu tikte yazılacak
// olaylar + süreç-içi koşu durumu (tracker; son 24 sa koşuları, kapanmışlar
// dahil) ile grupları kurar; bu tikin bir olayını ya da koşusu açık bir
// üyeyi taşıyan her grup için karar verir (servis, başlangıç sırası).
// CH/ES/VM okuması YOK. Lider değişince koşu durumu sıfırlanır (kabul
// edilen sapma, audit N7): yeni liderin ilk tiklerinde grup yalnız o anda
// açık olaylardan kurulur.
func decideGroups(kept, running []chstore.MLEvent, slack time.Duration, in GroupGateInput, now time.Time) ([]decidedGroup, shadowCounts) {
	byID := make(map[string]chstore.MLEvent, len(running)+len(kept))
	for _, e := range running {
		byID[e.ID] = e
	}
	keptIDs := make(map[string]bool, len(kept))
	for _, e := range kept {
		byID[e.ID] = e
		keptIDs[e.ID] = true
	}
	all := make([]chstore.MLEvent, 0, len(byID))
	for _, e := range byID {
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	var out []decidedGroup
	var c shadowCounts
	for _, g := range GroupMLEvents(all, slack) {
		touch := false
		var mem []chstore.MLEvent
		for _, id := range g.Members {
			e := byID[id]
			mem = append(mem, e)
			if keptIDs[id] || groupMemberOpen(e, in.Open) {
				touch = true
			}
		}
		if !touch {
			continue
		}
		d := DecideGroupPromotion(g, mem, in, now)
		out = append(out, decidedGroup{G: g, D: d, Members: mem})
		c.Groups++
		if d.WouldOpen {
			c.WouldOpen++
		}
		if d.SingleWouldOpen {
			c.SingleWouldOpen++
		}
	}
	return out, c
}

// stampGroupDecisions — SAF: kararı yazılacak üyelerin kanıtına damgalar
// (kronik kayıt gruba girmez, damgalanmaz).
func stampGroupDecisions(kept []chstore.MLEvent, groups []decidedGroup) {
	byMember := map[string]GroupDecision{}
	for _, g := range groups {
		for _, id := range g.G.Members {
			byMember[id] = g.D
		}
	}
	for i := range kept {
		if d, ok := byMember[kept[i].ID]; ok {
			kept[i].Evidence = stampGroupShadow(kept[i].Evidence, d)
		}
	}
}

// shadowPromoteGroups — karar + damga (canlı kip kapalıyken recorder'ın
// tek adımı; testler eski adıyla çağırır).
func shadowPromoteGroups(kept, running []chstore.MLEvent, slack time.Duration, in GroupGateInput, now time.Time) shadowCounts {
	gs, c := decideGroups(kept, running, slack, in, now)
	stampGroupDecisions(kept, gs)
	return c
}
