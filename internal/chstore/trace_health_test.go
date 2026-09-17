package chstore

// trace_health_test.go — v0.10.757: üç sorgunun sınır sözleşmesi (MV
// kaynağı, zaman sınırı, GROUP BY, LIMIT/bütçe) ve çıplak fiil kümesi
// (templater.httpMethods ile aynı dokuz fiil; templater chstore'u import
// ettiği için çapraz pin buradan değil, literal kümeden).

import (
	"regexp"
	"strings"
	"testing"
)

func TestTraceHealthSQLBounded(t *testing.T) {
	cases := []struct {
		name  string
		sql   string
		wants []string
	}{
		{"stored", storedSpanBucketsSQL(), []string{"FROM service_summary_5m", "time_bucket >= toDateTime(?, 'UTC') AND time_bucket < toDateTime(?, 'UTC')", "GROUP BY time_bucket", "LIMIT 2000", "max_execution_time = 5"}},
		{"names", operationNameQualitySQL(), []string{"FROM operation_summary_5m", "time_bucket >= toDateTime(?, 'UTC')", "GROUP BY name", "match(name, '^(GET|POST|", "name = ''", "max_execution_time = 10"}},
		{"cardinality", operationNameCardinalitySQL(), []string{"FROM operation_summary_5m", "uniqExact(name)", "GROUP BY service_name", "ORDER BY n DESC", "LIMIT 10", "max_execution_time = 10"}},
	}
	for _, c := range cases {
		for _, w := range c.wants {
			if !strings.Contains(c.sql, w) {
				t.Errorf("%s: %q yok:\n%s", c.name, w, c.sql)
			}
		}
		if strings.Contains(c.sql, "FROM spans") {
			t.Errorf("%s: ham spans'a dokunmamalı (MV-first)", c.name)
		}
	}
}

func TestBareHTTPMethodRegexMatchesTemplater(t *testing.T) {
	re := regexp.MustCompile(bareHTTPMethodRe)
	for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "TRACE", "CONNECT"} {
		if !re.MatchString(m) {
			t.Errorf("%s eşleşmedi", m)
		}
	}
	for _, m := range []string{"POST /a", "SELECT", "", "post"} {
		if re.MatchString(m) {
			t.Errorf("%q çıplak fiil sayıldı", m)
		}
	}
}
