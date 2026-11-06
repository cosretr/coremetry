package logstore

// signature.go — log mesajı imzası (v0.10.229, Influx D4 audit §4 adım 4).
//
// Aynı şablondan gelen mesajları (farklı UUID / id / zaman / IP / sayı)
// tek imzada toplar: yer tutucu `<x>`, boşluk sıkıştırma, 512 karakter
// tavanı. Bu bir GRUPLAMA anahtarıdır, redaksiyon DEĞİL: örnek mesaj
// verbatim saklanır (feedback-no-redaction). Drain şablonlamasından ayrı:
// burada örnek tabanı yok, tek mesaj → tek imza, deterministik.
//
// v0.10.1496 (operatör, prod ES): `{"request_id":"SPE1015…202610240340413867 00", …}`
// gövdeli 256 satır 164 desene dağılıyordu — harf+rakam karışık uzun
// kimlikler (`\b` sınırı olmadığı için sayı kuralı içlerine giremez)
// maskelenmiyordu. İki ek: (1) KİMLİK BELİRTECİ geçişi (MaskIDTokens —
// Drain maskesiyle ORTAK yüklem), (2) gövde JSON nesnesiyse imza = sıralı
// anahtar kümesi + mesaj alanının maskelenmiş metni; diğer değerler imzaya
// GİRMEZ.
//
// v0.10.1504 (operatör): bu çöküş başarılı ve başarısız satırı da tek desene
// topladı. Kısa kategorik değerler (status OK/FAILED, level, result, bool)
// desen toplamında örneklem kardinalitesine göre imzada TUTULUR — karar
// patterns.go'da (sigEnumKeys); satır başına NormalizeSignature değişmedi.
//
// v0.10.1829 (operatör: "GUID, UUID, id değerlerini yeni log pattern'i
// algılamasın"): maske kuralları ORTAK MaskVariableTokens'a taşındı
// (variable_tokens.go — Drain maskesiyle tek kural kümesi); burada yalnız
// boşluk ayraçlı ISO zaman damgası (ön adım), virgüllü liste birleştirmesi
// ve boşluk sıkıştırma kaldı.

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cespare/xxhash/v2"
)

const signatureMaxLen = 512

// signatureJSONMax — bu boyun üstündeki gövde JSON olarak çözülmez (örnek
// 2000 satır; çözüm maliyeti sınırlı kalsın), maskelenmiş ham metne düşer.
const signatureJSONMax = 64 << 10

var (
	// sigISO — boşluk ayraçlı ISO zaman damgası (`2026-09-02 10:15:30`):
	// iki belirteci kapsadığı için ortak maskede DEĞİL (o, boşluk sayısını
	// korur — Drain belirteç sayısı); imza yolunun ön adımı.
	sigISO = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?`)
	sigWS  = regexp.MustCompile(`\s+`)
)

// sigPlaceholder — imza yer tutucusu.
const sigPlaceholder = "<x>"

// sigMessageKeys — JSON gövdede mesaj sayılan alanlar, öncelik sırasıyla
// (büyük-küçük harf duyarsız; ilk dolu DİZGE değer kazanır).
var sigMessageKeys = []string{"message", "msg", "log", "error", "event"}

// sigLevelKeys — JSON gövdede seviye alanı (desen satırının Seviye
// kolonu, kayıt seviyesizse; (d) maddesi).
var sigLevelKeys = []string{"level", "severity", "log.level", "levelname", "loglevel", "severity_text"}

// NormalizeSignature — mesajın imza şablonu: boşluklu ISO zaman damgası →
// MaskVariableTokens (kural sırası variable_tokens.go) → virgüllü yer
// tutucu listesi tek yer tutucu → boşluk sıkıştırma → 512 bayt tavanı.
func NormalizeSignature(msg string) string {
	return signatureOf(msg).sig
}

// sigInfo — imza + yan ürünler (aynı JSON çözümünden, ikinci kez çözmeden).
type sigInfo struct {
	sig   string
	query string // PatternSearchQuery girdisi: JSON'da mesaj metni, değilse imza
	level string // JSON gövdedeki seviye metni ("" = yok)
	// js — v0.10.1504: gövde JSON nesnesiyse imzanın parçaları; desen
	// toplamı (patterns.go) kategorik alanları örneklem kardinalitesine
	// göre imzada TUTARAK yeniden çizer. nil = düz metin gövde.
	js *sigJSON
}

// sigJSON — v0.10.1504: JSON gövde imzasının çizim girdisi.
type sigJSON struct {
	keys    []string // sıralı özgün anahtarlar
	msgKey  string
	msgText string // maskelenmiş mesaj metni
	// cats — mesaj dışı alanlardan KATEGORİK aday olanların imzaya
	// yazılacak hâli (`"FAILED"`, `true`); anahtar özgün addır.
	cats map[string]string
}

func signatureOf(msg string) sigInfo {
	s := strings.TrimSpace(msg)
	if s == "" {
		return sigInfo{}
	}
	if s[0] == '{' && len(s) <= signatureJSONMax {
		if info, ok := jsonSignature(s); ok {
			return info
		}
	}
	sig := capSignature(maskSignatureText(s))
	return sigInfo{sig: sig, query: sig}
}

// maskSignatureText — düz metin maskesi (düz gövde ve JSON gövdenin mesaj
// alanı aynı yoldan). v0.10.1829: tırnaklı değer kuralı artık her konumda
// ve yalnız kimlik gibi içerikte (MaskVariableTokens 9. adım); `quoted`
// bayrağı kalktı.
func maskSignatureText(s string) string {
	s = sigISO.ReplaceAllString(s, sigPlaceholder)
	s = MaskVariableTokens(s, sigPlaceholder)
	s = CollapsePlaceholderLists(s, sigPlaceholder)
	return strings.TrimSpace(sigWS.ReplaceAllString(s, " "))
}

// jsonSignature — gövde bir JSON NESNESİ ise: `{"a":<x>,"msg":"<maskeli>",…}`
// (anahtarlar sıralı, mesaj alanı dışındaki her değer `<x>`). Kimlik
// değerleri böylece imzaya hiç girmez. Satır başına imza (NormalizeSignature)
// kategorik değerleri de `<x>` yazar; onları tutma kararı örneklem
// düzeyindedir (patterns.go, v0.10.1504) ve js.cats'ten okunur.
func jsonSignature(s string) (sigInfo, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil || len(m) == 0 {
		return sigInfo{}, false
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	msgKey, msgText := "", ""
	for _, want := range sigMessageKeys {
		for _, k := range keys {
			if !strings.EqualFold(k, want) {
				continue
			}
			var v string
			if json.Unmarshal(m[k], &v) == nil && strings.TrimSpace(v) != "" {
				msgKey, msgText = k, maskSignatureText(strings.TrimSpace(v))
			}
			break
		}
		if msgKey != "" {
			break
		}
	}
	level := ""
	for _, want := range sigLevelKeys {
		for _, k := range keys {
			if strings.EqualFold(k, want) {
				var v string
				if json.Unmarshal(m[k], &v) == nil {
					level = strings.TrimSpace(v)
				}
				break
			}
		}
		if level != "" {
			break
		}
	}
	js := &sigJSON{keys: keys, msgKey: msgKey, msgText: msgText}
	for _, k := range keys {
		if k == msgKey {
			continue
		}
		if lit, ok := sigCategorical(m[k]); ok {
			if js.cats == nil {
				js.cats = make(map[string]string, 4)
			}
			js.cats[k] = lit
		}
	}
	query := msgText
	if msgKey == "" {
		// Mesajsız gövde: arama anahtar adlarıyla (≥3 karakterli olanlar).
		query = strings.Join(keys, " <x> ")
	}
	return sigInfo{sig: js.render(nil), query: query, level: level, js: js}, true
}

// render — imza metni; keep(k) true olan kategorik anahtarın değeri
// yazılır, gerisi `<x>`. keep nil → v0.10.1496 imzası (tümü `<x>`).
func (j *sigJSON) render(keep func(string) bool) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range j.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kk, _ := json.Marshal(MaskIDTokens(k, "<x>"))
		b.Write(kk)
		b.WriteByte(':')
		if k == j.msgKey {
			b.WriteByte('"')
			b.WriteString(j.msgText)
			b.WriteByte('"')
		} else if lit, ok := j.cats[k]; ok && keep != nil && keep(k) {
			b.WriteString(lit)
		} else {
			b.WriteString("<x>")
		}
	}
	b.WriteByte('}')
	return capSignature(b.String())
}

// sigEnumMaxLen — kategorik değer tavanı (rune).
const sigEnumMaxLen = 24

// sigCategorical — v0.10.1504 SAF: bir JSON değeri kısa kategorik belirteç
// mi (status OK/FAILED, result, outcome, type, level, bool)? Evet ise
// imzaya yazılacak literal. Dizge: boş değil, ≤24 rune, ≤2 sözcük, sayı
// değil, maskeden değişmeden çıkar (kimlik / sayı / IP / tarih / hex
// parçası yok — IsIDLikeToken dahil), JSON'da kaçış gerektiren karakter
// yok. Sayı, null, nesne, dizi → hayır. Kardinalite kararı burada DEĞİL
// (satır başına bilinmez): patterns.go örneklem üstünde verir.
func sigCategorical(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	switch raw[0] {
	case 't', 'f':
		if b := string(raw); b == "true" || b == "false" {
			return b, true
		}
		return "", false
	case '"':
	default:
		return "", false
	}
	var v string
	if json.Unmarshal(raw, &v) != nil || v == "" || utf8.RuneCountInString(v) > sigEnumMaxLen {
		return "", false
	}
	if v != strings.TrimSpace(v) || strings.ContainsAny(v, "\"\\<>&\t\n\r") {
		return "", false
	}
	if n := len(strings.Fields(v)); n == 0 || n > 2 {
		return "", false
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return "", false
	}
	if IsIDLikeToken(v) || maskSignatureText(v) != v {
		return "", false
	}
	return `"` + v + `"`, true
}

func capSignature(s string) string {
	if len(s) > signatureMaxLen {
		s = s[:signatureMaxLen]
	}
	return s
}

func isSigAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// MaskIDTokens — v0.10.1496: metindeki harf/rakam koşularından (ayraç =
// harf/rakam dışı her şey; `_` ve `-` dahil) kimlik gibi olanları repl ile
// değiştirir. Doğal dil sözcükleri (rakamsız) ve kısa belirteçler
// (OP12, v2, sha256, log4j2) aynen kalır. Desenler (NormalizeSignature) ve
// Drain maskesi (templater.Mask) bu TEK yüklemi kullanır.
func MaskIDTokens(s, repl string) string {
	var out strings.Builder
	out.Grow(len(s))
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		if tok := s[start:end]; IsIDLikeToken(tok) {
			out.WriteString(repl)
		} else {
			out.WriteString(tok)
		}
		start = -1
	}
	for i, r := range s {
		if isSigAlnum(r) {
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
//   - izin listesi (idTokenAllowlist: `utf8mb4`, `log4j2`, `sha256`, `ed25519`…) → hayır
//   - yalnız rakam, ≥4 (sınırsız konumdaki sayılar: `order_123456`)
//   - yalnız hex, ≥8, en az bir rakam (`1a2b3c4d`; `deadbeef` sözcük sayılır)
//   - v0.10.1829 kısa hex: yalnız hex, ≥6, harf VE rakam (`7d9f6c`, `a07d33f9c40`)
//   - rakam baskın: ≥5 rakam ve rakam > yarısı (`ab12345`; `sha256`, `iso8601` kalır)
//   - v0.10.1829 karışık: ≥6, ≥2 rakam, ≥2 rakam öbeği (rakamlar harflerle
//     serpiştirilmiş) ve en uzun harf koşusu ≤3 (`TX9F3K2`, `QZ7M4P1`;
//     `Log4j2Plugins` — 7 harflik koşu — sınıf adı olarak kalır)
//   - uzun karışık: ≥10 ve ≥3 rakam, ya da ≥16 ve ≥2 rakam
//     (`SPE1015060201ZTod…`, ULID, base64 parçaları)
//   - base64-benzeri: ≥16, büyük ve küçük harf her biri ≥¼ ve harf durumu
//     değişimi ≥⅓ (camelCase sınıf adları — `NullPointerException` — kalır)
func IsIDLikeToken(tok string) bool {
	n, digits, letters, upper, lower, flips, hex := 0, 0, 0, 0, 0, 0, true
	digitGroups, letterRun, maxLetterRun := 0, 0, 0
	prevCase := 0 // 1 upper, 2 lower
	prevDigit := false
	for _, r := range tok {
		n++
		if r >= '0' && r <= '9' || unicode.IsDigit(r) {
			digits++
			if r > '9' || r < '0' {
				hex = false
			}
			if !prevDigit {
				digitGroups++
			}
			prevDigit, prevCase, letterRun = true, 0, 0
			continue
		}
		prevDigit = false
		letters++
		letterRun++
		if letterRun > maxLetterRun {
			maxLetterRun = letterRun
		}
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
	if digits > 0 && letters > 0 && n <= 10 && idTokenAllowlist[strings.ToLower(tok)] {
		return false
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
	case hex && n >= 6:
		return true // ≥8 hex (v0.10.1496) + v0.10.1496'nın kaçırdığı 6–7 hex (harf+rakam)
	case digits >= 5 && digits*2 > n:
		return true
	case n >= 16 && digits >= 2, n >= 10 && digits >= 3:
		return true
	case n >= 6 && digits >= 2 && digitGroups >= 2 && maxLetterRun <= 3:
		return true
	}
	return false
}

// SignatureHash — imzanın xxhash64'ü (grup anahtarı; UI'da kısa kimlik).
func SignatureHash(sig string) uint64 { return xxhash.Sum64String(sig) }

// severityFromText — v0.10.1496 (d): seviye metninden OTel severity_number
// bandının tabanı (severityBands ile aynı önekler; FATAL ayrı, 21). Metin
// tanınmazsa 0. ES'te severity_number alanı yapılandırılmamış kurulumlarda
// desen satırının Seviye kolonu 0 kalıyordu.
func severityFromText(t string) uint8 {
	t = strings.ToLower(strings.TrimSpace(t))
	switch {
	case t == "":
		return 0
	case strings.HasPrefix(t, "fatal"), strings.HasPrefix(t, "crit"), strings.HasPrefix(t, "panic"):
		return 21
	case strings.HasPrefix(t, "err"):
		return 17
	case strings.HasPrefix(t, "warn"):
		return 13
	case strings.HasPrefix(t, "info"):
		return 9
	case strings.HasPrefix(t, "debug"):
		return 5
	case strings.HasPrefix(t, "trace"):
		return 1
	}
	return 0
}
