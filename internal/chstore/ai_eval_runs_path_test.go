package chstore

// v0.10.2106 — CoSRE kalite döngüsü D1 (docs/cosre/quality-loop-audit.md
// §3.1, §8 soru 1): ai_eval_runs.path koşu yolu ayırıcısı ('v1' | 'v2').
// Sözleşme anomaly_events.promotion (v0.10.1797) emsalinin aynısı: ALTER
// `alters` diliminde, CREATE TABLE kolonu ANMAZ (kolon sırası: taze kurulum
// ile yükseltilmiş kurulum aynı sırayı taşısın), küme kipinde ON CLUSTER ve
// _local'a gitmez (state tablosu, Distributed sarmalayıcı yok), iki-boot
// probe + ertelenen DDL reprobe'u; probe false iken HİÇBİR sorgu kolonu
// anmaz, okuma eski satırı 'v1' görür.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/config"
)

const evalRunsPathALTER = "ALTER TABLE ai_eval_runs ADD COLUMN IF NOT EXISTS path LowCardinality(String) DEFAULT 'v1'"

func TestEvalRunsPathColumnMigration(t *testing.T) {
	found := false
	for _, a := range migrateDDLSlice(t, "alters") {
		if a == evalRunsPathALTER {
			found = true
		}
	}
	if !found {
		t.Fatalf("alters diliminde yok: %q", evalRunsPathALTER)
	}
	if mm := addColumnRe.FindStringSubmatch(evalRunsPathALTER); mm == nil || mm[1] != "ai_eval_runs" {
		t.Errorf("addColumnRe eşleşmiyor (boot kolon elemesi): %q", evalRunsPathALTER)
	}
	ddl := tableDDLByName(canonicalTables(30, 30, 7), "ai_eval_runs")
	if strings.Contains(ddl, " path ") || strings.Contains(ddl, "\tpath ") {
		t.Error("CREATE TABLE ai_eval_runs path anıyor — ALTER ile eklenmeli (kolon sırası)")
	}
	src := mustReadSource(t, "store.go")
	if !strings.Contains(src, "epOK, epErr := s.probeEvalRunsPathCol(ctx)") ||
		!strings.Contains(src, "s.hasEvalRunsPathCol.Store(epOK)") {
		t.Error("hasEvalRunsPathCol boot probe'u migrate()'te yok")
	}
	defer_ := mustReadSource(t, "ddl_defer.go")
	if !strings.Contains(defer_, "evalPathDone := s.reprobeEvalRunsPathCol(ctx)") ||
		!strings.Contains(defer_, "&& evalPathDone") {
		t.Error("reprobePromotedAttrs path kolonunu yeniden denemiyor — küme kipinde bayrak donar")
	}
	s := &Store{cfg: config.CHConfig{ClusterName: "c", ReplicaPath: "/p"}}
	got := s.adaptDDL(evalRunsPathALTER)
	if len(got) != 1 || !strings.Contains(got[0], "ai_eval_runs ON CLUSTER `c`") || strings.Contains(got[0], "_local") {
		t.Errorf("ALTER ON CLUSTER'a çevrilmedi ya da _local'a gitti: %v", got)
	}
}

// TestSaveEvalRunPathColumn — probe true: INSERT path'i SON kolon olarak
// taşır; probe false: hiçbir sorgu path anmaz (kolon inmemiş tabloya yazım
// düşmez — Replicated tablo tanımadığı kolonu reddeder).
func TestSaveEvalRunPathColumn(t *testing.T) {
	row := EvalRun{ID: "ev-1", StartedAt: time.Unix(100, 0), UpdatedAt: time.Unix(101, 0), Status: "running", Path: EvalRunPathV2}
	for _, c := range []struct {
		name  string
		probe bool
		nArgs int
	}{{"probe true", true, 22}, {"probe false", false, 21}} {
		t.Run(c.name, func(t *testing.T) {
			var qs []string
			var app [][]any
			st := &Store{conn: episodeFakeConn{queries: &qs, appended: &app}}
			st.hasEvalRunsPathCol.Store(c.probe)
			if err := st.SaveEvalRun(context.Background(), row); err != nil {
				t.Fatal(err)
			}
			if len(app) != 1 || len(app[0]) != c.nArgs {
				t.Fatalf("INSERT satırı = %v, want %d değer", app, c.nArgs)
			}
			mentions := strings.Contains(qs[0], "path")
			if mentions != c.probe {
				t.Errorf("INSERT path anıyor=%v, want %v: %q", mentions, c.probe, qs[0])
			}
			if c.probe && app[0][c.nArgs-1] != EvalRunPathV2 {
				t.Errorf("son argüman %v, want %q", app[0][c.nArgs-1], EvalRunPathV2)
			}
		})
	}
}

// TestEvalRunPathReadShape — okuma kolon listesi ve Scan hedefi probe ile
// hizalı; path boş (kolon yok / eski satır) → 'v1'.
func TestEvalRunPathReadShape(t *testing.T) {
	for _, path := range []bool{false, true} {
		cols := strings.Split(evalRunsSelectCols(path), ",")
		var r EvalRun
		if got := len(evalRunScanDest(&r, false, path)); got != len(cols) {
			t.Fatalf("path=%v: scan %d, kolon %d", path, got, len(cols))
		}
		if got := len(evalRunScanDest(&r, true, path)); got != len(cols)+1 {
			t.Fatalf("path=%v: cases'li scan %d", path, got)
		}
		if strings.Contains(evalRunsSelectCols(path), "path") != path {
			t.Errorf("path=%v: SELECT path anıyor mu yanlış", path)
		}
	}
	for _, c := range []struct{ in, want string }{{"", EvalRunPathV1}, {EvalRunPathV1, EvalRunPathV1}, {EvalRunPathV2, EvalRunPathV2}} {
		r := EvalRun{Path: c.in}
		normalizeEvalRun(&r)
		if r.Path != c.want {
			t.Errorf("normalize(%q) = %q, want %q", c.in, r.Path, c.want)
		}
	}
}

// TestListEvalRunsOmitsPathWhenProbeFalse — okuma da probe'a bağlı.
func TestListEvalRunsOmitsPathWhenProbeFalse(t *testing.T) {
	var qs []string
	var app [][]any
	st := &Store{conn: episodeFakeConn{queries: &qs, appended: &app}}
	if _, err := st.ListEvalRuns(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetEvalRun(context.Background(), "ev-1"); err != nil {
		t.Fatal(err)
	}
	for _, q := range qs {
		if strings.Contains(q, "path") {
			t.Errorf("probe false iken okuma path anıyor: %q", q)
		}
	}
	qs = qs[:0]
	st.hasEvalRunsPathCol.Store(true)
	_, _ = st.ListEvalRuns(context.Background(), 5)
	_, _ = st.GetEvalRun(context.Background(), "ev-1")
	for _, q := range qs {
		if !strings.Contains(q, "path") {
			t.Errorf("probe true iken okuma path anmıyor: %q", q)
		}
	}
}

// TestReprobeEvalRunsPathCol — bayrak donmaz; kolon inince çevrilir.
func TestReprobeEvalRunsPathCol(t *testing.T) {
	var qs []string
	var app [][]any
	cols := uint64(0)
	st := &Store{conn: episodeFakeConn{queries: &qs, appended: &app, colCount: &cols}}
	if st.reprobeEvalRunsPathCol(context.Background()) {
		t.Fatal("kolon yokken bayrak true döndü")
	}
	cols = 1
	if !st.reprobeEvalRunsPathCol(context.Background()) || !st.hasEvalRunsPathCol.Load() {
		t.Fatal("kolon inince bayrak çevrilmedi")
	}
	if err := st.SaveEvalRun(context.Background(), EvalRun{ID: "ev-2", Path: EvalRunPathV2}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(qs[len(qs)-1], "path") {
		t.Fatalf("reprobe sonrası INSERT path anmıyor: %q", qs[len(qs)-1])
	}
}
