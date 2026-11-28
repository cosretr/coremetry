package api

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Karar A2: nokta yalnız açık Problem'den; hata oranı girdisi bile yok.
func TestProblemHealth(t *testing.T) {
	cases := []struct {
		c         chstore.OpenProblemCounts
		want, why string
	}{
		{chstore.OpenProblemCounts{}, "green", ""},
		{chstore.OpenProblemCounts{Info: 1}, "yellow", "1 open info"},
		{chstore.OpenProblemCounts{Warning: 2, Info: 1}, "yellow", "2 open warning"},
		{chstore.OpenProblemCounts{Critical: 1, Warning: 3}, "red", "1 open critical"},
	}
	for _, tc := range cases {
		got, why := problemHealth(tc.c)
		if got != tc.want || why != tc.why {
			t.Errorf("%+v → %q/%q, want %q/%q", tc.c, got, why, tc.want, tc.why)
		}
	}
}
