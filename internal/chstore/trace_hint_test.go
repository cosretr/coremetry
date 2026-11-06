package chstore

// trace_hint_test.go — v0.10.1889 (Log Search 3.6, audit §4): bundle `hint`
// (log zamanı) ile ±1 sa ön-probe. İpucu yalnız HIZ: pencerenin kenarına
// dayanan sınır reddedilir ve eski kademeli yol koşar (sonuç ipucundan
// bağımsız).

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestHintedTraceBound(t *testing.T) {
	hint := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	qLo, qHi := hint.Add(-time.Hour), hint.Add(time.Hour)
	ns := func(t time.Time) int64 { return t.UnixNano() }
	cases := []struct {
		name  string
		start time.Time
		end   time.Time
		ok    bool
	}{
		{"ortada", hint.Add(-time.Minute), hint.Add(time.Second), true},
		{"ilk kovada başlar (taşabilir)", qLo.Add(2 * time.Minute), hint, false},
		{"tam ilk kova sınırında", qLo.Add(5 * time.Minute), hint, false},
		{"pencere sonunu aşar", hint, qHi.Add(time.Minute), false},
		{"bitiş başlangıçtan önce", hint, hint.Add(-time.Second), false},
	}
	for _, c := range cases {
		lo, hi, ok := hintedTraceBound(qLo, qHi, c.start, ns(c.end))
		if ok != c.ok {
			t.Errorf("%s: ok=%v, beklenen %v", c.name, ok, c.ok)
			continue
		}
		if ok && (!lo.Equal(c.start.Add(-5*time.Minute)) || !hi.Equal(c.end.Add(5*time.Minute))) {
			t.Errorf("%s: marj ±5 dk bekleniyordu: %v..%v", c.name, lo, hi)
		}
	}
	if _, _, ok := hintedTraceBound(qLo, qHi, hint, 0); ok {
		t.Error("bitişsiz satır kabul edilmemeli")
	}
}

// Kaynak pini: ön-probe zaman sınırlı + time_bucket iki uçlu; GetTraceNear
// ıskada GetTrace'e (kademeli yol) düşer.
func TestTraceWindowNearBounded(t *testing.T) {
	src, err := os.ReadFile("repo.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	w := strings.Index(s, "func (s *Store) traceWindowNear(")
	body := s[w : w+strings.Index(s[w:], "\n}\n")]
	for _, want := range []string{"time_bucket >= ? AND time_bucket < ?", "max_execution_time = 3", "hintedTraceBound("} {
		if !strings.Contains(body, want) {
			t.Errorf("traceWindowNear %q taşımalı", want)
		}
	}
	g := strings.Index(s, "func (s *Store) GetTraceNear(")
	gb := s[g : g+strings.Index(s[g:], "\n}\n")]
	if !strings.Contains(gb, "return s.GetTrace(ctx, traceID)") {
		t.Error("GetTraceNear ıskada GetTrace'e düşmeli")
	}
}
