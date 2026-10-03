package chstore

import (
	"reflect"
	"strings"
	"testing"
)

// v0.10.1071 — `/logs?pattern=` desen süzgeci liste okumasına (GetLogs →
// logsWhere) ulaşır: logstore'un hazır kurduğu yüklem (dedektörün
// chPatternMatchSQL'i) olduğu gibi AND'lenir, regex bağı yerinde; serbest
// metinle birlikte ikisi de durur. Boş PatternSQL hiçbir iz bırakmaz.
func TestLogsWhere_PatternConjunct(t *testing.T) {
	pred := "(multiSearchAnyCaseInsensitive(body, ['disk full', 'enospc']) AND match(body, ?))"
	wc := logsWhere(LogFilter{Search: "orders", PatternSQL: pred, PatternArgs: []any{"disk full|ENOSPC"}})
	sql := wc.sql()
	if !strings.Contains(sql, pred) {
		t.Fatalf("yüklem yok: %s", sql)
	}
	if !strings.Contains(sql, "multiSearchAnyCaseInsensitive(body, [?])") {
		t.Fatalf("serbest metin düştü: %s", sql)
	}
	if !reflect.DeepEqual(wc.args[len(wc.args)-1], "disk full|ENOSPC") || strings.Count(sql, "?") != len(wc.args) {
		t.Fatalf("bağlar: %v (%s)", wc.args, sql)
	}
	plain := logsWhere(LogFilter{Search: "orders"})
	if s := plain.sql(); strings.Contains(s, "match(") {
		t.Fatalf("desensiz sorguya yüklem sızdı: %s", s)
	}
}
