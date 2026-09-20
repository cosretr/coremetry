package chstore

import (
	"strings"
	"testing"
	"time"
)

// v0.10.810 — "aged out" kararı ve tüm-replika yedek okuması (operatör
// hatası: TTL içindeki trace "yaşlandı" kartı alıyordu; replika ıraksaması).

func TestTraceAgedOut(t *testing.T) {
	now := time.Date(2026, 9, 19, 21, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) int64 { return now.Add(-d).UnixNano() }
	cases := []struct {
		name string
		st   int64
		ret  string
		def  int
		want bool
	}{
		{"1 saat önce, 7d", ago(time.Hour), "7d", 30, false},
		{"8 gün önce, 7d", ago(8 * 24 * time.Hour), "7d", 30, true},
		{"6 gün önce, 7d", ago(6 * 24 * time.Hour), "7d", 30, false},
		{"3 gün önce, 48h (2 güne yuvarlanır)", ago(3 * 24 * time.Hour), "48h", 30, true},
		{"1 gün önce, 48h", ago(24 * time.Hour), "48h", 30, false},
		{"bozuk değer → varsayılan 30", ago(20 * 24 * time.Hour), "abc", 30, false},
		{"bozuk değer + varsayılan 0 → 30", ago(40 * 24 * time.Hour), "", 0, true},
		{"başlangıç bilinmiyor → yaşlı deme", 0, "7d", 30, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TraceAgedOut(c.st, now, c.ret, c.def); got != c.want {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestTraceAllReplicasSQLBounded(t *testing.T) {
	// v0.10.826 — tablo argümanı VERİTABANIYLA nitelenmiş olmalı; çıplak
	// `spans_local` ClickHouse'ta kod 42 ("Table name was not found in
	// function arguments") ile reddediliyordu ve yedek okuma kümede hiç
	// koşmadı. Şekil ayrıca cluster_all_replicas_args_test.go ile
	// kaynaktan pinli.
	q, err := traceAllReplicasSQL("prod-eu", "coremetry")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"clusterAllReplicas('prod-eu', `coremetry`.`spans_local`)", "trace_id = ?", "time >= ?", "time <= ?", "LIMIT 50000", "max_execution_time = 20", "skip_unavailable_shards = 1", traceSpanCols} {
		if !strings.Contains(q, want) {
			t.Errorf("SQL eksik: %s", want)
		}
	}
	if strings.Contains(q, "currentDatabase()") {
		t.Error("tablo fonksiyonunun argümanı başlatan düğümde çözülür — currentDatabase() uzak düğümde başka DB'ye işaret edebilir")
	}
	if strings.Contains(q, "FROM spans\n") || strings.Contains(q, "FROM spans ") {
		t.Error("yedek okuma Distributed sarmalayıcıya gitmemeli")
	}
	for _, bad := range []string{"", "coremetry;DROP", "cm-1", "1db", "`cm`"} {
		if _, err := traceAllReplicasSQL("prod-eu", bad); err == nil {
			t.Errorf("geçersiz db %q doğrulamadan geçti — ad tırnaksız eklenmemeli", bad)
		}
	}
	lo, hi := traceReplicaWindow(1_000_000_000_000, 2_000_000_000_000)
	if lo.UnixNano() != 1_000_000_000_000-int64(time.Minute) || hi.UnixNano() != 2_000_000_000_000+int64(time.Minute) {
		t.Errorf("pencere ±60 sn olmalı: %v %v", lo, hi)
	}
	lo, hi = traceReplicaWindow(5_000_000_000_000, 0) // end < start
	if hi.Sub(lo) != 2*time.Minute {
		t.Errorf("bozuk end → start ± 60 sn: %v", hi.Sub(lo))
	}
}

func TestDedupSpanRows(t *testing.T) {
	in := []SpanRow{{SpanID: "a", Name: "x"}, {SpanID: "b"}, {SpanID: "a", Name: "y"}, {SpanID: "", TraceID: "t", Name: "n", StartTime: 1}, {SpanID: "", TraceID: "t", Name: "n", StartTime: 1}}
	out := dedupSpanRows(in)
	if len(out) != 3 || out[0].Name != "x" || out[1].SpanID != "b" || out[2].Name != "n" {
		t.Errorf("%+v", out)
	}
	if got := dedupSpanRows(nil); got != nil {
		t.Error("nil → nil")
	}
}
