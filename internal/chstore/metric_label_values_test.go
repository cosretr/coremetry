package chstore

import (
	"strings"
	"testing"
)

// v0.10.868 (scale-audit 09-23) — etiket değeri önerisi: q sunucuda süzülür
// (positionCaseInsensitive), LIMIT bağlı; q boşken yüklem YOK (eski şekil).
func TestMetricLabelValuesSQLSubstringAndLimit(t *testing.T) {
	plain := metricLabelValuesSQL("attr_values[indexOf(attr_keys, ?)]", false)
	if strings.Contains(plain, "positionCaseInsensitive") || !strings.Contains(plain, "LIMIT ?") || !strings.Contains(plain, "max_execution_time = 5") {
		t.Fatalf("q'suz şekil: %s", plain)
	}
	withQ := metricLabelValuesSQL("attr_values[indexOf(attr_keys, ?)]", true)
	if !strings.Contains(withQ, "AND positionCaseInsensitive(attr_values[indexOf(attr_keys, ?)], ?) > 0") {
		t.Fatalf("q yüklemi yok: %s", withQ)
	}
	if strings.Count(withQ, "?") != strings.Count(plain, "?")+2 {
		t.Fatalf("q'lu şekil expr'i tekrar bağlar + q: %s", withQ)
	}
}

// v0.10.1102 — Kafka seçicisi sayfa kapsamında: panel süzgeçleri zaman
// yükleminden sonra, q'dan önce; koşulsuz şekil eskisiyle bayt-aynı.
func TestMetricLabelValuesScopedSQL(t *testing.T) {
	expr := "attr_values[indexOf(attr_keys, ?)]"
	if metricLabelValuesScopedSQL(expr, true, nil) != metricLabelValuesSQL(expr, true) {
		t.Fatal("koşulsuz şekil eski SQL ile aynı olmalı")
	}
	var wc whereClause
	ApplyMetricFilters(&wc, []FilterExpr{
		{Key: "service.name", Op: "IN", Values: []string{"svc-a", "svc-b"}},
		{Key: "topic", Op: "=", Values: []string{"orders"}},
	})
	got := metricLabelValuesScopedSQL(expr, true, wc.conds)
	iSvc := strings.Index(got, "service_name")
	iTime := strings.Index(got, "time <= ?")
	iQ := strings.Index(got, "positionCaseInsensitive")
	if iSvc < 0 || !(iTime < iSvc && iSvc < iQ) {
		t.Fatalf("süzgeç sırası (zaman < süzgeç < q) bozuk:\n%s", got)
	}
	if len(wc.conds) != 2 || len(wc.args) == 0 {
		t.Fatalf("iki süzgeç koşulu bekleniyordu: %v %v", wc.conds, wc.args)
	}
}
