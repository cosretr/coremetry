package logstore

import (
	"strings"
	"testing"
)

// v0.10.1988 — mesajı boş, içeriği traceRecords dizisinde olan kayıt okunur gövdeye çevrilir.
func TestTraceRecordsBody(t *testing.T) {
	src := map[string]any{"traceRecords": []any{
		map[string]any{"message": "{clientIp:10.0.0.1}", "metadata": map[string]any{"Type": "ClientIp"}},
		map[string]any{"message": "{request:{\"functionCode\":\"DASHBOARD\"}}", "metadata": map[string]any{"Type": "Request"}},
		map[string]any{"message": "", "metadata": map[string]any{"Type": "Empty"}},
		"not-an-object",
	}}
	got := traceRecordsBody(src)
	want := "ClientIp: {clientIp:10.0.0.1}\nRequest: {request:{\"functionCode\":\"DASHBOARD\"}}"
	if got != want {
		t.Fatalf("got %q", got)
	}
	if traceRecordsBody(map[string]any{"traceRecords": `[{"message":"x","metadata":{"Type":"T"}}]`}) != "T: x" {
		t.Fatal("JSON dizgesi okunmalı")
	}
	if traceRecordsBody(map[string]any{"message": "m"}) != "" {
		t.Fatal("alan yoksa boş")
	}
	big := []any{}
	for i := 0; i < 20; i++ {
		big = append(big, map[string]any{"message": strings.Repeat("a", 3000)})
	}
	if b := traceRecordsBody(map[string]any{"traceRecords": big}); len(b) > traceRecordsMaxTotal+10 {
		t.Fatalf("tavan: %d", len(b))
	}
}

// v0.10.1992 — dar listede ES script özeti: istek alanı + isabetten okuma.
func TestTraceRecordsSummaryField(t *testing.T) {
	sf := traceRecordsScriptFields()
	if _, ok := sf["cm_tr_summary"]; !ok {
		t.Fatal("script alanı adı")
	}
	if got := traceRecordsSummaryField(map[string]any{"cm_tr_summary": []any{"ClientIp: x | Request: y"}}); got != "ClientIp: x | Request: y" {
		t.Fatalf("got %q", got)
	}
	if traceRecordsSummaryField(map[string]any{}) != "" || traceRecordsSummaryField(nil) != "" {
		t.Fatal("yoksa boş")
	}
}
