package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/appschema"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/tempo"
)

// trace_investigate_code_test.go — v0.10.1034 (operatör-bildirimli, prod:
// "Kod inceleme çalışma mantığı ile direkt Ask CoSRE farklı."). "Kodu da
// incele" artık Ask CoSRE incelemesinin üstüne kod ekler; klasik toplayıcı
// yalnız Tempo yedeği.
//
// Pinlenenler: yol seçimi (kodlu + CH → inceleme + kod; kodlu + Tempo yedeği →
// klasik + kod; kodsuz → inceleme, akar); kodlu prompt'un sırası (inceleme user
// bloğu → kod → şema) ve kodlu inceleme istemi; stack + servis incelemenin AYNI
// log okumasından (ikinci log okuması yok) ve klasik toplayıcının seçimine eşit;
// taşma zinciri (tam → yarım; yarıya inemezse → kodsuz, düz inceleme istemi) +
// kuyruk; önbellek (kodlu/kodsuz ayrı anahtar; isabette okuma da kod çekimi de
// yok, künye yan kayıttan); seçili span'in odağı kodlu istekte de geçerli.
// Adlar sentetik: checkout / payments, com.example.*.

// codeTestStackPay — payments'ın ERROR logundaki stack: uygulama kareleri
// aracın 200 runelik öznitelik kesiminin ÖTESİNDE (araç çıktısından kod
// çekilemez; ham kayıt dikişi şart). SQLCODE şema sinyalini taşır.
var codeTestStackPay = strings.Join([]string{
	"org.springframework.dao.DataIntegrityViolationException: PreparedStatementCallback; SQL []; DB2 SQL Error: SQLCODE=-302, SQLSTATE=22001, SQLERRMC=null",
	"\tat org.springframework.jdbc.support.SQLStateSQLExceptionTranslator.doTranslate(SQLStateSQLExceptionTranslator.java:104)",
	"\tat org.springframework.jdbc.core.JdbcTemplate.translateException(JdbcTemplate.java:1575)",
	"\tat deployment.payments.war//com.example.payments.ChargeRepository.savePhone(ChargeRepository.java:246)",
	"\tat deployment.payments.war//com.example.payments.ChargeHandler.charge(ChargeHandler.java:58)",
}, "\n")

// codeTestStackCart — checkout'un WARN logunun GÖVDESİNDEKİ stack (logback deseni).
var codeTestStackCart = strings.Join([]string{
	"Sepet kaydı başarısız",
	"java.lang.IllegalStateException: sepet kilitli",
	"\tat deployment.checkout.war//com.example.checkout.CartStore.save(CartStore.java:212)",
	"\tat deployment.checkout.war//com.example.checkout.CartService.persist(CartService.java:77)",
}, "\n")

const (
	codeTestSQL    = "INSERT INTO SHOP.ORDER_PHONE (CUSTOMER_NO, PHONE) VALUES (?, ?)"
	codeTestStatus = "DB2 SQL Error: SQLCODE=-302, SQLSTATE=22001"
)

// codeTestLogs — trace'in ham log kayıtları (iki yolun da okuduğu AYNI küme).
func codeTestLogs(t0 time.Time) []*logstore.LogRecord {
	return []*logstore.LogRecord{
		{Timestamp: t0.UnixNano(), Severity: 9, SeverityText: "INFO", ServiceName: "checkout", TraceID: invTestTrace, SpanID: invTestRoot, Body: "cart loaded"},
		{Timestamp: t0.Add(time.Second).UnixNano(), Severity: 13, SeverityText: "WARN", ServiceName: "checkout", TraceID: invTestTrace, SpanID: invTestRoot, Body: codeTestStackCart},
		{Timestamp: t0.Add(2 * time.Second).UnixNano(), Severity: 17, SeverityText: "ERROR", ServiceName: "payments", TraceID: invTestTrace, SpanID: invTestPaySpan,
			Body:       "ödeme kaydı başarısız",
			Attributes: map[string]string{"exception.type": "org.springframework.dao.DataIntegrityViolationException", "exception.stacktrace": codeTestStackPay}},
	}
}

// codeTestLogStore — sahte log deposu: span süzgeci (LogsForSpan) + okuma sayacı.
// Kayıtlar KOPYA döner (klasik toplayıcı sayfayı yerinde sıralar).
type codeTestLogStore struct {
	logstore.Store
	mu       sync.Mutex
	recs     []*logstore.LogRecord
	searches int
	// failTraceWide — span süzgeçsiz (trace geneli) okuma backend'e ulaşamaz.
	failTraceWide bool
}

func (s *codeTestLogStore) Search(_ context.Context, f logstore.Filter) (*logstore.Page, error) {
	s.mu.Lock()
	s.searches++
	fail := s.failTraceWide && f.SpanID == ""
	s.mu.Unlock()
	if fail {
		return nil, dialRefused()
	}
	var out []*logstore.LogRecord
	for _, r := range s.recs {
		if f.SpanID != "" && r.SpanID != f.SpanID {
			continue
		}
		cp := *r
		out = append(out, &cp)
	}
	return &logstore.Page{Logs: out, Total: len(out)}, nil
}

func (s *codeTestLogStore) Backend() string { return "elasticsearch" }

func (s *codeTestLogStore) searchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.searches
}

// codeTestTraceJSON — get_trace zarfı (invTraceJSON) + span listesinde durum ve
// SQL alanları (chstore.SpanRow'un taşıdığı gibi).
func codeTestTraceJSON(t *testing.T, t0 time.Time) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(invTraceJSON(t0)), &m); err != nil {
		t.Fatal(err)
	}
	spans := m["spans"].([]any)
	pay := spans[1].(map[string]any)
	pay["statusCode"], pay["statusMessage"], pay["dbStatement"], pay["dbSystem"] = "error", codeTestStatus, codeTestSQL, "db2"
	spans[0].(map[string]any)["statusCode"] = "ok"
	an := m["analysis"].(map[string]any)
	an["error_spans"].([]any)[0].(map[string]any)["status_message"] = codeTestStatus
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// codeTestRunner — sahte koşucu, ama L okuması GERÇEK get_logs_for_trace
// işleyicisinden (gerçek Executor): ham-kayıt dikişi uçtan uca sınanır.
func codeTestRunner(t *testing.T, f *fakeInvRunner, ls logstore.Store) invToolRunner {
	t.Helper()
	var h agenttools.Handler
	for _, tl := range mcptools.ToolList(mcptools.Deps{LogStore: ls}) {
		if tl.Name == invToolLogs {
			h = tl.Handler
		}
	}
	if h == nil {
		t.Fatal("get_logs_for_trace ToolList'te yok")
	}
	return func(ctx context.Context, name string, args json.RawMessage, budget time.Duration) agenttools.Outcome {
		if name != invToolLogs {
			return f.run(ctx, name, args, budget)
		}
		var m map[string]any
		_ = json.Unmarshal(args, &m)
		f.mu.Lock()
		f.calls = append(f.calls, name)
		f.args[name] = m
		f.mu.Unlock()
		return agenttools.NewExecutor(map[string]agenttools.Handler{name: h}, nil, agenttools.Hooks{}).WithBudget(budget).Call(ctx, name, args)
	}
}

// codeTestTFS — sahte DevOps/TFS: depo ağacı + dosya içeriği; istekleri ve
// istenen depoları sayar (isabette "kod çekimi yok" iddiası buradan).
type codeTestTFS struct {
	mu    sync.Mutex
	paths []string
	fail  bool
	srv   *httptest.Server
}

var codeTestFiles = map[string]int{ // yol → işaretli satır
	"/src/main/java/com/example/payments/ChargeRepository.java": 246,
	"/src/main/java/com/example/payments/ChargeHandler.java":    58,
	"/src/main/java/com/example/checkout/CartStore.java":        212,
	"/src/main/java/com/example/checkout/CartService.java":      77,
}

func newCodeTestTFS(t *testing.T) (*devops.Service, *codeTestTFS) {
	t.Helper()
	f := &codeTestTFS{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		fail := f.fail
		f.mu.Unlock()
		if fail {
			http.Error(w, "upstream boom", http.StatusInternalServerError)
			return
		}
		p, q := r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(p, "/refs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []map[string]string{{"name": "refs/heads/master"}}})
		case strings.HasSuffix(p, "/items") && q.Get("recursionLevel") == "Full":
			var items []map[string]any
			for path := range codeTestFiles {
				items = append(items, map[string]any{"path": path, "gitObjectType": "blob"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": items})
		case strings.HasSuffix(p, "/items"):
			mark, ok := codeTestFiles[q.Get("path")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			var sb strings.Builder
			for i := 1; i <= 300; i++ {
				if i == mark {
					fmt.Fprintf(&sb, "        repo.save(phoneWithCountryCode); // HATA_SATIRI_%d\n", i)
					continue
				}
				fmt.Fprintf(&sb, "        // satır %d\n", i)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": sb.String()})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"defaultBranch": "refs/heads/master"})
		}
	}))
	t.Cleanup(f.srv.Close)
	dv := devops.New()
	dv.Configure(devops.Settings{BaseURL: f.srv.URL, Collection: "DefaultCollection", Project: "Shop", PAT: "test-pat", Flavor: devops.FlavorServer})
	return dv, f
}

func (f *codeTestTFS) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.paths)
}

// repos — istenen depo adları (…/repositories/<depo>/…).
func (f *codeTestTFS) repos() map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for _, p := range f.paths {
		if _, rest, ok := strings.Cut(p, "/repositories/"); ok {
			name, _, _ := strings.Cut(rest, "/")
			out[name] = true
		}
	}
	return out
}

func codeTestSchema(t *testing.T) *appschema.Service {
	t.Helper()
	cat, err := appschema.ParseCSV(strings.NewReader(
		"TABSCHEMA,TABNAME,COLNAME,TYPENAME,LENGTH,SCALE,NULLS\n" +
			"SHOP,ORDER_PHONE,CUSTOMER_NO,DECIMAL,15,0,N\n" +
			"SHOP,ORDER_PHONE,PHONE,VARCHAR,10,0,N\n"))
	if err != nil {
		t.Fatal(err)
	}
	sch := appschema.NewService()
	sch.Set(cat)
	return sch
}

// codeTestTempo — Tempo'da aynı iki span (klasik yolun kaynağı): durum + SQL aynı.
func codeTestTempo(t *testing.T, t0 time.Time) *tempo.Service {
	t.Helper()
	ns := t0.UnixNano()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/traces/"+invTestTrace) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"batches":[
{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},
 "scopeSpans":[{"spans":[{"traceId":%q,"spanId":%q,"name":"GET /cart","kind":2,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","status":{"code":1}}]}]},
{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"payments"}}]},
 "scopeSpans":[{"spans":[{"traceId":%q,"spanId":%q,"parentSpanId":%q,"name":"POST /charge","kind":2,"startTimeUnixNano":"%d","endTimeUnixNano":"%d",
   "status":{"code":2,"message":%q},"attributes":[{"key":"db.statement","value":{"stringValue":%q}}]}]}]}]}`,
			invTestTrace, invTestRoot, ns, ns+1_234_500_000,
			invTestTrace, invTestPaySpan, invTestRoot, ns+100_000_000, ns+1_000_100_000, codeTestStatus, codeTestSQL)
	}))
	t.Cleanup(srv.Close)
	tp := tempo.New()
	tp.Configure(tempo.Settings{Enabled: true, BaseURL: srv.URL})
	return tp
}

// codeTestEnv — kodlu yolun tam düzeneği: sahte LLM + sahte koşucu (L gerçek) +
// sahte log deposu + sahte TFS + şema kataloğu.
type codeTestEnv struct {
	s   *Server
	p   *invCaptureProvider
	f   *fakeInvRunner
	ls  *codeTestLogStore
	tfs *codeTestTFS
}

const codeTestAnswer = "**İşlem Akışı ve Veri Özeti**\n- payments ödeme kaydında hata verdi.\n**Kök Neden ve Sonraki Adım**\n- telefon kolonu kısa görünüyor; ChargeRepository.savePhone kontrol edilmeli.\n"

func newCodeTestEnv(t *testing.T) *codeTestEnv {
	t.Helper()
	e := &codeTestEnv{p: newInvCaptureProvider(t, codeTestAnswer), f: newFakeInvRunner(invTestT0), ls: &codeTestLogStore{recs: codeTestLogs(invTestT0)}}
	e.f.out[invToolTrace] = invOK(codeTestTraceJSON(t, invTestT0))
	dv, tfs := newCodeTestTFS(t)
	e.tfs = tfs
	run := codeTestRunner(t, e.f, e.ls)
	cop := copilot.New(copilot.ProviderOpenAI, "test-key", "gemma4")
	cop.Configure(copilot.ProviderOpenAI, "test-key", "gemma4", e.p.srv.URL, false, true)
	prev := newTraceInvestigationRunner
	newTraceInvestigationRunner = func(*Server, *http.Request) invToolRunner { return run }
	t.Cleanup(func() { newTraceInvestigationRunner = prev })
	e.s = &Server{copilot: cop, cache: newMemCache(), logs: e.ls, devops: dv, schema: codeTestSchema(t)}
	return e
}

func codeTestReq(query string, includeCode bool) *http.Request {
	var body io.Reader
	if includeCode {
		body = strings.NewReader(`{"includeCode":true}`)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/copilot/explain-trace/"+invTestTrace+query, body)
	r.SetPathValue("id", invTestTrace)
	return r
}

// call — istek + SSE çerçeveleri; answer ve olay sayıları.
type codeTestResult struct {
	ans           map[string]any
	steps, deltas int
	frames        []sseFrame
}

func (e *codeTestEnv) call(t *testing.T, query string, includeCode bool) codeTestResult {
	t.Helper()
	w := httptest.NewRecorder()
	e.s.copilotExplainTrace(w, codeTestReq(query, includeCode))
	var res codeTestResult
	res.frames = parseSSE(t, w.Body.String())
	for _, fr := range res.frames {
		switch fr.event {
		case "step", "step-result":
			res.steps++
		case "delta":
			res.deltas++
		case "answer":
			res.ans = fr.data
		}
	}
	if res.ans == nil {
		t.Fatalf("answer çerçevesi yok (%d): %s", w.Code, w.Body.String())
	}
	return res
}

// messagesAt — i. sağlayıcı isteğinin system ve user mesajı.
func (p *invCaptureProvider) messagesAt(i int) (system, user string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	msgs, _ := p.bodies[i]["messages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		c, _ := mm["content"].(string)
		switch mm["role"] {
		case "system":
			system = c
		case "user":
			user = c
		}
	}
	return system, user
}

func codeFiles(ans map[string]any) []string {
	code, _ := ans["code"].(map[string]any)
	files, _ := code["files"].([]any)
	var out []string
	for _, f := range files {
		fm, _ := f.(map[string]any)
		out = append(out, fmt.Sprintf("%v:%v", fm["path"], fm["line"]))
	}
	return out
}

// ── yol seçimi ─────────────────────────────────────────────────────────────

func TestTraceCodeExplainPathSelection(t *testing.T) {
	cases := []struct {
		name        string
		includeCode bool
		tempoOnly   bool
		wantSystem  func() string
		wantUser    []string // sırayla geçmeli
		notUser     []string
		wantSteps   int
		wantDeltas  bool
		wantSources bool
		wantCode    bool
		wantTail    bool
		wantLogRead int // log deposuna yapılan okuma (ikinci okuma yok)
	}{
		{
			name: "kodsuz → inceleme (akar, kod yok)", includeCode: false,
			wantSystem: copilot.SystemPromptTraceInvestigation,
			wantUser:   []string{`"CoSRE'ye sor" — trace ` + invTestTrace, "## [T]", "## [L]", "## [D]"},
			notUser:    []string{"KOD BAĞLAMI", "ŞEMA BAĞLAMI"},
			wantSteps:  10, wantDeltas: true, wantSources: true, wantTail: true, wantLogRead: 1,
		},
		{
			name: "kodlu + trace CH'de → inceleme + kod (buffered)", includeCode: true,
			wantSystem: copilot.SystemPromptTraceInvestigationWithCode,
			wantUser:   []string{`"CoSRE'ye sor" — trace ` + invTestTrace, "## [T]", "## [L]", "## [K]", "## [P]", "## [D]", "Kanıt kimlikleri ([T1]", "KOD BAĞLAMI (depo", "HATA_SATIRI_246", "ŞEMA BAĞLAMI", "ORDER_PHONE.PHONE VARCHAR(10)"},
			notUser:    []string{"KOD KAYNAĞI"}, // span seçili değil: inceleme zaten trace geneli okudu
			// 5 okuma × (step + step-result) + kod çekimi adımı (source_code step + step-result)
			wantSteps: 12, wantDeltas: false, wantSources: true, wantCode: true, wantTail: true, wantLogRead: 1,
		},
		{
			name: "kodlu + trace yalnız Tempo'da → klasik + kod (yedek)", includeCode: true, tempoOnly: true,
			wantSystem: copilot.SystemPromptTraceWithCode,
			wantUser:   []string{"Trace " + invTestTrace + " with 2 spans", "ilişkili LOGLARI", "KOD BAĞLAMI (depo", "HATA_SATIRI_246", "ŞEMA BAĞLAMI"},
			notUser:    []string{"## [T]", "CoSRE'ye sor"},
			wantSteps:  0, wantDeltas: false, wantSources: false, wantCode: true, wantTail: false, wantLogRead: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newCodeTestEnv(t)
			if tc.tempoOnly {
				e.f.out[invToolTrace] = invOK(`{"source":{"source":"traces","backend":"clickhouse","state":"empty","returned":0},"trace_id":"x","spans":[],"span_count":0,"total_span_count":0,"truncated":false}`)
				e.s.tempo = codeTestTempo(t, invTestT0)
			}
			res := e.call(t, "?stream=1", tc.includeCode)
			if e.p.requests() != 1 {
				t.Fatalf("LLM çağrısı = %d; 1", e.p.requests())
			}
			system, user := e.p.messagesAt(0)
			if system != tc.wantSystem() {
				t.Errorf("sistem istemi beklenen değil (%d rune)", runeLen(system))
			}
			at := -1
			for _, w := range tc.wantUser {
				i := strings.Index(user, w)
				if i < 0 {
					t.Errorf("user bloğunda %q yok", w)
					continue
				}
				if i < at {
					t.Errorf("%q sırası bozuk (kod > şema, inceleme önce)", w)
				}
				at = i
			}
			for _, no := range tc.notUser {
				if strings.Contains(user, no) {
					t.Errorf("user bloğunda %q olmamalı", no)
				}
			}
			if res.steps != tc.wantSteps {
				t.Errorf("adım olayı = %d; %d", res.steps, tc.wantSteps)
			}
			if (res.deltas > 0) != tc.wantDeltas {
				t.Errorf("delta = %d; akış beklentisi %v (kodlu yol buffered)", res.deltas, tc.wantDeltas)
			}
			if _, ok := res.ans["sources"]; ok != tc.wantSources {
				t.Errorf("sources var=%v; beklenen %v", ok, tc.wantSources)
			}
			if files := codeFiles(res.ans); (len(files) > 0) != tc.wantCode {
				t.Errorf("code künyesi = %v (code=%v); beklenen kod=%v", files, res.ans["code"], tc.wantCode)
			}
			text, _ := res.ans["text"].(string)
			if !strings.HasPrefix(text, strings.TrimSpace(codeTestAnswer)) {
				t.Errorf("answer.text model metniyle başlamıyor: %q", text)
			}
			if strings.Contains(text, "**Kaynak durumu**") != tc.wantTail {
				t.Errorf("inceleme kuyruğu var=%v; beklenen %v", !tc.wantTail, tc.wantTail)
			}
			if n := e.ls.searchCount(); n != tc.wantLogRead {
				t.Errorf("log deposu %d kez okundu; %d (stack için ikinci okuma YOK)", n, tc.wantLogRead)
			}
			// Ölçüm (rapor): istek başına araç / git / LLM çağrısı ve prompt boyu.
			t.Logf("araç çağrısı=%d log okuması=%d git isteği=%d llm=%d · user %d rune, sistem %d rune",
				e.f.callCount(), e.ls.searchCount(), e.tfs.requests(), e.p.requests(), runeLen(user), runeLen(system))
		})
	}
}

// ── stack + servis: incelemenin okuması = klasik toplayıcının seçimi ───────

func TestTraceLogStackMatchesClassicCollector(t *testing.T) {
	e := newCodeTestEnv(t)
	inv, err := e.s.investigateTrace(withInvestigationCodeInputs(withInvestigationRunner(context.Background(), newTraceInvestigationRunner(e.s, nil))), invTestTrace, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	cls := &Server{tempo: codeTestTempo(t, invTestT0), logs: &codeTestLogStore{recs: codeTestLogs(invTestT0)}}
	in, err := cls.buildTraceExplainInput(context.Background(), invTestTrace)
	if err != nil {
		t.Fatal(err)
	}
	if in.Stack == "" || in.StackService != "payments" {
		t.Fatalf("klasik toplayıcı fikstürü: servis=%q stack=%d rune", in.StackService, runeLen(in.Stack))
	}
	if inv.Stack != in.Stack || inv.StackService != in.StackService {
		t.Errorf("inceleme stack/servis klasikten farklı: %q/%d rune vs %q/%d rune", inv.StackService, runeLen(inv.Stack), in.StackService, runeLen(in.Stack))
	}
	if inv.Stack != codeTestStackPay {
		t.Error("stack kesik geldi — ham kayıt dikişi devrede değil (araç çıktısı 200 runede keser)")
	}
	if errorText, sqls := inv.codeSchemaInputs(); strings.Join(sqls, "|") != strings.Join(in.DBStatements, "|") || errorText != in.ErrorText {
		t.Errorf("şema girdisi farklı\ninceleme SQL=%v hata=%q\nklasik   SQL=%v hata=%q", sqls, errorText, in.DBStatements, in.ErrorText)
	}
	// Aracın modele giden çıktısı kesik: uygulama karesi L bölümünde YOK ama
	// kod çekicisinin stack'inde VAR (aynı okuma, iki temsil).
	if strings.Contains(invLSection(t, inv.User), "ChargeRepository.savePhone") {
		t.Error("fikstür araç kesimini sınamıyor — uygulama karesi L bölümüne sızdı")
	}
}

func TestTraceLogStack(t *testing.T) {
	recs := codeTestLogs(invTestT0)
	orig := append([]*logstore.LogRecord(nil), recs...)
	stack, svc := traceLogStack(recs)
	if stack != codeTestStackPay || svc != "payments" {
		t.Errorf("en ciddi stack'li kayıt seçilmedi: %q / %q", svc, stack)
	}
	for i := range recs {
		if recs[i] != orig[i] {
			t.Fatal("traceLogStack girdi dilimini yeniden sıraladı (aracın kendi kayıtları)")
		}
	}
	// Gövdeden gelen stack + nil kayıt + stack'siz kayıt.
	stack, svc = traceLogStack([]*logstore.LogRecord{nil, recs[0], recs[1]})
	if svc != "checkout" || !strings.Contains(stack, "CartStore.save(CartStore.java:212)") {
		t.Errorf("gövde stack'i seçilmedi: %q / %q", svc, stack)
	}
	if s, v := traceLogStack([]*logstore.LogRecord{recs[0]}); s != "" || v != "" {
		t.Errorf("stack'siz kayıtta stack üretildi: %q / %q", v, s)
	}
}

// 15 satır paritesi: klasik yol yalnız severity sırasındaki ilk
// traceExplainLogRows kayda bakar; 16. sıradaki (ör. INFO'da yakalanmış)
// exception kod çekimini sürmez — iki yol da aynı kararı verir.
func TestTraceLogStackRowCapParity(t *testing.T) {
	var recs []*logstore.LogRecord
	for i := 0; i < traceExplainLogRows; i++ {
		recs = append(recs, &logstore.LogRecord{Timestamp: invTestT0.UnixNano() + int64(i), Severity: 17, SeverityText: "ERROR",
			ServiceName: "payments", TraceID: invTestTrace, Body: fmt.Sprintf("ödeme reddedildi #%d", i)})
	}
	handled := &logstore.LogRecord{Timestamp: invTestT0.UnixNano() + 99, Severity: 9, SeverityText: "INFO", ServiceName: "checkout",
		TraceID: invTestTrace, Body: codeTestStackCart}
	if s, _ := traceLogStack(append(append([]*logstore.LogRecord(nil), recs...), handled)); s != "" {
		t.Error("16. sıradaki (INFO) stack aday sayıldı — klasik tavan 15")
	}
	if s, v := traceLogStack(append([]*logstore.LogRecord{handled}, recs[:traceExplainLogRows-1]...)); v != "checkout" || s == "" {
		t.Errorf("15. sıradaki stack aday olmalı: %q", v)
	}
	cls := &Server{tempo: codeTestTempo(t, invTestT0), logs: &codeTestLogStore{recs: append(append([]*logstore.LogRecord(nil), recs...), handled)}}
	in, err := cls.buildTraceExplainInput(context.Background(), invTestTrace)
	if err != nil {
		t.Fatal(err)
	}
	if in.Stack != "" {
		t.Error("klasik toplayıcı 16. sıradaki stack'i seçti — parite fikstürü yanlış")
	}
}

func TestInvSpanErrorEvidence(t *testing.T) {
	long := strings.Repeat("x", 700)
	spans := []invTraceSpan{
		{StatusCode: "ok", StatusMessage: "yok sayılır", DBStatement: "SELECT 1"},
		{StatusCode: "error", StatusMessage: "m1", DBStatement: "INSERT INTO A (X) VALUES (?)"},
		{StatusCode: "error", StatusMessage: "m2", DBStatement: "INSERT INTO A (X) VALUES (?)"}, // tekrar SQL
		{StatusCode: "error", StatusMessage: "m3", DBStatement: long},
		{StatusCode: "error", StatusMessage: "m4", DBStatement: "UPDATE B SET Y = 1"},
		{StatusCode: "error", StatusMessage: "m5", DBStatement: "DELETE FROM C"},
		{StatusCode: "error", StatusMessage: "m6"},
	}
	errs, sqls := invSpanErrorEvidence(spans)
	if strings.Join(errs, ",") != "m1,m2,m3,m4,m5" {
		t.Errorf("durum mesajları = %v; ilk 5 hata span'i", errs)
	}
	if len(sqls) != 3 || sqls[0] != "INSERT INTO A (X) VALUES (?)" || sqls[1] != truncRunesN(long, 600) || sqls[2] != "UPDATE B SET Y = 1" {
		t.Errorf("SQL = %v; ≤3, tekil, 600 rune", sqls)
	}
}

func TestCodeOutcomeTransient(t *testing.T) {
	for o, want := range map[devops.CodeOutcome]bool{
		devops.CodeOK: false, devops.CodePartial: false, devops.CodeUnconfigured: false, devops.CodeRepoUnresolved: false,
		devops.CodeProjectDeadEnd: false, devops.CodeNoStack: false, devops.CodeEmptyTree: false, devops.CodeTreeMiss: false,
		devops.CodeWindowFailed: false, devops.CodeOther: false,
		devops.CodeDeadline: true, devops.CodeCancelled: true, devops.CodeBackendError: true, devops.CodeCatalogError: true,
	} {
		if got := codeOutcomeTransient(o); got != want {
			t.Errorf("codeOutcomeTransient(%s) = %v; %v", o, got, want)
		}
	}
}

// ── taşma zinciri (mevcut, değişmeden) + inceleme kuyruğu ──────────────────

// codeOverflowProvider — ilk `overflow` çağrı bağlam taşması 400'ü; sonra cevap.
type codeOverflowProvider struct {
	mu       sync.Mutex
	overflow int
	answer   string
	systems  []string
	users    []string
	srv      *httptest.Server
}

func newCodeOverflowProvider(t *testing.T, overflow int, answer string) *codeOverflowProvider {
	t.Helper()
	p := &codeOverflowProvider{overflow: overflow, answer: answer}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		p.mu.Lock()
		for _, m := range body.Messages {
			switch m.Role {
			case "system":
				p.systems = append(p.systems, m.Content)
			case "user":
				p.users = append(p.users, m.Content)
			}
		}
		n := len(p.systems)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n <= p.overflow {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"This model's maximum context length is 8192 tokens. However, you requested 12000 tokens"}}`)
			return
		}
		out, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": p.answer}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
		_, _ = w.Write(out)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func TestInvCodeRunOverflowChain(t *testing.T) {
	const answer = "**Kök Neden ve Sonraki Adım**\n- 246. satırdaki null kontrolü eksik."
	tiny := devops.CodeContext{Repo: "payments", Branch: "master", Outcome: devops.CodeOK,
		Windows: []devops.CodeWindow{{Path: "/src/A.java", FromLine: 9, ToLine: 9, Content: "9| int x = 1;"}}}
	big := fetchFakeCodeContext(t) // 4000 runelik bütçede, hata satırı 246
	cases := []struct {
		name       string
		cc         devops.CodeContext
		overflow   int
		wantCalls  int
		wantErr    bool
		lastSystem func() string
		lastUser   []string // son denemenin user bloğunda
		lastNot    []string
		// numWarn — cevaptaki "246" kanıtta yoksa sayı uyarısı: kod bloğu 246'yı
		// taşıyorsa (codeEvidence) uyarı YOK, taşımıyorsa denetim yine çalışır.
		numWarn bool
	}{
		{"taşma yok → tek çağrı, tam kod", big, 0, 1, false, copilot.SystemPromptTraceInvestigationWithCode,
			[]string{"## [T]", "KOD BAĞLAMI (depo", ">>> 246| "}, nil, false},
		{"taşma → kod YARIYA iner (kodlu istem)", big, 1, 2, false, copilot.SystemPromptTraceInvestigationWithCode,
			[]string{"## [T]", "KOD BAĞLAMI (depo", ">>> 246| "}, nil, false},
		{"taşma + yarıya inemez → KODSUZ, düz inceleme istemi", tiny, 1, 2, false, copilot.SystemPromptTraceInvestigation,
			[]string{"## [T]", "KOD BAĞLAMI İSTENDİ — ÇÖZÜLEMEDİ: bağlam taşması"}, []string{"int x = 1", "pencere 1/"}, true},
		// Zincir bugün de iki adımlı: yarım kod da taşarsa hata döner (üçüncü deneme yok).
		{"yarım kod da taşar → hata (üçüncü deneme yok)", big, 2, 2, true, copilot.SystemPromptTraceInvestigationWithCode, nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newCodeOverflowProvider(t, tc.overflow, answer)
			cop := copilot.New(copilot.ProviderOpenAI, "test-key", "gemma4")
			cop.Configure(copilot.ProviderOpenAI, "test-key", "gemma4", p.srv.URL, false, true)
			s := &Server{copilot: cop}
			inv, _, err := runInvestigation(t, newFakeInvRunner(invTestT0), "")
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/copilot/explain-trace/"+invTestTrace, nil)
			out, err := s.invCodeRun(r, inv, inv.User, tc.cc, schemaEvidence{})(nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("hata = %v; beklenen hata=%v", err, tc.wantErr)
			}
			p.mu.Lock()
			systems, users := append([]string(nil), p.systems...), append([]string(nil), p.users...)
			p.mu.Unlock()
			if len(systems) != tc.wantCalls {
				t.Fatalf("sağlayıcı çağrısı = %d; %d", len(systems), tc.wantCalls)
			}
			if systems[0] != copilot.SystemPromptTraceInvestigationWithCode() {
				t.Error("ilk deneme kodlu inceleme istemiyle gitmedi")
			}
			if systems[len(systems)-1] != tc.lastSystem() {
				t.Errorf("son denemenin sistem istemi beklenen değil")
			}
			last := users[len(users)-1]
			for _, w := range tc.lastUser {
				if !strings.Contains(last, w) {
					t.Errorf("son denemede %q yok", w)
				}
			}
			for _, no := range tc.lastNot {
				if strings.Contains(last, no) {
					t.Errorf("son denemede %q olmamalı (kod düşmeliydi)", no)
				}
			}
			if tc.wantCalls == 2 && runeLen(users[1]) >= runeLen(users[0]) {
				t.Errorf("yeniden deneme kısalmadı: %d → %d rune", runeLen(users[0]), runeLen(users[1]))
			}
			if tc.wantErr {
				return
			}
			if !strings.HasPrefix(out, answer) || !strings.Contains(out, "**Kaynak durumu**") {
				t.Errorf("cevap inceleme kuyruğuyla kapanmadı: %q", out)
			}
			if got := strings.Contains(out, "Kanıtta bulunamayan sayı(lar): 246"); got != tc.numWarn {
				t.Errorf("sayı uyarısı=%v; beklenen %v (kod bloğundaki satır numarası kanıt sayılır)\n%s", got, tc.numWarn, out)
			}
		})
	}
}

// ── önbellek ───────────────────────────────────────────────────────────────

func TestTraceCodeVariantCache(t *testing.T) {
	sys, sysCode := copilot.SystemPromptTraceInvestigation(), copilot.SystemPromptTraceInvestigationWithCode()
	if traceInvestigationCacheKey(sys, invTestTrace, "") == traceInvestigationCacheKey(sysCode, invTestTrace, "") {
		t.Fatal("kodlu ve kodsuz inceleme aynı önbellek anahtarını paylaşıyor")
	}

	e := newCodeTestEnv(t)
	first := e.call(t, "?stream=1", true)
	reads, git, llm := e.f.callCount(), e.tfs.requests(), e.p.requests()
	if reads != 5 || git == 0 || llm != 1 {
		t.Fatalf("ilk istek: araç=%d git=%d llm=%d", reads, git, llm)
	}
	if first.ans["cached"] != nil || len(codeFiles(first.ans)) == 0 {
		t.Fatalf("ilk istek: cached=%v code=%v", first.ans["cached"], first.ans["code"])
	}

	hit := e.call(t, "?stream=1", true)
	if e.f.callCount() != reads || e.tfs.requests() != git || e.p.requests() != llm {
		t.Fatalf("isabette okuma/kod çekimi/LLM koştu (araç=%d git=%d llm=%d)", e.f.callCount(), e.tfs.requests(), e.p.requests())
	}
	if hit.ans["cached"] != true || hit.steps != 0 {
		t.Errorf("isabet etiketsiz ya da adım yayınladı: cached=%v adım=%d", hit.ans["cached"], hit.steps)
	}
	if got, want := strings.Join(codeFiles(hit.ans), ","), strings.Join(codeFiles(first.ans), ","); got != want || got == "" {
		t.Errorf("isabette kod künyesi = %q; ilk cevaptaki %q (yan kayıt)", got, want)
	}
	hc, _ := hit.ans["code"].(map[string]any)
	fc, _ := first.ans["code"].(map[string]any)
	if hc["repo"] != fc["repo"] || hc["repo"] != "payments" {
		t.Errorf("isabette depo = %v; %v", hc["repo"], fc["repo"])
	}
	if srcs, _ := hit.ans["sources"].([]any); len(srcs) == 0 {
		t.Error("isabette kaynak durumları kayboldu (yan kayıt)")
	}
	if hit.ans["text"] != first.ans["text"] {
		t.Error("isabet metni saklanan metin değil")
	}

	// Kodsuz istek kodlu satırı KULLANMAZ: kendi okumalarını koşar.
	plain := e.call(t, "?stream=1", false)
	if plain.ans["cached"] != nil || e.f.callCount() != reads*2 {
		t.Errorf("kodsuz istek kodlu satırdan servis edildi (cached=%v araç=%d)", plain.ans["cached"], e.f.callCount())
	}
	if plain.ans["code"] != nil {
		t.Errorf("kodsuz cevapta code = %v; null", plain.ans["code"])
	}
}

// Kod kaynağının GEÇİCİ arızası (git sunucusu 5xx) saklanmaz: arıza düzelince
// "Kod okunamadı" bir saat donmasın.
func TestTraceCodeVariantTransientCodeNotCached(t *testing.T) {
	e := newCodeTestEnv(t)
	e.tfs.mu.Lock()
	e.tfs.fail = true
	e.tfs.mu.Unlock()
	first := e.call(t, "?stream=1", true)
	code, _ := first.ans["code"].(map[string]any)
	if len(codeFiles(first.ans)) != 0 || code == nil || code["reason"] == "" {
		t.Fatalf("arızalı git sunucusunda kod geldi ya da gerekçeli künye yok: %v", first.ans["code"])
	}
	e.call(t, "?stream=1", true)
	if e.f.callCount() != 10 || e.p.requests() != 2 {
		t.Errorf("geçici kod arızalı cevap önbellekten servis edildi (araç=%d llm=%d)", e.f.callCount(), e.p.requests())
	}
}

// ── seçili span: kodlu istekte de odak; stack yoksa trace geneli TEK ek okuma ──
//
// Review düzeltmesi (v0.10.1034): span seçiliyken L okuması o span'e süzülüdür.
// Operatör çoğu zaman log basmayan bir span'e (kırmızı CLIENT yaprağı) ya da
// köke tıklar; exception'ı aşağı akıştaki servis basmıştır. Seçili span'in
// logunda stack YOKSA trace geneli TEK ek okuma (span süzgeçsiz, klasik limit)
// stack'i bulur; okumanın çıktısı inv.User'a girmez, kökeni söylenir.

func logsCalls(f *fakeInvRunner) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == invToolLogs {
			n++
		}
	}
	return n
}

func TestTraceCodeVariantSelectedSpan(t *testing.T) {
	// Seçili span'in KENDİ stack'i varsa o kazanır: ek okuma yok (trace genelinde
	// daha ciddi bir stack olsa bile — odak seçili span).
	for _, tc := range []struct{ span, focus, repo, notRepo string }{
		{invTestPaySpan, "payments", "payments", "checkout"},
		{invTestRoot, "checkout", "checkout", "payments"}, // checkout'un WARN gövde stack'i; payments ERROR'u daha ciddi ama seçili değil
	} {
		t.Run("kendi stack'i/"+tc.focus, func(t *testing.T) {
			e := newCodeTestEnv(t)
			res := e.call(t, "?stream=1&span="+tc.span, true)
			_, user := e.p.messagesAt(0)
			if !strings.Contains(user, "Odak: servis "+tc.focus+" (seçili span "+tc.span+")") {
				t.Errorf("kodlu istekte odak seçili span'in servisi değil:\n%s", user[:min(len(user), 400)])
			}
			if got := e.f.args[invToolLogs]["span_id"]; got != tc.span {
				t.Errorf("L okuması span'e süzülmedi: span_id=%v", got)
			}
			if got := e.f.args[invToolCompare]["service"]; got != tc.focus {
				t.Errorf("K okuması odak servisinde değil: %v", got)
			}
			if logsCalls(e.f) != 1 || e.ls.searchCount() != 1 {
				t.Errorf("seçili span'in stack'i varken ek log okuması yapıldı (araç=%d depo=%d)", logsCalls(e.f), e.ls.searchCount())
			}
			repos := e.tfs.repos()
			if !repos[tc.repo] || repos[tc.notRepo] {
				t.Errorf("kod deposu = %v; %s bekleniyordu (seçili span'in logundaki stack)", repos, tc.repo)
			}
			code, _ := res.ans["code"].(map[string]any)
			if code["repo"] != tc.repo || code["stackOrigin"] != nil || strings.Contains(user, "KOD KAYNAĞI") {
				t.Errorf("code.repo = %v (beklenen %s), stackOrigin = %v — kendi stack'inde köken notu olmamalı", code["repo"], tc.repo, code["stackOrigin"])
			}
		})
	}

	// Seçili span'in logu YOK (kök seçili; exception'ı aşağı akıştaki payments
	// basmış) → TEK ek trace geneli okuma; kod payments'ın stack'inden; inv.User
	// kodsuz istekle bayt bayt aynı; köken notu prompt'ta ve künyede.
	t.Run("seçili span'de stack yok → trace geneli ek okuma", func(t *testing.T) {
		e := newCodeTestEnv(t)
		e.ls.recs = codeTestLogs(invTestT0)[2:] // yalnız payments ERROR (span pay)
		res := e.call(t, "?stream=1&span="+invTestRoot, true)
		if logsCalls(e.f) != 2 || e.ls.searchCount() != 2 {
			t.Fatalf("ek okuma sayısı yanlış: araç=%d depo=%d (inceleme 1 + ek 1)", logsCalls(e.f), e.ls.searchCount())
		}
		if a := e.f.args[invToolLogs]; a["span_id"] != nil || a["limit"] != float64(traceExplainLogLimit) || a["trace_id"] != invTestTrace {
			t.Errorf("ek okuma argümanları = %v; trace_id + limit %d, span_id YOK", a, traceExplainLogLimit)
		}
		if res.steps != 14 { // inceleme 10 + ek okuma 2 + kod adımı 2
			t.Errorf("adım olayı = %d; 14 (ek okuma görünür)", res.steps)
		}
		repos := e.tfs.repos()
		if !repos["payments"] || repos["checkout"] {
			t.Errorf("kod deposu = %v; payments (aşağı akıştaki stack)", repos)
		}
		code, _ := res.ans["code"].(map[string]any)
		if origin, _ := code["stackOrigin"].(string); !strings.Contains(origin, "payments") || !strings.Contains(origin, "seçili span'in değil") {
			t.Errorf("künyede köken notu yok/yanlış: %q", origin)
		}
		_, codeUser := e.p.messagesAt(0)
		plain := e.call(t, "?stream=1&span="+invTestRoot, false)
		_, plainUser := e.p.messagesAt(1)
		if plain.ans["code"] != nil {
			t.Error("kodsuz cevapta code künyesi")
		}
		if !strings.HasPrefix(codeUser, plainUser) {
			t.Fatal("kodlu istekte inv.User kodsuz istekten farklı — ek okuma kanıta karıştı")
		}
		rest := codeUser[len(plainUser):]
		if !strings.HasPrefix(rest, "\n\nKOD KAYNAĞI (sunucu notu): seçili span "+invTestRoot) || !strings.Contains(rest, "basan servis: payments") {
			t.Errorf("kod bölümü köken notuyla başlamıyor:\n%s", rest[:min(len(rest), 300)])
		}
		if i, j := strings.Index(rest, "KOD KAYNAĞI"), strings.Index(rest, "KOD BAĞLAMI (depo"); j < 0 || i > j {
			t.Error("köken notu kod bloğundan önce değil")
		}
		// Hata metni ek okumanın stack'ini taşır → şema sinyali (SQLCODE) bulundu.
		if !strings.Contains(codeUser, "ŞEMA BAĞLAMI") {
			t.Error("şema kanıtı ek okumanın stack'iyle yeniden kurulmadı")
		}
		if e.f.callCount() != 5+1+5 { // kodlu 6, kodsuz 5 — kodsuzda ek okuma YOK
			t.Errorf("araç çağrısı = %d; 11", e.f.callCount())
		}
	})

	// Ek okuma kullanılamazsa (backend erişilemez) cevap SAKLANMAZ.
	t.Run("ek okuma başarısız → saklanmaz", func(t *testing.T) {
		e := newCodeTestEnv(t)
		e.ls.recs = codeTestLogs(invTestT0)[2:]
		e.ls.failTraceWide = true
		first := e.call(t, "?stream=1&span="+invTestRoot, true)
		if len(codeFiles(first.ans)) != 0 {
			t.Errorf("stack okunamadığı hâlde kod geldi: %v", first.ans["code"])
		}
		e.call(t, "?stream=1&span="+invTestRoot, true)
		if e.p.requests() != 2 || logsCalls(e.f) != 4 {
			t.Errorf("başarısız ek okumalı cevap önbellekten servis edildi (llm=%d log okuması=%d)", e.p.requests(), logsCalls(e.f))
		}
	})
}

// ── kod çekimi bir inceleme adımı (akış 25 sn sessiz kalmasın) ──────────────

func TestTraceCodeStepEvent(t *testing.T) {
	e := newCodeTestEnv(t)
	res := e.call(t, "?stream=1", true)
	var step, result map[string]any
	lastInvResult, stepAt := -1, -1
	for i, fr := range res.frames {
		switch {
		case fr.event == "step" && fr.data["tool"] == invToolCode:
			step, stepAt = fr.data, i
		case fr.event == "step-result" && fr.data["tool"] == invToolCode:
			result = fr.data
		case fr.event == "step-result":
			lastInvResult = i
		}
	}
	if step == nil || result == nil || result["i"] != step["i"] {
		t.Fatalf("kod adımı/sonucu = %v / %v", step, result)
	}
	if stepAt < lastInvResult {
		t.Error("kod adımı incelemenin okumalarından ÖNCE yayınlandı")
	}
	if args, _ := step["args"].(string); args != `{"service":"payments"}` {
		t.Errorf("kod adımı argümanı = %q", args)
	}
	preview, _ := result["preview"].(string)
	if !strings.Contains(preview, "ChargeRepository.java") {
		t.Errorf("önizlemede dosya künyesi yok: %s", preview)
	}
	if strings.Contains(preview, "HATA_SATIRI") || strings.Contains(preview, "repo.save") {
		t.Errorf("kod İÇERİĞİ adım önizlemesine sızdı (kod tarayıcıya gitmez): %s", preview)
	}
	srcs, _ := result["sources"].([]any)
	if len(srcs) != 1 {
		t.Fatalf("kod adımı rozeti = %v", result["sources"])
	}
	if s0 := srcs[0].(map[string]any); s0["source"] != "code" || (s0["state"] != "ok" && s0["state"] != "truncated") {
		t.Errorf("kod adımı rozeti = %v", s0)
	}
}

func TestCodeStepStatus(t *testing.T) {
	win := []devops.CodeWindow{{Path: "/src/A.java", Line: 9, FromLine: 9, ToLine: 9}}
	for _, tc := range []struct {
		cc   devops.CodeContext
		want sourcestate.State
	}{
		{devops.CodeContext{Windows: win, Outcome: devops.CodeOK}, sourcestate.OK},
		{devops.CodeContext{Windows: win, Outcome: devops.CodePartial}, sourcestate.Truncated},
		{devops.CodeContext{Outcome: devops.CodeUnconfigured}, sourcestate.NotConfigured},
		{devops.CodeContext{Outcome: devops.CodeDeadline}, sourcestate.Timeout},
		{devops.CodeContext{Outcome: devops.CodeBackendError}, sourcestate.Unreachable},
		{devops.CodeContext{Outcome: devops.CodeCatalogError}, sourcestate.Error},
		{devops.CodeContext{Outcome: devops.CodeNoStack}, sourcestate.Empty},
		{devops.CodeContext{Outcome: devops.CodeTreeMiss}, sourcestate.Empty},
	} {
		if got := codeStepStatus(tc.cc); got.State != tc.want || got.Source != "code" {
			t.Errorf("codeStepStatus(%s) = %s/%s; %s", tc.cc.Outcome, got.Source, got.State, tc.want)
		}
	}
}

// ── sayı denetimi kodla kandırılamaz ───────────────────────────────────────

func TestInvCodeNumberCheck(t *testing.T) {
	var content strings.Builder
	for i := 85; i <= 101; i++ {
		fmt.Fprintf(&content, "%d| int v%d = %d;\n", i, i, i*7)
	}
	win := devops.CodeWindow{Path: "/src/main/java/com/example/payments/ChargeHandler.java", Line: 93, FromLine: 85, ToLine: 101,
		Content: strings.TrimRight(content.String(), "\n")}
	full := devops.CodeContext{Repo: "payments", Branch: "master", Outcome: devops.CodeOK, Windows: []devops.CodeWindow{win}}
	one := devops.CodeContext{Repo: "payments", Branch: "master", Outcome: devops.CodeOK,
		Windows: []devops.CodeWindow{{Path: win.Path, Line: 93, FromLine: 93, ToLine: 93, Content: "93| repo.save(x);"}}}
	// Cevap: dosya:satır anar (meşru), pencere içindeki bir satır numarasını (87)
	// gecikme diye uydurur, çitli kod alıntısında bir sabit (4321) taşır.
	answer := "**Kök Neden ve Sonraki Adım**\n- Hata ChargeHandler.java:93 satırında; p95 87 ms'ye çıktı.\n```java\n// ChargeHandler.java:85-101\n>>> 93| repo.save(x, 4321);\n```"
	cases := []struct {
		name     string
		cc       devops.CodeContext
		overflow int
		flagged  []string
		clean    []string
	}{
		{"kod gönderildi: satır referansı kanıt, pencere satırı değil, çitli kod iddia değil", full, 0, []string{"87 ms"}, []string{"93", "4321"}},
		{"kod taşmada DÜŞTÜ: bloktan hiçbir şey kanıt sayılmaz", one, 1, []string{"87 ms", "93"}, []string{"4321"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newCodeOverflowProvider(t, tc.overflow, answer)
			cop := copilot.New(copilot.ProviderOpenAI, "test-key", "gemma4")
			cop.Configure(copilot.ProviderOpenAI, "test-key", "gemma4", p.srv.URL, false, true)
			s := &Server{copilot: cop}
			inv, _, err := runInvestigation(t, newFakeInvRunner(invTestT0), "")
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range []string{"87 ms", "93", "4321"} {
				if len(ungroundedNumbers(n, inv.User)) == 0 {
					t.Fatalf("fikstür %q'yı zaten kanıtlıyor — test anlamsız", n)
				}
			}
			r := httptest.NewRequest(http.MethodPost, "/api/copilot/explain-trace/"+invTestTrace, nil)
			out, err := s.invCodeRun(r, inv, inv.User, tc.cc, schemaEvidence{})(nil)
			if err != nil {
				t.Fatal(err)
			}
			_, warn, _ := strings.Cut(out, "⚠ Kanıtta bulunamayan sayı(lar): ")
			warn, _, _ = strings.Cut(warn, " — ")
			got := map[string]bool{}
			for _, x := range strings.Split(warn, ", ") {
				got[strings.TrimSpace(x)] = true
			}
			for _, f := range tc.flagged {
				if !got[f] {
					t.Errorf("%q işaretlenmedi (uyarı: %q)", f, warn)
				}
			}
			for _, c := range tc.clean {
				if got[c] {
					t.Errorf("%q yanlışlıkla işaretlendi (uyarı: %q)", c, warn)
				}
			}
		})
	}
	if got := maskFencedCode("a ```x 12``` b ```açık 34"); strings.Contains(got, "12") || strings.Contains(got, "34") || !strings.HasPrefix(got, "a ") {
		t.Errorf("maskFencedCode = %q", got)
	}
	if ev := codeRefEvidence(full); !strings.Contains(ev, "93 85 101") || strings.Contains(ev, "87") {
		t.Errorf("codeRefEvidence = %q; yalnız referanslar", ev)
	}
}

// ── varsayılan yol ek iş yapmaz ────────────────────────────────────────────

// Kodsuz incelemede ham-kayıt kancası KURULMAZ, stack ayrıştırılmaz; kanıt
// (inv.User) kodlu incelemeyle bayt bayt aynı.
func TestTraceInvestigationNoCodeNoSink(t *testing.T) {
	e := newCodeTestEnv(t)
	run := newTraceInvestigationRunner(e.s, nil)
	plain, err := e.s.investigateTrace(withInvestigationRunner(context.Background(), run), invTestTrace, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	coded, err := e.s.investigateTrace(withInvestigationCodeInputs(withInvestigationRunner(context.Background(), run)), invTestTrace, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Stack != "" || plain.StackService != "" || plain.wantCode || invSectionByKey(plain, "L").callCtx != nil {
		t.Errorf("kodsuz incelemede kanca kuruldu / stack ayrıştırıldı (stack=%d rune, servis=%q)", runeLen(plain.Stack), plain.StackService)
	}
	if coded.Stack != codeTestStackPay || invSectionByKey(coded, "L").callCtx == nil {
		t.Error("kodlu incelemede kanca kurulmadı")
	}
	if plain.User != coded.User {
		t.Error("kodlu ve kodsuz incelemenin kanıtı (inv.User) farklı")
	}
	// Kaynak pini: şema girdisi (hata metni + SQL) investigateTrace'te HESAPLANMAZ.
	if body := serverFuncBodies(t)["investigateTrace"]; strings.Contains(body, "invSpanErrorEvidence") || strings.Contains(body, "codeSchemaInputs") || strings.Contains(body, "traceLogStack") {
		t.Error("investigateTrace kod girdisi hesaplıyor — varsayılan yolda ek iş")
	}
}

// ── ölçüm: bütçe tavanlarında prompt boyu (eski kodlu yol vs yeni) ──────────

// codeCeilTraceJSON — get_trace zarfı, her listesi T bütçesini taşıracak kadar dolu.
func codeCeilTraceJSON(t0 time.Time) string {
	ns := t0.UnixNano()
	long := func(prefix string, i int) string {
		return fmt.Sprintf("%s-%02d %s", prefix, i, strings.Repeat("ayrıntı ", 20))
	}
	an := map[string]any{
		"wall_ms": 4321.5, "extent_ms": 4400.2, "start_unix_ns": ns, "end_unix_ns": ns + 4_400_200_000,
		"root":       map[string]any{"span_id": invTestRoot, "service": "checkout", "name": "POST /orders/checkout/confirm", "duration_ms": 4321.5},
		"span_count": 200, "error_span_count": 40, "orphan_count": 0,
		"notes": []string{long("not", 1), long("not", 2), long("not", 3), long("not", 4)},
	}
	var svcs, top, errs, path, ctxs []map[string]any
	for i := 0; i < 10; i++ {
		svc := fmt.Sprintf("svc-%02d", i)
		svcs = append(svcs, map[string]any{"service": svc, "self_ms": 400.5, "self_pct": 9.5, "spans": 20, "errors": 4})
		top = append(top, map[string]any{"span_id": fmt.Sprintf("%016x", 100+i), "service": svc, "name": long("op", i), "self_ms": 300.1, "duration_ms": 900.2, "error": true})
		errs = append(errs, map[string]any{"span_id": fmt.Sprintf("%016x", 200+i), "service": svc, "name": long("op", i), "status_message": long("hata", i), "start_unix_ns": ns + int64(i), "duration_ms": 12.5})
		path = append(path, map[string]any{"span_id": fmt.Sprintf("%016x", 300+i), "service": svc, "name": long("adım", i), "self_on_path_ms": 50.5})
		ctxs = append(ctxs, map[string]any{"service": svc, "env": "prod", "cluster": "cluster-a", "namespace": "ns-" + svc,
			"pods": []string{svc + "-pod-a1b2c3", svc + "-pod-d4e5f6", svc + "-pod-g7h8i9"}, "pods_total": 6, "versions": []string{"1.2.3", "1.2.4"}})
	}
	an["services"], an["top_self_spans"], an["error_spans"], an["critical_path"], an["critical_path_steps"], an["context"] = svcs, top, errs, path, 40, ctxs
	spans := []map[string]any{{"spanId": invTestRoot, "serviceName": "checkout", "name": "POST /orders/checkout/confirm", "statusCode": "ok"}}
	for i := 0; i < 10; i++ {
		spans = append(spans, map[string]any{"spanId": fmt.Sprintf("%016x", 200+i), "serviceName": fmt.Sprintf("svc-%02d", i), "name": long("op", i),
			"statusCode": "error", "statusMessage": long("hata", i) + " SQLCODE=-302, SQLSTATE=22001", "dbStatement": "INSERT INTO SHOP.WIDE_TABLE (" + codeCeilColumns() + ") VALUES (?)"})
	}
	b, _ := json.Marshal(map[string]any{
		"source":   map[string]any{"source": "traces", "backend": "clickhouse", "state": "ok", "returned": 200, "limit": 200},
		"trace_id": invTestTrace, "span_count": 200, "total_span_count": 200, "truncated": false, "analysis": an, "spans": spans,
	})
	return string(b)
}

func codeCeilColumns() string {
	var cols []string
	for i := 0; i < 40; i++ {
		cols = append(cols, fmt.Sprintf("COLUMN_NAME_%02d", i))
	}
	return strings.Join(cols, ", ")
}

// codeCeilLogs — 100 kayıt: uzun gövde (araç 500'de keser), her ERROR'da ayrı
// stack (araç 200'de keser; klasik yol 1500/900 bayt taşır).
func codeCeilLogs(t0 time.Time) []*logstore.LogRecord {
	var out []*logstore.LogRecord
	for i := 0; i < 100; i++ {
		var st strings.Builder
		fmt.Fprintf(&st, "com.example.svc%02d.WidgetException: işlem %d başarısız SQLCODE=-302\n", i, i)
		for j := 0; j < 40; j++ {
			fmt.Fprintf(&st, "\tat deployment.svc%02d.war//com.example.svc%02d.layer%02d.WidgetService.step%02d(WidgetService.java:%d)\n", i, i, j, j, 100+j)
		}
		out = append(out, &logstore.LogRecord{Timestamp: t0.Add(time.Duration(i) * time.Millisecond).UnixNano(), Severity: 17, SeverityText: "ERROR",
			ServiceName: fmt.Sprintf("svc-%02d", i%10), TraceID: invTestTrace, SpanID: fmt.Sprintf("%016x", 200+i%10),
			Body:       fmt.Sprintf("istek %d başarısız: %s", i, strings.Repeat("gövde ayrıntısı ", 60)),
			Attributes: map[string]string{"exception.type": fmt.Sprintf("com.example.svc%02d.WidgetException", i), "exception.stacktrace": st.String()}})
	}
	return out
}

// codeCeilTempo — 150 span (klasik yol 100'ü alır): uzun adlar, 10 hata span'i SQL'li.
func codeCeilTempo(t *testing.T, t0 time.Time) *tempo.Service {
	t.Helper()
	ns := t0.UnixNano()
	var spans []string
	for i := 0; i < 150; i++ {
		parent, status, attrs := "", `{"code":1}`, "[]"
		if i > 0 {
			parent = fmt.Sprintf(`,"parentSpanId":%q`, invTestRoot)
		}
		if i > 0 && i <= 10 {
			status = fmt.Sprintf(`{"code":2,"message":%q}`, fmt.Sprintf("hata-%02d %s SQLCODE=-302, SQLSTATE=22001", i, strings.Repeat("ayrıntı ", 20)))
			attrs = fmt.Sprintf(`[{"key":"db.statement","value":{"stringValue":%q}}]`, "INSERT INTO SHOP.WIDE_TABLE ("+codeCeilColumns()+") VALUES (?)")
		}
		id := invTestRoot
		if i > 0 {
			id = fmt.Sprintf("%016x", 1000+i)
		}
		spans = append(spans, fmt.Sprintf(`{"traceId":%q,"spanId":%q%s,"name":%q,"kind":2,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","status":%s,"attributes":%s}`,
			invTestTrace, id, parent, fmt.Sprintf("op-%03d %s", i, strings.Repeat("uzun-ad ", 6)), ns+int64(i)*1_000_000, ns+int64(i)*1_000_000+int64(150-i)*10_000_000, status, attrs))
	}
	body := `{"batches":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeSpans":[{"spans":[` + strings.Join(spans, ",") + `]}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	tp := tempo.New()
	tp.Configure(tempo.Settings{Enabled: true, BaseURL: srv.URL})
	return tp
}

// TestTraceCodePromptSizeCeilings — ÖLÇÜM (karar girdisi, küçük yerel modelde
// bağlam riski): bütçe tavanlarındaki fikstürle modele giden user prompt'unun
// rune boyu, (a) eski klasik + kod yolunda, (b) yeni inceleme + kod yolunda.
// Kod bloğu (4000 rune bütçe, fetchFakeCodeContext) ve şema bloğu (800) iki
// yolda AYNI — fark kanıt paketinden. Sayılar -v ile basılır; iddialar gevşek
// tavan (bütçe toplamı + başlık payı) — biri bütçeyi sessizce büyütürse kırmızı.
func TestTraceCodePromptSizeCeilings(t *testing.T) {
	cc := fetchFakeCodeContext(t)
	if !strings.Contains(cc.Reason, "kod bütçesi") && cc.Outcome != devops.CodePartial {
		t.Fatalf("kod fikstürü bütçe tavanında değil: %s", cc.Reason)
	}
	// Şema: 40 kolonlu INSERT + katalog → 800 runelik bölüm tavanı.
	var csv strings.Builder
	csv.WriteString("TABSCHEMA,TABNAME,COLNAME,TYPENAME,LENGTH,SCALE,NULLS\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&csv, "SHOP,WIDE_TABLE,COLUMN_NAME_%02d,VARCHAR,%d,0,N\n", i, 10+i)
	}
	cat, err := appschema.ParseCSV(strings.NewReader(csv.String()))
	if err != nil {
		t.Fatal(err)
	}
	sch := appschema.NewService()
	sch.Set(cat)

	// (a) klasik + kod — Tempo'da 150 span, 100 log (klasik 30 okur, 15'ini yazar).
	cls := &Server{tempo: codeCeilTempo(t, invTestT0), logs: &codeTestLogStore{recs: codeCeilLogs(invTestT0)}, schema: sch}
	in, err := cls.buildTraceExplainInput(context.Background(), invTestTrace)
	if err != nil {
		t.Fatal(err)
	}
	seA := cls.buildSchemaEvidence(in.ErrorText, in.DBStatements, mapperBlocks(cc))
	userA := in.User + cc.PromptBlock() + seA.Block

	// (b) inceleme + kod — T/L/K/P/D/O bölümleri taşacak kadar dolu.
	withFakeOracle(t, &fakeOracleReader{rows: func() (rows []chstore.OracleErrorRow) {
		for i := 0; i < 20; i++ {
			rows = append(rows, chstore.OracleErrorRow{SourceID: "o-1", Time: invTestT0.Add(time.Duration(i) * time.Millisecond), RowID: uint64(i),
				TraceID: invTestTrace, OperationCode: "CHARGE", ErrorCode: fmt.Sprintf("ORA-%05d", 60+i), Body: strings.Repeat("deadlock detected ", 10)})
		}
		return rows
	}()})
	ls := &codeTestLogStore{recs: codeCeilLogs(invTestT0)}
	f := newFakeInvRunner(invTestT0)
	f.out[invToolTrace] = invOK(codeCeilTraceJSON(invTestT0))
	f.out[invToolPods] = invOK(codeCeilPodsJSON())
	f.out[invToolCompare] = invOK(codeCeilCompareJSON(t))
	f.out[invToolDeploys] = invOK(codeCeilDeploysJSON(invTestT0))
	s := &Server{oracle: invOracleService(true), schema: sch}
	inv, err := s.investigateTrace(withInvestigationCodeInputs(withInvestigationRunner(context.Background(), codeTestRunner(t, f, ls))), invTestTrace, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	errorTextB, sqlB := inv.codeSchemaInputs()
	seB := s.buildSchemaEvidence(errorTextB, sqlB, mapperBlocks(cc))
	userB := inv.User + cc.PromptBlock() + seB.Block
	oracleRunes := 0
	if o := invSectionByKey(inv, "O"); o != nil {
		oracleRunes = runeLen(o.render())
	}

	sec := map[string]int{}
	for _, x := range inv.Sections {
		sec[x.Key] = runeLen(x.render())
	}
	t.Logf("ÖLÇÜM (rune) — kod bloğu %d, şema bloğu (a) %d / (b) %d", runeLen(cc.PromptBlock()), runeLen(seA.Block), runeLen(seB.Block))
	t.Logf("(a) ESKİ klasik+kod: user %d (kanıt %d + kod + şema); sistem %d (SystemPromptTraceWithCode), kodsuz klasik sistem %d",
		runeLen(userA), runeLen(in.User), runeLen(copilot.SystemPromptTraceWithCode()), runeLen(copilot.SystemPromptTrace()))
	t.Logf("(b) YENİ inceleme+kod: user %d (kanıt %d + kod + şema; Oracle'sız %d); sistem %d (SystemPromptTraceInvestigationWithCode), kodsuz inceleme sistemi %d",
		runeLen(userB), runeLen(inv.User), runeLen(userB)-oracleRunes, runeLen(copilot.SystemPromptTraceInvestigationWithCode()), runeLen(copilot.SystemPromptTraceInvestigation()))
	t.Logf("(b) bölümler: T=%d L=%d K=%d P=%d D=%d O=%d", sec["T"], sec["L"], sec["K"], sec["P"], sec["D"], sec["O"])
	t.Logf("toplam (sistem+user): (a) %d · (b) %d", runeLen(userA)+runeLen(copilot.SystemPromptTraceWithCode()), runeLen(userB)+runeLen(copilot.SystemPromptTraceInvestigationWithCode()))

	// Gevşek tavanlar: inceleme kanıtı bölüm bütçeleri toplamı (≈8.4K + O 1.2K +
	// stack 2×600) + başlık/önsöz payı; kod ≤ 4000 + pencere başlıkları; şema ≤ 800.
	if n := runeLen(inv.User); n > 14000 {
		t.Errorf("inceleme kanıtı %d rune — bölüm bütçeleri aşılmış", n)
	}
	if n := runeLen(cc.PromptBlock()); n > 5500 {
		t.Errorf("kod bloğu %d rune — 4000'lik bütçe aşılmış", n)
	}
	if runeLen(seB.Block) > 1000 || seB.Block == "" {
		t.Errorf("şema bloğu %d rune — 800 tavanı / fikstür", runeLen(seB.Block))
	}
	for _, k := range []string{"T", "L", "K", "P", "D", "O"} {
		if sec[k] == 0 {
			t.Errorf("bölüm %s boş — fikstür tavanı sınamıyor", k)
		}
	}
}

// codeCeilCompareJSON — invCompareJSON + her listesi dolu (not ×5, bağımlılık
// ×3, sürüm ×4, karışım kayması ×2): K bölümü kendi tavanında.
func codeCeilCompareJSON(t *testing.T) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(invCompareJSON("ok")), &m); err != nil {
		t.Fatal(err)
	}
	var notes []string
	for i := 0; i < 5; i++ {
		notes = append(notes, fmt.Sprintf("not-%d %s", i, strings.Repeat("kıyas ayrıntısı ", 20)))
	}
	m["notes"] = notes
	var vers []map[string]any
	for i := 0; i < 4; i++ {
		vers = append(vers, map[string]any{"value": fmt.Sprintf("1.%d.%d-build.%d", i, i, 1000+i), "count": 100 + i})
	}
	var deps []map[string]any
	for i := 0; i < 3; i++ {
		deps = append(deps, map[string]any{"kind": "service", "target": fmt.Sprintf("downstream-%02d-%s", i, strings.Repeat("x", 40)), "calls": 1200, "errors": 36, "error_rate_pct": 3, "p95_ms": 450.1})
	}
	for _, k := range []string{"problem", "reference"} {
		ps := m[k].(map[string]any)
		ps["versions"], ps["dependencies"] = vers, deps
	}
	m["traffic_mix_shift"] = []map[string]any{
		{"operation": "POST /orders/checkout/confirm/" + strings.Repeat("y", 40), "share_problem": 0.6, "share_reference": 0.3, "delta_pp": 30},
		{"operation": "GET /cart/items/" + strings.Repeat("z", 40), "share_problem": 0.1, "share_reference": 0.4, "delta_pp": -30},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func codeCeilPodsJSON() string {
	var pods, heap []string
	for i := 0; i < 12; i++ {
		pods = append(pods, fmt.Sprintf(`{"id":"checkout-7d9-%02d","cpu_pct":%d.5,"mem_bytes":512000000,"mem_pct":61.2,"up":true,"last_seen_unix_ns":1}`, i, 30+i))
		heap = append(heap, fmt.Sprintf(`{"pod":"checkout-7d9-%02d","heap_pct":%d.1,"post_gc_pct":40.2}`, i, 70+i))
	}
	return `{"heap_window_s":600,"heap":[` + strings.Join(heap, ",") + `],"pods":[` + strings.Join(pods, ",") + `],"pod_count":12,"pod_total":12,"pods_truncated":false,"pods_up":12}`
}

func codeCeilDeploysJSON(t0 time.Time) string {
	var ds []string
	for i := 0; i < 8; i++ {
		d := t0.Add(-time.Duration(10+i) * time.Minute)
		ds = append(ds, fmt.Sprintf(`{"service":"svc-%02d","version":"1.%d.%d-build.%d","time_iso":%q,"time_unix_ns":%d,"span_count":900}`, i, i, i, 1000+i, d.UTC().Format(time.RFC3339), d.UnixNano()))
	}
	return `{"deploys":[` + strings.Join(ds, ",") + `],"count":8,"window_s":21600,"has_more":false}`
}
