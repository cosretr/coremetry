package chstore

import (
	"strings"
	"testing"
)

// v0.10.801 (denetim S2) — olaysız SLO "vacuously" yeşil değil: NoData +
// ipucu; hiç başarısız olmayan / her olay başarısız SLI'lar ipucu taşır.
func TestSLOStatusFrom(t *testing.T) {
	cases := []struct {
		name        string
		total, good uint64
		target      float64
		wantHealthy bool
		wantNoData  bool
		wantHint    string // alt dize; "" = ipucu yok
	}{
		{"olay yok", 0, 0, 0.99, false, true, "Olay yok"},
		{"normal sağlıklı", 10000, 9990, 0.99, true, false, ""},
		{"ihlal", 10000, 9000, 0.99, false, false, ""},
		{"hiç başarısız olmuyor (büyük pencere)", 5000, 5000, 0.99, true, false, "hiç başarısız olmuyor"},
		{"hiç başarısız olmuyor ama küçük pencere → ipucu yok", 200, 200, 0.99, true, false, ""},
		{"her olay başarısız", 300, 0, 0.99, false, false, "ters"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := sloStatusFrom(c.total, c.good, c.target)
			if st.Healthy != c.wantHealthy || st.NoData != c.wantNoData {
				t.Fatalf("healthy=%v noData=%v, istenen %v/%v", st.Healthy, st.NoData, c.wantHealthy, c.wantNoData)
			}
			if c.wantHint == "" && st.Hint != "" {
				t.Errorf("ipucu beklenmiyordu: %q", st.Hint)
			}
			if c.wantHint != "" && !strings.Contains(st.Hint, c.wantHint) {
				t.Errorf("ipucu %q içermeli: %q", c.wantHint, st.Hint)
			}
		})
	}
	// Bütçe matematiği NoData'da bozulmaz (SLI 1.0 → bütçe tam, burn 0).
	st := sloStatusFrom(0, 0, 0.99)
	if st.BudgetRemaining != 1 || st.BurnRate != 0 || st.SLI != 1 {
		t.Errorf("NoData bütçe: %+v", st)
	}
	ok := sloStatusFrom(10000, 9950, 0.99)
	if ok.BurnRate < 0.49 || ok.BurnRate > 0.51 || ok.BudgetRemaining < 0.49 || ok.BudgetRemaining > 0.51 {
		t.Errorf("burn/bütçe: %+v", ok)
	}
}
