package chstore

// ingest_ledger_test.go — v0.10.767 (Faz B): saf delta, SQL/DDL sözleşmeleri,
// kablolama pinleri (feedback-tested-but-unreachable).

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestIngestLedgerDelta(t *testing.T) {
	now := time.Date(2026, 9, 17, 22, 7, 41, 0, time.UTC)
	// İlk örnek: prev sıfır → boot'tan beri her şey.
	row, ok := ingestLedgerDelta("spans", "pod-a", "b1", now, IngestCounters{}, IngestCounters{Accepted: 120, Dropped: 2, WriteFailed: 1})
	if !ok || row.Accepted != 120 || row.Dropped != 2 || row.WriteFailed != 1 || row.AcceptedTotal != 120 {
		t.Fatalf("ilk örnek: %+v ok=%v", row, ok)
	}
	if !row.Bucket.Equal(time.Date(2026, 9, 17, 22, 7, 0, 0, time.UTC)) {
		t.Errorf("kova dakikaya kırpılmalı: %v", row.Bucket)
	}
	if row.Signal != "spans" || row.Pod != "pod-a" || row.BootID != "b1" {
		t.Errorf("kimlik: %+v", row)
	}
	// Artış: delta.
	row, ok = ingestLedgerDelta("spans", "pod-a", "b1", now, IngestCounters{Accepted: 120, Dropped: 2, WriteFailed: 1}, IngestCounters{Accepted: 150, Dropped: 2, WriteFailed: 4})
	if !ok || row.Accepted != 30 || row.Dropped != 0 || row.WriteFailed != 3 || row.AcceptedTotal != 150 {
		t.Errorf("delta: %+v ok=%v", row, ok)
	}
	// Sıfır delta yine satır (kalp atışı).
	if _, ok = ingestLedgerDelta("spans", "pod-a", "b1", now, IngestCounters{Accepted: 5}, IngestCounters{Accepted: 5}); !ok {
		t.Error("sıfır delta satır üretmeli (pod canlı kalp atışı)")
	}
	// Geri giden sayaç: satır yok, negatif yok.
	if _, ok = ingestLedgerDelta("spans", "pod-a", "b1", now, IngestCounters{Accepted: 100}, IngestCounters{Accepted: 3}); ok {
		t.Error("geri giden sayaç satır üretmemeli")
	}
	// Yerel dilimli 'now' UTC kovaya iner.
	loc := time.FixedZone("X", 3*3600)
	row, _ = ingestLedgerDelta("spans", "p", "b", time.Date(2026, 9, 18, 1, 30, 5, 0, loc), IngestCounters{}, IngestCounters{})
	if row.Bucket.Location() != time.UTC || row.Bucket.Hour() != 22 {
		t.Errorf("kova UTC olmalı: %v", row.Bucket)
	}
}

func TestIngestLedgerSQLContracts(t *testing.T) {
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "CREATE TABLE IF NOT EXISTS ingest_ledger")
	if i < 0 {
		t.Fatal("ingest_ledger DDL store.go tables diliminde değil")
	}
	ddl := s[i:]
	ddl = ddl[:strings.Index(ddl, "`,")]
	for _, must := range []string{
		"ENGINE = ReplacingMergeTree(version)",
		"PARTITION BY toYYYYMM(bucket)", // Kural P1: partition kolonu ORDER BY'da
		"ORDER BY (signal, pod, bucket)",
		"TTL bucket + INTERVAL 30 DAY",
	} {
		if !strings.Contains(ddl, must) {
			t.Errorf("DDL %q taşımalı", must)
		}
	}
	// INSERT kolonları DDL'de var ve sıra Append ile aynı.
	ins := ingestLedgerInsertSQL()
	cols := strings.TrimSuffix(strings.TrimPrefix(ins, "INSERT INTO ingest_ledger ("), ")")
	for _, c := range strings.Split(cols, ", ") {
		if !strings.Contains(ddl, "\t"+c+" ") {
			t.Errorf("INSERT kolonu %q DDL'de yok", c)
		}
	}
	if !strings.HasPrefix(cols, "signal, pod, bucket, boot_id, accepted, dropped, write_failed, accepted_total") {
		t.Errorf("INSERT kolon sırası Append sırasıyla aynı olmalı: %s", cols)
	}
	for name, q := range map[string]string{"pods": ingestFleetPodsSQL(), "buckets": ingestFleetBucketsSQL()} {
		for _, must := range []string{"FROM ingest_ledger FINAL", "bucket >= toDateTime(?, 'UTC')", "bucket < toDateTime(?, 'UTC')", "LIMIT ", "max_execution_time"} {
			if !strings.Contains(q, must) {
				t.Errorf("%s SQL %q taşımalı", name, must)
			}
		}
	}
	// Toplamlar yalnız yerleşmiş kısımdan: üç sumIf, hepsi settledTo sınırlı.
	if strings.Count(ingestFleetPodsSQL(), "sumIf(") != 3 {
		t.Error("pod toplamları sumIf ile yerleşmiş pencereye sınırlı olmalı")
	}
}

// Kablolama: yazıcı main.go'da ingest rolünde, okuyucu trace-health ucunda,
// tablo purge listesinde ve state-tablo kapısında.
func TestIngestLedgerWired(t *testing.T) {
	for file, must := range map[string]string{
		"../../main.go":          "store.StartIngestLedger(ctx, \"spans\"",
		"../api/trace_health.go": "IngestLedgerFleet(ctx, \"spans\"",
		"purge.go":               "\"ingest_ledger\"",
		"conn_strategy_test.go":  "\"FROM ingest_ledger\"",
	} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), must) {
			t.Errorf("%s: %q yok", file, must)
		}
	}
}
