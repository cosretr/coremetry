package chstore

// v0.10.1072 — resolve anındaki hacim anlık görüntüsü
// (exception_groups.occurrences_at_resolve). Regressed grubun P1 kapısı
// bunun üstündeki hacme bakar (api.exceptionPriorityAt); burada yazım/okuma
// yolları ve şema/probe sözleşmesi pinlenir.

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// exSnapFakeConn — exception_groups okuma (QueryRow / Query) ve yazım
// (PrepareBatch) çiftinin CH'siz ikizi. row = saklı satırın Scan değerleri.
type exSnapFakeConn struct {
	driver.Conn
	row      []any
	colCount uint64
	queries  *[]string
	appended *[][]any
}

type exSnapRow struct {
	driver.Row
	vals []any
	err  error
}

func (r exSnapRow) Err() error { return r.err }
func (r exSnapRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return exSnapAssign(r.vals, dest)
}

type exSnapRows struct {
	driver.Rows
	rows [][]any
	i    int
}

func (r *exSnapRows) Next() bool             { r.i++; return r.i <= len(r.rows) }
func (r *exSnapRows) Err() error             { return nil }
func (r *exSnapRows) Close() error           { return nil }
func (r *exSnapRows) Scan(dest ...any) error { return exSnapAssign(r.rows[r.i-1], dest) }

func exSnapAssign(vals, dest []any) error {
	if len(vals) != len(dest) {
		return errors.New("Scan arite uyuşmazlığı")
	}
	for k, d := range dest {
		switch p := d.(type) {
		case *string:
			*p = vals[k].(string)
		case *int64:
			*p = vals[k].(int64)
		case *uint64:
			*p = vals[k].(uint64)
		case **time.Time:
			*p = nil
		default:
			return errors.New("beklenmeyen Scan hedefi")
		}
	}
	return nil
}

func (c exSnapFakeConn) QueryRow(ctx context.Context, q string, args ...any) driver.Row {
	*c.queries = append(*c.queries, q)
	if strings.Contains(q, "system.columns") {
		return exSnapRow{vals: []any{c.colCount}}
	}
	if c.row == nil {
		return exSnapRow{err: sql.ErrNoRows}
	}
	return exSnapRow{vals: c.row}
}

func (c exSnapFakeConn) Query(ctx context.Context, q string, args ...any) (driver.Rows, error) {
	*c.queries = append(*c.queries, q)
	return &exSnapRows{rows: [][]any{c.row}}, nil
}

func (c exSnapFakeConn) PrepareBatch(ctx context.Context, q string, opts ...driver.PrepareBatchOption) (driver.Batch, error) {
	*c.queries = append(*c.queries, q)
	return episodeFakeBatch{appended: c.appended}, nil
}

// exSnapStoredRow — açık (new) bir grup, 54.812 oluşum; snap=true ise
// saklı anlık görüntü (0) 14. kolon olarak eklenir.
func exSnapStoredRow(snap bool) []any {
	t0 := int64(1_790_000_000) * int64(time.Second)
	row := []any{"fp", "java.sql.SQLTimeoutException", "timeout", "crm-core", ExStateNew, "",
		t0, t0 + int64(time.Hour), nil, uint64(54_812), "", "", int64(0)}
	if snap {
		row = append(row, uint64(0))
	}
	return row
}

// TestResolveWritesOccurrenceSnapshot — manuel resolve ve bayat süpürme
// INSERT satırına o anki Occurrences'ı anlık görüntü olarak yazar.
func TestResolveWritesOccurrenceSnapshot(t *testing.T) {
	for _, path := range []string{"manuel", "bayat süpürme"} {
		t.Run(path, func(t *testing.T) {
			var qs []string
			var app [][]any
			st := &Store{conn: exSnapFakeConn{row: exSnapStoredRow(true), queries: &qs, appended: &app}}
			st.hasExResolveSnapCol.Store(true)
			var err error
			if path == "manuel" {
				err = st.SetExceptionGroupState(context.Background(), "fp", ExStateResolved)
			} else {
				_, err = st.AutoResolveStaleExceptionGroups(context.Background(), time.Hour)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(app) != 1 || len(app[0]) != 14 {
				t.Fatalf("INSERT = %v, want 14 değerli tek satır", app)
			}
			if app[0][4] != ExStateResolved {
				t.Errorf("state = %v, want resolved", app[0][4])
			}
			if app[0][13] != uint64(54_812) {
				t.Errorf("occurrences_at_resolve = %v, want 54812", app[0][13])
			}
		})
	}
}

// TestExceptionSnapshotOmittedWithoutColumn — probe false (küme kipinde
// kolonu ekleyen boot): okuma ve INSERT kolonu ANMAZ; resolve yine yazılır.
func TestExceptionSnapshotOmittedWithoutColumn(t *testing.T) {
	var qs []string
	var app [][]any
	st := &Store{conn: exSnapFakeConn{row: exSnapStoredRow(false), queries: &qs, appended: &app}}
	if err := st.SetExceptionGroupState(context.Background(), "fp", ExStateResolved); err != nil {
		t.Fatal(err)
	}
	if len(app) != 1 || len(app[0]) != 13 {
		t.Fatalf("INSERT = %v, want 13 değerli tek satır", app)
	}
	for _, q := range qs {
		if strings.Contains(q, "occurrences_at_resolve") {
			t.Errorf("probe false iken sorgu kolonu anıyor: %q", q)
		}
	}
}

// TestMergeCarriesResolveSnapshot — yenileme taraması alanı üretmez;
// regresyonda (tam o anda kapı onu okur) ve sonrasında ileri taşınır.
func TestMergeCarriesResolveSnapshot(t *testing.T) {
	resolved := int64(1000)
	existing := &ExceptionGroup{Fingerprint: "fp", State: ExStateResolved, ResolvedAt: &resolved,
		Occurrences: 54_000, OccurrencesAtResolve: 54_000}
	fresh := ExceptionGroup{Fingerprint: "fp", LastSeen: resolved + int64(time.Hour), Occurrences: 812}
	got := mergeExceptionGroup(fresh, existing)
	if got.State != ExStateRegressed {
		t.Fatalf("state = %s, want regressed", got.State)
	}
	if got.OccurrencesAtResolve != 54_000 {
		t.Errorf("anlık görüntü taşınmadı: %d", got.OccurrencesAtResolve)
	}
	if since, ok := ExceptionOccurrencesSinceResolve(got); !ok || since != 812 {
		t.Errorf("since = (%d, %v), want (812, true)", since, ok)
	}
	if _, ok := ExceptionOccurrencesSinceResolve(ExceptionGroup{Occurrences: 50_000}); ok {
		t.Error("anlık görüntüsüz grup 'since' üretti — ömür toplamına düşülmemeli")
	}
}

// TestResolveAgainResetsSnapshot — regress → yeniden resolve → regress:
// ikinci resolve anlık görüntüyü O ANKİ sayıya günceller, yani ikinci
// regresyonun "yeni" hacmi ilk resolve'dan değil ikinciden sayılır.
// Hem saf geçiş zinciri hem gerçek yazım yolu (SetExceptionGroupState).
func TestResolveAgainResetsSnapshot(t *testing.T) {
	const h = int64(time.Hour)
	g := ExceptionGroup{Fingerprint: "fp", State: ExStateNew, Occurrences: 1_000, LastSeen: 10 * h}
	markExceptionResolved(&g, g.LastSeen) // 1. resolve: görüntü 1000
	g = mergeExceptionGroup(ExceptionGroup{Fingerprint: "fp", LastSeen: 12 * h, Occurrences: 600}, &g)
	if since, ok := ExceptionOccurrencesSinceResolve(g); g.State != ExStateRegressed || !ok || since != 600 {
		t.Fatalf("1. regresyon: state=%s since=(%d,%v), want regressed 600", g.State, since, ok)
	}
	markExceptionResolved(&g, g.LastSeen) // 2. resolve: görüntü 1600
	if g.OccurrencesAtResolve != 1_600 {
		t.Fatalf("2. resolve görüntüyü güncellemedi: %d, want 1600", g.OccurrencesAtResolve)
	}
	g = mergeExceptionGroup(ExceptionGroup{Fingerprint: "fp", LastSeen: 14 * h, Occurrences: 100}, &g)
	if since, ok := ExceptionOccurrencesSinceResolve(g); g.State != ExStateRegressed || !ok || since != 100 {
		t.Fatalf("2. regresyon: state=%s since=(%d,%v), want regressed 100 (eski görüntüden 700 DEĞİL)", g.State, since, ok)
	}

	// Yazım yolu: saklı satır regressed, 1600 oluşum, eski görüntü 1000 →
	// manuel resolve INSERT'i görüntüyü 1600'e günceller.
	row := exSnapStoredRow(true)
	row[4] = ExStateRegressed
	row[9] = uint64(1_600)
	row[13] = uint64(1_000)
	var qs []string
	var app [][]any
	st := &Store{conn: exSnapFakeConn{row: row, queries: &qs, appended: &app}}
	st.hasExResolveSnapCol.Store(true)
	if err := st.SetExceptionGroupState(context.Background(), "fp", ExStateResolved); err != nil {
		t.Fatal(err)
	}
	if len(app) != 1 || app[0][13] != uint64(1_600) {
		t.Fatalf("yeniden resolve INSERT'i = %v, want occurrences_at_resolve 1600", app)
	}
}

// TestExceptionResolveSnapshotColumnMigration — şema + probe sözleşmesi:
// ALTER `alters` diliminde (planDDL tanır), CREATE TABLE'a eklenmemiş, boot
// probe'u ve ertelenen-DDL yeniden probe'u bağlı.
func TestExceptionResolveSnapshotColumnMigration(t *testing.T) {
	const want = "ALTER TABLE exception_groups ADD COLUMN IF NOT EXISTS occurrences_at_resolve UInt64 DEFAULT 0"
	found := false
	for _, a := range migrateDDLSlice(t, "alters") {
		if a == want {
			found = true
		}
	}
	if !found {
		t.Errorf("alters diliminde yok: %q", want)
	}
	if mm := addColumnRe.FindStringSubmatch(want); mm == nil || mm[1] != "exception_groups" {
		t.Errorf("addColumnRe eşleşmiyor: %q", want)
	}
	if ddl := tableDDLByName(canonicalTables(30, 30, 7), "exception_groups"); strings.Contains(ddl, "occurrences_at_resolve") ||
		!strings.Contains(ddl, "ORDER BY fingerprint") {
		t.Errorf("exception_groups CREATE değişmiş:\n%s", ddl)
	}
	src := mustReadSource(t, "store.go")
	if !strings.Contains(src, "exOK, exErr := s.probeExResolveSnapCol(ctx)") ||
		!strings.Contains(src, "s.hasExResolveSnapCol.Store(exOK)") {
		t.Error("hasExResolveSnapCol boot probe'u store.go migrate()'te yok")
	}
	if !strings.Contains(mustReadSource(t, "ddl_defer.go"), "s.reprobeExResolveSnapCol(ctx)") {
		t.Error("reprobePromotedAttrs anlık görüntü kolonunu yeniden denemiyor — küme kipinde bayrak süreç ömrü boyunca false donar")
	}
}

// TestReprobeExResolveSnapCol — kolon inince bayrak çevrilir ve bir sonraki
// yazım kolonu taşır.
func TestReprobeExResolveSnapCol(t *testing.T) {
	var qs []string
	var app [][]any
	conn := exSnapFakeConn{row: exSnapStoredRow(false), queries: &qs, appended: &app}
	st := &Store{conn: conn}
	if st.reprobeExResolveSnapCol(context.Background()) {
		t.Fatal("kolon yokken bayrak true döndü")
	}
	conn.colCount = 1
	conn.row = exSnapStoredRow(true)
	st.conn = conn
	if !st.reprobeExResolveSnapCol(context.Background()) {
		t.Fatal("kolon inince yeniden probe bayrağı çevirmedi")
	}
	if err := st.SetExceptionGroupState(context.Background(), "fp", ExStateResolved); err != nil {
		t.Fatal(err)
	}
	if len(app) != 1 || len(app[0]) != 14 || app[0][13] != uint64(54_812) {
		t.Fatalf("yeniden probe sonrası INSERT = %v, want anlık görüntülü 14 değer", app)
	}
}
