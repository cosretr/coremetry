package chstore

// op_p99_cache_test.go — v0.10.1118: taban önbelleğinin yol seçimi (sahte G/Ç).
// Pinler: soğuk başlangıç eski yol + arka plan tazelemesi; pencere sonu = ilk
// cari kova (iki kapsam paylaşınca EN ERKENİ — taban hiçbir kapsamın cari
// kovasını içermez); saatte bir tazeleme, tek uçuş; hata → son iyi taban
// korunur + 15 dk bekleme; LIMIT'e dayanan taban → eski yol + 6 sa bekleme;
// tur sorgusu tavanı → eski yol; bayat (> 6 sa) / ileride taban → eski yol;
// paylaşım anahtarı; uzun süre kullanılmayan giriş sıfırlanır; panik süreci
// düşürmez.

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakePivotIO struct {
	now                          time.Time
	legacyN, currentN, baselineN int
	baseArgs                     [][]any
	base                         map[OpPair]OpBaseline
	baseRows                     int
	baseErr                      error
	basePanic                    bool
	cur                          []OpP99CurRow
	curErr                       error
	spawned                      []func()
}

func (f *fakePivotIO) io() opPivotIO {
	return opPivotIO{
		legacy: func(context.Context, OpP99PivotSpec) ([]OpP99Row, error) {
			f.legacyN++
			return []OpP99Row{{Service: "legacy"}}, nil
		},
		current: func(context.Context, OpP99PivotSpec) ([]OpP99CurRow, error) {
			f.currentN++
			return f.cur, f.curErr
		},
		baseline: func(_ context.Context, _ string, args []any) (map[OpPair]OpBaseline, int, error) {
			f.baselineN++
			f.baseArgs = append(f.baseArgs, args)
			if f.basePanic {
				panic("boom")
			}
			return f.base, f.baseRows, f.baseErr
		},
		spawn: func(fn func()) { f.spawned = append(f.spawned, fn) },
		now:   func() time.Time { return f.now },
	}
}

func (f *fakePivotIO) drain() {
	fns := f.spawned
	f.spawned = nil
	for _, fn := range fns {
		fn()
	}
}

// pfSpecAt — op_latency biçimi, dwell 2: ilk cari kova now − 10 dk.
func pfSpecAt(now time.Time) OpP99PivotSpec {
	sp := pfOpLatSpec(2, nil, false, nil)
	sp.SlotStarts = []time.Time{now.Add(-5 * time.Minute), now.Add(-10 * time.Minute)}
	sp.BaseStart, sp.AlignedNow = sp.SlotStarts[1].Add(-24*time.Hour), now
	return sp
}

// pfSvcSpecAt — svc-slowdown biçimi: tek cari kova now − 5 dk (op_latency'den bir
// kova SONRA başlar), batch dışlaması.
func pfSvcSpecAt(now time.Time) OpP99PivotSpec {
	return ServiceSlowdownOpsSpec(now.Add(-5*time.Minute), DefaultServiceSlowdown(), DefaultAnomalySensitivity())
}

func TestOpPivotCacheLifecycle(t *testing.T) {
	ctx := context.Background()
	pair := OpPair{Service: "a", Operation: "op"}
	f := &fakePivotIO{now: pfNow, base: map[OpPair]OpBaseline{pair: {P99Ms: 100, P95Ms: 90, Calls: 28_800, Buckets: 288}}, baseRows: 1,
		cur: []OpP99CurRow{{Service: "a", Operation: "op", Slots: []OpP99Slot{{P99Ms: 900, Calls: 40}, {P99Ms: 900, Calls: 40}}}}}
	var c opPivotCache

	// Soğuk: eski yol, tazeleme arka planda, pencere [ilk cari − 24 sa, ilk cari).
	sp := pfSpecAt(f.now)
	if rows, _ := c.pivot(ctx, "s", sp, f.io()); f.legacyN != 1 || f.currentN != 0 || len(rows) != 1 || rows[0].Service != "legacy" {
		t.Fatalf("soğuk başlangıç eski yol olmalı: legacy=%d current=%d", f.legacyN, f.currentN)
	}
	if len(f.spawned) != 1 || f.baselineN != 0 {
		t.Fatal("tazeleme arka plana bırakılmalı (tur içinde değil)")
	}
	f.drain()
	if a := f.baseArgs[0]; a[0] != sp.BaseStart || a[1] != sp.SlotStarts[1] || a[2] != 30 {
		t.Fatalf("taban penceresi %v–%v (%v), beklenen %v–%v", a[0], a[1], a[2], sp.BaseStart, sp.SlotStarts[1])
	}

	// Sıcak: yalnız tur sorgusu + birleşim, tazeleme yok.
	rows, err := c.pivot(ctx, "s", sp, f.io())
	if err != nil || f.currentN != 1 || f.legacyN != 1 || len(f.spawned) != 0 || len(rows) != 1 || rows[0].Service != "a" {
		t.Fatalf("sıcak yol: rows=%v err=%v legacy=%d current=%d spawn=%d", rows, err, f.legacyN, f.currentN, len(f.spawned))
	}

	// 55 dk sonra: tazeleme yok; 60 dk: önbellek + TEK tazeleme.
	f.now = pfNow.Add(55 * time.Minute)
	c.pivot(ctx, "s", pfSpecAt(f.now), f.io())
	if len(f.spawned) != 0 {
		t.Fatal("gecikme < 1 sa iken tazeleme olmamalı")
	}
	f.now = pfNow.Add(60 * time.Minute)
	c.pivot(ctx, "s", pfSpecAt(f.now), f.io())
	c.pivot(ctx, "s", pfSpecAt(f.now), f.io())
	if len(f.spawned) != 1 || f.legacyN != 1 {
		t.Fatalf("1 sa: önbellek kullanılmalı ve tek tazeleme (spawn=%d legacy=%d)", len(f.spawned), f.legacyN)
	}

	// Tazeleme hatası: son iyi taban korunur; 15 dk boyunca yeniden deneme YOK.
	f.baseErr = errors.New("ch down")
	f.drain()
	for _, m := range []time.Duration{61, 70, 74} {
		f.now = pfNow.Add(m * time.Minute)
		c.pivot(ctx, "s", pfSpecAt(f.now), f.io())
	}
	if f.legacyN != 1 || len(f.spawned) != 0 || f.baselineN != 2 {
		t.Fatalf("hata sonrası son iyi taban, bekleme içinde deneme yok (legacy=%d spawn=%d baseline=%d)", f.legacyN, len(f.spawned), f.baselineN)
	}
	f.now = pfNow.Add(75 * time.Minute)
	c.pivot(ctx, "s", pfSpecAt(f.now), f.io())
	if len(f.spawned) != 1 {
		t.Fatal("15 dk sonra yeniden denenmeli")
	}
	f.drain()

	// Tazeleme 6 sa'ten uzun düşerse bayat taban kullanılmaz → eski yol.
	f.now = pfNow.Add(7 * time.Hour)
	c.pivot(ctx, "s", pfSpecAt(f.now), f.io())
	if f.legacyN != 2 {
		t.Fatal("bayat taban (> OpBaselineMaxLag) eski yola düşmeli")
	}
	f.baseErr = nil
	f.drain()
	if c.pivot(ctx, "s", pfSpecAt(f.now), f.io()); f.legacyN != 2 {
		t.Fatal("başarılı tazelemeden sonra önbellek yolu")
	}

	// Tur sorgusu tavanı → eski yol; tur sorgusu hatası → hata, eski yol yok.
	f.cur = make([]OpP99CurRow, OpP99CurrentLimit)
	c.pivot(ctx, "s", pfSpecAt(f.now), f.io())
	if f.legacyN != 3 {
		t.Fatal("tavana dayanan tur sorgusu eski yola düşmeli")
	}
	f.cur, f.curErr = nil, errors.New("timeout")
	if _, err := c.pivot(ctx, "s", pfSpecAt(f.now), f.io()); err == nil || f.legacyN != 3 {
		t.Fatal("tur sorgusu hatası çağırana dönmeli, eski yol koşmamalı")
	}
	f.curErr = nil

	// İleride taban (dwell büyüdü: ilk cari kova pencere sonundan önce) → eski yol + tazele.
	ahead := pfSpecAt(f.now)
	ahead.SlotStarts = append(ahead.SlotStarts, f.now.Add(-15*time.Minute))
	ahead.BaseStart = ahead.SlotStarts[2].Add(-24 * time.Hour)
	c.pivot(ctx, "s", ahead, f.io())
	if f.legacyN != 4 || len(f.spawned) != 1 {
		t.Fatalf("ileride taban eski yol + tazeleme (legacy=%d spawn=%d)", f.legacyN, len(f.spawned))
	}
	f.drain()

	// Uzun süre kullanılmayan giriş (liderliği kaybedip yeniden kazanan pod) sıfırlanır.
	f.now = f.now.Add(3 * time.Hour)
	c.pivot(ctx, "s", pfSpecAt(f.now), f.io())
	if f.legacyN != 5 {
		t.Fatal("2 sa'ten uzun kullanılmamış taban yeniden kurulmalı (eski yol)")
	}
	f.drain()
}

func TestOpPivotCacheNoBaselineBackoffAndCap(t *testing.T) {
	ctx := context.Background()
	f := &fakePivotIO{now: pfNow, baseErr: errors.New("down")}
	var c opPivotCache
	// İyi taban yok + hata: tespit eski yoldan sürer, ama 24 sa tazelemesi her
	// turda DEĞİL — 15 dk'da bir (eskinin ~2 katı okuma olmasın).
	for m := 0; m < 30; m++ {
		f.now = pfNow.Add(time.Duration(m) * time.Minute)
		c.pivot(ctx, "s", pfSpecAt(f.now), f.io())
		f.drain()
	}
	if f.legacyN != 30 || f.baselineN != 2 {
		t.Fatalf("30 turda 30 eski yol, 2 tazeleme (0. ve 15. dk) bekleniyordu: legacy=%d baseline=%d", f.legacyN, f.baselineN)
	}
	// Panik: tazeleme biter, işaret bırakılır, bekleme sonrası yeniden dener.
	f.basePanic = true
	f.now = pfNow.Add(30 * time.Minute)
	c.pivot(ctx, "s", pfSpecAt(f.now), f.io())
	f.drain()
	f.basePanic, f.baseErr, f.baseRows = false, nil, OpBaselineLimit
	f.now = pfNow.Add(45 * time.Minute)
	c.pivot(ctx, "s", pfSpecAt(f.now), f.io())
	f.drain()
	if f.baselineN != 4 {
		t.Fatalf("panikten sonra yeniden denenmeli: baseline=%d", f.baselineN)
	}
	// LIMIT'e dayanan taban kullanılmaz; 6 sa yeniden denenmez (saatte 1 GB boşa yok).
	legacy := f.legacyN
	for h := 1; h < 6; h++ {
		f.now = pfNow.Add(45*time.Minute + time.Duration(h)*time.Hour)
		c.pivot(ctx, "s", pfSpecAt(f.now), f.io())
	}
	if f.legacyN != legacy+5 || len(f.spawned) != 0 {
		t.Fatalf("capped taban: eski yol, 6 sa tazeleme yok (legacy=%d spawn=%d)", f.legacyN-legacy, len(f.spawned))
	}
	f.now = pfNow.Add(45*time.Minute + 6*time.Hour)
	c.pivot(ctx, "s", pfSpecAt(f.now), f.io())
	if len(f.spawned) != 1 {
		t.Fatal("capped taban 6 sa sonra yeniden denenmeli")
	}
}

// TestOpPivotCacheShared — iki kapsam AYNI girişi paylaşır (tek 24 sa okuması);
// pencere sonu iki kapsamın ilk cari kovalarının EN ERKENİ; değeri değiştiren
// girdiler (çağrı tabanı, pencere uzunluğu) ayrı giriş.
func TestOpPivotCacheShared(t *testing.T) {
	ctx := context.Background()
	f := &fakePivotIO{now: pfNow, base: map[OpPair]OpBaseline{}, baseRows: 0}
	var c opPivotCache
	op, svc := pfSpecAt(f.now), pfSvcSpecAt(f.now)
	// İlk gelen svc-slowdown (cari kova now − 5 dk): tek kapsam görüldü → E = now − 5 dk.
	c.pivot(ctx, OpPivotScopeSvcSlowdown, svc, f.io())
	f.drain()
	if f.baseArgs[0][1] != svc.SlotStarts[0] {
		t.Fatalf("pencere sonu %v, beklenen %v", f.baseArgs[0][1], svc.SlotStarts[0])
	}
	// op_latency'nin ilk cari kovası (now − 10 dk) E'den önce → "ahead": eski yol +
	// tazeleme, yeni E iki kapsamın en erkeni.
	c.pivot(ctx, OpPivotScopeOpLatency, op, f.io())
	if f.legacyN != 2 || len(f.spawned) != 1 {
		t.Fatalf("ahead: legacy=%d spawn=%d", f.legacyN, len(f.spawned))
	}
	f.drain()
	if f.baseArgs[1][1] != op.SlotStarts[1] {
		t.Fatalf("paylaşılan pencere sonu %v, beklenen en erken %v", f.baseArgs[1][1], op.SlotStarts[1])
	}
	// Artık iki kapsam da önbellekli; bir saat boyunca tek giriş, ek okuma yok.
	for m := 0; m < 55; m += 5 {
		f.now = pfNow.Add(time.Duration(m) * time.Minute)
		c.pivot(ctx, OpPivotScopeSvcSlowdown, pfSvcSpecAt(f.now), f.io())
		c.pivot(ctx, OpPivotScopeOpLatency, pfSpecAt(f.now), f.io())
	}
	if f.legacyN != 2 || len(f.spawned) != 0 || len(c.entries) != 1 {
		t.Fatalf("paylaşım: legacy=%d spawn=%d entries=%d", f.legacyN, len(f.spawned), len(c.entries))
	}
	// Saatlik tazelemeyi svc başlatır (gecikmesi önce 1 sa olur) ama pencere sonu
	// yine EN ERKEN (op_latency'nin ilk cari kovası) — çalkantı yok.
	f.now = pfNow.Add(65 * time.Minute)
	c.pivot(ctx, OpPivotScopeSvcSlowdown, pfSvcSpecAt(f.now), f.io())
	f.drain()
	if got, want := f.baseArgs[2][1], pfSpecAt(pfNow.Add(50 * time.Minute)).SlotStarts[1]; got != want {
		t.Fatalf("saatlik tazeleme sonu %v, beklenen op_latency'nin son ilk cari kovası %v", got, want)
	}
	c.pivot(ctx, OpPivotScopeOpLatency, pfSpecAt(f.now), f.io())
	if f.legacyN != 2 || len(f.spawned) != 0 {
		t.Fatalf("tazelemeden sonra iki kapsam da önbellekli: legacy=%d spawn=%d", f.legacyN, len(f.spawned))
	}

	// Taban DEĞERİNİ değiştirmeyenler aynı anahtar: kapsam, dışlama, BaseP95,
	// aktif küme, batch kapısı. Değiştirenler ayrı: çağrı tabanı, pencere uzunluğu.
	same := op
	same.BaseP95, same.Active = true, []OpPair{{Service: "a", Operation: "b"}}
	same.Batch = pfOpLatSpec(2, nil, true, nil).Batch
	same.ExcludeCond, same.ExcludeArgs = "(positionCaseInsensitive(service_name, ?) > 0)", []any{"-job"}
	if opBaselineKeyOf(same) != opBaselineKeyOf(op) || opBaselineKeyOf(svc) != opBaselineKeyOf(op) {
		t.Fatal("dışlama / nicelik / aktif küme / batch kapısı anahtarı bölmemeli")
	}
	mc := op
	mc.Floors.MinCalls = 50
	lb := op
	lb.BaseStart = lb.BaseStart.Add(-time.Hour)
	if opBaselineKeyOf(mc) == opBaselineKeyOf(op) || opBaselineKeyOf(lb) == opBaselineKeyOf(op) {
		t.Fatal("çağrı tabanı / pencere uzunluğu ayrı giriş olmalı")
	}
	// Eşdeğerlik ön şartı: tabanların hepsi ≤ 0 → eski yol, önbellek yok.
	zero := op
	zero.Floors = OpP99Floors{}
	n := len(c.entries)
	c.pivot(ctx, "z", zero, f.io())
	if f.legacyN != 3 || len(c.entries) != n {
		t.Fatal("eşdeğerliği garanti edilemeyen spec eski yoldan okumalı")
	}
}

func TestOpBaselineDecide(t *testing.T) {
	first, now := pfNow, pfNow.Add(10*time.Minute)
	good := map[OpPair]OpBaseline{}
	for _, tc := range []struct {
		name         string
		e            opBaselineEntry
		use, refresh bool
		reason       string
	}{
		{"boş", opBaselineEntry{}, false, true, "no_baseline"},
		{"boş, uçuşta", opBaselineEntry{refreshing: true}, false, false, "no_baseline"},
		{"boş, hata beklemesi", opBaselineEntry{nextTryAt: now.Add(time.Minute)}, false, false, "no_baseline"},
		{"boş, bekleme bitti", opBaselineEntry{nextTryAt: now}, false, true, "no_baseline"},
		{"taze", opBaselineEntry{base: good, end: first.Add(-30 * time.Minute)}, true, false, ""},
		{"sınırda 1 sa", opBaselineEntry{base: good, end: first.Add(-time.Hour)}, true, true, ""},
		{"1 sa, hata beklemesi", opBaselineEntry{base: good, end: first.Add(-time.Hour), nextTryAt: now.Add(time.Minute)}, true, false, ""},
		{"1 sa, uçuşta", opBaselineEntry{base: good, end: first.Add(-time.Hour), refreshing: true}, true, false, ""},
		{"6 sa (tavan dahil)", opBaselineEntry{base: good, end: first.Add(-6 * time.Hour)}, true, true, ""},
		{"bayat", opBaselineEntry{base: good, end: first.Add(-6*time.Hour - time.Minute)}, false, true, "stale"},
		{"ileride", opBaselineEntry{base: good, end: first.Add(time.Minute)}, false, true, "ahead"},
		{"tavan, beklemede", opBaselineEntry{capped: true, end: first.Add(-5 * time.Hour), nextTryAt: now.Add(time.Hour)}, false, false, "capped"},
		{"tavan, bekleme bitti", opBaselineEntry{capped: true, end: first.Add(-6 * time.Hour), nextTryAt: now}, false, true, "capped"},
	} {
		v := opBaselineDecide(tc.e, first, now)
		if v.use != tc.use || v.refresh != tc.refresh || v.reason != tc.reason {
			t.Errorf("%s: %+v", tc.name, v)
		}
	}
	// Pencere sonu: tazeyken görülmüş kapsamların en erkeni; bayat kapsam sayılmaz.
	seen := map[string]opScopeSeen{
		"a": {firstSlot: first.Add(-5 * time.Minute), at: now},
		"b": {firstSlot: first.Add(-time.Hour), at: now.Add(-16 * time.Minute)},
	}
	if got := opBaselineEnd(seen, first, now); !got.Equal(first.Add(-5 * time.Minute)) {
		t.Fatalf("pencere sonu %v", got)
	}
}
