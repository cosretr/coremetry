package briefing

// oracle.go — kalem 5: Oracle hata kodu farkı (briefing-audit "Tasarım
// önerisi" oracle_error_diff; R5 çözülmüş `ora:` grupları, Q6 çözülmemiş
// özneler takım satırına girmez). SAF: okuma api katmanında
// (OracleGroupAggregates ×2 + ListExceptionGroups), burada yalnız kıyas.
//
// Jenerik kod (kaynağın GenericCodes kümesi, oracle.QualifierFor ile aynı
// küme) TEK BAŞINA satır üretmez: yalnız aynı servis × operasyonun jenerik
// OLMAYAN bulgusuna ek bilgi olarak yazılır.

import (
	"fmt"
	"sort"
	"strings"
)

// OracleRow — bir (servis, kod, operasyon) üçlüsünün iki pencere toplamı.
type OracleRow struct {
	Service, Code, Op string
	Generic           bool
	Cur, Prev         uint64
	PageURL           string
}

// OracleFindings — SAF: jenerik olmayan satır Cur ≥ OracleMinCount ve
// (Prev = 0 → "yeni" | Cur/Prev ≥ OracleCodeX) ise bulgu; jenerik satırlar
// aynı servis × operasyonun bulgusuna eklenir, kendi başına bulgu değildir.
func OracleFindings(rows []OracleRow, th Thresholds) []Finding {
	type so struct{ svc, op string }
	generic := map[so][]OracleRow{}
	for _, r := range rows {
		if r.Generic && r.Cur > 0 {
			k := so{r.Service, r.Op}
			generic[k] = append(generic[k], r)
		}
	}
	var out []Finding
	for _, r := range rows {
		if r.Generic || r.Service == "" || r.Cur < th.OracleMinCount {
			continue
		}
		rat := ratio(float64(r.Cur), float64(r.Prev))
		f := Finding{Item: ItemOracle, Kind: "code_up", Service: r.Service, Label: r.Code + " · " + r.Op,
			Now: float64(r.Cur), Prev: float64(r.Prev), Unit: "hata", Score: rat, PageURL: r.PageURL}
		switch {
		case r.Prev == 0:
			f.Kind, f.Score = "code_new", newErrScore
			f.Text = fmt.Sprintf("%s Oracle %s · %s: %d hata (önceki pencerede yoktu)", r.Service, r.Code, r.Op, r.Cur)
		case rat >= th.OracleCodeX:
			f.Text = fmt.Sprintf("%s Oracle %s · %s: %d hata (önceki %d, %s)", r.Service, r.Code, r.Op, r.Cur, r.Prev, xs(rat))
		default:
			continue
		}
		if gs := generic[so{r.Service, r.Op}]; len(gs) > 0 {
			sort.Slice(gs, func(i, j int) bool { return gs[i].Code < gs[j].Code })
			parts := make([]string, 0, len(gs))
			for _, g := range gs {
				parts = append(parts, fmt.Sprintf("%s %d", g.Code, g.Cur))
			}
			f.Text += " · jenerik: " + strings.Join(parts, ", ")
		}
		out = append(out, f)
	}
	return out
}
