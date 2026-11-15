package api

// copilot_code_candidates_test.go — v0.10.1349 (operatör-bildirimli): HATASIZ
// bir trace'te "Kodu da incele" Code Search açıkken bile "⚠ Kod okunamadı —
// kodsuz analiz. (bu kayıtta stacktrace yok)" diyordu. Kök neden: ana
// operasyon yalnız Oracle HATA satırlarından ve span operation.code
// attribute'undan okunuyordu (ACM span'leri FUNCTION_CODE taşır) ve
// wantOperationCode geçerli bir MainOperation şart koşuyordu. Düzeltme: (c) log
// tier'ı + fonksiyon kodu sözlüğünden ADAY operasyonlar.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestLogOperationCode(t *testing.T) {
	const op = "LOAN_CONSUMER_APP_APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST"
	cases := []struct {
		name  string
		attrs map[string]string
		want  string
	}{
		{"operation_code", map[string]string{"operation_code": op}, op},
		{"OPERATION_CODE", map[string]string{"OPERATION_CODE": op}, op},
		{"nokta + önek (ES labels.)", map[string]string{"labels.operation.code": op}, op},
		{"camelCase operationCode", map[string]string{"operationCode": op}, op},
		{"tire", map[string]string{"acm-operation-name": op}, op},
		{"operation.name geçerli kod", map[string]string{"operation.name": op}, op},
		{"serbest operation.name elenir", map[string]string{"operation.name": "GET /application/x"}, ""},
		{"kısa değer elenir", map[string]string{"operation_code": "AB"}, ""},
		{"küçük harfli değer elenir", map[string]string{"operation_code": "loan_inq_rest"}, ""},
		{"anahtar sonu tutmuyor", map[string]string{"operation_code_v2": op, "operationcodes": op}, ""},
		{"CODE anahtarı NAME'den önce", map[string]string{"a.operation_name": "LOAN_NAME_ONLY_REST", "z.operation_code": op}, op},
		{"boşluk kırpılır", map[string]string{"operation_code": "  " + op + " "}, op},
		{"nil", nil, ""},
	}
	for _, c := range cases {
		if got := logOperationCode(c.attrs); got != c.want {
			t.Errorf("%s: %q, istenen %q", c.name, got, c.want)
		}
	}
}

// pickMainOperation (c) log tier'ı: yalnız Oracle + span boşken; en çok kayıt
// → en erken → ad. (a)/(b) aynen önde (TestPickMainOperation bayt bayt).
func TestPickMainOperationLogTier(t *testing.T) {
	const (
		trx = "LOAN_CONSUMER_APP_APPLICATIONTRX_EXPRESS_ORDER_PRE_VALIDATE_REST"
		inq = "LOAN_CONSUMER_APP_APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST"
	)
	lg := func(ts int64, attrs map[string]string) *logstore.LogRecord {
		return &logstore.LogRecord{Timestamp: ts, Attributes: attrs}
	}
	spans := []chstore.SpanRow{{SpanID: "r", Kind: "server", ServiceName: "acmweb", StartTime: 1, Attributes: map[string]string{"FUNCTION_CODE": "LCA01"}}}
	logs := []*logstore.LogRecord{
		lg(30, map[string]string{"operation_code": inq}),
		lg(10, map[string]string{"operation_code": trx}),
		lg(20, map[string]string{"OPERATION_CODE": inq}),
		lg(5, map[string]string{"msg": "x"}),
		nil,
	}
	main, others, src := pickMainOperation(nil, spans, logs)
	if main != inq || src != traceOpSourceLog || !reflect.DeepEqual(others, []string{trx}) {
		t.Fatalf("log tier: (%q, %v, %q)", main, others, src)
	}
	// Eşit sayıda en erken kazanır.
	main, _, _ = pickMainOperation(nil, nil, []*logstore.LogRecord{lg(30, map[string]string{"operation_code": inq}), lg(10, map[string]string{"operation_code": trx})})
	if main != trx {
		t.Errorf("eşitlikte en erken: %q", main)
	}
	// Span attribute'u (b) log'dan önce gelir.
	withSpan := []chstore.SpanRow{{SpanID: "r", Kind: "server", StartTime: 1, Attributes: map[string]string{"operation.code": trx}}}
	if main, _, src := pickMainOperation(nil, withSpan, logs); main != trx || src != traceOpSourceSpan {
		t.Errorf("span tier önde olmalı: (%q, %q)", main, src)
	}
	// Oracle satırı (a) hepsinden önce.
	if main, _, src := pickMainOperation([]chstore.OracleErrorRow{{OperationCode: trx}}, spans, logs); main != trx || src != traceOpSourceOracle {
		t.Errorf("oracle tier önde olmalı: (%q, %q)", main, src)
	}
	if main, others, src := pickMainOperation(nil, spans, []*logstore.LogRecord{lg(1, map[string]string{"operation.name": "GET /x"})}); main != "" || others != nil || src != "" {
		t.Errorf("geçersiz log değeri: (%q, %v, %q)", main, others, src)
	}
}

func TestTraceFunctionCodes(t *testing.T) {
	sp := func(id, parent, kind string, start int64, attrs map[string]string) chstore.SpanRow {
		return chstore.SpanRow{SpanID: id, ParentSpanID: parent, Kind: kind, StartTime: start, Attributes: attrs}
	}
	spans := []chstore.SpanRow{
		sp("i1", "r", "internal", 2, map[string]string{"FUNCTION_CODE": "INT1"}),
		sp("c1", "r", "client", 3, map[string]string{"function_code": "CLI1"}),
		sp("s2", "r", "server", 5, map[string]string{"FUNCTION_CODE": "SRV2"}),
		sp("s1", "r", "consumer", 4, map[string]string{"FUNCTION_CODE": " SRV1 "}),
		sp("r", "", "server", 9, map[string]string{"FUNCTION_CODE": "ROOT", "function_code": "ROOT"}),
		sp("d", "r", "internal", 6, map[string]string{"FUNCTION_CODE": "SRV1"}),
	}
	if got, want := traceFunctionCodes(spans), []string{"ROOT", "SRV1", "SRV2", "INT1", "CLI1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sıra kök → giriş → diğer: %v, istenen %v", got, want)
	}
	var many []chstore.SpanRow
	for i, c := range []string{"A", "B", "C", "D", "E", "F", "G"} {
		many = append(many, sp(c, "x", "internal", int64(i), map[string]string{"FUNCTION_CODE": c}))
	}
	if got := traceFunctionCodes(many); len(got) != 5 || got[0] != "A" {
		t.Errorf("≤5: %v", got)
	}
	if got := traceFunctionCodes(nil); got != nil {
		t.Errorf("boş: %v", got)
	}
	if got := traceFunctionCodes([]chstore.SpanRow{sp("r", "", "server", 1, map[string]string{"operation.code": "X_Y_Z_OPS"})}); got != nil {
		t.Errorf("fonksiyon kodu yok: %v", got)
	}
}

func TestOperationCandidates(t *testing.T) {
	const (
		a1 = "LOAN_A1_INQ_REST"
		a2 = "LOAN_A2_INQ_REST"
		a3 = "LOAN_A3_INQ_REST"
		b1 = "LOAN_B1_TRX_REST"
		b2 = "LOAN_B2_TRX_REST"
	)
	pair := func(code, op string, n uint64) chstore.OracleFnOp {
		return chstore.OracleFnOp{Code: code, Operation: op, Rows: n}
	}
	pairs := []chstore.OracleFnOp{
		pair("FB", b1, 900), pair("FA", a2, 50), pair("FA", a1, 70), pair("FB", b2, 10),
		pair("FA", a3, 5), pair("FA", "bad op", 1000), pair("FC", a1, 3), pair("FZ", "LOAN_UNRELATED_REST", 9999),
	}
	cases := []struct {
		name      string
		codes     []string
		pairs     []chstore.OracleFnOp
		max       int
		wantTried []string
		wantRest  []string
	}{
		{"kök kodu önce, kodlar arası örgü", []string{"FA", "FB"}, pairs, 2, []string{a1, b1}, []string{a2, b2, a3}},
		{"tek kod: satır sayısına göre", []string{"FA"}, pairs, 2, []string{a1, a2}, []string{a3}},
		{"tekrar eden ad bir kez", []string{"FC", "FA"}, pairs, 2, []string{a1, a2}, []string{a3}},
		{"geçersiz ad atılır, tek aday", []string{"FB"}, []chstore.OracleFnOp{pair("FB", "x y", 9), pair("FB", b1, 1)}, 2, []string{b1}, nil},
		{"sözlükte yok", []string{"FX"}, pairs, 2, nil, nil},
		{"kod yok", nil, pairs, 2, nil, nil},
		{"rest ≤3", []string{"FA", "FB"}, pairs, 1, []string{a1}, []string{b1, a2, b2}},
	}
	for _, c := range cases {
		tried, rest := operationCandidates(c.codes, c.pairs, c.max)
		if !reflect.DeepEqual(tried, c.wantTried) || !reflect.DeepEqual(rest, c.wantRest) {
			t.Errorf("%s: tried=%v rest=%v, istenen %v %v", c.name, tried, rest, c.wantTried, c.wantRest)
		}
	}
}

func TestOperationLookupState(t *testing.T) {
	op := devops.CodeSourceOperation
	cases := []struct {
		name string
		cc   devops.CodeContext
		want string
	}{
		{"stack yolu", devops.CodeContext{Source: "pin", Outcome: devops.CodeNoStack}, ""},
		{"kod okundu", devops.CodeContext{Source: op, Windows: []devops.CodeWindow{{Path: "/a.java"}}}, "ok"},
		{"Code Search kapalı", devops.CodeContext{Source: op, Outcome: devops.CodeNoStack, Reason: devops.OperationSearchOffReason}, "off"},
		{"operasyon adı yok", devops.CodeContext{Source: op, Outcome: devops.CodeNoStack, Reason: devops.OperationNoneReason}, "none"},
		{"arama ıskaladı", devops.CodeContext{Source: op, Outcome: devops.CodeTreeMiss}, "failed"},
		{"depo çözülemedi", devops.CodeContext{Source: op, Outcome: devops.CodeRepoUnresolved}, "failed"},
	}
	for _, c := range cases {
		if got := operationLookupState(c.cc); got != c.want {
			t.Errorf("%s: %q, istenen %q", c.name, got, c.want)
		}
	}
}

// fakeFnDict — fonksiyon kodu sözlüğü test dikişi (çağrı sayar).
type fakeFnDict struct {
	mu    sync.Mutex
	calls int
	codes []string
	pairs []chstore.OracleFnOp
}

func (f *fakeFnDict) OracleOperationsForFunctionCodes(_ context.Context, codes, _ []string, from, to time.Time, perCode int) ([]chstore.OracleFnOp, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.codes = append([]string(nil), codes...)
	if to.Sub(from) != 7*24*time.Hour || perCode <= 0 {
		return nil, context.DeadlineExceeded
	}
	return f.pairs, nil
}

func candidateServer(t *testing.T, dv *devops.Service, dict *fakeFnDict) *Server {
	t.Helper()
	s := &Server{devops: dv}
	fnCodeDictSeams.Store(s, fnCodeDictReader(dict))
	t.Cleanup(func() { fnCodeDictSeams.Delete(s) })
	return s
}

func TestTraceCodeContextCandidateBranch(t *testing.T) {
	ctx := context.Background()
	const candA = "LOAN_CONSUMER_APP_APPLICATIONINQ_NOT_IN_REPO_REST"
	healthy := traceExplainInput{RootService: "loan-service", FunctionCodes: []string{"FC1"}}

	t.Run("(i) boş ana operasyon + sözlük {FC1:[A,B]}, depo yalnız B", func(t *testing.T) {
		g, dv := newOpGit(t, true)
		dict := &fakeFnDict{pairs: []chstore.OracleFnOp{{Code: "FC1", Operation: candA, Rows: 90}, {Code: "FC1", Operation: opTestOp, Rows: 40}}}
		s := candidateServer(t, dv, dict)
		cc := s.traceCodeContext(ctx, healthy)
		if dict.calls != 1 || !reflect.DeepEqual(dict.codes, []string{"FC1"}) {
			t.Fatalf("sözlük çağrısı: %d %v", dict.calls, dict.codes)
		}
		if g.search != 2 || g.items != 2 || len(cc.Windows) != 2 {
			t.Fatalf("arama=%d (2) dosya=%d (2) pencere=%d reason=%q", g.search, g.items, len(cc.Windows), cc.Reason)
		}
		p := traceExplainExtra(healthy, cc, true)["code"].(*codeContextPayload)
		if p.OperationSource != devops.OperationSourceFunctionCode || p.Operation != opTestOp || p.OperationLookup != "ok" ||
			!reflect.DeepEqual(p.OperationCandidates, []string{candA, opTestOp}) || !reflect.DeepEqual(p.OtherOperations, []string{candA}) {
			t.Errorf("payload: %+v", p)
		}
		pb := cc.PromptBlock()
		if !strings.Contains(pb, "trace'in ANA operasyonu OLABİLİR: "+opTestOp) || !strings.Contains(pb, "Operasyon adı bir adaydır") ||
			!strings.Contains(pb, "+1 diğer operasyon okunmadı") {
			t.Errorf("aday bloğu:\n%s", pb)
		}
		if sys, _ := traceCodePromptAndKey(cc, schemaEvidence{}); sys != copilot.SystemPromptTraceWithOperationCandidate() {
			t.Error("aday yolunda aday prompt'u seçilmeli")
		}
		if a := attrMap(codeEvidenceAttrs(cc)); a["coremetry.ai.code.operation_source"].AsString() != "function-code" {
			t.Errorf("span attribute: %v", a["coremetry.ai.code.operation_source"])
		}
	})

	t.Run("(ii) sözlük boş ⇒ operasyon adı bulunamadı, arama yok", func(t *testing.T) {
		g, dv := newOpGit(t, true)
		dict := &fakeFnDict{}
		s := candidateServer(t, dv, dict)
		cc := s.traceCodeContext(ctx, healthy)
		if devops.OperationNoneReason != "bu kayıtta stacktrace yok; trace'in operasyon adı bulunamadı" {
			t.Fatalf("yazım: %q", devops.OperationNoneReason)
		}
		if g.search != 0 || cc.Reason != devops.OperationNoneReason {
			t.Fatalf("search=%d reason=%q", g.search, cc.Reason)
		}
		p := traceExplainExtra(healthy, cc, true)["code"].(*codeContextPayload)
		if p.OperationLookup != "none" || p.Operation != "" || len(p.Files) != 0 {
			t.Errorf("payload: %+v", p)
		}
		// Fonksiyon kodu da yoksa sözlüğe gidilmez.
		dict2 := &fakeFnDict{}
		s2 := candidateServer(t, dv, dict2)
		if cc := s2.traceCodeContext(ctx, traceExplainInput{RootService: "loan-service"}); dict2.calls != 0 || cc.Reason != devops.OperationNoneReason {
			t.Errorf("kodsuz trace: calls=%d reason=%q", dict2.calls, cc.Reason)
		}
	})

	t.Run("(iii) Code Search kapalı ⇒ sözlük ve arama yok", func(t *testing.T) {
		g, dv := newOpGit(t, false)
		dict := &fakeFnDict{pairs: []chstore.OracleFnOp{{Code: "FC1", Operation: opTestOp, Rows: 1}}}
		s := candidateServer(t, dv, dict)
		cc := s.traceCodeContext(ctx, healthy)
		if dict.calls != 0 || g.search != 0 || cc.Reason != devops.OperationSearchOffReason {
			t.Fatalf("calls=%d search=%d reason=%q", dict.calls, g.search, cc.Reason)
		}
		if p := traceExplainExtra(healthy, cc, true)["code"].(*codeContextPayload); p.OperationLookup != "off" {
			t.Errorf("lookup=%q", p.OperationLookup)
		}
	})

	t.Run("(iv) kesin operasyon: yol ve payload v0.10.1330 ile aynı", func(t *testing.T) {
		g, dv := newOpGit(t, true)
		dict := &fakeFnDict{pairs: []chstore.OracleFnOp{{Code: "FC1", Operation: candA, Rows: 1}}}
		s := candidateServer(t, dv, dict)
		in := opTestInput()
		in.FunctionCodes = []string{"FC1"}
		cc := s.traceCodeContext(ctx, in)
		if g.search != 1 || g.items != 2 || dict.calls != 0 {
			t.Fatalf("arama=%d dosya=%d sözlük=%d", g.search, g.items, dict.calls)
		}
		p := traceExplainExtra(in, cc, true)["code"].(*codeContextPayload)
		want := codePayload(cc, true)
		want.Operation, want.OperationSource = in.MainOperation, in.OperationSource
		want.OtherOperations, want.OperationLookup = in.OtherOperations, "ok"
		if !reflect.DeepEqual(p, want) {
			t.Errorf("payload v0.10.1330'dan farklı:\n got %+v\nwant %+v", p, want)
		}
		b, _ := json.Marshal(p)
		if strings.Contains(string(b), "operationCandidates") {
			t.Errorf("kesin yolda aday alanı olmamalı: %s", b)
		}
		pb := cc.PromptBlock()
		if strings.Contains(pb, "OLABİLİR") || strings.Contains(pb, "adaydır") || !strings.Contains(pb, "trace'in ANA operasyonu "+opTestOp+" (stacktrace yok;") {
			t.Errorf("kesin blok değişmiş:\n%s", pb)
		}
		if sys, _ := traceCodePromptAndKey(cc, schemaEvidence{}); sys != copilot.SystemPromptTraceWithOperationCode() {
			t.Error("kesin yolda operasyon kodu prompt'u")
		}
	})
}
