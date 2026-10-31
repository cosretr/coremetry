package logml

import "testing"

// v0.10.1743 — dilim 0: yerleşim yalnız config'ten; varsayılan sre.
func TestPlacement(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		role    string
		r       Roles
		run     bool
		label   string
	}{
		{"off", false, "sre", Roles{Sre: true}, false, "off"},
		{"sre on sre pod", true, "sre", Roles{Sre: true}, true, "sre"},
		{"sre on worker pod", true, "sre", Roles{Worker: true}, false, "sre"},
		{"worker on worker pod", true, "worker", Roles{Worker: true}, true, "worker"},
		{"worker on sre pod", true, "worker", Roles{Sre: true}, false, "worker"},
		{"all mode default", true, "", Roles{Worker: true, Sre: true}, true, "sre"},
		{"unknown role → sre", true, "ml", Roles{Sre: true}, true, "sre"},
	}
	for _, c := range cases {
		run, label := Placement(c.enabled, c.role, c.r)
		if run != c.run || label != c.label {
			t.Errorf("%s: got (%v,%q) want (%v,%q)", c.name, run, label, c.run, c.label)
		}
	}
}

func TestStatsSnapshotP95(t *testing.T) {
	var s stats
	for _, ms := range []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100} {
		s.observeES(ms)
	}
	// idx = floor((n-1)·0.95) = 8 → 90 (alt sıra; tek örnek şişirmez).
	if got := s.snapshot("sre", true, false, true).ESP95Ms; got != 90 {
		t.Fatalf("p95 of 10..100 = %d, want 90", got)
	}
	var e stats
	if got := e.snapshot("sre", false, false, false).ESP95Ms; got != 0 {
		t.Fatalf("empty p95 = %d, want 0", got)
	}
}
