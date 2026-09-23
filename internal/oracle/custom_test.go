package oracle

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// custom_test.go — v0.10.902 (operatör: "sorgunun son hâli bu, ona göre
// güncelle"). Özel SQL kipi pinleri: Normalize kapıları (allow-list, uzunluk,
// pencere kelepçesi, şema/tablo zorunlu değil), sarmalayıcı (tavan, bind yok),
// çıktı-kolonu eşleme kontrolü, sayaç penceresi.

// operatorSQL — ekran görüntüsündeki sorgunun şekli (şema/tablo adları
// sentetik; JOIN + HAVING + XMLAGG + SYSDATE penceresi, bind yok).
const operatorSQL = `SELECT ROUND((TRUNC(M.OPM_TIMESTAMP, 'MI') - DATE '1970-01-01') * 86400) - 10800 AS TimeSlice,
       TO_CHAR(TRUNC(M.OPM_TIMESTAMP, 'MI'), 'DD.MM.YYYY HH24:MI') AS Zaman,
       COALESCE(M.OPM_CHANNELCODE, 'XXX') AS KanalKod,
       M.OPM_FUNCTIONCODE AS FUNCTIONCODE,
       M.OPM_OPERATIONCODE AS OPERATIONCODE,
       UPPER(M.OPM_WEBSRVNAME) AS HOSTNAME,
       'TFAIL' AS Sonuc,
       COUNT(*) AS Adet,
       COUNT(M.OPM_TRACEID) AS TraceIdAdet,
       DBMS_LOB.SUBSTR(RTRIM(XMLAGG(XMLELEMENT(e, M.OPM_TRACEID || ', ') ORDER BY M.OPM_TIMESTAMP).EXTRACT('//text()').getClobVal(), ', '), 4000, 1) AS TRACEIDS
FROM   APPOWNER.MASTER_LOG M
LEFT JOIN APPOWNER.ERROR_CODE_LOOKUP B ON M.OPM_ERRORCODE = B.ERROR_CODE
WHERE  M.OPM_TIMESTAMP >= TRUNC(SYSDATE, 'MI') - INTERVAL '15' MINUTE
  AND  M.OPM_TIMESTAMP <  TRUNC(SYSDATE, 'MI')
  AND  (B.ERRORTYPE = 'T' OR B.ERROR_CODE IS NULL)
GROUP BY TRUNC(M.OPM_TIMESTAMP, 'MI'), COALESCE(M.OPM_CHANNELCODE, 'XXX'), M.OPM_FUNCTIONCODE, M.OPM_OPERATIONCODE, UPPER(M.OPM_WEBSRVNAME)
HAVING COUNT(*) > 1
ORDER BY TimeSlice, Adet DESC;`

func customSource() SourceConfig {
	s := base()
	s.Schema, s.Table = "", ""
	s.QueryMode = QueryModeCustom
	s.CustomSQL = operatorSQL
	s.TimestampColumn = "TIMESLICE"
	s.TypeColumn = "SONUC"
	s.Columns = map[string]string{
		FieldService: "OPERATIONCODE", FieldCode: "FUNCTIONCODE", FieldChannel: "KANALKOD",
		FieldHost: "HOSTNAME", FieldCount: "ADET", FieldTraceIDs: "TRACEIDS",
	}
	return s
}

func TestNormalize_CustomMode(t *testing.T) {
	out := mustNormalize(t, one(customSource()), Settings{})
	s := out.Sources[0]
	if !IsCustom(s) || QueryModeOf(s) != QueryModeCustom || s.WindowMin != DefaultWindowMin {
		t.Fatalf("özel kip normalize: mode=%q window=%d", s.QueryMode, s.WindowMin)
	}
	if !strings.HasSuffix(s.CustomSQL, "DESC;") {
		t.Fatalf("metin kırpılmış ama aynen saklanmalı (sondaki ; sarmalayıcıda düşer): %q", s.CustomSQL[len(s.CustomSQL)-10:])
	}
	// Tablo kipi: kip boş kalır (eski bloblar değişmez), IsCustom false.
	tbl := mustNormalize(t, one(base()), Settings{}).Sources[0]
	if tbl.QueryMode != "" || IsCustom(tbl) || QueryModeOf(tbl) != QueryModeTable || tbl.WindowMin != DefaultWindowMin {
		t.Fatalf("tablo kipi: %+v", tbl.QueryMode)
	}
	// Etkin özel kaynakta metin zorunlu; şema/tablo DEĞİL.
	empty := customSource()
	empty.CustomSQL = ""
	if _, err := Normalize(one(empty), Settings{}, NewSourceID); err == nil || !strings.Contains(err.Error(), "customSql zorunlu") {
		t.Fatalf("boş özel SQL kabul edildi: %v", err)
	}
	// Tablo kipinde şema/tablo hâlâ zorunlu.
	noTbl := base()
	noTbl.Table = ""
	if _, err := Normalize(one(noTbl), Settings{}, NewSourceID); err == nil || !strings.Contains(err.Error(), "şema ve tablo") {
		t.Fatalf("tablo kipinde tablo zorunlu kalmalı: %v", err)
	}
	// Allow-list: DML / çoklu ifade / FOR UPDATE ret; kapalı taslakta da.
	for _, bad := range []string{"DELETE FROM t", "SELECT 1 FROM dual; DROP TABLE t", "SELECT * FROM t FOR UPDATE", "BEGIN NULL; END;"} {
		c := customSource()
		c.CustomSQL, c.Enabled = bad, false
		if _, err := Normalize(one(c), Settings{}, NewSourceID); err == nil {
			t.Fatalf("kabul edilmemeliydi: %q", bad)
		}
	}
	long := customSource()
	long.CustomSQL = "SELECT " + strings.Repeat("x", MaxCustomSQLLen) + " FROM dual"
	if _, err := Normalize(one(long), Settings{}, NewSourceID); err == nil || !strings.Contains(err.Error(), "karakter") {
		t.Fatalf("uzunluk kapısı: %v", err)
	}
	// Pencere: 0 → varsayılan; aralık dışı → hata (sessiz kırpma yok).
	for _, c := range []struct {
		in   int
		want int
		err  bool
	}{{0, 15, false}, {1, 1, false}, {240, 240, false}, {241, 0, true}, {-5, 0, true}} {
		src := customSource()
		src.WindowMin = c.in
		out, err := Normalize(one(src), Settings{}, NewSourceID)
		if (err != nil) != c.err {
			t.Fatalf("windowMin %d: err=%v", c.in, err)
		}
		if err == nil && out.Sources[0].WindowMin != c.want {
			t.Fatalf("windowMin %d → %d, beklenen %d", c.in, out.Sources[0].WindowMin, c.want)
		}
	}
}

func TestBuildCustomPollQuery_WrapsAndCaps(t *testing.T) {
	src := customSource()
	q := buildCustomPollQuery(src, 0)
	if !strings.HasPrefix(q, "SELECT * FROM (\n") || !strings.HasSuffix(q, "\n) FETCH FIRST 5000 ROWS ONLY") {
		t.Fatalf("sarmalayıcı: %q", q)
	}
	if strings.Contains(q, ";") {
		t.Fatal("sondaki ';' sarmalayıcıda düşmeli (Oracle alt sorguda kabul etmez)")
	}
	if !strings.Contains(q, "HAVING COUNT(*) > 1") || !strings.Contains(q, "XMLAGG") {
		t.Fatal("operatör metni aynen taşınmalı")
	}
	if q2 := buildCustomPollQuery(src, 7); !strings.HasSuffix(q2, "FETCH FIRST 7 ROWS ONLY") {
		t.Fatalf("tavan: %q", q2[len(q2)-30:])
	}
	if q3 := buildCustomPollQuery(src, pollRowCap+1); !strings.HasSuffix(q3, "FETCH FIRST 5000 ROWS ONLY") {
		t.Fatal("tavan üstü pollRowCap'e kelepçelenir")
	}
}

func TestCustomWindowAndMappingCheckFromColumns(t *testing.T) {
	src := customSource()
	now := time.Date(2026, 9, 23, 15, 30, 0, 0, time.UTC)
	from, to := customWindow(src, now)
	if !to.Equal(now) || !from.Equal(now.Add(-15*time.Minute)) {
		t.Fatalf("pencere: %v..%v", from, to)
	}
	src.WindowMin = 999
	if f, _ := customWindow(src, now); !f.Equal(now.Add(-15 * time.Minute)) {
		t.Fatal("kelepçe dışı pencere varsayılana döner")
	}

	// Sorgu çıktısı (sürücü büyük harf): count/traceIds/service/... var, code (FUNCTIONCODE) yok.
	cols := []string{"TIMESLICE", "ZAMAN", "KANALKOD", "OPERATIONCODE", "HOSTNAME", "SONUC", "ADET", "TRACEIDADET", "DURATION", "TRACEIDS"}
	mc := mappingCheckFromColumns(customSource(), cols)
	if !mc.Checked || mc.Source != "query" || mc.Prefix != "" {
		t.Fatalf("kontrol: %+v", mc)
	}
	if len(mc.Missing) != 1 || mc.Missing[0].Field != FieldCode || mc.Missing[0].Column != "FUNCTIONCODE" || mc.Missing[0].Suggest != "" {
		t.Fatalf("eksik listesi: %+v", mc.Missing)
	}
	// timestamp (TIMESLICE), type (SONUC), service, channel, host, count, traceIds = 7 mevcut.
	if mc.Present != 7 {
		t.Fatalf("mevcut sayısı %d", mc.Present)
	}
	if e := mappingCheckFromColumns(customSource(), nil); e.Checked || e.Error == "" {
		t.Fatalf("kolonsuz çıktı: %+v", e)
	}
}

// TestPollCustomMode — poller özel kipte: sarmalanmış metin, BİND YOK, pencere
// [now − 15 dk, now]; satırlar TimeSlice (epoch sn) + Adet + TRACEIDS ile
// eşlenip patlatılır; kanca patlatılmış satırları alır.
func TestPollCustomMode(t *testing.T) {
	src := customSource()
	src.ID = "o-cccccccc"
	src.Name = "master-log"
	src.IntervalSec = 60
	state := &fakeState{}
	slice := float64(time.Date(2026, 9, 10, 8, 58, 0, 0, time.UTC).Unix())
	w, sink, calls, now := newTestWorker(t, src, state, func(int, []any) ([]map[string]any, error) {
		return []map[string]any{{
			"TIMESLICE": slice, "ZAMAN": "10.09.2026 11:58", "KANALKOD": "059912", "FUNCTIONCODE": "FSR0002",
			"OPERATIONCODE": "COMPLIANCE_ACTIMIZE", "HOSTNAME": "GATEWAY-01", "SONUC": "TFAIL",
			"ADET": float64(3), "TRACEIDADET": float64(2), "DURATION": float64(412),
			"TRACEIDS": "4bf92f3577b34da6a3ce929d0e0e4736, 0af7651916cd43dd8448eb211c80319c",
		}}, nil
	})
	var hooked []chstore.OracleErrorRow
	var hFrom, hTo time.Time
	w.SetRowsHook(func(_ context.Context, _ SourceConfig, rows []chstore.OracleErrorRow, from, to time.Time, _ bool) {
		hooked, hFrom, hTo = rows, from, to
	})
	w.Tick(context.Background())
	if len(*calls) != 1 {
		t.Fatalf("sorgu sayısı %d", len(*calls))
	}
	c := (*calls)[0]
	if c.args != nil || !strings.HasPrefix(c.sql, "SELECT * FROM (") || !strings.Contains(c.sql, "HAVING COUNT(*) > 1") || !strings.HasSuffix(c.sql, "FETCH FIRST 5000 ROWS ONLY") {
		t.Fatalf("özel kip sorgusu: args=%v sql=%q", c.args, c.sql)
	}
	if !hTo.Equal(*now) || !hFrom.Equal(now.Add(-15*time.Minute)) {
		t.Fatalf("kanca penceresi %v..%v", hFrom, hTo)
	}
	// 1 kaynak satırı → 2 trace satırı (ağırlık 1) + 1 artan satır (ağırlık 1).
	if len(hooked) != 3 || len(sink.calls) != 1 || len(sink.calls[0]) != 3 {
		t.Fatalf("patlatma: kanca %d, sink %v", len(hooked), sink.calls)
	}
	var traces, rest int
	for _, r := range hooked {
		if r.SourceID != src.ID || !r.Time.Equal(time.Unix(int64(slice), 0).UTC()) || r.OperationCode != "COMPLIANCE_ACTIMIZE" ||
			r.ErrorCode != "FSR0002" || r.ChannelCode != "059912" || r.HostName != "GATEWAY-01" || r.ErrorType != "TFAIL" {
			t.Fatalf("satır alanları: %+v", r)
		}
		if r.TraceID != "" {
			traces++
			if r.Weight != 1 {
				t.Fatalf("trace satırı ağırlık 1: %+v", r)
			}
		} else {
			rest++
			if r.Weight != 1 {
				t.Fatalf("artan satır ağırlık 3−2=1: %+v", r)
			}
		}
	}
	if traces != 2 || rest != 1 {
		t.Fatalf("trace=%d artan=%d", traces, rest)
	}
	st := w.Status()[0]
	if st.LastRows != 1 || st.LastMapped != 3 || st.LastError != "" || st.Capped {
		t.Fatalf("durum: %+v", st)
	}
}
