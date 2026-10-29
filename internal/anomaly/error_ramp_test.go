package anomaly

// error_ramp_test.go — sürekli teknik hata (5xx) yükselişi kuralı + operatörün
// prod vakasının (2026-10-24, mobil BFF gateway) sentetik replay'i.
//
// Senaryo saatleri UTC (yerel = UTC+3): 03:00 UTC = 06:00 yerel.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// incidentDay — vaka günü (Perşembe).
var incidentDay = time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)

// noise — deterministik ±%20 çarpan (kova başına).
func noise(b int64) float64 {
	x := uint64(b) * 0x9E3779B97F4A7C15
	x ^= x >> 29
	return 0.8 + 0.4*float64(x%1000)/1000
}

// gwTraffic — gateway toplam istek/sn (2xx+4xx+5xx), gün içi şekil.
func gwTraffic(t time.Time, mult float64) float64 {
	h := float64(t.UTC().Hour()) + float64(t.UTC().Minute())/60
	var rps float64
	switch {
	case h < 3:
		rps = 200
	case h < 7:
		rps = 300 + (h-3)/4*900 // 06:00 → 10:00 yerel: 300 → 1200
	case h < 15:
		rps = 1300
	default:
		rps = 600
	}
	return rps * mult
}

// gwScenario — kova → (5xx oranı %, toplam istek/sn). 4xx toplam trafiğe
// dahil ama hata DEĞİL (operatör: 4xx = iş hatası). incident: vaka günü 5xx
// rampası; trafficMult: vaka günü trafik çarpanı; flatRatio > 0: vaka günü
// 5xx oranı sabit bu yüzde (hız trafikle orantılı artar).
type gwScenario struct {
	baseRatio   float64 // geçmiş günlerin 5xx oranı (%)
	incident    bool
	trafficMult float64
	flatRatio   float64
	lowVolume   float64 // >0: trafiği bu çarpanla küçült (düşük hacimli servis)
}

func (sc gwScenario) at(b int64) spanRow {
	t := time.Unix(b, 0).UTC()
	mult := 1.0
	if sc.lowVolume > 0 {
		mult = sc.lowVolume
	}
	today := !t.Before(incidentDay)
	if today && sc.trafficMult > 0 {
		mult *= sc.trafficMult
	}
	total := gwTraffic(t, mult)
	errRPS := total * sc.baseRatio / 100 * noise(b)
	if today && sc.flatRatio > 0 {
		errRPS = total * sc.flatRatio / 100 * noise(b)
	}
	if today && sc.incident {
		h := t.Sub(incidentDay).Hours()
		switch {
		case h >= 3 && h < 7:
			errRPS = (0.5 + (h-3)/4*5.5) * mult // 0.5 → 6 /sn
		case h >= 7 && h < 8.75:
			errRPS = (6 + (h-7)/1.75*2) * mult // 6 → 8 /sn
		}
		if h >= 6.66 && h < 6.75 {
			errRPS *= 2 // 09:40 yerel kısa sıçrama
		}
		if h >= 8.75 {
			errRPS = total * sc.baseRatio / 100 * noise(b) // 11:45 müdahale
		}
	}
	return spanRow{v: errRPS / total * 100, rate: total}
}

func (sc gwScenario) rows(upTo time.Time) map[int64]spanRow {
	out := map[int64]spanRow{}
	for b := upTo.Add(-16 * 24 * time.Hour).Unix(); b < upTo.Unix(); b += bucketSeconds {
		b -= b % bucketSeconds
		out[b] = sc.at(b)
	}
	return out
}

// verdictAt — dedektörün `now` anındaki kararı (span ikiziyle aynı kova kümeleri).
func verdictAt(rows map[int64]spanRow, now time.Time, hasOpen bool, sens chstore.AnomalySensitivityConfig) anomalyOutcome {
	vals, rates := spanConsecutiveAt(rows, now, sens.DwellBuckets)
	seas := spanSeasonalAt(rows, now, seasonalDays, seasonalNeighborBuckets, sens.Calendar)
	return evaluateAnomaly(errorRampMetric, vals, seas, rates, seasonalMinSamples, hasOpen, batchLatSeries{}, sens)
}

// simulate — [from, to] boyunca 5 dk tik; açılış sayısı + ilk açılış + son durum.
func simulate(rows map[int64]spanRow, from, to time.Time, sens chstore.AnomalySensitivityConfig) (opens int, first time.Time, by string, open bool) {
	for now := from; !now.After(to); now = now.Add(5 * time.Minute) {
		oc := verdictAt(rows, now.Add(explainTickOffset), open, sens)
		switch oc.Action {
		case "open":
			if !open {
				opens++
				if first.IsZero() {
					first, by = now, oc.OpenedBy
				}
			}
			open = true
		case "resolve":
			open = false
		}
	}
	return opens, first, by, open
}

// TestBFFSlowRampOperatorCase — operatörün vakası: z kolu %1 mutlak taban
// yüzünden AÇAMAZ (8 hata/sn / 1300 istek/sn ≈ %0.6), sürekli yükseliş kolu
// açar; tek Problem; müdahaleden sonra kapanır.
func TestBFFSlowRampOperatorCase(t *testing.T) {
	sens := chstore.DefaultAnomalySensitivity()
	sc := gwScenario{baseRatio: 0.05, incident: true}
	rows := sc.rows(incidentDay.Add(24 * time.Hour))

	// 10:00 yerel (07:00 UTC): oran ≈ 6/1200 = %0.5.
	oc := verdictAt(rows, incidentDay.Add(7*time.Hour+explainTickOffset), false, sens)
	if oc.ZBlocker != zBlockAbsFloor {
		t.Errorf("z kolunu %q durdurmalı (kök neden), oldu %q (cur %.3f%%)", zBlockAbsFloor, oc.ZBlocker, oc.Current)
	}
	if oc.Action != "open" || oc.OpenedBy != "error_ramp" {
		t.Fatalf("sürekli yükseliş açmalıydı: %+v", oc)
	}

	// Kuralsız (eski davranış) bütün pencere sessiz — kaçırma koddan üretilebiliyor.
	off := sens
	off.ErrorRamp.Enabled = boolPtrT(false)
	if n, _, _, _ := simulate(rows, incidentDay.Add(3*time.Hour), incidentDay.Add(9*time.Hour), off); n != 0 {
		t.Fatalf("kural kapalıyken %d açılış — senaryo vakayı (açılmadı) üretmiyor", n)
	}

	n, first, by, openAtEnd := simulate(rows, incidentDay.Add(3*time.Hour), incidentDay.Add(10*time.Hour), sens)
	if n != 1 {
		t.Errorf("tek Problem beklendi (dedup / histerezis), %d açılış", n)
	}
	if by != "error_ramp" {
		t.Errorf("ilk açılış kolu %q", by)
	}
	if first.IsZero() || first.After(incidentDay.Add(6*time.Hour)) {
		t.Errorf("rampa 09:00 yerelden önce yakalanmalı, ilk açılış %v", first)
	}
	if openAtEnd {
		t.Error("11:45 müdahaleden sonra kapanmalıydı")
	}
}

// TestErrorRampTable — tasarım kararları.
func TestErrorRampTable(t *testing.T) {
	sens := chstore.DefaultAnomalySensitivity()
	end := incidentDay.Add(24 * time.Hour)
	from, to := incidentDay.Add(3*time.Hour), incidentDay.Add(10*time.Hour)
	cases := []struct {
		name  string
		sc    gwScenario
		opens bool
	}{
		{"yavaş rampa (BFF vakası) → açar", gwScenario{baseRatio: 0.05, incident: true}, true},
		{"olağan gün içi artış (oran sabit, hız trafikle artar) → açmaz", gwScenario{baseRatio: 0.05}, false},
		// Tasarım: oran düz, trafik 3× → hata hızı 3× (≥ 1/sn). Yük, teknik arıza değil.
		{"oran düz, hız yüksek (trafik 3×) → açmaz (tasarım)", gwScenario{baseRatio: 0.3, trafficMult: 3}, false},
		// Oran tabanın 2× altı (%0.3 → %0.5): ani-sapmanın tabanı da geçmez.
		{"oran 1.67× → açmaz", gwScenario{baseRatio: 0.3, flatRatio: 0.5}, false},
		// Oran 4× ama düşük hacim: hata hızı < 1/sn.
		{"düşük hacim, oran 4× → açmaz (hata hızı tabanı)", gwScenario{baseRatio: 0.05, flatRatio: 0.2, lowVolume: 0.1}, false},
	}
	for _, c := range cases {
		n, _, _, _ := simulate(c.sc.rows(end), from, to, sens)
		if (n > 0) != c.opens {
			t.Errorf("%s: %d açılış", c.name, n)
		}
	}
}

// TestErrorRampCheckUnit — saf çekirdek kenarları.
func TestErrorRampCheckUnit(t *testing.T) {
	cfg := chstore.DefaultErrorRamp()
	base := make([]float64, 30)
	for i := range base {
		base[i] = 0.1
	}
	mk := func(n int, v, rate float64) ([]float64, []float64) {
		vs, rs := make([]float64, n), make([]float64, n)
		for i := range vs {
			vs[i], rs[i] = v, rate
		}
		return vs, rs
	}
	// Pencere tamamı ihlalde.
	v, r := mk(6, 0.5, 1000)
	if got := errorRampCheck(v, r, base, cfg); !got.Holds || !got.Latest || got.Threshold != 0.2 {
		t.Errorf("ihlal bekleniyordu: %+v", got)
	}
	// Kısa sıçrama: son 2 kova.
	v, r = mk(6, 0.1, 1000)
	v[4], v[5] = 0.9, 0.9
	if got := errorRampCheck(v, r, base, cfg); got.Holds || !got.Latest || got.Breaching != 2 {
		t.Errorf("kısa sıçrama açmamalı ama son kova ihlalde: %+v", got)
	}
	// Hata hızı tabanı.
	v, r = mk(6, 0.5, 100) // 0.5 hata/sn
	if got := errorRampCheck(v, r, base, cfg); got.Holds || got.Blocker != rampBlockRate {
		t.Errorf("hız tabanı durdurmalı: %+v", got)
	}
	// p90 korkuluğu: sıçramalı taban.
	spiky := append([]float64(nil), base...)
	for i := 0; i < len(spiky); i += 3 {
		spiky[i] = 0.6
	}
	v, r = mk(6, 0.5, 1000)
	if got := errorRampCheck(v, r, spiky, cfg); got.Holds || got.Blocker != rampBlockRatio {
		t.Errorf("p90 korkuluğu (0.6) 0.5'i durdurmalı: %+v", got)
	}
	// Kapalı / hizasız / kısa taban.
	off := cfg
	off.Enabled = boolPtrT(false)
	if got := errorRampCheck(v, r, base, off); got.Evaluated || got.Blocker != rampBlockOff {
		t.Errorf("kapalı: %+v", got)
	}
	if got := errorRampCheck(v, r[:3], base, cfg); got.Blocker != rampBlockUnaligned {
		t.Errorf("hizasız: %+v", got)
	}
	if got := errorRampCheck(v, r, base[:3], cfg); got.Blocker != rampBlockBaseline {
		t.Errorf("kısa taban: %+v", got)
	}
}

// TestErrorRampHysteresis — açık satır, oran hâlâ eşiğin üstündeyken z bandına
// dönse de kapanmaz; oran düşünce kapanır.
func TestErrorRampHysteresis(t *testing.T) {
	sens := chstore.DefaultAnomalySensitivity()
	sc := gwScenario{baseRatio: 0.05, incident: true}
	rows := sc.rows(incidentDay.Add(24 * time.Hour))
	for now := incidentDay.Add(6 * time.Hour); now.Before(incidentDay.Add(8*time.Hour + 30*time.Minute)); now = now.Add(5 * time.Minute) {
		if oc := verdictAt(rows, now.Add(explainTickOffset), true, sens); oc.Action == "resolve" {
			t.Fatalf("%v: rampa sürerken açık satır kapandı: %+v", now, oc)
		}
	}
	if oc := verdictAt(rows, incidentDay.Add(9*time.Hour+explainTickOffset), true, sens); oc.Action != "resolve" {
		t.Errorf("müdahaleden sonra kapanmalı: %+v", oc)
	}
}

// TestDecideBlockerMatchesDecideAnomaly — ikiz sözleşme.
func TestDecideBlockerMatchesDecideAnomaly(t *testing.T) {
	sens := chstore.DefaultAnomalySensitivity()
	for _, m := range []string{"error_rate", "p99_ms", "request_rate"} {
		pol := policyFor(m, sens)
		for _, z := range []float64{-20, -7, -4, -1, 0, 1, 4, 6.5, 20} {
			for _, cur := range []float64{0, 0.5, 2, 15, 200} {
				for _, med := range []float64{0, 1, 10} {
					open := decideAnomaly(m, z, cur, med, pol, sens.CriticalZ).open
					b := decideBlocker(m, z, cur, med, pol, sens.CriticalZ)
					if open != (b == "") {
						t.Fatalf("%s z=%v cur=%v med=%v: open=%v blocker=%q", m, z, cur, med, open, b)
					}
				}
			}
		}
	}
}

// TestTechnicalErrorIs5xxOnly — operatör: 5xx = teknik hata, 4xx = iş hatası.
// VM hata kümesi pinli; oran paydası tüm istekler.
func TestTechnicalErrorIs5xxOnly(t *testing.T) {
	if vmREDFamilies[0].ServerErr != "5.." {
		t.Fatalf("HTTP teknik hata kümesi 5xx olmalı: %q", vmREDFamilies[0].ServerErr)
	}
	sc := vmQueryScope{SvcLabel: "service_name", StatusLabels: []string{"http_response_status_code"}}
	q, _ := vmREDExpr(vmREDFamilies[0], vmKindErr, sc, 0)
	if !strings.Contains(q, `http_response_status_code=~"5.."`) || strings.Contains(q, "4..") {
		t.Errorf("hata ifadesi yalnız 5xx: %s", q)
	}
	// Boş http_route: sorgular route'a göre ne süzer ne gruplar — servis düzeyi toplam.
	for k := vmREDKind(0); k < vmKindN; k++ {
		e, _ := vmREDExpr(vmREDFamilies[0], k, sc, 0)
		if strings.Contains(e, "http_route") || !strings.Contains(e, "sum by (service_name") {
			t.Errorf("%s: route süzgeci/gruplaması olmamalı (boş route serisi düşmesin): %s", k, e)
		}
	}
}
