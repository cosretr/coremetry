package api

import (
	"os"
	"strings"
	"testing"
)

// v0.10.813 — eşleşen span varken boş liste = sayaç + bayrak; sıfırda sessiz.
func TestNewEmptyDiagCounts(t *testing.T) {
	before := TracesEmptyMismatch()
	d := newEmptyDiag(uint64(0), false)
	if _, has := d["capMismatch"]; has || TracesEmptyMismatch() != before {
		t.Error("eşleşen 0 → bayrak/sayaç yok")
	}
	d = newEmptyDiag(9, false)
	if _, has := d["matchingCapped"]; has {
		t.Fatal("tavansız sayımda matchingCapped olmamalı")
	}
	if c := newEmptyDiag(10000, true); c["matchingCapped"] != true { // v0.10.852
		t.Fatalf("tavanlı sayım işaretlenmeli: %v", c)
	}
	if d["capMismatch"] != true || d["matchingSpans"] != 9 || TracesEmptyMismatch() != before+1 {
		t.Errorf("eşleşen 9 → capMismatch + sayaç: %+v", d)
	}
	h, err := os.ReadFile("health.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(h), `"traces_empty_mismatch": tracesEmptyMismatch.Load()`) {
		t.Error("/api/health sayacı taşımalı")
	}
	a, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(a), "diag := newEmptyDiag(n, capped)") {
		t.Error("emptyDiag bloğu newEmptyDiag kullanmalı")
	}
}
