package ml

// impact_probe_test.go — v0.10.2377 (etki olayı programı, dilim I2).
// SÖZLEŞME: zamanlı tikler arasında lider dakikada bir UCUZ yoklama yapar
// (dedektörün 1 dk'lık servis sorgusu + son tikin taban özeti, CH okuması
// yok). Kural ateşliyor VE o servisin açık koşusu yoksa tek bir tam tik ERKEN
// koşar; zamanlı tiklerin saati kaymaz. Erken tik yoklamanın satırlarını
// yeniden kullanır (aynı pencere bitişine ikinci sorgu çifti yok), tik sayan
// hiçbir şeyi ilerletmez (lider tik sayacı, kaçırma sayacı, yaygın yavaşlama
// ardışıklığı, günlük tavan) ve aynı aday bekleme süresi dolmadan yeniden
// erken tik açmaz. Testler sahte saatle koşar: gerçek uyku yok, saat ve saat
// dilimi bağımsız.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// impProbeVM — dakika dakika veri üreten sahte VM; aralık sorgularını sayar.
type impProbeVM struct {
	fakeVM
	t0     time.Time
	series map[string]func(min int) (count, errs float64, ok bool)
	ranges int
	fail   error
}

func (f *impProbeVM) QueryPromQLRange(_ context.Context, q string, from, to time.Time, step, _ int) ([]chstore.SpanMetricSeries, error) {
	f.ranges++
	if f.fail != nil {
		return nil, f.fail
	}
	isErr := strings.Contains(q, "=~")
	svcs := make([]string, 0, len(f.series))
	for svc := range f.series {
		svcs = append(svcs, svc)
	}
	sort.Strings(svcs)
	var out []chstore.SpanMetricSeries
	for _, svc := range svcs {
		vals := map[int64]float64{}
		for t := from.Unix(); t <= to.Unix(); t += int64(step) {
			m := t - svcImpactStepSec // nokta dakikanın sonu
			c, e, ok := f.series[svc](int((m - f.t0.Unix()) / 60))
			if !ok {
				continue
			}
			if !isErr {
				vals[m] = c
			} else if e > 0 {
				vals[m] = e
			}
		}
		if len(vals) > 0 {
			out = append(out, minuteRows("service_name", svc, vals)...)
		}
	}
	return out, nil
}

// probeFixture — etiketleri keşfedilmiş (yalnız ilk aile) dedektör + sahte VM.
func probeFixture(t0 time.Time) (*TraceRED, *impProbeVM, []impFam) {
	d := NewTraceRED()
	pin := anomaly.REDFamilyPins()[0]
	d.labels[pin.ID] = traceFamilyLabels{svc: "service_name", route: "http_route", status: "http_response_status_code", at: time.Now(), ok: true}
	vm := &impProbeVM{t0: t0, series: map[string]func(int) (float64, float64, bool){}}
	return d, vm, d.cachedFamilies()
}

func probeBase(ratio float64, n int, svcs ...string) map[string][]float64 {
	base := map[string][]float64{}
	for _, svc := range svcs {
		base[chstore.MLSeriesKey(svc, tracePart(svcRoute, svcMeasureRatio))] = repeat(ratio, n)
		base[chstore.MLSeriesKey(svc, tracePart(svcRoute, svcMeasureCount))] = repeat(10, n)
	}
	return base
}

// probeCfg — ML ve svc_error_rate açık varsayılan ayar.
func probeCfg() chstore.MLConfig {
	cfg := chstore.DefaultMLConfig()
	cfg.Enabled = true
	cfg.Detectors[detSvcErrorRate] = true
	return cfg
}

func candServices(cs []impactCandidate) string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Service)
	}
	return strings.Join(out, ",")
}

// probeTriggers: ateşleyen + açık değil → tetik; açık koşu ya da bekleme → yok.
func TestProbeTriggers(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	orders := impactCandidate{BaseID: chstore.MLEventID(detSvcErrorRate, "orders", ""), Service: "orders"}
	checkout := impactCandidate{BaseID: chstore.MLEventID(detSvcErrorRate, "checkout", ""), Service: "checkout"}
	for _, tc := range []struct {
		name  string
		cands []impactCandidate
		open  map[string]bool
		cool  map[string]time.Time
		want  string
	}{
		{"kural ateşlemiyor (aday yok)", nil, nil, nil, ""},
		{"ateşliyor, açık koşu yok", []impactCandidate{orders}, nil, nil, "orders"},
		{"ateşliyor ama olay zaten açık", []impactCandidate{orders}, map[string]bool{orders.BaseID: true}, nil, ""},
		{"bekleme süresi içinde", []impactCandidate{orders}, nil, map[string]time.Time{orders.BaseID: now.Add(-impactProbeCooldown + time.Second)}, ""},
		{"bekleme süresi doldu", []impactCandidate{orders}, nil, map[string]time.Time{orders.BaseID: now.Add(-impactProbeCooldown)}, "orders"},
		{"biri açık, diğeri yeni", []impactCandidate{checkout, orders}, map[string]bool{orders.BaseID: true}, nil, "checkout"},
		{"biri beklemede, diğeri yeni", []impactCandidate{checkout, orders}, nil, map[string]time.Time{checkout.BaseID: now.Add(-time.Minute)}, "orders"},
	} {
		got := candServices(probeTriggers(tc.cands, func(id string) bool { return tc.open[id] }, tc.cool, now))
		if got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

// Dedektörün yoklaması: taban özeti son tikten, karar svcImpactDecide ile aynı.
func TestSvcErrorRateProbe(t *testing.T) {
	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	now := t0.Add(11*time.Minute + 30*time.Second) // pencere bitişi 10:09 → dakika 4..8
	cfg := probeCfg()
	ctx := context.Background()
	tick := time.Duration(cfg.TickSec) * time.Second

	d, vm, _ := probeFixture(t0)
	vm.series["orders"] = incident(6, 40, 0.004, 0.05)
	vm.series["checkout"] = incident(0, 0, 0.004, 0)
	deps := Deps{VM: vm, Backoff: newVMBackoff()}

	// taban önbelleği yok → aday yok, VM'e de gidilmez
	if c, err := d.probeImpact(ctx, deps, cfg, now); err != nil || len(c) != 0 || vm.ranges != 0 {
		t.Fatalf("taban yokken yoklama sessiz ve sorgusuz olmalı: %v %v sorgu=%d", c, err, vm.ranges)
	}
	// taban var: orders ateşler, sağlıklı checkout ateşlemez; aile başına 2 sorgu
	d.cacheImpactBase(probeBase(0.004, 30, "orders", "checkout"), now.Add(-time.Minute))
	c, err := d.probeImpact(ctx, deps, cfg, now)
	if err != nil || candServices(c) != "orders" || c[0].BaseID != chstore.MLEventID(detSvcErrorRate, "orders", "") || vm.ranges != 2 {
		t.Fatalf("orders adayı bekleniyordu: %v %v sorgu=%d", c, err, vm.ranges)
	}
	// kural henüz ateşlemiyor (2 ihlal dakikası)
	if c, _ := d.probeImpact(ctx, deps, cfg, now.Add(-time.Minute)); len(c) != 0 {
		t.Fatalf("2 ihlalde aday olmamalı: %v", c)
	}
	// taban örneği az → sessiz
	d.cacheImpactBase(probeBase(0.004, cfg.MinBaselineSamples-1, "orders"), now.Add(-time.Minute))
	if c, _ := d.probeImpact(ctx, deps, cfg, now); len(c) != 0 {
		t.Fatalf("taban örneği azken aday olmamalı: %v", c)
	}
	// taban bayat (son tik çok eski) → yok sayılır, sorgu atılmaz
	d.cacheImpactBase(probeBase(0.004, 30, "orders"), now.Add(-impactBaseMaxTicks*tick-time.Second))
	q := vm.ranges
	if c, _ := d.probeImpact(ctx, deps, cfg, now); len(c) != 0 || vm.ranges != q {
		t.Fatalf("bayat taban yok sayılmalı: %v sorgu=%d→%d", c, q, vm.ranges)
	}
	// sorgu hatası: hata döner, aday yok, satırlar saklanmaz
	d.cacheImpactBase(probeBase(0.004, 30, "orders"), now.Add(-time.Minute))
	vm.fail = errors.New("connection refused")
	if c, err := d.probeImpact(ctx, deps, cfg, now.Add(time.Minute)); err == nil || len(c) != 0 {
		t.Fatalf("sorgu hatası dönmeli, aday olmamalı: %v %v", c, err)
	}
	if d.probeRows(svcImpactEnd(now.Add(time.Minute), cfg.IngestLagSec)) != nil {
		t.Fatal("hatalı yoklamanın satırları saklanmamalı")
	}
	// VM yok / dedektör kapalı
	if c, err := d.probeImpact(ctx, Deps{}, cfg, now); err != nil || len(c) != 0 {
		t.Fatalf("VM yokken sessiz: %v %v", c, err)
	}
	off := probeCfg()
	off.Detectors[detSvcErrorRate] = false
	if !d.probeOn(cfg) || d.probeOn(off) {
		t.Fatal("yoklama yalnız svc_error_rate açıkken")
	}
}

// Erken tik yoklamanın satırlarını yeniden kullanır: aynı pencere bitişine
// ikinci sorgu çifti gitmez; pencere bitişi değişince yeniden sorulur.
func TestEarlyTickReusesProbeRows(t *testing.T) {
	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	now := t0.Add(11*time.Minute + 30*time.Second)
	cfg := probeCfg()
	ctx := context.Background()
	d, vm, fams := probeFixture(t0)
	vm.series["orders"] = incident(6, 40, 0.004, 0.05)
	deps := Deps{VM: vm, Backoff: newVMBackoff()}
	d.cacheImpactBase(probeBase(0.004, 30, "orders"), now.Add(-time.Minute))
	if _, err := d.probeImpact(ctx, deps, cfg, now); err != nil || vm.ranges != 2 {
		t.Fatalf("yoklama 2 aralık sorgusu atmalı: %v sorgu=%d", err, vm.ranges)
	}
	// erken tik birkaç saniye sonra, aynı dakikada → aynı pencere bitişi
	early := now.Add(5 * time.Second)
	end := svcImpactEnd(early, cfg.IngestLagSec)
	agg, err := d.impactMinutes(ctx, deps, fams, end, early)
	if err != nil || vm.ranges != 2 {
		t.Fatalf("aynı pencere bitişinde yeniden sorgu olmamalı: %v sorgu=%d", err, vm.ranges)
	}
	minute := map[string]map[int64]float64{}
	agg.emit(minute)
	if v := svcImpactDecide(svcImpactSamples(minute, "orders", end.Unix()), repeat(0.004, 30), repeat(10, 30), cfg.MinBaselineSamples, false); !v.Fire {
		t.Fatalf("yeniden kullanılan satırlar aynı kararı vermeli: %+v", v)
	}
	// sonraki dakika: pencere bitişi farklı → yeniden sorulur
	next := now.Add(time.Minute)
	if _, err := d.impactMinutes(ctx, deps, fams, svcImpactEnd(next, cfg.IngestLagSec), next); err != nil || vm.ranges != 4 {
		t.Fatalf("yeni pencere bitişinde 2 sorgu daha: %v sorgu=%d", err, vm.ranges)
	}
}

// Recorder kapıları: ML kapalı / lider değil / tablo hazır değil → yoklama
// yok (sorgu da yok); sorgu hatası → erken tik yok; tetikleyen aday beklemeye girer.
func TestProbeImpactGates(t *testing.T) {
	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	now := t0.Add(11*time.Minute + 30*time.Second)
	cfg := probeCfg()
	ctx := context.Background()
	d, vm, _ := probeFixture(t0)
	vm.series["orders"] = incident(6, 40, 0.004, 0.05)
	d.cacheImpactBase(probeBase(0.004, 30, "orders"), now.Add(-time.Minute))
	r := &Recorder{detectors: []Detector{d}, runs: newTracker(), backoff: newVMBackoff(), vm: vm}

	off := cfg
	off.Enabled = false
	if r.probeImpact(ctx, cfg, true, now) || vm.ranges != 0 {
		t.Fatal("tablo hazır değilken (henüz kabul edilmiş tik yok) yoklama olmamalı")
	}
	r.admit(cfg, true, true, nil, false)
	if r.probeImpact(ctx, off, true, now) || r.probeImpact(ctx, cfg, false, now) || vm.ranges != 0 {
		t.Fatal("ML kapalıyken / lider değilken yoklama olmamalı")
	}
	vm.fail = errors.New("connection refused")
	if r.probeImpact(ctx, cfg, true, now) || len(r.probeCool) != 0 {
		t.Fatal("sorgu hatası erken tik açmamalı, beklemeye de almamalı")
	}
	vm.fail = nil
	if !r.probeImpact(ctx, cfg, true, now.Add(time.Minute)) {
		t.Fatal("ateşleyen + açık koşusu olmayan servis erken tik istemeli")
	}
	if r.probeImpact(ctx, cfg, true, now.Add(2*time.Minute)) {
		t.Fatal("bekleme süresi içinde aynı aday yeniden erken tik açmamalı")
	}
	// olay açıldıysa bekleme dolsa da tetik yok
	at := now.Add(impactProbeCooldown + 2*time.Minute)
	d.cacheImpactBase(probeBase(0.004, 30, "orders"), at)
	r.runs.observe(chstore.MLEvent{ID: chstore.MLEventID(detSvcErrorRate, "orders", ""), Detector: detSvcErrorRate, Service: "orders"}, at, 5*time.Minute)
	if r.probeImpact(ctx, cfg, true, at) {
		t.Fatal("açık koşusu olan servis erken tik açmamalı")
	}
	if len(r.probeCool) != 0 {
		t.Fatalf("süresi dolan bekleme kaydı budanmalı: %v", r.probeCool)
	}
}

// admit: lider tik sayacı yalnız ZAMANLI tikte artar; lider değilken sıfırlanır.
func TestAdmitCountsOnlyScheduledTicks(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	cfg.Enabled = true
	r := &Recorder{runs: newTracker()}
	for i, tc := range []struct {
		leader, ready, early, want bool
		err                        error
		ticks                      int
	}{
		{true, true, false, true, nil, 1},
		{true, true, true, true, nil, 1}, // erken tik saymaz
		{true, true, true, true, nil, 1},
		{true, true, false, true, nil, 2},
		{true, false, false, false, nil, 2},                  // tablo yok: atlandı, sayaç aynı
		{true, true, false, false, errors.New("ch down"), 2}, // tablo yoklaması düştü
		{false, true, true, false, nil, 0},                   // liderlik gitti: erken tikte de sıfır
		{true, true, true, true, nil, 0},                     // yeni lider: erken tik saymaz
		{true, true, false, true, nil, 1},
	} {
		if got := r.admit(cfg, tc.leader, tc.ready, tc.err, tc.early); got != tc.want || r.leaderTicks != tc.ticks {
			t.Fatalf("adım %d: admit=%v ticks=%d (want %v / %d)", i, got, r.leaderTicks, tc.want, tc.ticks)
		}
	}
	off := cfg
	off.Enabled = false
	if r.admit(off, true, true, nil, false) || r.leaderTicks != 1 {
		t.Fatal("ML kapalıyken tik atlanır")
	}
}

// Erken tik yaygın yavaşlamanın ardışıklık sayacını ilerletmez: 1 tiklik
// koşul erken tikte ateşlemez; zaten ateşlemiş koşu sürer.
func TestBreadthHoldDoesNotAdvance(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cfg := chstore.DefaultMLConfig()
	var kept []chstore.MLEvent
	for _, op := range []string{"GET /a", "GET /b", "GET /c"} {
		kept = append(kept, chstore.MLEvent{Detector: detOpLatency, Service: "orders", Code: op, Direction: "up", Baseline: 200, Observed: 1500,
			Evidence: `{"measure":"p95"}`})
	}
	first, next := breadthEvents(kept, nil, nil, now, cfg)
	if len(first) != 0 || next["orders"].ticks != 1 {
		t.Fatalf("ilk tik: ateşlemez, sayaç 1: %v %v", first, next)
	}
	if evs := breadthHoldEvents(kept, nil, next, now.Add(time.Minute), cfg); len(evs) != 0 || next["orders"].ticks != 1 {
		t.Fatalf("erken tik sayacı ilerletmemeli / ateşlememeli: %v %v", evs, next)
	}
	fired, next2 := breadthEvents(kept, nil, next, now.Add(5*time.Minute), cfg)
	if len(fired) != 1 || next2["orders"].ticks != 2 {
		t.Fatalf("ikinci ZAMANLI tik ateşler: %v %v", fired, next2)
	}
	hold := breadthHoldEvents(kept, nil, next2, now.Add(6*time.Minute), cfg)
	if len(hold) != 1 || hold[0].ID != fired[0].ID || hold[0].StartedAt != fired[0].StartedAt || next2["orders"].ticks != 2 {
		t.Fatalf("ateşlemiş koşu erken tikte aynı olayla sürmeli: %v", hold)
	}
	if evs := breadthHoldEvents(kept[:2], nil, next2, now.Add(6*time.Minute), cfg); len(evs) != 0 {
		t.Fatalf("koşul düştüyse erken tikte olay yok: %v", evs)
	}
}

type fakeRefunder struct{ got []string }

func (f *fakeRefunder) refundCap(det string) { f.got = append(f.got, det) }

// Erken tikte yazılmayan (kalite süzgecinin düşürdüğü) adayın günlük tavan
// hakkı geri verilir; bu tikte görülen (yazılan) adayınki harcanmış kalır.
func TestEarlyCapRefund(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tr := newTracker()
	rf := &fakeRefunder{}
	written := chstore.MLEventID(detSvcErrorRate, "orders", "")
	dropped := chstore.MLEventID(detOpLatency, "checkout", "GET /pay")
	stale := chstore.MLEventID(detOpErrorRate, "orders", "GET /a")
	tr.observe(chstore.MLEvent{ID: written, Detector: detSvcErrorRate, Service: "orders"}, now, 5*time.Minute)
	tr.observe(chstore.MLEvent{ID: stale, Detector: detOpErrorRate, Service: "orders"}, now.Add(-time.Hour), 5*time.Minute) // eski görülme
	refundEarlyCaps([]capSpend{{rf, detSvcErrorRate, written}, {rf, detOpLatency, dropped}, {rf, detOpErrorRate, stale}},
		func(id string) bool { return tr.seenAt(id, now) })
	if strings.Join(rf.got, ",") != detOpLatency+","+detOpErrorRate {
		t.Fatalf("yalnız bu tikte görülmeyen adaylar iade edilmeli: %v", rf.got)
	}
}

// ── döngü simülasyonu (sahte saat) ───────────────────────────────────────

type simTick struct {
	at    time.Time
	early bool
}

// Zamanlı tik 300 sn, yoklama 60 sn. orders 10:06'da, checkout 10:17'de
// bozulur; payments 10:27'de bozulur ama olayı kalite süzgecinde düşer.
func TestTickLoopEarlyTicks(t *testing.T) {
	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	stop := t0.Add(50 * time.Minute)
	cfg := probeCfg()
	cfg.TickSec, cfg.IngestLagSec = 300, 120
	cfg.Quality.ImpactMinAbs = 0.03 // payments (%0,4 → %2) etki eşiğinin altında kalır
	ctx := context.Background()

	d, vm, fams := probeFixture(t0)
	vm.series["orders"] = incident(6, 60, 0.004, 0.05)
	vm.series["checkout"] = incident(17, 60, 0.004, 0.05)
	vm.series["payments"] = incident(27, 60, 0.004, 0.02)
	vm.series["search"] = incident(0, 0, 0.004, 0) // hep sağlıklı
	base := probeBase(0.004, 30, "orders", "checkout", "payments", "search")
	r := &Recorder{detectors: []Detector{d}, runs: newTracker(), backoff: newVMBackoff(), vm: vm}

	clk := t0.Add(30 * time.Second)
	var ticks []simTick
	var probes []time.Time
	firstEvent := map[string]time.Time{}
	loop := tickLoop{
		now:  func() time.Time { return clk },
		wait: func(dur time.Duration) bool { clk = clk.Add(dur); return clk.Before(stop) },
		cfg:  func() chstore.MLConfig { return cfg },
		probe: func(cfg chstore.MLConfig, now time.Time) bool {
			probes = append(probes, now)
			return r.probeImpact(ctx, cfg, true, now)
		},
		// tam tikin servis hata oranı kesiti: gerçek kabul kapısı, 1 dk satırları
		// (yoklamadan yeniden kullanım), karar, kalite süzgeci, kapanış.
		tick: func(cfg chstore.MLConfig, early bool) {
			now := clk
			if !r.admit(cfg, true, true, nil, early) {
				t.Fatalf("%s tik kabul edilmeliydi", now.Format("15:04:05"))
			}
			ticks = append(ticks, simTick{now, early})
			tick := time.Duration(cfg.TickSec) * time.Second
			deps := Deps{VM: vm, Backoff: r.backoff,
				openRun:  func(id string) bool { return r.runs.openRun(id, now, tick) },
				runStart: func(id string) int64 { return r.runs.startedOf(id, now, tick) }}
			end := svcImpactEnd(now, cfg.IngestLagSec)
			q := vm.ranges
			agg, err := d.impactMinutes(ctx, deps, fams, end, now)
			if err != nil {
				t.Fatal(err)
			}
			if early && vm.ranges != q {
				t.Fatalf("%s erken tik yoklamanın satırlarını yeniden kullanmalı", now.Format("15:04:05"))
			}
			minute := map[string]map[int64]float64{}
			agg.emit(minute)
			d.cacheImpactBase(base, now)
			kept, _ := qualityFilter(d.svcImpactEvents(deps, minute, base, end, cfg, "weekday", now), cfg, r.runs, now)
			for _, e := range kept {
				if _, ok := firstEvent[e.Service]; !ok {
					firstEvent[e.Service] = now
				}
			}
			if !early {
				r.runs.closeStale(now, tickCoverage{})
			} else {
				clk = clk.Add(20 * time.Second) // erken tik zaman alır; takvim yine kaymamalı
			}
		},
	}
	loop.run()

	at := func(min, sec int) time.Time {
		return t0.Add(time.Duration(min)*time.Minute + time.Duration(sec)*time.Second)
	}
	var sched, early []time.Time
	for _, tk := range ticks {
		if tk.early {
			early = append(early, tk.at)
		} else {
			sched = append(sched, tk.at)
		}
	}
	// zamanlı tikler: 10:00:30'dan başlayıp tam 300 sn arayla, erken tiklere rağmen
	if len(sched) != 10 {
		t.Fatalf("10 zamanlı tik bekleniyordu: %d", len(sched))
	}
	for i, s := range sched {
		if want := at(5*i, 30); !s.Equal(want) {
			t.Fatalf("zamanlı tik %d kaydı: %s (want %s)", i, s.Format("15:04:05"), want.Format("15:04:05"))
		}
	}
	// erken tikler: orders 10:11:30 (3. ihlal dakikası 10:08, ingest payı 120 sn →
	// 10:11'den sonraki ilk yoklama), checkout 10:22:30, payments 10:32:30 ve
	// (olayı düştüğü için) bekleme dolunca 10:47:30
	wantEarly := []time.Time{at(11, 30), at(22, 30), at(32, 30), at(47, 30)}
	if len(early) != len(wantEarly) {
		t.Fatalf("erken tikler: %v (want %v)", early, wantEarly)
	}
	for i := range early {
		if !early[i].Equal(wantEarly[i]) {
			t.Fatalf("erken tik %d: %s (want %s)", i, early[i].Format("15:04:05"), wantEarly[i].Format("15:04:05"))
		}
	}
	// olay erken tikte yazıldı (yoklama olmasa 10:15:30 / 10:25:30 olurdu)
	if !firstEvent["orders"].Equal(at(11, 30)) || !firstEvent["checkout"].Equal(at(22, 30)) {
		t.Fatalf("ilk olay anları: %v", firstEvent)
	}
	if _, ok := firstEvent["payments"]; ok {
		t.Fatal("payments olayı kalite süzgecinde düşmeliydi (senaryo bozuk)")
	}
	if _, ok := firstEvent["search"]; ok {
		t.Fatal("sağlıklı servis olay üretmemeli")
	}
	// lider tik sayacı yalnız zamanlı tikleri saydı
	if r.leaderTicks != len(sched) {
		t.Fatalf("lider tik sayacı %d, zamanlı tik %d", r.leaderTicks, len(sched))
	}
	// yoklama aralığı ≥ 60 sn; zamanlı tike impactProbeSkipBefore ya da daha az kala yoklama yok
	for i, p := range probes {
		if i > 0 && p.Sub(probes[i-1]) < impactProbeInterval {
			t.Fatalf("yoklamalar arası %s", p.Sub(probes[i-1]))
		}
		for _, s := range sched {
			if s.After(p) && s.Sub(p) <= impactProbeSkipBefore {
				t.Fatalf("%s yoklaması zamanlı tike (%s) çok yakın", p.Format("15:04:05"), s.Format("15:04:05"))
			}
		}
	}
	// kör dakika yok: erken tiksiz (kaymasız) bir çevrimde her pencere bitişi
	// değerlendirilir — iki zamanlı tik arasında 4 yoklama (+60, +120, +180, +240).
	// Eski kural +240'ı atlıyor, tam 3 dakikalık olay o boşluğa düşerse kaçıyordu.
	quiet := 0
	for i := 1; i < len(sched); i++ {
		n := 0
		for _, p := range probes {
			if p.After(sched[i-1]) && p.Before(sched[i]) {
				n++
			}
		}
		if n == 4 {
			quiet++
		} else if n > 4 || n < 3 {
			t.Fatalf("%s–%s arası %d yoklama", sched[i-1].Format("15:04:05"), sched[i].Format("15:04:05"), n)
		}
	}
	if quiet == 0 {
		t.Fatal("hiçbir çevrimde 4 yoklama yok: +240 penceresi değerlendirilmiyor")
	}
	// VM maliyeti: yoklama başına 2, zamanlı tik başına 2, erken tik 0 aralık sorgusu
	if want := 2 * (len(probes) + len(sched)); vm.ranges != want {
		t.Fatalf("aralık sorgusu %d, beklenen %d (yoklama %d, zamanlı tik %d)", vm.ranges, want, len(probes), len(sched))
	}
}
