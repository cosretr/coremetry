package ml

// impact_summary.go — v0.10.2482 ("etki öncelikte" adım 1, YALNIZ GÖSTERİM):
// Problem'in lider etki olayı → etki özeti (chstore.StdImpactSummary).
// Sayılar yalnız olayın kayıtlı kanıtından (impactFactsEvidence: dakika
// dakika oran / yüzdelik / lag + istek yükü) ve OOM'da Problem'in kendi
// sayılarından; yeni ölçü sorgusu yok.
//
//   - hata oranı (servis / uç nokta): başarısız istek/dk = medyan(oran × yük),
//     pay = medyan(oran) — uç nokta olayında payın paydası uç noktanın trafiği.
//   - yanıt süresi (medyan / en yavaş %10; servis / uç nokta): etkilenen
//     istek/dk = medyan(yük), gecikme = medyan(ölçü) − taban (olayın Baseline'ı).
//   - consumer lag: biriken = en yeni ölçülen dakikanın lag'i; artış/dk =
//     (son − ilk ölçülen) / aradaki dakika (≥ 2 ölçülmüş dakika).
//   - OOMKilled: düşen pod / iş yükünün pod'ları (chstore.OOMImpactSummary).
//   - etki kuralı olmayan her şey (kullanıcı alarm kuralı, monitör, klasik
//     anomali, ≥ 2 sinyalli grup): nil.
//
// Seçim detay başlığının "Neden" satırlarıyla AYNI (ImpactLeadReadOf +
// ImpactLeadEvent): satır ile başlık aynı olayın sayısını söyler.

import (
	"encoding/json"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// impactSummaryMinMinutes — özetin okunabilmesi için asgari ölçülmüş dakika
// (oran / yanıt süresi: değer + sıfırdan büyük yük). Tek dakika medyan
// değildir. Lag'de biriken tek ölçülmüş dakikadan okunur (artış ≥ 2 ister).
const impactSummaryMinMinutes = 2

// ImpactLeadRead — Problem'in etki olayını hangi okumayla bulacağımız.
type ImpactLeadRead struct {
	Detectors []string // servis Problem'i: servis × bu dedektörler
	LeadID    string   // grup Problem'i: lider olay kimliği
	Measure   string   // beklenen StdImpactRule.Measure (Problem'in KENDİ ölçüsü)
}

// ImpactLeadReadOf — SAF: Problem'in etki olayı okunur mu, nasıl.
//
//   - ML servis Problem'i (`anomaly-auto:svc:`), ölçü hata oranı ya da yanıt
//     süresi: servis × (servis dedektörü + uç nokta etkisi).
//   - Tek sinyal kolunun grup Problem'i (consumer lag): lider olay.
//
// Kapanmış Problem okunmaz: kanıt olayın SON tikinin penceresidir.
func ImpactLeadReadOf(p chstore.Problem) (ImpactLeadRead, bool) {
	if p.Service == "" || p.Status == "resolved" {
		return ImpactLeadRead{}, false
	}
	if chstore.IsMLSvcProblem(p.RuleID) {
		det, measure, ok := ImpactFactsDetectorFor(p.Metric)
		if !ok {
			return ImpactLeadRead{}, false
		}
		return ImpactLeadRead{Detectors: []string{det, EndpointImpactDetector}, Measure: measure}, true
	}
	if id, ok := ImpactFactsLeadOf(p); ok {
		return ImpactLeadRead{LeadID: id, Measure: chstore.MLImpactLag}, true
	}
	return ImpactLeadRead{}, false
}

// ImpactLeadEvent — SAF: okunan olaylardan Problem'in lider etki olayı +
// gerçekleri. Servis Problem'i: Problem'e bağlı, gerçekleri okunabilen EN
// YENİ olay (yalnız rd.Detectors); grup Problem'i: lider olay. Ölçü
// Problem'inkiyle uyuşmazsa ok=false.
func ImpactLeadEvent(rd ImpactLeadRead, p chstore.Problem, evs []chstore.MLEvent) (chstore.MLEvent, *chstore.StdImpactRule, bool) {
	var lead chstore.MLEvent
	var f *chstore.StdImpactRule
	if rd.LeadID != "" {
		for _, e := range evs {
			if e.ID == rd.LeadID && e.Service == p.Service {
				lead, f = e, ImpactRuleFacts(e)
				break
			}
		}
	} else {
		var cand []chstore.MLEvent
		for _, e := range evs {
			if e.Service == p.Service && containsStr(rd.Detectors, e.Detector) {
				cand = append(cand, e)
			}
		}
		lead, f = newestImpactRuleEvent(chstore.ProblemEventsOf(cand, p, chstore.ProblemEventSlack))
	}
	if f == nil || f.Measure != rd.Measure {
		return chstore.MLEvent{}, nil, false
	}
	return lead, f, true
}

// newestImpactRuleEvent — SAF: gerçekleri okunabilen EN YENİ (son görülme)
// olay (NewestImpactRuleFacts'ın olaylı hali).
func newestImpactRuleEvent(evs []chstore.MLEvent) (chstore.MLEvent, *chstore.StdImpactRule) {
	var best *chstore.StdImpactRule
	var lead chstore.MLEvent
	for _, e := range evs {
		if best != nil && e.LastSeen <= lead.LastSeen {
			continue
		}
		if f := ImpactRuleFacts(e); f != nil {
			best, lead = f, e
		}
	}
	return lead, best
}

// ProblemImpactSummary — SAF: Problem + okunan olaylar → etki özeti; nil =
// etki ölçüsü yok. OOM Problem'i olay okumadan (Problem'in sayıları).
func ProblemImpactSummary(p chstore.Problem, evs []chstore.MLEvent) *chstore.StdImpactSummary {
	if p.Status == "resolved" {
		return nil
	}
	if s := chstore.OOMImpactSummary(p); s != nil {
		return s
	}
	rd, ok := ImpactLeadReadOf(p)
	if !ok {
		return nil
	}
	e, _, ok := ImpactLeadEvent(rd, p, evs)
	if !ok {
		return nil
	}
	return ImpactSummaryOf(e)
}

// ImpactSummaryOf — SAF: etki kuralı olayı → özet. nil: etki kuralı olayı
// değil, kanıt bozuk / kısa (< 2 ölçülmüş dakika), yük sıfır.
func ImpactSummaryOf(e chstore.MLEvent) *chstore.StdImpactSummary {
	if ImpactRuleFacts(e) == nil {
		return nil
	}
	var ev impactFactsEvidence
	if json.Unmarshal([]byte(e.Evidence), &ev) != nil {
		return nil
	}
	kind, measure, vals, ok := impactFactsKind(e, ev)
	if !ok {
		return nil
	}
	scope, route := chstore.ImpactScopeService, ""
	if e.Detector == detEndpointImpact {
		scope, route = chstore.ImpactScopeRoute, e.Code
	}
	switch kind {
	case impactFailureRate:
		var failing, ratios []float64
		for i, r := range vals {
			if r != nil && i < len(ev.Loads) && ev.Loads[i] > 0 {
				failing = append(failing, *r*ev.Loads[i])
				ratios = append(ratios, *r)
			}
		}
		if len(failing) < impactSummaryMinMinutes {
			return nil
		}
		return chstore.FinishImpactSummary(&chstore.StdImpactSummary{Kind: chstore.ImpactSumErrors, Scope: scope, Route: route,
			PerMin: medianOf(failing), SharePct: medianOf(ratios) * 100})
	case impactResponseMedian, impactResponseSlow:
		var loads, ms []float64
		for i, v := range vals {
			if v != nil && i < len(ev.Loads) && ev.Loads[i] > 0 {
				loads = append(loads, ev.Loads[i])
				ms = append(ms, *v)
			}
		}
		if len(loads) < impactSummaryMinMinutes {
			return nil
		}
		s := &chstore.StdImpactSummary{Kind: chstore.ImpactSumLatency, Scope: scope, Route: route, Measure: measure, PerMin: medianOf(loads)}
		if d := medianOf(ms) - e.Baseline; e.Baseline > 0 && d > 0 {
			s.DelayMs = d
		}
		return chstore.FinishImpactSummary(s)
	case impactConsumerLag:
		first, last := -1, -1
		for i, v := range vals {
			if v != nil {
				if first < 0 {
					first = i
				}
				last = i
			}
		}
		if first < 0 {
			return nil
		}
		s := &chstore.StdImpactSummary{Kind: chstore.ImpactSumLag, Backlog: *vals[last]}
		if last > first {
			g := (*vals[last] - *vals[first]) / float64(last-first)
			s.GrowthPerMin = &g
		}
		return chstore.FinishImpactSummary(s)
	}
	return nil
}
