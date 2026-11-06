package templater

// masker_v1_frozen_test.go — v0.10.1829: v1 maskesinin (v0.10.1496–1828)
// DONDURULMUŞ kopyası. Defterdeki (log_templates) eski şablonlar bununla
// üretildi; geçiş testi Remask(maskV1(line)) == Mask(line) eşitliğini
// buna karşı sınar. Üretim kodu bunu KULLANMAZ; DEĞİŞTİRME.

import (
	"strings"
	"unicode"
)

func v1Alnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// maskV1 — v1 Drain maskesi: maskRules (değişmedi) + v1 kimlik geçişi.
func maskV1(line string) string {
	out := line
	for _, r := range maskRules {
		out = r.re.ReplaceAllString(out, r.repl)
	}
	return maskIDTokensV1(out, "<*>")
}

func maskIDTokensV1(s, repl string) string {
	var out strings.Builder
	out.Grow(len(s))
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		if tok := s[start:end]; isIDLikeTokenV1(tok) {
			out.WriteString(repl)
		} else {
			out.WriteString(tok)
		}
		start = -1
	}
	for i, r := range s {
		if v1Alnum(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i)
		out.WriteRune(r)
	}
	flush(len(s))
	return out.String()
}

// IsIDLikeToken — tek harf/rakam koşusu kimlik mi? SAF.
//   - yalnız rakam, ≥4 (sınırsız konumdaki sayılar: `order_123456`)
//   - yalnız hex, ≥8, en az bir rakam (`1a2b3c4d`; `deadbeef` sözcük sayılır)
//   - rakam baskın: ≥5 rakam ve rakam > yarısı (`ab12345`; `sha256`, `iso8601` kalır)
//   - uzun karışık: ≥10 ve ≥3 rakam, ya da ≥16 ve ≥2 rakam
//     (`SPE1015060201ZTod…`, ULID, base64 parçaları)
//   - base64-benzeri: ≥16, büyük ve küçük harf her biri ≥¼ ve harf durumu
//     değişimi ≥⅓ (camelCase sınıf adları — `NullPointerException` — kalır)
func isIDLikeTokenV1(tok string) bool {
	n, digits, letters, upper, lower, flips, hex := 0, 0, 0, 0, 0, 0, true
	prevCase := 0 // 1 upper, 2 lower
	for _, r := range tok {
		n++
		switch {
		case r >= '0' && r <= '9':
			digits++
			prevCase = 0
			continue
		case unicode.IsDigit(r):
			digits++
			hex = false
			prevCase = 0
			continue
		}
		letters++
		if !((r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			hex = false
		}
		c := 0
		if unicode.IsUpper(r) {
			upper++
			c = 1
		} else if unicode.IsLower(r) {
			lower++
			c = 2
		}
		if prevCase != 0 && c != 0 && c != prevCase {
			flips++
		}
		prevCase = c
	}
	switch {
	case n == 0:
		return false
	case n >= 16 && upper*4 >= n && lower*4 >= n && flips*3 >= n:
		return true // base64-benzeri (rakamlı ya da rakamsız)
	case digits == 0:
		return false
	case letters == 0:
		return n >= 4
	case hex && n >= 8:
		return true
	case digits >= 5 && digits*2 > n:
		return true
	case n >= 16 && digits >= 2, n >= 10 && digits >= 3:
		return true
	}
	return false
}
