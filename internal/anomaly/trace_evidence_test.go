package anomaly

// trace_evidence_test.go — v0.10.1103 (operatör: "Oracle hata grubu için varsa
// coremetry üzerindeki trace ve o trace loglarını da kullanabilsin"). Span
// yolunun örnek trace + log gövdesi ortak yardımcılara (traceEvidence /
// traceLogsEvidence) çıkarıldı; bu dosya çıktının BAYT BAYT eski olduğunu
// pinler: legacySampleTraceEvidence, v0.10.1102'deki satır içi kodun
// (BuildExceptionExplainInput) okuma dışı birebir kopyasıdır ve aynı sahte
// okuyucularla yeni sampleTraceEvidence'la karşılaştırılır. Ayrıca sınırlar
// (≤60 span, ≤20 hata span'i garantili, ≤5 kanıt span'i, ≤3 SQL, ≤30 log
// çekilir / 12 satır) ve "yüklenmeyen trace için log sorgusu yok" kuralı.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/stackparse"
)

// fakeSpanLoader — GetTrace sahtesi; çağrıları sayar.
type fakeSpanLoader struct {
	spans map[string][]chstore.SpanRow
	err   error
	calls []string
}

func (f *fakeSpanLoader) GetTrace(_ context.Context, id string) ([]chstore.SpanRow, error) {
	f.calls = append(f.calls, id)
	if f.err != nil {
		return nil, f.err
	}
	return f.spans[id], nil
}

// fakeTraceLogs — logstore.Store'un yalnız Search'ü (LogsForTrace yolu).
// mk her çağrıda TAZE sayfa üretir (yardımcı sayfayı yerinde sıralar).
type fakeTraceLogs struct {
	logstore.Store
	mk    func() *logstore.Page
	err   error
	calls int
	got   logstore.Filter
}

func (f *fakeTraceLogs) Search(_ context.Context, fl logstore.Filter) (*logstore.Page, error) {
	f.calls++
	f.got = fl
	if f.err != nil {
		return nil, f.err
	}
	if f.mk == nil {
		return &logstore.Page{}, nil
	}
	return f.mk(), nil
}

const teTrace = "4bf92f3577b34da6a3ce929d0e0e4736"

var teBase = time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC).UnixNano()

// teSpans — n span; errEvery'de bir hata span'i, bazılarında SQL (tekrarlı).
func teSpans(n, errEvery int) []chstore.SpanRow {
	out := make([]chstore.SpanRow, 0, n)
	for i := 0; i < n; i++ {
		sp := chstore.SpanRow{
			TraceID: teTrace, SpanID: fmt.Sprintf("%016x", i+1), Name: fmt.Sprintf("op-%d", i),
			ServiceName: "svc-orders", Kind: "SPAN_KIND_INTERNAL",
			StartTime: teBase + int64(i)*int64(time.Millisecond), EndTime: teBase + int64(i+3)*int64(time.Millisecond),
		}
		if i > 0 {
			sp.ParentSpanID = fmt.Sprintf("%016x", i)
		}
		if errEvery > 0 && i%errEvery == errEvery-1 {
			sp.StatusCode, sp.StatusMessage = "error", "ORA-00001: unique constraint violated"
			sp.DBSystem = "oracle"
			sp.DBStatement = fmt.Sprintf("INSERT INTO orders_%d VALUES (:1)", (i/errEvery)%4)
		}
		out = append(out, sp)
	}
	// Başlangıcı en erken olmayan ilk span (zarf min'i ilk span'den gelmemeli).
	out[0].StartTime += int64(time.Second)
	out[0].EndTime += int64(time.Second)
	return out
}

const teJavaStack = "java.sql.SQLIntegrityConstraintViolationException: ORA-00001: unique constraint violated\n" +
	"\tat oracle.jdbc.driver.T4CTTIoer11.processError(T4CTTIoer11.java:629)\n" +
	"\tat oracle.jdbc.driver.T4CPreparedStatement.executeForRows(T4CPreparedStatement.java:1067)\n" +
	"\tat com.example.orders.OrderRepository.save(OrderRepository.java:88)\n" +
	"\tat com.example.orders.OrderService.place(OrderService.java:41)\n" +
	"\tat com.example.orders.OrderController.post(OrderController.java:27)"

// tePage — karışık severity'li 16 log: öznitelikte stack, gövdede stack,
// tekrar eden stack, stack'siz satırlar.
func tePage() *logstore.Page {
	var lgs []*logstore.LogRecord
	for i := 0; i < 16; i++ {
		lg := &logstore.LogRecord{
			Severity: uint8(5 + (i*7)%9), SeverityText: "INFO", ServiceName: "svc-orders",
			Body: fmt.Sprintf("order %d processed", i), SpanID: fmt.Sprintf("%016x", i+1),
			ResourceAttributes: map[string]string{"service.version": fmt.Sprintf("1.%d.0", i%3)},
		}
		switch i % 5 {
		case 0:
			lg.SeverityText, lg.Severity = "ERROR", 17
			lg.Attributes = map[string]string{"exception.stacktrace": teJavaStack, "exception.type": "java.sql.SQLIntegrityConstraintViolationException"}
		case 1:
			lg.SeverityText, lg.Severity = "ERROR", 18
			lg.ServiceName = "svc-ledger"
			lg.Body = "save failed: " + teJavaStack
		case 2:
			lg.SeverityText = "WARN"
			lg.Body = strings.Repeat("ç", 700) // rune kırpması
		}
		lgs = append(lgs, lg)
	}
	return &logstore.Page{Logs: lgs, Total: len(lgs)}
}

// legacyOut — v0.10.1102 satır içi kodun ürettiği her şey.
type legacyOut struct {
	traceBlock, logsBlock               string
	evTraces, evSpans, dbStmts          []string
	traceSpans                          []chstore.SpanRow
	logStack, logStackSvc, logStackSpan string
	logStackRes                         map[string]string
	stackForPrompt, stackRaw, stackSvc  string
	stackSample                         int
}

// legacySampleTraceEvidence — v0.10.1102 BuildExceptionExplainInput'un örnek
// trace + log gövdesi, BİREBİR kopya (store.GetTrace → ld.GetTrace dışında
// tek karakter değişmedi). Değiştirme: bu, yeni kodun karşılaştırıldığı
// donmuş referanstır.
func legacySampleTraceEvidence(ctx context.Context, ld TraceSpanLoader, logs logstore.Store, traceID string, sampleStacks []string) legacyOut {
	type liteSpan struct {
		Name       string  `json:"name"`
		Service    string  `json:"service"`
		Kind       string  `json:"kind"`
		ParentSpan string  `json:"parent,omitempty"`
		SpanID     string  `json:"id"`
		DurationMs float64 `json:"durMs"`
		Status     string  `json:"status,omitempty"`
		StatusMsg  string  `json:"statusMsg,omitempty"`
		// v0.10.115 — yalnız hata span'larında: SQL hatasında çalışan ifade.
		DBSystem    string `json:"dbSystem,omitempty"`
		DBStatement string `json:"dbStatement,omitempty"`
	}
	var traceBlock, logsBlock string
	var dbStmts []string
	seenStmt := map[string]bool{}
	var logStack, logStackSvc string
	var logStackSpan string
	var logStackRes map[string]string
	var logLines []liteLog
	var logStacks []string
	var evTraces, evSpans []string
	var traceMinT, traceMaxT int64
	var traceSpans []chstore.SpanRow
	if traceID != "" {
		tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		spans, terr := ld.GetTrace(tctx, traceID)
		cancel()
		if terr == nil && len(spans) > 0 {
			evTraces = append(evTraces, traceID)
			traceSpans = spans
			traceMinT, traceMaxT = spans[0].StartTime, spans[0].EndTime
			for _, sp := range spans {
				if sp.StartTime < traceMinT {
					traceMinT = sp.StartTime
				}
				if sp.EndTime > traceMaxT {
					traceMaxT = sp.EndTime
				}
			}
			include := make([]bool, len(spans))
			kept, errKept := 0, 0
			for i, sp := range spans {
				if sp.StatusCode == "error" && errKept < 20 {
					include[i] = true
					kept++
					errKept++
				}
			}
			for i := range spans {
				if kept >= 60 {
					break
				}
				if !include[i] {
					include[i] = true
					kept++
				}
			}
			compact := make([]liteSpan, 0, kept)
			for i, sp := range spans {
				if !include[i] {
					continue
				}
				l := liteSpan{Name: sp.Name, Service: sp.ServiceName, Kind: sp.Kind,
					ParentSpan: sp.ParentSpanID, SpanID: sp.SpanID,
					DurationMs: float64(sp.EndTime-sp.StartTime) / 1e6}
				if sp.StatusCode == "error" {
					l.Status = "error"
					l.StatusMsg = sp.StatusMessage
					if len(evSpans) < 5 {
						evSpans = append(evSpans, sp.SpanID)
					}
					if sp.DBStatement != "" {
						l.DBSystem = sp.DBSystem
						l.DBStatement = truncRunes(sp.DBStatement, 600)
						if len(dbStmts) < 3 && !seenStmt[l.DBStatement] {
							seenStmt[l.DBStatement] = true
							dbStmts = append(dbStmts, l.DBStatement)
						}
					}
				}
				compact = append(compact, l)
			}
			if tp, e := json.Marshal(compact); e == nil {
				traceBlock = fmt.Sprintf("\n\nÖrnek hata TRACE'i (%s, %d span):\n```json\n%s\n```",
					traceID, len(compact), string(tp))
			}
		}
	}
	if logs != nil && traceID != "" && traceMaxT > 0 {
		from := time.Unix(0, traceMinT).Add(-time.Minute)
		to := time.Unix(0, traceMaxT).Add(time.Minute)
		lctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		if page, lerr := logstore.LogsForTrace(lctx, logs, traceID, from, to, 30); lerr == nil && page != nil && len(page.Logs) > 0 {
			lgs := page.Logs
			sort.SliceStable(lgs, func(i, j int) bool { return lgs[i].Severity > lgs[j].Severity })
			ll := make([]liteLog, 0, 12)
			for _, lg := range lgs {
				if len(ll) >= 12 {
					break
				}
				stackText, stackFromBody := stackparse.FromLog(lg.Attributes, lg.Body)
				if logStack == "" && stackText != "" {
					logStack, logStackSvc = stackText, lg.ServiceName
					logStackSpan, logStackRes = lg.SpanID, lg.ResourceAttributes
				}
				bodyForPrompt := lg.Body
				if stackFromBody {
					bodyForPrompt = stackparse.MessageHead(lg.Body)
				}
				e := liteLog{Sev: lg.SeverityText, Svc: lg.ServiceName, Body: truncRunes(bodyForPrompt, 500)}
				e.ExType = lg.Attributes["exception.type"]
				ll = append(ll, e)
				logStacks = append(logStacks, stackText)
			}
			logLines = ll
		}
		cancel()
	}
	stackForPrompt, stackRaw, stackSvc, stackSample := pickExceptionStack(sampleStacks, logStack, logStackSvc)
	if len(logLines) > 0 {
		folded := foldDuplicateLogStacks(logStacks, stackForPrompt)
		for i := range logLines {
			if i < len(folded) {
				logLines[i].Stack = folded[i]
			}
		}
		if lp, e := json.Marshal(logLines); e == nil {
			logsBlock = fmt.Sprintf("\n\nBu trace'in ilişkili LOGLARI (yüksek severity önce):\n```json\n%s\n```", string(lp))
		}
	}
	return legacyOut{traceBlock: traceBlock, logsBlock: logsBlock, evTraces: evTraces, evSpans: evSpans, dbStmts: dbStmts,
		traceSpans: traceSpans, logStack: logStack, logStackSvc: logStackSvc, logStackSpan: logStackSpan, logStackRes: logStackRes,
		stackForPrompt: stackForPrompt, stackRaw: stackRaw, stackSvc: stackSvc, stackSample: stackSample}
}

func TestSampleTraceEvidenceByteIdenticalToLegacy(t *testing.T) {
	sampleStack := strings.Replace(teJavaStack, "OrderRepository.save", "OrderRepository.persist", 1)
	cases := []struct {
		name         string
		traceID      string
		spans        []chstore.SpanRow
		spanErr      error
		logs         bool
		logErr       error
		page         func() *logstore.Page
		sampleStacks []string
		wantLogCalls int
	}{
		{"tam: span + log, örnek stack'li", teTrace, teSpans(90, 3), nil, true, nil, tePage, []string{"", sampleStack}, 1},
		{"tam: span + log, stack log-fallback'ten", teTrace, teSpans(90, 3), nil, true, nil, tePage, []string{"", ""}, 1},
		{"az span, hata yok", teTrace, teSpans(4, 0), nil, true, nil, tePage, nil, 1},
		{"log deposu yok (CH-only)", teTrace, teSpans(30, 2), nil, false, nil, tePage, nil, 0},
		{"log okuması düştü", teTrace, teSpans(30, 2), nil, true, errors.New("es down"), tePage, nil, 1},
		{"trace'te log yok", teTrace, teSpans(30, 2), nil, true, nil, func() *logstore.Page { return &logstore.Page{} }, nil, 1},
		{"trace yüklenmedi → log sorgusu YOK", teTrace, nil, errors.New("ch down"), true, nil, tePage, []string{sampleStack}, 0},
		{"trace boş döndü → log sorgusu YOK", teTrace, []chstore.SpanRow{}, nil, true, nil, tePage, nil, 0},
		{"örnekte trace yok", "", teSpans(10, 2), nil, true, nil, tePage, []string{sampleStack}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mkLoader := func() *fakeSpanLoader {
				return &fakeSpanLoader{spans: map[string][]chstore.SpanRow{teTrace: tc.spans}, err: tc.spanErr}
			}
			mkLogs := func() (logstore.Store, *fakeTraceLogs) {
				if !tc.logs {
					return nil, nil
				}
				f := &fakeTraceLogs{mk: tc.page, err: tc.logErr}
				return f, f
			}
			oldLd, newLd := mkLoader(), mkLoader()
			oldLogs, oldF := mkLogs()
			newLogs, newF := mkLogs()
			old := legacySampleTraceEvidence(context.Background(), oldLd, oldLogs, tc.traceID, tc.sampleStacks)
			got := sampleTraceEvidence(context.Background(), newLd, newLogs, tc.traceID, tc.sampleStacks)

			if got.TraceBlock != old.traceBlock {
				t.Fatalf("trace bloğu bayt bayt eski olmalı:\nyeni %q\neski %q", got.TraceBlock, old.traceBlock)
			}
			if got.LogsBlock != old.logsBlock {
				t.Fatalf("log bloğu bayt bayt eski olmalı:\nyeni %q\neski %q", got.LogsBlock, old.logsBlock)
			}
			// Fikstür boşsa pin anlamsız: tam vakalarda her blok dolu, katlama ve SQL var.
			if strings.HasPrefix(tc.name, "tam") && (got.TraceBlock == "" || !strings.Contains(got.LogsBlock, dupStackRef) ||
				len(got.Trace.DBStatements) != traceEvidenceDBStmts || got.StackRaw == "") {
				t.Fatalf("fikstür kanıtı eksik: trace=%d log=%d sql=%d stack=%d", len(got.TraceBlock), len(got.LogsBlock), len(got.Trace.DBStatements), len(got.StackRaw))
			}
			for _, p := range []struct {
				name      string
				got, want any
			}{
				{"evTraces", got.EvTraces, old.evTraces}, {"evSpans", got.Trace.EvSpans, old.evSpans},
				{"dbStmts", got.Trace.DBStatements, old.dbStmts}, {"traceSpans", got.Trace.Spans, old.traceSpans},
				{"logStack", got.Logs.Stack, old.logStack}, {"logStackSvc", got.Logs.StackService, old.logStackSvc},
				{"logStackSpan", got.Logs.StackSpan, old.logStackSpan}, {"logStackRes", got.Logs.StackRes, old.logStackRes},
				{"stackForPrompt", got.StackForPrompt, old.stackForPrompt}, {"stackRaw", got.StackRaw, old.stackRaw},
				{"stackSvc", got.StackService, old.stackSvc}, {"stackSample", got.StackSample, old.stackSample},
			} {
				if !reflect.DeepEqual(p.got, p.want) {
					t.Errorf("%s: yeni %#v, eski %#v", p.name, p.got, p.want)
				}
			}
			if !reflect.DeepEqual(oldLd.calls, newLd.calls) {
				t.Errorf("GetTrace çağrıları ayrıştı: yeni %v eski %v", newLd.calls, oldLd.calls)
			}
			if tc.logs {
				if oldF.calls != tc.wantLogCalls || newF.calls != tc.wantLogCalls {
					t.Fatalf("log sorgusu: yeni %d eski %d, beklenen %d", newF.calls, oldF.calls, tc.wantLogCalls)
				}
				if !reflect.DeepEqual(oldF.got, newF.got) {
					t.Errorf("log süzgeci ayrıştı: yeni %+v eski %+v", newF.got, oldF.got)
				}
			}
			// Tam prompt da (montaj dahil) aynı.
			g := &chstore.ExceptionGroup{Type: "java.sql.SQLIntegrityConstraintViolationException", Message: "ORA-00001", Service: "svc-orders",
				FirstSeen: teBase, LastSeen: teBase + int64(time.Hour)}
			if a, b := assembleExceptionPrompt(g, time.UTC, "", got.StackForPrompt, got.TraceBlock, got.LogsBlock, "", ""),
				assembleExceptionPrompt(g, time.UTC, "", old.stackForPrompt, old.traceBlock, old.logsBlock, "", ""); a != b {
				t.Fatal("montajlı prompt ayrıştı")
			}
		})
	}
}

// Sınırlar: ≤60 span, ≤20 hata span'i garantili (derindeki hatalar dahil),
// ≤5 kanıt span'i, ≤3 ayrık SQL; zarf tüm span'lerden.
func TestCompactTraceEvidenceBounds(t *testing.T) {
	spans := teSpans(200, 4) // 50 hata span'i, 4 ayrık SQL
	r := compactTraceEvidence(spans)
	errs := 0
	for _, l := range r.Compact {
		if l.Status == "error" {
			errs++
		}
	}
	if len(r.Compact) != traceEvidenceMaxSpans || errs != traceEvidenceErrSpans {
		t.Fatalf("kompakt: %d span, %d hata", len(r.Compact), errs)
	}
	if len(r.EvSpans) != traceEvidenceEvSpans || len(r.DBStatements) != traceEvidenceDBStmts {
		t.Fatalf("kanıt: %d span, %d SQL", len(r.EvSpans), len(r.DBStatements))
	}
	// span 0 kaydırılmış: min ilk span'den DEĞİL span 1'den, max span 0'dan.
	if r.MinT != spans[1].StartTime || r.MaxT != spans[0].EndTime {
		t.Fatalf("zarf: %d..%d", r.MinT, r.MaxT)
	}
	if z := compactTraceEvidence(nil); z.Loaded() {
		t.Fatal("boş span → yüklenmedi")
	}
	if z := traceEvidence(context.Background(), nil, teTrace); z.Loaded() {
		t.Fatal("nil yükleyici → yüklenmedi")
	}
}

// Log yardımcısı: ≤30 çekilir, ≤12 satır, severity azalan; pencere zarf ±1 dk.
func TestTraceLogsEvidenceBounds(t *testing.T) {
	f := &fakeTraceLogs{mk: tePage}
	minT, maxT := teBase, teBase+int64(5*time.Second)
	r := traceLogsEvidence(context.Background(), f, teTrace, minT, maxT)
	if f.calls != 1 || f.got.TraceID != teTrace || f.got.Limit != traceLogsFetch {
		t.Fatalf("sorgu: %d çağrı, %+v", f.calls, f.got)
	}
	if !f.got.From.Equal(time.Unix(0, minT).Add(-time.Minute)) || !f.got.To.Equal(time.Unix(0, maxT).Add(time.Minute)) {
		t.Fatalf("pencere: %s → %s", f.got.From, f.got.To)
	}
	if len(r.Lines) != traceLogsLines || len(r.Stacks) != traceLogsLines {
		t.Fatalf("satır: %d", len(r.Lines))
	}
	if r.Lines[0].Sev != "ERROR" || r.Stack == "" || r.StackService == "" {
		t.Fatalf("severity sırası / stack istihkakı: %+v", r.Lines[0])
	}
	js, ok := r.foldedJSON("")
	if !ok || strings.Count(js, dupStackRef) == 0 {
		t.Fatalf("tekrar eden stack katlanmalı: %s", js)
	}
	if r.Lines[0].Stack != "" {
		t.Fatal("foldedJSON girdi satırlarını değiştirmemeli")
	}
	// maxT 0 (trace yüklenmedi) / logs nil → sorgu YOK.
	f2 := &fakeTraceLogs{mk: tePage}
	if r := traceLogsEvidence(context.Background(), f2, teTrace, 0, 0); f2.calls != 0 || len(r.Lines) != 0 {
		t.Fatal("yüklenmeyen trace için log sorgusu yapılmamalı")
	}
	if r := traceLogsEvidence(context.Background(), nil, teTrace, minT, maxT); len(r.Lines) != 0 {
		t.Fatal("nil log deposu → boş")
	}
}
