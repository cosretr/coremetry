package api

import "testing"

func TestIngestSamplingWindow(t *testing.T) {
	for in, want := range map[string]int{"": 300, "x": 300, "-5": 300, "10": 60, "600": 600, "86400": 900} {
		if got := ingestSamplingWindow(in); got != want {
			t.Errorf("ingestSamplingWindow(%q) = %d, want %d", in, got, want)
		}
	}
}
