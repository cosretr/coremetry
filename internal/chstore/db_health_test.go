package chstore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// v0.10.1073 — veritabanı sağlık kuralı (db-health): okuma SQL'leri (golden +
// sınırlar), ayar varsayılanları/normalizasyonu, kural id ↔ özne sözleşmesi.

func ptrStrings(s []string) *[]string { return &s }

func TestDBHealthBucketsSQLGolden(t *testing.T) {
	since := time.Date(2026, 10, 2, 21, 30, 0, 0, time.UTC)
	until := since.Add(15 * time.Minute)
	cfg := DefaultDBHealth()
	sens := AnomalySensitivityConfig{BatchServicePatterns: ptrStrings([]string{"-batch", "nightly-job"})}
	open := []string{"db-health:oracle@db-host-01/crm-db"}
	sql, args := dbHealthBucketsQuery(since, until, cfg, sens, open)

	want := `
		SELECT db_system, instance, db_name, time_bucket,
		       sum(c_calls)                                                       AS calls,
		       sum(c_errs)                                                        AS errs,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(c_q), 3) / 1e6 AS p99_ms,
		       count()                                                            AS callers,
		       countIf(c_affected)                                                AS affected,
		       arraySlice(arrayMap(x -> x.1, arrayReverseSort(x -> x.2,
		         groupArrayIf((service_name, c_calls), c_affected))), 1, 3)       AS top_callers
		FROM (
			SELECT db_system, instance, db_name, service_name, time_bucket,
			       countMerge(span_count_state)                                  AS c_calls,
			       countIfMerge(error_count_state)                               AS c_errs,
			       quantilesTDigestMergeState(0.5, 0.95, 0.99)(duration_q_state) AS c_q,
			       c_calls >= ? AND ((c_errs * 100 >= ? * c_calls)
			         OR (arrayElement(finalizeAggregation(c_q), 3) / 1e6 >= ?))  AS c_affected
			FROM db_caller_summary_5m
			WHERE time_bucket >= ? AND time_bucket < ?
			  AND NOT (positionCaseInsensitive(service_name, ?) > 0 OR positionCaseInsensitive(service_name, ?) > 0)
			GROUP BY db_system, instance, db_name, service_name, time_bucket
		)
		GROUP BY db_system, instance, db_name, time_bucket
		HAVING (errs * 100 >= ? * calls) OR (p99_ms >= ?)
		    OR concat('db-health:', lower(trimBoth(db_system)), '@', instance, '/', db_name) IN ?
		ORDER BY affected DESC, calls DESC
		LIMIT 20000
		SETTINGS max_execution_time = 10`
	if sql != want {
		t.Errorf("SQL golden'dan saptı:\n--- got ---\n%s\n--- want ---\n%s", sql, want)
	}
	if strings.Count(sql, "?") != len(args) {
		t.Fatalf("bind sayısı %d, argüman %d", strings.Count(sql, "?"), len(args))
	}
	wantArgs := []any{uint64(10), 5.0, 2000.0, since, until, "-batch", "nightly-job", 5.0, 2000.0}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Errorf("arg[%d] = %v, istenen %v", i, args[i], wantArgs[i])
		}
	}
	if got, ok := args[len(args)-1].([]string); !ok || len(got) != 1 || got[0] != open[0] {
		t.Errorf("son arg açık kural id dizisi olmalı: %#v", args[len(args)-1])
	}
	// Sözleşme: MV-first, ham spans yok, nitelenmemiş tablo, tek tDigest birleştirmesi.
	for _, bad := range []string{"FROM spans", "coremetry."} {
		if strings.Contains(sql, bad) {
			t.Errorf("SQL %q içermemeli", bad)
		}
	}
	if strings.Count(sql, "quantilesTDigestMerge(") != 1 {
		t.Error("iç sorgu çağıran p99'unu finalizeAggregation ile almalı (ikinci tDigest birleştirmesi yok)")
	}

	// Batch listesi boş (`[]` = kural kapalı) → batch dalı YOK; açık id yok → boş dizi (nil değil).
	off, offArgs := dbHealthBucketsQuery(since, until, cfg, AnomalySensitivityConfig{BatchServicePatterns: ptrStrings([]string{})}, nil)
	if strings.Contains(off, "positionCaseInsensitive") || len(offArgs) != 8 {
		t.Errorf("boş batch listesinde batch koşulu olmamalı: %d arg\n%s", len(offArgs), off)
	}
	if ids, ok := offArgs[7].([]string); !ok || ids == nil {
		t.Errorf("açık id yokken boş dizi bağlanmalı: %#v", offArgs[7])
	}
}

// Kural id'sinin SQL ikizi Go biçimiyle aynı parçaları aynı sırayla kurar.
func TestDBHealthRuleIDSQLTwin(t *testing.T) {
	if dbHealthRuleIDSQL != `concat('`+RuleDBHealthPrefix+`', lower(trimBoth(db_system)), '@', instance, '/', db_name)` {
		t.Errorf("SQL ikizi: %s", dbHealthRuleIDSQL)
	}
	if got := DBHealthRuleID(" Oracle ", "h", "d"); got != "db-health:oracle@h/d" {
		t.Errorf("Go biçimi: %s", got)
	}
}

func TestDBHealthReferenceSQLContract(t *testing.T) {
	sql := dbHealthReferenceSQL()
	for _, w := range []string{"FROM db_summary_5m", "time_bucket >= ? AND time_bucket < ?", dbHealthRuleIDSQL + " IN ?",
		"GROUP BY db_system, instance, db_name, time_bucket", "LIMIT 20000", "max_execution_time = 10"} {
		if !strings.Contains(sql, w) {
			t.Errorf("%q yok:\n%s", w, sql)
		}
	}
	if strings.Count(sql, "?") != 3 {
		t.Errorf("3 bind bekleniyor, %d", strings.Count(sql, "?"))
	}
}

func TestDBHealthConfigDefaultsAndNormalize(t *testing.T) {
	d := DefaultDBHealth()
	if !d.On() || d.ErrorPct != 5 || d.P99Ms != 2000 || d.P99RiseFactor != 3 || d.MinCallerCalls != 10 ||
		d.MinCalls != 100 || d.MinCallers != 2 || d.MaxNewPerTick != 20 {
		t.Fatalf("varsayılanlar spec ile uyuşmuyor: %+v", d)
	}
	// Eski blob (health alanı yok) → varsayılan, AÇIK.
	var old DBSlowQueryConfig
	if err := json.Unmarshal([]byte(`{"enabled":true,"thresholdMs":1000}`), &old); err != nil {
		t.Fatal(err)
	}
	n := NormalizeDBSlowQuery(old)
	if n.Health == nil || !n.Health.On() || n.Health.ErrorPct != 5 || n.Health.MinCallers != 2 || n.Health.MinCallerCalls != 10 {
		t.Fatalf("eski blob varsayılan sağlık ayarına düşmeli: %+v", n.Health)
	}
	// Kapatma gidiş-dönüşte korunur (*bool: false ≠ yok).
	off := false
	n.Health.Enabled = &off
	raw, _ := json.Marshal(n)
	var back DBSlowQueryConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if NormalizeDBSlowQuery(back).Health.On() {
		t.Errorf("kapatılmış kural gidiş-dönüşte açıldı: %s", raw)
	}
	// Sıfır/negatif alanlar varsayılana; hata % 100'de kesilir; kat < 1 varsayılana.
	h := NormalizeDBHealth(DBHealthConfig{ErrorPct: 250, P99Ms: -1, MinCallers: -3, P99RiseFactor: 0.5})
	if h.ErrorPct != 100 || h.P99Ms != 2000 || h.MinCalls != 100 || h.MinCallers != 2 || h.MaxNewPerTick != 20 ||
		h.P99RiseFactor != 3 || h.MinCallerCalls != 10 || h.Enabled == nil || !*h.Enabled {
		t.Errorf("normalize: %+v", h)
	}
	if k := NormalizeDBHealth(DBHealthConfig{P99RiseFactor: 1.5, MinCallerCalls: 1}); k.P99RiseFactor != 1.5 || k.MinCallerCalls != 1 {
		t.Errorf("geçerli değerler korunmalı: %+v", k)
	}
}

func TestDBHealthRuleIDRoundTripAndSubjectForm(t *testing.T) {
	cases := []struct {
		system, instance, db string
		wantSubject, form    string
	}{
		{"Oracle", "db-host-01", "crm-db", "db:oracle@crm-db", DBSubjectFormDBName},
		{"postgresql", "pg.example.internal", "default", "db:postgresql@pg.example.internal", DBSubjectFormInstance},
		{"oracle", "oracle", "ORDERS", "db:oracle@ORDERS", DBSubjectFormDBName},
	}
	for _, c := range cases {
		id := DBHealthRuleID(c.system, c.instance, c.db)
		if !strings.HasPrefix(id, RuleDBHealthPrefix) {
			t.Fatalf("önek yok: %s", id)
		}
		sys, inst, db, ok := ParseDBHealthRuleID(id)
		if !ok || sys != strings.ToLower(c.system) || inst != c.instance || db != c.db {
			t.Errorf("gidiş-dönüş %s → %q %q %q %v", id, sys, inst, db, ok)
		}
		if got := DBHealthSubject(c.system, c.instance, c.db); got != c.wantSubject {
			t.Errorf("özne %q, istenen %q", got, c.wantSubject)
		}
		// Liste işareti / detay kartı biçimi özneyle AYNI kuraldan.
		if got := DBProblemSubjectForm(id); got != c.form {
			t.Errorf("%s biçimi %q, istenen %q", id, got, c.form)
		}
	}
	for _, bad := range []string{"db-health:", "db-health:@x/y", "db-health:oracle@x", "db-health:oracle@x/", "db-slow-stmt"} {
		if _, _, _, ok := ParseDBHealthRuleID(bad); ok {
			t.Errorf("%q çözülmemeli", bad)
		}
	}
}

func TestDBHealthMetricCategory(t *testing.T) {
	if got := ProblemCategory(Problem{RuleID: DBHealthRuleID("oracle", "h", "d"), Metric: DBHealthMetricErrorPct}); got != CategoryError {
		t.Errorf("hata boyutu kategorisi %q", got)
	}
	if got := ProblemCategory(Problem{RuleID: DBHealthRuleID("oracle", "h", "d"), Metric: DBHealthMetricP99Ms}); got != CategorySlowdown {
		t.Errorf("p99 boyutu kategorisi %q", got)
	}
}
