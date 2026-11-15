package chstore

// business_code_summary_5m — biçim kapısı ve kapsamsız okuma anahtarı.

import (
	"regexp"
	"strings"
	"testing"
)

func TestFunctionCodeShape(t *testing.T) {
	re := regexp.MustCompile(FunctionCodeShape)
	for _, ok := range []string{"BQRZ001", "IQV0181", "CQC4013", "CXSPRF1", "BQR1614"} {
		if !re.MatchString(ok) {
			t.Errorf("%s kod sayılmalı", ok)
		}
	}
	// prod ölçümünün (2026-10-15) kuyruğu: GUID, sayısal ID, kod+ID karışımı.
	for _, bad := range []string{"1b2a827c-e08d-41a7-857a-000000000000", "52393568", "LOGIN-43930337", "PAY_WITHQR-50927278", "48684888-REGULAR_GAIN", "060203hagA0026", "bkrb001"} {
		if re.MatchString(bad) {
			t.Errorf("%s kod SAYILMAMALI (kardinalite)", bad)
		}
	}
}

func TestBusinessCodeMVDDL(t *testing.T) {
	d := businessCodeMVDDL()
	for _, want := range []string{
		"ORDER BY (kind, code, service_name, time_bucket)",
		"TTL toDate(time_bucket) + INTERVAL 30 DAY",
		"'" + FunctionCodeShape + "'",
		"indexOf(attr_keys, 'FUNCTION_CODE')", "indexOf(attr_keys, 'function_code')",
		"indexOf(attr_keys, 'CHANNEL_CODE')",
		"arrayFilter(x -> x.2 != ''",
		"countIfState(status_code = 'error')",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("DDL %q içermiyor", want)
		}
	}
	if strings.Contains(d, "attr_function_code") {
		t.Error("MV materialized kolona dayanmamalı (dış Distributed'da yok)")
	}
}
