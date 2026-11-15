package ml

// vote_runtime_guard_test.go — v0.10.2269 (operatör kararı, kesin): anomali
// oyları ("Gerçek anomali / Sorun değil", anomaly_verdicts) yalnız karne
// etiketidir; hiçbir çalışma zamanı yolunu (tespit, taban, eşik, bildirim)
// sürmez. Yapısal bekçi: aşağıdaki paketlerin üretim kodu oy okuyucularını
// çağıramaz, SQL'de anomaly_verdicts geçemez. Tek istisna backtest puanlaması
// (Backtest → MLLabels → scoreHits; simüle taban etiketsiz kurulur).
// Problem kararları ("Problem değil", problem_verdicts) bu kuralın konusu
// değildir — ayrı karar.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// voteReaders — anomaly_verdicts okuyan chstore erişicileri.
var voteReaders = map[string]bool{
	"MLLabels":                    true,
	"MLLabelsFromRows":            true,
	"MLScorecard":                 true,
	"ListAnomalyVerdicts":         true,
	"EnrichAnomaliesWithVerdicts": true,
}

// voteReadAllowed — dosya (repo-göreli) → izinli okuyucu. Yalnız puanlama.
var voteReadAllowed = map[string]map[string]bool{
	"internal/ml/backtest.go": {"MLLabels": true},
}

func TestAnomalyVotesDoNotReachRuntime(t *testing.T) {
	roots := []string{"../ml", "../anomaly", "../evaluator", "../notify"}
	allowedSeen := map[string]bool{}
	for _, root := range roots {
		files := 0
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			files++
			rel := "internal/" + filepath.ToSlash(strings.TrimPrefix(p, "../"))
			f, perr := parser.ParseFile(token.NewFileSet(), p, nil, 0) // yorumlar dahil değil
			if perr != nil {
				t.Errorf("%s: %v", rel, perr)
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if voteReaders[x.Sel.Name] {
						if voteReadAllowed[rel][x.Sel.Name] {
							allowedSeen[rel+"|"+x.Sel.Name] = true
							return true
						}
						t.Errorf("%s: %s — anomali oyları çalışma zamanını sürmez (yalnız karne/backtest puanlaması)", rel, x.Sel.Name)
					}
				case *ast.BasicLit:
					if x.Kind == token.STRING && strings.Contains(x.Value, "anomaly_verdicts") {
						t.Errorf("%s: anomaly_verdicts sorgusu — oylar çalışma zamanında okunmaz", rel)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("%s: %v", root, err)
		}
		if files == 0 {
			t.Fatalf("%s: hiç dosya taranmadı — bekçi kör", root)
		}
	}
	// bekçi canlı mı: izinli tek okuma hâlâ yerinde (yoksa liste bayatlamıştır)
	for file, names := range voteReadAllowed {
		for name := range names {
			if !allowedSeen[file+"|"+name] {
				t.Errorf("izin listesi bayat: %s artık %s çağırmıyor — girdiyi sil", file, name)
			}
		}
	}
}
