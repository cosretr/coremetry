package anomaly

// v0.10.2011 — exception fırtınası yerel dedektör kapısından bağımsız koşar.
//
// Belirti (anomaly-reassessment-audit N3, S7): v0.10.1746 `scan()` başına
// `NativeDetectorOn(metric_anomaly)` erken dönüşünü koydu; fırtına kontrolü
// `scan()`'in sonunda çağrıldığından varsayılan kurulumda (hepsi kapalı)
// HİÇ koşmuyordu. 1746'nın operatör kararı ise "exception akışı (fırtına,
// P1, fatal, shared) KALIR" idi. Bu test iki şeyi çiviler: (1) tik planı
// kapı kapalıyken de fırtınayı koşar, (2) `scan()` gövdesi fırtınayı
// çağırmaz (iki kez koşmasın) ve kapıyı kendisi okumaz (kapı tik planında).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestPlanAnomalyTick(t *testing.T) {
	cases := []struct {
		name       string
		nativeOn   bool
		wantNative bool
		wantStorm  bool
	}{
		{"kapı kapalı (varsayılan): yerel tarama yok, fırtına KOŞAR", false, false, true},
		{"kapı açık: ikisi de koşar", true, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := planAnomalyTick(c.nativeOn)
			if p.native != c.wantNative || p.storm != c.wantStorm {
				t.Fatalf("planAnomalyTick(%v) = %+v, istenen native=%v storm=%v", c.nativeOn, p, c.wantNative, c.wantStorm)
			}
		})
	}
}

// TestScanDoesNotOwnStormOrGate — `scan()` fırtınayı çağırmaz ve yerel kapıyı
// okumaz; ikisi de `runIfLeader` → `planAnomalyTick` üzerinden.
func TestScanDoesNotOwnStormOrGate(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "anomaly.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]map[string]bool{"scan": {}, "runIfLeader": {}}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || calls[fn.Name.Name] == nil {
			continue
		}
		seen := calls[fn.Name.Name]
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				seen[sel.Sel.Name] = true
			}
			return true
		})
	}
	if calls["scan"]["checkExceptionStorm"] {
		t.Error("scan() checkExceptionStorm çağırmamalı — fırtına runIfLeader'da, kapıdan bağımsız")
	}
	if calls["scan"]["NativeDetectorOn"] {
		t.Error("scan() yerel kapıyı okumamalı — kapı planAnomalyTick'te")
	}
	if !calls["runIfLeader"]["checkExceptionStorm"] {
		t.Error("runIfLeader checkExceptionStorm çağırmalı")
	}
}
