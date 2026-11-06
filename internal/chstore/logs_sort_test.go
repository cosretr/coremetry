package chstore

// logs_sort_test.go — v0.10.1833 (Log Search Faz 1 dilim 1.10): CH liste
// sıralaması — ORDER BY yalnız beyaz liste kolonlarından, zaman eşitlik
// bozucu + rowkey, ofset token'ı imzalı ve tavanlı.

import (
	"strings"
	"testing"
)

func TestLogsSortOrderSQL(t *testing.T) {
	for _, c := range []struct {
		cols []LogSortCol
		asc  bool
		want string
		ok   bool
	}{
		{[]LogSortCol{{"service_name", false}}, false, "service_name ASC, time DESC, " + logsRowKeyExpr + " DESC", true},
		{[]LogSortCol{{"severity_num", true}, {"service_name", false}}, true, "severity_num DESC, service_name ASC, time ASC, " + logsRowKeyExpr + " ASC", true},
		{[]LogSortCol{{"time", false}, {"service_name", true}}, false, "time ASC, service_name DESC, " + logsRowKeyExpr + " DESC", true},
		{[]LogSortCol{{"body", false}}, false, "", false},
		{[]LogSortCol{{"time; DROP TABLE logs", false}}, false, "", false},
	} {
		got, ok := logsSortOrderSQL(c.cols, c.asc)
		if ok != c.ok || got != c.want {
			t.Errorf("%+v: %q %v", c.cols, got, ok)
		}
	}
}

func TestLogsSortCursor(t *testing.T) {
	tok := EncodeLogsSortCursor(500, "service:asc")
	if n, ok := DecodeLogsSortCursor(tok, "service:asc"); !ok || n != 500 {
		t.Errorf("gidiş-dönüş: %d %v", n, ok)
	}
	if _, ok := DecodeLogsSortCursor(tok, "service:desc"); ok {
		t.Error("başka sıralamanın token'ı düşmeli")
	}
	if _, ok := DecodeLogsSortCursor(EncodeLogsSortCursor(logsSortedMaxOffset+1, "s"), "s"); ok {
		t.Error("tavan üstü ofset düşmeli")
	}
	// Zaman-keyset token'ı sıralı yolda geçersiz (ve tersi).
	if _, ok := DecodeLogsSortCursor(EncodeLogsCursor(1, 2, false), "s"); ok {
		t.Error("keyset token'ı ofset olarak okunmamalı")
	}
	if _, ok := DecodeLogsCursor(tok); ok {
		t.Error("ofset token'ı keyset olarak okunmamalı")
	}
	if strings.Contains(tok, "|") {
		t.Error("token opak (base64) olmalı")
	}
}
