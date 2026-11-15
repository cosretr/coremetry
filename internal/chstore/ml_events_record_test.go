package chstore

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// v0.10.2199 — anomali kartı H1 kolon taşıma pini (audit §9 "RMT tam satır
// taşıma" (3)): INSERT kolon listesi = SELECT kolon listesi, ve Upsert →
// List → Upsert gidiş-dönüşü hiçbir alanı sıfırlamaz.

// fillFromArgs — INSERT değerlerini Scan hedeflerine yazar (CH'siz gidiş-dönüş).
func fillFromArgs(t *testing.T, dest, args []any) {
	t.Helper()
	if len(dest) != len(args) {
		t.Fatalf("scan hedefi %d, insert değeri %d — kolon listeleri ayrıştı", len(dest), len(args))
	}
	for i := range dest {
		dv := reflect.ValueOf(dest[i]).Elem()
		av := reflect.ValueOf(args[i])
		if !av.Type().AssignableTo(dv.Type()) {
			t.Fatalf("kolon %d: insert %s, scan %s", i, av.Type(), dv.Type())
		}
		dv.Set(av)
	}
}

func fullMLEvent() MLEvent {
	return MLEvent{ID: "e1", Detector: "op_latency", Signal: MLSignalTrace, Stage: MLStageClosed, Service: "svc",
		Level: "error", Pod: "p-1", Code: "callBagService|p95", StartedAt: 1_700_000_000_000_000_000, LastSeen: 1_700_000_600_000_000_000,
		WindowSec: 300, Baseline: 43, Observed: 107, Score: 5.5, Direction: "down", Evidence: `{"a":1}`, DupOf: "e0",
		Class: "service_anomaly", PublishState: "invalid", OnsetAt: 1_699_999_700_000_000_000, Record: `{"v":1}`}
}

func TestMLEventRecordColsRoundTrip(t *testing.T) {
	in := fullMLEvent()
	var out MLEvent
	dest, done := mlEventScan(&out, true)
	fillFromArgs(t, dest, mlEventInsertArgs(in, true))
	done()
	// Karar alanları ml_events'te değil (anomaly_verdicts) — gidiş-dönüş dışı.
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("gidiş-dönüş alan kaybetti:\n in=%+v\nout=%+v", in, out)
	}
	// İkinci tur (Upsert → List → Upsert) aynı değerleri yazar.
	if !reflect.DeepEqual(mlEventInsertArgs(in, true), mlEventInsertArgs(out, true)) {
		t.Fatal("ikinci yazım ilkinden farklı")
	}
}

func TestMLEventRecordColsAbsent(t *testing.T) {
	// Kolon yokken (iki-boot ara durumu) eski biçim: 17 kolon, H1 alanları yazılmaz.
	cols := mlEventCols(false)
	if len(cols) != len(mlEventBaseCols) || strings.Contains(strings.Join(cols, ","), "record") {
		t.Fatalf("kolonsuz liste H1 kolonu taşıyor: %v", cols)
	}
	var out MLEvent
	dest, done := mlEventScan(&out, false)
	fillFromArgs(t, dest, mlEventInsertArgs(fullMLEvent(), false))
	done()
	if out.Class != "" || out.PublishState != "" || out.OnsetAt != 0 || out.Record != "" {
		t.Fatalf("kolonsuz okuma H1 alanı doldurdu: %+v", out)
	}
	if out.Observed != 107 || out.DupOf != "e0" {
		t.Fatalf("eski alanlar kayboldu: %+v", out)
	}
}

func TestMLEventRecordColsZeroOnset(t *testing.T) {
	// Eski satır: onset_at DEFAULT toDateTime64(0, 9) → 0 okunur.
	in := fullMLEvent()
	in.OnsetAt = 0
	var out MLEvent
	dest, done := mlEventScan(&out, true)
	fillFromArgs(t, dest, mlEventInsertArgs(in, true))
	done()
	if out.OnsetAt != 0 {
		t.Fatalf("onset 0 kalmalı, %d", out.OnsetAt)
	}
	if got := mlEventInsertArgs(in, true)[19].(time.Time); got.Unix() != 0 {
		t.Fatalf("onset 0 epoch yazılmalı, %v", got)
	}
}

func TestMLEventRecordColsMatchDDL(t *testing.T) {
	// Her H1 kolonu için store.go'da ADD COLUMN IF NOT EXISTS var ve probe aynı adları sayar.
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range mlEventRecordCols {
		if !strings.Contains(string(src), "ALTER TABLE ml_events ADD COLUMN IF NOT EXISTS "+c+" ") {
			t.Fatalf("kolon %s için ALTER yok", c)
		}
	}
}
