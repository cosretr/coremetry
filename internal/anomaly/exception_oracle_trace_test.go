package anomaly

// exception_oracle_trace_test.go — v0.10.1103 (operatör: "Oracle hata grubu
// için varsa coremetry üzerindeki trace ve o trace loglarını da
// kullanabilsin"). Pinler: çözülen trace'lerden (en yeni önce) Coremetry'de
// BULUNAN ilki TEK trace olarak yüklenir; loglar YALNIZ trace yüklendiyse ve
// log deposu varsa okunur; prompt trace bloğunu ve log bloğunu yalnız
// yüklendiklerinde taşır (yoksa açık "yok" satırı); EvSpans / LogsBlock /
// DBStatements kardeş yoldaki gibi dolar, Stack BOŞ kalır.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestOracleCoremetryTrace(t *testing.T) {
	refs := []OracleTraceRef{{TraceID: "t1"}, {TraceID: "t2"}, {TraceID: "t3"}}
	cases := []struct {
		name  string
		found map[string]chstore.TraceFact
		want  string
	}{
		{"hiçbiri bulunmadı", map[string]chstore.TraceFact{}, ""},
		{"nil harita (çözüm düştü)", nil, ""},
		{"en yeni bulunan kazanır", map[string]chstore.TraceFact{"t3": {}, "t2": {Service: "svc-orders"}}, "t2"},
		{"servissiz bulunan da sayılır", map[string]chstore.TraceFact{"t1": {}}, "t1"},
		{"listede olmayan id yok sayılır", map[string]chstore.TraceFact{"tX": {}}, ""},
	}
	for _, c := range cases {
		if got := oracleCoremetryTrace(refs, c.found); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
}

func TestBuildOracleExceptionExplainInputCoremetryTrace(t *testing.T) {
	t.Cleanup(func() { oracleFactsFn.Store(nil) })
	SetOracleExplainFacts(func(chstore.ExceptionGroup) (OracleExplainFacts, bool) { return oraFacts(), true })
	g := oraGroup()
	id := func(n int) string { return fmt.Sprintf("%032x", n) } // oraRows: satır i → id(i%traces+1)
	spans := teSpans(12, 3)

	const traceHdr, logsHdr = "Coremetry TRACE'i (", "Bu trace'in ilişkili LOGLARI"
	cases := []struct {
		name       string
		rows       []chstore.OracleErrorRow
		found      []string // nil → yalnız ids[0] (eski sahte)
		spanErr    error
		logs       bool
		wantTrace  string // GetTrace'e giden tek id ("" = çağrı yok)
		wantLoaded bool
		wantLogQ   int
		wantIn     []string
		wantOut    []string
	}{
		{"trace Coremetry'de yok", oraRows(6, 3), []string{}, nil, true, "", false, 0,
			[]string{"Coremetry trace'i: yok (satırların trace id'leri Coremetry'de bulunamadı ya da okunamadı)"},
			[]string{traceHdr, logsHdr}},
		{"satırlar trace id taşımıyor", oraRowsNoTrace(4), nil, nil, true, "", false, 0,
			[]string{"Coremetry trace'i: yok (satırlar trace id taşımıyor)"},
			[]string{traceHdr, logsHdr, "Çözülen trace'ler"}},
		{"en yeni bulunan yüklenir, log deposu yok", oraRows(6, 3), []string{id(3), id(2)}, nil, false, id(2), true, 0,
			[]string{traceHdr, "trace; " + id(2) + ", servis svc-orders, 12 span", "Bu trace'in logları: bulunamadı", "çağıran servisi ve operasyonu"},
			[]string{logsHdr, "Coremetry trace'i: yok"}},
		{"trace + loglar", oraRows(6, 3), []string{id(1)}, nil, true, id(1), true, 1,
			[]string{traceHdr, "trace; " + id(1), logsHdr, "ORA-00001", "svc-ledger"},
			[]string{"Bu trace'in logları: bulunamadı", "Coremetry trace'i: yok"}},
		{"trace okunamadı → log sorgusu YOK", oraRows(6, 3), []string{id(1)}, errors.New("ch down"), true, id(1), false, 0,
			[]string{"Coremetry trace'i: yok (satırların trace id'leri Coremetry'de bulunamadı ya da okunamadı)"},
			[]string{traceHdr, logsHdr}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rd := &fakeOracleReader{rows: tc.rows, found: tc.found, getTraceErr: tc.spanErr,
				spans: map[string][]chstore.SpanRow{id(1): spans, id(2): spans, id(3): spans}}
			var logs logstore.Store
			var lf *fakeTraceLogs
			if tc.logs {
				lf = &fakeTraceLogs{mk: tePage}
				logs = lf
			}
			in := BuildOracleExceptionExplainInput(context.Background(), rd, logs, g, time.UTC)

			if tc.wantTrace == "" && len(rd.gotTraces) != 0 || tc.wantTrace != "" && (len(rd.gotTraces) != 1 || rd.gotTraces[0] != tc.wantTrace) {
				t.Fatalf("GetTrace TEK bulunan id'ye gitmeli: %v, beklenen %q", rd.gotTraces, tc.wantTrace)
			}
			if lf != nil && lf.calls != tc.wantLogQ {
				t.Fatalf("log sorgusu %d, beklenen %d", lf.calls, tc.wantLogQ)
			}
			if lf != nil && tc.wantLogQ > 0 && (lf.got.TraceID != tc.wantTrace || lf.got.Limit != traceLogsFetch) {
				t.Fatalf("log süzgeci: %+v", lf.got)
			}
			for _, w := range tc.wantIn {
				if !strings.Contains(in.User, w) {
					t.Errorf("prompt %q içermeli:\n%s", w, in.User)
				}
			}
			for _, w := range tc.wantOut {
				if strings.Contains(in.User, w) {
					t.Errorf("prompt %q İÇERMEMELİ:\n%s", w, in.User)
				}
			}
			if in.Stack != "" || in.Oracle == nil {
				t.Fatalf("Oracle girdisi stack'siz kalmalı: stack=%q", in.Stack)
			}
			if tc.wantLoaded {
				if in.Oracle.CoremetryTrace != tc.wantTrace || in.TraceID != tc.wantTrace || in.Oracle.TraceSpans != 12 ||
					len(in.EvSpans) != 4 || len(in.DBStatements) == 0 {
					t.Fatalf("yüklenen trace alanları: ctx=%q trace=%q spans=%d ev=%v sql=%v",
						in.Oracle.CoremetryTrace, in.TraceID, in.Oracle.TraceSpans, in.EvSpans, in.DBStatements)
				}
				// Trace bloğu çözülen trace'lerin JSON'undan SONRA, örnek satırlardan ÖNCE.
				if i, j, k := strings.Index(in.User, "Çözülen trace'ler"), strings.Index(in.User, traceHdr), strings.Index(in.User, "En yeni "); !(i < j && j < k) {
					t.Fatalf("blok sırası: çözülen=%d trace=%d örnek=%d", i, j, k)
				}
			} else if in.Oracle.CoremetryTrace != "" || len(in.EvSpans) != 0 || len(in.DBStatements) != 0 {
				t.Fatalf("yüklenmeyen trace kanıt bırakmamalı: %+v", in)
			}
			if tc.wantLogQ > 0 {
				if in.LogsBlock == "" || !strings.Contains(in.User, in.LogsBlock) || in.Oracle.LogLines != traceLogsLines {
					t.Fatalf("LogsBlock User'ın içindeki blok olmalı (%d satır)", in.Oracle.LogLines)
				}
			} else if in.LogsBlock != "" {
				t.Fatalf("log bloğu yalnız loglar okunduysa: %q", in.LogsBlock)
			}
		})
	}
}

// oraRowsNoTrace — trace id'siz Oracle satırları.
func oraRowsNoTrace(n int) []chstore.OracleErrorRow {
	rows := oraRows(n, 1)
	for i := range rows {
		rows[i].TraceID = ""
	}
	return rows
}
