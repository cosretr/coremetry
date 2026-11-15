package devops

// operation_code_test.go — v0.10.1330: stack yokken trace'in ANA
// operasyonunun kodu (giriş metodu + ilk yönetici çağrısı).

import (
	"context"
	"strings"
	"testing"
	"time"
)

const opFixtureOp = "LOAN_CONSUMER_APP_APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST"

// opControllerFixture — operatörün ekranındaki denetleyiciyi örnekler: çok
// satırlı anotasyon yığını, dizi ilklendiricisi, "}" taşıyan dize, yorumda
// sahte bir çağrı, iç içe bloklar.
const opControllerFixture = `package com.bank.loan.service.rest;

import com.bank.loan.SelfServiceNames;

@RestController
@RequestMapping("application")
public class CreditProductInformationController {

    @Autowired
    private CreditProductInformationManager creditProductInformationManager;

    private OtherManager otherManager;

    @Service(SelfServiceNames.Rest.LOAN_CONSUMER_APP_APPLICATIONINQ_OTHER_REST)
    @RequestMapping(value = "other", method = { RequestMethod.POST })
    public OtherOutput other(@RequestBody OtherInput input) {
        return otherManager.other(input);
    }

    @Service(SelfServiceNames.Rest.LOAN_CONSUMER_APP_APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST)
    @RequestMapping(value = "information", method = { RequestMethod.POST })
    @Operation(summary = "Kredi ürün bilgisi {sorgu}", description = "x")
    public CreditProductInformationOutput getCreditProductInformation(@RequestBody CreditProductInformationInput input) throws ACMException {
        // otherManager.notThis(input) — yorumdaki çağrı sayılmaz
        String marker = "kapanış } dizede";
        CreditProductInformationOutput output = creditProductInformationManager.getCreditProductInfo(input);
        if (output.getReturnCode() != 0) {
            if (output.getReturnMessage() == null) {
                output.setReturnMessage("bilinmiyor");
            }
            throw new ExternalSystemException(String.valueOf(output.getReturnCode()), output.getReturnMessage(),
                SelfServiceNames.Rest.LOAN_CONSUMER_APP_APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST);
        }
        return output;
    }

    public void after() {
        System.out.println("sonraki metot");
    }
}
`

const opManagerImplFixture = `package com.bank.loan.manager;

@Component
public class CreditProductInformationManagerImpl implements CreditProductInformationManager {

    @Autowired
    private HostClient hostClient;

    @Override
    @Transactional(readOnly = true)
    public CreditProductInformationOutput getCreditProductInfo(CreditProductInformationInput input) {
        HostResponse r = hostClient.call("CRPRDINF", input.toHost());
        if (r == null) {
            throw new IllegalStateException("host yanıtı yok");
        }
        return CreditProductInformationOutput.from(r);
    }
}
`

func TestOperationSearchQuery(t *testing.T) {
	if got := OperationSearchQuery(" " + opFixtureOp + " "); got != `"`+opFixtureOp+`" ext:java` {
		t.Fatalf("sorgu: %q", got)
	}
}

func TestValidOperationCode(t *testing.T) {
	for op, want := range map[string]bool{
		opFixtureOp: true, "ABC_12": true, "ABC12": false, "abc_def": false,
		"A B C D E F": false, `X" OR repo:*`: false, strings.Repeat("A", 121): false,
	} {
		if ValidOperationCode(op) != want {
			t.Errorf("ValidOperationCode(%q) != %v", op, want)
		}
	}
}

func TestRankOperationHits(t *testing.T) {
	hits := []ServiceCodeHit{
		{Path: "docs/operations.csv"},
		{Path: "src/main/resources/swagger.yaml"},
		{Path: "src/main/java/com/bank/SelfServiceNames.java", Snippet: "String " + opFixtureOp + " ="},
		{Path: "src/main/java/com/bank/OperationConstants.java"},
		{Path: "src/test/java/com/bank/CreditControllerTest.java", Snippet: "SelfServiceNames.Rest.X"},
		{Path: "src/main/resources/mapper/LoanMapper.xml"},
		{Path: "src/main/java/com/bank/util/AuditHelper.java"},
		{Path: "src/main/java/com/bank/loan/service/rest/BOperationRest.java"},
		{Path: "src/main/java/com/bank/loan/service/rest/AController.java"},
		{Path: "src/main/java/com/bank/loan/web/CreditProductInformationController.java", Snippet: "@Service(SelfServiceNames.Rest." + opFixtureOp + ")"},
	}
	got := RankOperationHits(hits)
	var paths []string
	for _, h := range got {
		paths = append(paths, h.Path)
	}
	want := []string{
		"src/main/java/com/bank/loan/web/CreditProductInformationController.java", // +100 +10
		"src/main/java/com/bank/loan/service/rest/AController.java",               // +10 +5, yol eşitliği
		"src/main/java/com/bank/loan/service/rest/BOperationRest.java",            // +10 +5
		"src/main/java/com/bank/util/AuditHelper.java",                            // 0
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sıra:\n got=%q\nwant=%q", paths, want)
	}
}

func TestAnnotatedMethodWindow(t *testing.T) {
	mw, ok := AnnotatedMethodWindow(opControllerFixture, opFixtureOp, 60)
	if !ok {
		t.Fatal("metot bulunamadı")
	}
	w := mw.Window
	lines := splitSourceLines(opControllerFixture)
	if !strings.HasPrefix(strings.TrimSpace(lines[w.FromLine-1]), "@Service(SelfServiceNames.Rest."+opFixtureOp) {
		t.Errorf("pencere @Service anotasyonundan başlamalı: %d %q", w.FromLine, lines[w.FromLine-1])
	}
	if strings.TrimSpace(lines[w.ToLine-1]) != "}" || !strings.Contains(lines[w.ToLine-2], "return output;") {
		t.Errorf("pencere metodun kapanışında bitmeli (dizedeki } ve iç bloklar sayılmaz): %d", w.ToLine)
	}
	if strings.Contains(w.Content, "sonraki metot") || strings.Contains(w.Content, "OtherOutput") {
		t.Error("komşu metotlar pencereye girmemeli")
	}
	if w.Line != 0 || mw.Truncated() {
		t.Errorf("hata satırı yok, kırpma yok: line=%d trunc=%v", w.Line, mw.Truncated())
	}
	if !strings.Contains(mw.Body, "creditProductInformationManager.getCreditProductInfo(input)") {
		t.Error("gövde çağrıyı taşımalı")
	}
	// Kısa tavan: ilk N satır + Truncated.
	short, ok := AnnotatedMethodWindow(opControllerFixture, opFixtureOp, 5)
	if !ok || short.Window.ToLine-short.Window.FromLine+1 != 5 || !short.Truncated() {
		t.Errorf("tavan uygulanmadı: %+v", short.Window)
	}
	// Sabit yalnız throw içinde (anotasyonda değil) → bulunmaz.
	onlyThrow := strings.Replace(opControllerFixture, "@Service(SelfServiceNames.Rest."+opFixtureOp+")", "@Service(SelfServiceNames.Rest.OTHER_CODE_X)", 1)
	if _, ok := AnnotatedMethodWindow(onlyThrow, opFixtureOp, 60); ok {
		t.Error("anotasyon dışındaki kullanım giriş metodu sayılmamalı")
	}
	// Tek satırlık yazım (anotasyonlar + imza + gövde aynı satırda).
	one := "class A {\n    @Service(SelfServiceNames.Rest." + opFixtureOp + ") @RequestMapping(value = \"information\", method = { RequestMethod.POST }) public X get(@RequestBody Y input) { return m.get(input); }\n}\n"
	if mw, ok := AnnotatedMethodWindow(one, opFixtureOp, 60); !ok || mw.Window.FromLine != 2 || mw.Window.ToLine != 2 {
		t.Errorf("tek satırlık metot: %+v ok=%v", mw.Window, ok)
	}
	// Dizi ilklendiricisi içinde.
	arr := "class A {\n  @Service(value = { SelfServiceNames.Rest.FIRST_CODE, SelfServiceNames.Rest." + opFixtureOp + " })\n  public void f() {\n    a();\n  }\n}\n"
	if mw, ok := AnnotatedMethodWindow(arr, opFixtureOp, 60); !ok || mw.Window.FromLine != 2 || mw.Window.ToLine != 5 {
		t.Errorf("dizi ilklendiricisi: %+v ok=%v", mw.Window, ok)
	}
}

func TestManagerCalleeAndFieldType(t *testing.T) {
	mw, _ := AnnotatedMethodWindow(opControllerFixture, opFixtureOp, 60)
	recv, method, ok := ManagerCallee(mw.Body)
	if !ok || recv != "creditProductInformationManager" || method != "getCreditProductInfo" {
		t.Fatalf("çağrı: %q %q %v (yorumdaki otherManager sayılmamalı)", recv, method, ok)
	}
	if typ, found := FieldType(opControllerFixture, recv); !found || typ != "CreditProductInformationManager" {
		t.Errorf("alan tipi: %q %v", typ, found)
	}
	if typ, found := FieldType("class A { }", "fooManager"); found || typ != "FooManager" {
		t.Errorf("yedek ad: %q %v", typ, found)
	}
	if _, _, ok := ManagerCallee(`String s = "xManager.y(";`); ok {
		t.Error("dizedeki çağrı sayılmamalı")
	}
	if r, m, ok := ManagerCallee(`return this.loanService.apply(x);`); !ok || r != "loanService" || m != "apply" {
		t.Errorf("this. öneki: %q %q", r, m)
	}
}

func TestCalleeMethodWindow(t *testing.T) {
	mw, ok := CalleeMethodWindow(opManagerImplFixture, "getCreditProductInfo", 60)
	if !ok {
		t.Fatal("çağrılan metot bulunamadı")
	}
	lines := splitSourceLines(opManagerImplFixture)
	if strings.TrimSpace(lines[mw.Window.FromLine-1]) != "@Override" {
		t.Errorf("üstteki anotasyonlar dahil olmalı: %q", lines[mw.Window.FromLine-1])
	}
	if !strings.Contains(mw.Window.Content, "hostClient.call") || strings.TrimSpace(lines[mw.Window.ToLine-1]) != "}" {
		t.Errorf("gövde: %s", mw.Window.Content)
	}
	iface := "public interface M {\n    X getCreditProductInfo(Y input);\n}\n"
	if _, ok := CalleeMethodWindow(iface, "getCreditProductInfo", 60); ok {
		t.Error("arayüz bildirimi (gövdesiz) pencere vermemeli")
	}
	caller := "class C {\n  void a() {\n    m.getCreditProductInfo(x);\n  }\n}\n"
	if _, ok := CalleeMethodWindow(caller, "getCreditProductInfo", 60); ok {
		t.Error("çağrı satırı bildirim sayılmamalı")
	}
}

func TestCalleePathCandidates(t *testing.T) {
	paths := []string{
		"/src/main/java/com/bank/loan/manager/CreditProductInformationManager.java",
		"/src/main/java/com/bank/loan/manager/CreditProductInformationManagerImpl.java",
		"/legacy/x/y/z/CreditProductInformationManagerImpl.java",
		"/src/test/java/com/bank/loan/manager/CreditProductInformationManagerImpl.java",
	}
	got := CalleePathCandidates(paths, "CreditProductInformationManager", "/src/main/java/com/bank/loan/service/rest/C.java")
	want := []string{paths[1], paths[2], paths[0]}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("adaylar:\n got=%q\nwant=%q", got, want)
	}
}

func TestPromptBlockOperationWindows(t *testing.T) {
	cc := CodeContext{Repo: "loan-service", Branch: "release", OperationOthers: 15, Windows: []CodeWindow{
		{Path: "/src/C.java", FromLine: 20, ToLine: 34, Content: "20| @Service(x)\n21| public X get() {", Operation: true, Frame: opFixtureOp},
		{Path: "/src/MImpl.java", FromLine: 9, ToLine: 17, Content: "9| @Override", Operation: true, Callee: true, Frame: "CreditProductInformationManagerImpl.getCreditProductInfo"},
	}}
	pb := cc.PromptBlock()
	for _, want := range []string{
		"stacktrace yok — pencereler operasyonun giriş metodu ve ilk çağrısıdır",
		"+15 diğer operasyon okunmadı",
		"operasyon kodu 1/2 — trace'in ANA operasyonu " + opFixtureOp + " (stacktrace yok; bu metot operasyonun giriş noktası, hata satırı işareti yok)",
		"operasyon kodu 2/2 — çağrılan yönetici metodu CreditProductInformationManagerImpl.getCreditProductInfo",
		"Kodu trace ve Oracle hata satırıyla BİRLİKTE oku",
		"operasyon kodunda görünmüyor",
	} {
		if !strings.Contains(pb, want) {
			t.Errorf("blokta yok: %q\n%s", want, pb)
		}
	}
	if strings.Contains(pb, FrameMarker) {
		t.Error("operasyon penceresinde >>> işareti olmamalı")
	}
	if strings.Contains(pb, "Kodu stack'le BİRLİKTE oku") {
		t.Error("stack kapanışı operasyon bloğunda olmamalı")
	}
	// Stack penceresi bloğu bayt bayt eskisi (operasyon alanları sıfır).
	st := CodeContext{Repo: "r", Branch: "b", Windows: []CodeWindow{{Path: "/a.java", FromLine: 1, ToLine: 2, Line: 2, Content: "1| a\n2| b", Frame: "A.a(a.java:2)"}}}
	if !strings.Contains(st.PromptBlock(), FrameMarker+" 2| b") || strings.Contains(st.PromptBlock(), "operasyon") {
		t.Error("stack bloğu değişmiş")
	}
}

// operationFixture — fake TFS: ağaç + iki dosya + Code Search isabetleri.
func operationFixture(t *testing.T, search bool) (*fakeTFS, *Service) {
	t.Helper()
	f := newFakeTFS(t)
	const ctrl = "/src/main/java/com/bank/loan/service/rest/CreditProductInformationController.java"
	const impl = "/src/main/java/com/bank/loan/manager/CreditProductInformationManagerImpl.java"
	f.tree = []string{ctrl, impl, "/src/main/java/com/bank/loan/manager/CreditProductInformationManager.java", "/src/main/java/com/bank/SelfServiceNames.java"}
	f.files[ctrl] = opControllerFixture
	f.files[impl] = opManagerImplFixture
	f.searchHits = []CodeSearchHit{
		{Project: "Payments", Repository: "loan-service", Path: "/src/main/java/com/bank/SelfServiceNames.java", Branch: "release"},
		{Project: "Payments", Repository: "loan-service", Path: ctrl, Branch: "release"},
		{Project: "Payments", Repository: "other-repo", Path: "/x/OtherController.java", Branch: "release"},
	}
	svc := New()
	cfg := f.settings()
	cfg.CodeSearch = search
	svc.Configure(cfg)
	return f, svc
}

// v0.10.1330 — istek bütçesi: TEK arama + en çok İKİ dosya çekimi; sıcak
// tekrar (cache) aramasız ve çekimsiz.
func TestFetchOperationCode_RequestBudget(t *testing.T) {
	f, svc := operationFixture(t, true)
	req := OperationCodeRequest{Repo: "loan-service", Service: "loan-service", Operation: opFixtureOp, Others: 3}
	cc := svc.FetchOperationCode(context.Background(), req)
	if len(cc.Windows) != 2 {
		t.Fatalf("pencere=%d reason=%q", len(cc.Windows), cc.Reason)
	}
	if cc.Source != CodeSourceOperation || cc.Outcome != CodeOK || cc.OperationOthers != 3 {
		t.Errorf("künye: source=%q outcome=%q others=%d reason=%q", cc.Source, cc.Outcome, cc.OperationOthers, cc.Reason)
	}
	e, c := cc.Windows[0], cc.Windows[1]
	if !e.Operation || e.Callee || e.Frame != opFixtureOp || !strings.HasSuffix(e.Path, "CreditProductInformationController.java") {
		t.Errorf("giriş penceresi: %+v", e)
	}
	if !c.Callee || c.Frame != "CreditProductInformationManagerImpl.getCreditProductInfo" {
		t.Errorf("çağrılan penceresi: %+v", c)
	}
	if !strings.Contains(e.WebURL, "line=") || e.Line != 0 {
		t.Errorf("link metodun satırına, Line=0: %q %d", e.WebURL, e.Line)
	}
	f.mu.Lock()
	search, items := f.hits["search"], f.hits["item"]
	f.mu.Unlock()
	if search != 1 || items > 2 {
		t.Fatalf("bütçe: arama=%d (1), dosya=%d (≤2)", search, items)
	}
	cc2 := svc.FetchOperationCode(context.Background(), req)
	f.mu.Lock()
	search2, items2 := f.hits["search"], f.hits["item"]
	f.mu.Unlock()
	if search2 != 1 || items2 != items || len(cc2.Windows) != 2 {
		t.Errorf("sıcak tekrar cache'ten: arama=%d dosya=%d pencere=%d", search2, items2, len(cc2.Windows))
	}
	if svc.operationDeadline() > 15*time.Second {
		t.Errorf("süre tavanı 15 sn'yi aşmamalı: %s", svc.operationDeadline())
	}
}

func TestFetchOperationCode_SearchOffAndNoHits(t *testing.T) {
	f, svc := operationFixture(t, false)
	cc := svc.FetchOperationCode(context.Background(), OperationCodeRequest{Repo: "loan-service", Operation: opFixtureOp})
	if cc.Outcome != CodeNoStack || cc.Reason != OperationSearchOffReason || cc.Source != CodeSourceOperation {
		t.Errorf("Code Search kapalı: %+v", cc)
	}
	if f.countReqs("") != 0 {
		t.Errorf("kapalıyken istek atılmamalı: %v", f.snapshotReqs())
	}
	f2, svc2 := operationFixture(t, true)
	f2.searchHits = []CodeSearchHit{{Project: "Payments", Repository: "loan-service", Path: "/docs/ops.csv"}}
	cc2 := svc2.FetchOperationCode(context.Background(), OperationCodeRequest{Repo: "loan-service", Operation: opFixtureOp})
	if len(cc2.Windows) != 0 || cc2.Outcome != CodeTreeMiss || !strings.Contains(cc2.Reason, "kaynak giriş dosyası değil") {
		t.Errorf("kaynak dışı isabet: %+v", cc2)
	}
	if f2.hits["item"] != 0 {
		t.Error("isabet elendiyse dosya çekilmemeli")
	}
}

// PAT Reason'a asla sızmaz (sunucu Authorization'ı hata gövdesinde yankılasa bile).
func TestFetchOperationCode_PATNeverInReason(t *testing.T) {
	_, svc := operationFixture(t, true)
	cfg := svc.CurrentSettings()
	cfg.BaseURL = "http://u:" + cfg.PAT + "@127.0.0.1:1" // bağlanamaz; URL'de kimlik
	svc.Configure(cfg)
	cc := svc.FetchOperationCode(context.Background(), OperationCodeRequest{Repo: "loan-service", Operation: opFixtureOp})
	if cc.Reason == "" || strings.Contains(cc.Reason, cfg.PAT) {
		t.Fatalf("Reason PAT taşıyor ya da boş: %q", cc.Reason)
	}
}

// ── v0.10.1349 (operatör-bildirimli): fonksiyon kodu ADAYLARI ────────────
// Hatasız trace'te kesin operasyon yok; adaylar sırayla denenir, ilk aramanın
// İSABET ettiği adayda durulur. Bütçe: ≤2 aday, en kötü 2 arama + 2 dosya,
// tüm döngü ≤15 sn.

const (
	opCandMissA = "LOAN_CONSUMER_APP_APPLICATIONINQ_MISSING_A_REST"
	opCandMissB = "LOAN_CONSUMER_APP_APPLICATIONINQ_MISSING_B_REST"
)

// candidateFixture — depo YALNIZ opFixtureOp'un controller'ını taşır.
func candidateFixture(t *testing.T) (*fakeTFS, *Service) {
	t.Helper()
	f, svc := operationFixture(t, true)
	hits := f.searchHits
	f.searchFor = func(text string) []CodeSearchHit {
		if strings.Contains(text, `"`+opFixtureOp+`"`) {
			return hits
		}
		return nil
	}
	return f, svc
}

func TestFetchOperationCodeCandidates_Budget(t *testing.T) {
	req := OperationCodeRequest{Repo: "loan-service", Service: "loan-service", Others: 2}

	t.Run("ilk iki aday ıskalar ⇒ 2 arama, üçüncü denenmez", func(t *testing.T) {
		f, svc := candidateFixture(t)
		start := time.Now()
		cc := svc.FetchOperationCodeCandidates(context.Background(), req, []string{opCandMissA, opCandMissB, opFixtureOp})
		f.mu.Lock()
		search, items := f.hits["search"], f.hits["item"]
		f.mu.Unlock()
		if search != 2 || items != 0 || len(cc.Windows) != 0 {
			t.Fatalf("arama=%d (2) dosya=%d (0) pencere=%d", search, items, len(cc.Windows))
		}
		if cc.Reason != "bu kayıtta stacktrace yok; fonksiyon kodu adayları ("+opCandMissA+", "+opCandMissB+") için depoda controller bulunamadı" {
			t.Errorf("reason: %q", cc.Reason)
		}
		if cc.Source != CodeSourceOperation || cc.OperationSource != OperationSourceFunctionCode || cc.Operation != "" ||
			len(cc.OperationCandidates) != 2 || cc.OperationCandidates[1] != opCandMissB || cc.Outcome != CodeTreeMiss {
			t.Errorf("künye: %+v", cc)
		}
		if el := time.Since(start); el > 15*time.Second {
			t.Errorf("süre: %s", el)
		}
	})

	t.Run("ilk aday isabet ⇒ 1 arama, ≤2 dosya", func(t *testing.T) {
		f, svc := candidateFixture(t)
		cc := svc.FetchOperationCodeCandidates(context.Background(), req, []string{opFixtureOp, opCandMissA})
		f.mu.Lock()
		search, items := f.hits["search"], f.hits["item"]
		f.mu.Unlock()
		if search != 1 || items > 2 || len(cc.Windows) != 2 {
			t.Fatalf("arama=%d (1) dosya=%d (≤2) pencere=%d reason=%q", search, items, len(cc.Windows), cc.Reason)
		}
		if cc.Operation != opFixtureOp || cc.OperationSource != OperationSourceFunctionCode || cc.OperationOthers != 2 {
			t.Errorf("künye: op=%q src=%q others=%d", cc.Operation, cc.OperationSource, cc.OperationOthers)
		}
	})

	t.Run("ikinci aday isabet ⇒ 2 arama + 2 dosya (en kötü hâl)", func(t *testing.T) {
		f, svc := candidateFixture(t)
		cc := svc.FetchOperationCodeCandidates(context.Background(), req, []string{opCandMissA, opFixtureOp})
		f.mu.Lock()
		search, items := f.hits["search"], f.hits["item"]
		f.mu.Unlock()
		if search != 2 || items > 2 || cc.Operation != opFixtureOp || len(cc.Windows) != 2 {
			t.Fatalf("arama=%d dosya=%d op=%q pencere=%d", search, items, cc.Operation, len(cc.Windows))
		}
	})

	t.Run("aday yok / geçersiz ⇒ istek yok", func(t *testing.T) {
		f, svc := candidateFixture(t)
		cc := svc.FetchOperationCodeCandidates(context.Background(), req, []string{"bad op", ""})
		if cc.Reason != OperationNoneReason || cc.Outcome != CodeNoStack || f.countReqs("") != 0 {
			t.Errorf("aday yok: %+v istek=%v", cc, f.snapshotReqs())
		}
	})

	t.Run("Code Search kapalı ⇒ istek yok", func(t *testing.T) {
		f, svc := operationFixture(t, false)
		cc := svc.FetchOperationCodeCandidates(context.Background(), req, []string{opFixtureOp})
		if cc.Reason != OperationSearchOffReason || f.countReqs("") != 0 {
			t.Errorf("kapalı: %+v", cc)
		}
	})
	if New().operationDeadline() > 15*time.Second {
		t.Error("dış tavan 15 sn'yi aşmamalı")
	}
}

func TestPromptBlockOperationCandidateHeader(t *testing.T) {
	win := func() []CodeWindow {
		return []CodeWindow{
			{Path: "/src/C.java", FromLine: 20, ToLine: 34, Content: "20| @Service(x)", Operation: true, Frame: opFixtureOp},
			{Path: "/src/MImpl.java", FromLine: 9, ToLine: 17, Content: "9| @Override", Operation: true, Callee: true, Frame: "M.get"},
		}
	}
	exact := CodeContext{Repo: "loan-service", Branch: "release", OperationOthers: 2, Windows: win()}
	cand := exact
	cand.Windows = win()
	cand.OperationSource = OperationSourceFunctionCode
	pb := cand.PromptBlock()
	for _, want := range []string{
		"operasyon kodu 1/2 — trace'in ANA operasyonu OLABİLİR: " + opFixtureOp + " (fonksiyon kodu sözlüğünden ADAY, kesin değil; stacktrace yok",
		"operasyon kodu 2/2 — çağrılan yönetici metodu M.get",
		"Yalnız bir ADAY operasyonun kodu okundu; +2 diğer operasyon okunmadı.",
		"Operasyon adı bir adaydır — kod trace'le uyuşmuyorsa bunu söyle.",
	} {
		if !strings.Contains(pb, want) {
			t.Errorf("aday bloğunda yok: %q\n%s", want, pb)
		}
	}
	// Diğer kaynaklar (oracle | span | log | "") bayt bayt v0.10.1330.
	base := exact.PromptBlock()
	for _, src := range []string{"oracle", "span", "log"} {
		c := exact
		c.OperationSource = src
		if c.PromptBlock() != base {
			t.Errorf("%s kaynağında blok değişmiş", src)
		}
	}
	if strings.Contains(base, "OLABİLİR") || strings.Contains(base, "adaydır") || !strings.Contains(base, "Yalnız ANA operasyonun kodu okundu; +2 diğer operasyon okunmadı.") {
		t.Errorf("kesin blok:\n%s", base)
	}
}
