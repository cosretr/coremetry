package api

// trace_health_test.go — v0.10.757: saf yardımcılar + route/kayıt pinleri.

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

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

// v0.10.767 (Faz B) — yerleşmiş pencere, kova toplamı, defter-yok tespiti.
func TestSettledWindow(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 7, 30, 0, time.UTC)
	from, to := settledWindow(now, 3600)
	if !from.Equal(time.Date(2026, 9, 17, 11, 5, 0, 0, time.UTC)) || !to.Equal(time.Date(2026, 9, 17, 11, 55, 0, 0, time.UTC)) {
		t.Errorf("1 saat: %v → %v", from, to)
	}
	// 5 dk pencere yerleşme payından kısa → boş (from == to), asla ters.
	from, to = settledWindow(now, 300)
	if !to.Equal(from) {
		t.Errorf("kısa pencere boş olmalı: %v → %v", from, to)
	}
}

func TestSumStoredIn(t *testing.T) {
	ns := func(h, m int) int64 { return time.Date(2026, 9, 17, h, m, 0, 0, time.UTC).UnixNano() }
	b := []chstore.StoredSpanBucket{{TimeNs: ns(10, 0), Spans: 1}, {TimeNs: ns(10, 5), Spans: 10}, {TimeNs: ns(10, 10), Spans: 100}}
	from, to := time.Date(2026, 9, 17, 10, 5, 0, 0, time.UTC), time.Date(2026, 9, 17, 10, 10, 0, 0, time.UTC)
	if got := sumStoredIn(b, from, to); got != 10 {
		t.Errorf("[10:05,10:10) → %d, istenen 10", got)
	}
	if got := sumStoredIn(b, from, from); got != 0 {
		t.Errorf("boş pencere → %d", got)
	}
}

func TestLedgerMissing(t *testing.T) {
	if ledgerMissing(nil) {
		t.Error("nil → false")
	}
	if !ledgerMissing(errors.New("code: 60, message: Table coremetry.ingest_ledger doesn't exist")) {
		t.Error("UNKNOWN_TABLE (60) → true")
	}
	if ledgerMissing(errors.New("code: 241, message: Memory limit exceeded")) {
		t.Error("başka hata → false")
	}
}

// v0.10.770 — oran yalnız defterin kapsadığı kovalardan (prod: deploy'dan
// 15 dk sonra 6 saatlik pencerede "%7495 saklandı").
func TestLedgerCoveredFrom(t *testing.T) {
	sf := time.Date(2026, 9, 17, 14, 5, 0, 0, time.UTC)
	if got := ledgerCoveredFrom(sf, 0); !got.Equal(sf) {
		t.Errorf("örnek yok → pencere başı, %v", got)
	}
	first := time.Date(2026, 9, 17, 19, 51, 30, 0, time.UTC)
	if got := ledgerCoveredFrom(sf, first.UnixNano()); !got.Equal(time.Date(2026, 9, 17, 19, 55, 0, 0, time.UTC)) {
		t.Errorf("ilk kısmi kova dışarıda kalmalı (yukarı yuvarla): %v", got)
	}
	aligned := time.Date(2026, 9, 17, 19, 55, 0, 0, time.UTC)
	if got := ledgerCoveredFrom(sf, aligned.UnixNano()); !got.Equal(aligned) {
		t.Errorf("hizalı örnek olduğu gibi: %v", got)
	}
	early := time.Date(2026, 9, 17, 13, 0, 0, 0, time.UTC)
	if got := ledgerCoveredFrom(sf, early.UnixNano()); !got.Equal(sf) {
		t.Errorf("pencereden eski örnek → pencere başı: %v", got)
	}
}

func TestSumAcceptedIn(t *testing.T) {
	ns := func(h, m int) int64 { return time.Date(2026, 9, 17, h, m, 0, 0, time.UTC).UnixNano() }
	b := []chstore.IngestFleetBucket{{TimeNs: ns(19, 50), Accepted: 5}, {TimeNs: ns(19, 55), Accepted: 50}, {TimeNs: ns(20, 0), Accepted: 500}}
	if got := sumAcceptedIn(b, time.Date(2026, 9, 17, 19, 55, 0, 0, time.UTC), time.Date(2026, 9, 17, 20, 0, 0, 0, time.UTC)); got != 50 {
		t.Errorf("[19:55,20:00) → %d", got)
	}
}
