package anomaly

// log_pattern_spec_test.go — v0.10.1060 (log deseni anomalisi: zaman içinde
// sayım grafiği). LogPatternSpecByName grafiğin DEDEKTÖRÜN saydığını
// çizmesinin tek bağıdır: ad → dedektörün kendi regex + token'ları.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestLogPatternSpecByName(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		ok     bool
		regex  string
		tokens []string
	}{
		{"bilinen desen", "Oracle errors (ORA-)", true, `ORA-[0-9]+`, []string{"ora-"}},
		{"çok tokenlı desen", "Disk full", true, `no space left on device|disk full|ENOSPC`, []string{"no space left", "disk full", "enospc"}},
		{"bilinmeyen ad", "Some retired pattern", false, "", nil},
		{"boş ad", "", false, "", nil},
		{"büyük-küçük harf birebir", "disk full", false, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := LogPatternSpecByName(c.in)
			if ok != c.ok {
				t.Fatalf("ok=%v, want %v", ok, c.ok)
			}
			if !ok {
				return
			}
			if got.Regex != c.regex || !reflect.DeepEqual(got.Tokens, c.tokens) {
				t.Fatalf("spec=%+v, want regex=%q tokens=%v", got, c.regex, c.tokens)
			}
		})
	}
}

// Her küratörlü desen kendi adıyla bulunur ve dedektörün CountPatterns'a
// verdiği spec'in AYNISINI döndürür; adlar benzersiz (çift ad = ilk eşleşen
// desen sayılırdı, grafik başka deseni çizerdi).
func TestLogPatternSpecByName_EveryPatternRoundTrips(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range patterns {
		if seen[p.Name] {
			t.Fatalf("desen adı iki kez: %q", p.Name)
		}
		seen[p.Name] = true
		got, ok := LogPatternSpecByName(p.Name)
		if !ok || got.Regex != p.Regex || !reflect.DeepEqual(got.Tokens, p.Tokens) {
			t.Fatalf("%q: spec=%+v ok=%v", p.Name, got, ok)
		}
	}
}

// v0.10.1071 — spec desenin ADINI taşır: /logs `pattern=` süzgecinin önbellek
// anahtarı ve çipi bu addan (logstore.PatternKey); eşleşmeye girmez.
func TestLogPatternSpecByName_CarriesName(t *testing.T) {
	for _, p := range patterns {
		got, ok := LogPatternSpecByName(p.Name)
		if !ok || got.Name != p.Name || logstore.PatternKey(&got) != p.Name {
			t.Fatalf("%q: spec=%+v ok=%v", p.Name, got, ok)
		}
	}
}

// v0.10.1071 (operatör, prod: "'OR <sistem adı>' ibaresi yanlış olmuş, o bir
// hata değil.") — "External system rejected" desenindeki kurum içi sistem ADI
// hem regex alternasyonundan hem token listesinden çıkarıldı. Ad depo kuralı
// gereği burada da yazılmaz; desen tam hâliyle pinlenir: geri eklenen her
// alternasyon / token bu testi kırar. ES dedektörü regex'i yok sayıp token'larla
// saydığından token listesi desenin ES'teki TAM tanımıdır.
func TestExternalSystemRejected_NoSystemNameToken(t *testing.T) {
	got, ok := LogPatternSpecByName("External system rejected")
	if !ok {
		t.Fatal("desen bulunamadı")
	}
	if got.Regex != `ExternalSystemException|Request not allowed for URI|Service Unavailable` {
		t.Fatalf("regex=%q", got.Regex)
	}
	want := []string{"externalsystemexception", "not allowed for uri", "service unavailable"}
	if !reflect.DeepEqual(got.Tokens, want) {
		t.Fatalf("tokens=%v, want %v", got.Tokens, want)
	}
	// Her token bir regex alternatifinin küçük harfli alt-dizesi: token'ı
	// regex'te karşılığı olmayan bir ek (bir sistem adı gibi) olamaz.
	alts := strings.Split(strings.ToLower(got.Regex), "|")
	for _, tok := range got.Tokens {
		hit := false
		for _, a := range alts {
			if strings.Contains(a, tok) {
				hit = true
			}
		}
		if !hit {
			t.Fatalf("token %q hiçbir regex alternatifinde yok", tok)
		}
	}
}
