package api

// cosre_contracts_test.go — v0.10.2108 (CoSRE kalite döngüsü D3,
// docs/cosre/quality-loop-audit.md §3.2–3.3): modelsiz `cosre-contracts`
// kapısı (scripts/cosre-contracts.sh; make evalset-check, gate.sh, ci.yml).
//
// Kapı CI'da koşar ve CI'da model YOK: adım modele ya da ağa ASLA gitmemeli.
// Bu test bunu kaynağından pinler —
//   - betik model/ağ izi taşımaz (-tags evalset replay'i, COREMETRY_EVAL_*
//     uç/model değişkenleri, curl);
//   - betiğin api alt kümesindeki her ad gerçekten var (ölü regex sessizce
//     hiçbir şey koşmazdı) ve model replay'i (TestEvalsetReplay) eşlemez;
//   - o testlerin gövdeleri ve çağırdıkları test yardımcıları (geçişli)
//     sahte model, HTTP sunucusu ya da ağ çağrısı kurmaz;
//   - bütün olarak koşan paketlerin (evalrubric, uistream) test dosyaları da.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const cosreContractsScript = "../../scripts/cosre-contracts.sh"

// cosreContractsForbidden — model / ağ / sunucu kuran her şeyin izi.
var cosreContractsForbidden = []string{
	"newLoopLLM", "newLoopTestServer", "httptest.NewServer", "http.Get(", "http.Post(",
	"http.DefaultClient", "net.Dial", "COREMETRY_EVAL", "copilot.New(", ".aiCall(",
}

// cosreContractsVars — betikteki KEY='değer' / KEY="değer" atamaları.
func cosreContractsVars(t *testing.T) (string, map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(cosreContractsScript)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^([A-Z_]+)=['"]([^'"]*)['"]`).FindAllStringSubmatch(string(raw), -1) {
		vars[m[1]] = m[2]
	}
	return string(raw), vars
}

func TestCosreContractsNeverReachModel(t *testing.T) {
	src, vars := cosreContractsVars(t)
	for _, bad := range []string{"-tags", "COREMETRY_EVAL_BASE_URL=", "COREMETRY_EVAL_MODEL=", "curl ", "wget "} {
		if strings.Contains(src, bad) {
			t.Errorf("betik %q içeriyor — kapı modele/ağa gidebilir", bad)
		}
	}
	runRe, pkgs := vars["API_RUN"], vars["PKGS"]
	if runRe == "" || pkgs == "" {
		t.Fatalf("betikte API_RUN / PKGS yok: %v", vars)
	}
	re := regexp.MustCompile(runRe)
	if re.MatchString("TestEvalsetReplay") {
		t.Fatal("API_RUN model replay'ini (TestEvalsetReplay) eşliyor")
	}

	// Paketin test dosyalarındaki bütün fonksiyonlar (ad → gövde kaynağı).
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*_test.go")
	bodies := map[string]string{}
	calls := map[string][]string{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, raw, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Recv != nil {
				continue
			}
			bodies[fd.Name.Name] = string(raw[fset.Position(fd.Body.Pos()).Offset:fset.Position(fd.Body.End()).Offset])
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					calls[fd.Name.Name] = append(calls[fd.Name.Name], id.Name)
				}
				return true
			})
		}
	}
	var named []string
	for name := range bodies {
		if strings.HasPrefix(name, "Test") && re.MatchString(name) {
			named = append(named, name)
		}
	}
	if want := strings.Count(runRe, "|") + 1; len(named) != want {
		t.Fatalf("API_RUN %d ad, pakette eşleşen %d: %v", want, len(named), named)
	}
	// Geçişli kapanış: test gövdesi + çağırdığı test yardımcıları.
	seen := map[string]bool{}
	var walk func(fn, from string)
	walk = func(fn, from string) {
		if seen[fn] {
			return
		}
		seen[fn] = true
		body := bodies[fn]
		if fn != "TestCosreContractsNeverReachModel" { // bu test yasak listeyi DİZGİ olarak taşır
			for _, bad := range cosreContractsForbidden {
				if strings.Contains(body, bad) {
					t.Errorf("%s (%s üzerinden) %q kullanıyor — modelsiz kapıda olamaz", fn, from, bad)
				}
			}
		}
		for _, id := range calls[fn] {
			if _, ok := bodies[id]; ok && id != fn {
				walk(id, from)
			}
		}
	}
	for _, n := range named {
		walk(n, n)
	}

	// Bütün olarak koşan paketler: bütün test dosyaları.
	for _, pkg := range strings.Fields(pkgs) {
		dir := filepath.Join("..", "..", strings.TrimPrefix(pkg, "./"))
		tf, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
		if len(tf) == 0 {
			t.Errorf("%s: test dosyası yok", pkg)
		}
		for _, f := range tf {
			raw, _ := os.ReadFile(f)
			for _, bad := range cosreContractsForbidden {
				if strings.Contains(string(raw), bad) {
					t.Errorf("%s %q kullanıyor — modelsiz kapıda olamaz", f, bad)
				}
			}
		}
	}
}
