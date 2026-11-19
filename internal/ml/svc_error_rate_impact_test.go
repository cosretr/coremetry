package ml

// svc_error_rate_impact_test.go — v0.10.2373 (etki olayı programı, dilim I3;
// operatör kararı 2026-11-19). SÖZLEŞME: servis hata oranı 1 DAKİKALIK
// örneklerle, etki kuralıyla (son 5 örneğin en az 3'ü eşikte ve en son ölçülen
// de eşikte; eşik = max(%1, taban × 1,5, taban + 1 puan); dakikada en az 10
// istek) karar verir. Açık olay, pencerede ihlal kaldıkça uzar ve günlük
// tavanı harcamaz; başlangıcı (ilk ihlal dakikası) ve kimliği tikler boyunca
// sabittir. Testler saat ve saat dilimi bağımsız: `now` hep açık verilir.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func impS(ratio, load float64) impactSample { return impactSample{Value: ratio, Load: load, OK: true} }

var impGap = impactSample{}

func TestSvcImpactQueries(t *testing.T) {
	pin := anomaly.REDFamilyPins()[0]
	c, e := svcImpactQueries(pin, "service_name", "http_response_status_code")
	for _, q := range []string{c, e} {
		if !strings.HasPrefix(q, "sum by (service_name) (increase(") || !strings.Contains(q, "_count") || !strings.Contains(q, "[60s]") ||
			strings.Contains(q, "topk") || strings.Contains(q, "http_route") || strings.Contains(q, "_bucket") {
			t.Fatalf("sorgu: %s", q)
		}
	}
	if strings.Contains(c, "status_code") || !strings.Contains(e, `http_response_status_code=~"5.."`) {
		t.Fatalf("count / err: %s | %s", c, e)
	}
}

// Pencere sonu = ingest payını geçmiş son TAM dakikanın bitişi; saat dilimi
// sonucu değiştirmez.
func TestSvcImpactEnd(t *testing.T) {
	at := func(h, m, s int) time.Time { return time.Date(2026, 11, 19, h, m, s, 0, time.UTC) }
	cases := []struct {
		name string
		now  time.Time
		lag  int
		want time.Time
	}{
		{"dakika ortası", at(12, 7, 45), 120, at(12, 5, 0)},
		{"tam dakika", at(12, 7, 0), 120, at(12, 5, 0)},
		{"pay yok", at(12, 7, 59), 0, at(12, 7, 0)},
		{"saat dönümü", at(13, 1, 10), 120, at(12, 59, 0)},
	}
	east := time.FixedZone("east", 3*3600+1800)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, now := range []time.Time{c.now, c.now.In(east)} {
				if got := svcImpactEnd(now, c.lag); !got.Equal(c.want) {
					t.Fatalf("end=%s want %s", got.UTC(), c.want)
				}
			}
		})
	}
}

// minuteRows — VM aralık yanıtı biçiminde satır: nokta zamanı dakikanın SONU
// ((t−60 sn, t] artışı). vals: dakika başı (unix sn) → değer.
func minuteRows(label, svc string, vals map[int64]float64) []chstore.SpanMetricSeries {
	r := chstore.SpanMetricSeries{GroupKey: []string{fmt.Sprintf("%s=%q", label, svc)}}
	for m, v := range vals {
		r.Points = append(r.Points, chstore.SpanMetricPoint{Time: (m + svcImpactStepSec) * 1e9, Value: v})
	}
	return []chstore.SpanMetricSeries{r}
}

// Dakika ızgarası + "ilk aile kazanır" + ölçülmemiş dakika kuralları.
func TestSvcImpactSamples(t *testing.T) {
	end := time.Date(2026, 11, 19, 12, 5, 0, 0, time.UTC).Unix()
	m := func(i int) int64 { return end - int64(impactWindow-i)*svcImpactStepSec } // i. örneğin dakika başı
	a := newSvcAgg()
	// HTTP (0): dakika 0 ve 1 dolu, 2'de istek YOK (0), 3 boşluk, 4 dolu; pencere dışı iki nokta
	a.addMinutes(0, "service_name", "count", minuteRows("service_name", "orders", map[int64]float64{m(0): 200, m(1): 100, m(2): 0, m(4): 50, m(5): 999, m(-1): 999}), end)
	a.addMinutes(0, "service_name", "err", minuteRows("service_name", "orders", map[int64]float64{m(0): 10, m(4): 5}), end)
	a.errRan(0)
	// gRPC (1): aynı servis her dakika dolu — yalnız HTTP'nin sustuğu dakikada (2, 3) kullanılır
	a.addMinutes(1, "service_name", "count", minuteRows("service_name", "orders", map[int64]float64{m(0): 1000, m(1): 1000, m(2): 40, m(3): 40, m(4): 1000}), end)
	a.addMinutes(1, "service_name", "err", minuteRows("service_name", "orders", map[int64]float64{m(2): 4, m(3): 2}), end)
	a.errRan(1)
	// err sorgusu koşmamış aile (2): oran bilinmez → örnek ölçülmemiş
	a.addMinutes(2, "service_name", "count", minuteRows("service_name", "checkout", map[int64]float64{m(3): 80, m(4): 80}), end)
	minute := map[string]map[int64]float64{}
	fam := a.emit(minute)
	got := svcImpactSamples(minute, "orders", end)
	want := []impactSample{impS(0.05, 200), impS(0, 100), impS(0.1, 40), impS(0.05, 40), impS(0.1, 50)}
	if len(got) != impactWindow {
		t.Fatalf("örnek sayısı %d", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("örnek %d = %+v want %+v", i, got[i], want[i])
		}
	}
	if fam["orders"] != 0 {
		t.Errorf("en yeni dakikanın ailesi HTTP olmalı: %v", fam)
	}
	for i, s := range svcImpactSamples(minute, "checkout", end) {
		if s.OK {
			t.Errorf("err sorgusu koşmayan ailede örnek %d ölçülmüş sayılmamalı: %+v", i, s)
		}
	}
	for i, s := range svcImpactSamples(minute, "yok", end) {
		if s.OK {
			t.Errorf("verisi olmayan serviste örnek %d ölçülmüş: %+v", i, s)
		}
	}
}

func TestSvcImpactDecide(t *testing.T) {
	base := repeat(0.004, 30) // %0,4 → eşik %1,4
	rate := repeat(10, 30)
	const L = 600 // istek/dk
	cases := []struct {
		name      string
		samples   []impactSample
		baseRatio []float64
		open      bool
		fire      bool
		emit      bool
		threshold float64
		reason    string
	}{
		{"5'te 3 ve en yeni ihlal → ateşler", []impactSample{impS(0.004, L), impS(0.004, L), impS(0.03, L), impS(0.03, L), impS(0.03, L)}, base, false, true, true, 0.014, ""},
		{"2 ihlal → açmaz", []impactSample{impS(0.004, L), impS(0.004, L), impS(0.004, L), impS(0.03, L), impS(0.03, L)}, base, false, false, false, 0.014, "ihlal 2/5"},
		{"3 ihlal ama en yeni temiz → açmaz", []impactSample{impS(0.03, L), impS(0.03, L), impS(0.03, L), impS(0.004, L), impS(0.004, L)}, base, false, false, false, 0.014, "düzeliyor"},
		{"göreli aşıldı, mutlak aşılmadı (%1,2 < %1,4) → açmaz", repeatS(impS(0.012, L)), base, false, false, false, 0.014, "pencere temiz"},
		{"taban 0: eşik %1", repeatS(impS(0.01, L)), repeat(0, 30), false, true, true, 0.01, ""},
		{"taban 0: %0,9 → açmaz", repeatS(impS(0.009, L)), repeat(0, 30), false, false, false, 0.01, "pencere temiz"},
		{"taban %5: eşik %7,5 (%7 açmaz)", repeatS(impS(0.07, L)), repeat(0.05, 30), false, false, false, 0.075, "pencere temiz"},
		{"taban %5: %8 açar", repeatS(impS(0.08, L)), repeat(0.05, 30), false, true, true, 0.075, ""},
		{"yük kapısı: dakikada 9 istek → açmaz", repeatS(impS(0.5, 9)), base, false, false, false, 0.014, "yük az"},
		{"taban örneği az → sessiz, gerekçeli", repeatS(impS(0.5, L)), base[:19], false, false, false, 0, "taban örneği az"},
		{"taban örneği az → açık olay da uzamaz", repeatS(impS(0.5, L)), base[:19], true, false, false, 0, "taban örneği az"},
		{"ölçüm yok → sessiz", repeatS(impGap), base, false, false, false, 0.014, "ölçüm yok"},
		{"boşluk ihlal de temiz de değil: 3 ihlal + 2 boşluk, en yeni ölçülen ihlal → ateşler", []impactSample{impS(0.03, L), impGap, impS(0.03, L), impS(0.03, L), impGap}, base, false, true, true, 0.014, ""},
		{"açık + düzeliyor (3 ihlal, en yeni temiz) → uzar", []impactSample{impS(0.03, L), impS(0.03, L), impS(0.03, L), impS(0.004, L), impS(0.004, L)}, base, true, false, true, 0.014, "düzeliyor"},
		{"açık + tek ihlal kaldı → uzar", []impactSample{impS(0.03, L), impS(0.004, L), impS(0.004, L), impS(0.004, L), impS(0.004, L)}, base, true, false, true, 0.014, "ihlal 1/5"},
		{"açık + pencere temiz → uzamaz", repeatS(impS(0.004, L)), base, true, false, false, 0.014, "pencere temiz"},
		{"açık + ihlal yok, pencere eksik → uzamaz (kapanış koşu sayacında)", []impactSample{impS(0.004, L), impGap, impGap, impS(0.004, L), impS(0.004, L)}, base, true, false, false, 0.014, "ihlal 0/5"},
		{"açık + yalnız az yüklü yüksek oran → uzamaz", repeatS(impS(0.5, 3)), base, true, false, false, 0.014, "yük az"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := svcImpactDecide(c.samples, c.baseRatio, rate, 20, c.open)
			if v.Fire != c.fire || v.Emit != c.emit {
				t.Fatalf("fire=%v emit=%v want %v/%v (%s)", v.Fire, v.Emit, c.fire, c.emit, v.Reason)
			}
			if diff := v.Threshold - c.threshold; diff > 1e-12 || diff < -1e-12 {
				t.Fatalf("eşik %v want %v", v.Threshold, c.threshold)
			}
			if !strings.Contains(v.Reason, c.reason) {
				t.Fatalf("gerekçe %q, %q içermeli", v.Reason, c.reason)
			}
			if v.Emit && v.Onset < 0 {
				t.Fatal("yazılan olayın onset'i olmalı")
			}
		})
	}
	if v := svcImpactDecide(repeatS(impS(0.03, L)), base, rate, 20, false); v.Median != 0.004 || v.BaseRate != 10 || v.Observed != 0.03 || v.Z <= 0 {
		t.Fatalf("taban alanları: %+v", v)
	}
}

func repeatS(s impactSample) []impactSample {
	out := make([]impactSample, impactWindow)
	for i := range out {
		out[i] = s
	}
	return out
}

// Olay alanları: gözlenen = en yeni ölçülen dakika, taban = medyan, başlangıç
// = ilk ihlal dakikasının başı (açık koşunun yazılmış başlangıcı önceliklidir),
// kanıt kararı açıklar ve kova sürekliliği iddia etmez.
func TestSvcImpactEventFields(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	end := time.Date(2026, 11, 19, 12, 5, 0, 0, time.UTC)
	samples := []impactSample{impS(0.004, 600), impGap, impS(0.03, 610), impS(0.04, 620), impS(0.035, 630)}
	v := svcImpactDecide(samples, repeat(0.004, 30), repeat(10, 30), 20, false)
	ev := svcImpactEvent("orders", samples, v, end, 0, cfg, "weekday")
	onset := end.Add(-3 * time.Minute)
	if ev.ID != chstore.MLEventID(detSvcErrorRate, "orders", "") || ev.Detector != detSvcErrorRate || ev.Signal != chstore.MLSignalTrace ||
		ev.Service != "orders" || ev.Code != "" || ev.Direction != "up" || ev.Stage != chstore.MLStageShadow {
		t.Fatalf("kimlik: %+v", ev)
	}
	if ev.StartedAt != onset.UnixNano() || ev.LastSeen != end.UnixNano() || ev.WindowSec != uint32(cfg.BucketSec) {
		t.Fatalf("zaman: started=%d last=%d window=%d", ev.StartedAt, ev.LastSeen, ev.WindowSec)
	}
	if ev.Observed != 0.035 || ev.Baseline != 0.004 || ev.Score < cfg.CriticalZ {
		t.Fatalf("değer: %+v", ev)
	}
	var m struct {
		Rule       string     `json:"rule"`
		Measure    string     `json:"measure"`
		Threshold  float64    `json:"threshold"`
		Violations int        `json:"violations"`
		Window     int        `json:"window"`
		MinViol    int        `json:"minViolations"`
		MinLoad    float64    `json:"minLoad"`
		SampleSec  int        `json:"sampleSec"`
		MinuteFrom int64      `json:"minuteFrom"`
		Ratios     []*float64 `json:"ratios"`
		Loads      []float64  `json:"loads"`
		BaseRate   *float64   `json:"baseRate"`
		Fired      bool       `json:"fired"`
		Dwell      *int       `json:"dwell"`
	}
	if err := json.Unmarshal([]byte(ev.Evidence), &m); err != nil {
		t.Fatal(err)
	}
	if m.Rule != "impact" || m.Measure != svcMeasureRatio || m.Violations != 3 || m.Window != impactWindow || m.MinViol != impactViolations ||
		m.MinLoad != 10 || m.SampleSec != 60 || m.MinuteFrom != end.Add(-5*time.Minute).Unix() || !m.Fired || m.BaseRate == nil || *m.BaseRate != 10 {
		t.Fatalf("kanıt: %s", ev.Evidence)
	}
	if d := m.Threshold - 0.014; d > 1e-12 || d < -1e-12 {
		t.Fatalf("eşik %v", m.Threshold)
	}
	if len(m.Ratios) != 5 || m.Ratios[1] != nil || m.Ratios[4] == nil || *m.Ratios[4] != 0.035 || len(m.Loads) != 5 || m.Loads[2] != 610 || m.Loads[1] != 0 {
		t.Fatalf("dakika dizileri: %s", ev.Evidence)
	}
	if m.Dwell != nil {
		t.Fatal("etki olayı kova sürekliliği (dwell) iddia etmemeli")
	}
	// açık koşunun yazılmış başlangıcı daha erkense o kalır; daha geçse ilk ihlal
	early := onset.Add(-7 * time.Minute).UnixNano()
	if got := svcImpactEvent("orders", samples, v, end, early, cfg, "weekday").StartedAt; got != early {
		t.Fatalf("açık koşunun başlangıcı korunmalı: %d", got)
	}
	if got := svcImpactEvent("orders", samples, v, end, end.UnixNano(), cfg, "weekday").StartedAt; got != onset.UnixNano() {
		t.Fatalf("başlangıç ilk ihlalden geç olamaz: %d", got)
	}
}

// ── kayan pencere simülasyonu ────────────────────────────────────────────

// impSim — dakika dakika veri üreten sahte kaynak + gerçek koşu izleyicisi:
// her tik VM satırı → toplayıcı → karar → kalite süzgeci → kapanış.
type impSim struct {
	t     *testing.T
	d     *TraceRED
	tr    *tracker
	cfg   chstore.MLConfig
	t0    time.Time
	base  map[string][]float64
	ratio map[string]func(min int) (count, errs float64, ok bool)
}

func newImpSim(t *testing.T, tickSec int) *impSim {
	cfg := chstore.DefaultMLConfig()
	cfg.TickSec, cfg.IngestLagSec = tickSec, 120
	return &impSim{t: t, d: NewTraceRED(), tr: newTracker(), cfg: cfg, t0: time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC),
		base: map[string][]float64{}, ratio: map[string]func(int) (float64, float64, bool){}}
}

func (s *impSim) service(svc string, baseRatio float64, f func(min int) (count, errs float64, ok bool)) {
	s.base[chstore.MLSeriesKey(svc, tracePart(svcRoute, svcMeasureRatio))] = repeat(baseRatio, 30)
	s.base[chstore.MLSeriesKey(svc, tracePart(svcRoute, svcMeasureCount))] = repeat(10, 30)
	s.ratio[svc] = f
}

// tick — t0 + k dk + 30 sn anındaki tik. Döner: yazılan olaylar, kapanışlar.
func (s *impSim) tick(k int) (kept, closing []chstore.MLEvent) {
	now := s.t0.Add(time.Duration(k)*time.Minute + 30*time.Second)
	tick := time.Duration(s.cfg.TickSec) * time.Second
	end := svcImpactEnd(now, s.cfg.IngestLagSec)
	agg := newSvcAgg()
	for svc, f := range s.ratio {
		cnt, errs := map[int64]float64{}, map[int64]float64{}
		for i := 0; i < impactWindow; i++ {
			m := end.Unix() - int64(impactWindow-i)*svcImpactStepSec
			if c, e, ok := f(int((m - s.t0.Unix()) / 60)); ok {
				cnt[m] = c
				if e > 0 {
					errs[m] = e
				}
			}
		}
		agg.addMinutes(0, "service_name", "count", minuteRows("service_name", svc, cnt), end.Unix())
		agg.addMinutes(0, "service_name", "err", minuteRows("service_name", svc, errs), end.Unix())
	}
	agg.errRan(0)
	minute := map[string]map[int64]float64{}
	agg.emit(minute)
	deps := Deps{openRun: func(id string) bool { return s.tr.openRun(id, now, tick) },
		runStart: func(id string) int64 { return s.tr.startedOf(id, now, tick) }}
	evs := s.d.svcImpactEvents(deps, minute, s.base, end, s.cfg, "weekday", now)
	kept, _ = qualityFilter(evs, s.cfg, s.tr, now)
	closing, _ = s.tr.closeStale(now, tickCoverage{})
	return kept, closing
}

// incident — [from, to) dakikalarında hata oranı high, dışında low; 600 istek/dk.
func incident(from, to int, low, high float64) func(int) (float64, float64, bool) {
	return func(m int) (float64, float64, bool) {
		if m >= from && m < to {
			return 600, 600 * high, true
		}
		return 600, 600 * low, true
	}
}

// Dakikalık tik (hızlı tik kipi): başlangıç → ateşleme → uzatma → kapanış.
// Hata 10:10'da başlar, 10:20'de biter. İlk olay, üçüncü ihlal dakikası
// (10:12) ingest payını geçince: 10:15:30 tiki.
func TestSvcImpactSlidingWindowMinuteTicks(t *testing.T) {
	s := newImpSim(t, 60)
	s.cfg.DailyEventCap = 1 // uzatmalar tavan harcamaz: tek hak yeter
	s.service("orders", 0.004, incident(10, 20, 0.004, 0.05))
	s.service("checkout", 0.004, incident(0, 0, 0.004, 0)) // hep sağlıklı
	onset := s.t0.Add(10 * time.Minute).UnixNano()
	var id string
	closed := 0
	for k := 5; k <= 40; k++ {
		kept, closing := s.tick(k)
		for _, e := range kept {
			if e.Service != "orders" {
				t.Fatalf("k=%d sağlıklı servis olay üretti: %+v", k, e)
			}
		}
		switch {
		case k < 15: // 3 ihlal dakikası henüz görünmüyor
			if len(kept) != 0 {
				t.Fatalf("k=%d erken olay: %+v", k, kept)
			}
		case k <= 26: // 15..22 ateşler (en yeni dakika ihlal), 23..26 uzar (pencerede ihlal var)
			if len(kept) != 1 {
				t.Fatalf("k=%d olay bekleniyordu, %d geldi", k, len(kept))
			}
			e := kept[0]
			if id == "" {
				id = e.ID
			}
			if e.ID != id || e.StartedAt != onset {
				t.Fatalf("k=%d kimlik/başlangıç oynadı: id=%s started=%d (want %s / %d)", k, e.ID, e.StartedAt, id, onset)
			}
			m := evidenceMap(e.Evidence)
			fired, _ := m["fired"].(bool)
			wantObs := 0.05
			if k > 22 {
				wantObs = 0.004
			}
			if fired != (k <= 22) || e.Observed != wantObs || e.Baseline != 0.004 {
				t.Fatalf("k=%d fired=%v observed=%v", k, fired, e.Observed)
			}
			if wantLast := s.t0.Add(time.Duration(k-2) * time.Minute).UnixNano(); e.LastSeen != wantLast {
				t.Fatalf("k=%d last_seen=%d want %d", k, e.LastSeen, wantLast)
			}
		default: // 27+: pencere temiz → olay yazılmaz
			if len(kept) != 0 {
				t.Fatalf("k=%d temiz pencerede olay: %+v", k, kept)
			}
		}
		for _, c := range closing {
			closed++
			if k != 29 || c.ID != id || c.Stage != chstore.MLStageClosed || c.StartedAt != onset {
				t.Fatalf("k=%d kapanış: %+v", k, c)
			}
		}
	}
	if closed != 1 {
		t.Fatalf("tek kapanış bekleniyordu: %d", closed)
	}
}

// Bugünkü 5 dakikalık tik: pencere tikten tike kayar (örtüşmez) ama başlangıç
// ilk ihlal dakikasında kalır; dalgalı hata (pencerede tek ihlal) olayı
// kapatıp yeniden açmaz.
func TestSvcImpactSlidingWindowFiveMinuteTicks(t *testing.T) {
	s := newImpSim(t, 300)
	s.service("orders", 0.004, func(m int) (float64, float64, bool) {
		switch {
		case m >= 10 && m < 18: // sürekli hata
			return 600, 30, true
		case m >= 18 && m < 33 && m%4 == 0: // dalgalı: 4 dakikada bir
			return 600, 30, true
		case m == 21: // scrape boşluğu
			return 0, 0, false
		}
		return 600, 2.4, true
	})
	onset := s.t0.Add(10 * time.Minute).UnixNano()
	var id string
	for k := 5; k <= 60; k += 5 {
		kept, closing := s.tick(k)
		switch {
		case k < 15:
			if len(kept) != 0 {
				t.Fatalf("k=%d erken olay", k)
			}
		case k <= 35: // 15, 20 ateşler; 25, 30, 35 pencerede en az bir ihlal → uzar
			if len(kept) != 1 {
				t.Fatalf("k=%d olay bekleniyordu, %d geldi", k, len(kept))
			}
			if id == "" {
				id = kept[0].ID
			}
			if kept[0].ID != id || kept[0].StartedAt != onset {
				t.Fatalf("k=%d kimlik/başlangıç oynadı: %+v", k, kept[0])
			}
		default:
			if len(kept) != 0 {
				t.Fatalf("k=%d temiz pencerede olay", k)
			}
		}
		if len(closing) > 0 && k != 50 {
			t.Fatalf("k=%d beklenmeyen kapanış (3 kaçırılan tik: 40, 45, 50)", k)
		}
	}
}

// Günlük tavan yalnız YENİ olayı sayar; tavan doluyken açık olay uzar.
func TestSvcImpactDayCap(t *testing.T) {
	s := newImpSim(t, 60)
	s.cfg.DailyEventCap = 1
	s.service("orders", 0.004, incident(10, 40, 0.004, 0.05))
	s.service("checkout", 0.004, incident(20, 40, 0.004, 0.05))
	seen := map[string]int{}
	for k := 5; k <= 35; k++ {
		kept, _ := s.tick(k)
		for _, e := range kept {
			seen[e.Service]++
		}
	}
	if seen["orders"] != 21 || seen["checkout"] != 0 {
		t.Fatalf("tavan: orders 15..35 arası her tik (21), checkout hiç: %v", seen)
	}
}

// vmRange: vmInstant ile aynı aile backoff'u; beklemedeki aileye aralık
// sorgusu da gitmez ve atlama işaretlenir.
func TestVMRangeSharesFamilyBackoff(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	vm := &fakeVM{err: errors.New("HTTP 422 maxSamplesPerQuery")}
	deps := Deps{VM: vm, Backoff: newVMBackoff(), meter: newTickMeter()}
	if _, skipped, err := vmRange(context.Background(), deps, "trace:http", "q", now.Add(-4*time.Minute), now, 60, now); skipped || err == nil || len(vm.calls) != 1 {
		t.Fatalf("ilk aralık sorgusu VM'e gitmeli ve hata dönmeli: skipped=%v err=%v calls=%v", skipped, err, vm.calls)
	}
	if _, skipped, _ := vmInstant(context.Background(), deps, "trace:http", "q", now, now.Add(time.Minute)); !skipped || len(vm.calls) != 1 {
		t.Fatal("aralık sorgusunun beklemeye aldığı aileye anlık sorgu gitmemeli")
	}
	if _, skipped, _ := vmRange(context.Background(), deps, "trace:http", "q", now, now, 60, now.Add(time.Minute)); !skipped || len(vm.calls) != 1 || !deps.meter.wasSkipped() {
		t.Fatal("beklemede: aralık sorgusu gönderilmemeli, atlama işaretlenmeli")
	}
	vm.err = nil
	if _, skipped, err := vmRange(context.Background(), deps, "trace:http", "q", now, now, 60, now.Add(6*time.Minute)); skipped || err != nil || vm.calls[len(vm.calls)-1] != "range:q" {
		t.Fatalf("bekleme bitince yeniden dener: %v %v %v", skipped, err, vm.calls)
	}
}

// svcImpactFetch: aile başına 2 aralık sorgusu; err sorgusu düşerse aile yine
// dakikayı sahiplenir ama oran bilinmez (o ailenin servisleri sessiz).
func TestSvcImpactFetch(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 7, 30, 0, time.UTC)
	end := svcImpactEnd(now, 120)
	pin := anomaly.REDFamilyPins()[0]
	fl := traceFamilyLabels{svc: "service_name", route: "http_route", status: "http_response_status_code", ok: true}
	vm := &impVM{end: end.Unix()}
	deps := Deps{VM: vm, Backoff: newVMBackoff()}
	agg := newSvcAgg()
	if err := svcImpactFetch(context.Background(), deps, pin, fl, 0, agg, end, now); err != nil {
		t.Fatal(err)
	}
	if len(vm.queries) != 2 || vm.from != end.Add(-4*time.Minute).Unix() || vm.to != end.Unix() || vm.step != 60 {
		t.Fatalf("aile başına 2 sorgu, 5 nokta: %d sorgu from=%d to=%d step=%d", len(vm.queries), vm.from, vm.to, vm.step)
	}
	minute := map[string]map[int64]float64{}
	agg.emit(minute)
	for i, sm := range svcImpactSamples(minute, "orders", end.Unix()) {
		if !sm.OK || sm.Load != 600 || sm.Value != 0.05 {
			t.Fatalf("örnek %d: %+v", i, sm)
		}
	}
	// err sorgusu hata verir: hata döner, sayım eklenir, oran yok
	vm2 := &impVM{end: end.Unix(), failErr: true}
	agg2 := newSvcAgg()
	if err := svcImpactFetch(context.Background(), Deps{VM: vm2, Backoff: newVMBackoff()}, pin, fl, 0, agg2, end, now); err == nil {
		t.Fatal("err sorgusu hatası dönmeli")
	}
	minute2 := map[string]map[int64]float64{}
	agg2.emit(minute2)
	if len(minute2[chstore.MLSeriesKey("orders", tracePart(svcRoute, svcMeasureCount))]) != impactWindow {
		t.Fatal("sayım eklenmeli (aile dakikayı sahiplenir)")
	}
	for i, sm := range svcImpactSamples(minute2, "orders", end.Unix()) {
		if sm.OK {
			t.Fatalf("oran bilinmiyorken örnek %d ölçülmüş sayılmamalı", i)
		}
	}
}

// impVM — 1 dk'lık count/err aralık yanıtı veren sahte VM (600 istek, 30 hata).
type impVM struct {
	fakeVM
	end, from, to int64
	step          int
	queries       []string
	failErr       bool
}

func (f *impVM) QueryPromQLRange(_ context.Context, q string, from, to time.Time, step, _ int) ([]chstore.SpanMetricSeries, error) {
	f.queries, f.from, f.to, f.step = append(f.queries, q), from.Unix(), to.Unix(), step
	isErr := strings.Contains(q, "=~")
	if isErr && f.failErr {
		return nil, errors.New("connection refused")
	}
	vals := map[int64]float64{}
	for i := 0; i < impactWindow; i++ {
		v := 600.0
		if isErr {
			v = 30
		}
		vals[f.end-int64(impactWindow-i)*svcImpactStepSec] = v
	}
	return minuteRows("service_name", "orders", vals), nil
}

// Kartın teknik satırı kova sürekliliği ("Süreklilik 3 kova (15 dk)") iddia
// etmez; snapshot eşikleri dwellBuckets taşısa da etki kuralını söyler.
func TestSvcImpactCardThresholdLine(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	end := time.Date(2026, 11, 19, 12, 5, 0, 0, time.UTC)
	samples := repeatS(impS(0.03, 600))
	ev := svcImpactEvent("orders", samples, svcImpactDecide(samples, repeat(0.004, 30), repeat(10, 30), 20, false), end, 0, cfg, "weekday")
	in := CardInputFromEvent(ev, end.Add(3*time.Minute), time.UTC)
	if in.ImpactRule == nil || in.DwellBuckets != 0 {
		t.Fatalf("girdi: %+v dwell=%d", in.ImpactRule, in.DwellBuckets)
	}
	in.DwellBuckets, in.ImpactMinRel = 2, 0.2 // olay anı kaydının eşikleri (ApplySnapshot)
	got := BuildExplainCard(in).Technical.Thresholds
	if want := "Etki kuralı: son 5 dakikalık örnekte en az 3 ihlal · eşik %1,4 · dakikada en az 10 istek"; got != want {
		t.Fatalf("teknik satır:\n got %q\nwant %q", got, want)
	}
	if !strings.HasPrefix(ExplainSummary(in), "orders: servis hata oranı normalde %0,4, şimdi %3") {
		t.Fatalf("özet cümlesi değişmemeli: %s", ExplainSummary(in))
	}
}

// startedOf: süren (açık ya da yeniden açılabilir) epizodun yazılmış
// başlangıcı; yeni epizot açılacaksa 0.
func TestTrackerStartedOf(t *testing.T) {
	tr := newTracker()
	tick := time.Minute
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	base := chstore.MLEventID(detSvcErrorRate, "orders", "")
	if tr.startedOf(base, now, tick) != 0 {
		t.Fatal("koşu yokken 0")
	}
	started := now.Add(-3 * time.Minute).UnixNano()
	_, ep := tr.observe(chstore.MLEvent{ID: base, Detector: detSvcErrorRate, Service: "orders"}, now, tick)
	if tr.startedOf(base, now, tick) != 0 {
		t.Fatal("henüz yazılmamış koşuda 0")
	}
	tr.setEvent(chstore.MLEvent{ID: ep, Detector: detSvcErrorRate, Service: "orders", StartedAt: started})
	if got := tr.startedOf(base, now.Add(2*time.Minute), tick); got != started {
		t.Fatalf("açık koşu: %d", got)
	}
	for i := 1; i <= episodeCloseMisses; i++ {
		tr.closeStale(now.Add(time.Duration(i)*tick), tickCoverage{})
	}
	if got := tr.startedOf(base, now.Add(20*time.Minute), tick); got != started {
		t.Fatalf("kapanmış ama yeniden açılabilir koşu aynı epizottur: %d", got)
	}
	if got := tr.startedOf(base, now.Add(episodeReopenWindow+10*time.Minute), tick); got != 0 {
		t.Fatalf("yeniden açma penceresi geçince yeni epizot: %d", got)
	}
}

// Yalıtım: 1 dk'lık sorgunun ağır hatası KENDİ backoff ailesine yazılır;
// işlem düzeyi `trace:<aile>` sorguları beklemeye girmez. Hata notu yalnız
// svc_error_rate'i "ölçülemedi" gösterir ve başarıda silinir.
func TestSvcImpactFailureIsIsolated(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 7, 30, 0, time.UTC)
	end := svcImpactEnd(now, 120)
	pin := anomaly.REDFamilyPins()[0]
	fl := traceFamilyLabels{svc: "service_name", route: "http_route", status: "http_response_status_code", ok: true}
	bo := newVMBackoff()
	bo.fail(svcImpactBackoff+pin.ID, errors.New("timeout"), now) // 1 dk'lık sorgu ailesi beklemede
	if !bo.skipping(svcImpactBackoff+pin.ID, now.Add(time.Second)) || bo.skipping("trace:"+pin.ID, now.Add(time.Second)) {
		t.Fatal("etki sorgusu kendi ailesinde bekler, işlem düzeyi aileyi bekletmez")
	}
	vm := &impVM{end: end.Unix()}
	if err := svcImpactFetch(context.Background(), Deps{VM: vm, Backoff: bo, meter: newTickMeter()}, pin, fl, 0, newSvcAgg(), end, now.Add(time.Second)); err != nil || len(vm.queries) != 0 {
		t.Fatalf("beklemedeki etki ailesi VM'e gitmemeli: err=%v sorgu=%d", err, len(vm.queries))
	}
	d := NewTraceRED()
	if d.Unmeasured() != nil {
		t.Fatal("hata yokken ölçülemedi notu olmamalı")
	}
	d.setImpactNote(fmt.Errorf("vm http/svc_count_1m: timeout"))
	if u := d.Unmeasured(); len(u) != 1 || !strings.HasPrefix(u[detSvcErrorRate], "ölçülemedi: ") {
		t.Fatalf("yalnız svc_error_rate ölçülemedi görünmeli: %v", u)
	}
	d.setImpactNote(nil)
	if d.Unmeasured() != nil {
		t.Fatal("başarıda not silinmeli")
	}
}
