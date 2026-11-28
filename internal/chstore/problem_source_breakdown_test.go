package chstore

import (
	"strings"
	"testing"
)

func TestProblemSourceFamily(t *testing.T) {
	cases := []struct {
		name, ruleID, metric, want string
	}{
		{"ML grup", "anomaly-auto:group:ev-1", "ml_group_signals", SrcFamilyMLGroup},
		{"ML grup lag", "anomaly-auto:group:ev-1", "ml_group_lag", SrcFamilyMLLag},
		{"ML grup lag deseni", "anomaly-auto:group:ev-1", "ml_group_lag_pattern", SrcFamilyMLLag},
		{"ML OOM", "anomaly-auto:group:ev-2", "oom_kills", SrcFamilyMLOOM},
		{"ML servis", "anomaly-auto:svc:svc-a", "error_rate", SrcFamilyMLSvc},
		{"ML özne", "anomaly-auto:subject:svc-a:p99_ms:up", "anomaly_ratio", SrcFamilyMLSubject},
		{"ML tekil terfi", "anomaly-auto:abcdef0123456789", "anomaly_ratio", SrcFamilyMLSingle},
		{"dış kaynak kapalı", "anomaly:ext-down:src-1", "ext:up", SrcFamilyExternal},
		{"dış kaynak tavan", "anomaly:ext-cap:ext:src-1:ext:fail_count", "ext:fail_count", SrcFamilyExternal},
		{"dış kaynak küme", "anomaly-cluster:ext:src-1", "ext:error_count", SrcFamilyExternal},
		{"dış kaynak seri", "anomaly:ext:src/OP/E:ext:error_count", "ext:error_count", SrcFamilyExternal},
		{"anomali kümesi", "anomaly-cluster:svc-a", "error_rate", SrcFamilyAnomalyCluster},
		{"klasik anomali", "anomaly:svc-a:error_rate", "error_rate", SrcFamilyAnomaly},
		{"yaygın yavaşlama", "svc-slowdown:svc-a", "p99_ms", SrcFamilySvcSlowdown},
		{"exception fırtınası", "exception-storm", "", SrcFamilyException},
		{"exception ortak bağımlılık", "exception:shared-dependency", "", SrcFamilyException},
		{"exception grubu", "exception-group:fp1", "", SrcFamilyException},
		{"db sağlık", "db-health:oracle@h/d", "db.error_pct", SrcFamilyDB},
		{"db kapasite", "db-capacity:oracle@h/d", "db.capacity", SrcFamilyDB},
		{"db yavaş ifade", "db-slow-stmt", "db.statement_p95_ms", SrcFamilyDB},
		{"runtime gc", "runtime:jvm-gc", "runtime.jvm_gc", SrcFamilyRuntime},
		{"runtime dotnet", "runtime:dotnet-gc-pressure", "runtime.dotnet_gc_pressure", SrcFamilyRuntime},
		{"monitor", "monitor:m-1", "probe", SrcFamilyMonitor},
		{"self", "self-ingest-stall", "self.ingest_stall", SrcFamilySelf},
		{"slo", "slo:slo-a:critical", "burn_rate", SrcFamilySLO},
		{"yerleşik", "builtin-warn-http-p99-3s", "http_p99_ms", SrcFamilyBuiltin},
		{"kullanıcı kuralı hex", "0123456789abcdef", "error_rate", SrcFamilyUserRule},
		{"kullanıcı kuralı uuid", "0a1b2c3d-0000-4000-8000-0123456789ab", "http_p99_ms", SrcFamilyUserRule},
		{"incident", "incident:critical", "", SrcFamilyIncident},
		{"bilinmeyen öneksiz kimlik", "rule-custom-x", "custom", SrcFamilyOther},
		{"büyük harf hex kullanıcı kimliği değil", "0123456789ABCDEF", "error_rate", SrcFamilyOther},
		{"boş", "", "", SrcFamilyOther},
	}
	for _, c := range cases {
		if got := ProblemSourceFamily(c.ruleID, c.metric); got != c.want {
			t.Errorf("%s: ProblemSourceFamily(%q, %q) = %q, want %q", c.name, c.ruleID, c.metric, got, c.want)
		}
	}
}

// SQL ikizi aynı tablodan, aynı sırayla derlenir: her kural koşulu
// ifadede ve bir öncekinden SONRA geçer; varsayılan "other".
func TestProblemSourceFamilySQLMirrorsTable(t *testing.T) {
	sql := problemSourceFamilySQL()
	if !strings.HasPrefix(sql, "multiIf(") || !strings.HasSuffix(sql, ", 'other')") {
		t.Fatalf("multiIf biçimi bozuk: %s", sql)
	}
	at := 0
	for _, r := range problemSourceRules {
		frag := r.sqlCond() + ", " + sqlStr(r.Family)
		i := strings.Index(sql[at:], frag)
		if i < 0 {
			t.Fatalf("kural SQL'de yok ya da sırası kaymış: %s", frag)
		}
		at += i + len(frag)
	}
	if !strings.Contains(sql, "metric IN ('oom_kills')") {
		t.Error("metrik koşullu kural SQL'de metrik süzgecini taşımalı")
	}
	if sqlStr(`a'b\c`) != `'a\'b\\c'` {
		t.Errorf("sqlStr kaçışı: %s", sqlStr(`a'b\c`))
	}
}

func TestProblemSourceBreakdownSQLBounded(t *testing.T) {
	sql := problemSourceBreakdownSQL(3600)
	for _, want := range []string{
		"FROM problems AS p FINAL",
		// v0.10.2484 — Problem katmanı vaka etiketi: aynı taramada tek LEFT JOIN.
		"LEFT JOIN (" + problemCaseLabelSQL + ") AS c ON c.source_id = p.id",
		"toUInt64(countIf(has_case AND case_ok))     AS case_correct",
		"WHERE started_at >= ? AND started_at < ?",
		"LIMIT 100000",
		"max_execution_time = 10",
		"max_rows_to_read = 20000000",
		"read_overflow_mode = 'break'",
		"GROUPING SETS ((fam), (fam, m), (fam, svc), (bkt))",
		"intDiv(start_s, 3600) * 3600 AS bkt",
		problemSourceFamilySQL() + " AS fam",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q içermeli", want)
		}
	}
	if strings.Count(sql, "FROM problems") != 1 || strings.Count(sql, "FROM incident_cases") != 1 {
		t.Error("tek tarama: problems ve incident_cases birer kez okunmalı")
	}
}

func TestProblemSourceAggRowSet(t *testing.T) {
	cases := []struct {
		r    ProblemSourceAggRow
		want string
	}{
		{ProblemSourceAggRow{Bucket: 86400}, "series"},
		{ProblemSourceAggRow{Family: "ml_svc", Metric: "error_rate"}, "metric"},
		{ProblemSourceAggRow{Family: "ml_svc", Service: "svc-a"}, "service"},
		{ProblemSourceAggRow{Family: "ml_svc", Service: "-"}, "service"},
		{ProblemSourceAggRow{Family: "ml_svc"}, "family"},
	}
	for _, c := range cases {
		if got := c.r.Set(); got != c.want {
			t.Errorf("%+v.Set() = %q, want %q", c.r, got, c.want)
		}
	}
}
