package chstore

// v0.10.2013 — anomali tekilleştirme D2: gruplama okuması sınırlı (zaman +
// LIMIT + max_execution_time), FINAL ve kanıt kolonunu okumuyor.

import (
	"strings"
	"testing"
)

func TestMLGroupRowsSQLBounded(t *testing.T) {
	for _, want := range []string{"FROM ml_events FINAL", "last_seen >= ?", "service IN ?", "LIMIT ?", "max_execution_time"} {
		if !strings.Contains(mlGroupRowsSQL, want) {
			t.Errorf("SQL %q içermeli", want)
		}
	}
	if strings.Contains(mlGroupRowsSQL, "evidence") {
		t.Error("gruplama okuması kanıt kolonunu okumamalı")
	}
}
