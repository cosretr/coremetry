package chstore

// op_p99_cached_test.go — v0.10.1118: operasyon pivotunun taban önbellekli yolu
// ESKİ tek geçişle (OpP99PivotQuery) aynı satırları döndürmeli.
//
// Ölçüm (operatör prod system.query_log, 1 sa): pivot n=13/sa, ort. 1692 ms,
// ≈ 974 MiB okuma; kardeşi n=3/sa, 977 MiB — her tur 24 sa tDigest durumunu
// yalnız taban için yeniden birleştiriyordu. Yeni yol: taban önbelleği
// (OpBaselineQuery) + yalnız cari kovalar (OpP99CurrentQuery) + SAF birleşim
// (JoinOpP99Pivot).
//
// NE ÇİVİLİYOR:
//   - refLegacyPivot: eski SQL'in (dış maxIf/sumIf, HAVING, ORDER BY oran,
//     LIMIT) metinden yazılmış Go referansı; yeni yol (taban HAVING'i + tur
//     HAVING'inin Go ikizi + JoinOpP99Pivot) aynı sentetik satırlarla AYNI
//     çıktıyı verir: dwell 1/2/3, aktif-küme histerezisi, batch kapısı + muaf
//     çiftler, BaseP95 kipi, CurP95MinMs, eşit oran sırası, LIMIT kesimi, sınır
//     eşitlikleri, NaN; ayrıca rastgele özellik testi;
//   - iki yeni sorgu SINIRLI (zaman aralıklı WHERE, LIMIT, max_execution_time,
//     tek FROM operation_summary_5m) ve tur sorgusu TABAN penceresini okumaz;
//   - clickhouse local (ikili yoksa atlanır): gerçek üç sorgu metni sentetik
//     AggregatingMergeTree üstünde — eski tek geçiş ile taban + cari + birleşim
//     aynı satırları döndürür.

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ── Sentetik girdi: iç alt sorgunun çıktısı (çift × kova numarası) ─────────

type pfAgg struct {
	p99, p95       float64
	calls, buckets uint64
}

// pfPair — bir çiftin iç alt sorgu satırları: base = slot 0 (taban penceresi),
// slots[i] = slot i+1 (0 = en yeni kova); nil = o kovada satır yok.
type pfPair struct {
	svc, op string
	base    *pfAgg
	slots   []*pfAgg
}

func agg(p99 float64, calls uint64) *pfAgg { return &pfAgg{p99: p99, p95: p99, calls: calls} }

// baseAgg — taban: kova başına 100 çağrı × 288 aktif kova (24 sa).
func baseAgg(p99, p95 float64) *pfAgg {
	return &pfAgg{p99: p99, p95: p95, calls: 28_800, buckets: 288}
}

func pfInList(l []OpPair, svc, op string) bool {
	for _, p := range l {
		if p.Service == svc && p.Operation == op {
			return true
		}
	}
	return false
}

// refLegacyPivot — ESKİ tek geçişin (OpP99PivotQuery) anlamı, SQL metninden
// birebir: dış sorgu kova başına maxIf/sumIf (satırı olmayan kova 0), HAVING
// metin sırasıyla, ORDER BY cur_p99 / base DESC, LIMIT OpP99PivotLimit. SQL
// eşit oranların sırasını tanımlamaz; referans yeni yolun söz verdiği sırayı
// (servis, operasyon artan) kullanır — SQL'in izin verdiği sıralardan biri.
func refLegacyPivot(sp OpP99PivotSpec, pairs []pfPair, isBatch func(string) bool) []OpP99Row {
	dwell := len(sp.SlotStarts)
	f := sp.Floors
	get := func(a *pfAgg) pfAgg {
		if a == nil {
			return pfAgg{}
		}
		return *a
	}
	out := []OpP99Row{}
	for _, pp := range pairs {
		hasRow := pp.base != nil
		for _, s := range pp.slots {
			hasRow = hasRow || s != nil
		}
		if !hasRow { // iç sorgu satır üretmez → dış GROUP BY'da çift yok
			continue
		}
		slot := func(i int) pfAgg { // i = 1..dwell
			if i-1 < len(pp.slots) {
				return get(pp.slots[i-1])
			}
			return pfAgg{}
		}
		b := get(pp.base)
		curP99, curCalls, curP95 := slot(1).p99, slot(1).calls, slot(1).p95
		base := b.p99 // maxIf(p99, slot = 0) AS base_p99
		if sp.BaseP95 {
			base = b.p95 // maxIf(p95, slot = 0) AS base_p95
		}
		// HAVING cur_calls >= ? AND base_calls >= ?
		//    AND base > 0 AND cur_p99 >= ? * base AND cur_p99 >= ?
		h := int64(curCalls) >= int64(f.MinCalls) && int64(b.calls) >= int64(f.MinCalls) &&
			base > 0 && curP99 >= f.Ratio*base && curP99 >= f.MinP99Ms
		// AND calls_i >= ? AND p99_i >= ? * base AND p99_i >= ?   (i = 2..dwell)
		// [OR (service_name, name) IN (aktif)]
		earlier := true
		for i := 2; i <= dwell; i++ {
			s := slot(i)
			earlier = earlier && int64(s.calls) >= int64(f.MinCalls) && s.p99 >= f.Ratio*base && s.p99 >= f.MinP99Ms
		}
		switch {
		case dwell <= 1:
		case len(sp.Active) == 0:
			h = h && earlier
		default:
			h = h && (earlier || pfInList(sp.Active, pp.svc, pp.op))
		}
		// AND cur_p95 >= ?
		if sp.CurP95MinMs > 0 {
			h = h && curP95 >= sp.CurP95MinMs
		}
		// AND NOT (<batch> AND (s, n) NOT IN (muaf) AND her kova calls * base_buckets >= ? * base_calls * ?)
		if g := sp.Batch; g.Cond != "" {
			load := isBatch(pp.svc) && !pfInList(g.Exempt, pp.svc, pp.op)
			for i := 1; i <= dwell; i++ {
				load = load && slot(i).calls*b.buckets >= uint64(g.SurgeFactor)*b.calls*g.CurBuckets
			}
			h = h && !load
		}
		if !h {
			continue
		}
		row := OpP99Row{Service: pp.svc, Operation: pp.op, CurP99Ms: curP99, BaseMs: base,
			CurCalls: curCalls, BaseCalls: b.calls, Earlier: make([]OpP99Slot, dwell-1)}
		for i := 2; i <= dwell; i++ {
			row.Earlier[i-2] = OpP99Slot{P99Ms: slot(i).p99, Calls: slot(i).calls}
		}
		if sp.CurP95MinMs > 0 {
			row.CurP95Ms = curP95
		}
		if sp.Batch.Cond != "" {
			row.BaseBuckets = b.buckets
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := out[i].CurP99Ms/out[i].BaseMs, out[j].CurP99Ms/out[j].BaseMs
		if ri != rj {
			return ri > rj
		}
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Operation < out[j].Operation
	})
	if len(out) > OpP99PivotLimit {
		out = out[:OpP99PivotLimit]
	}
	return out
}

// curHavingTwin — OpP99CurrentQuery HAVING'inin Go ikizi (tabandan bağımsız
// tabanlar; canlı motor testi SQL'in kendisini sınar).
func curHavingTwin(sp OpP99PivotSpec, r OpP99CurRow) bool {
	f := sp.Floors
	ok := int64(r.Slots[0].Calls) >= int64(f.MinCalls) && r.Slots[0].P99Ms >= f.MinP99Ms
	if sp.CurP95MinMs > 0 {
		ok = ok && r.CurP95Ms >= sp.CurP95MinMs
	}
	if len(r.Slots) > 1 {
		e := true
		for _, s := range r.Slots[1:] {
			e = e && int64(s.Calls) >= int64(f.MinCalls) && s.P99Ms >= f.MinP99Ms
		}
		if len(sp.Active) == 0 {
			ok = ok && e
		} else {
			ok = ok && (e || pfInList(sp.Active, r.Service, r.Operation))
		}
	}
	return ok
}

// newPathPivot — yeni yol aynı girdilerle: taban önbelleği (OpBaselineQuery'nin
// HAVING base_calls ≥ MinCalls'ı), tur satırları (yalnız cari penceresinde
// satırı olan çiftler; pushdown = tur HAVING'i uygulanmış), satır sırası
// karıştırılmış, JoinOpP99Pivot.
func newPathPivot(sp OpP99PivotSpec, pairs []pfPair, isBatch func(string) bool, pushdown bool, rng *rand.Rand) []OpP99Row {
	dwell := len(sp.SlotStarts)
	base := map[OpPair]OpBaseline{}
	for _, pp := range pairs {
		if b := pp.base; b != nil && int64(b.calls) >= int64(sp.Floors.MinCalls) {
			base[OpPair{Service: pp.svc, Operation: pp.op}] = OpBaseline{P99Ms: b.p99, P95Ms: b.p95, Calls: b.calls, Buckets: b.buckets}
		}
	}
	var cur []OpP99CurRow
	for _, pp := range pairs {
		r := OpP99CurRow{Service: pp.svc, Operation: pp.op, Slots: make([]OpP99Slot, dwell)}
		has := false
		for i := 0; i < dwell && i < len(pp.slots); i++ {
			if s := pp.slots[i]; s != nil {
				has = true
				r.Slots[i] = OpP99Slot{P99Ms: s.p99, Calls: s.calls}
				if i == 0 {
					r.CurP95Ms = s.p95
				}
			}
		}
		if !has {
			continue
		}
		if sp.Batch.Cond != "" {
			r.IsBatch = isBatch(pp.svc)
		}
		if pushdown && !curHavingTwin(sp, r) {
			continue
		}
		cur = append(cur, r)
	}
	if rng != nil {
		rng.Shuffle(len(cur), func(i, j int) { cur[i], cur[j] = cur[j], cur[i] })
	}
	return JoinOpP99Pivot(sp, cur, base)
}

func rowsText(rows []OpP99Row) string {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%+v\n", r)
	}
	return b.String()
}

var pfNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// pfOpLatSpec — trace_op_latency biçimi (tabanlar 30 / 3× / 200 ms; batch
// kapısı opsiyonel). Kova başları yeniden eskiye, taban 24 sa.
func pfOpLatSpec(dwell int, active []OpPair, batch bool, exempt []OpPair) OpP99PivotSpec {
	slots := make([]time.Time, dwell)
	for i := range slots {
		slots[i] = pfNow.Add(-time.Duration(i+1) * 5 * time.Minute)
	}
	sp := OpP99PivotSpec{
		SlotStarts: slots, BaseStart: slots[dwell-1].Add(-24 * time.Hour), AlignedNow: pfNow,
		Floors: OpP99Floors{MinCalls: 30, Ratio: 3.0, MinP99Ms: 200.0},
		Active: active,
	}
	if batch {
		cond, args := DefaultAnomalySensitivity().BatchServiceSQL("service_name")
		sp.Batch = OpP99BatchGate{Cond: cond, Args: args, Exempt: exempt, SurgeFactor: 2, CurBuckets: 1}
	}
	return sp
}

// pfSvcSlowSpec — yaygın yavaşlama biçimi (ServiceSlowdownOpsSpec: 30 / 20× /
// 5000 ms, p95 tabanı, yavaş pay 2500 ms, batch dışlaması iç WHERE'de).
func pfSvcSlowSpec() OpP99PivotSpec {
	return ServiceSlowdownOpsSpec(pfNow.Add(-5*time.Minute), DefaultServiceSlowdown(), DefaultAnomalySensitivity())
}

func pfIsBatch(svc string) bool { return DefaultAnomalySensitivity().IsBatchService(svc) }

func pfKeys(rows []OpP99Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Service + "/" + r.Operation
	}
	return out
}

// TestJoinOpP99PivotMatchesLegacy — tablo: her vaka için eski SQL referansı ile
// yeni yol (tur HAVING'i uygulanmış VE uygulanmamış) birebir aynı; ayrıca
// beklenen çiftler (anlam pini).
func TestJoinOpP99PivotMatchesLegacy(t *testing.T) {
	ex := []OpPair{{Service: "orders-batch", Operation: "Exempt"}}
	nan := math.NaN()
	tests := []struct {
		name  string
		sp    OpP99PivotSpec
		pairs []pfPair
		want  []string // beklenen çıktı sırası (nil = yalnız eşitlik)
	}{
		{
			name: "dwell 1 — tabanlar ve sınır eşitlikleri",
			sp:   pfOpLatSpec(1, nil, false, nil),
			pairs: []pfPair{
				{svc: "a", op: "fire", base: baseAgg(100, 90), slots: []*pfAgg{agg(400, 40)}},
				{svc: "a", op: "ratio-eq", base: baseAgg(100, 90), slots: []*pfAgg{agg(300, 40)}}, // tam 3× → geçer (>=)
				{svc: "a", op: "ratio-below", base: baseAgg(100, 90), slots: []*pfAgg{agg(299.99, 40)}},
				{svc: "a", op: "abs-below", base: baseAgg(50, 50), slots: []*pfAgg{agg(199.99, 40)}},
				{svc: "a", op: "abs-eq", base: baseAgg(50, 50), slots: []*pfAgg{agg(200, 40)}},
				{svc: "a", op: "cur-thin", base: baseAgg(100, 90), slots: []*pfAgg{agg(900, 29)}},
				{svc: "a", op: "cur-calls-eq", base: baseAgg(100, 90), slots: []*pfAgg{agg(900, 30)}},
				{svc: "a", op: "base-thin", base: &pfAgg{p99: 100, p95: 90, calls: 29, buckets: 3}, slots: []*pfAgg{agg(900, 40)}},
				{svc: "a", op: "base-zero", base: baseAgg(0, 0), slots: []*pfAgg{agg(900, 40)}},
				{svc: "a", op: "base-nan", base: baseAgg(nan, nan), slots: []*pfAgg{agg(900, 40)}},
				{svc: "a", op: "cur-nan", base: baseAgg(100, 90), slots: []*pfAgg{agg(nan, 40)}},
				{svc: "a", op: "no-base", slots: []*pfAgg{agg(900, 40)}},
				{svc: "a", op: "no-cur", base: baseAgg(100, 90)},
			},
			want: []string{"a/cur-calls-eq", "a/abs-eq", "a/fire", "a/ratio-eq"}, // 4× eşitliği: servis/operasyon artan
		},
		{
			name: "dwell 2 — sürdürme (aktif küme yok)",
			sp:   pfOpLatSpec(2, nil, false, nil),
			pairs: []pfPair{
				{svc: "b", op: "sustained", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 40), agg(800, 40)}},
				{svc: "b", op: "single-spike", base: baseAgg(20, 20), slots: []*pfAgg{agg(4000, 40), agg(20, 40)}},
				{svc: "b", op: "thin-prev", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 40), agg(800, 10)}},
				{svc: "b", op: "prev-only", base: baseAgg(20, 20), slots: []*pfAgg{agg(20, 40), agg(800, 40)}},
				{svc: "b", op: "no-prev", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 40), nil}},
				{svc: "b", op: "prev-ratio-eq", base: baseAgg(100, 100), slots: []*pfAgg{agg(800, 40), agg(300, 40)}},
				{svc: "b", op: "prev-abs-below", base: baseAgg(50, 50), slots: []*pfAgg{agg(800, 40), agg(199, 40)}},
			},
			want: []string{"b/sustained", "b/prev-ratio-eq"},
		},
		{
			name: "dwell 2 — aktif-küme histerezisi",
			sp: pfOpLatSpec(2, []OpPair{{Service: "c", Operation: "active-dip"}, {Service: "c", Operation: "active-clean"},
				{Service: "c", Operation: "active-no-prev"}}, false, nil),
			pairs: []pfPair{
				{svc: "c", op: "active-dip", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 40), agg(20, 40)}},
				{svc: "c", op: "active-clean", base: baseAgg(20, 20), slots: []*pfAgg{agg(20, 40), agg(800, 40)}},
				{svc: "c", op: "active-no-prev", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 40), nil}},
				{svc: "c", op: "inactive-dip", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 40), agg(20, 40)}},
				{svc: "c", op: "inactive-sustained", base: baseAgg(20, 20), slots: []*pfAgg{agg(900, 40), agg(900, 40)}},
			},
			want: []string{"c/inactive-sustained", "c/active-dip", "c/active-no-prev"},
		},
		{
			name: "dwell 3 — aktif + eksik orta kova",
			sp:   pfOpLatSpec(3, []OpPair{{Service: "d", Operation: "act"}}, false, nil),
			pairs: []pfPair{
				{svc: "d", op: "all-three", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 40), agg(700, 40), agg(600, 40)}},
				{svc: "d", op: "gap-middle", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 40), nil, agg(600, 40)}},
				{svc: "d", op: "act", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 40), nil, nil}},
			},
			want: []string{"d/act", "d/all-three"},
		},
		{
			name: "dwell 1 — batch yük kapısı + muaf çift",
			sp:   pfOpLatSpec(1, nil, true, ex),
			pairs: []pfPair{
				{svc: "orders-batch", op: "surge", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 500)}},                                     // 500 ≥ 2×100 → susar
				{svc: "orders-batch", op: "surge-eq", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 200)}},                                  // tam 2× → susar (>=)
				{svc: "orders-batch", op: "normal-load", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 199)}},                               // yük yok → açar
				{svc: "orders-batch", op: "Exempt", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 500)}},                                    // muaf → açar
				{svc: "api", op: "surge", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 500)}},                                              // batch değil → açar
				{svc: "nightly-BATCH", op: "drip", base: &pfAgg{p99: 20, p95: 20, calls: 1_276, buckets: 288}, slots: []*pfAgg{agg(800, 40)}}, // aktif kova ort. ~4.4 → 40 ≥ 2× → susar
			},
			want: []string{"api/surge", "orders-batch/Exempt", "orders-batch/normal-load"},
		},
		{
			name: "dwell 2 — batch: her kova yük altında olmalı",
			sp:   pfOpLatSpec(2, nil, true, nil),
			pairs: []pfPair{
				{svc: "orders-batch", op: "surge-both", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 500), agg(800, 500)}},
				{svc: "orders-batch", op: "surge-latest", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 500), agg(800, 100)}},
				{svc: "orders-batch", op: "surge-prev", base: baseAgg(20, 20), slots: []*pfAgg{agg(800, 100), agg(800, 500)}},
			},
			want: []string{"orders-batch/surge-latest", "orders-batch/surge-prev"},
		},
		{
			name: "yaygın yavaşlama — BaseP95 + CurP95MinMs",
			sp:   pfSvcSlowSpec(),
			pairs: []pfPair{
				// dünkü olay p99 tabanını şişirdi, p95 tabanı temiz → 30× p95 → açar
				{svc: "crm", op: "p95-base", base: baseAgg(9000, 200), slots: []*pfAgg{{p99: 6000, p95: 3000, calls: 40}}},
				{svc: "crm", op: "slow-share-low", base: baseAgg(200, 200), slots: []*pfAgg{{p99: 6000, p95: 1000, calls: 40}}},
				{svc: "crm", op: "slow-share-eq", base: baseAgg(200, 200), slots: []*pfAgg{{p99: 6000, p95: 2500, calls: 40}}},
				{svc: "crm", op: "ratio-low", base: baseAgg(400, 400), slots: []*pfAgg{{p99: 6000, p95: 3000, calls: 40}}},
				{svc: "crm", op: "abs-low", base: baseAgg(100, 100), slots: []*pfAgg{{p99: 4999, p95: 3000, calls: 40}}},
			},
			want: []string{"crm/p95-base", "crm/slow-share-eq"},
		},
	}
	rng := rand.New(rand.NewSource(7))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref := refLegacyPivot(tc.sp, tc.pairs, pfIsBatch)
			for _, push := range []bool{false, true} {
				got := newPathPivot(tc.sp, tc.pairs, pfIsBatch, push, rng)
				if rowsText(got) != rowsText(ref) {
					t.Fatalf("pushdown=%v: yeni yol eski SQL'den farklı\nyeni:\n%s\neski:\n%s", push, rowsText(got), rowsText(ref))
				}
			}
			if tc.want != nil && strings.Join(pfKeys(ref), ",") != strings.Join(tc.want, ",") {
				t.Fatalf("beklenen %v, referans %v", tc.want, pfKeys(ref))
			}
		})
	}
}

// TestJoinOpP99PivotTiesAndLimit — eşit oranlar LIMIT sınırında: SQL'in
// tanımsız bıraktığı sıra artık kararlı (servis, operasyon) ve referansla
// aynı; ayrık oranlarda en büyük 200.
func TestJoinOpP99PivotTiesAndLimit(t *testing.T) {
	sp := pfOpLatSpec(1, nil, false, nil)
	var pairs []pfPair
	for i := 0; i < 10; i++ { // en yüksek oran (10×), LIMIT'in başında
		pairs = append(pairs, pfPair{svc: fmt.Sprintf("hot-%02d", i), op: "op", base: baseAgg(100, 100), slots: []*pfAgg{agg(1000, 40)}})
	}
	for i := 249; i >= 0; i-- { // 250 eşit oran (6×), ters sırada girer
		pairs = append(pairs, pfPair{svc: fmt.Sprintf("tie-%03d", i), op: "op", base: baseAgg(100, 100), slots: []*pfAgg{agg(600, 40)}})
	}
	ref := refLegacyPivot(sp, pairs, pfIsBatch)
	got := newPathPivot(sp, pairs, pfIsBatch, true, rand.New(rand.NewSource(1)))
	if rowsText(got) != rowsText(ref) {
		t.Fatalf("eşitlik/kesim farklı")
	}
	if len(got) != OpP99PivotLimit || got[9].Service != "hot-09" || got[10].Service != "tie-000" || got[199].Service != "tie-189" {
		t.Fatalf("kesim: len=%d [9]=%s [10]=%s [199]=%s", len(got), got[9].Service, got[10].Service, got[199].Service)
	}

	pairs = nil
	for i := 0; i < 300; i++ {
		pairs = append(pairs, pfPair{svc: "s", op: fmt.Sprintf("op-%03d", i), base: baseAgg(100, 100), slots: []*pfAgg{agg(300+float64(i), 40)}})
	}
	ref = refLegacyPivot(sp, pairs, pfIsBatch)
	got = newPathPivot(sp, pairs, pfIsBatch, false, rand.New(rand.NewSource(2)))
	if rowsText(got) != rowsText(ref) || len(got) != 200 || got[0].Operation != "op-299" || got[199].Operation != "op-100" {
		t.Fatalf("ayrık oranlarda en büyük 200 değil: len=%d", len(got))
	}
}

// TestJoinOpP99PivotRandomized — özellik: eşik çevresinde ayrık değerlerden
// (sınır eşitlikleri sık) rastgele girdiler, her kipte referansla aynı.
func TestJoinOpP99PivotRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	p99s := []float64{0, 20, 50, 66.66666666666667, 100, 199.99, 200, 299.99, 300, 600, 999, 1000, 2500, 4999, 5000, 6000, math.NaN()}
	calls := []uint64{0, 10, 29, 30, 31, 100, 199, 200, 500}
	pick := func(xs []float64) float64 { return xs[rng.Intn(len(xs))] }
	pickC := func() uint64 { return calls[rng.Intn(len(calls))] }
	svcs := []string{"api", "web", "orders-batch", "nightly-batch", "crm"}
	for iter := 0; iter < 400; iter++ {
		dwell := 1 + rng.Intn(3)
		var sp OpP99PivotSpec
		if rng.Intn(4) == 0 {
			sp = pfSvcSlowSpec()
			sp.CurP95MinMs = []float64{0, 2500}[rng.Intn(2)]
			sp.BaseP95 = rng.Intn(2) == 0
		} else {
			sp = pfOpLatSpec(dwell, nil, rng.Intn(2) == 0, nil)
		}
		dwell = len(sp.SlotStarts)
		n := rng.Intn(260)
		pairs := make([]pfPair, 0, n)
		for i := 0; i < n; i++ {
			pp := pfPair{svc: svcs[rng.Intn(len(svcs))], op: fmt.Sprintf("op-%d", i)}
			if rng.Intn(6) != 0 {
				bb := uint64(1 + rng.Intn(288))
				pp.base = &pfAgg{p99: pick(p99s), p95: pick(p99s), calls: []uint64{0, 29, 30, 1_276, 28_800}[rng.Intn(5)], buckets: bb}
			}
			for s := 0; s < dwell; s++ {
				if rng.Intn(5) == 0 {
					pp.slots = append(pp.slots, nil)
					continue
				}
				pp.slots = append(pp.slots, &pfAgg{p99: pick(p99s), p95: pick(p99s), calls: pickC()})
			}
			pairs = append(pairs, pp)
			if rng.Intn(5) == 0 && dwell > 1 {
				sp.Active = append(sp.Active, OpPair{Service: pp.svc, Operation: pp.op})
			}
			if sp.Batch.Cond != "" && rng.Intn(8) == 0 {
				sp.Batch.Exempt = append(sp.Batch.Exempt, OpPair{Service: pp.svc, Operation: pp.op})
			}
		}
		ref := refLegacyPivot(sp, pairs, pfIsBatch)
		for _, push := range []bool{false, true} {
			if got := newPathPivot(sp, pairs, pfIsBatch, push, rng); rowsText(got) != rowsText(ref) {
				t.Fatalf("iter %d (dwell %d, pushdown %v): farklı\nyeni:\n%s\neski:\n%s", iter, dwell, push, rowsText(got), rowsText(ref))
			}
		}
	}
}

// ── Sorgu pinleri ────────────────────────────────────────────────────────────

const goldenOpP99CurrentDwell2SQL = `
		SELECT service_name, name,
		       maxIf(p99, slot = 1)   AS cur_p99,
		       sumIf(calls, slot = 1) AS cur_calls,
		       maxIf(p95, slot = 1)   AS cur_p95,
		       maxIf(p99, slot = 2)   AS p99_2,
		       sumIf(calls, slot = 2) AS calls_2
		FROM (
		  SELECT service_name, name,
		         multiIf(time_bucket >= ?, 1, time_bucket >= ?, 2, 0) AS slot,
		         quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state) AS q,
		         arrayElement(q, 3) / 1e6 AS p99,
		         arrayElement(q, 2) / 1e6 AS p95,
		         countMerge(span_count_state) AS calls
		  FROM operation_summary_5m
		  WHERE time_bucket >= ? AND time_bucket < ?
		  GROUP BY service_name, name, slot
		)
		GROUP BY service_name, name
		HAVING cur_calls >= ? AND cur_p99 >= ?
		   AND calls_2 >= ? AND p99_2 >= ?
		LIMIT 50000
		SETTINGS max_execution_time = 25`

const goldenOpBaselineSQL = `
		SELECT service_name, name,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state) AS q, 3) / 1e6 AS base_p99,
		       arrayElement(q, 2) / 1e6     AS base_p95,
		       countMerge(span_count_state) AS base_calls,
		       uniqExact(time_bucket)       AS base_buckets
		FROM operation_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ?
		GROUP BY service_name, name
		HAVING base_calls >= ?
		LIMIT 500000
		SETTINGS max_execution_time = 60`

func TestOpP99CachedQueriesGolden(t *testing.T) {
	sp := pfOpLatSpec(2, nil, false, nil)
	q, args := OpP99CurrentQuery(sp)
	if q != goldenOpP99CurrentDwell2SQL {
		t.Fatalf("tur sorgusu metni değişti:\n%s", q)
	}
	want := []any{sp.SlotStarts[0], sp.SlotStarts[1], sp.SlotStarts[1], sp.AlignedNow, 30, 200.0, 30, 200.0}
	if fmt.Sprintf("%#v", args) != fmt.Sprintf("%#v", want) {
		t.Fatalf("tur argümanları\n%#v\nbeklenen\n%#v", args, want)
	}
	ss := pfSvcSlowSpec()
	end := ss.SlotStarts[0]
	q, args = OpBaselineQuery(end.Add(-24*time.Hour), end, ss.Floors.MinCalls)
	if q != goldenOpBaselineSQL {
		t.Fatalf("taban sorgusu metni değişti:\n%s", q)
	}
	want = []any{end.Add(-24 * time.Hour), end, 30}
	if fmt.Sprintf("%#v", args) != fmt.Sprintf("%#v", want) {
		t.Fatalf("taban argümanları\n%#v\nbeklenen\n%#v", args, want)
	}
}

// TestOpP99CachedQueriesBounded — iki yeni sorgu her biçimde SINIRLI: tek FROM
// operation_summary_5m, zaman aralıklı WHERE, LIMIT, max_execution_time;
// yer tutucu = argüman. Tur sorgusu yalnız cari pencereyi okur (iç WHERE başı
// = en eski cari kova, taban penceresi değil) ve taban kolonu taşımaz.
func TestOpP99CachedQueriesBounded(t *testing.T) {
	bounded := func(t *testing.T, name, q string, nArgs int, limit string) {
		t.Helper()
		for _, frag := range []string{"WHERE time_bucket >= ? AND time_bucket < ?", "\n\t\tLIMIT " + limit + "\n", "SETTINGS max_execution_time = "} {
			if strings.Count(q, frag) != 1 {
				t.Fatalf("%s: %q bir kez geçmeli:\n%s", name, frag, q)
			}
		}
		if strings.Count(q, "FROM operation_summary_5m") != 1 || strings.Contains(q, "FROM spans") {
			t.Fatalf("%s: tek MV geçişi değil:\n%s", name, q)
		}
		if strings.Count(q, "?") != nArgs {
			t.Fatalf("%s: yer tutucu %d, argüman %d", name, strings.Count(q, "?"), nArgs)
		}
	}
	act := []OpPair{{Service: "a", Operation: "x"}, {Service: "b", Operation: "y"}}
	for dwell := 1; dwell <= 6; dwell++ {
		for _, sp := range []OpP99PivotSpec{
			pfOpLatSpec(dwell, nil, false, nil),
			pfOpLatSpec(dwell, act, true, act[:1]),
		} {
			name := fmt.Sprintf("dwell %d batch=%v", dwell, sp.Batch.Cond != "")
			q, args := OpP99CurrentQuery(sp)
			bounded(t, name, q, len(args), "50000")
			if !strings.HasSuffix(q, "SETTINGS max_execution_time = 25") {
				t.Fatalf("%s: tur sorgusu süre sınırı eski sorguyla aynı olmalı", name)
			}
			if strings.Contains(q, "base_") || strings.Contains(q, "slot = 0") {
				t.Fatalf("%s: tur sorgusu taban kolonu taşımamalı:\n%s", name, q)
			}
			// iç WHERE başı (multiIf argümanlarından sonra) = en eski cari kova.
			off := 0
			if sp.Batch.Cond != "" {
				off = len(sp.Batch.Args)
			}
			if args[off+dwell] != sp.SlotStarts[dwell-1] || args[off+dwell+1] != sp.AlignedNow {
				t.Fatalf("%s: iç WHERE cari pencere değil: %v", name, args[off+dwell:off+dwell+2])
			}
			if got := strings.Count(q, "AS p99_"); got != dwell-1 {
				t.Fatalf("%s: %d önceki kova kolonu", name, got)
			}
		}
	}
	sp := pfSvcSlowSpec()
	q, args := OpP99CurrentQuery(sp)
	bounded(t, "svc-slowdown tur", q, len(args), "50000")
	if !strings.HasSuffix(q, "SETTINGS max_execution_time = 10") || !strings.Contains(q, "AND NOT (positionCaseInsensitive(service_name, ?) > 0)") ||
		!strings.Contains(q, "AND cur_p95 >= ?") {
		t.Fatalf("svc-slowdown tur sorgusu: süre 10 s, dışlama ve yavaş pay tabanı olmalı:\n%s", q)
	}
	q, args = OpBaselineQuery(pfNow.Add(-24*time.Hour), pfNow, 30)
	bounded(t, "taban", q, len(args), "500000")
	if !strings.HasSuffix(q, "SETTINGS max_execution_time = 60") || strings.Contains(q, "NOT ") {
		t.Fatalf("op_latency tabanı: 60 s sınır, dışlama yok:\n%s", q)
	}
}

// TestOpP99PivotScanDestOrder — tarayıcılar sorgu kolonlarıyla aynı sayıda
// hedef üretir (eski tek geçiş + tur sorgusu).
func TestOpP99PivotScanDestOrder(t *testing.T) {
	cols := func(q string) int {
		sel := q[:strings.Index(q, "\n\t\tFROM (")]
		return strings.Count(sel, " AS ") + 2 // service_name, name
	}
	for _, sp := range []OpP99PivotSpec{pfOpLatSpec(1, nil, false, nil), pfOpLatSpec(3, nil, true, nil), pfSvcSlowSpec()} {
		var r OpP99Row
		q, _ := OpP99PivotQuery(sp)
		if n := len(OpP99PivotScanDest(&r, sp)); n != cols(q) {
			t.Fatalf("eski tek geçiş: %d hedef, %d kolon", n, cols(q))
		}
		var c OpP99CurRow
		var ib uint8
		q, _ = OpP99CurrentQuery(sp)
		if n := len(OpP99CurrentScanDest(&c, sp, &ib)); n != cols(q) {
			t.Fatalf("tur sorgusu: %d hedef, %d kolon", n, cols(q))
		}
	}
}

// ── Canlı motor: clickhouse local ────────────────────────────────────────────

// TestOpP99CachedAgreesOnClickHouse — GERÇEK üç sorgu metni (argümanlar
// gömülü) sentetik operation_summary_5m üstünde: eski tek geçişin satırları ==
// taban sorgusu [ilk cari kova − 24 sa, ilk cari kova) + tur sorgusu +
// JoinOpP99Pivot. Taban penceresi eski sorgununkiyle aynı (gecikme 0) → birebir.
func TestOpP99CachedAgreesOnClickHouse(t *testing.T) {
	bin, err := exec.LookPath("clickhouse")
	if err != nil {
		t.Skip("clickhouse ikilisi yok — canlı motor eşdeğerliği atlandı (saf eşdeğerlik testleri koşar)")
	}
	ts := func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }
	type fx struct {
		svc, op             string
		baseMs              int64 // 288 kova × 100 çağrı
		baseCalls           int   // kova başına
		curMs, prevMs       int64
		curCalls, prevCalls int
	}
	cases := []fx{
		{"svc-a", "sustained", 20, 100, 800, 800, 40, 40},
		{"svc-b", "single-spike", 20, 100, 4_000, 20, 40, 40},
		{"svc-c", "thin-prev", 20, 100, 800, 800, 40, 10},
		{"svc-d", "prev-only", 20, 100, 20, 800, 40, 40},
		{"svc-e", "no-prev-data", 20, 100, 800, 0, 40, 0},
		{"orders-batch", "surge-both", 20, 100, 800, 800, 500, 500},
		{"orders-batch", "surge-latest", 20, 100, 800, 800, 500, 100},
		{"orders-batch", "Active", 20, 100, 800, 800, 500, 500},
		{"svc-f", "active-dip", 20, 100, 800, 20, 40, 40},
		{"svc-g", "active-clean", 20, 100, 20, 800, 40, 40},
		{"svc-h", "ratio-eq", 100, 100, 300, 300, 40, 40},
		{"crm", "slow-a", 200, 100, 6_000, 6_000, 40, 40},
		{"crm", "slow-b", 200, 100, 7_000, 7_000, 40, 40},
		{"svc-i", "thin-base", 20, 0, 800, 800, 40, 10},
		{"svc-j", "no-cur", 20, 100, 0, 800, 0, 40},
	}
	var fixture strings.Builder
	fixture.WriteString(`CREATE TABLE operation_summary_5m (service_name String, name String, time_bucket DateTime('UTC'),
		span_count_state AggregateFunction(count), duration_q_state AggregateFunction(quantilesTDigest(0.5, 0.95, 0.99), UInt64))
		ENGINE = AggregatingMergeTree PARTITION BY toDate(time_bucket) ORDER BY (service_name, name, time_bucket);` + "\n")
	ins := func(svc, op, bucket string, n int, ms int64) {
		fmt.Fprintf(&fixture, "INSERT INTO operation_summary_5m SELECT '%s' AS s, '%s' AS o, %s AS tb, countState(), quantilesTDigestState(0.5, 0.95, 0.99)(toUInt64(%d)) FROM numbers(%d) GROUP BY s, o, tb;\n",
			svc, op, bucket, ms*1_000_000, n)
	}
	for _, sp := range []OpP99PivotSpec{pfOpLatSpec(2, nil, false, nil)} {
		slots, base := sp.SlotStarts, sp.BaseStart
		for _, c := range cases {
			if c.baseCalls > 0 {
				ins(c.svc, c.op, fmt.Sprintf("toDateTime('%s', 'UTC') + toIntervalMinute(5 * intDiv(number, %d))", ts(base), c.baseCalls), 288*c.baseCalls, c.baseMs)
			}
			if c.curCalls > 0 {
				ins(c.svc, c.op, fmt.Sprintf("toDateTime('%s', 'UTC')", ts(slots[0])), c.curCalls, c.curMs)
			}
			if c.prevCalls > 0 {
				ins(c.svc, c.op, fmt.Sprintf("toDateTime('%s', 'UTC')", ts(slots[1])), c.prevCalls, c.prevMs)
			}
		}
		// Taban penceresinin DIŞI (24 sa'ten eski) — iki yol da okumamalı.
		ins("svc-a", "sustained", fmt.Sprintf("toDateTime('%s', 'UTC')", ts(base.Add(-5*time.Minute))), 1000, 90_000)
	}

	dir := t.TempDir()
	run := func(q string, args []any) [][]string {
		t.Helper()
		sel := inlineDBHealthArgs(t, q, args)
		i := strings.LastIndex(sel, "\n\t\tSETTINGS ")
		sel = sel[:i] + "\n\t\tFORMAT TSVRaw" + sel[i:] + ";\n"
		f, err := os.CreateTemp(dir, "q-*.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(fixture.String() + sel); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		out, err := exec.Command(bin, "local", "--multiquery", "--queries-file", f.Name()).CombinedOutput()
		if err != nil {
			t.Fatalf("clickhouse local: %v\n%s\n%s", err, out, sel)
		}
		var rows [][]string
		for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if ln != "" {
				rows = append(rows, strings.Split(ln, "\t"))
			}
		}
		return rows
	}
	fill := func(t *testing.T, cols []string, dest []any) {
		t.Helper()
		if len(cols) != len(dest) {
			t.Fatalf("kolon %d, hedef %d: %v", len(cols), len(dest), cols)
		}
		for i, d := range dest {
			var err error
			switch p := d.(type) {
			case *string:
				*p = cols[i]
			case *float64:
				*p, err = strconv.ParseFloat(cols[i], 64)
			case *uint64:
				*p, err = strconv.ParseUint(cols[i], 10, 64)
			case *uint8:
				var v uint64
				v, err = strconv.ParseUint(cols[i], 10, 8)
				*p = uint8(v)
			}
			if err != nil {
				t.Fatalf("kolon %d %q: %v", i, cols[i], err)
			}
		}
	}

	active := []OpPair{{Service: "orders-batch", Operation: "Active"}, {Service: "svc-f", Operation: "active-dip"}, {Service: "svc-g", Operation: "active-clean"}}
	svcSpec := pfOpLatSpec(1, nil, false, nil)
	svcSpec.Floors = OpP99Floors{MinCalls: 30, Ratio: 20, MinP99Ms: 5000}
	svcSpec.CurP95MinMs, svcSpec.BaseP95, svcSpec.TimeoutSec = 2500, true, 10
	cond, bargs := DefaultAnomalySensitivity().BatchServiceSQL("service_name")
	svcSpec.ExcludeCond, svcSpec.ExcludeArgs = cond, bargs
	specs := map[string]OpP99PivotSpec{
		"op_latency dwell 1":                   pfOpLatSpec(1, nil, false, nil),
		"op_latency dwell 1 batch":             pfOpLatSpec(1, nil, true, active[:1]),
		"op_latency dwell 2":                   pfOpLatSpec(2, nil, false, nil),
		"op_latency dwell 2 aktif + batch":     pfOpLatSpec(2, active, true, active[:1]),
		"svc-slowdown (p95, yavaş pay, dışla)": svcSpec,
	}
	// dwell 1 kipleri en yeni kovayı slots[0]'dan okur: aynı fikstür (pfNow).
	for name, sp := range specs {
		t.Run(name, func(t *testing.T) {
			q, args := OpP99PivotQuery(sp)
			var legacy []OpP99Row
			for _, cols := range run(q, args) {
				var r OpP99Row
				fill(t, cols, OpP99PivotScanDest(&r, sp))
				legacy = append(legacy, r)
			}
			end := sp.SlotStarts[len(sp.SlotStarts)-1]
			bq, bargs := OpBaselineQuery(end.Add(-end.Sub(sp.BaseStart)), end, sp.Floors.MinCalls) // dışlamasız (paylaşılan taban)
			base := map[OpPair]OpBaseline{}
			for _, cols := range run(bq, bargs) {
				var p OpPair
				var b OpBaseline
				fill(t, cols, []any{&p.Service, &p.Operation, &b.P99Ms, &b.P95Ms, &b.Calls, &b.Buckets})
				base[p] = b
			}
			cq, cargs := OpP99CurrentQuery(sp)
			var cur []OpP99CurRow
			for _, cols := range run(cq, cargs) {
				var r OpP99CurRow
				var ib uint8
				fill(t, cols, OpP99CurrentScanDest(&r, sp, &ib))
				r.IsBatch = ib != 0
				cur = append(cur, r)
			}
			got := JoinOpP99Pivot(sp, cur, base)
			// SQL eşitlikte sırayı tanımlamaz → eski satırları da aynı kurala diz.
			sort.SliceStable(legacy, func(i, j int) bool {
				ri, rj := legacy[i].CurP99Ms/legacy[i].BaseMs, legacy[j].CurP99Ms/legacy[j].BaseMs
				if ri != rj {
					return ri > rj
				}
				if legacy[i].Service != legacy[j].Service {
					return legacy[i].Service < legacy[j].Service
				}
				return legacy[i].Operation < legacy[j].Operation
			})
			if len(legacy) == 0 {
				t.Fatal("fikstür eski yolda hiç satır üretmedi — vaka anlamsız")
			}
			if rowsText(got) != rowsText(legacy) {
				t.Fatalf("önbellekli yol eski tek geçişten farklı\nyeni:\n%s\neski:\n%s", rowsText(got), rowsText(legacy))
			}
			if _, ok := base[OpPair{Service: "svc-i", Operation: "thin-base"}]; ok {
				t.Fatal("taban HAVING'i (base_calls ≥ MinCalls) uygulanmadı")
			}
			// Paylaşılan taban dışlamasız: svc-slowdown vakasında batch çiftleri
			// tabanda VAR ama eski (dışlamalı) yolla aynı satırlar çıktı.
			if _, ok := base[OpPair{Service: "orders-batch", Operation: "surge-both"}]; !ok {
				t.Fatal("paylaşılan taban batch çiftlerini de taşımalı (dışlama yalnız tur sorgusunda)")
			}
		})
	}
}
