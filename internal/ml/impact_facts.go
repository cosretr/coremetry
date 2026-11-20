package ml

// impact_facts.go — v0.10.2383 (triage mockup dilim 2): Problem standardının
// "Neden böyle değerlendiriyoruz" satırlarının GERÇEKLERİ. Servis hata oranı
// olayının kanıtı (svc_error_rate.go svcImpactEvent: rule "impact", eşik,
// ihlal sayısı, pencere, dakika dakika oran + yük) kararın kendisini taşır;
// bu dosya onu chstore.StdImpactRule'a çevirir.
//
// Kuralın katsayıları (taban × 1,5, +1 puan, en az %1) kanıtta YOK; tek
// tanımları impact_rule.go impactRules. chstore ve arayüz ikinci bir kopya
// tutmaz — değerler buradan gider. v0.10.2391: aynı okuma servis yanıt
// süresi (rtEvent; ölçü medyan / en yavaş %10, ms) ve consumer lag
// (lagImpactEvent; kayıt, yük kapısı yok) olayları için. Kanıttaki eşik
// bugünkü sabitlerle yeniden hesaplanan eşikle uyuşmuyorsa (sabitler olaydan sonra değişmiş) katsayılar
// YAZILMAZ: satır yalnız eşiği söyler, yanlış bir formül iddia etmez.

import (
	"encoding/json"
	"math"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ImpactFactsDetector — gerçekleri taşıyan olayın dedektörü (detay ucu
// yalnız bu dedektörün olaylarını okur).
const ImpactFactsDetector = detSvcErrorRate

// ImpactFactsDetectorFor — SAF (v0.10.2391): ML servis Problem'inin ölçüsü →
// gerçekleri taşıyan dedektör + beklenen ölçü (StdImpactRule.Measure).
// ok=false: ölçünün etki kuralı yok (yaygın yavaşlama p95_ms vb.) — okuma yok.
// Problem ölçüsünün tek tanımı svcMetric.
func ImpactFactsDetectorFor(metric string) (detector, measure string, ok bool) {
	switch metric {
	case "error_rate":
		return detSvcErrorRate, "", true
	case rtMeasures[0].agg + "_ms":
		return detSvcResponseTime, rtMeasures[0].key, true
	case rtMeasures[1].agg + "_ms":
		return detSvcResponseTime, rtMeasures[1].key, true
	}
	return "", "", false
}

// impactFactsEvidence — kanıtın okunan alanları (svcImpactEvent, rtEvent,
// lagImpactEvent yazar).
type impactFactsEvidence struct {
	Rule          string     `json:"rule"`
	Source        string     `json:"source"`
	Threshold     float64    `json:"threshold"`
	Violations    int        `json:"violations"`
	Window        int        `json:"window"`
	MinViolations int        `json:"minViolations"`
	MinLoad       float64    `json:"minLoad"`
	Ratios        []*float64 `json:"ratios"` // null = o dakika ölçülmedi
	Loads         []float64  `json:"loads"`
	// v0.10.2391 — yanıt süresi: olayın ölçüsü (ml_series part) + dakika
	// dakika yüzdelik (ms); consumer lag: dakika dakika lag (kayıt).
	Measure string     `json:"measure"`
	P50s    []*float64 `json:"p50s"`
	P90s    []*float64 `json:"p90s"`
	Values  []*float64 `json:"values"`
}

// impactFactsKind — SAF: olay → etki ölçüsü + gerçeklerin Measure'ı +
// dakika dakika değerler. ok=false: etki kuralı gerçeği taşımayan olay.
func impactFactsKind(e chstore.MLEvent, ev impactFactsEvidence) (kind impactKind, measure string, vals []*float64, ok bool) {
	switch e.Detector {
	case detSvcErrorRate:
		return impactFailureRate, "", ev.Ratios, true
	case detSvcResponseTime:
		for i, m := range rtMeasures {
			if ev.Measure == m.part {
				return m.kind, m.key, [2][]*float64{ev.P50s, ev.P90s}[i], true
			}
		}
	case detMetricKafka:
		if isLagImpactPart(e.Code) {
			return impactConsumerLag, chstore.MLImpactLag, ev.Values, true
		}
	}
	return "", "", nil, false
}

// ImpactRuleFacts — SAF: etki kuralı olayı → gerçekler. Servis hata oranı
// (v0.10.2383), servis yanıt süresi ve consumer lag (v0.10.2391). nil = uygun
// değil (başka dedektör, etki kuralı öncesi kanıt, bozuk / eksik alan):
// çağıran bugünkü tek cümleyi çizer.
func ImpactRuleFacts(e chstore.MLEvent) *chstore.StdImpactRule {
	if e.Evidence == "" || (e.Detector != detSvcErrorRate && e.Detector != detSvcResponseTime && e.Detector != detMetricKafka) {
		return nil
	}
	var ev impactFactsEvidence
	if json.Unmarshal([]byte(e.Evidence), &ev) != nil || ev.Rule != "impact" {
		return nil
	}
	if ev.Window <= 0 || ev.MinViolations <= 0 || ev.Violations <= 0 || ev.Violations > ev.Window ||
		!(ev.Threshold > 0) || math.IsInf(ev.Threshold, 0) {
		return nil
	}
	kind, measure, vals, ok := impactFactsKind(e, ev)
	if !ok {
		return nil
	}
	f := &chstore.StdImpactRule{Window: ev.Window, Violations: ev.Violations, MinViolations: ev.MinViolations,
		Threshold: ev.Threshold, MinLoad: ev.MinLoad, Unsampled: ev.Source == "vm", Measure: measure}
	// Eşiğin nasıl kurulduğu: yalnız bugünkü sabitler bu olayın eşiğini üretiyorsa.
	rule := impactRules[kind]
	if math.Abs(rule.threshold(e.Baseline)-ev.Threshold) <= 1e-9*math.Max(1, ev.Threshold) {
		f.Floor, f.Rel, f.Abs = rule.Floor, rule.Rel, rule.Abs
	}
	// Yük: en yeni İHLAL dakikasının isteği (eşiğin üstünde VE yük kapısını
	// geçmiş — impactDecide'ın ihlal tanımı). İhlal dakikası bulunamazsa ya da
	// kuralın yük kapısı yoksa (lag) 0.
	if f.MinLoad > 0 {
		for i := len(vals) - 1; i >= 0; i-- {
			if r := vals[i]; r != nil && i < len(ev.Loads) && *r >= ev.Threshold && ev.Loads[i] >= ev.MinLoad {
				f.LoadPerMin = ev.Loads[i]
				break
			}
		}
	}
	return f
}

// NewestImpactRuleFacts — SAF: olaylar arasında gerçekleri okunabilen EN YENİ
// (son görülme) olayın gerçekleri; yoksa nil.
func NewestImpactRuleFacts(evs []chstore.MLEvent) *chstore.StdImpactRule {
	var best *chstore.StdImpactRule
	var seen int64
	for _, e := range evs {
		if best != nil && e.LastSeen <= seen {
			continue
		}
		if f := ImpactRuleFacts(e); f != nil {
			best, seen = f, e.LastSeen
		}
	}
	return best
}

// ImpactFactsLeadOf — SAF (v0.10.2391): tek sinyal kolunun açtığı grup
// Problem'i (`anomaly-auto:group:<olay>`, Value = sinyal sayısı = 1, ölçü
// hâlâ grubun) → lider olayın kimliği (GroupRuleID: kural kimliğindeki ilk
// üye; kaynak işaretinin de okuduğu olay, groupLead). Consumer lag etki olayı
// Problem'i bu koldan açar. ≥ 2 sinyalli grup ya da servis düzeyi ölçünün
// devraldığı satır ok=false: tek olayın kuralı Problem'in tamamını açıklamaz.
func ImpactFactsLeadOf(p chstore.Problem) (string, bool) {
	if p.Metric != groupProblemMetric || p.Value != 1 {
		return "", false
	}
	return GroupEventOfRule(p.RuleID)
}
