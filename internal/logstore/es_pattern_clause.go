package logstore

// es_pattern_clause.go — v0.10.1087 (operatör, prod ES: "Elastic'te eksik.").
//
// Log deseni token'ları ES'te `message:"token"` ifade sorgusuydu. Standart
// çözümleyici (UAX#29) kod benzeri sözcüğü TEK terim tutar: harf/rakam
// bitişikliği ayrılmaz (`MQJCA1011` → `mqjca1011`), iki harf arasındaki nokta
// da ayırmaz (`java.lang.NullPointerException` → `java.lang.nullpointerexception`).
// Terimin ÖNEKİ ya da İÇİ olan token (`nullpointer`, `mqjca`, `wfly`,
// `sqlexception`) ifade sorgusuyla hiç eşleşmez; CH aynı token'ı alt-dize
// olarak eşler. Sonuç: ES'te bu desenler ~0 sayar, olay açılmaz.
//
// Yan tümce artık token başına bir üyeli bool.should:
//
//	prefix       tek terim biçimli (yalnız [a-z0-9_] ve harf arası nokta) ve
//	             ≥ esPrefixMinLen karakter token → terim sözlüğünde önek
//	             araması (indeks dostu: tek seek + öneki taşıyan terimler);
//	match_phrase çok sözcüklü, tireli / iki noktalı ya da kısa token (`ora-`,
//	             `tns-`, `401`, `panic:`, `x509:`) → eskisi gibi tam terim.
//	             Kısa önek `ora*` "oracle"/"orange"ı, `401*` "4012"yi sayardı;
//	             bunlar ifade kalır, fazlasını 1080 örneklemi ayıklar.
//
// PatternSpec.ESPrefixes yalnız ES'te prefix olarak eklenen biçimlerdir: kural
// dışı kalan kısa kod önekleri (`wfly`, `jbas`) ve paket-nitelikli sınıf
// adlarının başı (`java.lang.nullpointer`). İçeriden eşleşme (`*token*`)
// YOK: baştaki joker terim sözlüğünün tamamını gezer (allow_leading_wildcard
// kapalı tutulmasının sebebi).
//
// Dedektör (patternCountBody), anomali grafiği (patternHistogramBody), /logs
// `pattern=` süzgeci (patternFilterClause) ve 1080 örneklemi
// (patternSampleBody) bu TEK işlevden okur — dördü ayrışamaz.

import "strings"

// esPrefixMinLen — otomatik prefix kuralının alt sınırı. Altındaki token
// ancak ESPrefixes'te açıkça adı geçerse prefix olur.
const esPrefixMinLen = 5

// ESPatternTerm — desen yan tümcesinin bir üyesi: Prefix → `prefix` sorgusu
// (değer küçük harf), aksi → `match_phrase`.
type ESPatternTerm struct {
	Value  string
	Prefix bool
}

// esSingleTermShape — SAF: token standart çözümleyicide tek terim olarak
// kalır mı (prefix ancak o terimin başıyla karşılaştırılabilir). Yalnız
// [a-z0-9_]; nokta yalnız iki harf arasında (UAX#29 WB6/7). Tire, iki nokta,
// boşluk → hayır (çözümleyici böler, prefix hiçbir terimle eşleşmez).
func esSingleTermShape(tok string) bool {
	if tok == "" {
		return false
	}
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
		case c == '.':
			if i == 0 || i == len(tok)-1 || !isASCIILower(tok[i-1]) || !isASCIILower(tok[i+1]) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func isASCIILower(c byte) bool { return c >= 'a' && c <= 'z' }

// esTokenPrefix — SAF: otomatik kural — tek terim biçimli ve ≥ esPrefixMinLen.
func esTokenPrefix(tok string) bool {
	return len(tok) >= esPrefixMinLen && esSingleTermShape(tok)
}

// ESPatternTerms — SAF: desenin ES planı. Sıra: Tokens (kurala göre prefix /
// ifade), ardından ESPrefixes (hep prefix). ESPrefixes'te de adı geçen token
// tek kez, prefix olarak yazılır; tekrarlar düşer. Biçimi tek terim olmayan
// ESPrefixes girdisi ifadeye düşer (prefix'i hiçbir terimle eşleşmezdi;
// küratörlü liste testle bunu zaten engeller).
func ESPatternTerms(p PatternSpec) []ESPatternTerm {
	forced := make(map[string]bool, len(p.ESPrefixes))
	for _, e := range p.ESPrefixes {
		forced[strings.ToLower(e)] = true
	}
	out := make([]ESPatternTerm, 0, len(p.Tokens)+len(p.ESPrefixes))
	seen := make(map[ESPatternTerm]bool, cap(out))
	add := func(t ESPatternTerm) {
		if t.Value == "" || seen[t] {
			return
		}
		seen[t] = true
		out = append(out, t)
	}
	for _, tok := range p.Tokens {
		low := strings.ToLower(tok)
		pre := esSingleTermShape(low) && (forced[low] || esTokenPrefix(low))
		if pre {
			add(ESPatternTerm{Value: low, Prefix: true})
		} else {
			add(ESPatternTerm{Value: tok})
		}
	}
	for _, e := range p.ESPrefixes {
		low := strings.ToLower(e)
		add(ESPatternTerm{Value: low, Prefix: esSingleTermShape(low)})
	}
	return out
}

// esPatternTermClause — SAF: tek üyenin ES sorgusu. prefix `case_insensitive`
// taşır (ES ≥ 7.10; depo bunu v0.8.377 seviye bantlarından beri varsayar) —
// değer zaten küçük harf, bayrak küçük harfe çevirmeyen çözümleyicide işe
// yarar. query_string'deki gibi kaçış gerekmez: match_phrase metni
// çözümleyiciden geçirir, prefix değeri olduğu gibi karşılaştırır.
func esPatternTermClause(t ESPatternTerm, bodyField string) map[string]any {
	if t.Prefix {
		return map[string]any{"prefix": map[string]any{
			bodyField: map[string]any{"value": t.Value, "case_insensitive": true},
		}}
	}
	return map[string]any{"match_phrase": map[string]any{bodyField: t.Value}}
}

// patternMatchClause — SAF: desen eşleşme yan tümcesi (filtre bağlamında
// kullanılır; skor yok). Token'sız desen → match_none (regex ES'te
// sorgulanmaz; sahte "her şey" yerine dürüst sıfır).
func patternMatchClause(p PatternSpec, bodyField string) map[string]any {
	if len(p.Tokens) == 0 {
		return map[string]any{"match_none": map[string]any{}}
	}
	terms := ESPatternTerms(p)
	should := make([]any, 0, len(terms))
	for _, t := range terms {
		should = append(should, esPatternTermClause(t, bodyField))
	}
	return map[string]any{"bool": map[string]any{
		"should":               should,
		"minimum_should_match": 1,
	}}
}
