package logstore

// variable_tokens.go — v0.10.1829: log kalıbı KİMLİĞİ için ortak
// değişken-belirteç maskesi (MaskVariableTokens).
//
// Operator-reported: "Sadece bazen loglarda GUID, UUID, id değerleri
// oluyor; bunları yeni log pattern'i algılamasın." Aynı mesaj farklı bir
// GUID / UUID / id / sayıyla gelince iki kimlik de (Logs › Desenler imzası
// NormalizeSignature ve Drain şablonu templater.Mask) ayrı kalıp
// üretiyordu: yapışık UUID (`id_<uuid>`, sol `\b` yok), kısa karışık /
// hex kimlik (`TX9F3K2`, `7d9f6c`), pod son eki, binlik ayraçlı sayı
// (`26,916,627` → grup sayısı değişken), ondalık, tek hane, birimli sayı
// (`1500ms`), biçim yer tutucusu (`{0}`, `%s`), e-posta, IPv6, Java nesne
// hash'i. İki kimlik bu TEK işlevi çağırır, her biri kendi yer tutucusuyla
// (`<x>` / `<*>`).
//
// Bu bir GRUPLAMA anahtarıdır, redaksiyon DEĞİL (feedback-no-redaction):
// gösterilen log satırı, saklanan veri, örnek satır ve CoSRE girdisi
// VERBATIM kalır; maske yalnız kimlik hesabına girer.
//
// Kural sırası (önemli; her kural öncekilerin çıktısında koşar):
//
//  1. UUID/GUID — tireli 8-4-4-4-12, her harf durumu; sol `\b` ARANMAZ
//     (`id_`, `=`, `:`, `/`, `#`, harf sonrası da yakalanır); bir grup
//     zaten yer tutucuysa da (eski maskenin parçaladığı UUID) eşleşir.
//     Süslü/parantezli ve `urn:uuid:` biçimleri 11. adımda tek yer tutucu.
//     32-hex GUID 10. adımda (hex kuralı).
//  2. ISO-8601 (T ayraçlı) zaman damgası + tek başına HH:MM:SS(.fff).
//  3. E-posta adresi.
//  4. IPv4(+port), köşeli IPv6(+port; `[<x>]:port` da), çıplak IPv6 (doğrulamalı: `::`
//     içerir ya da ≥6 grup; `std::cout` / `Foo::bar` sınır denetimiyle
//     dışarıda).
//  5. Java nesne hash'i: `Sınıf@1a2b3c` → `Sınıf@<x>`.
//  6. Kubernetes üretilmiş son eki: `-` sonrası 1–2 bölüm, k8s alfabesi
//     `bcdfghjklmnpqrstvwxz2456789`, 5–10 karakter, en az bir bölümde
//     rakam → `-<x>`. KALAN: yalnız harfli 5'lik son ek (`-bcdfg`) pod
//     listesi olmadan ayırt edilemez, maskelenmez.
//  7. Biçim yer tutucuları: `{}` `{0}` `{12}` `%s` `%d` `%v` `%f` `%i` ve
//     mesaj içindeki SQL bind `?` — değerle AYNI yer tutucu (biçimlenmemiş
//     şablon satırı biçimlenmiş satırla birleşsin). `{name}` METİN kalır.
//  8. Tırnaklı değer (`"…"`, `'…'`) yalnız İÇERİK kimlik gibiyse (gelişigüzel
//     tırnaklı metin maskelenmez); ham içerik sınanır, sayı adımından önce.
//  9. Sayılar — sözcük-sınırlı (sol komşu harf/rakam/`_` değil) her
//     uzunlukta tamsayı; binlik ayraçlı (`26,916,627`, `1.234.567`,
//     NBSP/ince boşluklu `1 234 567`) ve ondalık (`18.45`, `3,14`) TEK yer
//     tutucu; birim korunur (`<x>ms`, `<x>MB`, `<x>%`). Sözlük belirteçleri
//     (`http2`, `p95`, `x86_64`, `k8s`…) sol sınır yüzünden dokunulmaz;
//     `HTTP/1.1`, `HTTP/2` protokol sürümü korunur.
// 10. Belirteç geçişi (MaskIDTokens / IsIDLikeToken): harf/rakam koşusu
//     kimlik gibiyse — hex ≥6 (harf+rakam), karışık ≥6 (≥2 rakam, ≥2 rakam
//     öbeği, ≤3 harflik koşu), rakam baskın, uzun karışık, base64-benzeri,
//     `_` yapışık ≥4 rakam. Sözlük belirteçleri izin listesinde
//     (idTokenAllowlist).
// 11. Birleştirme (kararlı olana dek, UUID + pod kuralı yeniden): ayraçla
//     (boşluk HARİÇ) bağlı yer tutucu koşusu tek yer tutucu (`<x>,<x>,<x>`
//     → `<x>`), `{<x>}` → `<x>`, `urn:uuid:<x>` → `<x>`.
//
// Boşluk/sekme ASLA eklenmez ya da silinmez: Drain belirteç sayısı (katman 1
// anahtarı + bilinen-şablon SQL ön süzgeci) yeniden maskelemede değişmez
// (TestMaskVariableTokens_TokenCountInvariant). Boşlukla ayrılmış liste
// birleştirmesi yalnız imza yolunda (CollapsePlaceholderLists).
//
// İdempotent: MaskVariableTokens(MaskVariableTokens(s)) == MaskVariableTokens(s)
// (saklanmış şablonun yeniden maskelenmesi — templater.Remask — buna dayanır).

import (
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// MaskerVersion — v0.10.1829: kalıp kimliği maskesinin sürümü. Kural kümesi
// kimlik ÜRETEN biçimde değişince artır: templater puller'ı system_settings
// `log_masker_version` blobundaki değerle karşılaştırır, farklıysa yeni
// sürümü yazar ve "yeni şablon" uyarılarını 24 sa susturur (persisted Drain
// kimlikleri değişen şablonlar için toptan "yeni" doğar). 1 = v0.10.1496–
// 1828 (MaskIDTokens son geçişli), 2 = bu dosya.
const MaskerVersion = 2

// kubeSuffixAlphabet — Kubernetes rand.String alfabesi (sesli harf ve 0/1/3
// yok; `k8s.io/apimachinery/pkg/util/rand`).
const kubeSuffixAlphabet = `bcdfghjklmnpqrstvwxz2456789`

// numberUnits — 9. adımda sayının ardından korunan birimler (küçük harf).
var numberUnits = map[string]bool{
	"ns": true, "us": true, "µs": true, "μs": true, "ms": true, "s": true, "sec": true, "secs": true,
	"m": true, "min": true, "mins": true, "h": true, "hr": true, "hrs": true, "d": true, "w": true,
	"b": true, "byte": true, "bytes": true, "k": true, "kb": true, "kib": true, "mb": true, "mib": true,
	"gb": true, "gib": true, "tb": true, "tib": true, "%": true, "rps": true, "qps": true, "ops": true,
}

// idTokenAllowlist — 10. adımın ASLA kimlik saymadığı sözlük belirteçleri
// (küçük harfle). Çoğu zaten hiçbir kurala takılmaz; liste, kural
// genişlediğinde sessizce maskelenmesinler diye açık
// (TestMaskVariableTokens_DictionaryTokensKept hepsini sınar).
var idTokenAllowlist = map[string]bool{
	"http2": true, "http11": true, "h2": true, "h2c": true, "utf8": true, "utf8mb3": true, "utf8mb4": true,
	"utf16": true, "utf16le": true, "utf16be": true, "utf32": true, "latin1": true,
	"sha1": true, "sha224": true, "sha256": true, "sha384": true, "sha512": true, "md5": true,
	"base32": true, "base58": true, "base64": true, "x86": true, "amd64": true, "arm64": true, "aarch64": true,
	"win32": true, "win64": true, "v1": true, "v2": true, "v3": true, "p50": true, "p90": true, "p95": true,
	"p99": true, "p999": true, "k8s": true, "s3": true, "ec2": true, "log4j": true, "log4j2": true,
	"oauth2": true, "i18n": true, "l10n": true, "e2e": true, "ipv4": true, "ipv6": true, "junit4": true,
	"junit5": true, "iso8601": true, "rfc3339": true, "rfc1123": true, "rfc2822": true, "pkcs1": true,
	"pkcs7": true, "pkcs8": true, "pkcs12": true, "x509": true, "ed25519": true, "x25519": true,
	"aes128": true, "aes256": true, "chacha20": true, "poly1305": true, "bzip2": true, "lz4": true,
	"mp3": true, "mp4": true, "h264": true, "h265": true, "tls12": true, "tls13": true, "sslv3": true,
}

type varRules struct {
	repl     string
	uuid     *regexp.Regexp
	isoT     *regexp.Regexp
	hms      *regexp.Regexp
	email    *regexp.Regexp
	ipv4     *regexp.Regexp
	ipv6Br   *regexp.Regexp
	brPort   *regexp.Regexp
	ipv6     *regexp.Regexp
	javaHash *regexp.Regexp
	pod      *regexp.Regexp
	fmtBrace *regexp.Regexp
	fmtPct   *regexp.Regexp
	number   *regexp.Regexp
	quotedD  *regexp.Regexp
	quotedS  *regexp.Regexp
	collapse *regexp.Regexp
	brace    *regexp.Regexp
	urn      *regexp.Regexp
	listRun  *regexp.Regexp
}

var varRuleCache sync.Map // repl → *varRules

func rulesFor(repl string) *varRules {
	if r, ok := varRuleCache.Load(repl); ok {
		return r.(*varRules)
	}
	p := regexp.QuoteMeta(repl)
	h4, h8, h12 := `[0-9a-fA-F]{4}`, `[0-9a-fA-F]{8}`, `[0-9a-fA-F]{12}`
	g := func(h string) string { return `(?:` + h + `|` + p + `)` }
	hexg := `(?:[0-9a-fA-F]{1,4}|` + p + `)`
	ip6 := `(?:` + hexg + `?:){2,7}` + hexg + `?`
	k := `[` + kubeSuffixAlphabet + `]`
	ks := `(` + k + `{5,10}|` + p + `)`
	r := &varRules{
		repl:     repl,
		uuid:     regexp.MustCompile(g(h8) + `-` + g(h4) + `-` + g(h4) + `-` + g(h4) + `-` + g(h12)),
		isoT:     regexp.MustCompile(`\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:[Zz]|[+-]\d{2}:?\d{2})?`),
		hms:      regexp.MustCompile(`\b\d{1,2}:\d{2}:\d{2}(?:[.,]\d+)?\b`),
		email:    regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}\b`),
		ipv4:     regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d{1,5})?\b`),
		ipv6Br:   regexp.MustCompile(`\[(` + ip6 + `)\](?::\d{1,5})?`),
		ipv6:     regexp.MustCompile(ip6),
		brPort:   regexp.MustCompile(`\[` + p + `\]:(?:\d{1,5}|` + p + `)`),
		javaHash: regexp.MustCompile(`[A-Za-z0-9_$\]]@([0-9a-f]{4,8})`),
		pod:      regexp.MustCompile(`-` + ks + `(?:-` + ks + `)?`),
		fmtBrace: regexp.MustCompile(`\{\d*\}`),
		fmtPct:   regexp.MustCompile(`%[sdvfi]|\?`),
		number: regexp.MustCompile(`(?:\d{1,3}(?:[\x{00A0}\x{202F}\x{2009}]\d{3})+|(?:\d+|` + p + `)(?:[.,](?:\d+|` + p + `))*)` +
			`([A-Za-zµμ%]*)`),
		quotedD:  regexp.MustCompile(`"([^"\\\s]{1,256})"`),
		quotedS:  regexp.MustCompile(`'([^'\\\s]{1,256})'`),
		collapse: regexp.MustCompile(p + `(?:[-_.:/+=#,@\x{00A0}\x{202F}\x{2009}]*` + p + `)+`),
		brace:    regexp.MustCompile(`\{` + p + `\}`),
		urn:      regexp.MustCompile(`(?i:urn:uuid:)` + p),
		listRun:  regexp.MustCompile(p + `(?:\s*[,;]\s*` + p + `)+`),
	}
	v, _ := varRuleCache.LoadOrStore(repl, r)
	return v.(*varRules)
}

// MaskVariableTokens — v0.10.1829: kalıp kimliği için değişken belirteçleri
// repl ile değiştirir (kural sırası dosya başında). SAF, idempotent, boşluk
// sayısını korur. NormalizeSignature ("<x>") ve templater.Mask ("<*>") bunu
// çağırır.
func MaskVariableTokens(s, repl string) string {
	if s == "" || repl == "" {
		return s
	}
	r := rulesFor(repl)
	hasDigit := strings.ContainsAny(s, "0123456789")
	hasP := strings.Contains(s, repl)
	// Ucuz ön süzgeçler (bayt sayımı): kural gereken ayraç yoksa regex koşmaz.
	dashes, colons := strings.Count(s, "-"), strings.Count(s, ":")
	if (hasDigit || hasP) && dashes >= 4 {
		s = replaceChecked(s, r.uuid, func(s string, loc []int) (string, bool) {
			return repl, !alnumAt(s, loc[1])
		})
	}
	if hasDigit && colons >= 2 {
		if dashes >= 2 {
			s = r.isoT.ReplaceAllString(s, repl)
		}
		s = r.hms.ReplaceAllString(s, repl)
	}
	if strings.IndexByte(s, '@') >= 0 {
		s = r.email.ReplaceAllString(s, repl)
	}
	if hasDigit && strings.Count(s, ".") >= 3 {
		s = r.ipv4.ReplaceAllString(s, repl)
	}
	if colons >= 2 && (hasDigit || hasP) {
		s = replaceChecked(s, r.ipv6Br, func(s string, loc []int) (string, bool) {
			return repl, validIPv6(s[loc[2]:loc[3]], repl)
		})
		s = replaceChecked(s, r.ipv6, func(s string, loc []int) (string, bool) {
			if !validIPv6(s[loc[0]:loc[1]], repl) {
				return "", false
			}
			if pr, ok := prevRune(s, loc[0]); ok && (isWordRune(pr) || pr == ':' || pr == '.') {
				return "", false
			}
			if nr, ok := nextRune(s, loc[1]); ok && (isWordRune(nr) || nr == ':' || nr == '.') {
				return "", false
			}
			return repl, true
		})
	}
	if hasDigit && strings.IndexByte(s, '@') >= 0 {
		s = replaceChecked(s, r.javaHash, func(s string, loc []int) (string, bool) {
			if alnumAt(s, loc[1]) {
				return "", false
			}
			return s[loc[0]:loc[2]] + repl, true
		})
	}
	if (hasDigit || hasP) && dashes >= 1 {
		s = replaceChecked(s, r.pod, func(s string, loc []int) (string, bool) {
			return "-" + repl, podSuffixMatch(s, loc, repl)
		})
	}
	if strings.ContainsAny(s, "{%?") {
		s = r.fmtBrace.ReplaceAllString(s, repl)
		s = replaceChecked(s, r.fmtPct, func(s string, loc []int) (string, bool) {
			return repl, formatPlaceholderAt(s, loc[0], loc[1])
		})
	}
	if strings.ContainsAny(s, `"'`) {
		quoted := func(q string) func(s string, loc []int) (string, bool) {
			return func(s string, loc []int) (string, bool) {
				if !quotedValueIsID(s[loc[2]:loc[3]], repl) {
					return "", false
				}
				return q + repl + q, true
			}
		}
		s = replaceChecked(s, r.quotedD, quoted(`"`))
		s = replaceChecked(s, r.quotedS, quoted(`'`))
	}
	if hasDigit || strings.Contains(s, repl) {
		s = replaceChecked(s, r.number, func(s string, loc []int) (string, bool) {
			return numberReplacement(s, loc, repl)
		})
	}
	s = MaskIDTokens(s, repl)
	if strings.Contains(s, repl) {
		// Birleştirme kararlı olana dek; UUID ve pod kuralları da yeniden:
		// sonraki adımların ürettiği yer tutucu bir grubu tamamlayabilir
		// (`550e8400-…-4466554400001` → `<x>-e29b-41d4-a716-<x>`), idempotentlik
		// buna dayanır.
		for i := 0; i < 4; i++ {
			next := replaceChecked(s, r.uuid, func(s string, loc []int) (string, bool) {
				return repl, !alnumAt(s, loc[1])
			})
			next = replaceChecked(next, r.pod, func(s string, loc []int) (string, bool) {
				return "-" + repl, podSuffixMatch(s, loc, repl)
			})
			// `[::1]:8080` Drain'in köşeli parantez kuralından `[<*>]:<*>` olarak
			// gelir; yalnız portluysa tek yer tutucu (çıplak `[<*>]` MDC
			// kuralının çıktısıdır, dokunulmaz).
			next = r.brPort.ReplaceAllString(next, repl)
			next = r.collapse.ReplaceAllString(next, repl)
			next = r.brace.ReplaceAllString(next, repl)
			next = r.urn.ReplaceAllString(next, repl)
			if next == s {
				break
			}
			s = next
		}
	}
	return s
}

// CollapsePlaceholderLists — v0.10.1829 (yalnız imza yolu): virgül/noktalı
// virgülle ayrılmış yer tutucu listesi (`<x>, <x>, <x>`) tek yer tutucu.
// Drain'de KULLANILMAZ: belirteç sayısını değiştirir (Drain'in köşeli
// parantez kuralı `[1, 2, 3]` → `[<*>]` zaten tek belirteç yapar).
func CollapsePlaceholderLists(s, repl string) string {
	if !strings.Contains(s, repl) {
		return s
	}
	return rulesFor(repl).listRun.ReplaceAllString(s, repl)
}

// replaceChecked — re eşleşmelerini fn kabul ederse fn'in döndürdüğüyle
// değiştirir (RE2'de bakış-arkası/ilerisi yok; sınır denetimi fn'de).
func replaceChecked(s string, re *regexp.Regexp, fn func(s string, loc []int) (string, bool)) string {
	locs := re.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	last, changed := 0, false
	for _, loc := range locs {
		rep, ok := fn(s, loc)
		if !ok {
			continue
		}
		if !changed {
			b.Grow(len(s))
		}
		b.WriteString(s[last:loc[0]])
		b.WriteString(rep)
		last, changed = loc[1], true
	}
	if !changed {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func prevRune(s string, i int) (rune, bool) {
	if i <= 0 {
		return 0, false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return r, true
}

func nextRune(s string, i int) (rune, bool) {
	if i >= len(s) {
		return 0, false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r, true
}

// alnumAt — s[i]'de harf/rakam başlıyor mu (eşleşmenin sağ sınırı).
func alnumAt(s string, i int) bool {
	r, ok := nextRune(s, i)
	return ok && isSigAlnum(r)
}

// validIPv6 — aday bir IPv6 mı: `::` içeriyorsa ≤8 grup ve en az bir dolu
// grup; yoksa 6–8 dolu grup (tam biçim ya da MAC). Her iki durumda en az
// bir rakam ya da yer tutucu (`a::b` gibi sözcük kalıntıları dışarıda).
func validIPv6(c, repl string) bool {
	if strings.Count(c, "::") > 1 || strings.Contains(c, ":::") {
		return false
	}
	groups := strings.Split(c, ":")
	if len(groups) > 9 {
		return false
	}
	filled := 0
	for _, g := range groups {
		if g != "" {
			filled++
		}
	}
	if !strings.ContainsAny(c, "0123456789") && !strings.Contains(c, repl) {
		return false
	}
	if strings.Contains(c, "::") {
		return filled >= 1
	}
	return filled >= 6 && filled == len(groups)
}

// podSuffixMatch — 6. adımın kabul denetimi: `-` öncesi harf/rakam, eşleşme
// sonrası harf/rakam/`_`/`-` değil, en az bir GERÇEK bölüm rakam içerir.
func podSuffixMatch(s string, loc []int, repl string) bool {
	if pr, ok := prevRune(s, loc[0]); !ok || !isSigAlnum(pr) {
		return false
	}
	if nr, ok := nextRune(s, loc[1]); ok && (isWordRune(nr) || nr == '-') {
		return false
	}
	digit := false
	for i := 2; i+1 < len(loc); i += 2 {
		if loc[i] < 0 {
			continue
		}
		seg := s[loc[i]:loc[i+1]]
		if seg != repl && strings.ContainsAny(seg, "2456789") {
			digit = true
		}
	}
	return digit
}

// formatPlaceholderAt — `%s`/`%d`/… ya da SQL bind `?` tek başına mı?
// `%`: önünde harf/rakam/`%` yok, ardında harf/rakam yok. `?`: önünde
// boşluk / `(` / `,` / `=` (ya da satır başı), ardında boşluk / `)` / `,` /
// `;` (ya da satır sonu) — cümle sonu soru işareti ("ready?") dışarıda.
func formatPlaceholderAt(s string, start, end int) bool {
	pr, hasPrev := prevRune(s, start)
	nr, hasNext := nextRune(s, end)
	if s[start] == '?' {
		if hasPrev && !(unicode.IsSpace(pr) || pr == '(' || pr == ',' || pr == '=') {
			return false
		}
		return !hasNext || unicode.IsSpace(nr) || nr == ')' || nr == ',' || nr == ';'
	}
	if hasPrev && (isSigAlnum(pr) || pr == '%') {
		return false
	}
	return !hasNext || !isSigAlnum(nr)
}

// numberReplacement — 9. adım: sözcük-sınırlı sayı (+birim) → repl(+birim).
func numberReplacement(s string, loc []int, repl string) (string, bool) {
	start, end := loc[0], loc[1]
	unit := s[loc[2]:loc[3]]
	core := s[start:loc[2]]
	if core == repl && unit == "" {
		return "", false // yalnız yer tutucu: dokunma
	}
	if !strings.HasPrefix(core, repl) {
		if pr, ok := prevRune(s, start); ok && isWordRune(pr) {
			return "", false
		}
		if start >= 5 && strings.EqualFold(s[start-5:start], "HTTP/") {
			return "", false // protokol sürümü (HTTP/1.1, HTTP/2) kimlik değil
		}
	}
	if unit != "" && !numberUnits[strings.ToLower(unit)] {
		return "", false // sayıya yapışık sözcük (`3DS`, `2fa`, `7d9f6c`): 10. adıma kalır
	}
	if nr, ok := nextRune(s, end); ok && isWordRune(nr) {
		return "", false
	}
	return repl + unit, true
}

// quotedValueIsID — boşluksuz bir tırnak içeriği, ayraçları atılınca kimlik
// mi? İçerik zaten bir yer tutucu taşıyorsa (yeniden maskeleme ya da Drain
// ön kurallarının çıktısı) HAYIR: tırnak kuralı ham içerikte karar verir,
// kısmen maskelenmiş içeriği (`"<x>ms"`) yutmak idempotentliği bozardı.
func quotedValueIsID(v, repl string) bool {
	if strings.Contains(v, repl) {
		return false
	}
	var b strings.Builder
	for _, r := range v {
		if isSigAlnum(r) {
			b.WriteRune(r)
		}
	}
	return IsIDLikeToken(b.String())
}
