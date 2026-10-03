package chstore

import "testing"

// team_aliases_test.go — v0.9.427 pinleri: LDAP↔telemetri takım adı
// eşlemesi. Türkçe İ katlaması (ToLower'ın combining-dot artığı) ve
// alias çözümü; boş tablo = eski case-insensitive davranış.
func TestTeamAliases(t *testing.T) {
	ta := TeamAliases{Aliases: map[string]string{
		"dijitalsy":          "SY-Dijital Altyapı",
		"orionsy":            "SY-Ortak Servisler",
		"SY-ORTAK SERVİSLER": "SY-Ortak Servisler", // kendi-kendine alias zararsız
	}}

	cases := []struct {
		a, b string
		want bool
	}{
		// Operatörün gerçek senaryoları:
		{"SY-Dijital Altyapı", "dijitalsy", true},
		{"dijitalsy", "SY-DİJİTAL ALTYAPI", true}, // Türkçe İ katlaması
		{"orionsy", "SY-Ortak Servisler", true},
		{"OrionSY", "sy-ortak servisler", true},
		// Alias'sız adlar: normalizasyonlu eşitlik (eski EqualFold kapsanır).
		{"SY-Ödemeler", "sy-ödemeler", true},
		{"  SY-Ödemeler ", "SY-Ödemeler", true},
		// Farklı takımlar eşleşmez.
		{"dijitalsy", "orionsy", false},
		{"SY-Dijital Altyapı", "SY-Ortak Servisler", false},
		// Boş ad asla eşleşmez (boş-boş dahil — filtre semantiği).
		{"", "", false},
		{"", "dijitalsy", false},
	}
	for _, c := range cases {
		if got := ta.TeamEqual(c.a, c.b); got != c.want {
			t.Errorf("TeamEqual(%q, %q) = %v, want %v (canon: %q vs %q)",
				c.a, c.b, got, c.want, ta.CanonTeam(c.a), ta.CanonTeam(c.b))
		}
	}

	// Boş tablo → düz normalize eşitlik.
	var empty TeamAliases
	if !empty.TeamEqual("TeamA", "teama") || empty.TeamEqual("TeamA", "TeamB") {
		t.Errorf("boş tablo davranışı bozuk")
	}
}
