package oracle

// custom_paging_test.go — v0.10.1092 iki önkoşul düzeltmesinin pinleri:
//
//  A. Başarılı ÖZEL SQL UI testi durum şeridine "başarısız (gerekçesiz)"
//     yazılıyordu: TestWith'in defer'i yerel `res`i yakalıyor, özel dal bir
//     KOPYA döndürüyordu. Adlı dönüşle kapanış çağırana gideni görür.
//  B. Özel SQL penceresi tek atış `FETCH FIRST 5000` ile okunuyordu (sırasız,
//     bind'siz, yeniden poll'suz): pencerenin kalanı hiç okunmuyordu. Artık TEK
//     ifade: ORDER BY "<zaman>", "<eşlenen anahtarlar>" FETCH FIRST tavan+1 —
//     tek anlık görüntü, tek SYSDATE; satır > tavan → kesik.
//
// Adlar sentetik (APP_ERR_*, db-host-01); Oracle yok — sahte database/sql
// sürücüsü ve sahte satır okuyucu.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const syntheticCustomSQL = `SELECT ROUND((TRUNC(E.APP_ERR_TS, 'MI') - DATE '1970-01-01') * 86400) AS TIMESLICE,
       E.APP_ERR_CHANNEL AS CHANNEL, E.APP_ERR_CODE AS ERRCODE, E.APP_ERR_OP AS OPCODE,
       COUNT(*) AS ADET
FROM   APP_SCHEMA.APP_ERR_LOG E
WHERE  E.APP_ERR_TS >= TRUNC(SYSDATE, 'MI') - INTERVAL '15' MINUTE
GROUP BY TRUNC(E.APP_ERR_TS, 'MI'), E.APP_ERR_CHANNEL, E.APP_ERR_CODE, E.APP_ERR_OP;`

func syntheticCustomSource() SourceConfig {
	return SourceConfig{
		ID: "o-11111111", Name: "app-err", Enabled: true, Host: "db-host-01", Port: 1521,
		ServiceName: "APPPDB", User: "u", Password: "p", IntervalSec: 60,
		QueryMode: QueryModeCustom, CustomSQL: syntheticCustomSQL,
		TimestampColumn: "TIMESLICE", TypeColumn: "ERRCODE",
		Columns: map[string]string{
			FieldService: "OPCODE", FieldCode: "ERRCODE", FieldChannel: "CHANNEL", FieldCount: "ADET",
		},
	}
}

// ── sahte database/sql sürücüsü (yalnız SELECT; her sorgu aynı satırları döndürür) ──

type fakeDrv struct {
	cols []string
	rows [][]driver.Value
}

func (d *fakeDrv) Open(string) (driver.Conn, error) { return &fakeConn{d: d}, nil }

type fakeConn struct{ d *fakeDrv }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return &fakeStmt{d: c.d}, nil }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("tx yok") }

type fakeStmt struct{ d *fakeDrv }

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }
func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("yalnız SELECT")
}
func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	return &fakeDrvRows{cols: s.d.cols, rows: s.d.rows}, nil
}

type fakeDrvRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *fakeDrvRows) Columns() []string { return r.cols }
func (r *fakeDrvRows) Close() error      { return nil }
func (r *fakeDrvRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

var fakeDrvOnce sync.Once
var fakeDrvShared = &fakeDrv{}

func fakeOracleDB(t *testing.T, cols []string, rows [][]driver.Value) func(string) (sqlDB, error) {
	t.Helper()
	fakeDrvOnce.Do(func() { sql.Register("oracle-fake-v0-10-next", fakeDrvShared) })
	fakeDrvShared.cols, fakeDrvShared.rows = cols, rows
	return func(dsn string) (sqlDB, error) { return sql.Open("oracle-fake-v0-10-next", dsn) }
}

// A — başarılı özel SQL testi durum satırına OK olarak düşer (regresyon:
// eskiden OK=false, Error="" kaydediliyordu → "Son bağlantı denemesi … başarısız").
func TestTestWith_CustomSuccessRecordedOK(t *testing.T) {
	src := syntheticCustomSource()
	svc := New()
	slice := time.Date(2026, 10, 3, 9, 58, 0, 0, time.UTC).Unix()
	svc.openDB = fakeOracleDB(t, []string{"TIMESLICE", "CHANNEL", "ERRCODE", "OPCODE", "ADET"}, [][]driver.Value{
		{slice, "MOB", "APP_ERR_001", "OP_TRANSFER", int64(4)},
	})
	res := svc.TestWith(context.Background(), src, TestOptions{})
	if !res.OK || res.Error != "" || res.RowCount != 1 {
		t.Fatalf("özel test başarılı olmalıydı: ok=%v err=%q rows=%d", res.OK, res.Error, res.RowCount)
	}
	if !strings.HasSuffix(res.PollQuery, "\nORDER BY \"TIMESLICE\", \"OPCODE\", \"ERRCODE\", \"CHANNEL\"\nFETCH FIRST 50001 ROWS ONLY") {
		t.Fatalf("önizleme poller'ın tek sıralı ifadesi olmalı: %q", res.PollQuery)
	}
	// L9 — sorgunun INTERVAL '15' MINUTE'u pencereyle (15) tutarlı: uyarı yok.
	if strings.Contains(res.Hint, "INTERVAL") || strings.Contains(res.Hint, "geriye bakış") {
		t.Fatalf("tutarlı pencerede uyarı olmamalı: %q", res.Hint)
	}
	svc.Configure(Settings{Sources: []SourceConfig{src}})
	st := svc.Status()
	if len(st) != 1 || st[0].LastCheckAt == 0 || !st[0].LastCheckOK || st[0].LastError != "" {
		t.Fatalf("durum satırı başarılı testi OK kaydetmeli: %+v", st)
	}
}

// L9 — sorgunun geriye bakışı ile pencere tutarlılığı.
func TestCustomIntervalWarning(t *testing.T) {
	for _, c := range []struct {
		sql  string
		win  int
		want string // "" = uyarı yok; aksi alt dize
	}{
		{"SELECT 1 FROM t WHERE ts >= SYSDATE - INTERVAL '15' MINUTE", 15, ""},
		{"select 1 from t where ts >= sysdate - interval '20' minute", 15, "20 dk > pencere 15 dk"},
		{"SELECT 1 FROM t WHERE ts >= SYSDATE - INTERVAL '1' HOUR", 30, "60 dk > pencere 30 dk"},
		{"SELECT 1 FROM t WHERE ts >= SYSDATE - INTERVAL '10' MINUTE", 15, "Pencere 15 dk > sorgunun geriye bakışı 10 dk"},
		{"SELECT 1 FROM t WHERE ts >= SYSDATE - 15/1440", 15, "bulunamadı"},
	} {
		got := customIntervalWarning(c.sql, c.win)
		if (c.want == "") != (got == "") || (c.want != "" && !strings.Contains(got, c.want)) {
			t.Errorf("%q / %d → %q, beklenen %q", c.sql, c.win, got, c.want)
		}
	}
}

// B — sıralı ifadenin ALTIN metni: zaman önce, sonra yalnız AÇIKÇA eşlenen
// kararlı kolonlar (count/traceIds yok), adlar TIRNAKLI, FETCH FIRST tavan+1,
// sondaki ';' düşer. Eşlemede küçük harf varsa ikinci yazım büyük harf.
func TestBuildCustomOrderedQuery_Golden(t *testing.T) {
	src := syntheticCustomSource()
	vs := customOrderVariants(src)
	if len(vs) != 1 {
		t.Fatalf("eşleme zaten büyük harf → tek yazım: %v", vs)
	}
	got := buildCustomOrderedQuery(src, vs[0])
	want := "SELECT * FROM (\n" + strings.TrimSuffix(syntheticCustomSQL, ";") +
		"\n)\nORDER BY \"TIMESLICE\", \"OPCODE\", \"ERRCODE\", \"CHANNEL\"\nFETCH FIRST 50001 ROWS ONLY"
	if got != want {
		t.Fatalf("sıralı ifade:\n%s\n--- beklenen ---\n%s", got, want)
	}
	// Tırnaklı takma ad (AS "TimeSlice") — eşlemedeki yazımla tırnaklı önce,
	// büyük harf yedeği sonra.
	q := syntheticCustomSource()
	q.TimestampColumn = "TimeSlice"
	q.Columns = map[string]string{FieldService: "OpCode", FieldCode: "ERRCODE"}
	vq := customOrderVariants(q)
	if len(vq) != 2 || strings.Join(vq[0], ", ") != `"TimeSlice", "OpCode", "ERRCODE"` || strings.Join(vq[1], ", ") != `"TIMESLICE", "OPCODE", "ERRCODE"` {
		t.Fatalf("tırnaklı yazımlar: %v", vq)
	}
	// maxPages tavanı: 2 sayfa → FETCH FIRST 10001.
	q.MaxPages = 2
	if s := buildCustomOrderedQuery(q, vq[0]); !strings.HasSuffix(s, "FETCH FIRST 10001 ROWS ONLY") {
		t.Fatalf("tavan: %q", s[len(s)-40:])
	}
	// Konsol sarmalayıcısı DEĞİŞMEDİ (sırasız, FETCH FIRST).
	if c := WrapConsoleSQL("SELECT 1 FROM dual", 10); c != "SELECT * FROM (\nSELECT 1 FROM dual\n) FETCH FIRST 10 ROWS ONLY" {
		t.Fatalf("konsol sarmalayıcısı: %q", c)
	}
	if ws := WrapConsoleSQLOrdered("SELECT 1 FROM dual", nil, 0); ws != fmt.Sprintf("SELECT * FROM (\nSELECT 1 FROM dual\n)\nFETCH FIRST %d ROWS ONLY", maxCustomFetch) {
		t.Fatalf("sırasız yedek + kelepçe: %q", ws)
	}
}

func TestMaxPagesOfAndNormalize(t *testing.T) {
	for _, c := range []struct{ in, want int }{{0, 10}, {1, 1}, {20, 20}, {21, 10}, {-1, 10}} {
		if got := MaxPagesOf(SourceConfig{MaxPages: c.in}); got != c.want {
			t.Errorf("MaxPagesOf(%d)=%d, beklenen %d", c.in, got, c.want)
		}
	}
	ok := syntheticCustomSource()
	ok.MaxPages = 0
	out, err := Normalize(one(ok), Settings{}, NewSourceID)
	if err != nil || out.Sources[0].MaxPages != 0 {
		t.Fatalf("0 = varsayılan, blob'a yazılmaz: %v %+v", err, out.Sources)
	}
	bad := syntheticCustomSource()
	bad.MaxPages = 21
	if _, err := Normalize(one(bad), Settings{}, NewSourceID); err == nil || !strings.Contains(err.Error(), "maxPages") {
		t.Fatalf("aralık dışı reddedilmeli: %v", err)
	}
}

func TestCustomTruncatedText(t *testing.T) {
	if got := CustomTruncatedText(10); got != "tavan: pencerenin tamamı okunamadı (10 sayfa)" {
		t.Fatalf("metin: %q", got)
	}
}

// rowsN — n satır, her biri farklı kanal.
func rowsN(slice int64, n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = map[string]any{"TIMESLICE": float64(slice), "OPCODE": "OP_TRANSFER", "ERRCODE": "APP_ERR_001",
			"CHANNEL": fmt.Sprintf("C%d", i), "ADET": float64(1)}
	}
	return out
}

// B — TEK ifade: tavanın altında pencere TAM (Capped/Truncated yok, kanca
// capped=false, sayaç son-kesimi yok); 11 200 satır tek sorguda.
func TestPollCustomSingleStatementComplete(t *testing.T) {
	src := syntheticCustomSource()
	slice := time.Date(2026, 9, 10, 8, 58, 0, 0, time.UTC).Unix()
	w, sink, calls, _ := newTestWorker(t, src, &fakeState{}, func(int, []any) ([]map[string]any, error) {
		return rowsN(slice, 11200), nil
	})
	var hookCapped bool
	var hooked int
	w.SetRowsHook(func(_ context.Context, _ SourceConfig, rows []chstore.OracleErrorRow, _, _ time.Time, capped bool) {
		hookCapped, hooked = capped, len(rows)
	})
	w.Tick(context.Background())
	if len(*calls) != 1 || (*calls)[0].args != nil || !strings.HasSuffix((*calls)[0].sql, "FETCH FIRST 50001 ROWS ONLY") {
		t.Fatalf("tek sıralı ifade bekleniyor: %d sorgu", len(*calls))
	}
	st := w.Status()[0]
	if st.Truncated || st.Capped || st.Unordered || hookCapped || st.LastRows != 11200 || hooked != 11200 {
		t.Fatalf("tam pencere: st=%+v kancaCapped=%v kanca=%d", st, hookCapped, hooked)
	}
	if len(sink.calls) != 1 || len(sink.calls[0]) != 11200 {
		t.Fatalf("yazım: %d çağrı", len(sink.calls))
	}
}

// B — satır > tavan → pencere KESİK (Truncated + Capped), fazla satır atılır,
// kanca capped=true.
func TestPollCustomRowCap(t *testing.T) {
	src := syntheticCustomSource()
	src.MaxPages = 2
	slice := time.Date(2026, 9, 10, 8, 58, 0, 0, time.UTC).Unix()
	w, _, calls, _ := newTestWorker(t, src, &fakeState{}, func(int, []any) ([]map[string]any, error) {
		return rowsN(slice, 2*customPageSize+1), nil
	})
	var hookCapped bool
	w.SetRowsHook(func(_ context.Context, _ SourceConfig, _ []chstore.OracleErrorRow, _, _ time.Time, capped bool) {
		hookCapped = capped
	})
	w.Tick(context.Background())
	st := w.Status()[0]
	if len(*calls) != 1 || !st.Truncated || !st.Capped || st.Pages != 2 || !hookCapped || st.LastRows != 2*customPageSize || st.LastError != "" {
		t.Fatalf("satır tavanı: calls=%d st=%+v kanca=%v", len(*calls), st, hookCapped)
	}
}

// B — ORA-00904: tırnaklı eşleme yazımı → büyük harf yazımı → SIRASIZ yedek
// (Capped + Unordered; kesiklik yine satır sayısından).
func TestPollCustomOrderFallbacks(t *testing.T) {
	src := syntheticCustomSource()
	src.TimestampColumn = "TimeSlice" // iki yazım: "TimeSlice" sonra "TIMESLICE"
	slice := time.Date(2026, 9, 10, 8, 58, 0, 0, time.UTC).Unix()
	w, _, calls, now := newTestWorker(t, src, &fakeState{}, func(call int, _ []any) ([]map[string]any, error) {
		if call == 1 {
			return nil, errors.New(`sorgu: ORA-00904: "TimeSlice": invalid identifier`)
		}
		return rowsN(slice, 3), nil
	})
	w.Tick(context.Background())
	if len(*calls) != 2 || !strings.Contains((*calls)[0].sql, `ORDER BY "TimeSlice"`) || !strings.Contains((*calls)[1].sql, `ORDER BY "TIMESLICE"`) {
		t.Fatalf("ikinci yazım büyük harf olmalı: %d sorgu", len(*calls))
	}
	if st := w.Status()[0]; st.LastError != "" || st.Unordered || st.Capped || st.LastRows != 3 {
		t.Fatalf("büyük harf yazımı tuttu: %+v", st)
	}
	// İki yazım da düşerse: sırasız yedek, Capped + Unordered.
	*now = now.Add(61 * time.Second)
	n := 0
	w.queryRows = func(_ context.Context, _ SourceConfig, sqlText string, _ []any) ([]map[string]any, error) {
		n++
		*calls = append(*calls, queryCall{sql: sqlText})
		if n <= 2 {
			return nil, errors.New("sorgu: ORA-00904: invalid identifier")
		}
		return rowsN(slice, 3), nil
	}
	w.Tick(context.Background())
	last := (*calls)[len(*calls)-1].sql
	if n != 3 || strings.Contains(last, "ORDER BY") || !strings.HasSuffix(last, "FETCH FIRST 50001 ROWS ONLY") {
		t.Fatalf("sırasız yedek: n=%d %q", n, last)
	}
	if st := w.Status()[0]; st.LastError != "" || !st.Unordered || !st.Capped || st.Truncated {
		t.Fatalf("sırasız yedek durumu: %+v", st)
	}
	// ORA-00904 dışı hata poll hatasıdır (yedek denenmez).
	*now = now.Add(61 * time.Second)
	w.queryRows = func(context.Context, SourceConfig, string, []any) ([]map[string]any, error) {
		return nil, errors.New("ORA-01013: user requested cancel")
	}
	w.Tick(context.Background())
	if st := w.Status()[0]; st.LastError == "" {
		t.Fatal("başka hata poll hatası olmalı")
	}
}
