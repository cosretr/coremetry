package api

// trace_health_test.go — v0.10.757: saf yardımcılar + route/kayıt pinleri.

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestTraceHealthRangeClamp(t *testing.T) {
	for in, want := range map[string]int{"": 3600, "abc": 3600, "10": 300, "300": 300, "7200": 7200, "999999": 86400} {
		if got := traceHealthRange(in); got != want {
			t.Errorf("range_s=%q → %d, istenen %d", in, got, want)
		}
	}
}

func TestSumStored(t *testing.T) {
	if sumStored(nil) != 0 {
		t.Error("nil → 0")
	}
	if got := sumStored([]chstore.StoredSpanBucket{{Spans: 2}, {Spans: 40}}); got != 42 {
		t.Errorf("toplam %d", got)
	}
}

func TestTraceHealthWiring(t *testing.T) {
	src, err := os.ReadFile("trace_health.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		`registerRoutesExtra("trace-health"`,
		`"GET /api/admin/clickhouse/trace-health"`,
		"auth.RequireRole(auth.RoleAdmin,",
		"s.serveCached(w, r, key, 30*time.Second,",
		`resp.Errors["stored"]`, `resp.Errors["coverage"]`, `resp.Errors["names"]`, // bölüm başına yumuşak hata
		"otlp.IngestRejectCounts()", "s.distributionBacklog()", "s.store.TraceMVGapDayList(ctx)",
		"IngestRole: !s.roleIngestOff", // v0.10.760 — api-rolü pod "kayıp yok" demesin
	} {
		if !strings.Contains(s, want) {
			t.Errorf("trace_health.go %q içermeli", want)
		}
	}
}
