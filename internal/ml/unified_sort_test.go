package ml

// v0.10.2083 — /anomalies başlık sıralaması sunucuda (operatör 2026-11-13).
// v0.10.2051 listeyi sunucu sayfalı yaptığında başlık tıklaması kaybolmuştu
// (tablo standardı: sunucu sayfalı tablo istemcide sıralanmaz). Bu testler
// beyaz listeyi, her kolon × yönü ve yönden bağımsız eşitlik bozucuyu çiviler.

import (
	"slices"
	"testing"
)

func TestNormalizeUnifiedSort(t *testing.T) {
	cases := []struct {
		section, col, dir string
		wantCol, wantDir  string
	}{
		{"open", "", "", "", ""},
		{"open", "headline", "asc", "", ""}, // tarayıcıda kurulan cümle: sıralanmaz
		{"open", "score; DROP", "asc", "", ""},
		{"open", "service", "asc", "service", "asc"},
		{"open", "service", "x", "service", "desc"},
		{"open", "status", "desc", "", ""}, // açık bölüm varsayılanı
		{"open", "status", "asc", "status", "asc"},
		{"open", "started", "desc", "started", "desc"},
		{"done", "started", "desc", "", ""}, // biten bölüm varsayılanı
		{"done", "started", "asc", "started", "asc"},
		{"done", "signals", "asc", "signals", "asc"},
		{"done", "status", "desc", "status", "desc"},
	}
	for _, c := range cases {
		gc, gd := NormalizeUnifiedSort(c.section, c.col, c.dir)
		if gc != c.wantCol || gd != c.wantDir {
			t.Errorf("%s %q %q → (%q,%q), istenen (%q,%q)", c.section, c.col, c.dir, gc, gd, c.wantCol, c.wantDir)
		}
	}
}

func sortFixture() []UnifiedRow {
	return []UnifiedRow{
		{Key: "k1", Service: "svc-b", Start: 30, Status: UnifiedWatching, Signals: []string{"log"}},
		{Key: "k2", Service: "svc-a", Start: 10, Status: UnifiedRunning, Signals: []string{"trace", "log"}},
		{Key: "k3", Service: "svc-c", Start: 20, Status: UnifiedRunning, Signals: []string{"metric"}},
		{Key: "k4", Service: "svc-a", Start: 30, Status: UnifiedWatching, Signals: []string{"log"}},
		{Key: "k0", Service: "svc-a", Start: 30, Status: UnifiedWatching, Signals: []string{"log"}},
	}
}

func keysOf(rows []UnifiedRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Key
	}
	return out
}

func TestSortUnifiedColumns(t *testing.T) {
	cases := []struct {
		section, col, dir string
		want              []string
	}{
		// varsayılanlar: açık = durum sonra yeni başlangıç; biten = yeni başlangıç
		{"open", "", "", []string{"k3", "k2", "k0", "k1", "k4"}},
		{"done", "", "", []string{"k0", "k1", "k4", "k3", "k2"}},
		// eşitlikte yönden bağımsız: başlangıç (yeni önce), sonra anahtar
		{"open", "started", "desc", []string{"k0", "k1", "k4", "k3", "k2"}},
		{"open", "started", "asc", []string{"k2", "k3", "k0", "k1", "k4"}},
		{"open", "service", "asc", []string{"k0", "k4", "k2", "k1", "k3"}},
		{"open", "service", "desc", []string{"k3", "k1", "k0", "k4", "k2"}},
		{"open", "signals", "asc", []string{"k2", "k0", "k1", "k4", "k3"}},
		{"open", "signals", "desc", []string{"k3", "k0", "k1", "k4", "k2"}},
		{"open", "status", "asc", []string{"k0", "k1", "k4", "k3", "k2"}},
		{"open", "status", "desc", []string{"k3", "k2", "k0", "k1", "k4"}},
		// tanınmayan kolon → bölüm varsayılanı
		{"open", "headline", "asc", []string{"k3", "k2", "k0", "k1", "k4"}},
	}
	for _, c := range cases {
		rows := sortFixture()
		SortUnified(rows, c.section, c.col, c.dir)
		if got := keysOf(rows); !slices.Equal(got, c.want) {
			t.Errorf("%s %s.%s → %v, istenen %v", c.section, c.col, c.dir, got, c.want)
		}
	}
}

// Girdi sırası sonucu değiştirmemeli (sayfalama deterministik).
func TestSortUnifiedInputOrderIndependent(t *testing.T) {
	for _, col := range append([]string{""}, UnifiedSortCols...) {
		for _, dir := range []string{"asc", "desc"} {
			a := sortFixture()
			b := sortFixture()
			slices.Reverse(b)
			SortUnified(a, "open", col, dir)
			SortUnified(b, "open", col, dir)
			if !slices.Equal(keysOf(a), keysOf(b)) {
				t.Errorf("%s.%s girdi sırasına bağlı: %v vs %v", col, dir, keysOf(a), keysOf(b))
			}
		}
	}
}
