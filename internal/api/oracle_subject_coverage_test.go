package api

// v0.10.999 — Oracle özne kapsamı ucu: yalnız admin, pencere kelepçesi,
// okuma sınırı ve kayıt pini. Sınıflamanın tablo testi internal/oracle'da.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/oracle"
)

func TestOracleCoverageHours(t *testing.T) {
	for raw, want := range map[string]int{"": 24, "24": 24, "1": 1, " 6 ": 6, "12": 24, "0": 24, "-1": 24, "168": 24, "x": 24} {
		if got := oracleCoverageHours(raw); got != want {
			t.Errorf("hours=%q → %d, istenen %d", raw, got, want)
		}
	}
}

func TestOracleSubjectCoverageRouteGates(t *testing.T) {
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(8), stats: newCacheStats()}
	s.oracle = oracle.New()
	mux := http.NewServeMux()
	s.registerOracleSubjectCoverageRoutes(mux)
	get := func(role string) int {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, asRole(httptest.NewRequest("GET", "/api/settings/oracle/s1/subject-coverage", nil), role))
		return w.Code
	}
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		if code := get(role); code != http.StatusForbidden {
			t.Errorf("rol %s → %d, 403 beklenir (operasyon kodları + pod adları admin bilgisidir)", role, code)
		}
	}
	if code := get(auth.RoleAdmin); code != http.StatusServiceUnavailable {
		t.Errorf("depo yokken %d, 503 beklenir", code)
	}
}

func TestOracleSubjectCoverageSourcePins(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	st := read("../chstore/oracle_op_coverage.go")
	if strings.Count(st, "FROM oracle_error_log FINAL") != 2 || strings.Count(st, "SETTINGS max_execution_time = 10") != 2 ||
		strings.Count(st, "WHERE source_id = ? AND time >= ? AND time < ?") != 2 || !strings.Contains(st, "LIMIT ?") {
		t.Error("OracleOpCoverage: iki okuma da kaynak + zaman sınırlı, FINAL, max_execution_time; döküm LIMIT taşımalı")
	}
	h := read("oracle_subject_coverage.go")
	for _, w := range []string{
		`registerRoutesExtra("oracle-subject-coverage"`,
		`auth.RequireRole(auth.RoleAdmin, s.getOracleSubjectCoverage)`,
		`fmt.Sprintf("oracle-subject-coverage:id=%s:h=%d", id, hours)`,
		"s.serveCached(w, r, key, 60*time.Second",
		"oracle.BuildCoverage(obs, totals, learned, alive, now, oracleCoverageUnresolved)",
	} {
		if !strings.Contains(h, w) {
			t.Errorf("oracle_subject_coverage.go %q taşımalı", w)
		}
	}
}
