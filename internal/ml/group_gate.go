package ml

// group_gate.go — v0.10.2015 (anomali tekilleştirme D3,
// docs/anomaly/anomaly-reassessment-audit.md §6 B, §9): promoteMLGroups
// GÖLGE kipi. Grup (group.go) Problem'e terfi kapısından geçseydi "açardı"
// kaydı tutulur: hangi grup, hangi kural kimliği, hangi öncelik, neden ya
// da neden değil. CANLI Problem, bildirim ve anomaly_events kopyası YOK
// (canlı yol D4, ayrı operatör onayı).
//
// KAPI (audit S2 önerilen varsayılanı; S1 örnekleri gelince ayarlanır):
//   - batch kuralı (batch.go batchKeep) üyelere yeniden uygulanır: kalıp
//     sonradan eklenmiş olsa da `-batch` serviste yük sinyali sayılmaz;
//   - kalan üyelerde ≥ 2 FARKLI dedektör (aynı dedektörün iki olayı teyit
//     değildir);
//   - takvim dilimi tanımlı (PromotionGate ile aynı ön koşul).
//   Karne (etiket isabeti) kapı DEĞİL — etiket yokken terfi hiç olmuyordu
//   (N2); karne susturma için kalır.
//
// ÖNCELİK (gölge tahmini, computePriority'nin şiddet/sapma merdiveniyle
// aynı biçim): şiddet critical = ≥ 2 sinyal ya da ≥ 3 dedektör, değilse
// warning; büyük sapma = üyelerin en büyük gözlenen/taban oranı ≥ 2.
// critical+büyük → P1, critical → P2, warning+büyük → P2, warning → P3.
// Canlı yolda öncelik yine okuma anında computePriority'den gelir.
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

// MLGroupRulePrefix — canlı yolda (D4) açılacak Problem'in kural kimliği
// öneki; `anomaly-auto:` ailesi mevcut bildirim türünü ve incident yolunu
// değiştirmez (audit §6 B). Gölgede yalnız kayda yazılır.
const MLGroupRulePrefix = "anomaly-auto:group:"

const (
	groupMinDetectors = 2
	bigBreachRatio    = 2.0
	groupShadowKey    = "groupShadow"
)

// GroupGateInput — kapının dış girdileri (tik başına bir kez).
type GroupGateInput struct {
	IsBatch    func(string) bool // nil = batch kuralı kapalı
	LoadParts  map[string]bool   // Load işaretli metrik serileri (metricLoadParts)
	CalendarTZ string
}

// GroupDecision — gölge terfi kararı.
type GroupDecision struct {
	Group     string   `json:"group"`
	WouldOpen bool     `json:"wouldOpen"`
	RuleID    string   `json:"rule"`
	Severity  string   `json:"severity,omitempty"`
	Priority  string   `json:"priority,omitempty"`
	Reason    string   `json:"reason"`
	Detectors []string `json:"detectors"`
	At        int64    `json:"at"` // unix sn
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

// DecideGroupPromotion — SAF: grubun gölge terfi kararı. members: grubun
// üye satırları (fazlası zararsız; kimliğe göre süzülür).
func DecideGroupPromotion(g MLGroup, members []chstore.MLEvent, in GroupGateInput, now time.Time) GroupDecision {
	d := GroupDecision{Group: g.ID, RuleID: MLGroupRulePrefix + g.ID, At: now.Unix()}
	inGroup := make(map[string]bool, len(g.Members))
	for _, id := range g.Members {
		inGroup[id] = true
	}
	dets, sigs := map[string]bool{}, map[string]bool{}
	peak, kept, cut := 1.0, 0, 0
	for _, e := range members {
		if !inGroup[e.ID] {
			continue
		}
		if !batchKeep(e, in.IsBatch, in.LoadParts) {
			cut++
			continue
		}
		kept++
		dets[e.Detector] = true
		sig := e.Signal
		if sig == "" {
			sig = chstore.MLDetectorSignal(e.Detector)
		}
		sigs[sig] = true
		peak = math.Max(peak, eventRatio(e))
	}
	d.Detectors = sortedKeys(dets)
	switch {
	case kept == 0 && cut > 0:
		d.Reason = "açmazdı: batch servisi, yalnız yük sinyali (batch kuralı)"
	case len(dets) < groupMinDetectors:
		d.Reason = fmt.Sprintf("açmazdı: tek dedektör (%s), başka dedektör teyit etmedi", strings.Join(d.Detectors, ", "))
	case in.CalendarTZ == "":
		d.Reason = "açmazdı: takvim dilimi tanımlı değil"
	default:
		d.WouldOpen = true
		d.Severity = "warning"
		if len(sigs) >= 2 || len(dets) >= 3 {
			d.Severity = "critical"
		}
		big := peak >= bigBreachRatio
		switch {
		case d.Severity == "critical" && big:
			d.Priority = "P1"
		case d.Severity == "critical", big:
			d.Priority = "P2"
		default:
			d.Priority = "P3"
		}
		d.Reason = fmt.Sprintf("açardı: %d dedektör teyit etti (%s), en büyük sapma %.1f kat", len(dets), strings.Join(d.Detectors, ", "), peak)
	}
	return d
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
// üzerinde gruplama + gölge kapı. S1 örnekleri gelince backtest/replay
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
type shadowCounts struct{ Groups, WouldOpen int64 }

// shadowPromoteGroups — recorder tik adımı (SAF çekirdek): bu tikte
// yazılacak olayların gruplarını süreç-içi koşu durumu (tracker; son 24 sa
// koşuları, kapanmışlar dahil) + bu tikin olaylarıyla kurar, karar verir
// ve kararı yazılacak üyenin kanıtına damgalar. CH/ES/VM okuması YOK.
// Lider değişince koşu durumu sıfırlanır (kabul edilen sapma, audit N7):
// yeni liderin ilk tiklerinde grup yalnız o anda açık olaylardan kurulur.
func shadowPromoteGroups(kept, running []chstore.MLEvent, slack time.Duration, in GroupGateInput, now time.Time) shadowCounts {
	byID := make(map[string]chstore.MLEvent, len(running)+len(kept))
	for _, e := range running {
		byID[e.ID] = e
	}
	for _, e := range kept {
		byID[e.ID] = e
	}
	all := make([]chstore.MLEvent, 0, len(byID))
	for _, e := range byID {
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	idx := GroupIndex(GroupMLEvents(all, slack))
	decided := map[string]GroupDecision{}
	var c shadowCounts
	for i := range kept {
		g := idx[kept[i].ID]
		if g == nil {
			continue
		}
		d, ok := decided[g.ID]
		if !ok {
			d = DecideGroupPromotion(*g, all, in, now)
			decided[g.ID] = d
			c.Groups++
			if d.WouldOpen {
				c.WouldOpen++
			}
		}
		kept[i].Evidence = stampGroupShadow(kept[i].Evidence, d)
	}
	return c
}
