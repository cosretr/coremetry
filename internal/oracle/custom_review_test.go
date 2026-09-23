package oracle

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// custom_review_test.go — v0.10.902 inceleme turu regresyonları.

// Tavana çarpan özel sorgu hemen yeniden koşmaz (pencere SYSDATE'e bağlı;
// aynı 5000 satır, 5 s'de bir ağır aggregate). Kanca capped=true alır.
func TestPollCustomCappedNoHotLoop(t *testing.T) {
	src := customSource()
	src.ID, src.Name, src.IntervalSec = "o-dddddddd", "master-log", 60
	slice := float64(time.Date(2026, 9, 10, 8, 58, 0, 0, time.UTC).Unix())
	w, _, calls, now := newTestWorker(t, src, &fakeState{}, func(int, []any) ([]map[string]any, error) {
		out := make([]map[string]any, pollRowCap)
		for i := range out {
			out[i] = map[string]any{"TIMESLICE": slice, "OPERATIONCODE": "OP", "FUNCTIONCODE": "F", "KANALKOD": "K", "ADET": float64(2)}
		}
		return out, nil
	})
	var gotCapped bool
	w.SetRowsHook(func(_ context.Context, _ SourceConfig, _ []chstore.OracleErrorRow, _, _ time.Time, capped bool) {
		gotCapped = capped
	})
	w.Tick(context.Background())
	st := w.Status()[0]
	if !st.Capped || !gotCapped {
		t.Fatalf("tavan durumu/kanca: st=%v kanca=%v", st.Capped, gotCapped)
	}
	if want := now.Add(60 * time.Second).UnixMilli(); st.NextDueAt != want {
		t.Fatalf("özel kipte tavan bir sonraki aralığa: %d, beklenen %d", st.NextDueAt, want)
	}
	*now = now.Add(5 * time.Second)
	w.Tick(context.Background())
	if len(*calls) != 1 {
		t.Fatalf("5 s sonra yeniden koşmamalı: %d sorgu", len(*calls))
	}
}

// Kimlik değişken toplamları içermez: aynı grup Adet 3 → 4 olunca trace
// satırları ve artan satır AYNI row_id ile (RMT'de tek satır) kalır.
func TestAggregatedRowIDStableAcrossCountChange(t *testing.T) {
	m, err := NewMapper(aggregatedSrc())
	if err != nil {
		t.Fatal(err)
	}
	const a = "4bf92f3577b34da6a3ce929d0e0e4736"
	r3, _ := m.MapAll([]map[string]any{aggregatedRow(float64(3), a)})
	r4row := aggregatedRow(float64(4), a)
	r4row["DURATION"] = float64(555)
	r4, _ := m.MapAll([]map[string]any{r4row})
	if len(r3) != 2 || len(r4) != 2 {
		t.Fatalf("patlatma: %d / %d", len(r3), len(r4))
	}
	for i := range r3 {
		if r3[i].RowID != r4[i].RowID {
			t.Fatalf("satır %d kimliği Adet/DURATION ile değişti", i)
		}
	}
	if r4[1].Weight != 3 {
		t.Fatalf("artan ağırlık 4−1: %d", r4[1].Weight)
	}
	// Tablo kipi (count/traceIds eşlenmemiş) eski sözleşme: içerik kimliği.
	tm, _ := NewMapper(baseSrc())
	x, _, _ := tm.Map(sampleRow())
	if x.RowID != rowID(x) {
		t.Fatal("tablo kipinde kimlik tüm içerik olmalı")
	}
}

// NUMBER(14) YYYYMMDDHH24MISS epoch değildir (2612 yılı → watermark zehri).
func TestEpochRejectsCompactDates(t *testing.T) {
	m, _ := NewMapper(baseSrc())
	for _, v := range []any{float64(20260923152100), int64(20260923152100), "20260923152100", "2026092315", float64(2e13)} {
		if got, ok := m.parseTime(v); ok {
			t.Fatalf("%v epoch sayılmamalı: %v", v, got)
		}
	}
	if _, ok := m.parseTime("1790176860"); !ok {
		t.Fatal("10 haneli saniye geçerli")
	}
}

// DBMS_LOB.SUBSTR(…, 4000) ile kesik listenin son yarım id'si "geçersiz" sayılmaz.
func TestTruncatedTraceListLastFragment(t *testing.T) {
	m, _ := NewMapper(aggregatedSrc())
	id := "4bf92f3577b34da6a3ce929d0e0e4736"
	var b strings.Builder
	for b.Len() < traceListMaxChars-40 {
		b.WriteString(id + ", ")
	}
	b.WriteString(id[:22])
	for b.Len() < traceListMaxChars {
		b.WriteString("0")
	}
	_, st := m.MapAll([]map[string]any{aggregatedRow(float64(500), b.String())})
	if st.BadTraceID != 0 {
		t.Fatalf("kesik son parça geçersiz sayıldı: %d", st.BadTraceID)
	}
	_, st2 := m.MapAll([]map[string]any{aggregatedRow(float64(3), id+", kotu")})
	if st2.BadTraceID != 1 {
		t.Fatalf("kesik OLMAYAN listede bozuk id sayılmalı: %d", st2.BadTraceID)
	}
}

// Sarmalayıcı yorumları/ipuçlarını KORUR; literal içi -- sorguyu kesmez;
// sonda yorumlu ';' de düşer.
func TestWrapKeepsHintsAndLiterals(t *testing.T) {
	q := "SELECT /*+ INDEX(m ix_ts) */ m.a FROM t m WHERE m.b = 'x--y' AND m.ts >= SYSDATE - 1; -- not"
	w := WrapConsoleSQL(q, 10)
	if !strings.Contains(w, "/*+ INDEX(m ix_ts) */") || !strings.Contains(w, "'x--y' AND m.ts >= SYSDATE - 1") {
		t.Fatalf("metin korunmadı: %q", w)
	}
	if strings.Contains(w, ";") {
		t.Fatalf("sondaki ';' düşmeli: %q", w)
	}
	if !IsSafeConsoleSQL(q) {
		t.Fatal("denetim yorum sıyırarak geçmeli")
	}
}

// Özel kipte zaman kolonu zorunlu; tablo kipinde count/traceIds eşlemesi atılır;
// boş tip kutusu (Normalize → ERR_TYPE) çıktı kontrolünde eksik sayılmaz.
func TestNormalizeCustomTimestampAndTableModeDropsAggFields(t *testing.T) {
	c := customSource()
	c.TimestampColumn = ""
	if _, err := Normalize(one(c), Settings{}, NewSourceID); err == nil || !strings.Contains(err.Error(), "timestampColumn zorunlu") {
		t.Fatalf("özel kipte zaman kolonu zorunlu: %v", err)
	}
	tb := base()
	tb.Columns = map[string]string{FieldCount: "ADET", FieldTraceIDs: "TRACEIDS", FieldCode: "MCA_ERR_CODE"}
	out := mustNormalize(t, one(tb), Settings{}).Sources[0]
	if _, has := out.Columns[FieldCount]; has || out.Columns[FieldCode] != "MCA_ERR_CODE" {
		t.Fatalf("tablo kipi eşlemesi: %v", out.Columns)
	}
	nt := customSource()
	nt.TypeColumn = ""
	norm := mustNormalize(t, one(nt), Settings{}).Sources[0]
	mc := mappingCheckFromColumns(norm, []string{"TIMESLICE", "OPERATIONCODE", "FUNCTIONCODE", "KANALKOD", "HOSTNAME", "ADET", "TRACEIDS"})
	if len(mc.Missing) != 0 {
		t.Fatalf("boş tip kutusu eksik sayılmamalı: %+v", mc.Missing)
	}
}
