package ml

// why_not_problem.go — v0.10.2482 (Sinyaller: "neden Problem değil (henüz)").
// Problems › Sinyaller satırının kısa durumu YALNIZ kapının kendi kararından
// türer: üyelerin kanıtındaki grup kararı (groupShadow — DecideGroupPromotion
// her tik yazar), servis düzeyi kararı (svcShadow — decideSvcShadow) ve tek
// sinyal kolunun üye şartı (singleArmCheck — singleSignalShadow'un AYNI
// işlevi). Dedektör yeniden koşmaz, ek sorgu yok: satırlar zaten okunmuş.
//
// Kalan süre ("N dk daha sürerse Problem açılır") yalnız kural onu KESİN
// kıldığında verilir: karar bitmemiş, satır sürüyor, canlı kip açık (kapı
// kararı Problem açar) ve bir üyenin tek karşılanmayan şartı sürdürme
// penceresi (SingleUnmetShort: diğer bütün şartlar şimdi karşılanıyor). Aksi
// hâlde karşılanmayan şart süresiz söylenir. Cümleyi arayüz kurar (TR / EN);
// burada kod + parametre.

import (
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Durum kodları (FE lib/whyNotProblem.ts ile aynı küme).
const (
	WhyGatePassed         = "gate_passed"          // kapı "açar" dedi; bağlı açık Problem yok
	WhyEnded              = "ended"                // grup bitti, Problem şartı oluşmadı
	WhyBatch              = "batch"                // batch servisi, yalnız yük sinyali
	WhySingle             = "single"               // tek sinyal, ikinci sinyal teyit etmedi
	WhySingleShort        = "single_short"         // tek sinyal, sürdürme penceresi dolmadı
	WhySingleBelowRatio   = "single_below_ratio"   // tek sinyal, normalin N katının altında
	WhySingleBelowCount   = "single_below_count"   // tek sinyal, taban 0 iken sayım N'in altında
	WhySingleNotWorsening = "single_not_worsening" // tek sinyal, kötüleşme yönünde değil
)

// WhyNotProblem — satırın durumu. Running: satır sürüyor mu (liste bölümü).
// EligibleAt (unix sn): yalnız kesin kalan süre varken — o anda sürdürme
// penceresi dolar ve kapı açar. Minutes / Ratio / Count: karşılanmayan
// şartın eşiği (metin için).
type WhyNotProblem struct {
	Code       string   `json:"code"`
	Running    bool     `json:"running"`
	EligibleAt int64    `json:"eligibleAt,omitempty"`
	Minutes    int      `json:"minutes,omitempty"`
	Ratio      float64  `json:"ratio,omitempty"`
	Count      float64  `json:"count,omitempty"`
	Signals    []string `json:"signals,omitempty"`
}

// WhyNotInput — kapının dış girdileri + satır bağlamı.
type WhyNotInput struct {
	Gate    GroupGateInput
	Live    bool // chstore.MLConfig.GroupProblemsOn: kapı kararı Problem açar mı
	Running bool // satır süren bölümde mi (ml.UnifiedOpen)
	Now     time.Time
}

// singleUnmetRank — satırda gösterilecek şart: Problem'e en yakın olan.
var singleUnmetRank = map[string]int{
	SingleUnmetShort: 1, SingleUnmetBelowRatio: 2, SingleUnmetBelowCount: 2,
	SingleUnmetNotWorsening: 3, SingleUnmetBatch: 4,
}

// WhyNotProblemOf — SAF: satırın üyelerinden (ML olayları) durum. ok=false:
// kanıtta kapı kararı yok (eski satır) — durum gösterilmez.
func WhyNotProblemOf(members []chstore.MLEvent, in WhyNotInput) (WhyNotProblem, bool) {
	out := WhyNotProblem{Running: in.Running}
	var d GroupDecision
	hasD, svcPassed := false, false
	for _, e := range members {
		if gd, ok := GroupShadowOf(e.Evidence); ok && (!hasD || gd.At > d.At) {
			d, hasD = gd, true
		}
		if s, ok := SvcShadowOf(e.Evidence); ok && s.WouldOpen {
			svcPassed = true
		}
	}
	if svcPassed || (hasD && d.WouldOpen) {
		out.Code = WhyGatePassed
		return out, true
	}
	if !hasD {
		return WhyNotProblem{}, false
	}
	out.Signals = d.Signals
	switch {
	case len(d.Signals) == 0 && !d.Finished:
		out.Code = WhyBatch // kept == 0 && cut > 0 (DecideGroupPromotion batch dalı)
		return out, true
	case len(d.Signals) != 1:
		out.Code = WhyEnded
		return out, true
	}
	return singleWhy(members, d, in, out), true
}

// singleWhy — SAF: tek sinyalli grubun üye şartları (singleArmCheck).
// Süren karar: şimdiki anla. Bitmiş karar / biten satır: her üye son
// görüldüğü anda, açık sayılarak (kural o anda neyi karşılamadı).
func singleWhy(members []chstore.MLEvent, d GroupDecision, in WhyNotInput, out WhyNotProblem) WhyNotProblem {
	live := in.Running && !d.Finished
	best, bestRank := "", 0
	var wait time.Duration
	for _, e := range members {
		if isSvcDetector(e.Detector) { // kendi kapısı (svcShadow); tek sinyal kolu saymaz
			continue
		}
		gin, now := in.Gate, in.Now
		if !live {
			gin.Open = nil
			e.Stage = ""
			now = time.Unix(0, e.LastSeen)
		}
		c := singleArmCheck(e, gin, now)
		r, ok := singleUnmetRank[c.Unmet]
		if !ok {
			continue // açar (bayat karar) ya da hiç aday değil: özel şart söylenmez
		}
		if best == "" || r < bestRank {
			best, bestRank, wait = c.Unmet, r, c.Wait
		} else if c.Unmet == SingleUnmetShort && c.Wait < wait {
			wait = c.Wait
		}
	}
	switch best {
	case SingleUnmetShort:
		out.Code, out.Minutes = WhySingleShort, int(singleMinSustained/time.Minute)
		if live && in.Live && wait > 0 {
			out.EligibleAt = in.Now.Add(wait).Unix()
		}
	case SingleUnmetBelowRatio:
		out.Code, out.Ratio = WhySingleBelowRatio, singleMinRatio
	case SingleUnmetBelowCount:
		out.Code, out.Count = WhySingleBelowCount, singleCountOnlyMin
	case SingleUnmetNotWorsening:
		out.Code = WhySingleNotWorsening
	case SingleUnmetBatch:
		out.Code = WhyBatch
	default:
		out.Code = WhySingle
	}
	return out
}

// ReadGateInput — SAF: okuma ucunun kapı girdisi; kaydedicinin girdisinin
// satırlardan kurulabilen kısmı (recorder: IsBatch = AnomalySensitivityFor
// Detectors().IsBatchService, LoadParts = metricLoadParts(cfg)). Kip on iken
// Published satırların son yazım kararıdır (publish_state). Open nil =
// stage'e bak (replay kuralı); LagHistory yalnız öncelik kurar, durumu
// etkilemez.
func ReadGateInput(cfg chstore.MLConfig, isBatch func(string) bool, items []chstore.MLEvent) GroupGateInput {
	in := GroupGateInput{IsBatch: isBatch, LoadParts: metricLoadParts(cfg)}
	if cfg.PublishGatesMode() == chstore.MLGatesOn {
		in.Published = make(map[string]PubVerdict, len(items))
		for _, e := range items {
			in.Published[e.ID] = PubVerdict{State: e.PublishState}
		}
	}
	return in
}
