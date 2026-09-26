package chstore

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// ai_eval_runs_test.go — v0.10.940: canlı CH'siz saf pinler. DDL şekli
// (clickhouse-schema karar ağacı: state → RMT(version), ORDER BY = dedup
// anahtarı, partition yok, TTL), INSERT kolon/argüman hizası, epoch
// sentinel'i ve liste tavanı.

func TestEvalRunsDDLShape(t *testing.T) {
	ddl := tableDDLByName(canonicalTables(30, 30, 7), "ai_eval_runs")
	if ddl == "" {
		t.Fatal("ai_eval_runs CREATE'i tables diliminde yok")
	}
	for _, want := range []string{
		"ENGINE = ReplacingMergeTree(version)",
		"ORDER BY id\n",
		"TTL toDateTime(started_at) + INTERVAL 180 DAY",
		"version        UInt64  DEFAULT toUnixTimestamp64Nano(now64(9))",
		"finished_at    DateTime64(9) DEFAULT toDateTime64(0, 9)",
		"cases          String  DEFAULT '' CODEC(ZSTD(3))",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL %q taşımalı", want)
		}
	}
	// Kural P1 + C4: partition yok (yeniden yazılan satır), Nullable yok.
	for _, bad := range []string{"PARTITION BY", "Nullable"} {
		if strings.Contains(ddl, bad) {
			t.Errorf("DDL %q taşımamalı", bad)
		}
	}
}

// INSERT kolon listesi ile argüman sayısı ve DDL kolonları hizalı olmalı —
// kayma sessiz değil ama yalnız canlı CH'de patlardı.
func TestEvalRunInsertArgsAlignWithColumns(t *testing.T) {
	open := strings.Index(evalRunsInsertSQL, "(")
	cols := strings.Split(strings.TrimSuffix(strings.TrimSpace(evalRunsInsertSQL[open+1:]), ")"), ",")
	args := evalRunInsertArgs(EvalRun{ID: "ev-1"})
	if len(cols) != len(args) {
		t.Fatalf("INSERT %d kolon, %d argüman", len(cols), len(args))
	}
	ddl := tableDDLByName(canonicalTables(30, 30, 7), "ai_eval_runs")
	for _, c := range cols {
		c = strings.TrimSpace(c)
		if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(c) + `\s`).MatchString(ddl) {
			t.Errorf("INSERT kolonu %q DDL'de yok", c)
		}
	}
	// Seçim kolonları da DDL'de olmalı (+ cases nokta okumada).
	for _, c := range strings.Split(evalRunsSelectCols, ",") {
		c = strings.TrimSpace(c)
		if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(c) + `\s`).MatchString(ddl) {
			t.Errorf("SELECT kolonu %q DDL'de yok", c)
		}
	}
	var r EvalRun
	if got, want := len(evalRunScanDest(&r, false)), len(strings.Split(evalRunsSelectCols, ",")); got != want {
		t.Fatalf("scan hedefi %d, kolon %d", got, want)
	}
	if got := len(evalRunScanDest(&r, true)); got != len(strings.Split(evalRunsSelectCols, ","))+1 {
		t.Fatalf("cases'li scan hedefi %d", got)
	}
}

func TestEvalRunTimeSentinelAndVersion(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
	}{
		{"sıfır → epoch → sıfır", time.Time{}},
		{"gerçek zaman korunur", time.Date(2026, 9, 26, 18, 40, 0, 123, time.FixedZone("TR", 3*3600))},
	}
	for _, c := range cases {
		col := evalRunTimeArg(c.in)
		if c.in.IsZero() && col.Unix() != 0 {
			t.Errorf("%s: sentinel epoch değil: %v", c.name, col)
		}
		back := evalRunTimeFromCol(col)
		if !back.Equal(c.in) || (!back.IsZero() && back.Location() != time.UTC) {
			t.Errorf("%s: gidiş-dönüş %v → %v", c.name, c.in, back)
		}
	}
	a := EvalRun{ID: "x", UpdatedAt: time.Unix(100, 1)}
	b := EvalRun{ID: "x", UpdatedAt: time.Unix(100, 2)}
	if evalRunVersion(b) <= evalRunVersion(a) {
		t.Fatal("version updated_at ile KESİN artmalı (son yazım FINAL'de kazanır)")
	}
	if args := evalRunInsertArgs(EvalRun{ID: "x"}); args[10] == nil {
		t.Fatal("nil surfaces boş diziye çevrilmeli")
	}
	var r EvalRun
	normalizeEvalRun(&r)
	if r.Surfaces == nil || !r.FinishedAt.IsZero() {
		t.Fatalf("normalize: %+v", r)
	}
}

func TestEvalRunsListLimitClamp(t *testing.T) {
	for _, c := range []struct{ in, want int }{{0, 20}, {-5, 20}, {1, 1}, {20, 20}, {50, 50}, {51, 50}, {1000, 50}} {
		if got := clampEvalRunsLimit(c.in); got != c.want {
			t.Errorf("clamp(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
