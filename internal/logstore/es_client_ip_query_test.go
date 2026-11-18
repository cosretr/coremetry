package logstore

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"
)

// v0.10.2349 (docs/cosre/investigation-design.md S2, §4.1 "Elasticsearch"):
// CoSRE v2 search_logs `client_ip` YENİ SORGU ŞEKLİ ÜRETMEZ. mcptools planı
// IP'yi `words`ün aynı tek gövde ifadesine ekler (`"<ip>"` ya da
// `"<kelimeler>" AND "<ip>"`; TestPlanSearchLogsClientIP); burada o metnin ES
// gövdesi: TEK query_string yan tümcesi, gövde alanı, AND, baştaki joker
// kapalı; joker / önek / regex / toplama / yeni alan yok.
func TestESClientIPSearchIsSinglePhraseClause(t *testing.T) {
	s := &ESStore{}
	s.cfg.defaults()
	s.fields = s.cfg.Fields
	t0 := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	base := Filter{Service: "checkout", From: t0.Add(-time.Hour), To: t0, Limit: 10}
	plain, _ := json.Marshal(s.buildQuery(Filter{Service: "checkout", From: base.From, To: base.To, Limit: 10, Search: `"timeout"`}))
	for _, search := range []string{`"198.51.100.7"`, `"timeout" AND "198.51.100.7"`, `"2001:db8::7"`} {
		f := base
		f.Search = search
		raw, err := json.Marshal(s.buildQuery(f))
		if err != nil {
			t.Fatal(err)
		}
		q := string(raw)
		if n := strings.Count(q, `"query_string"`); n != 1 {
			t.Fatalf("%s: %d query_string (1 olmalı): %s", search, n, q)
		}
		ip := search[strings.LastIndex(search[:len(search)-1], `"`)+1 : len(search)-1]
		if !strings.Contains(q, ip) || !strings.Contains(q, `"default_operator":"AND"`) ||
			!strings.Contains(q, `"allow_leading_wildcard":false`) || !strings.Contains(q, `"default_field":"`+s.fields.Body+`"`) {
			t.Errorf("%s: gövde ifadesi beklenen biçimde değil: %s", search, q)
		}
		for _, banned := range []string{`"wildcard"`, `"prefix"`, `"regexp"`, `"aggs"`, `"aggregations"`, `"terms"`, `client.address`, `"fuzzy"`} {
			if strings.Contains(q, banned) {
				t.Errorf("%s: %s üretmemeli: %s", search, banned, q)
			}
		}
		// Şekil: IP'li gövde ile kelimeli gövdenin anahtar kümesi aynı (yalnız metin farklı).
		if keysOf(t, raw) != keysOf(t, plain) {
			t.Errorf("%s: sorgu şekli değişti:\n%s\n%s", search, q, plain)
		}
	}
}

// keysOf — JSON'un anahtar yolları (değersiz), sıralı ve birleşik.
func keysOf(t *testing.T, raw []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(prefix string, x any)
	walk = func(prefix string, x any) {
		switch y := x.(type) {
		case map[string]any:
			for k, c := range y {
				out = append(out, prefix+"/"+k)
				walk(prefix+"/"+k, c)
			}
		case []any:
			for _, c := range y {
				walk(prefix+"[]", c)
			}
		}
	}
	walk("", v)
	set := map[string]bool{}
	var uniq []string
	for _, k := range out {
		if !set[k] {
			set[k] = true
			uniq = append(uniq, k)
		}
	}
	sort.Strings(uniq)
	return strings.Join(uniq, ",")
}
