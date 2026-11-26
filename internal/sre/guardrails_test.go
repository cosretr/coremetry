package sre

// guardrails_test.go — v0.10.1293 — AI SRE Faz 4: yalnız öneri (runbook
// eşleme + geri alma metni) — paket düzeyi korkuluklar.
//
// OPERATÖR KURALI: RECOMMENDATION ONLY — ajan hiçbir şeyi uygulamaz.
//
//   - TestSRENoMutatingCalls: go/ast — internal/sre'nin test-dışı dosyalarında
//     Upsert*|Insert*|Delete*|Mark*|Update*|Set*|Append*|Resolve*|Ack*|Execute*
//     adlı HİÇBİR çağrı yok; asgari, gerekçeli izin listesi aşağıda (kendi
//     kaydı / kendi süreç-içi durumu / SAF chstore yardımcısı). Yeni bir izin =
//     bu tabloya gerekçeli satır. TestOrchestratorReadOnly (orchestrator.go'nun
//     `st.` okuyucuları yalnız Get/Find/List/Count/okuma zenginleştirmesi) bunun
//     dar, dosya-özel kardeşidir — ikisi birlikte kalır.
//   - TestSREImports: net/http, internal/agent (runbook yürütücüsü),
//     internal/api, internal/k8sjobs, internal/srelaunch ve her k8s istemcisi
//     (k8s.io/…, client-go) DOĞRUDAN import edilmez.
//   - TestSRENeverUpsertsProblemsOrPriority: paket + api'nin sre_* uçları
//     problems / öncelik / incident yazıcısı çağırmaz, yazan SQL taşımaz,
//     öncelik ayar anahtarına (problem_priority) dokunmaz.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// mutatingCall — yasak çağrı adı önekleri (dışa açık ya da paket-içi
// büyük harfli).
var mutatingCall = regexp.MustCompile(`^(Upsert|Insert|Delete|Mark|Update|Set|Append|Resolve|Ack|Execute)`)

// mutatingAllow — "dosya:çağrı ifadesi" → gerekçe. ASGARİ tutulur; anahtar
// alıcıyı da taşır (aynı adlı başka bir yazıcı izin almasın).
var mutatingAllow = map[string]string{
	// Sentezleyicinin SAF işaretleyicisi: değişim satırlarına kenar + yön
	// kuralıyla "olası neden" bayrağı koyup KOPYA döndürür; hiçbir yere yazmaz
	// (chstore). Ad "Mark" ile başlıyor, iş okuma.
	"deps.go:chstore.MarkCorrelationCauses":  "SAF chstore yardımcısı (kopya döner, yazım yok)",
	"facts.go:chstore.MarkCorrelationCauses": "SAF chstore yardımcısı (kopya döner, yazım yok)",
	// Ajanın KENDİ süreç-içi ayar anlık görüntüsü (atomic); system_settings'e
	// yalnız admin PUT ucu (SaveSettings → PutSetting) yazar.
	"settings.go:SetSettings": "kendi süreç-içi ayar anlık görüntüsü (atomic)",
	// v0.10.1494 (CoSRE 2d) — Context Pack bayrağının süreç-içi anlık görüntüsü
	// (atomic); system_settings'e yalnız admin PUT (SaveContextPackSettings → PutSetting).
	"contextpack_settings.go:SetContextPackSettings": "kendi süreç-içi paket ayarı anlık görüntüsü (atomic)",
	// Redis koordinasyon anahtarları — ajanın kendi kalp atışı / sayaçları /
	// soğuma kilidi (TTL'li, coremetry:sre:* önekli); telemetri ya da problem
	// durumu değil.
	"drain.go:dr.d.Cache.Set":         "kalp atışı (coremetry:sre:hb:<pod>, 30 s TTL)",
	"enqueuer.go:e.cache.SetNX":       "servis soğuma sahibi (sre:enq:svc, TTL)",
	"enqueuer.go:leader.SetOnAcquire": "lider kilidi geri çağrısı (süreç-içi)",
	// v0.10.2477 — vaka hafızası OKUMASININ önbelleği (sre:similar:v1:<özet>, 10 dk TTL).
	"known.go:c.Set": "benzer vaka okuma önbelleği (sre:similar:v1, 10 dk TTL)",
}

func TestSRENoMutatingCalls(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	scanned, allowedSeen := 0, map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		ast.Inspect(af, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			if name == "" || !mutatingCall.MatchString(name) {
				return true
			}
			key := f + ":" + types.ExprString(call.Fun)
			if _, ok := mutatingAllow[key]; ok {
				allowedSeen[key] = true
				return true
			}
			t.Errorf("%s: %s(…) — AI SRE yalnız öneri üretir; yazan/uygulayan çağrı yok (izin listesi gerekçeli)", fset.Position(call.Pos()), name)
			return true
		})
	}
	if scanned < 15 {
		t.Fatalf("yalnız %d dosya tarandı — tarama bozuk", scanned)
	}
	for k := range mutatingAllow {
		if !allowedSeen[k] {
			t.Errorf("izin listesi bayat: %s artık yok — satırı sil (liste yalnız küçülür)", k)
		}
	}
	// Ajanın TEK kalıcı yazımı kendi kaydı: PutSREInvestigation yalnız queue.go
	// save() içinde.
	puts := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, _ := parser.ParseFile(fset, f, nil, 0)
		ast.Inspect(af, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "PutSREInvestigation" {
				puts++
				if f != "queue.go" {
					t.Errorf("%s: PutSREInvestigation yalnız queue.go save() içinde", fset.Position(sel.Pos()))
				}
			}
			return true
		})
	}
	if puts != 1 {
		t.Errorf("PutSREInvestigation çağrısı %d (beklenen 1: save)", puts)
	}
}

func TestSREImports(t *testing.T) {
	banned := []string{
		"net/http",
		"github.com/cilcenk/coremetry/internal/agent",
		"github.com/cilcenk/coremetry/internal/api",
		"github.com/cilcenk/coremetry/internal/k8sjobs",
		"github.com/cilcenk/coremetry/internal/srelaunch",
	}
	bannedPrefix := []string{"k8s.io/", "sigs.k8s.io/", "github.com/kubernetes/"}
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for _, im := range af.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			for _, b := range banned {
				if path == b || strings.HasPrefix(path, b+"/") {
					t.Errorf("%s: %q — AI SRE bu paketi import etmez (yalnız öneri; yürütücü / HTTP / k8s yok)", f, path)
				}
			}
			for _, b := range bannedPrefix {
				if strings.HasPrefix(path, b) {
					t.Errorf("%s: %q — k8s istemcisi yok", f, path)
				}
			}
			if strings.Contains(path, "client-go") {
				t.Errorf("%s: %q — k8s istemcisi yok", f, path)
			}
		}
	}
	if scanned < 15 {
		t.Fatalf("yalnız %d dosya tarandı — tarama bozuk", scanned)
	}
}

func TestSRENeverUpsertsProblemsOrPriority(t *testing.T) {
	writer := regexp.MustCompile(`(?i)^(upsert|insert|save|put|write|update|set|mark|resolve|ack|acknowledge|delete|append|close|reopen|escalate|assign|snooze|mute)\w*(problem|priority|incident)`)
	sqlWrite := regexp.MustCompile(`(?i)(insert\s+into|alter\s+table|delete\s+from|update)\s+(problems|incidents|incident_problems|root_cause_hypotheses)`)
	files, _ := filepath.Glob("*.go")
	api, _ := filepath.Glob("../api/sre_*.go")
	files = append(files, api...)
	fset := token.NewFileSet()
	scanned, apiScanned := 0, 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		if strings.HasPrefix(f, "../api/") {
			apiScanned++
		}
		ast.Inspect(af, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if writer.MatchString(x.Sel.Name) {
					t.Errorf("%s: %s — AI SRE problem / öncelik / incident yazamaz", fset.Position(x.Pos()), x.Sel.Name)
				}
			case *ast.BasicLit:
				if x.Kind != token.STRING {
					break
				}
				if sqlWrite.MatchString(x.Value) {
					t.Errorf("%s: yazan SQL %s", fset.Position(x.Pos()), x.Value)
				}
				if strings.Contains(x.Value, `"problem_priority"`) || strings.Contains(x.Value, `"problem_escalation"`) {
					t.Errorf("%s: öncelik ayar anahtarı %s — ajan okumaz/yazmaz", fset.Position(x.Pos()), x.Value)
				}
			}
			return true
		})
	}
	if scanned < 15 || apiScanned < 4 {
		t.Fatalf("tarama bozuk: %d dosya, %d api sre_* dosyası", scanned, apiScanned)
	}
}
