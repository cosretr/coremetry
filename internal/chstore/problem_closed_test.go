package chstore

import "testing"

// TestProblemListClosedWindow — v0.10.2476: Kapanan görünümünün SQL
// parçaları (pencere resolved_at'e, sıralama en son kapanan önce).
func TestProblemListClosedWindow(t *testing.T) {
	if _, ok := problemResolvedSinceConjunct(0); ok {
		t.Error("0 = kısıt yok olmalı")
	}
	if sql, ok := problemResolvedSinceConjunct(1); !ok || sql != "resolved_at IS NOT NULL AND toUnixTimestamp64Nano(resolved_at) >= ?" {
		t.Errorf("conjunct = %q", sql)
	}
	if got := problemListOrder(ProblemFilter{}); got != "started_at DESC" {
		t.Errorf("varsayılan sıra = %q", got)
	}
	if got := problemListOrder(ProblemFilter{OrderByResolved: true}); got != "resolved_at DESC, started_at DESC" {
		t.Errorf("kapanan sırası = %q", got)
	}
}
