package chstore

// v0.10.1814 — günlük baseline işinin ml_series okuması: zaman sınırlı,
// dilimli, FINAL, sıralı, LIMIT + max_execution_time (CLAUDE.md CH kuralı).

import (
	"strings"
	"testing"
)

func TestMLSeriesDaySliceSQLBounded(t *testing.T) {
	q := mlSeriesDaySliceSQL()
	for _, want := range []string{"FROM ml_series FINAL", "bucket >= ? AND bucket < ?", "cityHash64(service) % ? = ?",
		"ORDER BY signal, service, part, bucket, bucket_sec", "LIMIT ?", "max_execution_time = 25"} {
		if !strings.Contains(q, want) {
			t.Errorf("sorguda %q yok:\n%s", want, q)
		}
	}
	if strings.Count(q, "?") != 5 {
		t.Errorf("bağ sayısı 5 olmalı (from, to, dilim sayısı, dilim, limit)")
	}
}
