package ml

// profile.go — v0.10.1816, öğrenme döngüsü Faz 1 dilim 1.5 (audit
// docs/anomaly/learning-loop-audit.md §4.4, §6.7): servis profilleri.
//
// Girdi: ml_baseline_obs'un trace "_calls" satırları (servis düzeyi trafik
// = route "|count" serilerinin kova toplamı, v0.10.1814) — saatlik çağrı.
// Satırı olmayan servis değerlendirilmez (profili "bilinmiyor" kalır; tahmin
// YOK) — TEK istisna adı batch kalıbına giren servis (v0.10.1876): batch
// kuralı ADDAN çıkar, sayı istemez; taban satırı olmasa da "batch".
// Log hacmi çağrı DEĞİL: yedek kaynak olarak kullanılmaz.
//
// Kurallar (sırayla, ilk tutan; §4.4 birebir):
//   batch          ad batch kalıplarıyla eşleşir (IsBatchService — tek yüklem)
//   low_traffic    günlük medyan çağrı < 2.000 VEYA mesai saatlerinde boş
//                  saat payı > %50
//   business_hours (mesai dışı medyan / mesai içi medyan) < 0,15 VE
//                  (hafta sonu / hafta içi günlük medyan) < 0,25
//   always_on      diğerleri
//
// Mesai: ürün ayarı yok → 08:30–18:00 yerel, takvim iş günleri (hafta içi,
// satır sınıfı holiday değil; half_day öğleden sonrası mesai dışı). Saat
// dilimi "mesai" sayılır ⇔ ortası (hh:30) [08:30, 18:00) içinde → 08..17.
// Boş = o saatte satır yok ya da çağrı 0. Gözlenen hafta = pencerede en az
// bir satırı olan hafta; o haftanın satırsız saatleri 0 çağrıdır (VM serisi
// bayatlayınca nokta düşer: boş saat gerçek sessizliktir).
//
// Histerezis: değişim iki ardışık haftalık değerlendirme ister — pencere
// [W−4h, W) ve bir hafta önceki [W−5h, W−1h) AYNI sınıfı verirse yazılır.
// Durumsuz: ikisi de ml_baseline_obs'tan, ek "bekleyen" kaydı yok.
// Elle sabitlenen profil sınıflandırıcıca EZİLMEZ.
//
// Profil bu sürümde HİÇBİR dedektöre girmez (dilim 1.6).

import (
	"math"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Profil adları.
const (
	ProfileBatch         = "batch"
	ProfileLowTraffic    = "low_traffic"
	ProfileBusinessHours = "business_hours"
	ProfileAlwaysOn      = "always_on"
	ProfileUnknown       = "unknown"
)

// Eşikler — tek yer (audit §4.4).
const (
	profileWindowWeeks     = 4
	profileLowDailyCalls   = 2000.0
	profileLowEmptyShare   = 0.5
	profileOffOnMax        = 0.15
	profileWeekendMax      = 0.25
	profileBusinessFromMin = 8*60 + 30 // 08:30
	profileBusinessToMin   = 18 * 60   // 18:00
)

// ProfileNames — elle sabitlenebilen profiller.
var ProfileNames = []string{ProfileBatch, ProfileLowTraffic, ProfileBusinessHours, ProfileAlwaysOn}

// ValidProfile — SAF.
func ValidProfile(p string) bool {
	for _, x := range ProfileNames {
		if x == p {
			return true
		}
	}
	return false
}

// ProfileEvidence — kuralın kullandığı sayılar (UI "Kanıt"). Tanımsız oran nil.
type ProfileEvidence struct {
	DailyMedianCalls float64  `json:"dailyMedianCalls"`
	EmptyBizShare    float64  `json:"emptyBizShare"`
	OffOnRatio       *float64 `json:"offOnRatio,omitempty"`
	WeekendRatio     *float64 `json:"weekendRatio,omitempty"`
	Weeks            int      `json:"weeks"`
}

// profileBusinessHour — SAF: saat diliminin ortası mesai aralığında mı.
func profileBusinessHour(h int) bool {
	mid := h*60 + 30
	return mid >= profileBusinessFromMin && mid < profileBusinessToMin
}

func medianOf(xs []float64) float64 {
	m, _ := medianMAD(xs)
	return m
}

// ClassifyProfile — SAF: [end − 4 hafta, end) penceresindeki "_calls"
// gözlemleri → profil + kanıt. end = bir ISO haftanın pazartesisi (UTC tarih,
// MLBaselineObs.WeekStart biçimi).
func ClassifyProfile(service string, obs []chstore.MLBaselineObs, end time.Time, isBatch func(string) bool) (string, ProfileEvidence) {
	from := end.AddDate(0, 0, -7*profileWindowWeeks)
	type cell struct {
		calls float64
		class string
		seen  bool
	}
	weeks := map[int64]*[168]cell{}
	for _, o := range obs {
		if o.WeekStart.Before(from) || !o.WeekStart.Before(end) || o.Slot > 167 {
			continue
		}
		w := weeks[o.WeekStart.Unix()]
		if w == nil {
			w = &[168]cell{}
			weeks[o.WeekStart.Unix()] = w
		}
		c := &w[o.Slot]
		c.calls += float64(o.Calls)
		c.class, c.seen = o.Class, true
	}
	ev := ProfileEvidence{Weeks: len(weeks)}
	batch := isBatch != nil && isBatch(service)
	if len(weeks) == 0 { // pencerede satır yok: tahmin yok — batch adı hariç (v0.10.1876)
		if batch {
			return ProfileBatch, ev
		}
		return ProfileUnknown, ev
	}
	keys := make([]int64, 0, len(weeks))
	for k := range weeks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var daily, weekday, weekend, biz, off []float64
	empty, bizTotal := 0, 0
	for _, k := range keys {
		w := weeks[k]
		for d := 0; d < 7; d++ {
			total, holiday := 0.0, false
			for h := 0; h < 24; h++ {
				c := w[d*24+h]
				total += c.calls
				if c.class == "holiday" {
					holiday = true
				}
			}
			daily = append(daily, total)
			switch {
			case d >= 5:
				weekend = append(weekend, total)
			case !holiday:
				weekday = append(weekday, total)
			}
			if d >= 5 || holiday {
				continue
			}
			for h := 0; h < 24; h++ {
				c := w[d*24+h]
				inBiz := profileBusinessHour(h) && c.class != "half_day"
				if inBiz {
					bizTotal++
					if c.calls == 0 {
						empty++
					}
					biz = append(biz, c.calls)
				} else {
					off = append(off, c.calls)
				}
			}
		}
	}
	ev.DailyMedianCalls = medianOf(daily)
	if bizTotal > 0 {
		ev.EmptyBizShare = float64(empty) / float64(bizTotal)
	}
	if m := medianOf(biz); m > 0 {
		r := medianOf(off) / m
		ev.OffOnRatio = &r
	}
	if m := medianOf(weekday); m > 0 && len(weekend) > 0 {
		r := medianOf(weekend) / m
		ev.WeekendRatio = &r
	}
	switch {
	case batch:
		return ProfileBatch, ev
	case ev.DailyMedianCalls < profileLowDailyCalls || ev.EmptyBizShare > profileLowEmptyShare:
		return ProfileLowTraffic, ev
	case ev.OffOnRatio != nil && *ev.OffOnRatio < profileOffOnMax && ev.WeekendRatio != nil && *ev.WeekendRatio < profileWeekendMax:
		return ProfileBusinessHours, ev
	}
	return ProfileAlwaysOn, ev
}

// profileDecision — SAF histerezis: güncel profil cur ("" = kayıt yok),
// bu hafta now, önceki hafta prev. Değişim yalnız iki ardışık değerlendirme
// aynı (ve cur'dan farklı) sınıfı verirse; sabitlenmiş profil değişmez.
func profileDecision(cur string, pinned bool, now, prev string) (string, bool) {
	if pinned || now == cur || now != prev {
		return cur, false
	}
	if cur == "" && now == ProfileUnknown {
		return cur, false // kayıt yokken "bilinmiyor" yazılmaz
	}
	return now, true
}

// roundEvidence — SAF: JSON'da okunur sayılar (3 anlamlı basamak).
func roundEvidence(ev ProfileEvidence) ProfileEvidence {
	r := func(v float64) float64 { return math.Round(v*1000) / 1000 }
	ev.DailyMedianCalls = math.Round(ev.DailyMedianCalls)
	ev.EmptyBizShare = r(ev.EmptyBizShare)
	if ev.OffOnRatio != nil {
		x := r(*ev.OffOnRatio)
		ev.OffOnRatio = &x
	}
	if ev.WeekendRatio != nil {
		x := r(*ev.WeekendRatio)
		ev.WeekendRatio = &x
	}
	return ev
}
