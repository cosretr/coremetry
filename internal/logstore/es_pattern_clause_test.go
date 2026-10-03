package logstore

// es_pattern_clause_test.go — v0.10.1087 (operatör, prod ES: "Elastic'te
// eksik."). Çivilenen: token başına mod (prefix / ifade) kuralı, yan tümcenin
// şekli, baştaki joker / query_string olmaması ve CH yükleminin ESPrefixes'ten
// etkilenmemesi. Uçtan uca analizör fikstürü: anomaly/log_patterns_es_prefix_test.go.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestESPatternTerms_ModePerToken(t *testing.T) {
	cases := []struct {
		tok    string
		prefix bool
		why    string
	}{
		{"nullpointer", true, "kod benzeri sözcüğün başı"},
		{"sqlexception", true, "≥5, tek terim"},
		{"timeout", true, "TimeoutException terimi önekle bulunur"},
		{"mqjca", true, "tam 5 karakter: MQJCA1011 tek terim"},
		{"exception", true, "tek sözcük ≥5"},
		{"ij000655", true, "harf+rakam tek terim"},
		{"java.lang.nullpointer", true, "harf arası nokta tek terimde kalır"},
		{"ora-", false, "tire çözümleyicide düşer; `ora*` oracle'ı sayardı"},
		{"tns-", false, "tire"},
		{"401", false, "kısa: `401*` 4012'yi sayardı"},
		{"panic:", false, "iki nokta"},
		{"x509:", false, "iki nokta"},
		{"wfly", false, "4 karakter: yalnız ESPrefixes ile prefix"},
		{"not allowed for uri", false, "çok sözcüklü ifade"},
		{"tls: handshake", false, "çok sözcüklü"},
		{"v2.api", false, "rakam-nokta-harf çözümleyicide bölünür"},
		{".hidden", false, "baştaki nokta"},
		{"hikari-pool", false, "tire"},
	}
	for _, c := range cases {
		got := ESPatternTerms(PatternSpec{Tokens: []string{c.tok}})
		want := []ESPatternTerm{{Value: c.tok, Prefix: c.prefix}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q (%s): got %+v, want %+v", c.tok, c.why, got, want)
		}
	}
}

// ESPrefixes: kısa token açıkça prefix'e alınır (ifade tekrar yazılmaz), ek
// biçimler Tokens'tan sonra gelir, tekrarlar düşer, büyük harf küçültülür.
func TestESPatternTerms_ESPrefixes(t *testing.T) {
	got := ESPatternTerms(PatternSpec{
		Tokens:     []string{"wfly", "jbas", "nullpointer", "null pointer"},
		ESPrefixes: []string{"wfly", "jbas", "Java.Lang.NullPointer", "nullpointer", "ora-"},
	})
	want := []ESPatternTerm{
		{Value: "wfly", Prefix: true},
		{Value: "jbas", Prefix: true},
		{Value: "nullpointer", Prefix: true},
		{Value: "null pointer"},
		{Value: "java.lang.nullpointer", Prefix: true},
		{Value: "ora-"}, // tek terim biçimli değil: prefix hiçbir terimle eşleşmezdi
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestPatternMatchClause_Golden(t *testing.T) {
	c := patternMatchClause(PatternSpec{
		Tokens:     []string{"mqjca", "managed connection", "ij000655"},
		ESPrefixes: []string{"java.sql.sqlexception"},
	}, "message")
	got, _ := json.Marshal(c)
	want := `{"bool":{"minimum_should_match":1,"should":[` +
		`{"prefix":{"message":{"case_insensitive":true,"value":"mqjca"}}},` +
		`{"match_phrase":{"message":"managed connection"}},` +
		`{"prefix":{"message":{"case_insensitive":true,"value":"ij000655"}}},` +
		`{"prefix":{"message":{"case_insensitive":true,"value":"java.sql.sqlexception"}}}]}}`
	if string(got) != want {
		t.Fatalf("yan tümce:\n got %s\nwant %s", got, want)
	}
	// Token'sız desen: ESPrefixes olsa da dürüst sıfır (CountPatterns'ın
	// match_none slotu ile aynı).
	none := patternMatchClause(PatternSpec{ESPrefixes: []string{"wfly"}}, "message")
	if !reflect.DeepEqual(none, map[string]any{"match_none": map[string]any{}}) {
		t.Fatalf("token'sız: %v", none)
	}
	// Tırnak kaçışı gerekmez (match_phrase metni çözümleyiciden geçer).
	q, _ := json.Marshal(patternMatchClause(PatternSpec{Tokens: []string{`say "hi"`}}, "message"))
	if !strings.Contains(string(q), `{"match_phrase":{"message":"say \"hi\""}}`) {
		t.Fatalf("tırnaklı ifade: %s", q)
	}
}

// Maliyet sözleşmesi: baştaki joker / regexp / query_string YOK; her prefix
// değeri küçük harf ve tek terim biçimli; kısa token (<5) ancak açıkça.
func TestPatternMatchClause_NoLeadingWildcard(t *testing.T) {
	specs := append([]PatternSpec{}, parityPatterns...)
	specs = append(specs, PatternSpec{
		Tokens:     []string{"wfly", "jbas", "nullpointer", "ora-", "401", "panic:"},
		ESPrefixes: []string{"wfly", "jbas", "java.lang.nullpointer"},
	})
	for _, p := range specs {
		raw, _ := json.Marshal(patternMatchClause(p, "message"))
		s := string(raw)
		for _, banned := range []string{`"wildcard"`, `"regexp"`, `"query_string"`, `"*`, `*"`, `"fuzzy"`} {
			if strings.Contains(s, banned) {
				t.Errorf("%v: %s yasak: %s", p.Tokens, banned, s)
			}
		}
		forced := map[string]bool{}
		for _, e := range p.ESPrefixes {
			forced[e] = true
		}
		for _, term := range ESPatternTerms(p) {
			if !term.Prefix {
				continue
			}
			if term.Value != strings.ToLower(term.Value) || !esSingleTermShape(term.Value) {
				t.Errorf("prefix değeri tek terim biçimli küçük harf olmalı: %q", term.Value)
			}
			if len(term.Value) < esPrefixMinLen && !forced[term.Value] {
				t.Errorf("kısa prefix %q açıkça istenmedi", term.Value)
			}
		}
	}
}

// CH DOKUNULMADI: ESPrefixes CH yüklemine girmez (aynı SQL, aynı bağ);
// önbellek anahtarı adsız spec'te yüklemi ayırır.
func TestPatternMatchClause_CHUntouched(t *testing.T) {
	base := PatternSpec{Regex: `NullPointerException`, Tokens: []string{"nullpointer", "null pointer"}}
	withES := base
	withES.ESPrefixes = []string{"java.lang.nullpointer"}
	c1, a1 := chPatternConjunct(&base)
	c2, a2 := chPatternConjunct(&withES)
	if c1 != c2 || !reflect.DeepEqual(a1, a2) {
		t.Fatalf("CH yüklemi ESPrefixes'ten etkilendi:\n%s\n%s", c1, c2)
	}
	if c1 != "(multiSearchAnyCaseInsensitive(body, ['nullpointer', 'null pointer']) AND match(body, ?))" {
		t.Fatalf("CH yüklemi değişti: %s", c1)
	}
	if PatternKey(&base) == PatternKey(&withES) {
		t.Fatal("adsız spec anahtarı ESPrefixes'i ayırmalı")
	}
	if PatternKey(&base) != "spec:NullPointerException|nullpointer\x00null pointer" {
		t.Fatalf("ESPrefixes'siz anahtar değişmemeli: %q", PatternKey(&base))
	}
}
