package ml

// impact_rule.go — v0.10.2372 (etki olayı programı, dilim I1; operatör kararı
// 2026-11-19: "tek etki olayı Problem açsın", emsal Dynatrace Davis): ETKİ
// OLAYI kuralının SAF çekirdeği. I1'de bağlantı yoktu; v0.10.2373 (I3) servis
// hata oranını (svc_error_rate.go svcImpactDecide) bağladı.
//
// NEDEN: eski servis kuralı 5 dakikalık 3 ardışık kova isterdi (v0.10.2373'te
// silindi), üstüne veri gecikmesi ve tik hizası → Problem en erken ~20 dk. Etki kuralı
// 1 DAKİKALIK örneklerle karar verir: son 5 örneğin en az 3'ü eşiği aşmış ve
// en son ölçülen örnek de aşıyorsa olay. Eşik hem GÖRELİ hem MUTLAK artışı
// birlikte ister (Dynatrace otomatik kipinin ilkesi: ikisi de aşılmalı) ve
// düşük yükte susar. Eşikler kod sabiti (ayar değil; K2 emsali).
//
// Kapanış: pencerenin 5 örneği de ölçülmüş ve hiçbiri eşiği aşmıyorsa "temiz".
// Ölçülmemiş dakika ihlal DEĞİL, temiz de DEĞİL (scrape boşluğu ≠ 0); eşiğin
// üstünde olup yük kapısından geçemeyen dakika da öyle (az trafikte yüksek
// oran olayı ne açar ne kapatır).

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// impactKind — etki ölçüsü.
type impactKind string

const (
	impactFailureRate    impactKind = "failure_rate"    // hata oranı, 0..1
	impactResponseMedian impactKind = "response_median" // medyan yanıt süresi, ms
	impactResponseSlow   impactKind = "response_slow"   // en yavaş %10 (p90), ms
	impactConsumerLag    impactKind = "consumer_lag"    // consumer lag, kayıt
)

const (
	impactWindow     = 5  // karar penceresi: son N bir dakikalık örnek
	impactViolations = 3  // penceredeki asgari ihlal
	impactStepSec    = 60 // örnek adımı (1 dk; kod sabiti)
)

// impactRule — bir ölçünün eşik kuralı. Eşik = max(Floor, taban·Rel,
// taban+Abs): göreli VE mutlak artış birlikte aşılmalı. MinLoad > 0 ise o
// dakikadaki istek sayısı bunun altındayken örnek ihlal sayılmaz.
type impactRule struct {
	Rel     float64 // tabanın katı
	Abs     float64 // tabana eklenen mutlak artış (ölçünün biriminde)
	Floor   float64 // tabandan bağımsız mutlak alt sınır
	MinLoad float64 // istek/dk; 0 = yük kapısı yok
}

// impactRules — ölçü başına kural. Hata oranı: +%50 göreli VE +1 puan mutlak,
// en az %1 (taban 0 → %1; %0,4 → %1,4; %5 → %7,5). Medyan yanıt: +%50 VE
// +100 ms. En yavaş %10: 2 kat VE +1 sn. Lag: 2 kat ve en az lag etki tabanı
// (chstore.MLKafkaLagImpactFloor); yük kapısı yok (lag istek saymaz).
var impactRules = map[impactKind]impactRule{
	impactFailureRate:    {Rel: 1.5, Abs: 0.01, Floor: 0.01, MinLoad: 10},
	impactResponseMedian: {Rel: 1.5, Abs: 100, MinLoad: 10},
	impactResponseSlow:   {Rel: 2.0, Abs: 1000, MinLoad: 10},
	impactConsumerLag:    {Rel: 2.0, Floor: chstore.MLKafkaLagImpactFloor},
}

// threshold — SAF: tabana göre ihlal eşiği. Negatif / NaN taban 0 sayılır.
func (r impactRule) threshold(base float64) float64 {
	if math.IsNaN(base) || base < 0 {
		base = 0
	}
	return math.Max(r.Floor, math.Max(base*r.Rel, base+r.Abs))
}

// impactSample — bir dakikalık örnek. OK=false: o dakika ölçülmedi.
type impactSample struct {
	Value float64 // ölçü (kind'ın biriminde)
	Load  float64 // o dakikadaki istek sayısı (lag'de kullanılmaz)
	OK    bool
}

// impactVerdict — etki kararı.
type impactVerdict struct {
	Fire       bool
	Clear      bool    // pencere tam ölçüldü, ihlal yok → olay kapanabilir
	Reason     string  // Fire=false iken neden (tanı; arayüz metni değil)
	Violations int     // penceredeki ihlal sayısı
	Measured   int     // penceredeki ölçülmüş örnek sayısı
	Threshold  float64 // uygulanan eşik
	Observed   float64 // en son ölçülen örneğin değeri
	Peak       float64 // penceredeki en büyük ihlal değeri
	Onset      int     // samples içinde ilk ihlalin indisi (-1 = yok)
}

// impactDecide — SAF: samples zaman sırasında (en eski → en yeni) bir
// dakikalık örnekler; yalnız son impactWindow örnek okunur. base = mevsimsel
// taban (aynı birimde). Bilinmeyen ölçü ateşlemez.
func impactDecide(kind impactKind, samples []impactSample, base float64) impactVerdict {
	v := impactVerdict{Onset: -1}
	rule, ok := impactRules[kind]
	if !ok {
		v.Reason = "bilinmeyen ölçü"
		return v
	}
	v.Threshold = rule.threshold(base)
	off := 0
	if len(samples) > impactWindow {
		off = len(samples) - impactWindow
	}
	lastViolates, haveLast, lowLoad := false, false, 0
	for i := off; i < len(samples); i++ {
		s := samples[i]
		if !s.OK || math.IsNaN(s.Value) {
			continue
		}
		v.Measured++
		over := s.Value >= v.Threshold
		viol := over && (rule.MinLoad <= 0 || s.Load >= rule.MinLoad)
		if over && !viol {
			lowLoad++ // eşiğin üstünde ama yük az: ihlal değil, temiz de değil
		}
		v.Observed, lastViolates, haveLast = s.Value, viol, true
		if !viol {
			continue
		}
		v.Violations++
		if v.Onset < 0 {
			v.Onset = i
		}
		v.Peak = math.Max(v.Peak, s.Value)
	}
	switch {
	case !haveLast:
		v.Reason = "ölçüm yok"
	case v.Violations >= impactViolations && lastViolates:
		v.Fire = true
	case v.Violations >= impactViolations:
		v.Reason = "son örnek eşiğin altında (düzeliyor)"
	case v.Violations == 0 && lowLoad == 0 && v.Measured >= impactWindow:
		v.Clear = true
		v.Reason = "pencere temiz"
	case v.Violations == 0 && lowLoad > 0:
		v.Reason = "eşiğin üstünde ama yük az (karar verilemez)"
	default:
		v.Reason = fmt.Sprintf("ihlal %d/%d, en az %d gerekir", v.Violations, impactWindow, impactViolations)
	}
	return v
}

// ── ölçüler arası ortak parçalar (v0.10.2382, I5: hata oranı + consumer lag) ──

// impactMinuteOf — SAF: aralık sorgusu noktasının (unix ns) DAKİKA BAŞI. VM
// noktası t, (t − 60 sn, t] aralığını temsil eder (sayaçta o dakikanın artışı,
// gauge'da dakikanın kapanış okuması) → dakika başı t − 60. Pencere
// [end − 5 dk, end) dışındaki nokta ok=false.
func impactMinuteOf(pointNs, end int64) (int64, bool) {
	m := pointNs/1e9/impactStepSec*impactStepSec - impactStepSec
	return m, m >= end-impactWindow*impactStepSec && m < end
}

// impactEmit — SAF: karar bu tik olay yazar mı: yeni ateşleme ya da AÇIK
// koşunun uzatması (kural "temiz" demiyor ve pencerede en az bir ihlal var).
func impactEmit(v impactVerdict, open bool) bool {
	return v.Fire || (open && !v.Clear && v.Violations > 0)
}

// impactRunStart — SAF: olayın başlangıcı (unix ns) = ilk ihlal dakikasının
// başı (onset: n örnekli pencerede indis, −1 = yok → en yeni dakika). Açık
// koşunun yazılmış başlangıcı (openStart, 0 = yok) daha erkense o kalır:
// pencere kayarken başlangıç oynamaz.
func impactRunStart(end time.Time, n, onset int, openStart int64) int64 {
	started := (end.Unix() - impactStepSec) * 1e9
	if onset >= 0 {
		started = (end.Unix() - int64(n-onset)*impactStepSec) * 1e9
	}
	if openStart > 0 && openStart < started {
		started = openStart
	}
	return started
}

// impactMark — olay kanıtındaki etki kuralı damgası (rule=impact): olay 1
// dakikalık örneklerle ve etki kuralıyla açıldı. Problem kapısı, kalite
// süzgeci ve olay kaydı olayı bu damgadan tanır.
type impactMark struct {
	Fired      bool    // bu yazımda kural ateşliyor (false = açık koşunun uzatması)
	Threshold  float64 // uygulanan eşik
	Violations int     // penceredeki ihlal
	Window     int     // pencere (dakika)
}

// impactMarkOf — SAF: kanıttan damga (yoksa ok=false).
func impactMarkOf(evidence string) (impactMark, bool) {
	if !strings.Contains(evidence, `"impact"`) { // çoğu olayda damga yok: çözümleme yapma
		return impactMark{}, false
	}
	var m struct {
		Rule       string  `json:"rule"`
		Fired      bool    `json:"fired"`
		Threshold  float64 `json:"threshold"`
		Violations int     `json:"violations"`
		Window     int     `json:"window"`
	}
	if json.Unmarshal([]byte(evidence), &m) != nil || m.Rule != "impact" {
		return impactMark{}, false
	}
	return impactMark{Fired: m.Fired, Threshold: m.Threshold, Violations: m.Violations, Window: m.Window}, true
}
