package chstore

import (
	"regexp"
	"strings"
	"testing"
)

var fromRawSpansRe = regexp.MustCompile(`(?i)\bfrom\s+spans\b`)

// Login özeti sorguları: süre sınırlı, LIMIT'li, ham `spans` YOK (invariant #3).
func TestLoginSummarySQLBounds(t *testing.T) {
	cases := map[string]string{
		"spansToday":  loginSpansTodaySQL(),
		"tracesToday": loginTracesTodaySQL(),
		"services":    loginServicesSQL(),
		"cosre":       cosreQuestionsSQL(),
		"endpoints":   loginEndpointsSQL(),
		"dbNames":     loginDBNamesSQL(),
		"bytes":       totalUncompressedBytesSQL(""),
		"bytesCl":     totalUncompressedBytesSQL("c1"),
	}
	for name, sql := range cases {
		if !strings.Contains(sql, "max_execution_time") {
			t.Errorf("%s: max_execution_time yok", name)
		}
		if !strings.Contains(sql, "LIMIT") {
			t.Errorf("%s: LIMIT yok", name)
		}
		if fromRawSpansRe.MatchString(sql) || strings.Contains(sql, " FROM spans") {
			t.Errorf("%s: ham spans okuması", name)
		}
	}
}

func TestLoginSummarySQLSources(t *testing.T) {
	if !strings.Contains(loginSpansTodaySQL(), "service_summary_5m") || !strings.Contains(loginSpansTodaySQL(), "toStartOfDay(now())") {
		t.Error("spans bugün: service_summary_5m + CH günü")
	}
	if !strings.Contains(loginTracesTodaySQL(), "trace_summary_1d") || !strings.Contains(loginTracesTodaySQL(), "day = today()") {
		t.Error("traces bugün: trace_summary_1d")
	}
	if s := loginServicesSQL(); !strings.Contains(s, "uniq(service_name)") || !strings.Contains(s, "coremetry-") {
		t.Error("servis sayısı: uniq + self servis dışlaması")
	}
	// v0.10.1915 — endpoint = giriş span'i (server/consumer), MV'den, 1 saat, self servis hariç.
	if s := loginEndpointsSQL(); !strings.Contains(s, "FROM spanmetrics_1m") || !strings.Contains(s, "kind IN ('server', 'consumer')") ||
		!strings.Contains(s, "toIntervalHour(1)") || !strings.Contains(s, "coremetry-") {
		t.Errorf("endpoint sayısı: spanmetrics_1m + giriş türleri + 1 sa + self dışlama: %s", s)
	}
	if !strings.Contains(cosreQuestionsSQL(), "tier != 'explain'") {
		t.Error("cosre: ✨ explain soru sayılmaz")
	}
}

// Küme kipinde cluster(...), değilse system.parts; güvensiz ad gömülmez.
func TestTotalUncompressedBytesSQLClusterMode(t *testing.T) {
	if s := totalUncompressedBytesSQL(""); strings.Contains(s, "cluster(") || !strings.Contains(s, "FROM system.parts") {
		t.Errorf("tek düğüm: system.parts beklenir: %s", s)
	}
	if s := totalUncompressedBytesSQL("prod_c1"); !strings.Contains(s, "cluster('prod_c1', system.parts)") {
		t.Errorf("küme kipi: cluster( beklenir: %s", s)
	}
	if s := totalUncompressedBytesSQL("x'); DROP TABLE users; --"); strings.Contains(s, "DROP") || strings.Contains(s, "cluster(") {
		t.Errorf("güvensiz küme adı gömülmemeli: %s", s)
	}
	if s := totalUncompressedBytesSQL("c1"); !strings.Contains(s, "active = 1") || !strings.Contains(s, "currentDatabase()") {
		t.Errorf("yalnız aktif parçalar, geçerli veritabanı: %s", s)
	}
}
