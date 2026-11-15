package api

// copilot_code_operation_test.go — v0.10.1330: stack yokken trace explain'in
// kod incelemesi ana operasyonun koduna (Code Search) düşer.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
)

const opTestOp = "LEND_PERSONAL_WEB_APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST"

const opTestCtrlPath = "/src/main/java/com/bank/loan/service/rest/CreditProductInformationController.java"
const opTestImplPath = "/src/main/java/com/bank/loan/manager/CreditProductInformationManagerImpl.java"

const opTestCtrl = `package com.bank.loan.service.rest;

public class CreditProductInformationController {
    @Autowired
    private CreditProductInformationManager creditProductInformationManager;

    @Service(SelfServiceNames.Rest.` + opTestOp + `)
    @RequestMapping(value = "information", method = { RequestMethod.POST })
    public CreditProductInformationOutput getCreditProductInformation(@RequestBody CreditProductInformationInput input) throws ACMException {
        CreditProductInformationOutput output = creditProductInformationManager.getCreditProductInfo(input);
        if (output.getReturnCode() != 0) {
            throw new ExternalSystemException(String.valueOf(output.getReturnCode()), output.getReturnMessage(), "}");
        }
        return output;
    }
}
`

const opTestImpl = `package com.bank.loan.manager;

public class CreditProductInformationManagerImpl implements CreditProductInformationManager {
    @Override
    public CreditProductInformationOutput getCreditProductInfo(CreditProductInformationInput input) {
        return hostClient.call("CRPRDINF", input);
    }
}
`

type opGit struct {
	mu     sync.Mutex
	search int
	items  int
}

func newOpGit(t *testing.T, codeSearch bool) (*opGit, *devops.Service) {
	t.Helper()
	g := &opGit{}
	files := map[string]string{opTestCtrlPath: opTestCtrl, opTestImplPath: opTestImpl}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, q := r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(p, "/_apis/search/codesearchresults"):
			g.mu.Lock()
			g.search++
			g.mu.Unlock()
			// v0.10.1349 — depo YALNIZ opTestOp'un controller'ını taşır: başka
			// bir operasyonun araması ıskalar (aday döngüsü testi).
			var body struct {
				SearchText string `json:"searchText"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !strings.Contains(body.SearchText, `"`+opTestOp+`"`) {
				_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{
				map[string]any{"path": opTestCtrlPath, "repository": map[string]string{"name": "loan-service", "type": "git"},
					"matches": map[string]any{"content": []any{map[string]any{"line": 7, "codeSnippet": "@Service(SelfServiceNames.Rest." + opTestOp + ")"}}}},
			}})
		case strings.HasSuffix(p, "/refs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []map[string]string{{"name": "refs/heads/release"}}})
		case strings.HasSuffix(p, "/items") && q.Get("recursionLevel") == "Full":
			items := []map[string]any{}
			for pth := range files {
				items = append(items, map[string]any{"path": pth, "gitObjectType": "blob"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": items})
		case strings.HasSuffix(p, "/items"):
			g.mu.Lock()
			g.items++
			g.mu.Unlock()
			c, ok := files[q.Get("path")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": c})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"defaultBranch": "refs/heads/release"})
		}
	}))
	t.Cleanup(srv.Close)
	dv := devops.New()
	dv.Configure(devops.Settings{BaseURL: srv.URL, Collection: "DefaultCollection", Project: "Loans",
		PAT: "test-pat", Flavor: devops.FlavorServer, CodeSearch: codeSearch})
	return g, dv
}

func opTestInput() traceExplainInput {
	return traceExplainInput{RootService: "loan-service", MainOperation: opTestOp,
		OtherOperations: []string{"LOAN_A_OTHER_REST", "LOAN_B_OTHER_REST"}, OperationSource: traceOpSourceOracle}
}

func TestWantOperationCode(t *testing.T) {
	noStack := devops.CodeContext{Outcome: devops.CodeNoStack, Reason: "bu kayıtta stacktrace yok"}
	cases := []struct {
		name string
		in   traceExplainInput
		cc   devops.CodeContext
		want bool
	}{
		{"stack yok + geçerli operasyon", traceExplainInput{MainOperation: opTestOp}, noStack, true},
		{"stack var (frame'siz bile)", traceExplainInput{MainOperation: opTestOp, Stack: "java.lang.X: y"}, noStack, false},
		// v0.10.1349 (operatör-bildirimli) — hatasız trace: kesin operasyon
		// yokken de yola girilir (fonksiyon kodu adayları / "bulunamadı").
		{"operasyon yok (hatasız trace)", traceExplainInput{}, noStack, true},
		{"biçimsiz operasyon → aday yolu", traceExplainInput{MainOperation: `x" OR *`}, noStack, true},
		{"stack yolu kod buldu", traceExplainInput{}, devops.CodeContext{Outcome: devops.CodeOK, Windows: []devops.CodeWindow{{Path: "/a.java"}}}, false},
		{"kod entegrasyonu yok", traceExplainInput{MainOperation: opTestOp}, devops.CodeContext{Outcome: devops.CodeUnconfigured}, false},
	}
	for _, c := range cases {
		if got := wantOperationCode(c.in, c.cc); got != c.want {
			t.Errorf("%s: %v, istenen %v", c.name, got, c.want)
		}
	}
}

func TestTraceCodePromptAndKey(t *testing.T) {
	se := schemaEvidence{Block: "\n\nŞEMA"}
	stack := devops.CodeContext{Repo: "r", Windows: []devops.CodeWindow{{Path: "/a.java", FromLine: 1, ToLine: 1, Line: 1, Content: "1| a"}}}
	sys, key := traceCodePromptAndKey(stack, se)
	if sys != copilot.SystemPromptTraceWithCode() || key != stack.PromptBlock()+se.Block {
		t.Error("stack yolu bayt bayt eskisi olmalı")
	}
	none := devops.CodeContext{Outcome: devops.CodeNoStack, Reason: "bu kayıtta stacktrace yok"}
	if sys, key := traceCodePromptAndKey(none, se); sys != copilot.SystemPromptTraceWithCode() || key != se.Block {
		t.Error("stacksiz kodsuz (operasyon yolu koşmadı) anahtarı eskisi olmalı")
	}
	op := devops.CodeContext{Repo: "r", Source: devops.CodeSourceOperation, Windows: []devops.CodeWindow{{Path: "/C.java", FromLine: 1, ToLine: 1, Content: "1| a", Operation: true, Frame: opTestOp}}}
	if sys, _ := traceCodePromptAndKey(op, se); sys != copilot.SystemPromptTraceWithOperationCode() {
		t.Error("operasyon penceresi varken operasyon prompt'u")
	}
	// v0.10.1349 — fonksiyon kodu ADAYI: aday prompt'u; anahtar bloğu taşır
	// (aday başlığı kesin başlıktan farklı → cevaplar karışmaz).
	cand := op
	cand.OperationSource = devops.OperationSourceFunctionCode
	if sys, key := traceCodePromptAndKey(cand, se); sys != copilot.SystemPromptTraceWithOperationCandidate() || !strings.Contains(key, "OLABİLİR") {
		t.Errorf("aday yolu: prompt/anahtar: %q", key)
	}
	_, kExact := traceCodePromptAndKey(op, se)
	_, kCand := traceCodePromptAndKey(cand, se)
	if kExact == kCand {
		t.Error("kesin ve aday anahtarı ayrışmalı")
	}
	noOp := devops.CodeContext{Source: devops.CodeSourceOperation, Outcome: devops.CodeNoStack, Reason: devops.OperationNoneReason}
	if sys, key := traceCodePromptAndKey(noOp, se); sys != copilot.SystemPromptTraceWithCode() || !strings.Contains(key, devops.OperationNoneReason) {
		t.Errorf("aday yok: kodlu prompt + gerekçe anahtarda: %q", key)
	}
	off := devops.CodeContext{Source: devops.CodeSourceOperation, Outcome: devops.CodeNoStack, Reason: devops.OperationSearchOffReason}
	sys, key = traceCodePromptAndKey(off, se)
	if sys != copilot.SystemPromptTraceWithCode() || !strings.Contains(key, devops.OperationSearchOffReason) {
		t.Errorf("arama kapalıyken kodlu prompt + gerekçe anahtarda: %q", key)
	}
}

func TestTraceCodeContextOperationBranch(t *testing.T) {
	ctx := context.Background()

	t.Run("stack var ⇒ operasyon yolu koşmaz", func(t *testing.T) {
		g, dv := newOpGit(t, true)
		s := &Server{devops: dv}
		in := opTestInput()
		in.Stack = "java.lang.IllegalStateException: x\n\tat org.springframework.Foo.bar(Foo.java:1)\n"
		in.StackService = "loan-service"
		cc := s.traceCodeContext(ctx, in)
		if cc.Source == devops.CodeSourceOperation || g.search != 0 {
			t.Fatalf("stack varken operasyon araması koştu: source=%q search=%d", cc.Source, g.search)
		}
	})

	t.Run("Code Search kapalı ⇒ ipucu", func(t *testing.T) {
		g, dv := newOpGit(t, false)
		s := &Server{devops: dv}
		cc := s.traceCodeContext(ctx, opTestInput())
		if cc.Source != devops.CodeSourceOperation || cc.Outcome != devops.CodeNoStack || cc.Reason != devops.OperationSearchOffReason || g.search != 0 {
			t.Fatalf("kapalı: %+v search=%d", cc, g.search)
		}
		p := traceExplainExtra(opTestInput(), cc, true)["code"].(*codeContextPayload)
		if p.Operation != opTestOp || p.OperationLookup != "off" || len(p.Files) != 0 {
			t.Errorf("payload: %+v", p)
		}
	})

	t.Run("isabet ⇒ operasyonlu payload", func(t *testing.T) {
		g, dv := newOpGit(t, true)
		s := &Server{devops: dv}
		in := opTestInput()
		cc := s.traceCodeContext(ctx, in)
		if len(cc.Windows) != 2 || g.search != 1 || g.items != 2 {
			t.Fatalf("pencere=%d arama=%d dosya=%d reason=%q", len(cc.Windows), g.search, g.items, cc.Reason)
		}
		p := traceExplainExtra(in, cc, true)["code"].(*codeContextPayload)
		if p.Operation != opTestOp || p.OperationSource != "oracle" || len(p.OtherOperations) != 2 || p.OperationLookup != "ok" {
			t.Errorf("künye: %+v", p)
		}
		if len(p.Files) != 2 || p.Files[0].Kind != "operation" || p.Files[1].Kind != "callee" || p.Files[0].Line != 0 || p.Files[0].URL == "" {
			t.Errorf("dosyalar: %+v", p.Files)
		}
		b, _ := json.Marshal(p)
		if strings.Contains(string(b), "hostClient") || strings.Contains(string(b), "creditProductInformationManager.get") {
			t.Error("kod GÖVDESİ tarayıcıya gitmemeli")
		}
		pb := cc.PromptBlock()
		if !strings.Contains(pb, "+2 diğer operasyon okunmadı") || strings.Contains(pb, devops.FrameMarker) {
			t.Errorf("prompt bloğu:\n%s", pb)
		}
	})

	t.Run("PAT Reason'a sızmaz", func(t *testing.T) {
		_, dv := newOpGit(t, true)
		cfg := dv.CurrentSettings()
		cfg.BaseURL = "http://u:" + cfg.PAT + "@127.0.0.1:1"
		dv.Configure(cfg)
		cc := (&Server{devops: dv}).traceCodeContext(ctx, opTestInput())
		if cc.Reason == "" || strings.Contains(cc.Reason, "test-pat") {
			t.Fatalf("Reason: %q", cc.Reason)
		}
		p := traceExplainExtra(opTestInput(), cc, true)["code"].(*codeContextPayload)
		if p.OperationLookup != "failed" {
			t.Errorf("lookup=%q", p.OperationLookup)
		}
	})
}

func TestCodeEvidenceAttrsSource(t *testing.T) {
	a := attrMap(codeEvidenceAttrs(devops.CodeContext{Source: devops.CodeSourceOperation, Outcome: devops.CodeOK}))
	if a["coremetry.ai.code.source"].AsString() != "operation" {
		t.Errorf("source attribute: %v", a)
	}
}
