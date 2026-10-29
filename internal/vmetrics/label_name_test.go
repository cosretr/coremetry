package vmetrics

// v0.10.1574 — servise kapsanmış etiket keşfinin etiket adı kapısı: geçersiz
// ad (enjeksiyon, boşluk, rakamla başlama) varsayılan servis etiketine düşer.

import "testing"

func TestValidPromLabelName(t *testing.T) {
	for l, want := range map[string]bool{
		"service_name": true, "app": true, "_x1": true,
		"": false, "1abc": false, "a-b": false, `a="x"`: false, "service.name": false, "a b": false,
	} {
		if got := validPromLabelName(l); got != want {
			t.Errorf("%q → %v, want %v", l, got, want)
		}
	}
}
