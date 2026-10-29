package anomaly

// v0.10.1586 — Davis paritesi FAZ 0.5 / C: trace_op error_spike VE new_error
// iki ardışık kova ister (op_latency v0.10.1085 emsali). Belirti (FP sınıfı
// d_single_window / d_flapping): recorder 1 dk'da bir son 5 dk kovaya bakar;
// tek kovalık bir hata patlaması 5 tik boyunca yeniden yazılıyor, olay
// started_at'i terfinin MinSustainedSec (300 sn) eşiğini aşıyor ve tek bir
// kova anomaly-auto Problem'i açıyordu.

import (
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestClassifyTraceOpsErrorSpikeRequiresSustain(t *testing.T) {
	const wr = 5.0 / (24 * 60) // 5 dk kova / 24 sa taban
	batch := func(s string) bool { return strings.HasSuffix(s, "-batch") }
	spike := traceOpBucket{Service: "orders-api", Operation: "GET /orders", CurErrs: 200, CurCalls: 1000, BaseErrs: 2880} // taban 10/kova → 20×
	newErr := traceOpBucket{Service: "orders-api", Operation: "POST /pay", CurErrs: 50, CurCalls: 500}
	with := func(b traceOpBucket, prev traceOpSlot, active bool) traceOpBucket {
		b.Earlier, b.Active = []traceOpSlot{prev}, active
		return b
	}
	cases := []struct {
		name     string
		in       traceOpBucket
		isBatch  func(string) bool
		dwell    int
		wantKind string
	}{
		{"dwell 1 = eski tek pencere (önceki kova bilinmiyor)", spike, nil, 1, "error_spike"},
		{"error_spike: önceki kova taban düzeyinde → olay yok", with(spike, traceOpSlot{Errs: 9, Calls: 1000}, false), nil, 2, ""},
		{"error_spike: önceki kova da 3× üstünde → olay", with(spike, traceOpSlot{Errs: 40, Calls: 1000}, false), nil, 2, "error_spike"},
		{"error_spike: önceki kova sayıyı geçiyor ama oran 3× altında → yok", with(spike, traceOpSlot{Errs: 20, Calls: 1000}, false), nil, 2, ""},
		{"error_spike: önceki kova bilgisi yok → yok", spike, nil, 2, ""},
		{"error_spike: AKTİF olay muaf → tazelenir", with(spike, traceOpSlot{}, true), nil, 2, "error_spike"},
		{"new_error: önceki kovada hata yok → olay yok", with(newErr, traceOpSlot{Errs: 0, Calls: 500}, false), nil, 2, ""},
		{"new_error: önceki kova pay tabanının altında → yok", with(newErr, traceOpSlot{Errs: 10, Calls: 5000}, false), nil, 2, ""},
		{"new_error: önceki kova da tabanları geçiyor → olay", with(newErr, traceOpSlot{Errs: 12, Calls: 400}, false), nil, 2, "new_error"},
		{"new_error: AKTİF olay muaf", with(newErr, traceOpSlot{}, true), nil, 2, "new_error"},
		{
			"batch uzun-taban dalı kapısız (seyrek koşan iş tek koşuya bakar)",
			traceOpBucket{Service: "nightly-batch", Operation: "run", CurErrs: 100, CurCalls: 200, ExtErrs: 1, ExtCalls: 1000, BaseCalls: 50, Earlier: []traceOpSlot{{}}},
			batch, 2, "new_error",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyTraceOps([]traceOpBucket{c.in}, wr, c.isBatch, c.dwell)
			kind := ""
			if len(got) == 1 {
				kind = got[0].Kind
			}
			if kind != c.wantKind {
				t.Fatalf("kind=%q, want %q (%+v)", kind, c.wantKind, got)
			}
		})
	}
}

func TestTraceOpWindowsBaseEndsBeforeSustainWindow(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 7, 30, 0, time.UTC)
	slots, base, aligned, wr := traceOpWindows(now, 5*time.Minute, 1)
	if !aligned.Equal(time.Date(2026, 10, 29, 10, 5, 0, 0, time.UTC)) || len(slots) != 1 ||
		!slots[0].Equal(aligned.Add(-5*time.Minute)) || !base.Equal(slots[0].Add(-24*time.Hour)) || wr != 5.0/(24*60) {
		t.Fatalf("dwell 1 bugünkü pencereler olmalı: slots=%v base=%v wr=%v", slots, base, wr)
	}
	slots, base, _, wr = traceOpWindows(now, 5*time.Minute, 2)
	if len(slots) != 2 || !slots[1].Equal(aligned.Add(-10*time.Minute)) {
		t.Fatalf("iki kova: %v", slots)
	}
	if !base.Equal(slots[1].Add(-24 * time.Hour)) {
		t.Fatalf("taban sürdürme penceresinin TAMAMINDAN önce bitmeli: base=%v slots=%v", base, slots)
	}
	if wr != 5.0/(24*60) {
		t.Fatalf("windowRatio kova/taban boyu olmalı: %v", wr)
	}
}

func TestTraceOpQueryDwellTwoShape(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 5, 0, 0, time.UTC)
	slots, base, aligned, wr := traceOpWindows(now, 5*time.Minute, 1)
	q1, a1 := traceOpQueryDwell(slots, base, aligned, wr, "", nil, []traceOpPair{{"orders-api", "x"}})
	q0, a0 := traceOpQuery(slots[0], base, aligned, wr, "", nil)
	if q1 != q0 || len(a1) != len(a0) {
		t.Fatal("dwell 1 traceOpQuery ile birebir olmalı (aktif liste yok sayılır)")
	}
	slots, base, aligned, wr = traceOpWindows(now, 5*time.Minute, 2)
	cond := "positionCaseInsensitive(service_name, ?) > 0"
	q, args := traceOpQueryDwell(slots, base, aligned, wr, cond, []any{"-batch"}, []traceOpPair{{"orders-api", "GET /orders"}})
	for _, want := range []string{
		"multiIf(time_bucket >= ?, 0, time_bucket >= ?, 1, -1) AS slot",
		"sumIf(errs,  slot = -1) AS base_errs",
		"sumIf(calls, slot = -1) AS base_calls",
		"sumIf(errs,  slot = 1) AS prev_errs_1",
		"sumIf(calls, slot = 1) AS prev_calls_1",
		"(prev_errs_1 >= ? AND prev_calls_1 > 0 AND prev_errs_1 >= ? * prev_calls_1",
		"OR (prev_errs_1 >= ? * base_errs * ?)",
		"OR (" + cond + " AND base_errs = 0)",
		"OR (service_name, name) IN ((?, ?))",
		"FROM operation_summary_5m",
		"WHERE time_bucket >= ? AND time_bucket < ?",
		"LIMIT 200",
		"SETTINGS max_execution_time = 25",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("sorguda yok: %q\n%s", want, q)
		}
	}
	if strings.Contains(q, "FROM spans") {
		t.Error("ham spans okuması (MV-first)")
	}
	if n := strings.Count(q, "?"); n != len(args) {
		t.Fatalf("yer tutucu %d ≠ argüman %d", n, len(args))
	}
	if !args[0].(time.Time).Equal(slots[0]) || !args[1].(time.Time).Equal(slots[1]) || !args[2].(time.Time).Equal(base) {
		t.Fatalf("kova bağları sırası: %v", args[:3])
	}
	if args[len(args)-2] != "orders-api" || args[len(args)-1] != "GET /orders" {
		t.Fatalf("aktif tuple argümanları sonda: %v", args[len(args)-2:])
	}
}

// SQL HAVING (sürdürme kolu dahil) ile Go aynı çiftleri geçirir — `clickhouse
// local` ikilisi yoksa atlanır (CI'da şekil testi kapsar). Bilinen tek fark
// (fikstür dışı): batch çiftinde base_errs = 0 SQL'de muaf (uzun-taban kararı
// Go'da), Go sürdürülmemiş düz new_error'ı eler — SQL üst küme.
func TestTraceOpDwellHavingAgreesWithGo(t *testing.T) {
	bin, err := exec.LookPath("clickhouse")
	if err != nil {
		t.Skip("clickhouse ikilisi yok — Go↔SQL sürdürme karşılaştırması atlandı")
	}
	sens := batchSens("-batch")
	now := time.Date(2026, 10, 29, 10, 5, 0, 0, time.UTC)
	slots, base, aligned, wr := traceOpWindows(now, 5*time.Minute, 2)
	const be = 2880 // 24 sa'te 2880 hata → kova başına 10
	rows := []traceOpBucket{
		{Service: "orders-api", Operation: "sustained", CurErrs: 200, CurCalls: 1000, BaseErrs: be, Earlier: []traceOpSlot{{40, 1000}}},
		{Service: "orders-api", Operation: "single", CurErrs: 200, CurCalls: 1000, BaseErrs: be, Earlier: []traceOpSlot{{9, 1000}}},
		{Service: "orders-api", Operation: "prevratio", CurErrs: 200, CurCalls: 1000, BaseErrs: be, Earlier: []traceOpSlot{{20, 1000}}},
		{Service: "orders-api", Operation: "prevshare", CurErrs: 200, CurCalls: 1000, BaseErrs: be, Earlier: []traceOpSlot{{40, 100000}}},
		{Service: "orders-api", Operation: "active", CurErrs: 200, CurCalls: 1000, BaseErrs: be, Earlier: []traceOpSlot{{0, 0}}, Active: true},
		{Service: "orders-api", Operation: "new-sus", CurErrs: 50, CurCalls: 500, Earlier: []traceOpSlot{{12, 400}}},
		{Service: "orders-api", Operation: "new-single", CurErrs: 50, CurCalls: 500, Earlier: []traceOpSlot{{0, 500}}},
		{Service: "nightly-batch", Operation: "sustained", CurErrs: 900, CurCalls: 2000, BaseErrs: be, BaseCalls: 288000, Earlier: []traceOpSlot{{900, 2000}}},
		{Service: "nightly-batch", Operation: "new-sus", CurErrs: 50, CurCalls: 500, BaseCalls: 1000, Earlier: []traceOpSlot{{50, 500}}},
	}
	var active []traceOpPair
	for _, r := range rows {
		if r.Active {
			active = append(active, traceOpPair{r.Service, r.Operation})
		}
	}
	cond, cargs := sens.BatchServiceSQL("service_name")
	q, args := traceOpQueryDwell(slots, base, aligned, wr, cond, cargs, active)
	having := q[strings.Index(q, "HAVING ")+len("HAVING ") : strings.Index(q, "ORDER BY cur_errs DESC")]
	pred := inlineTraceOpArgs(t, having, args[4:]) // ilk dördü alt sorgunun kova/zaman bağları
	vals := make([]string, len(rows))
	for i, r := range rows {
		vals[i] = fmt.Sprintf("('%s', '%s', %d, %d, %d, %d, %d, %d)", r.Service, r.Operation, r.CurErrs, r.BaseErrs, r.CurCalls, r.BaseCalls, r.Earlier[0].Errs, r.Earlier[0].Calls)
	}
	sql := "SELECT service_name, name FROM values('service_name String, name String, cur_errs UInt64, base_errs UInt64, cur_calls UInt64, base_calls UInt64, prev_errs_1 UInt64, prev_calls_1 UInt64', " +
		strings.Join(vals, ", ") + ") WHERE " + pred + " ORDER BY service_name, name FORMAT TSVRaw"
	out, err := exec.Command(bin, "local", "--query", sql).CombinedOutput()
	if err != nil {
		t.Fatalf("clickhouse local: %v\n%s\n%s", err, out, sql)
	}
	sqlSet := map[string]bool{}
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ln != "" {
			sqlSet[strings.Replace(ln, "\t", "/", 1)] = true
		}
	}
	goSet := map[string]bool{}
	for _, a := range classifyTraceOps(rows, wr, sens.IsBatchService, 2) {
		goSet[a.Service+"/"+a.Operation] = true
	}
	if !reflect.DeepEqual(sqlSet, goSet) {
		t.Fatalf("SQL ve Go farklı çiftleri geçiriyor\nSQL: %v\nGo:  %v", sqlSet, goSet)
	}
	for k, want := range map[string]bool{
		"orders-api/sustained": true, "orders-api/single": false, "orders-api/prevratio": false,
		"orders-api/prevshare": false, "orders-api/active": true, "orders-api/new-sus": true,
		"orders-api/new-single": false, "nightly-batch/new-sus": true,
	} {
		if goSet[k] != want {
			t.Fatalf("%s: geçti=%v, beklenen %v", k, goSet[k], want)
		}
	}
}
