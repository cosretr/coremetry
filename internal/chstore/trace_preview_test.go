package chstore

// trace_preview_test.go — v0.10.1881 (Log Search Faz 3 dilim 3.4). Toplu trace
// önizlemesinin SQL'i: MV-first (trace_summary_5m, ham spans DEĞİL), zaman
// tabanı + üst sınırı, `trace_id IN` yer tutucu sayısı = kimlik sayısı, LIMIT
// ve max_execution_time (CLAUDE.md CH kuralı).

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTracePreviewSQL(t *testing.T) {
	s := &Store{}
	q := s.tracePreviewSQL(3)
	for _, want := range []string{
		"FROM trace_summary_5m",
		"time_bucket >= ? AND time_bucket <= ?",
		"trace_id IN (?,?,?)",
		"GROUP BY trace_id",
		"LIMIT 200",
		"max_execution_time = 3",
		"countMerge(span_count_state)",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("SQL %q içermeli:\n%s", want, q)
		}
	}
	if strings.Contains(q, "FROM spans") {
		t.Fatal("ham spans okunmamalı (MV-first)")
	}
}

func TestTracePreviewsEmpty(t *testing.T) {
	got, err := (&Store{}).TracePreviews(context.Background(), nil, time.Now(), time.Now())
	if err != nil || len(got) != 0 {
		t.Fatalf("boş kimlik listesi sorgusuz boş dönmeli: %v %v", got, err)
	}
}
