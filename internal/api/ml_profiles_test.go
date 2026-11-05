package api

// v0.10.1816 — servis profilleri listesi: cache anahtarı TÜM girdileri taşır
// (v0.5.187 sınıfı) ve kelepçe anahtardan ÖNCE (sınırsız girdi ayrı cache
// girdisi basmaz).

import "testing"

func TestMLProfilesKeyAndClamp(t *testing.T) {
	keys := map[string]bool{}
	for _, in := range [][4]string{
		{"", "", "0", "50"}, {"orders", "", "0", "50"}, {"orders", "always_on", "0", "50"},
		{"orders", "always_on", "50", "50"}, {"orders", "always_on", "50", "100"}, {"orders:p=x", "", "0", "50"},
	} {
		q, p, o, l := parseMLProfilesQuery(in[0], in[1], in[2], in[3])
		k := mlProfilesKey(q, p, o, l)
		if keys[k] {
			t.Fatalf("anahtar çakıştı: %s", k)
		}
		keys[k] = true
	}
	q, p, o, l := parseMLProfilesQuery("  x  ", "nonsense", "-5", "100000")
	if q != "x" || p != "" || o != 0 || l != 50 {
		t.Fatalf("kelepçe: %q %q %d %d", q, p, o, l)
	}
	if _, p, _, _ := parseMLProfilesQuery("", "unknown", "", ""); p != "unknown" {
		t.Fatal("bilinmiyor süzgeci geçerli")
	}
}
