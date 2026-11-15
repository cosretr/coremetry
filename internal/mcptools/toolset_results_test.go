package mcptools

// v0.10.1302 — CoSRE: modele göre araç seti, SONUÇ ve ŞEMA metinleri.
//
// Kompakt açıklamalar tek kanal değil: araç sonuçlarının hint / next / not /
// hata metinleri ve şema özellik açıklamaları da küçük modele gider. simple
// varyantta bunlardan biri gizli bir aracı anarsa model sunulmayan araca
// yönelir ("unknown tool" ile tur yakar) — operatörün sık kullandığı ACM
// operasyon akışı (resolve_oracle_operation → search_traces) tam bu yüzden
// çıkmaza giriyordu. Kapılar:
//   - STATİK: mcptools'taki her dizge literali simple tarafıyla basıldığında
//     gizli bir aracı anıyorsa yalnız izinli bağlamdadır (MCP Description /
//     Name, gizli aracın kendi kodu, switch/karşılaştırma/harita anahtarı);
//     ⟦ taşıyan literal yalnız basılan bir yerde durur.
//   - ÇALIŞMA ZAMANI: simple katalog şemaları, işaretli sonuç üreticileri ve
//     resolve_oracle_operation → query_traces zinciri.

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcp"
)

// simpleRenderedProducers — işaretli metni döndüren SAF üreticiler; çıktıları
// handler'da renderToolsetFields / mcp.ToolsetText ile basılır (aşağıdaki
// çalışma-zamanı testleri her birini doğrular).
var simpleRenderedProducers = map[string]bool{
	"oracleOperationResult": true, // hint
	"logHistogramPayload":   true, // next
	"logHistEmptyReasons":   true, // reasons
	"capabilitiesFrom":      true, // Detail / Reason
}

// hiddenOnlyHelpers — yalnız gizli araçların (query_metric / list_metric_labels)
// çağırdığı yardımcılar; metinleri simple varyantta hiç üretilmez.
var hiddenOnlyHelpers = map[string]bool{"mtUnknownLabelErr": true}

func TestSimpleToolsetTextStaticGate(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) < 20 {
		t.Fatalf("mcptools kaynakları okunamadı (%d, %v)", len(files), err)
	}
	fset := token.NewFileSet()
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, isFn := decl.(*ast.FuncDecl)
			hiddenOwner := isFn && (hiddenOnlyHelpers[fn.Name.Name] || declaresHiddenTool(fn))
			rendered := isFn && simpleRenderedProducers[fn.Name.Name]
			scanLiterals(decl, func(lit *ast.BasicLit, stack []ast.Node) {
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return
				}
				scanned++
				pos := fset.Position(lit.Pos())
				if strings.Contains(s, "⟦") && !rendered && !markerRenderedContext(stack) {
					t.Errorf("%s: ⟦ işaretli literal basılmayan bir yerde (ShortDescription / şema \"description\" / mcp.ToolsetText / renderToolsetFields üreticisi değil): %.80q", pos, s)
				}
				if m := hiddenToolNameRe.FindString(mcp.RenderToolsetMarkers(s, true)); m != "" && !hiddenOwner && !hiddenNameAllowed(lit, stack) {
					t.Errorf("%s: simple varyantta model metni gizli aracı (%s) anıyor: %.100q", pos, m, s)
				}
			})
		}
	}
	if scanned < 1000 {
		t.Fatalf("yalnız %d literal tarandı — tarayıcı bozuk", scanned)
	}
}

// declaresHiddenTool — fonksiyon gövdesinde Name: "<gizli araç>" var mı
// (aracın fabrikası; sonuçları simple varyantta hiç üretilmez).
func declaresHiddenTool(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		if kv, ok := n.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Name" {
				if bl, ok := kv.Value.(*ast.BasicLit); ok {
					if v, err := strconv.Unquote(bl.Value); err == nil && simpleHiddenTools[v] {
						found = true
					}
				}
			}
		}
		return !found
	})
	return found
}

func scanLiterals(root ast.Node, visit func(*ast.BasicLit, []ast.Node)) {
	var stack []ast.Node
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			visit(lit, stack)
		}
		stack = append(stack, n)
		return true
	})
}

// kvKeyName — KeyValue anahtarının adı (Ident ya da dizge literali).
func kvKeyName(kv *ast.KeyValueExpr) string {
	switch k := kv.Key.(type) {
	case *ast.Ident:
		return k.Name
	case *ast.BasicLit:
		v, _ := strconv.Unquote(k.Value)
		return v
	}
	return ""
}

// markerRenderedContext — işaretli literalin basıldığı bağlamlar.
func markerRenderedContext(stack []ast.Node) bool {
	for i := len(stack) - 1; i >= 0; i-- {
		switch n := stack[i].(type) {
		case *ast.KeyValueExpr:
			switch kvKeyName(n) {
			case "ShortDescription": // ChatDescriptionFor
				return true
			case "description": // şema: RenderSchemaToolsetMarkers (sohbet spec'i + Register)
				return true
			}
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ToolsetText" {
				return true
			}
		}
	}
	return false
}

// hiddenNameAllowed — gizli araç adının simple modelde görünmediği bağlamlar.
func hiddenNameAllowed(lit *ast.BasicLit, stack []ast.Node) bool {
	if len(stack) == 0 {
		return false
	}
	switch p := stack[len(stack)-1].(type) {
	case *ast.CaseClause, *ast.IndexExpr, *ast.ValueSpec: // switch dalı / harita okuması / const-var adı
		return true
	case *ast.BinaryExpr:
		if p.Op == token.EQL || p.Op == token.NEQ {
			return true
		}
	case *ast.KeyValueExpr:
		if p.Key == lit { // harita anahtarı (simpleHiddenTools gibi), değer değil
			return true
		}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if kv, ok := stack[i].(*ast.KeyValueExpr); ok {
			switch kvKeyName(kv) {
			case "Description", "Name": // dış MCP sözleşmesi (sohbete gitmez) / kimlik
				return true
			}
		}
	}
	return false
}

// Çalışma zamanı: simple katalogun basılmış şemaları gizli aracı anmaz; hiçbir
// varyantta (ve dış MCP tools/list'te) işaret sızmaz.
func TestSimpleCatalogSchemasNeverNameHiddenTools(t *testing.T) {
	for _, tl := range ChatToolListFor(Deps{}, "simple") {
		b, _ := json.Marshal(mcp.RenderSchemaToolsetMarkers(tl.InputSchema, true))
		if m := hiddenToolNameRe.FindString(string(b)); m != "" {
			t.Errorf("%s: simple şema gizli aracı anıyor (%s)", tl.Name, m)
		}
		if strings.ContainsAny(string(b), "⟦⟧") {
			t.Errorf("%s: simple şemada işaret kaldı", tl.Name)
		}
	}
	for _, tl := range ToolList(Deps{}) {
		b, _ := json.Marshal(mcp.RenderSchemaToolsetMarkers(tl.InputSchema, false))
		if strings.ContainsAny(string(b), "⟦⟧") {
			t.Errorf("%s: full şemada işaret kaldı", tl.Name)
		}
	}
}

func TestRegisterSchemasCarryNoMarkers(t *testing.T) {
	srv := mcp.New("coremetry-test", "v0.0.0-test")
	Register(srv, Deps{})
	raw := mcpToolsListRaw(t, srv)
	if strings.ContainsAny(raw, "⟦⟧") {
		t.Fatal("dış MCP tools/list işaret sızdırıyor")
	}
	if !strings.Contains(raw, "as in search_traces") {
		t.Fatal("dış MCP full şema metnini görmeli")
	}
}

// Çalışma zamanı: işaretli sonuç üreticileri simple ctx'te gizli aracı anmaz,
// full ctx'te bugünkü metni verir.
func TestToolResultHintsFollowToolset(t *testing.T) {
	simple := WithToolset(context.Background(), "simple")
	full := WithToolset(context.Background(), "full")
	check := func(what string, render func(ctx context.Context) string, fullWant string) {
		t.Helper()
		if s := render(simple); hiddenToolNameRe.MatchString(s) || strings.ContainsAny(s, "⟦⟧") {
			t.Errorf("%s (simple): %q", what, s)
		}
		if f := render(full); !strings.Contains(f, fullWant) || strings.ContainsAny(f, "⟦⟧") {
			t.Errorf("%s (full) %q beklenirdi: %q", what, fullWant, f)
		}
		if f := render(context.Background()); f != render(full) {
			t.Errorf("%s: işaretsiz ctx full olmalı", what)
		}
	}
	check("log histogram next/reasons", func(ctx context.Context) string {
		body := renderToolsetFields(ctx, logHistogramPayload("svc", "", "elasticsearch", 3600, 60, nil), "next", "reasons")
		b, _ := json.Marshal(body)
		return string(b)
	}, "search_logs")
	check("capabilities", func(ctx context.Context) string {
		tool := toolByName(t, ToolList(Deps{MetricsName: func() string { return "vm" }}), "get_capabilities")
		out, err := tool.Handler(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(out)
		return string(b)
	}, "query_metric /")
	check("query_traces servissiz hata", func(ctx context.Context) string {
		r, err := runQueryTraces(WithAnchor(ctx, cqAnchor), (&cqTraces{}).backends(), cqRaw(t, map[string]any{}))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(r)
		return string(b)
	}, "search_traces")
	check("oracle hint", func(ctx context.Context) string {
		tool := toolByName(t, ToolList(Deps{OracleOps: acmOracle()}), OracleOperationToolName)
		out, err := tool.Handler(ctx, json.RawMessage(`{"name":"`+acmOpName+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(out)
		return string(b)
	}, "search_traces'i KOD BAŞINA")
}

// Uçtan uca (ACM operasyon akışı): simple ctx'te resolve_oracle_operation'ın
// ipucu simple katalogda GÖRÜNEN bir aracı anar ve o araç ipucunun dediği
// argümanla (operation = name, servis yok) fonksiyon kodu süzgeciyle koşar.
func TestOracleHintLeadsToVisibleToolInSimple(t *testing.T) {
	ctx := WithAnchor(WithToolset(context.Background(), "simple"), cqAnchor)
	d := Deps{OracleOps: acmOracle()}
	tool := toolByName(t, ChatToolListFor(d, "simple"), OracleOperationToolName)
	out, err := tool.Handler(ctx, json.RawMessage(`{"name":"`+acmOpName+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	hint, _ := out.(map[string]any)["hint"].(string)
	visible := toolNames(ChatToolListFor(d, "simple"))
	named := ""
	for _, tl := range ToolList(d) {
		if strings.HasPrefix(hint, tl.Name) {
			named = tl.Name
		}
	}
	if named == "" || !visible[named] {
		t.Fatalf("ipucu simple katalogda görünen bir araçla başlamalı (%q): %s", named, hint)
	}
	if named != queryTracesName || !strings.Contains(hint, "operation") || !strings.Contains(hint, "servis gerekmez") {
		t.Fatalf("ipucu query_traces operation=<ad> (servissiz) demeli: %s", hint)
	}
	// İpucunun dediği çağrı: query_traces operation=<name>, servis yok.
	c := &cqTraces{}
	b := c.backends()
	b.oracleOps = d.OracleOps
	r, err := runQueryTraces(ctx, b, cqRaw(t, map[string]any{"operation": out.(map[string]any)["name"]}))
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK || r.Error != nil {
		t.Fatalf("servissiz çevrilmiş operasyon koşmalı: %+v", r)
	}
	if len(c.got) != 1 || c.got[0].Service != "" || len(c.got[0].Filters) != 1 || c.got[0].Filters[0].Key != "FUNCTION_CODE" || c.got[0].Filters[0].Values[0] != "GCWCD01" {
		t.Fatalf("okuma servissiz FUNCTION_CODE süzgeciyle yapılmalı: %+v", c.got)
	}
	raw, _ := json.Marshal(r)
	if ev, ok := ToolResultEvidence(queryTracesName, string(raw)); !ok || !strings.Contains(ev.Href, "GCWCD01") || strings.Contains(ev.Href, "service=") {
		t.Errorf("kanıt href'i servissiz kod süzgeci taşımalı: %+v", ev)
	}
	// Çevrilemeyen ad servissiz kalırsa yine service ister (eski sözleşme).
	r2, _ := runQueryTraces(ctx, (&cqTraces{}).backends(), cqRaw(t, map[string]any{"operation": "GET /x"}))
	if r2.OK || r2.Error == nil || r2.Error.Code != cosreErrBadArgs {
		t.Fatalf("span operasyonu servissiz reddedilmeli: %+v", r2)
	}
}
