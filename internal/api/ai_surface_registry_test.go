package api

// ai_surface_registry_test.go — v0.10.1153 (operatör, prod: "/ai her CoSRE
// etkileşimini göstermiyor"). Yüzey kaydının TAMLIĞI: sohbet kodunun ai_calls
// satırına yazdığı her yüzey etiketi aisurface kaydında olmalı — yoksa
// kayıttan türeyen okuyucular (evalset eşlemesi, router-gap listesi, KB
// dışlaması, /ai etkileşim gruplaması) onu yine sessizce kaçırır
// (wiki-chat / wiki-select / rag-chat'in başına gelen tam buydu).
//
// Canlı CH / model yok: go/ast ile kaynak taraması.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
)

// chatSurfaceFiles — copilotChat'in kademe zinciri (yeni chat_*.go dosyası
// kendiliğinden kapsanır).
func chatSurfaceFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("chat_*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "copilot_chat.go", "copilot_guided.go", "copilot_intent.go", "copilot_drawer.go",
		"rag.go", "trace_explain_unified.go")
	var out []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// stringConsts — dosyalardaki string sabitleri (ad → değer).
func stringConsts(t *testing.T, fset *token.FileSet, paths []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range paths {
		af, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			gd, ok := n.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				return true
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if bl, ok := vs.Values[i].(*ast.BasicLit); ok && bl.Kind == token.STRING {
							if v, err := strconv.Unquote(bl.Value); err == nil {
								out[name.Name] = v
							}
						}
					}
				}
			}
			return true
		})
	}
	return out
}

// surfaceSite — kaynakta bir yüzey etiketinin yazıldığı yer.
type surfaceSite struct {
	pos  string
	expr ast.Expr
}

// surfaceArgCalls — yüzeyi N. argümanda alan sarmalayıcılar.
var surfaceArgCalls = map[string]int{
	"copilotStreamSurface":      1,
	"copilotExplainSurface":     1,
	"copilotExplainJSONSurface": 1,
	"wikiBudget":                2,
}

func collectSurfaceSites(t *testing.T, fset *token.FileSet, paths []string) []surfaceSite {
	t.Helper()
	var sites []surfaceSite
	for _, p := range paths {
		af, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				name := ""
				switch fn := x.Fun.(type) {
				case *ast.SelectorExpr:
					name = fn.Sel.Name
				case *ast.Ident:
					name = fn.Name
				}
				if i, ok := surfaceArgCalls[name]; ok && i < len(x.Args) {
					sites = append(sites, surfaceSite{fset.Position(x.Pos()).String(), x.Args[i]})
				}
			case *ast.KeyValueExpr:
				// copilot.CallMeta{Surface: …}
				if k, ok := x.Key.(*ast.Ident); ok && k.Name == "Surface" {
					sites = append(sites, surfaceSite{fset.Position(x.Pos()).String(), x.Value})
				}
			case *ast.AssignStmt:
				// m.Surface = …
				for i, lhs := range x.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Surface" && i < len(x.Rhs) {
						sites = append(sites, surfaceSite{fset.Position(x.Pos()).String(), x.Rhs[i]})
					}
				}
			}
			return true
		})
	}
	return sites
}

func TestChatSurfacesRegistered(t *testing.T) {
	fset := token.NewFileSet()
	files := chatSurfaceFiles(t)
	local := stringConsts(t, fset, append(append([]string(nil), files...), "chat_exception_followup.go"))
	reg := stringConsts(t, fset, []string{filepath.Join("..", "ai", "aisurface", "aisurface.go")})
	used := map[string]bool{}
	for _, s := range collectSurfaceSites(t, fset, files) {
		var label string
		switch e := s.expr.(type) {
		case *ast.BasicLit:
			if e.Kind != token.STRING {
				continue
			}
			label, _ = strconv.Unquote(e.Value)
		case *ast.Ident:
			v, ok := local[e.Name]
			if !ok {
				continue // değişken (çağıranın parametresi) — kaynağı ayrı bir sitede
			}
			label = v
		case *ast.SelectorExpr:
			if x, ok := e.X.(*ast.Ident); ok && x.Name == "aisurface" {
				v, ok := reg[e.Sel.Name]
				if !ok {
					t.Errorf("%s: aisurface.%s bir string sabiti değil", s.pos, e.Sel.Name)
					continue
				}
				label = v
			} else {
				continue
			}
		default:
			continue
		}
		used[label] = true
		if _, ok := aisurface.Lookup(label); !ok {
			t.Errorf("%s: sohbet yüzeyi %q aisurface kaydında YOK — kayda Rol/Eval kararıyla ekle "+
				"(yoksa /ai gruplaması, evalset ve router-gap listeleri onu sessizce kaçırır)", s.pos, label)
		}
	}
	// Ters yön: kayıtta olup sohbet kodunda hiç yazılmayan etiket ölü kayıttır.
	for _, sp := range aisurface.All() {
		if !used[sp.Label] {
			t.Errorf("kayıttaki %q sohbet kodunda hiç kullanılmıyor — kaydı güncelle", sp.Label)
		}
	}
}

// copilotChat'in kademe adları (cspan.tier("…")) etkileşim etiketinin
// kademeleri: kayıtsız ad "other"a düşer ve /ai'da kademe kaybolur.
func TestChatTierNamesRegistered(t *testing.T) {
	src, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "copilot_chat.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	ast.Inspect(af, func(nd ast.Node) bool {
		c, ok := nd.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := c.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "tier" || len(c.Args) == 0 {
			return true
		}
		bl, ok := c.Args[0].(*ast.BasicLit)
		if !ok {
			t.Errorf("%s: kademe adı literal değil", fset.Position(c.Pos()))
			return true
		}
		v, _ := strconv.Unquote(bl.Value)
		n++
		if !aisurface.KnownTier(v) {
			t.Errorf("%s: kademe %q aisurface'te kayıtlı değil", fset.Position(c.Pos()), v)
		}
		return true
	})
	if n < 6 {
		t.Fatalf("yalnız %d kademe bulundu — tarama bozuldu mu?", n)
	}
	// Varsayılan kademe (serbest döngü) beginChatSpan'da.
	if span, _ := os.ReadFile("chat_span.go"); !strings.Contains(string(span), `tierV: "loop"`) || !aisurface.KnownTier("loop") {
		t.Error("varsayılan kademe loop değil")
	}
}
