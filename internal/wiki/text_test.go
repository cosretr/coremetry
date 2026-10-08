package wiki

// v0.10.1122 — Türkçe katlama + teknik terim korumalı jetonlayıcı (SAF).

import (
	"reflect"
	"testing"
)

func TestFoldTurkish(t *testing.T) {
	cases := map[string]string{
		"İSTANBUL":            "istanbul",
		"ıspanak":             "ispanak",
		"Şifre Sıfırlama":     "sifre sifirlama",
		"ÇÖĞÜŞİI":             "cogusii",
		"INFO ID":             "info id", // İngilizce büyük I → i (Türkçe ı DEĞİL)
		"svc-orders":          "svc-orders",
		"Kullanıcı Doğrulama": "kullanici dogrulama",
	}
	for in, want := range cases {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
	// İki yazım aynı biçime iner: klavye farkı aramayı bölmez.
	if Fold("şifre") != Fold("sifre") || Fold("IŞIK") != Fold("ışık") {
		t.Error("Türkçe karakterli ve karaktersiz yazım aynı katlanmalı")
	}
}

func TestTokensKeepsTechnicalCompounds(t *testing.T) {
	got := Tokens("Restart svc-orders after ERR-1042; see orders.v2 and payment_service.")
	want := []string{"restart", "svc", "orders", "svc-orders", "after", "err", "1042", "err-1042",
		"see", "orders", "v2", "orders.v2", "and", "payment", "service", "payment_service"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tokens:\n got %q\nwant %q", got, want)
	}
}

func TestTokensEdgeCases(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a b 7", []string{"7"}},            // tek harf düşer, tek rakam kalır
		{"trailing-", []string{"trailing"}}, // ayraç sonda: bileşik yok
		{"x--y", nil},                       // tek harfler; ayraç çifti bileşik kurmaz
		{"https://wiki.example.test/a", []string{"https", "wiki", "example", "test", "wiki.example.test/a"}},
		{"Şifre-Sıfırlama", []string{"sifre", "sifirlama", "sifre-sifirlama"}},
	}
	for _, c := range cases {
		got := Tokens(c.in)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Tokens(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQueryTermsDropsStopwordsCompoundFirst(t *testing.T) {
	got := QueryTerms("svc-orders nasıl restart edilir? Şifre ve svc-orders")
	want := []string{"svc-orders", "svc", "orders", "restart", "edilir", "sifre"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("QueryTerms = %q, want %q", got, want)
	}
	if len(QueryTerms("nasıl ve ne")) != 0 {
		t.Error("yalnız stopword'lü sorgu terim üretmemeli")
	}
	many := QueryTerms("aa1 bb2 cc3 dd4 ee5 ff6 gg7 hh8 ii9 jj10 kk11 ll12 mm13 nn14")
	if len(many) != queryTermsMax {
		t.Errorf("terim tavanı %d, got %d", queryTermsMax, len(many))
	}
}
