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
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/appschema"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/stackparse"
	"github.com/cilcenk/coremetry/internal/tempo"
)

// code_budget_prompt_test.go — v0.10.1038. KOD BÜTÇESİ AYARININ PROMPT'A
// ETKİSİ: ölçüm + tavan pini.
//
// Operatör: "Kod bütçesi daha fazla karakter olabilir bence, default 10k
// gibi, performans sorunu olmayacaksa." Bütçe Settings.CodeBudgetRunes
// oldu (varsayılan 10.000, eskiden sabit 4000). Bu dosya klasik trace + kod
// yolunun (buildTraceExplainInput + kod bloğu + şema) modele giden user
// prompt'unu iki bütçede ölçer — küçük gerçekçi bir fikstürde ve her
// bölümün tavanında — ve iki şeyi pinler: (1) büyüme yalnız kod bloğundan
// gelir ve bütçe farkıyla sınırlıdır; (2) LLM çağrı sayısı bütçeden
// bağımsızdır (1; taşmada 2). Sayılar -v ile basılır.

const cbTrace = "0af7651916cd43dd8448eb211c80319c"

// cbLogStore — sahte log deposu (yalnız trace okuması).
type cbLogStore struct {
	logstore.Store
	recs []*logstore.LogRecord
}

func (s *cbLogStore) Search(_ context.Context, _ logstore.Filter) (*logstore.Page, error) {
	out := make([]*logstore.LogRecord, 0, len(s.recs))
	for _, r := range s.recs {
		cp := *r
		out = append(out, &cp)
	}
	return &logstore.Page{Logs: out, Total: len(out)}, nil
}

func (s *cbLogStore) Backend() string { return "elasticsearch" }

// cbStack — uygulama frame'li gerçekçi bir JBoss stack'i (frames derin).
func cbStack(frames int) string {
	var b strings.Builder
	b.WriteString("java.lang.IllegalStateException: no lines: C-1001\n")
	b.WriteString("\tat deployment.orders.war//com.acme.orders.OrderValidator.process03(OrderValidator.java:90)\n")
	b.WriteString("\tat deployment.orders.war//com.acme.orders.OrderService.process04(OrderService.java:113)\n")
	b.WriteString("\tat deployment.orders.war//com.acme.orders.OrderController.process05(OrderController.java:136)\n")
	for i := 0; i < frames; i++ {
		fmt.Fprintf(&b, "\tat org.jboss.as.ee.component.interceptors.Interceptor%02d.processInvocation(Interceptor%02d.java:%d)\n", i, i, 40+i)
	}
	return b.String()
}

// cbLogs — n kayıt: ilki ERROR + tam stack, gerisi gövdeli INFO/WARN
// (ceil=true: her kayıt uzun gövde + kendi stack'i — klasik yolun 15'lik
// tavanı ve 600/1500/900 bayt kesmeleri dolar).
func cbLogs(t0 time.Time, n int, ceil bool) []*logstore.LogRecord {
	var out []*logstore.LogRecord
	for i := 0; i < n; i++ {
		r := &logstore.LogRecord{Timestamp: t0.Add(time.Duration(i) * time.Millisecond).UnixNano(),
			Severity: 9, SeverityText: "INFO", ServiceName: "orders-api", TraceID: cbTrace,
			Body: fmt.Sprintf("sipariş %d işleniyor (müşteri C-1001)", i)}
		if i == 0 || ceil {
			r.Severity, r.SeverityText = 17, "ERROR"
			r.Body = "sipariş işlenemedi: no lines: C-1001"
			r.Attributes = map[string]string{"exception.type": "java.lang.IllegalStateException", "exception.stacktrace": cbStack(30)}
		}
		if ceil {
			r.Body += " " + strings.Repeat("gövde ayrıntısı ", 60)
		}
		out = append(out, r)
	}
	return out
}

// cbTempo — n span'lik trace (Tempo yedeği; klasik yol en çok 100 alır);
// errSpans tanesi hata + SQL taşır.
func cbTempo(t *testing.T, t0 time.Time, n, errSpans int, ceil bool) *tempo.Service {
	t.Helper()
	ns := t0.UnixNano()
	cols := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		cols = append(cols, fmt.Sprintf("COLUMN_NAME_%02d", i))
	}
	var spans []string
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%016x", 0xa000+i)
		parent, status, attrs := "", `{"code":1}`, "[]"
		if i > 0 {
			parent = fmt.Sprintf(`,"parentSpanId":%q`, fmt.Sprintf("%016x", 0xa000))
		}
		name := fmt.Sprintf("OrderService.process%02d", i%8)
		if ceil {
			name += " " + strings.Repeat("uzun-ad ", 6)
		}
		if i > 0 && i <= errSpans {
			msg := "no lines: C-1001"
			if ceil {
				msg = fmt.Sprintf("hata-%02d %s SQLCODE=-302, SQLSTATE=22001", i, strings.Repeat("ayrıntı ", 20))
				attrs = fmt.Sprintf(`[{"key":"db.statement","value":{"stringValue":%q}}]`, "INSERT INTO SHOP.WIDE_TABLE ("+strings.Join(cols, ", ")+") VALUES (?)")
			}
			status = fmt.Sprintf(`{"code":2,"message":%q}`, msg)
		}
		spans = append(spans, fmt.Sprintf(`{"traceId":%q,"spanId":%q%s,"name":%q,"kind":2,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","status":%s,"attributes":%s}`,
			cbTrace, id, parent, name, ns+int64(i)*1_000_000, ns+int64(i)*1_000_000+int64(n-i+1)*10_000_000, status, attrs))
	}
	body := `{"batches":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"orders-api"}}]},"scopeSpans":[{"spans":[` + strings.Join(spans, ",") + `]}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	tp := tempo.New()
	tp.Configure(tempo.Settings{Enabled: true, BaseURL: srv.URL})
	return tp
}

// cbJava — üç uygulama dosyası. ceil=false: gerçekçi satır boyu (~34
// karakter; ±30 penceresi ~2.450 rune — repodaki JBoss demo kaynağında
// ölçülen bant 2.313-2.746, medyan 2.520). ceil=true: ~95 karakterlik
// satırlar (pencere ~6.100 rune) — üç pencere HER İKİ bütçeyi de doldurur.
func cbJava(class string, ceil bool) string {
	var b strings.Builder
	b.WriteString("package com.acme.orders;\n")
	for i := 2; i <= 200; i++ {
		switch {
		case ceil:
			fmt.Fprintf(&b, "        // %s satır %d — bu satır kod bütçesini doldurmak için bilerek uzun tutuldu\n", class, i)
		case i%4 == 0:
			fmt.Fprintf(&b, "        total = total.add(l.getAmount());\n")
		case i%4 == 1:
			fmt.Fprintf(&b, "        if (lines.isEmpty()) {\n")
		case i%4 == 2:
			fmt.Fprintf(&b, "            throw new IllegalStateException(\"no lines\");\n")
		default:
			fmt.Fprintf(&b, "        }\n")
		}
	}
	return b.String()
}

// cbCodeContext — sahte DevOps'tan gerçek FetchCode akışıyla kod bağlamı;
// ikinci dönüş git isteği sayısı.
func cbCodeContext(t *testing.T, budget int, ceil bool) (devops.CodeContext, int) {
	t.Helper()
	files := map[string]string{}
	var tree []map[string]any
	for _, cls := range []string{"OrderValidator", "OrderService", "OrderController"} {
		p := "/src/main/java/com/acme/orders/" + cls + ".java"
		files[p] = cbJava(cls, ceil)
		tree = append(tree, map[string]any{"path": p, "gitObjectType": "blob"})
	}
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		p, q := r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(p, "/refs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []map[string]string{{"name": "refs/heads/release"}, {"name": "refs/heads/master"}}})
		case strings.HasSuffix(p, "/items") && q.Get("recursionLevel") == "Full":
			_ = json.NewEncoder(w).Encode(map[string]any{"value": tree})
		case strings.HasSuffix(p, "/items"):
			_ = json.NewEncoder(w).Encode(map[string]any{"content": files[q.Get("path")]})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"defaultBranch": "refs/heads/master"})
		}
	}))
	t.Cleanup(srv.Close)
	dv := devops.New()
	dv.Configure(devops.Settings{BaseURL: srv.URL, Collection: "DefaultCollection", Project: "Orders",
		PAT: "test-pat", Flavor: devops.FlavorServer, CodeBudgetRunes: budget})
	cc := dv.FetchCode(context.Background(), "orders-api", devops.ProjectHint{}, stackparse.ParseJava(cbStack(0)), nil, nil)
	if len(cc.Windows) == 0 {
		t.Fatalf("kod çekilemedi: %s", cc.Reason)
	}
	mu.Lock()
	defer mu.Unlock()
	return cc, calls
}

// cbSchema — 40 kolonlu WIDE_TABLE kataloğu (şema bölümü kendi 800'lük tavanında).
func cbSchema(t *testing.T) *appschema.Service {
	t.Helper()
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
	return sch
}

func cbRunes(s string) int { return utf8.RuneCountInString(s) }

// TestTraceCodePromptByCodeBudget — ÖLÇÜM + TAVAN PİNİ (v0.10.1038).
func TestTraceCodePromptByCodeBudget(t *testing.T) {
	t0 := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	for _, fx := range []struct {
		name               string
		spans, errs, logs  int
		ceil               bool
		wantWindowsDefault int
	}{
		{"küçük gerçekçi (12 span, 3 log, ~2.4K'lık 3 pencere)", 12, 2, 3, false, 3},
		{"tavan (150 span, 100 log, şema, ~6.1K'lık 3 pencere)", 150, 10, 100, true, 2},
	} {
		t.Run(fx.name, func(t *testing.T) {
			s := &Server{tempo: cbTempo(t, t0, fx.spans, fx.errs, fx.ceil), logs: &cbLogStore{recs: cbLogs(t0, fx.logs, fx.ceil)}}
			if fx.ceil {
				s.schema = cbSchema(t)
			}
			in, err := s.buildTraceExplainInput(context.Background(), cbTrace)
			if err != nil {
				t.Fatal(err)
			}
			if in.Stack == "" {
				t.Fatal("fikstür stack taşımıyor — kod yolu sınanmıyor")
			}
			type result struct {
				cc                        devops.CodeContext
				user, block, code, schema int
				gitCalls, llm, llmOverflw int
			}
			measure := func(budget int) result {
				cc, git := cbCodeContext(t, budget, fx.ceil)
				se := s.buildSchemaEvidence(in.ErrorText, in.DBStatements, mapperBlocks(cc))
				res := result{cc: cc, gitCalls: git, block: cbRunes(cc.PromptBlock()), schema: cbRunes(se.Block)}
				for _, w := range cc.Windows {
					res.code += cbRunes(w.Content)
				}
				res.user = cbRunes(in.User + cc.PromptBlock() + se.Block)
				// LLM çağrı sayısı: normal yol ve taşma yolu (bütçeden bağımsız olmalı).
				for _, overflow := range []bool{false, true} {
					fp := newFakeProvider(t, overflow)
					srv := codeServer(t, fp, newCapRecorder())
					r := httptest.NewRequest(http.MethodPost, "/api/copilot/explain-trace/"+cbTrace, nil)
					if _, err := srv.copilotExplainEvidence(r, copilot.SystemPromptTrace(), copilot.SystemPromptTraceWithCode(), in.User, cc, se); err != nil {
						t.Fatalf("bütçe=%d taşma=%v: %v", budget, overflow, err)
					}
					if overflow {
						res.llmOverflw = len(fp.sent())
					} else {
						res.llm = len(fp.sent())
					}
				}
				return res
			}
			old, def := measure(4000), measure(0)
			// Ham pencere boyları (bütçe tavanında: hiçbiri kırpılmaz).
			raw, _ := cbCodeContext(t, devops.MaxCodeBudgetRunes, fx.ceil)
			for i, w := range raw.Windows {
				t.Logf("ham pencere %d: %s %d-%d = %d rune", i+1, w.Path, w.FromLine, w.ToLine, cbRunes(w.Content))
			}
			sys := cbRunes(copilot.SystemPromptTraceWithCode())
			t.Logf("kanıt (in.User) %d rune · sistem (kodlu) %d rune", cbRunes(in.User), sys)
			for _, x := range []struct {
				label string
				r     result
			}{{"4000  ", old}, {"10.000", def}} {
				t.Logf("bütçe %s: user %d (kanıt %d + kod bloğu %d [kod %d, %d pencere] + şema %d) · toplam+sistem %d · git %d · LLM %d (taşmada %d)",
					x.label, x.r.user, cbRunes(in.User), x.r.block, x.r.code, len(x.r.cc.Windows), x.r.schema, x.r.user+sys, x.r.gitCalls, x.r.llm, x.r.llmOverflw)
			}
			t.Logf("büyüme: +%d rune user prompt", def.user-old.user)

			// (1) Bütçe tutuyor; başlık payı sınırlı.
			if old.code > 4000 || def.code > devops.DefaultCodeBudgetRunes {
				t.Errorf("kod bütçesi aşıldı: 4000→%d, 10.000→%d", old.code, def.code)
			}
			for _, r := range []result{old, def} {
				if r.block-r.code > 1500 {
					t.Errorf("kod bloğu başlıkları %d rune — pencere başına başlık şişmiş", r.block-r.code)
				}
			}
			if len(def.cc.Windows) != fx.wantWindowsDefault || len(old.cc.Windows) >= 3 {
				t.Errorf("pencere: 4000'de %d, 10.000'de %d (istenen %d)", len(old.cc.Windows), len(def.cc.Windows), fx.wantWindowsDefault)
			}
			// (2) Büyüme YALNIZ koddan ve bütçe farkıyla sınırlı (kanıt ve şema aynı).
			if old.schema != def.schema {
				t.Errorf("şema bloğu bütçeyle değişti: %d → %d", old.schema, def.schema)
			}
			if grow := def.user - old.user; grow <= 0 || grow > (devops.DefaultCodeBudgetRunes-4000)+300 {
				t.Errorf("büyüme %d rune — bütçe farkı (6000) + not payı aşıldı", grow)
			}
			// (3) Çağrı sayıları bütçeden bağımsız.
			if old.gitCalls != def.gitCalls || old.llm != 1 || def.llm != 1 || old.llmOverflw != 2 || def.llmOverflw != 2 {
				t.Errorf("çağrı sayısı değişti: git %d/%d, LLM %d/%d, taşmada %d/%d", old.gitCalls, def.gitCalls, old.llm, def.llm, old.llmOverflw, def.llmOverflw)
			}
		})
	}
}

// TestOverflowRetryKeepsHalfOfSentCode — v0.10.1038: taşma yeniden
// denemesi modelin AZ ÖNCE taştığı kodun yarısını gönderir. Varsayılan
// bütçede (10.000) 4.500 rune'luk bir blok, bütçe-yarısı (5.000) kuralıyla
// hiç küçülemiyor ve kodsuz denemeye düşüyordu; artık ≤ 2.250 rune'luk kodla
// yeniden denenir. Minik blok eski kuralla kodsuza düşer
// (TestExplainCodeDropsCodeWhenHalvingCannotShrink).
func TestOverflowRetryKeepsHalfOfSentCode(t *testing.T) {
	win := func(path string) devops.CodeWindow {
		lines := make([]string, 0, 61)
		for i := 100; i <= 160; i++ {
			lines = append(lines, fmt.Sprintf("%d| %s", i, strings.Repeat("x", 31)))
		}
		return devops.CodeWindow{Path: path, Frame: "com.acme.orders.A.run(A.java:130)", Line: 130, FromLine: 100, ToLine: 160, Content: strings.Join(lines, "\n")}
	}
	cc := devops.CodeContext{Repo: "orders-api", Branch: "release", Outcome: devops.CodeOK,
		Budget: devops.DefaultCodeBudgetRunes, Windows: []devops.CodeWindow{win("/A.java"), win("/B.java")}}
	sent := 0
	for _, w := range cc.Windows {
		sent += cbRunes(w.Content)
	}
	if sent < 4400 || sent > 4600 {
		t.Fatalf("fikstür %d rune, ~4.500 olmalı", sent)
	}
	fp := newFakeProvider(t, true) // ilk çağrı taşma 400'ü
	s := codeServer(t, fp, newCapRecorder())
	r := httptest.NewRequest(http.MethodPost, "/api/copilot/explain-trace/"+cbTrace, nil)
	if _, err := s.copilotExplainEvidence(r, copilot.SystemPromptTrace(), copilot.SystemPromptTraceWithCode(), "Trace x", cc, schemaEvidence{}); err != nil {
		t.Fatal(err)
	}
	got := fp.sent()
	if len(got) != 2 {
		t.Fatalf("sağlayıcı çağrısı=%d, istenen 2", len(got))
	}
	retry := got[1]
	// (System prompt bloğun ADINI tırnak içinde anar; kodsuz düşüşün kendisi
	// "…ÇÖZÜLEMEDİ: bağlam taşması" satırıdır.)
	if strings.Contains(retry, "ÇÖZÜLEMEDİ: bağlam taşması") || !strings.Contains(retry, "KOD BAĞLAMI (depo: orders-api") {
		t.Fatalf("yeniden deneme kodsuza düştü — yarıya inen kod gönderilmeliydi:\n%s", retry)
	}
	if !strings.Contains(retry, ">>> 130| ") {
		t.Fatal("yarıya inen pencere hata satırını taşımıyor")
	}
	if !strings.Contains(retry, fmt.Sprintf("gönderilen kod yarıya indirildi (%d karakter)", sent/2)) {
		t.Fatalf("not gerçek yarıyı (%d) söylemiyor:\n%s", sent/2, retry)
	}
	if len(got[1]) >= len(got[0]) {
		t.Fatalf("ikinci prompt kısalmadı: %d → %d", len(got[0]), len(got[1]))
	}
}
