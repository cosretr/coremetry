package anomaly

// log_pattern_spec_test.go — v0.10.1060 (log deseni anomalisi: zaman içinde
// sayım grafiği). LogPatternSpecByName grafiğin DEDEKTÖRÜN saydığını
// çizmesinin tek bağıdır: ad → dedektörün kendi regex + token'ları.

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
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

// v0.10.1062 — her küratörlü desenin /logs arama metni var (servissiz olayda
// "Logları aç" bağlantısının tek kaynağı) ve CH'de dedektörün token ön
// süzgecine derlenir: token başına bir multiSearchAnyCaseInsensitive, bağlar
// token'ların kendisi. Token'da `kelime:değer` biçimi yok — ES expandShorthand
// `service:x` gibi bir parçayı (tırnak içinde de) alan sorgusuna çevirir, metin
// gövde araması olmaktan çıkardı. Sonda ':' ("panic:") zararsız: kısayol
// ifadesi iki noktadan sonra boşluk olmayan bir değer ister.
var fieldShaped = regexp.MustCompile(`(^|[\s(])[A-Za-z_.]+:[^\s]`)

func TestEveryPatternHasFaithfulLogsSearch(t *testing.T) {
	for _, p := range patterns {
		spec, _ := LogPatternSpecByName(p.Name)
		text := logstore.PatternSearchText(spec)
		if text == "" {
			t.Fatalf("%q: arama metni boş (token yok)", p.Name)
		}
		for _, tok := range p.Tokens {
			if fieldShaped.MatchString(tok) {
				t.Fatalf("%q: token %q alan sorgusu biçiminde (kelime:değer)", p.Name, tok)
			}
		}
		sql, args := chstore.LogSearchConjunct(text)
		if n := strings.Count(sql, "multiSearchAnyCaseInsensitive(body, [?])"); n != len(p.Tokens) || len(args) != len(p.Tokens) {
			t.Fatalf("%q: CH yüklemi %q args=%v", p.Name, sql, args)
		}
		for i, a := range args {
			if a != p.Tokens[i] {
				t.Fatalf("%q: bağ %d = %v, token %q", p.Name, i, a, p.Tokens[i])
			}
		}
	}
}
