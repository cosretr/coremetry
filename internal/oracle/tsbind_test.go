package oracle

import (
	"strings"
	"testing"
	"time"
)

// v0.10.885 — zaman bind'i kolon tipine göre SQL fonksiyonu: kolon ifadenin
// dışında (partition budaması), değer dize, biçim sabit; sözlük kutuyu ezer.
func TestTsKindOf(t *testing.T) {
	cases := []struct {
		typ     string
		hasZone bool
		want    tsKind
	}{
		{"DATE", false, tsKindDate}, {"date", true, tsKindDate},
		{"TIMESTAMP(3)", false, tsKindTimestamp}, {"TIMESTAMP(6)", true, tsKindTimestamp},
		{"TIMESTAMP(6) WITH TIME ZONE", false, tsKindTZ}, {"TIMESTAMP(6) WITH LOCAL TIME ZONE", false, tsKindTZ},
		{"", false, tsKindTimestamp}, {"", true, tsKindTZ}, {"VARCHAR2", true, tsKindTZ},
	}
	for _, c := range cases {
		if got := tsKindOf(c.typ, c.hasZone); got != c.want {
			t.Errorf("%q/%v → %v, want %v", c.typ, c.hasZone, got, c.want)
		}
	}
}

func TestTsBindExprAndValue(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Istanbul")
	at := time.Date(2026, 9, 23, 5, 56, 7, 123456789, time.UTC) // 08:56:07 İstanbul
	if e := tsKindTimestamp.bindExpr(1); e != "TO_TIMESTAMP(:1, 'YYYY-MM-DD HH24:MI:SS.FF6')" {
		t.Errorf("timestamp: %s", e)
	}
	if e := tsKindDate.bindExpr(2); e != "TO_DATE(:2, 'YYYY-MM-DD HH24:MI:SS')" {
		t.Errorf("date: %s", e)
	}
	if e := tsKindTZ.bindExpr(12); e != "TO_TIMESTAMP_TZ(:12, 'YYYY-MM-DD HH24:MI:SS.FF6 TZH:TZM')" {
		t.Errorf("tz: %s", e)
	}
	if v := tsKindTimestamp.bindValue(at, loc); v != "2026-09-23 08:56:07.123456" {
		t.Errorf("timestamp değeri duvar saati: %s", v)
	}
	if v := tsKindDate.bindValue(at, loc); v != "2026-09-23 08:56:07" {
		t.Errorf("date değeri: %s", v)
	}
	if v := tsKindTZ.bindValue(at, loc); v != "2026-09-23 05:56:07.123456 +00:00" {
		t.Errorf("tz değeri UTC anı: %s", v)
	}
	for _, k := range []tsKind{tsKindTimestamp, tsKindDate, tsKindTZ} {
		if e := k.bindExpr(1); strings.ContainsAny(e, ";") || strings.Contains(e, "--") {
			t.Errorf("ifade tek deyim olmalı: %s", e)
		}
	}
}

func TestBuildPollQueryUsesDictionaryType(t *testing.T) {
	cfg := cfgFor(t)
	from := time.Date(2026, 9, 23, 5, 0, 0, 0, time.UTC)
	q, args, err := buildPollQuery(cfg, from, from.Add(time.Minute), 10, "DATE")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q, "ERR_TIMESTAMP > TO_DATE(:1, 'YYYY-MM-DD HH24:MI:SS') AND ERR_TIMESTAMP <= TO_DATE(:2, 'YYYY-MM-DD HH24:MI:SS')") {
		t.Errorf("DATE kolon → TO_DATE: %s", q)
	}
	if args[0] != any("2026-09-23 08:00:00") {
		t.Errorf("DATE değeri saniye, duvar saati: %v", args[0])
	}
	cfg.TimestampHasZone = true
	q, _, _ = buildPollQuery(cfg, from, from.Add(time.Minute), 10, "TIMESTAMP(6) WITH TIME ZONE")
	if !strings.Contains(q, "TO_TIMESTAMP_TZ(:1") {
		t.Errorf("TZ kolon → TO_TIMESTAMP_TZ: %s", q)
	}
	if !strings.Contains(tsTypeSQL(), "ALL_TAB_COLUMNS") || !strings.Contains(tsTypeSQL(), "DATA_TYPE") {
		t.Error("sözlük sorgusu")
	}
}
