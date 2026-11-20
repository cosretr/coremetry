package chstore

import (
	"strings"
	"testing"
)

// spanmetrics_exemplar_test.go — depolama sıkıştırması: spanmetrics
// kademeleri exemplar'ı ham bayt (unhex) + ZSTD(3) state kolonlarıyla
// yazar; okuyucu kademenin şekline göre hex'e döner; eski şekil göç
// defterinden drop + recreate edilir.

func TestSpanmetricsCompactDDL(t *testing.T) {
	mvs := canonicalMVs()
	for _, tbl := range smExemplarTables {
		ddl := mvDDLByName(mvs, tbl)
		if ddl == "" {
			t.Fatalf("%s DDL yok", tbl)
		}
		for _, want := range []string{
			"argMaxState(" + smExemplarTraceIDDDL + ", duration) AS " + smSlowExemplarCol,
			"argMaxIfState(" + smExemplarTraceIDDDL + ", duration, status_code = 'error') AS " + smErrorExemplarCol,
			smSlowExemplarCol + "  AggregateFunction(argMax, String, Int64) CODEC(ZSTD(3))",
			smErrorExemplarCol + " AggregateFunction(argMaxIf, String, Int64, UInt8) CODEC(ZSTD(3))",
			"duration_sum_state       AggregateFunction(sum, Int64) CODEC(ZSTD(3))",
			"duration_q_state         AggregateFunction(quantilesTDigest(0.5, 0.9, 0.95, 0.99), Int64) CODEC(ZSTD(3))",
			"quantilesTDigestState(0.5, 0.9, 0.95, 0.99)(duration)",
		} {
			if !strings.Contains(ddl, want) {
				t.Errorf("%s DDL %q içermeli", tbl, want)
			}
		}
		if strings.Contains(ddl, "argMaxState(trace_id,") || strings.Contains(ddl, "AS slow_exemplar_state") {
			t.Errorf("%s eski hex exemplar şeklini taşıyor", tbl)
		}
		// Göç defteri: yeni kolon yoksa drop + recreate.
		found := false
		for _, m := range mvDimMigrations {
			if m.Table == tbl && m.Column == smSlowExemplarCol {
				found = true
			}
		}
		if !found {
			t.Errorf("%s mvDimMigrations'ta yok — mevcut kurulumlar eski şekilde kalır", tbl)
		}
	}
	if !strings.Contains(exemplarStateCols[0]+exemplarStateCols[1], smSlowExemplarCol) {
		t.Errorf("kolon TTL'i yeni exemplar kolonlarını hedeflemeli: %v", exemplarStateCols)
	}
}

func TestSpanmetricsExemplarReadShape(t *testing.T) {
	prev := smLegacyExemplar.Load()
	t.Cleanup(func() { smLegacyExemplar.Store(prev) })

	smLegacyExemplar.Store(nil) // probe öncesi = yeni şekil
	slow, errExpr := smExemplarMerges("spanmetrics_1m")
	if slow != "lower(hex(argMaxMerge(slow_exemplar_bin_state)))" || errExpr != "lower(hex(argMaxIfMerge(error_exemplar_bin_state)))" {
		t.Errorf("yeni şekil: %s / %s", slow, errExpr)
	}
	legacy := map[string]bool{"spanmetrics_10s": true}
	smLegacyExemplar.Store(&legacy)
	if s, e := smExemplarMerges("spanmetrics_10s"); s != "argMaxMerge(slow_exemplar_state)" || e != "argMaxIfMerge(error_exemplar_state)" {
		t.Errorf("eski şekil: %s / %s", s, e)
	}
	if c, _ := smExemplarStateCols("spanmetrics_1m"); c != smSlowExemplarCol {
		t.Errorf("kademe başına şekil: 1m yeni olmalı, %s", c)
	}
	if got := smExemplarFinal("spanmetrics_10s", "argMaxMerge(slow_ex_state)"); got != "argMaxMerge(slow_ex_state)" {
		t.Errorf("eski şekil sarmalanmamalı: %s", got)
	}
}

// metric_points kodekleri: taze kurulum CREATE'i ile mevcut kurulum ALTER'ı
// aynı kodeği yazar; ORDER BY'a dokunan ifade YOK.
func TestMetricPointsCodecs(t *testing.T) {
	src := mustReadSource(t, "store.go")
	for _, frag := range []string{
		"count         UInt64       DEFAULT 0 CODEC(T64, ZSTD(3))",
		"min_value     Float64      DEFAULT 0 CODEC(Gorilla, ZSTD(3))",
		"max_value     Float64      DEFAULT 0 CODEC(Gorilla, ZSTD(3))",
		"bucket_bounds Array(Float64) DEFAULT [] CODEC(ZSTD(3))",
		"bucket_counts Array(UInt64)  DEFAULT [] CODEC(T64, ZSTD(3))",
		"range metricPointsCodecAlters",
	} {
		if !strings.Contains(src, frag) {
			t.Errorf("store.go %q içermeli", frag)
		}
	}
	if len(metricPointsCodecAlters) != 6 {
		t.Fatalf("6 kolon bekleniyordu: %d", len(metricPointsCodecAlters))
	}
	for _, a := range metricPointsCodecAlters {
		if !strings.HasPrefix(a, "ALTER TABLE metric_points MODIFY COLUMN ") || !strings.Contains(a, " DEFAULT ") ||
			!strings.Contains(a, "ZSTD(3))") || strings.Contains(a, "ORDER BY") {
			t.Errorf("kodek ALTER'ı şekli: %s", a)
		}
	}
}
