package anomaly

// sample_trace_logs_test.go — v0.10.1370 regresyon testleri (operatör-
// bildirimli: "Teknik hatalar için Exceptions sayfasında trace incelemesi gibi
// loga bakmıyor — Ask CoSRE veya explain için"). Kök neden: exception
// açıklamasının log okuması trace'in ClickHouse'ta yüklenmesine bağlıydı
// (traceLogsEvidence maxT<=0 → sorgu yok; Oracle `ora:` satırlarının trace
// id'leri çoğu zaman Coremetry'de yok → log sorgusu HİÇ atılmıyordu). Pinler:
// referanslar → TEK çoklu-trace süzgeci (TraceIDs, damgalar ±2 dk, Limit 60);
// log deposu yok / referans yok / hepsi dışarıda → sorgu yok; trace başına ≤8
// satır, logu olmayan trace "log bulunamadı", deterministik sıra; okuma
// düşerse blok boş (prompt eskisi gibi); span ve Oracle referans kurucuları;
// Tempo-önce yükleyici (interaktif yol) CH hiçbir id'yi bulamadığında en yeni
// id'yi BİR kez dener.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	slA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	slB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	slC = "cccccccccccccccccccccccccccccccc"
	slD = "dddddddddddddddddddddddddddddddd"
)

var slT0 = time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)

func slRef(id string, offSec int, src string) sampleLogRef {
	return sampleLogRef{TraceID: id, AtNs: slT0.Add(time.Duration(offSec) * time.Second).UnixNano(), Source: src}
}

func TestSampleLogFilterFromRefs(t *testing.T) {
	cases := []struct {
		name     string
		refs     []sampleLogRef
		exclude  string
		wantIDs  []string
		wantOK   bool
		wantFrom time.Time
		wantTo   time.Time
	}{
		{"üç trace, pencere damgaların ±2 dk'sı",
			[]sampleLogRef{slRef(slA, 30, ""), slRef(slB, -90, ""), slRef(slC, 0, "")}, "",
			[]string{slA, slB, slC}, true, slT0.Add(-90*time.Second - 2*time.Minute), slT0.Add(30*time.Second + 2*time.Minute)},
		{"≤3 tavan, sıra korunur",
			[]sampleLogRef{slRef(slA, 0, ""), slRef(slB, 0, ""), slRef(slC, 0, ""), slRef(slD, 0, "")}, "",
			[]string{slA, slB, slC}, true, slT0.Add(-2 * time.Minute), slT0.Add(2 * time.Minute)},
		{"tekrar (büyük/küçük harf) ve boş id atılır",
			[]sampleLogRef{slRef(slA, 0, ""), slRef(strings.ToUpper(slA), 5, ""), slRef("  ", 0, ""), slRef(slB, 10, "")}, "",
			[]string{slA, slB}, true, slT0.Add(-2 * time.Minute), slT0.Add(10*time.Second + 2*time.Minute)},
		{"exclude (tek-trace bloğunda okunan) dışarıda, yerine sonraki girer",
			[]sampleLogRef{slRef(slA, 0, ""), slRef(slB, 0, ""), slRef(slC, 0, ""), slRef(slD, 0, "")}, strings.ToUpper(slA),
			[]string{slB, slC, slD}, true, slT0.Add(-2 * time.Minute), slT0.Add(2 * time.Minute)},
		{"damgasız referans pencereye girmez ama id sorulur",
			[]sampleLogRef{slRef(slA, 0, ""), {TraceID: slB}}, "",
			[]string{slA, slB}, true, slT0.Add(-2 * time.Minute), slT0.Add(2 * time.Minute)},
		{"hiç damga yok → sorgu yok (sınırsız pencere yok)", []sampleLogRef{{TraceID: slA}}, "", nil, false, time.Time{}, time.Time{}},
		{"referans yok", nil, "", nil, false, time.Time{}, time.Time{}},
		{"hepsi exclude", []sampleLogRef{slRef(slA, 0, "")}, slA, nil, false, time.Time{}, time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, ok := sampleLogFilter(pickSampleLogRefs(tc.refs, tc.exclude, sampleLogTraces))
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, beklenen %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if fmt.Sprint(f.TraceIDs) != fmt.Sprint(tc.wantIDs) || f.TraceID != "" || f.Limit != 60 {
				t.Fatalf("süzgeç: ids=%v traceID=%q limit=%d", f.TraceIDs, f.TraceID, f.Limit)
			}
			if !f.From.Equal(tc.wantFrom) || !f.To.Equal(tc.wantTo) {
				t.Fatalf("pencere %s → %s, beklenen %s → %s", f.From, f.To, tc.wantFrom, tc.wantTo)
			}
		})
	}
}

func TestSampleTraceLogsEvidenceNoQuery(t *testing.T) {
	refs := []sampleLogRef{slRef(slA, 0, sampleLogSrcCH)}
	if r := sampleTraceLogsEvidence(context.Background(), nil, refs, ""); r.Block != "" || r.Asked != 0 {
		t.Fatalf("log deposu yok → sıfır değer: %+v", r)
	}
	for _, c := range []struct {
		name    string
		refs    []sampleLogRef
		exclude string
	}{
		{"referans yok", nil, ""},
		{"hepsi exclude", refs, slA},
		{"damgasız", []sampleLogRef{{TraceID: slA}}, ""},
	} {
		f := &fakeTraceLogs{}
		if r := sampleTraceLogsEvidence(context.Background(), f, c.refs, c.exclude); f.calls != 0 || r.Block != "" {
			t.Errorf("%s: sorgu atılmamalı (calls=%d block=%q)", c.name, f.calls, r.Block)
		}
	}
	// Okuma düştü (yavaş ES) → blok boş: prompt eskisi gibi.
	f := &fakeTraceLogs{err: logstore.ErrBackendSlow}
	if r := sampleTraceLogsEvidence(context.Background(), f, refs, ""); f.calls != 1 || r.Block != "" || r.Lines != 0 {
		t.Fatalf("okuma düştü → tek deneme, boş blok: calls=%d %+v", f.calls, r)
	}
}

// slLogs — slA'ya 11 log (karışık şiddet, gövdede stack ve çit), slC'ye 1 log,
// slB'ye log yok; listede olmayan trace'in logu atılır.
func slLogs() []*logstore.LogRecord {
	var out []*logstore.LogRecord
	for i := 0; i < 11; i++ {
		lg := &logstore.LogRecord{TraceID: slA, Severity: 9, SeverityText: "INFO", ServiceName: "svc-caller",
			Timestamp: int64(100 - i), Body: fmt.Sprintf("adim %02d", i)}
		if i%5 == 0 {
			lg.Severity, lg.SeverityText = 17, "ERROR"
		}
		out = append(out, lg)
	}
	out[3].Body = "save failed: " + teJavaStack
	out[3].Severity, out[3].SeverityText = 21, "FATAL"
	out[3].Attributes = map[string]string{"exception.type": "java.sql.SQLIntegrityConstraintViolationException"}
	out = append(out,
		&logstore.LogRecord{TraceID: strings.ToUpper(slC), Severity: 13, SeverityText: "WARN", ServiceName: "svc-gw",
			Body: "gövde ```çit``` " + strings.Repeat("ş", 400)},
		&logstore.LogRecord{TraceID: slD, Severity: 21, Body: "başka trace"},
		nil)
	return out
}

func TestRenderSampleTraceLogs(t *testing.T) {
	refs := []sampleLogRef{slRef(slA, 0, sampleLogSrcNone), slRef(slB, 0, sampleLogSrcCH), slRef(slC, 0, sampleLogSrcTempo)}
	r := renderSampleTraceLogs(refs, slLogs())
	if r.Asked != 3 || r.WithLogs != 2 || r.Lines != sampleLogPerTrace+1 {
		t.Fatalf("sayaçlar: %+v", r)
	}
	for _, want := range []string{
		"\n\nÖrnek trace'lerin LOGLARI (trace id ile, 3 trace soruldu / 2 trace'te log bulundu; yüksek severity önce):",
		"\ntrace aaaaaaaaaaaa… (Coremetry'de yok, 8 satır):\n```json\n[{\"sev\":\"FATAL\",\"svc\":\"svc-caller\",\"exType\":\"java.sql.SQLIntegrityConstraintViolationException\",\"body\":\"save failed: java.sql.SQLIntegrityConstraintViolationException: ORA-00001: unique constraint violated\"},{\"sev\":\"ERROR\"",
		"\ntrace bbbbbbbbbbbb… (clickhouse): log bulunamadı",
		"\ntrace cccccccccccc… (tempo, 1 satır):",
		"ˋˋˋçitˋˋˋ",
	} {
		if !strings.Contains(r.Block, want) {
			t.Errorf("blok %q içermeli:\n%s", want, r.Block)
		}
	}
	for _, bad := range []string{"başka trace", "\\tat oracle.jdbc", "```çit"} {
		if strings.Contains(r.Block, bad) {
			t.Errorf("blok %q İÇERMEMELİ", bad)
		}
	}
	// Trace sırası referans sırası; ≤300 rune gövde.
	ia, ib, ic := strings.Index(r.Block, "trace aaaa"), strings.Index(r.Block, "trace bbbb"), strings.Index(r.Block, "trace cccc")
	if !(ia < ib && ib < ic) {
		t.Fatalf("trace sırası: %d %d %d", ia, ib, ic)
	}
	if strings.Contains(r.Block, strings.Repeat("ş", 300)) {
		t.Error("gövde 300 rune'da kırpılmalı")
	}
	// Deterministik: ters sıralı girdi aynı bloğu üretir.
	lgs := slLogs()
	for i, j := 0, len(lgs)-1; i < j; i, j = i+1, j-1 {
		lgs[i], lgs[j] = lgs[j], lgs[i]
	}
	if r2 := renderSampleTraceLogs(refs, lgs); r2.Block != r.Block {
		t.Fatalf("blok girdi sırasından bağımsız olmalı:\n%s\n---\n%s", r.Block, r2.Block)
	}
	// Hiç log yok → başlık + her trace için açık "log bulunamadı".
	z := renderSampleTraceLogs(refs[:1], nil)
	if z.WithLogs != 0 || !strings.Contains(z.Block, "1 trace soruldu / 0 trace'te log bulundu") ||
		!strings.Contains(z.Block, "trace aaaaaaaaaaaa… (Coremetry'de yok): log bulunamadı") {
		t.Fatalf("logsuz blok: %q", z.Block)
	}
}

func TestSampleTraceLogsEvidenceOneQuery(t *testing.T) {
	f := &fakeTraceLogs{mkF: func(logstore.Filter) *logstore.Page { return &logstore.Page{Logs: slLogs()} }}
	refs := []sampleLogRef{slRef(slD, 0, sampleLogSrcCH), slRef(slA, 0, sampleLogSrcCH), slRef(slB, 0, sampleLogSrcCH), slRef(slC, 0, sampleLogSrcCH)}
	r := sampleTraceLogsEvidence(context.Background(), f, refs, slD)
	if f.calls != 1 || fmt.Sprint(f.got.TraceIDs) != fmt.Sprint([]string{slA, slB, slC}) {
		t.Fatalf("TEK çoklu-trace sorgusu, exclude dışarıda: calls=%d ids=%v", f.calls, f.got.TraceIDs)
	}
	if r.Asked != 3 || r.WithLogs != 2 || strings.Contains(r.Block, "dddddddddddd") {
		t.Fatalf("sonuç: %+v", r)
	}
}

func TestSpanSampleLogRefs(t *testing.T) {
	samples := []chstore.ExceptionSample{
		{TraceID: slA, Time: 10}, {TraceID: ""}, {TraceID: slB, Time: 20}, {TraceID: slA, Time: 30},
	}
	got := spanSampleLogRefs(samples)
	if len(got) != 3 || got[0] != (sampleLogRef{slA, 10, sampleLogSrcCH}) || got[1] != (sampleLogRef{slB, 20, sampleLogSrcCH}) {
		t.Fatalf("span referansları: %+v", got)
	}
	// Tekrar pickSampleLogRefs'te düşer; ilk görülen (en yeni örnek) kalır.
	if p := pickSampleLogRefs(got, "", sampleLogTraces); len(p) != 2 || p[0].AtNs != 10 {
		t.Fatalf("seçim: %+v", p)
	}
}

func TestOracleLogRefs(t *testing.T) {
	rows := oraRows(8, 4) // satır i → id(i%4+1), en yeni önce
	got := oracleLogRefs(rows, 3)
	if len(got) != 3 {
		t.Fatalf("≤3: %+v", got)
	}
	for i, r := range got {
		if r.TraceID != fmt.Sprintf("%032x", i+1) || r.AtNs != rows[i].Time.UnixNano() || r.Source != "" {
			t.Errorf("ref %d: %+v", i, r)
		}
	}
	if z := oracleLogRefs(oraRowsNoTrace(3), 3); len(z) != 0 {
		t.Fatalf("trace id'siz satır → ref yok: %+v", z)
	}
	found := map[string]chstore.TraceFact{got[1].TraceID: {}}
	src := oracleSampleLogRefs(got, found, got[0].TraceID, sampleLogSrcTempo)
	if src[0].Source != sampleLogSrcTempo || src[1].Source != sampleLogSrcCH || src[2].Source != sampleLogSrcNone {
		t.Fatalf("kaynak etiketleri: %+v", src)
	}
}

// fakeProbeReader — v0.10.1370: interaktif yolun Tempo-önce sarmalayıcısının
// sahtesi (TraceSourceLoader); deneme çağrılarını kaydeder.
type fakeProbeReader struct {
	*fakeOracleReader
	probeSpans map[string][]chstore.SpanRow
	probeErr   error
	probes     []string
}

func (f *fakeProbeReader) GetTraceSource(_ context.Context, id string) ([]chstore.SpanRow, string, error) {
	f.probes = append(f.probes, id)
	if f.probeErr != nil {
		return nil, "", f.probeErr
	}
	return f.probeSpans[id], sampleLogSrcTempo, nil
}

func TestOracleTempoFirstProbe(t *testing.T) {
	t.Cleanup(func() { oracleFactsFn.Store(nil) })
	SetOracleExplainFacts(func(chstore.ExceptionGroup) (OracleExplainFacts, bool) { return oraFacts(), true })
	id := func(n int) string { return fmt.Sprintf("%032x", n) }
	spans := teSpans(12, 3)
	cases := []struct {
		name       string
		found      []string
		probeSpans map[string][]chstore.SpanRow
		probeErr   error
		wantProbes []string
		wantLoaded string
		wantMulti  []string
	}{
		{"CH hiçbirini bulmadı → en yeni id BİR kez Tempo-önce", []string{}, map[string][]chstore.SpanRow{id(1): spans}, nil,
			[]string{id(1)}, id(1), []string{id(2), id(3)}},
		{"Tempo'da da yok → 'yok' satırı korunur", []string{}, nil, nil, []string{id(1)}, "", []string{id(1), id(2), id(3)}},
		{"deneme düştü", []string{}, nil, errors.New("tempo down"), []string{id(1)}, "", []string{id(1), id(2), id(3)}},
		{"CH buldu → deneme YOK", []string{id(2)}, map[string][]chstore.SpanRow{id(1): spans}, nil, nil, id(2), []string{id(1), id(3)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rd := &fakeProbeReader{fakeOracleReader: &fakeOracleReader{rows: oraRows(6, 3), found: tc.found,
				spans: map[string][]chstore.SpanRow{id(2): spans}}, probeSpans: tc.probeSpans, probeErr: tc.probeErr}
			lf := &fakeTraceLogs{mkF: oraLogsByFilter}
			in := BuildOracleExceptionExplainInput(context.Background(), rd, lf, oraGroup(), time.UTC)
			if fmt.Sprint(rd.probes) != fmt.Sprint(tc.wantProbes) {
				t.Fatalf("deneme çağrıları %v, beklenen %v", rd.probes, tc.wantProbes)
			}
			if in.Oracle.CoremetryTrace != tc.wantLoaded {
				t.Fatalf("yüklenen trace %q, beklenen %q", in.Oracle.CoremetryTrace, tc.wantLoaded)
			}
			var multi []string
			single := 0
			for _, f := range lf.all {
				if len(f.TraceIDs) > 0 {
					multi = f.TraceIDs
				} else {
					single++
				}
			}
			if fmt.Sprint(multi) != fmt.Sprint(tc.wantMulti) || (tc.wantLoaded != "") != (single == 1) {
				t.Fatalf("log sorguları: tek=%d çoklu=%v, beklenen çoklu %v", single, multi, tc.wantMulti)
			}
			if tc.wantLoaded == "" && !strings.Contains(in.User, "Coremetry trace'i: yok (satırların trace id'leri Coremetry'de bulunamadı ya da okunamadı)") {
				t.Fatal("yüklenemeyen trace için 'yok' satırı kalmalı")
			}
			if tc.wantLoaded != "" && !strings.Contains(in.User, "Coremetry TRACE'i (Oracle satırının taşıdığı trace; "+tc.wantLoaded) {
				t.Fatal("yüklenen trace bloğu basılmalı")
			}
		})
	}
}

// Kaynak pini: span kurucusu örnek trace loglarını sampleTraceEvidence'ın
// DIŞINDA ekler (TestSampleTraceEvidenceByteIdenticalToLegacy yeşil kalır) ve
// LogsBlock User'daki bitişik blokla aynıdır.
func TestSpanBuilderAppendsSampleLogsOutsideLegacy(t *testing.T) {
	b, err := os.ReadFile("exception_context.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"sl := sampleTraceLogsEvidenceLim(ctx, logs, spanSampleLogRefs(samples), sampleLogsExclude, lim)", // v0.10.2384 — tavanlar parametreli
		"logsBlock := st.LogsBlock + sl.Block",
		"st.TraceBlock, logsBlock, deployBlock",
		"LogsBlock:    logsBlock,",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("exception_context.go %q bağlantısını kaybetti", want)
		}
	}
	body := src[strings.Index(src, "func sampleTraceEvidenceLim("):] // v0.10.2384 — gövde tavan parametreli kopyada
	body = body[:strings.Index(body, "\n}\n")]
	if strings.Contains(body, "sampleTraceLogsEvidence") {
		t.Error("örnek trace logları sampleTraceEvidence'a girmemeli (bayt pin)")
	}
}
