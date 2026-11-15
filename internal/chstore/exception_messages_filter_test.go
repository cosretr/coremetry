package chstore

import (
	"reflect"
	"strings"
	"testing"
)

// v0.10.1331 — CoSRE operasyon → Exceptions: ExceptionGroupFilter.Messages
// ex_message üzerinde BİREBİR küme (IN), ILIKE alt-dizesi değil; boşken
// cümle bayt-bayt eski.
func TestExceptionGroupWhereMessages(t *testing.T) {
	op := "LEND_PERSONAL_WEB_APPLICATIONTRX_DIRECT_CREDIT_PRE_APPROVAL_REST"
	wc := buildExceptionGroupWhere(ExceptionGroupFilter{Messages: []string{op}, Oracle: "only", ActiveFromNs: 100, Sort: "lastSeen"})
	sql := wc.sql()
	if !strings.Contains(sql, "ex_message IN (?)") {
		t.Fatalf("birebir ex_message kümesi yok:\n%s", sql)
	}
	if strings.Contains(sql, "ILIKE") {
		t.Fatalf("Messages alt-dize araması yapmamalı:\n%s", sql)
	}
	found := false
	for _, a := range wc.args {
		if reflect.DeepEqual(a, []string{op}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("operasyon kodu bağ argümanı değil: %#v", wc.args)
	}
	base := buildExceptionGroupWhere(ExceptionGroupFilter{Search: "x"})
	empty := buildExceptionGroupWhere(ExceptionGroupFilter{Search: "x", Messages: nil})
	if base.sql() != empty.sql() || strings.Contains(empty.sql(), "ex_message IN") {
		t.Fatalf("boş Messages cümleyi değiştirmemeli:\n%s", empty.sql())
	}
}
