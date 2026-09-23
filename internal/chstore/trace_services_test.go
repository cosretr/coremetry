package chstore

import (
	"strings"
	"testing"
)

// v0.10.768 — Oracle test özetinin CH ayağı: SQL sınırlı (iki zaman sınırı,
// IN listesi, LIMIT, bütçe) ve id tavanı. v0.10.892 — seçim: en geç başlayan
// HATA span'ının servisi (argMaxIf … status_code = 'error') → kök → herhangi;
// kök tek başına "hep aynı kanal servisleri" veriyordu.
func TestTraceServicesSQLBounded(t *testing.T) {
	q := traceServicesSQL(3)
	for _, must := range []string{"FROM spans", "time >= ?", "time <= ?", "trace_id IN (?,?,?)", "LIMIT 200", "max_execution_time",
		"argMaxIf(service_name, time, status_code = 'error') AS err_svc", "anyIf(service_name, parent_id = '')"} {
		if !strings.Contains(q, must) {
			t.Errorf("%q yok:\n%s", must, q)
		}
	}
	if strings.Count(q, "?") != 5 {
		t.Errorf("5 bind bekleniyordu: %s", q)
	}
	if strings.Index(q, "AS err_svc") > strings.Index(q, "AS root_svc") || strings.Index(q, "AS any_svc") > strings.Index(q, "AS ex_type") {
		t.Error("kolon sırası err_svc, root_svc, any_svc, ex_type (Scan sırası)")
	}
	// v0.10.895 — ex_type: MATERIALIZED kolon varsa doğrudan, yoksa JSON_VALUE.
	if q2 := traceFactsSQL(1, exFragments(true)); !strings.Contains(q2, "argMaxIf(ex_type, time, ex_match = 1)") {
		t.Errorf("ex kolonlu biçim: %s", q2)
	}
	if !strings.Contains(q, "JSON_VALUE") || strings.Count(q, "?") != 5 {
		t.Errorf("kolonsuz biçim JSON_VALUE'ya düşer, bind sayısı değişmez: %s", q)
	}
}

func TestPickTraceService(t *testing.T) {
	for _, c := range []struct{ e, r, a, want string }{
		{"loan-svc", "apigateway", "x", "loan-svc"}, {"", "apigateway", "x", "apigateway"}, {"", "", "x", "x"}, {"", "", "", ""},
	} {
		if got := pickTraceService(c.e, c.r, c.a); got != c.want {
			t.Errorf("(%q,%q,%q) → %q, want %q", c.e, c.r, c.a, got, c.want)
		}
	}
}
