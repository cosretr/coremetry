package chstore

// v0.10.940 (değerlendirme paneli, K2) — evalset çağrıları /ai'da AYRI
// görünür ve üretim sayılarını ŞİŞİRMEZ. Ayrım yeni kolon değil, yüzey öneki
// ("evalset-"); bu dosya iki şeyi pinler:
//
//  1. Süzgeçli her okuyucunun ürettiği SQL kaynak koşulunu taşır, üretim
//     (ve sıfır değer) evalset'i DIŞLAR, evalset kipi tersine çevirir, bind
//     sayısı arg sayısıyla birebir kalır.
//  2. Envanter: chstore'da ai_calls okuyan her fonksiyon bir KARAR taşır
//     (süzgeçli agregat / kendi yüzey whitelist'i / nokta-geri bildirim).
//     Yeni bir okuyucu karar yazılmadan eklenirse test kırılır — sekiz
//     alt sorgudan birini unutmak tam da K2'nin sessiz bozulma sınıfı.
//
// Canlı CH yok: yalnız saf SQL kurucular ve go/ast.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	wantProdCond    = "NOT startsWith(surface, 'evalset-')"
	wantEvalsetCond = "startsWith(surface, 'evalset-')"
)

func TestAICallsSourceCond(t *testing.T) {
	if AICallEvalsetSurfacePrefix != "evalset-" {
		t.Fatalf("önek %q — CLI koşucusu ve panel \"evalset-\" yazıyor", AICallEvalsetSurfacePrefix)
	}
	cases := []struct {
		src  AICallSource
		want string
	}{
		{AICallSourceProduction, wantProdCond},
		{"", wantProdCond}, // sıfır değer = üretim: unutulmuş parametre şişirmez
		{"all", wantProdCond},
		{"Evalset", wantProdCond},
		{AICallSourceEvalset, wantEvalsetCond},
	}
	for _, c := range cases {
		if got := aiCallsSourceCond(c.src); got != c.want {
			t.Errorf("src=%q: %q, beklenen %q", c.src, got, c.want)
		}
	}
}

// assertSourced — prod SQL üretim koşulunu, evalset SQL yalnız evalset
// koşulunu taşır; ikisi farklıdır.
func assertSourced(t *testing.T, name, prod, eval string) {
	t.Helper()
	if !strings.Contains(prod, "FROM ai_calls") {
		t.Errorf("%s: ai_calls okumuyor mu? %q", name, prod)
	}
	if !strings.Contains(prod, wantProdCond) {
		t.Errorf("%s: üretim SQL'i evalset'i dışlamıyor:\n%s", name, prod)
	}
	if !strings.Contains(eval, wantEvalsetCond) || strings.Contains(eval, "NOT startsWith") {
		t.Errorf("%s: evalset SQL'i yalnız evalset'i seçmiyor:\n%s", name, eval)
	}
	if prod == eval {
		t.Errorf("%s: kaynak SQL'i değiştirmiyor", name)
	}
}

func TestAIStatsQueriesAllSourced(t *testing.T) {
	prod := reflect.ValueOf(aiStatsQueries(AICallSourceProduction))
	eval := reflect.ValueOf(aiStatsQueries(AICallSourceEvalset))
	zero := reflect.ValueOf(aiStatsQueries(""))
	typ := prod.Type()
	if typ.NumField() < 8 {
		t.Fatalf("aiStatsSQL %d alan — ComputeAIStats'in sekiz alt sorgusu burada olmalı", typ.NumField())
	}
	for i := 0; i < typ.NumField(); i++ {
		name := "aiStatsQueries." + typ.Field(i).Name
		p, e := prod.Field(i).String(), eval.Field(i).String()
		assertSourced(t, name, p, e)
		if zero.Field(i).String() != p {
			t.Errorf("%s: sıfır değer üretimle aynı SQL'i üretmeli", name)
		}
		// Arg listesi değişmedi: her alt sorgu tam iki bind (from, to).
		if n := strings.Count(p, "?"); n != 2 {
			t.Errorf("%s: %d bind, beklenen 2", name, n)
		}
	}
}

func TestAICallsTimeseriesSQLSourced(t *testing.T) {
	prod := aiCallsTimeseriesSQL(300, AICallSourceProduction)
	assertSourced(t, "aiCallsTimeseriesSQL", prod, aiCallsTimeseriesSQL(300, AICallSourceEvalset))
	if n := strings.Count(prod, "?"); n != 2 {
		t.Errorf("timeseries: %d bind, beklenen 2", n)
	}
	if !strings.Contains(prod, "INTERVAL 300 second") {
		t.Errorf("kova Sprintf'i bozuldu: %s", prod)
	}
}

func TestAICallsListSQLSourced(t *testing.T) {
	to := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	for _, p := range []ListAICallsParams{
		{To: to, Limit: 100},
		{From: to.Add(-time.Hour), To: to, Limit: 50, Surface: "chat", Provider: "openai", Status: "error"},
		// Üretim görünümünde açıkça evalset yüzeyi istemek: kaynak koşulu
		// yine eklenir, sonuç boş — yüzey süzgeci kaynağı delemez.
		{To: to, Limit: 10, Surface: "evalset-IntentClassify"},
	} {
		for _, cached := range []bool{false, true} {
			prodP, evalP := p, p
			prodP.Source, evalP.Source = AICallSourceProduction, AICallSourceEvalset
			prod, args := aiCallsListSQL(prodP, cached)
			eval, _ := aiCallsListSQL(evalP, cached)
			assertSourced(t, "aiCallsListSQL", prod, eval)
			if zero, _ := aiCallsListSQL(p, cached); zero != prod {
				t.Errorf("sıfır Source üretimle aynı SQL'i üretmeli:\n%s", zero)
			}
			if n := strings.Count(prod, "?"); n != len(args) {
				t.Errorf("%+v cached=%v: %d bind, %d arg", p, cached, n, len(args))
			}
			if args[len(args)-1] != p.Limit {
				t.Errorf("LIMIT son arg olmalı, %v", args[len(args)-1])
			}
		}
	}
}

// Bütçe DAİMA üretim — kaynak parametresi yok, evalset kipi olamaz.
func TestAIBudgetUsageQueriesProductionOnly(t *testing.T) {
	totals, byModel := aiBudgetUsageQueries()
	for name, q := range map[string]string{"totals": totals, "byModel": byModel} {
		if !strings.Contains(q, "FROM ai_calls") || !strings.Contains(q, wantProdCond) {
			t.Errorf("bütçe %s: üretim koşulu yok:\n%s", name, q)
		}
		if n := strings.Count(q, "?"); n != 2 {
			t.Errorf("bütçe %s: %d bind, beklenen 2", name, n)
		}
		if !strings.Contains(q, "max_execution_time") {
			t.Errorf("bütçe %s: max_execution_time düştü", name)
		}
	}
}

// aiCallsReaderDecisions — chstore'da ai_calls OKUYAN her fonksiyonun
// v0.10.940 kararı. Yeni okuyucu = buraya bilinçli bir satır.
//
//	source    — agregat; SQL'i aiCallsWindowWhere/aiCallsSourceCond'dan
//	            kurulur (yukarıdaki testler çıktıyı pinler).
//	whitelist — kendi yüzey whitelist'i var; "evalset-*" eşleşemez.
//	point     — kimlik/exchange ile nokta okuma ya da 👎/KB geri bildirim
//	            listesi (ai_feedback sürer); kaynak ne olursa olsun satır
//	            bulunmalı, süzgeç YOK.
var aiCallsReaderDecisions = map[string]string{
	"aiStatsQueries":                 "source",
	"aiCallsTimeseriesSQL":           "source",
	"aiCallsListSQL":                 "source",
	"aiBudgetUsageQueries":           "source",
	"RouterGaps":                     "whitelist", // surface IN ('chat', 'chat-intent-none')
	"GetAICall":                      "point",
	"aiCallEvalSelect":               "point", // AICallForEvalset (exchange_id)
	"AICallSurfaceByExchange":        "point",
	"AICallSampleByExchange":         "point",
	"ListNegativeFeedbackCalls":      "point",
	"NegativeFeedbackCallByExchange": "point",
	"ListKBCandidates":               "point",
}

var aiCallsReadRe = regexp.MustCompile(`(?i)\b(from|join)\s+ai_calls\b`)

func TestAICallsReaderInventory(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	type reader struct {
		sql    strings.Builder
		idents map[string]bool
	}
	found := map[string]*reader{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, decl := range af.Decls {
			name := ""
			switch d := decl.(type) {
			case *ast.FuncDecl:
				name = d.Name.Name
			case *ast.GenDecl:
				name = "decl@" + fset.Position(d.Pos()).String()
			}
			rd := &reader{idents: map[string]bool{}}
			hit := false
			ast.Inspect(decl, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.BasicLit:
					if x.Kind != token.STRING {
						return true
					}
					v, err := strconv.Unquote(x.Value)
					if err != nil {
						return true
					}
					if aiCallsReadRe.MatchString(v) {
						hit = true
					}
					rd.sql.WriteString(v)
				case *ast.Ident:
					rd.idents[x.Name] = true
				}
				return true
			})
			if hit {
				found[name] = rd
			}
		}
	}
	var names []string
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		dec, ok := aiCallsReaderDecisions[n]
		if !ok {
			t.Errorf("%s ai_calls okuyor ama kararı yok: agregatsa aiCallsWindowWhere/aiCallsSourceCond ile süz "+
				"(evalset üretim sayısını şişirmesin, v0.10.940 K2), nokta okumaysa aiCallsReaderDecisions'a gerekçesiyle ekle", n)
			continue
		}
		rd := found[n]
		switch dec {
		case "source":
			if !rd.idents["aiCallsWindowWhere"] && !rd.idents["aiCallsSourceCond"] {
				t.Errorf("%s: 'source' kararlı ama kaynak kurucusunu çağırmıyor", n)
			}
		case "whitelist":
			if !strings.Contains(rd.sql.String(), "surface IN (") || strings.Contains(rd.sql.String(), AICallEvalsetSurfacePrefix) {
				t.Errorf("%s: yüzey whitelist'i kayboldu — evalset satırları sayılabilir", n)
			}
		}
	}
	for n := range aiCallsReaderDecisions {
		if _, ok := found[n]; !ok {
			t.Errorf("%s artık ai_calls okumuyor (ya da yeniden adlandı) — envanteri güncelle", n)
		}
	}
}
