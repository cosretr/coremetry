package tzdefault

// tzdefault_test.go — v0.10.746 (operatör: "default Europe/Istanbul olsun,
// imaj"). Saf çözücü: boş/UTC → time.UTC'nin kendisi (işaretçi eşitliği —
// api paketindeki eski `== time.UTC` pinleri buna yaslanır), IANA adı →
// o konum, çöp → UTC + hata. Location() env'i bir kez okur; test ortamı
// env'i taşımadığında UTC'dir ("doğruluk bir ayara asılı" — ayarı
// Resolve ile doğrudan ölçüyoruz).

import (
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"boş → UTC", "", "UTC", false},
		{"yalnız boşluk → UTC", "  ", "UTC", false},
		{"UTC adı", "UTC", "UTC", false},
		{"utc küçük harf", "utc", "UTC", false},
		{"İstanbul (imaj varsayılanı)", "Europe/Istanbul", "Europe/Istanbul", false},
		{"kenar boşluğu kırpılır", " Europe/Istanbul ", "Europe/Istanbul", false},
		{"geçersiz ad → UTC + hata", "Not/A_Zone", "UTC", true},
		{"yol kaçışı → UTC + hata", "../etc/passwd", "UTC", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, err := Resolve(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if l.String() != tc.want {
				t.Fatalf("konum=%q, istenen %q", l, tc.want)
			}
			if tc.want == "UTC" && l != time.UTC {
				t.Fatal("UTC sonucu time.UTC'nin kendisi olmalı (işaretçi pini)")
			}
		})
	}
}

// TestIstanbulShiftsClock — operatörün gördüğü hata: 22:30 UTC = 01:30
// ertesi gün İstanbul. Varsayılan İstanbul olunca arka plan açıklayıcı
// da bu saati yazar.
func TestIstanbulShiftsClock(t *testing.T) {
	ist, err := Resolve("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	got := time.Date(2026, 9, 16, 22, 30, 0, 0, time.UTC).In(ist).Format("2006-01-02 15:04")
	if got != "2026-09-17 01:30" {
		t.Fatalf("İstanbul = %q", got)
	}
}

// TestLocationStable — Location() süreç boyunca aynı işaretçiyi döndürür
// ve env yokken UTC'dir (CI/test ortamı COREMETRY_TZ taşımaz).
func TestLocationStable(t *testing.T) {
	a, b := Location(), Location()
	if a != b {
		t.Fatal("Location() kararlı değil")
	}
	if a.String() == "" {
		t.Fatal("boş konum adı")
	}
}
