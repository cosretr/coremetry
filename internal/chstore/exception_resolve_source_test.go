package chstore

// v0.10.2432 — süpürme kapanışı vs insan kapanışı (exception_resolve_source.go).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

func TestMergeResolveSourceStateMachine(t *testing.T) {
	const h = int64(time.Hour)
	r := 10 * h
	past := r + exResolveGrace.Nanoseconds() + int64(time.Minute)
	inGrace := r + exResolveGrace.Nanoseconds()/2
	for _, c := range []struct {
		name      string
		state     string
		source    string
		lastSeen  int64 // saklı last_seen
		newSeen   int64
		want      string
		wantSrc   string
		keepAISum bool
	}{
		{"süpürme kapanışı, grace sonrası → sıradan açık", ExStateResolved, ExResolveSourceAuto, r, past, ExStateNew, ExResolveSourceAuto, true},
		{"insan kapanışı, grace sonrası → regressed", ExStateResolved, ExResolveSourceUser, r - h, past, ExStateRegressed, ExResolveSourceUser, false},
		{"eski satır, resolved_at == last_seen (süpürme kanıtı) → açık", ExStateResolved, "", r, past, ExStateNew, ExResolveSourceAuto, true},
		{"eski satır, resolved_at ≠ last_seen (insan) → regressed", ExStateResolved, "", r - h, past, ExStateRegressed, "", false},
		{"insan kaynağı eşitlikte de insan", ExStateResolved, ExResolveSourceUser, r, past, ExStateRegressed, ExResolveSourceUser, false},
		{"süpürme kapanışı, grace içinde → resolved kalır", ExStateResolved, ExResolveSourceAuto, r, inGrace, ExStateResolved, ExResolveSourceAuto, true},
		{"insan kapanışı, grace içinde → resolved kalır", ExStateResolved, ExResolveSourceUser, r - h, inGrace, ExStateResolved, ExResolveSourceUser, true},
		{"ignored susar (kaynak ne olursa)", ExStateIgnored, ExResolveSourceAuto, r, past, ExStateIgnored, ExResolveSourceAuto, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := r
			existing := &ExceptionGroup{Fingerprint: "fp", State: c.state, ResolvedAt: &rr, ResolveSource: c.source,
				LastSeen: c.lastSeen, Occurrences: 40, OccurrencesAtResolve: 40, AISummary: "özet", AISummaryAt: 5}
			got := mergeExceptionGroup(ExceptionGroup{Fingerprint: "fp", LastSeen: c.newSeen, Occurrences: 3}, existing)
			if got.State != c.want || got.ResolveSource != c.wantSrc {
				t.Fatalf("state/source = %s/%q, want %s/%q", got.State, got.ResolveSource, c.want, c.wantSrc)
			}
			if (got.AISummary != "") != c.keepAISum {
				t.Errorf("AISummary = %q, keep=%v", got.AISummary, c.keepAISum)
			}
			if got.ResolvedAt == nil || *got.ResolvedAt != r || got.OccurrencesAtResolve != 40 || got.Occurrences != 43 {
				t.Errorf("tam satır taşınmadı: %+v", got)
			}
		})
	}
	// Süpürme → açık → insan resolve → dönüş: regressed (son kapanış insan).
	g := ExceptionGroup{Fingerprint: "fp", State: ExStateNew, Occurrences: 10, LastSeen: r}
	markExceptionResolved(&g, g.LastSeen)
	g.ResolveSource = ExResolveSourceAuto
	g = mergeExceptionGroup(ExceptionGroup{Fingerprint: "fp", LastSeen: past, Occurrences: 1}, &g)
	if g.State != ExStateNew {
		t.Fatalf("süpürme dönüşü = %s", g.State)
	}
	markExceptionResolved(&g, past+h)
	g.ResolveSource = ExResolveSourceUser
	g = mergeExceptionGroup(ExceptionGroup{Fingerprint: "fp", LastSeen: past + 2*h, Occurrences: 1}, &g)
	if g.State != ExStateRegressed {
		t.Fatalf("insan kapanışı dönüşü = %s, want regressed", g.State)
	}
}

// Süpürme "auto", manuel resolve "user" yazar; kolon INSERT'in 15. değeri.
func TestResolveWritesSource(t *testing.T) {
	for _, c := range []struct{ path, want string }{{"manuel", ExResolveSourceUser}, {"bayat süpürme", ExResolveSourceAuto}} {
		t.Run(c.path, func(t *testing.T) {
			var qs []string
			var app [][]any
			st := &Store{conn: exSnapFakeConn{row: append(exSnapStoredRow(true), ""), queries: &qs, appended: &app}}
			st.hasExResolveSnapCol.Store(true)
			st.hasExResolveSourceCol.Store(true)
			var err error
			if c.path == "manuel" {
				err = st.SetExceptionGroupState(context.Background(), "fp", ExStateResolved)
			} else {
				_, err = st.AutoResolveStaleExceptionGroups(context.Background(), time.Hour)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(app) != 1 || len(app[0]) != 15 || app[0][14] != c.want || app[0][4] != ExStateResolved {
				t.Fatalf("INSERT = %v, want 15 değer, resolve_source %q", app, c.want)
			}
		})
	}
}

// Probe false: okuma ve INSERT kolonu ANMAZ.
func TestResolveSourceOmittedWithoutColumn(t *testing.T) {
	var qs []string
	var app [][]any
	st := &Store{conn: exSnapFakeConn{row: exSnapStoredRow(true), queries: &qs, appended: &app}}
	st.hasExResolveSnapCol.Store(true)
	if _, err := st.AutoResolveStaleExceptionGroups(context.Background(), time.Hour); err != nil {
		t.Fatal(err)
	}
	if len(app) != 1 || len(app[0]) != 14 {
		t.Fatalf("INSERT = %v, want 14 değer", app)
	}
	for _, q := range qs {
		if strings.Contains(q, "resolve_source") {
			t.Errorf("probe false iken sorgu kolonu anıyor: %q", q)
		}
	}
}

func TestExceptionResolveSourceColumnMigration(t *testing.T) {
	const want = "ALTER TABLE exception_groups ADD COLUMN IF NOT EXISTS resolve_source LowCardinality(String) DEFAULT ''"
	found := false
	for _, a := range migrateDDLSlice(t, "alters") {
		found = found || a == want
	}
	if !found {
		t.Errorf("alters diliminde yok: %q", want)
	}
	if ddl := tableDDLByName(canonicalTables(30, 30, 7), "exception_groups"); strings.Contains(ddl, "resolve_source") {
		t.Errorf("exception_groups CREATE değişmiş:\n%s", ddl)
	}
	src := mustReadSource(t, "store.go")
	if !strings.Contains(src, "srcOK, srcErr := s.probeExResolveSourceCol(ctx)") || !strings.Contains(src, "s.hasExResolveSourceCol.Store(srcOK)") {
		t.Error("resolve_source boot probe'u store.go'da yok")
	}
	if !strings.Contains(mustReadSource(t, "ddl_defer.go"), "s.reprobeExResolveSourceCol(ctx)") {
		t.Error("ertelenen DDL resolve_source'u yeniden denemiyor")
	}
	var qs []string
	var app [][]any
	conn := exSnapFakeConn{queries: &qs, appended: &app}
	st := &Store{conn: conn}
	if st.reprobeExResolveSourceCol(context.Background()) {
		t.Fatal("kolon yokken bayrak true")
	}
	conn.colCount = 1
	st.conn = conn
	if !st.reprobeExResolveSourceCol(context.Background()) {
		t.Fatal("kolon inince bayrak çevrilmedi")
	}
}

// regressFixConn — düzeltme turu: Query aday satırları, QueryRow span
// kanıtını (servis → var/yok) döner, PrepareBatch yazımı kaydeder.
type regressFixConn struct {
	driver.Conn
	rows     [][]any
	spanAt   map[string]bool
	queries  *[]string
	appended *[][]any
}

func (c regressFixConn) Query(ctx context.Context, q string, args ...any) (driver.Rows, error) {
	*c.queries = append(*c.queries, q)
	return &exSnapRows{rows: c.rows}, nil
}

func (c regressFixConn) QueryRow(ctx context.Context, q string, args ...any) driver.Row {
	*c.queries = append(*c.queries, q)
	n := uint64(0)
	if c.spanAt[args[0].(string)] {
		n = 1
	}
	return exSnapRow{vals: []any{n}}
}

func (c regressFixConn) PrepareBatch(ctx context.Context, q string, opts ...driver.PrepareBatchOption) (driver.Batch, error) {
	*c.queries = append(*c.queries, q)
	return episodeFakeBatch{appended: c.appended}, nil
}

func TestCorrectAutoResolvedRegressions(t *testing.T) {
	row := func(fp, svc string) []any {
		r := exSnapStoredRow(true)
		r[0], r[3], r[4] = fp, svc, ExStateRegressed
		return append(r, "")
	}
	var qs []string
	var app [][]any
	st := &Store{conn: regressFixConn{
		rows:   [][]any{row("fp-swept", "svc-a"), row("fp-human", "svc-b"), row("ora:abc", "svc-a")},
		spanAt: map[string]bool{"svc-a": true}, queries: &qs, appended: &app,
	}}
	st.hasExResolveSnapCol.Store(true)
	st.hasExResolveSourceCol.Store(true)
	// exSnapAssign resolved_at'ı nil basar; adayın damgası Scan sonrası gelir —
	// burada fake satırlar damgasız olduğundan önce boş tur beklenir.
	if n, err := st.CorrectAutoResolvedRegressions(context.Background()); err != nil || n != 0 {
		t.Fatalf("damgasız satırlar düzeltildi: %d %v", n, err)
	}
	q := legacyRegressedSQL(exGroupCols{snap: true, source: true})
	for _, want := range []string{"state = 'regressed'", "resolved_at IS NOT NULL", "resolve_source = ''", "NOT startsWith(fingerprint, 'ora:')", "LIMIT 2000", "max_execution_time"} {
		if !strings.Contains(q, want) {
			t.Errorf("aday sorgusu %q içermeli: %s", want, q)
		}
	}
	if strings.Contains(legacyRegressedSQL(exGroupCols{}), "resolve_source") {
		t.Error("kolon yokken aday sorgusu resolve_source anıyor")
	}
	for _, want := range []string{"service_name = ?", "time >= ", "time <= ", "LIMIT 1", "max_execution_time"} {
		if !strings.Contains(spanAtSQL, want) {
			t.Errorf("span kanıt sorgusu sınırsız (%q yok)", want)
		}
	}
}

// Kanıt kararı ve yazım: resolved_at damgalı adaylarla (Scan'i atlayıp
// karar + yazım yolunu doğrudan sınar).
func TestCorrectAutoResolvedRegressionsDecides(t *testing.T) {
	rs := int64(42) * int64(time.Hour)
	var qs []string
	var app [][]any
	st := &Store{conn: regressFixConn{spanAt: map[string]bool{"svc-a": true}, queries: &qs, appended: &app}}
	st.hasExResolveSourceCol.Store(true)
	cands := []ExceptionGroup{
		{Fingerprint: "fp-swept", Service: "svc-a", State: ExStateRegressed, ResolvedAt: &rs, Occurrences: 9, Notes: "n", Assignee: "u1"},
		{Fingerprint: "fp-human", Service: "svc-b", State: ExStateRegressed, ResolvedAt: &rs},
		{Fingerprint: "ora:abc", Service: "svc-a", State: ExStateRegressed, ResolvedAt: &rs},
		{Fingerprint: "fp-known", Service: "svc-a", State: ExStateRegressed, ResolvedAt: &rs, ResolveSource: ExResolveSourceUser},
	}
	fix, err := st.autoResolvedRegressionFixes(context.Background(), cands)
	if err != nil {
		t.Fatal(err)
	}
	if len(fix) != 1 || fix[0].Fingerprint != "fp-swept" || fix[0].State != ExStateNew || fix[0].ResolveSource != ExResolveSourceAuto ||
		fix[0].Notes != "n" || fix[0].Assignee != "u1" || fix[0].Occurrences != 9 || *fix[0].ResolvedAt != rs {
		t.Fatalf("düzeltme = %+v, want yalnız fp-swept → new/auto, tam satır", fix)
	}
}
