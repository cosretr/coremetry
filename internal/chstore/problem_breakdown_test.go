package chstore

// problem_breakdown_test.go — v0.10.2388 ("Hata nerede" bölümü). Sözleşme:
//
//   - RouteNormalErrRate: rotanın olağan hata oranı dedektörün taban diliminin
//     MEDYANIDIR; trafiği olup hata serisi olmayan kova "hata yok"tur (0),
//     eksik sayılmaz. Örnek yetersizse oran BİLİNMİYOR (uydurulmaz).
//   - Dört okuma da sınırlıdır: servis + zaman WHERE, LIMIT, max_execution_time.

import (
	"math"
	"strings"
	"testing"
)

func TestRouteNormalErrRate(t *testing.T) {
	cases := []struct {
		name string
		b    MLRouteBaseline
		min  int
		want float64
		ok   bool
	}{
		{"taban yok", MLRouteBaseline{}, 5, 0, false},
		{"örnek yetersiz", MLRouteBaseline{CountPos: 4, ErrRatios: []float64{0.1, 0.2}}, 5, 0, false},
		{"hata serisi hiç yok, trafik var → %0", MLRouteBaseline{CountPos: 30}, 5, 0, true},
		{"tek sayıda örnek: ortadaki", MLRouteBaseline{CountPos: 5, ErrRatios: []float64{0.05, 0.01, 0.03, 0.02, 0.04}}, 5, 0.03, true},
		{"çift sayıda örnek: iki ortancanın ortalaması", MLRouteBaseline{CountPos: 6, ErrRatios: []float64{0.01, 0.02, 0.03, 0.04, 0.05, 0.06}}, 5, 0.035, true},
		{"hata serisi olmayan kovalar sıfırla doldurulur", MLRouteBaseline{CountPos: 6, ErrRatios: []float64{0.3, 0.2}}, 5, 0, true},
		{"sıfır dolgusu medyanı aşağı çeker ama sıfırlamaz", MLRouteBaseline{CountPos: 5, ErrRatios: []float64{0.4, 0.2, 0.3}}, 5, 0.2, true},
		{"hata örneği trafik kovasından çoksa dolgu yok", MLRouteBaseline{CountPos: 2, ErrRatios: []float64{0.1, 0.1, 0.1, 0.1, 0.5}}, 5, 0.1, true},
		{"bozuk örnek atılır", MLRouteBaseline{CountPos: 5, ErrRatios: []float64{math.NaN(), -1, 0.02, 0.02, 0.02, 0.02, 0.02}}, 5, 0.02, true},
		{"oran 1'i aşamaz", MLRouteBaseline{CountPos: 5, ErrRatios: []float64{3, 3, 3, 3, 3}}, 5, 1, true},
		{"min ≤ 0 → en az bir örnek", MLRouteBaseline{CountPos: 1}, 0, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := RouteNormalErrRate(c.b, c.min)
			if ok != c.ok || math.Abs(got-c.want) > 1e-12 {
				t.Fatalf("got %v ok=%v, want %v ok=%v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestProblemBreakdownSQLBounded(t *testing.T) {
	cases := []struct {
		name string
		q    string
		want []string
		deny []string
	}{
		{"rota tabanı", mlRouteBaselineSQL(), []string{"FROM ml_series", "signal = ?", "service = ?", "bucket >= ?", "toDate(bucket, ?) IN (?)",
			"endsWith(part, '|count')", "endsWith(part, '|err_ratio')", "NOT startsWith(part, 'dep|')", "LIMIT ?", "max_execution_time = 5"}, nil},
		{"exception türleri (kolonlu)", serviceErrorExceptionTypesSQL(true, "optimize_skip_unused_shards = 0"), []string{"FROM spans", "service_name = ?",
			"time >= ? AND time <= ?", "status_code = 'error'", "ex_match = 1", "GROUP BY ex_type", "LIMIT ?", "max_execution_time = 5", "optimize_skip_unused_shards = 0"}, nil},
		{"exception türleri (kolonsuz)", serviceErrorExceptionTypesSQL(false, "optimize_skip_unused_shards = 1"), []string{"FROM spans", "service_name = ?",
			"time >= ? AND time <= ?", "status_code = 'error'", exMatchPred, "LIMIT ?", "max_execution_time = 5"}, nil},
		{"tür başına en yeni grup", exceptionGroupsByTypeSQL(), []string{"FROM exception_groups FINAL", "service = ?", "ex_type IN (?)", "last_seen >= toDateTime64(?, 9, 'UTC')",
			NotOracleGroupSQL, "argMax(fingerprint, last_seen)", "LIMIT ?", "max_execution_time = 5"}, nil},
		{"bağımlılık olayları", serviceDependencyEventsSQL(), []string{"FROM ml_events FINAL", "service = ?", "'dependency_error_rate'", "'dependency_latency'",
			"last_seen >= ?", "started_at <= ?", "LIMIT ?", "max_execution_time = 5"}, []string{"evidence", "record"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, w := range c.want {
				if !strings.Contains(c.q, w) {
					t.Errorf("sorguda %q yok:\n%s", w, c.q)
				}
			}
			for _, d := range c.deny {
				if strings.Contains(c.q, d) {
					t.Errorf("sorgu %q okumamalı (ağır kolon):\n%s", d, c.q)
				}
			}
		})
	}
}

func TestDependencyTargetOf(t *testing.T) {
	cases := []struct{ code, fam, target string }{
		{"db|orders_db", "db", "orders_db"},
		{"messaging|checkout.events", "messaging", "checkout.events"},
		{"http|customer-rel:8443", "http", "customer-rel:8443"},
		{"db|a|b", "db", "a|b"},
		{"orders_db", "", ""},
		{"db|", "", ""},
		{"|x", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		fam, target := DependencyTargetOf(c.code)
		if fam != c.fam || target != c.target {
			t.Errorf("%q → (%q, %q), want (%q, %q)", c.code, fam, target, c.fam, c.target)
		}
	}
}
