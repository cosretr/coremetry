package chstore

// v0.10.1813 — ml_baseline_obs (öğrenme döngüsü audit §4.1, Faz 1.2):
// tablo + slot kodlaması + sınırlı okuma. Yeni yüzey; regresyon değil,
// sözleşme pinleri (slot/hafta kodlaması kayarsa bant yanlış haftanın
// yanlış saatini kıyaslar).

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/config"
)

func TestMLBaselineSlot(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Skip("tzdata yok")
	}
	cases := []struct {
		name     string
		t        time.Time
		loc      *time.Location
		slot     uint8
		weekFrom string
	}{
		{"pazartesi 00:00 → 0", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), time.UTC, 0, "2026-11-02"},
		{"pazar 23:59 → 167, aynı hafta", time.Date(2026, 11, 8, 23, 59, 0, 0, time.UTC), time.UTC, 167, "2026-11-02"},
		{"çarşamba 13:00 yerel (UTC 10:00) → 61", time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC), ist, 61, "2026-11-02"},
		{"UTC pazar 22:00 = yerel pazartesi 01:00 → yeni hafta slot 1", time.Date(2026, 11, 8, 22, 0, 0, 0, time.UTC), ist, 1, "2026-11-09"},
		{"ay/yıl sınırı: 2027-01-01 cuma 09:00", time.Date(2027, 1, 1, 9, 0, 0, 0, time.UTC), time.UTC, 4*24 + 9, "2026-12-28"},
		{"nil dilim = UTC", time.Date(2026, 11, 3, 5, 0, 0, 0, time.UTC), nil, 29, "2026-11-02"},
	}
	for _, c := range cases {
		slot, monday := MLBaselineSlot(c.t, c.loc)
		if slot != c.slot || monday.Format("2006-01-02") != c.weekFrom || monday.Weekday() != time.Monday {
			t.Errorf("%s: slot=%d monday=%s", c.name, slot, monday.Format("2006-01-02 Mon"))
		}
	}
}

func TestMLBaselineBandWindow(t *testing.T) {
	asOf := time.Date(2026, 11, 4, 15, 0, 0, 0, time.UTC) // çarşamba
	from, to := mlBaselineBandWindow(asOf, 4)
	if from.Format("2006-01-02") != "2026-10-05" || to.Format("2006-01-02") != "2026-11-02" {
		t.Fatalf("4 hafta: [%s, %s)", from.Format("2006-01-02"), to.Format("2006-01-02"))
	}
	if f, _ := mlBaselineBandWindow(asOf, 0); f.Format("2006-01-02") != "2026-10-26" {
		t.Fatalf("0 → 1 hafta: %s", f.Format("2006-01-02"))
	}
	if f, _ := mlBaselineBandWindow(asOf, 1000); f.Format("2006-01-02") != to.AddDate(0, 0, -7*mlBaselineMaxWeeks).Format("2006-01-02") {
		t.Fatalf("tavan 60 hafta: %s", f.Format("2006-01-02"))
	}
}

// Her okuma: zaman (week_start) + anahtar sınırlı, FINAL, LIMIT, max_execution_time.
func TestMLBaselineBandSQLBounded(t *testing.T) {
	q := mlBaselineBandSQL()
	for _, want := range []string{"FROM ml_baseline_obs FINAL", "week_start >= ? AND week_start < ?", "service IN ?", "slot IN ?", "LIMIT ?", "max_execution_time"} {
		if !strings.Contains(q, want) {
			t.Errorf("sorguda %q yok", want)
		}
	}
}

// DDL sözleşmesi (audit §4.1) + küme kipinde state tablosu: Replicated motor,
// ON CLUSTER, Distributed sarmalayıcı YOK (ml_series ile aynı ev deseni).
func TestMLBaselineObsDDL(t *testing.T) {
	ddl := tableDDLByName(canonicalTables(30, 30, 7), "ml_baseline_obs")
	if ddl == "" {
		t.Fatal("ml_baseline_obs tables diliminde yok")
	}
	for _, want := range []string{
		"ENGINE = ReplacingMergeTree(version)",
		"PARTITION BY week_start",
		"ORDER BY (signal, service, part, slot, week_start, source)",
		"TTL week_start + INTERVAL 70 DAY DELETE WHERE class = 'normal'",
		"week_start + INTERVAL 400 DAY",
		"q10        Float32", "q99        Float32", "slot       UInt8", "week_start Date",
		"class      LowCardinality(String)",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL'de %q yok", want)
		}
	}
	if strings.Contains(ddl, "toDate(") {
		t.Error("week_start zaten Date: toDate() sarmalama yok (v0.6.36 sınıfı)")
	}
	s := &Store{cfg: config.CHConfig{ClusterName: "c", ReplicaPath: "/p"}}
	got := s.adaptDDL(ddl)
	if len(got) != 1 {
		t.Fatalf("state tablosu: Distributed sarmalayıcı olmamalı, %d ifade", len(got))
	}
	if !strings.Contains(got[0], "ReplicatedReplacingMergeTree(") || !strings.Contains(got[0], "ON CLUSTER `c`") || !strings.Contains(got[0], "DELETE WHERE class = 'normal'") {
		t.Fatalf("küme DDL'i: %s", got[0])
	}
}
