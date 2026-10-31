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
	// SourceID — Oracle kaynağı (JSON değil; trace örneği okuması için).
	SourceID string `json:"-"`
}

// fleetTextPrefix — Q6 filo satırının öneki (servis çözülmemiş özne).
const fleetTextPrefix = "Filo · servisi çözülmemiş: "

// OracleFleetFindings — SAF (Q6): çözülmemiş öznelerin (`oracle:<kaynak>`)
// satırları → filo bulguları. Kural OracleFindings ile AYNI (eşik, jenerik
// kod tek başına satır değil); Scope = ScopeFleet → takım satırından ayrı.
func OracleFleetFindings(rows []OracleRow, th Thresholds) []Finding {
	fs := OracleFindings(rows, th)
	for i := range fs {
		fs[i].Scope = ScopeFleet
		fs[i].Kind = "fleet_" + fs[i].Kind
		fs[i].Text = fleetTextPrefix + fs[i].Text
	}
	return fs
}

// OracleRowFor — SAF: bulgunun kaynak satırı (servis + "kod · operasyon" etiketi).
func OracleRowFor(rows []OracleRow, f Finding) (OracleRow, bool) {
	for _, r := range rows {
		if r.Service == f.Service && r.Code+" · "+r.Op == f.Label {
			return r, true
		}
	}
	return OracleRow{}, false
}

// ValidTraceID — SAF: 32 hex, sıfır olmayan W3C trace kimliği.
func ValidTraceID(id string) bool {
	if len(id) != 32 || id == "00000000000000000000000000000000" {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// FirstTraceID — SAF: satırların ilk geçerli trace kimliği (küçük harf); yoksa "".
func FirstTraceID(ids []string) string {
	for _, id := range ids {
		if id = strings.ToLower(strings.TrimSpace(id)); ValidTraceID(id) {
			return id
		}
	}
	return ""
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
