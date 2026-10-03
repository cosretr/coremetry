package anomaly

// op_latency_dwell_test.go — v0.10.1085: trace_op_latency sürdürme kuralı.
//
// Operatör (prod): dedektör açıkken ~30 çağrılı bir operasyonda TEK yavaş
// istek (4 sn'lik bir Kafka publish) o 5 dk kovanın p99'u oluyor, "168×"
// anomali açılıyor ve sonraki kovada 1×'e iniyordu (1056 bu yüzden kapattı).
// Sürdürme önerisine onay: "Önerini yapalım".
//
// KURAL: (servis, operasyon) ancak ardışık `dwell` (vars. 2) TAMAMLANMIŞ
// kovanın HER BİRİNDE ihlal ederse olay — her kova ≥ 30 çağrı, p99 ≥ 200 ms,
// p99 ≥ 3 × taban. Olay alanları en yeni kovadan (şekil değişmedi). dwell = 1
// = eski tek-kova kararı.
//
// NE ÇİVİLİYOR:
//   - saf sınıflayıcı tablosu (tek kova → yok, iki → var, önceki kova taban
//     altı → yok, dwell 1 → eski davranış, batch servis yük altında → yok);
//   - özellik: dwell ≥ 2 SAF SUSTURMA (dwell 1 kümesinin alt kümesi, alanlar
//     birebir) ve "her kova tek başına ihlal" ile eşdeğer;
//   - SQL: dwell 2 golden, her dwell'de tek sınırlı MV geçişi, batch kolu;
//   - pencereler: dwell 1 eskiyle birebir, taban sürdürme penceresinden önce;
//   - clickhouse local (ikili yoksa atlanır): gerçek sorgu metni sentetik
//     AggregatingMergeTree üstünde Go kemeriyle aynı çiftleri döndürür;
//   - kablo: dedektör dwell'i anahtarla aynı atomic okumadan alır.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// dwellRow — taban: 24 sa, kova başına 100 çağrı (288 aktif kova), p99 base.
func dwellRow(svc, op string, base, cur float64, curCalls uint64, prev ...opLatencySlot) opLatencyBucket {
	return opLatencyBucket{Service: svc, Operation: op, CurP99Ms: cur, BaseP99Ms: base,
		CurCalls: curCalls, BaseCalls: 100 * 288, BaseBuckets: 288, Earlier: prev}
}

func slotOf(p99 float64, calls uint64) opLatencySlot { return opLatencySlot{P99Ms: p99, Calls: calls} }

func activeRow(r opLatencyBucket) opLatencyBucket { r.Active = true; return r }

func TestClassifyOpLatencyDwell(t *testing.T) {
	batchOn := planOpLatBatch(chstore.AnomalySensitivityConfig{}, 1, nil, nil).gate // vars. ["-batch"]
	batchActive := planOpLatBatch(chstore.AnomalySensitivityConfig{}, 1, opLatKeys("orders-batch", "Active"), nil).gate
	tests := []struct {
		name  string
		dwell int
		gate  opLatBatchGate
		in    opLatencyBucket
		want  bool
	}{
		// ── Operatör vakası: tek yavaş istek ──
		{"tek 4 sn'lik publish en yeni kovada, önceki kova olağan → YOK", 2, opLatBatchGate{},
			dwellRow("svc-a", "kafka.publish", 24, 4_000, 30, slotOf(24, 31)), false},
		{"sonraki tik: sıçrama önceki kovada, en yeni 1× → YOK", 2, opLatBatchGate{},
			dwellRow("svc-a", "kafka.publish", 24, 24, 31, slotOf(4_000, 30)), false},
		// ── Sürdürme ──
		{"iki ardışık kova ihlal → olay", 2, opLatBatchGate{},
			dwellRow("svc-a", "GET /x", 20, 800, 40, slotOf(700, 40)), true},
		{"önceki kova çağrı tabanının altında (29) → YOK", 2, opLatBatchGate{},
			dwellRow("svc-a", "GET /x", 20, 800, 40, slotOf(800, 29)), false},
		{"önceki kova tam çağrı tabanında (30) → olay", 2, opLatBatchGate{},
			dwellRow("svc-a", "GET /x", 20, 800, 40, slotOf(800, 30)), true},
		{"önceki kova 200 ms altında (9× ama 180 ms) → YOK", 2, opLatBatchGate{},
			dwellRow("svc-a", "GET /x", 20, 800, 40, slotOf(180, 40)), false},
		{"önceki kova 3× altında (2.9×) → YOK", 2, opLatBatchGate{},
			dwellRow("svc-a", "GET /x", 100, 900, 40, slotOf(290, 40)), false},
		{"önceki kova verisiz (sıfır değer) → YOK", 2, opLatBatchGate{},
			dwellRow("svc-a", "GET /x", 20, 800, 40, slotOf(0, 0)), false},
		{"önceki kova hiç taşınmadı → YOK (bilinmiyor = ihlal değil)", 2, opLatBatchGate{},
			dwellRow("svc-a", "GET /x", 20, 800, 40), false},
		// ── dwell = 1: eski tek-kova davranışı ──
		{"dwell 1: tek kova ihlali (önceki olağan) → olay", 1, opLatBatchGate{},
			dwellRow("svc-a", "kafka.publish", 24, 4_000, 30, slotOf(24, 31)), true},
		{"dwell 1: önceki kova taşınmasa da → olay", 1, opLatBatchGate{},
			dwellRow("svc-a", "kafka.publish", 24, 4_000, 30), true},
		{"dwell 0 (geçersiz) → 1 gibi", 0, opLatBatchGate{},
			dwellRow("svc-a", "kafka.publish", 24, 4_000, 30), true},
		// ── dwell = 3 ──
		{"dwell 3: üç kovadan ikisi ihlal → YOK", 3, opLatBatchGate{},
			dwellRow("svc-a", "GET /x", 20, 800, 40, slotOf(800, 40), slotOf(20, 40)), false},
		{"dwell 3: üçü ihlal → olay", 3, opLatBatchGate{},
			dwellRow("svc-a", "GET /x", 20, 800, 40, slotOf(800, 40), slotOf(600, 40)), true},
		// ── Batch kapısı (v0.10.1046), her kova yük altındaysa susturur ──
		{"batch: iki kova da yük altında (×5) → YOK", 2, batchOn,
			dwellRow("orders-batch", "Chunk", 20, 800, 500, slotOf(800, 500)), false},
		{"batch: yalnız en yeni kova yük altında → olay", 2, batchOn,
			dwellRow("orders-batch", "Chunk", 20, 800, 500, slotOf(800, 100)), true},
		{"batch: yalnız önceki kova yük altında → olay", 2, batchOn,
			dwellRow("orders-batch", "Chunk", 20, 800, 100, slotOf(800, 500)), true},
		{"batch olmayan servis, iki kova yük altında → olay", 2, batchOn,
			dwellRow("payments-api", "Chunk", 20, 800, 500, slotOf(800, 500)), true},
		{"batch, olayı aktif çift → muaf, olay", 2, batchActive,
			dwellRow("orders-batch", "Active", 20, 800, 500, slotOf(800, 500)), true},
		{"batch: tek kova ihlali yük yokken de → YOK (sürdürme önce)", 2, batchOn,
			dwellRow("orders-batch", "Chunk", 20, 800, 100, slotOf(20, 100)), false},
		// ── Histerezis: sürdürme yalnız AÇILIŞA ──
		{"aktif + en yeni ihlal + önceki temiz → tazele", 2, opLatBatchGate{},
			activeRow(dwellRow("svc-a", "GET /x", 20, 800, 40, slotOf(20, 40))), true},
		{"aktif + en yeni ihlal + önceki verisiz → tazele", 2, opLatBatchGate{},
			activeRow(dwellRow("svc-a", "GET /x", 20, 800, 40)), true},
		{"aktif + en yeni temiz (önceki ihlal) → yazım YOK", 2, opLatBatchGate{},
			activeRow(dwellRow("svc-a", "GET /x", 20, 20, 40, slotOf(800, 40))), false},
		{"aktif + en yeni çağrı tabanı altı → yazım YOK", 2, opLatBatchGate{},
			activeRow(dwellRow("svc-a", "GET /x", 20, 800, 29, slotOf(800, 40))), false},
		{"aktif değil + yalnız en yeni ihlal → açılış YOK (sürdürme şart)", 2, opLatBatchGate{},
			dwellRow("svc-a", "GET /x", 20, 800, 40, slotOf(20, 40)), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyOpLatency([]opLatencyBucket{tt.in}, tt.dwell, tt.gate)
			if tt.want != (len(got) == 1) {
				t.Fatalf("olay=%v bekleniyordu, got=%+v", tt.want, got)
			}
			if !tt.want {
				return
			}
			// Olay şekli değişmedi: alanlar en yeni kovadan, tek-kova kararıyla birebir.
			ref := classifyOpLatency([]opLatencyBucket{tt.in}, 1, opLatBatchGate{})
			if !reflect.DeepEqual(got, ref) {
				t.Fatalf("raporlanan alanlar değişti:\n%+v\n%+v", got, ref)
			}
			if got[0].CurP99Ms != tt.in.CurP99Ms || got[0].CurCalls != tt.in.CurCalls {
				t.Fatalf("olay en yeni kovayı raporlamalı: %+v", got[0])
			}
		})
	}
}

// TestOpLatencyDwellIsPureSuppression — ÖZELLİK TABLOSU: dwell 2 kümesi dwell 1
// kümesinin alt kümesi (alanlar birebir) ve bir çift ancak İKİ kovası da tek
// başına (dwell 1 kararıyla) ihlal ediyorsa kalır.
func TestOpLatencyDwellIsPureSuppression(t *testing.T) {
	var all []opLatencyBucket
	i := 0
	for _, base := range []float64{0, 20, 100} {
		for _, cp := range []float64{150, 250, 400, 4_000} {
			for _, pp := range []float64{0, 150, 250, 299, 400, 4_000} {
				for _, cc := range []uint64{29, 30, 500} {
					for _, pc := range []uint64{0, 29, 30, 500} {
						i++
						all = append(all, dwellRow("svc-a", fmt.Sprintf("op-%04d", i), base, cp, cc, slotOf(pp, pc)))
					}
				}
			}
		}
	}
	fired1, fired2 := 0, 0
	for _, r := range all {
		one := classifyOpLatency([]opLatencyBucket{r}, 1, opLatBatchGate{})
		two := classifyOpLatency([]opLatencyBucket{r}, 2, opLatBatchGate{})
		prevAsCur := r
		prevAsCur.CurP99Ms, prevAsCur.CurCalls, prevAsCur.Earlier = r.Earlier[0].P99Ms, r.Earlier[0].Calls, nil
		prevAlone := classifyOpLatency([]opLatencyBucket{prevAsCur}, 1, opLatBatchGate{})
		if len(two) > len(one) {
			t.Fatalf("sürdürme YENİ olay üretti: %+v", r)
		}
		if len(two) == 1 && !reflect.DeepEqual(two, one) {
			t.Fatalf("kalan olay alanları değişti:\n%+v\n%+v", one, two)
		}
		if want := len(one) == 1 && len(prevAlone) == 1; want != (len(two) == 1) {
			t.Fatalf("dwell 2 = iki kova da tek başına ihlal olmalı: %+v (cur %v, önceki %v, dwell2 %v)", r, len(one), len(prevAlone), len(two))
		}
		fired1 += len(one)
		fired2 += len(two)
	}
	if fired2 == 0 || fired2 >= fired1 {
		t.Fatalf("ızgara anlamsız: dwell1 %d olay, dwell2 %d olay", fired1, fired2)
	}
}

// goldenDwell2OpLatencySQL — dwell 2, batch kuralı kapalı: tek MV geçişi,
// is_cur → kova numarası, önceki kova için iki kolon + üç taban.
const goldenDwell2OpLatencySQL = `
		SELECT service_name, name,
		       maxIf(p99, slot = 1)   AS cur_p99,
		       maxIf(p99, slot = 0)   AS base_p99,
		       sumIf(calls, slot = 1) AS cur_calls,
		       sumIf(calls, slot = 0) AS base_calls,
		       maxIf(p99, slot = 2)   AS p99_2,
		       sumIf(calls, slot = 2) AS calls_2
		FROM (
		  SELECT service_name, name,
		         multiIf(time_bucket >= ?, 1, time_bucket >= ?, 2, 0) AS slot,
		         arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p99,
		         countMerge(span_count_state) AS calls
		  FROM operation_summary_5m
		  WHERE time_bucket >= ? AND time_bucket < ?
		  GROUP BY service_name, name, slot
		)
		GROUP BY service_name, name
		HAVING cur_calls >= ? AND base_calls >= ?
		   AND base_p99 > 0 AND cur_p99 >= ? * base_p99 AND cur_p99 >= ?
		   AND calls_2 >= ? AND p99_2 >= ? * base_p99 AND p99_2 >= ?
		ORDER BY cur_p99 / base_p99 DESC
		LIMIT 200
		SETTINGS max_execution_time = 25`

func TestOpLatencyQueryDwellGolden(t *testing.T) {
	slots, base, now, _ := opLatencyWindows(time.Date(2026, 10, 2, 12, 2, 30, 0, time.UTC), 5*time.Minute, 2)
	q, args := opLatencyQuery(slots, base, now, opLatBatchPlan{}, nil)
	if q != goldenDwell2OpLatencySQL {
		t.Fatalf("dwell 2 SQL metni değişti:\n%s", q)
	}
	want := []any{slots[0], slots[1], base, now,
		opLatencyMinCalls, opLatencyMinCalls, opLatencyMinRatio, opLatencyMinP99Ms,
		opLatencyMinCalls, opLatencyMinRatio, opLatencyMinP99Ms}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("argümanlar\n%v\nbeklenen\n%v", args, want)
	}
}

// TestOpLatencyQueryDwellBounded — her dwell'de (1–6) TEK sınırlı MV geçişi:
// tek FROM operation_summary_5m, zaman sınırlı WHERE, LIMIT 200,
// max_execution_time; çift başına döngü yok, yer tutucu = argüman.
func TestOpLatencyQueryDwellBounded(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	sens := chstore.AnomalySensitivityConfig{}
	for dwell := 1; dwell <= 6; dwell++ {
		for name, plan := range map[string]opLatBatchPlan{
			"kapı yok":   {},
			"batch kolu": planOpLatBatch(sens, 1, opLatKeys("a-batch", "op1"), nil),
		} {
			slots, base, aligned, _ := opLatencyWindows(now, 5*time.Minute, dwell)
			q, args := opLatencyQuery(slots, base, aligned, plan, nil)
			for _, frag := range []string{"WHERE time_bucket >= ? AND time_bucket < ?", "\n\t\tLIMIT 200\n", "SETTINGS max_execution_time = 25"} {
				if strings.Count(q, frag) != 1 {
					t.Fatalf("dwell %d %s: %q bir kez geçmeli:\n%s", dwell, name, frag, q)
				}
			}
			if strings.Count(q, "FROM ") != 2 || strings.Count(q, "FROM operation_summary_5m") != 1 {
				t.Fatalf("dwell %d %s: ek tarama/alt sorgu açıldı:\n%s", dwell, name, q)
			}
			if strings.Count(q, "?") != len(args) {
				t.Fatalf("dwell %d %s: yer tutucu %d, argüman %d", dwell, name, strings.Count(q, "?"), len(args))
			}
			if got := strings.Count(q, "AS p99_"); got != dwell-1 {
				t.Fatalf("dwell %d %s: %d önceki kova kolonu", dwell, name, got)
			}
			group := "GROUP BY service_name, name, slot"
			if dwell == 1 {
				group = "GROUP BY service_name, name, is_cur"
			}
			if !strings.Contains(q, group) {
				t.Fatalf("dwell %d %s: kova gruplaması %q yok", dwell, name, group)
			}
			if plan.cond != "" && strings.Count(q, "* base_buckets >= ? * base_calls * ?") != dwell {
				t.Fatalf("dwell %d: batch kolu her kovayı sormalı:\n%s", dwell, q)
			}
		}
	}
}

// TestOpLatencyQueryDwellBatch — dwell 2 batch kolu: susturma YALNIZ iki kova da
// yük altındaysa (AND), argüman sırası metinle aynı.
func TestOpLatencyQueryDwellBatch(t *testing.T) {
	slots, base, now, _ := opLatencyWindows(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), 5*time.Minute, 2)
	sens := chstore.AnomalySensitivityConfig{}
	cond, _ := sens.BatchServiceSQL("service_name")
	plan := planOpLatBatch(sens, 1, opLatKeys("a-batch", "op1"), nil)
	q, args := opLatencyQuery(slots, base, now, plan, nil)
	for _, frag := range []string{
		"maxIf(buckets, slot = 0) AS base_buckets",
		"uniqExact(time_bucket) AS buckets",
		"AND NOT (" + cond + " AND (service_name, name) NOT IN ((?, ?))\n\t\t            AND cur_calls * base_buckets >= ? * base_calls * ?\n\t\t            AND calls_2 * base_buckets >= ? * base_calls * ?)",
	} {
		if !strings.Contains(q, frag) {
			t.Fatalf("sorgu %q taşımıyor:\n%s", frag, q)
		}
	}
	want := []any{slots[0], slots[1], base, now,
		opLatencyMinCalls, opLatencyMinCalls, opLatencyMinRatio, opLatencyMinP99Ms,
		opLatencyMinCalls, opLatencyMinRatio, opLatencyMinP99Ms,
		"-batch", "a-batch", "op1", batchLoadSurgeFactor, uint64(1), batchLoadSurgeFactor, uint64(1)}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("argümanlar\n%v\nbeklenen\n%v", args, want)
	}
}

// TestOpLatencyWindows — dwell 1 eski pencerelerle birebir; dwell 2'de kovalar
// tamamlanmış ve ardışık, taban İKİ kovadan da önce biter.
func TestOpLatencyWindows(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 2, 30, 0, time.UTC)
	aligned := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	slots, base, gotAligned, cb := opLatencyWindows(now, 5*time.Minute, 1)
	// v0.10.1085 öncesi aritmetik: curStart = hizalı − pencere, taban 24 sa önce.
	if gotAligned != aligned || cb != 1 || len(slots) != 1 ||
		slots[0] != aligned.Add(-5*time.Minute) || base != aligned.Add(-5*time.Minute-24*time.Hour) {
		t.Fatalf("dwell 1: slots=%v base=%v aligned=%v cb=%d", slots, base, gotAligned, cb)
	}

	slots, base, _, _ = opLatencyWindows(now, 5*time.Minute, 2)
	if !reflect.DeepEqual(slots, []time.Time{aligned.Add(-5 * time.Minute), aligned.Add(-10 * time.Minute)}) ||
		base != aligned.Add(-10*time.Minute-24*time.Hour) {
		t.Fatalf("dwell 2: slots=%v base=%v", slots, base)
	}

	// Geniş pencere: kova = pencere (10 dk = 2 MV kovası), taban max(24 sa, 12 × pencere).
	slots, base, _, cb = opLatencyWindows(now, 10*time.Minute, 2)
	if cb != 2 || slots[1] != aligned.Add(-20*time.Minute) || base != aligned.Add(-20*time.Minute-24*time.Hour) {
		t.Fatalf("10 dk pencere: slots=%v base=%v cb=%d", slots, base, cb)
	}
	if s, _, _, _ := opLatencyWindows(now, 5*time.Minute, 0); len(s) != 1 {
		t.Fatal("dwell 0 → 1 kova olmalı")
	}
}

// TestOpLatencyDwellAgreesOnClickHouse — GERÇEK sorgu metni (argümanlar
// gömülü) sentetik bir operation_summary_5m AggregatingMergeTree'si üstünde
// koşar; dönen çiftler Go kemeriyle (aynı satırları opLatencyScanDest ile
// ayrıştırıp classifyOpLatency) ve beklenen kümeyle aynı. `clickhouse local`
// yoksa atlanır (CI'yı golden + tablo kapsar).
func TestOpLatencyDwellAgreesOnClickHouse(t *testing.T) {
	bin, err := exec.LookPath("clickhouse")
	if err != nil {
		t.Skip("clickhouse ikilisi yok — dwell uçtan uca sorgusu atlandı")
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	slots, base, aligned, _ := opLatencyWindows(now, 5*time.Minute, 2)
	ts := func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

	// Her çiftin tabanı: 288 kova × 100 çağrı @ 20 ms; sürdürme kovaları aşağıda.
	type fx struct {
		svc, op             string
		curMs, prevMs       int64
		curCalls, prevCalls int
		wantFire            bool
	}
	cases := []fx{
		{"svc-a", "sustained", 800, 800, 40, 40, true},
		{"svc-b", "single-spike", 4_000, 20, 40, 40, false},
		{"svc-c", "thin-prev", 800, 800, 40, 10, false},
		{"svc-d", "prev-only", 20, 800, 40, 40, false},
		{"svc-e", "no-prev-data", 800, 0, 40, 0, false},
		{"orders-batch", "surge-both", 800, 800, 500, 500, false},
		{"orders-batch", "surge-latest", 800, 800, 500, 100, true},
		{"orders-batch", "Active", 800, 800, 500, 500, true},
		// Histerezis: aktif çift en yeni kova tek başına ihlal ettikçe tazelenir.
		{"svc-f", "active-dip", 800, 20, 40, 40, true},
		{"svc-g", "active-clean", 20, 800, 40, 40, false},
	}
	var sql strings.Builder
	sql.WriteString(`CREATE TABLE operation_summary_5m (service_name String, name String, time_bucket DateTime,
		span_count_state AggregateFunction(count), duration_q_state AggregateFunction(quantilesTDigest(0.5, 0.95, 0.99), UInt64))
		ENGINE = AggregatingMergeTree ORDER BY (service_name, name, time_bucket);` + "\n")
	ins := func(svc, op, bucket string, n int, ms int64) {
		fmt.Fprintf(&sql, "INSERT INTO operation_summary_5m SELECT '%s' AS s, '%s' AS o, %s AS tb, countState(), quantilesTDigestState(0.5, 0.95, 0.99)(toUInt64(%d)) FROM numbers(%d) GROUP BY s, o, tb;\n",
			svc, op, bucket, ms*1_000_000, n)
	}
	for _, c := range cases {
		ins(c.svc, c.op, fmt.Sprintf("toDateTime('%s') + toIntervalMinute(5 * intDiv(number, 100))", ts(base)), 288*100, 20)
		ins(c.svc, c.op, fmt.Sprintf("toDateTime('%s')", ts(slots[0])), c.curCalls, c.curMs)
		if c.prevCalls > 0 {
			ins(c.svc, c.op, fmt.Sprintf("toDateTime('%s')", ts(slots[1])), c.prevCalls, c.prevMs)
		}
	}
	activeKeys := opLatKeys("orders-batch", "Active", "svc-f", "active-dip", "svc-g", "active-clean")
	plan := planOpLatBatch(chstore.AnomalySensitivityConfig{}, 1, activeKeys, nil)
	sustainExempt := opLatSustainExempt(2, activeKeys, nil)
	activeSet := map[opLatPair]bool{}
	for _, p := range sustainExempt {
		activeSet[p] = true
	}
	q, args := opLatencyQuery(slots, base, aligned, plan, sustainExempt)
	for i, a := range args {
		if tm, ok := a.(time.Time); ok {
			args[i] = ts(tm)
		}
	}
	sql.WriteString(inlineTraceOpArgs(t, q, args) + "\nFORMAT TSVRaw;\n")
	f, err := os.CreateTemp(t.TempDir(), "oplat-*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(sql.String()); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	out, err := exec.Command(bin, "local", "--multiquery", "--queries-file", f.Name()).CombinedOutput()
	if err != nil {
		t.Fatalf("clickhouse local: %v\n%s", err, out)
	}

	var rows []opLatencyBucket
	sqlSet := map[string]bool{}
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ln == "" {
			continue
		}
		var b opLatencyBucket
		dest := opLatencyScanDest(&b, 2, true)
		cols := strings.Split(ln, "\t")
		if len(cols) != len(dest) {
			t.Fatalf("kolon sayısı %d, tarayıcı %d: %q", len(cols), len(dest), ln)
		}
		for i, d := range dest {
			switch p := d.(type) {
			case *string:
				*p = cols[i]
			case *float64:
				*p, err = strconv.ParseFloat(cols[i], 64)
			case *uint64:
				*p, err = strconv.ParseUint(cols[i], 10, 64)
			}
			if err != nil {
				t.Fatalf("kolon %d %q: %v", i, cols[i], err)
			}
		}
		b.Active = activeSet[opLatPair{Service: b.Service, Operation: b.Operation}]
		rows = append(rows, b)
		sqlSet[b.Service+"/"+b.Operation] = true
	}
	goSet := map[string]bool{}
	for _, a := range classifyOpLatency(rows, 2, plan.gate) {
		goSet[a.Service+"/"+a.Operation] = true
	}
	wantSet := map[string]bool{}
	for _, c := range cases {
		if c.wantFire {
			wantSet[c.svc+"/"+c.op] = true
		}
	}
	keys := func(m map[string]bool) []string {
		var k []string
		for s := range m {
			k = append(k, s)
		}
		sort.Strings(k)
		return k
	}
	if !reflect.DeepEqual(sqlSet, wantSet) || !reflect.DeepEqual(goSet, wantSet) {
		t.Fatalf("SQL %v / Go %v / beklenen %v", keys(sqlSet), keys(goSet), keys(wantSet))
	}
	for _, r := range rows {
		if r.Service == "svc-a" && (r.CurP99Ms != 800 || r.CurCalls != 40 || r.BaseCalls != 28_800 ||
			r.BaseBuckets != 288 || len(r.Earlier) != 1 || r.Earlier[0].Calls != 40 || r.Earlier[0].P99Ms != 800) {
			t.Fatalf("kolon sırası tarayıcıyla uyuşmuyor: %+v", r)
		}
	}
}

// TestOpLatencyDwellWiring — kaynak pini: dedektör dwell'i anahtarla AYNI
// atomic okumadan (sens) alır, kapıdan SONRA; pencereye, tarayıcıya ve
// sınıflayıcıya aynı değer gider; tik başına ayar CH'den okunmaz.
func TestOpLatencyDwellWiring(t *testing.T) {
	b, err := os.ReadFile("op_latency.go")
	if err != nil {
		t.Fatal(err)
	}
	src := regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(string(b), "")
	i := strings.Index(src, "func DetectOpLatencyAnomalies(")
	if i < 0 {
		t.Fatal("DetectOpLatencyAnomalies bulunamadı")
	}
	body := src[i:]
	idx := func(s string) int { return strings.Index(body, s) }
	gate := idx("if !sens.OpLatencyOn() {")
	dwell := idx("dwell := sens.OpLatencyDwell()")
	win := idx("opLatencyWindows(time.Now(), window, dwell)")
	scan := idx(`opLatencyScanDest(&b, dwell, plan.cond != "")`)
	cls := idx("classifyOpLatency(buckets, dwell, plan.gate)")
	if gate < 0 || dwell < 0 || win < 0 || scan < 0 || cls < 0 || !(gate < dwell && dwell < win && win < scan && scan < cls) {
		t.Fatalf("kablo bozuk (kapı %d, dwell %d, pencere %d, tarama %d, sınıflama %d)", gate, dwell, win, scan, cls)
	}
	// Histerezis: aynı aktif-olay okuması, dwell ≥ 2'de servis daraltmasız;
	// muaf liste sorguya ve satıra (Active) gider.
	for _, want := range []string{
		"if dwell > 1 {\n\t\tsvcCond, svcArgs = \"\", nil\n\t}",
		"sustainExempt := opLatSustainExempt(dwell, active, readErr)",
		"opLatencyQuery(slotStarts, baseStart, alignedNow, plan, sustainExempt)",
		"b.Active = activeSet[opLatPair{Service: b.Service, Operation: b.Operation}]",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("DetectOpLatencyAnomalies %q taşımıyor", want)
		}
	}
	if strings.Count(body, "ListActiveAnomalyKeys(") != 1 {
		t.Fatal("aktif-olay okuması tek değil")
	}
	if strings.Count(body, "conn.Query(ctx, q, args...)") != 1 {
		t.Fatal("tespit sorgusu tek değil — kova başına ek sorgu mu açıldı?")
	}
}

// TestOpLatSustainExempt — sürdürme muafiyet listesi: dwell 1 ya da okuma
// hatası → boş; tekrar bir kez, sıra korunur.
func TestOpLatSustainExempt(t *testing.T) {
	keys := opLatKeys("a", "op1", "b", "op2", "a", "op1")
	if got := opLatSustainExempt(1, keys, nil); len(got) != 0 {
		t.Fatalf("dwell 1'de muafiyet olmamalı: %v", got)
	}
	if got := opLatSustainExempt(2, keys, errors.New("ch down")); len(got) != 0 {
		t.Fatalf("okuma hatasında muafiyet olmamalı: %v", got)
	}
	want := []opLatPair{{"a", "op1"}, {"b", "op2"}}
	if got := opLatSustainExempt(2, keys, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, beklenen %v", got, want)
	}
}

// TestOpLatencyQueryDwellActive — aktif çiftler önceki kova koşullarından
// tuple IN ile muaf (aynı geçiş); liste boşsa metin dwell 2 golden'ı.
func TestOpLatencyQueryDwellActive(t *testing.T) {
	slots, base, now, _ := opLatencyWindows(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), 5*time.Minute, 2)
	if q, _ := opLatencyQuery(slots, base, now, opLatBatchPlan{}, []opLatPair{}); q != goldenDwell2OpLatencySQL {
		t.Fatalf("boş aktif listede metin değişti:\n%s", q)
	}
	q, args := opLatencyQuery(slots, base, now, opLatBatchPlan{}, []opLatPair{{"svc-a", "GET /x"}, {"svc-b", "op"}})
	frag := "\n\t\t   AND ((calls_2 >= ? AND p99_2 >= ? * base_p99 AND p99_2 >= ?)\n\t\t        OR (service_name, name) IN ((?, ?), (?, ?)))"
	if !strings.Contains(q, frag) {
		t.Fatalf("aktif muafiyeti yok:\n%s", q)
	}
	if strings.Count(q, "?") != len(args) || strings.Count(q, "FROM operation_summary_5m") != 1 {
		t.Fatalf("yer tutucu %d / argüman %d", strings.Count(q, "?"), len(args))
	}
	tail := args[len(args)-7:]
	want := []any{opLatencyMinCalls, opLatencyMinRatio, opLatencyMinP99Ms, "svc-a", "GET /x", "svc-b", "op"}
	if !reflect.DeepEqual(tail, want) {
		t.Fatalf("argüman kuyruğu %v, beklenen %v", tail, want)
	}
	// dwell 1'de liste yok sayılır (önceki kova yok) — legacy metin.
	q1, _ := opLatencyQuery(slots[:1], base, now, opLatBatchPlan{}, []opLatPair{{"svc-a", "GET /x"}})
	if strings.Contains(q1, " IN (") {
		t.Fatalf("dwell 1'de IN listesi olmamalı:\n%s", q1)
	}
}
