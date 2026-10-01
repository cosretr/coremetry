package chstore

import (
	"strings"
	"testing"
	"time"
)

// v0.10.1000 — fonksiyon kodu → servis okuması (function_code_services.go):
// iki yolun SQL'i zaman sınırlı + LIMIT + max_execution_time taşır, kodlar
// bind-arg; kademe pencereye göre seçilir; kod listesi temizlenir ve tavanlıdır.

func TestFunctionCodeSQLBounds(t *testing.T) {
	cases := []struct {
		name, sql string
		want      []string
	}{
		{"rollup", functionCodeRollupSQL("rollup_spans_wide_1m", 3), []string{
			"FROM rollup_spans_wide_1m", "ts >= ? AND ts < ?", "function_code IN (?,?,?)",
			"sum(span_count)", "sum(error_count)", "GROUP BY function_code, service_name",
			"LIMIT 5000", "SETTINGS max_execution_time = 5"}},
		{"spans", functionCodeSpansSQL("attr_function_code", 2), []string{
			"FROM spans", "time >= ? AND time < ?", "attr_function_code IN (?,?)",
			"countIf(status_code = 'error')", "GROUP BY fc, service_name",
			"LIMIT 5000", "SETTINGS max_execution_time = 5"}},
	}
	for _, c := range cases {
		for _, w := range c.want {
			if !strings.Contains(c.sql, w) {
				t.Errorf("%s: SQL %q içermeli:\n%s", c.name, w, c.sql)
			}
		}
		if strings.Contains(c.sql, "coremetry.") {
			t.Errorf("%s: tablo adı niteliksiz olmalı", c.name)
		}
	}
}

func TestFunctionCodeRollupTable(t *testing.T) {
	for _, c := range []struct {
		window time.Duration
		want   string
	}{
		{35 * time.Minute, "rollup_spans_wide_1m"},
		{3 * time.Hour, "rollup_spans_wide_1m"},
		{6 * time.Hour, "rollup_spans_wide_5m"},
		{24 * time.Hour, "rollup_spans_wide_5m"},
	} {
		if got := functionCodeRollupTable(c.window); got != c.want {
			t.Errorf("%s: %s, beklenen %s", c.window, got, c.want)
		}
	}
}

func TestCleanFunctionCodes(t *testing.T) {
	got := cleanFunctionCodes([]string{" F1 ", "", "F2", "F1", "  "})
	if strings.Join(got, ",") != "F1,F2" {
		t.Fatalf("temizlik: %v", got)
	}
	many := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		many = append(many, "F"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+string(rune('0'+i%10))+string(rune('A'+(i/26)%26)))
	}
	if got := cleanFunctionCodes(many); len(got) > functionCodeMaxCodes {
		t.Fatalf("tavan: %d", len(got))
	}
}
