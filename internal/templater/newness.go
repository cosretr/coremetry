package templater

// newness.go — v0.10.2040 (Operator-reported: "Aslında benzer loglar var ama
// yeni log pattern gibi söyledin."). Servisin en sık satırı (uzun
// `anahtar=değer, …` dökümü) "yeni log şablonu" sayıldı. Kök neden:
//
//  1. Maske `anahtar=değer` içindeki kısa rastgele kimliği sabit sayıyordu
//     (logstore.isRandomIDValue ile düzeldi; MaskerVersion 3).
//  2. Aile kuralı (family.go) EŞİT belirteç sayısı ister; değeri boşluk
//     içeren alanlar (tarayıcı kimliği, tarih metni, cihaz dizgesi)
//     satırdan satıra belirteç sayısını değiştirir → "aynı aile değil".
//     Burada YALNIZ log_template_new "yeni mi" kararı için ek bir yol:
//     ÖN EK İMZASI (prefixSignature). Drain kümelemesi, Desenler ve ML
//     şablon dedektörleri (en uzun sabit dizi ailesi) DEĞİŞMEZ.
//  3. Gövdesi JSON zarfı olan satırda şablon tüm zarftan çıkıyordu (zarf
//     alanları — istek kimliği, zaman, iş parçacığı — belirteç sayısını ve
//     sabitleri oynatır): TemplateSource `message` / `msg` / `log` alanını
//     döndürür.

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// newnessMinTokens — ön ek imzası yalnız uzun satırlarda (kısa satırda
	// ön ek satırın kendisidir; Drain kuralı yeterli).
	newnessMinTokens = 6
	// newnessPrefixChars — imzanın boyu: `<*>` atıldıktan sonra şablonun
	// ilk 48 karakteri (≈ ilk 3–6 sabit belirteç).
	newnessPrefixChars = 48
	// newnessMinLetters — imza en az bu kadar HARF taşımalı; "INFO" ya da
	// tek sözcükle başlayan genel satırlar aşırı birleşmesin.
	newnessMinLetters = 24
	// newnessMinKeys / newnessKeyJaccard — `anahtar=değer` dökümünde en az 4
	// anahtar ve anahtar kümeleri ≥ %80 ortak.
	newnessMinKeys    = 4
	newnessKeyJaccard = 0.8
	// newnessTokenJaccard — düz metinde sabit belirteç kümeleri ≥ %60 ortak.
	newnessTokenJaccard = 0.6
)

// SameNewnessFamily — "bu aday bilinen şablonun varyantı mı" (yalnız
// log_template_new kararı): Drain ailesi (SameFamily) YA DA aynı ön ek
// imzası.
func (p ParsedTemplate) SameNewnessFamily(q ParsedTemplate) bool {
	if p.SameFamily(q) {
		return true
	}
	a, ok := prefixSignature(p.tokens)
	if !ok {
		return false
	}
	b, ok := prefixSignature(q.tokens)
	if !ok || a != b {
		return false
	}
	// Ön ek tek başına yetmez (aynı JSON başlığıyla başlayan iki farklı mesaj):
	// gövdenin yapısı da benzemeli. `anahtar=değer` dökümünde anahtar
	// kümeleri, değilse sabit belirteç kümeleri.
	ka, kb := kvKeys(p.tokens), kvKeys(q.tokens)
	if len(ka) >= newnessMinKeys && len(kb) >= newnessMinKeys {
		return jaccard(ka, kb) >= newnessKeyJaccard
	}
	return jaccard(constTokens(p.tokens), constTokens(q.tokens)) >= newnessTokenJaccard
}

// kvKeys — SAF: `anahtar=…` belirteçlerinin anahtarları (baştaki noktalama atılır).
func kvKeys(tokens []string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tokens {
		i := strings.IndexByte(t, '=')
		if i <= 0 {
			continue
		}
		k := t[:i]
		if j := strings.LastIndexAny(k, "({[,;\"'"); j >= 0 {
			k = k[j+1:]
		}
		if k != "" {
			out[k] = true
		}
	}
	return out
}

// constTokens — SAF: `<*>` atılmış boş olmayan belirteçler.
func constTokens(tokens []string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tokens {
		if t = strings.ReplaceAll(t, templateWildcard, ""); t != "" {
			out[t] = true
		}
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// prefixSignature — SAF: `<*>` atılmış belirteçlerin ilk newnessPrefixChars
// karakteri (rune sınırında). ok=false: satır kısa ya da imza yeterince
// harf taşımıyor.
func prefixSignature(tokens []string) (string, bool) {
	if len(tokens) < newnessMinTokens {
		return "", false
	}
	var b strings.Builder
	for _, t := range tokens {
		t = strings.ReplaceAll(t, templateWildcard, "")
		if t == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(t)
		if b.Len() >= newnessPrefixChars {
			break
		}
	}
	sig := b.String()
	if len(sig) > newnessPrefixChars {
		cut := newnessPrefixChars
		for cut > 0 && !utf8.RuneStart(sig[cut]) {
			cut--
		}
		sig = sig[:cut]
	}
	letters := 0
	for _, r := range sig {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return sig, letters >= newnessMinLetters
}

// LiteralPrefix — SAF: ön ek imzası taşıyan şablonun ilk `<*>`'a kadarki
// sabit metni (defter okumasının SQL ön süzgeci, startsWith). Ön ek imzası
// yoksa ya da sabit baş 24 karakterden kısaysa (genel JSON başı `{"Timestamp":"` defteri boşuna açmasın) "".
func (p ParsedTemplate) LiteralPrefix() string {
	if _, ok := prefixSignature(p.tokens); !ok {
		return ""
	}
	s := strings.Join(p.tokens, " ")
	if i := strings.Index(s, templateWildcard); i >= 0 {
		s = s[:i]
	}
	if len(s) < 24 {
		return ""
	}
	return s
}

// TemplateSource — SAF: şablonun çıkarılacağı metin. Gövde bir JSON nesnesiyse
// ve `message` / `msg` / `log` alanı boş olmayan bir dizgeyse o alan; değilse
// gövdenin kendisi. Maliyet: yalnız `{` ile başlayıp `}` ile biten satırda
// bir json.Unmarshal (örneklenen satırlar; tik başına ~1000).
func TemplateSource(body string) string {
	t := strings.TrimSpace(body)
	if len(t) < 2 || t[0] != '{' || t[len(t)-1] != '}' {
		return body
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(t), &m) != nil {
		return body
	}
	for _, k := range []string{"message", "msg", "log"} {
		raw, ok := m[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return body
}
