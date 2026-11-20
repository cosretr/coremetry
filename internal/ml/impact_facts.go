package ml

// impact_facts.go — v0.10.2383 (triage mockup dilim 2): Problem standardının
// "Neden böyle değerlendiriyoruz" satırlarının GERÇEKLERİ. Servis hata oranı
// olayının kanıtı (svc_error_rate.go svcImpactEvent: rule "impact", eşik,
// ihlal sayısı, pencere, dakika dakika oran + yük) kararın kendisini taşır;
// bu dosya onu chstore.StdImpactRule'a çevirir.
//
// Kuralın katsayıları (taban × 1,5, +1 puan, en az %1) kanıtta YOK; tek
// tanımları impact_rule.go impactRules. chstore ve arayüz ikinci bir kopya
// tutmaz — değerler buradan gider. Kanıttaki eşik bugünkü sabitlerle yeniden
// hesaplanan eşikle uyuşmuyorsa (sabitler olaydan sonra değişmiş) katsayılar
// YAZILMAZ: satır yalnız eşiği söyler, yanlış bir formül iddia etmez.

import (
	"encoding/json"
	"math"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ImpactFactsDetector — gerçekleri taşıyan olayın dedektörü (detay ucu
// yalnız bu dedektörün olaylarını okur).
const ImpactFactsDetector = detSvcErrorRate

// impactFactsEvidence — kanıtın okunan alanları (svcImpactEvent yazar).
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
}

// ImpactRuleFacts — SAF: servis hata oranı olayı → etki kuralı gerçekleri.
// nil = uygun değil (başka dedektör, v0.10.2373 öncesi kanıt, bozuk / eksik
// alan): çağıran bugünkü tek cümleyi çizer.
func ImpactRuleFacts(e chstore.MLEvent) *chstore.StdImpactRule {
	if e.Detector != detSvcErrorRate || e.Evidence == "" {
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
	f := &chstore.StdImpactRule{Window: ev.Window, Violations: ev.Violations, MinViolations: ev.MinViolations,
		Threshold: ev.Threshold, MinLoad: ev.MinLoad, Unsampled: ev.Source == "vm"}
	// Eşiğin nasıl kurulduğu: yalnız bugünkü sabitler bu olayın eşiğini üretiyorsa.
	rule := impactRules[impactFailureRate]
	if math.Abs(rule.threshold(e.Baseline)-ev.Threshold) <= 1e-9 {
		f.Floor, f.Rel, f.Abs = rule.Floor, rule.Rel, rule.Abs
	}
	// Yük: en yeni İHLAL dakikasının isteği (eşiğin üstünde VE yük kapısını
	// geçmiş — impactDecide'ın ihlal tanımı). İhlal dakikası bulunamazsa 0.
	for i := len(ev.Ratios) - 1; i >= 0; i-- {
		if r := ev.Ratios[i]; r != nil && i < len(ev.Loads) && *r >= ev.Threshold && ev.Loads[i] >= ev.MinLoad {
			f.LoadPerMin = ev.Loads[i]
			break
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
