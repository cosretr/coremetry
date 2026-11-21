package chstore

import (
	"strings"
	"testing"
	"time"
)

// trace_id_ts — trace açılışının PK araması. Kayıtlar gün-bir (v0.5.426
// sınıfı: bir kayıttan eksik MV küme kipinde tek shard'ı okur).
func TestTraceIDTsRegistered(t *testing.T) {
	ddl := canonicalMVDDL(traceIDTsMV)
	if ddl == "" {
		t.Fatal("trace_id_ts katalogda yok")
	}
	for _, want := range []string{"ORDER BY (trace_id, time_bucket)", "PARTITION BY toDate(time_bucket)",
		"minState(toDateTime(time))", "maxState(toUInt32(intDiv(toUnixTimestamp64Nano(time) + duration, 1000000000) + 1))", "FROM spans", "WHERE trace_id != ''"} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL %q taşımalı", want)
		}
	}
	purge := false
	for _, n := range telemetryPurgeTables {
		purge = purge || n == traceIDTsMV
	}
	if !highVolumeTables[traceIDTsMV] || !strings.Contains(defaultShardPolicy[traceIDTsMV], "trace_id") ||
		tablesWithoutTraceID[traceIDTsMV] || !purge {
		t.Error("highVolumeTables / defaultShardPolicy(trace_id) / purge kaydı eksik ya da tablesWithoutTraceID'de")
	}
	if !strings.Contains(priorSourceProbeSQL[priorSrcTraceIDTs], "FROM "+traceIDTsMV+" ") {
		t.Errorf("kapsama probu yanlış tablo: %s", priorSourceProbeSQL[priorSrcTraceIDTs])
	}
	for _, w := range []string{"WHERE trace_id = ?", "LIMIT 1", "max_execution_time"} {
		if !strings.Contains(traceIDTsLookupSQL, w) {
			t.Errorf("arama SQL'i %q taşımalı", w)
		}
	}
}

func TestTraceIDTsBound(t *testing.T) {
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Second).UnixNano()
	yes := func(time.Time) bool { return true }
	no := func(time.Time) bool { return false }

	lo, hi, ok := traceIDTsBound(start, end, yes)
	if !ok || !lo.Equal(start.Add(-5*time.Minute)) || !hi.Equal(time.Unix(0, end).Add(5*time.Minute)) {
		t.Fatalf("kapsanan isabet ±5 dk pencere vermeli: %v %v %v", lo, hi, ok)
	}
	if _, _, ok := traceIDTsBound(start, end, no); ok {
		t.Error("kapsam dışı isabet (MV trace'ten sonra kuruldu) kullanılmamalı")
	}
	if _, _, ok := traceIDTsBound(time.Unix(0, 0), 0, yes); ok {
		t.Error("ıska (endNanos 0) ok=false olmalı")
	}
	var asked time.Time
	traceIDTsBound(start, end, func(t time.Time) bool { asked = t; return true })
	if !asked.Equal(start.Add(-traceAggSlack)) {
		t.Errorf("kapsama trace başlangıcından traceAggSlack önce sorulmalı: %v", asked)
	}
}
