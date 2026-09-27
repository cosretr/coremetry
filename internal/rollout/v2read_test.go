package rollout

// v2read_test.go — v0.10.984 — P2.3 v2 → v1 durum eşlemesi ve imaj
// referansı ayrıştırması (v2read.go başlığı).

import "testing"

func TestV2StatusMappingTable(t *testing.T) {
	cases := []struct{ v2, v1 string }{
		{V2StatusProgressing, StatusInProgress},
		{V2StatusSucceeded, StatusCompleted},
		{V2StatusStuck, StatusStalled},
		{V2StatusRolledBack, StatusRolledBack},
		{V2StatusSuperseded, StatusSuperseded},
	}
	for _, c := range cases {
		if got := V2StatusToV1(c.v2); got != c.v1 {
			t.Errorf("V2StatusToV1(%q) = %q, want %q", c.v2, got, c.v1)
		}
		// Ters yön: v1 süzgeci aynı v2 değerini bulmalı (gidiş-dönüş).
		if got := V1StatusToV2(c.v1); got != c.v2 {
			t.Errorf("V1StatusToV2(%q) = %q, want %q", c.v1, got, c.v2)
		}
		// v2 yazımıyla elle gelen süzgeç de aynı satırları bulur.
		if got := V1StatusToV2(c.v2); got != c.v2 {
			t.Errorf("V1StatusToV2(v2 %q) = %q", c.v2, got)
		}
	}
	// v1 sözlüğünün beşi de eşlemenin görüntüsünde (FE STATUSES listesi).
	seen := map[string]bool{}
	for _, c := range cases {
		seen[V2StatusToV1(c.v2)] = true
	}
	for _, v1 := range []string{StatusInProgress, StatusCompleted, StatusRolledBack, StatusSuperseded, StatusStalled} {
		if !seen[v1] {
			t.Errorf("v1 durumu %q hiçbir v2 durumundan üretilmiyor", v1)
		}
	}
	// Sözlük dışı: uydurma yok, aynen geçer.
	if V2StatusToV1("weird") != "weird" || V1StatusToV2("weird") != "weird" || V2StatusToV1("") != "" {
		t.Fatal("sözlük dışı değer aynen geçmeli")
	}
}

func TestSplitImageRef(t *testing.T) {
	cases := []struct{ in, repo, tag string }{
		{"reg.io/team/app:1.2.3", "reg.io/team/app", "1.2.3"},
		{"reg.io:5000/app:2.0", "reg.io:5000/app", "2.0"},
		{"reg.io:5000/app", "reg.io:5000/app", ""},
		{"app", "app", ""},
		{"app:latest", "app", "latest"},
		{"reg.io/app@sha256:abc", "reg.io/app", "sha256:abc"},
		// İnceleme: digest'e sabitlenmiş tag'li referans — tag okunur.
		{"reg.io/team/app:1.4.2@sha256:ab12", "reg.io/team/app", "1.4.2"},
		{"reg.io:5000/app:2.0@sha256:ab12", "reg.io:5000/app", "2.0"},
		{"reg.io:5000/app@sha256:ab12", "reg.io:5000/app", "sha256:ab12"},
		{"app:@sha256:ab12", "app", "sha256:ab12"},
		{"  ", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		r, tg := SplitImageRef(c.in)
		if r != c.repo || tg != c.tag {
			t.Errorf("SplitImageRef(%q) = (%q, %q), want (%q, %q)", c.in, r, tg, c.repo, c.tag)
		}
	}
}

// TestPrimaryImagePair — İnceleme (v0.10.984): sıralı dizinin ilki sidecar
// olabilir ("docker.io/istio/proxyv2" < "harbor.corp/..."); birincil imaj
// DEĞİŞENDEN seçilir, önceki taraf aynı repodan.
func TestPrimaryImagePair(t *testing.T) {
	const istio, istio2 = "docker.io/istio/proxyv2:1.20.1", "docker.io/istio/proxyv2:1.21.0"
	cases := []struct {
		name         string
		cur, prev    []string
		workload     string
		wantC, wantP string
	}{
		{"sidecar önce sıralanır, uygulama değişti", []string{istio, "harbor.corp/pay/api:2.5.0"},
			[]string{istio, "harbor.corp/pay/api:2.4.0"}, "payments", "harbor.corp/pay/api:2.5.0", "harbor.corp/pay/api:2.4.0"},
		{"digest'li tag değişimi", []string{istio, "reg.io/api:2.5@sha256:bb"}, []string{istio, "reg.io/api:2.4@sha256:aa"}, "x",
			"reg.io/api:2.5@sha256:bb", "reg.io/api:2.4@sha256:aa"},
		{"yalnız sidecar değişti → o", []string{istio2, "harbor.corp/pay/api:2.4.0"}, []string{istio, "harbor.corp/pay/api:2.4.0"}, "api",
			istio2, istio},
		{"config: hiçbir şey değişmedi → iş yükü adı", []string{istio, "harbor.corp/pay/api:2.4.0"},
			[]string{istio, "harbor.corp/pay/api:2.4.0"}, "api", "harbor.corp/pay/api:2.4.0", "harbor.corp/pay/api:2.4.0"},
		{"iş yükü adı içerme", []string{istio, "harbor.corp/pay/api:2.4.0"}, nil, "pay-api-v2", "harbor.corp/pay/api:2.4.0", ""},
		{"repo değişimi", []string{istio, "new.reg/api:3"}, []string{istio, "old.reg/api:2"}, "x", "new.reg/api:3", "old.reg/api:2"},
		{"sidecar enjeksiyonu: uygulama adıyla kalır", []string{istio, "harbor.corp/pay/api:2.4.0"},
			[]string{"harbor.corp/pay/api:2.4.0"}, "api", "harbor.corp/pay/api:2.4.0", "harbor.corp/pay/api:2.4.0"},
		{"eşleşme yok → ilk (geri düşüş)", []string{"", " a:1 ", "b:2"}, []string{"a:1", "b:2"}, "zzz", "a:1", "a:1"},
		{"ilk gözlem (önceki yok)", []string{"a:1"}, nil, "", "a:1", ""},
		{"yalnız önceki", nil, []string{"a:1"}, "", "", "a:1"},
		{"boş", nil, nil, "api", "", ""},
	}
	for _, c := range cases {
		gc, gp := PrimaryImagePair(c.cur, c.prev, c.workload)
		if gc != c.wantC || gp != c.wantP {
			t.Errorf("%s: PrimaryImagePair = (%q, %q), want (%q, %q)", c.name, gc, gp, c.wantC, c.wantP)
		}
	}
}
