package api

// chat_service_pattern.go — v0.10.2471 (operator-reported 2026-11-26):
// "harvester-* ile başlayan servisleri incele", "harvester ile başlayan
// servisler nasıl", "harvester-* servislerinde sorun var mı", "adı payment
// içeren servisler", "-batch ile biten servisler" — CoSRE bunun bir SERVİS
// KÜMESİ adlandırdığını anlamıyordu (kaynak sorusu / endpoint kartı / tek
// servis araması).
//
// Üç SAF parça burada, okumalar chat_service_set.go'da:
//
//  1. parseServicePattern — önek / sonek / içeren / glob kalıbı (Türkçe ve
//     İngilizce deyiş + çıplak yıldızlı jeton) → {kind, pattern}. Sıradan tek
//     servis sorusunda, URL / yol / PromQL / SQL içindeki yıldızda ve başka bir
//     nesneyi (endpoint, pod, log, topic…) niteleyen deyişte ATEŞLEMEZ.
//  2. matchServicePattern + planServiceSet — kalıp canlı kataloğa (span adları
//     ∪ metrik-yalnız servisler; guidedServiceNames, 60 sn önbellek) büyük /
//     küçük harf duyarsız uygulanır. 0 → "yok" + en yakın adlar (tahmin yok);
//     1 → o tek servis; 2..N → küme; N > servicePatternCap → hata, sonra trafik
//     sırasıyla ilk servicePatternCap, kalan sayı söylenir, daraltma istenir.
//  3. serviceSetFollowUp — küme konuşmanın kapsamıyken "hangisi en yavaş",
//     "hata verenleri göster" gibi devam soruları aynı kümede yeniden koşar.
//
// Küme YENİ bir mekanizma değil: konuşma bağlamında (ChatContext.ServiceSet)
// durur ve v2 döngüsüne kullanıcı kapsamının yerine (userScope, neden
// "pattern") iner — başlık rozeti "Kapsam: harvester-* · 6 servis", `services`
// argümanı enjeksiyonu ve kapsam kararı takım kapsamıyla AYNI yoldan.

import (
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	svcPatPrefix   = "prefix"
	svcPatSuffix   = "suffix"
	svcPatContains = "contains"
	svcPatGlob     = "glob"
)

const (
	// servicePatternCap — bir turda kümede incelenen servis tavanı. Gerekçe:
	// örneklenmemiş RED okuyucusunun (buildServicesREDMetric) servis seçicisi
	// `svc=~"a|b|…"` ve tavanı 100 ad; 50 ad seçiciyi, `services` argümanı
	// enjeksiyonunu ve cevap tablosunu küçük yerel modelin bağlamında tutar ve
	// Services sayfasının sayfa boyuyla aynıdır. Aşan kalıp hata + trafik
	// sırasıyla ilk 50'ye iner; kalan sayı cevapta söylenir.
	servicePatternCap = 50
	// servicePatternRankMax — tavanı aşan kalıpta sıralamaya giren eşleşme
	// tavanı (alfabetik ilk N; "a*" gibi bir kalıp bütün filoyu IN listesine
	// koymasın).
	servicePatternRankMax = 1000
	// servicePatternNearMax — "yok" cevabındaki en yakın ad sayısı.
	servicePatternNearMax = 5
	// serviceSetTTL — kümenin konuşma kapsamı olarak ömrü (son kullanımdan).
	serviceSetTTL = 2 * time.Hour
)

// svcPattern — sorudaki servis adı kalıbı.
type svcPattern struct {
	Kind string
	// Pattern — küçük harf. prefix / suffix / contains: çıplak parça
	// ("harvester-"); glob: yıldızlı desen ("pay*-api").
	Pattern string
	// Explicit — deyiş servis sözcüğüne bağlı ("… servisler"). Değilse kalıp
	// YUMUŞAKTIR: katalogda eşleşme yoksa kademe turu hiç sahiplenmez.
	Explicit bool
	// Start / End — kalıp deyişinin özgün metindeki bayt aralığı.
	Start, End int
}

// Label — kalıbın insan etiketi (glob yazımı).
func (p svcPattern) Label() string {
	switch p.Kind {
	case svcPatPrefix:
		return p.Pattern + "*"
	case svcPatSuffix:
		return "*" + p.Pattern
	case svcPatContains:
		return "*" + p.Pattern + "*"
	}
	return p.Pattern
}

// KindTR — cevap metnindeki tür adı.
func (p svcPattern) KindTR() string {
	switch p.Kind {
	case svcPatPrefix:
		return "önek"
	case svcPatSuffix:
		return "sonek"
	case svcPatContains:
		return "içeren"
	}
	return "desen"
}

// match — SAF: ad kalıba uyuyor mu (büyük / küçük harf duyarsız).
func (p svcPattern) match(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || p.Pattern == "" {
		return false
	}
	switch p.Kind {
	case svcPatPrefix:
		return strings.HasPrefix(n, p.Pattern)
	case svcPatSuffix:
		return strings.HasSuffix(n, p.Pattern)
	case svcPatContains:
		return strings.Contains(n, p.Pattern)
	}
	return globMatch(p.Pattern, n)
}

// globMatch — SAF: yalnız `*` joker (sıfır ya da daha çok karakter); regex yok.
func globMatch(pat, s string) bool {
	parts := strings.Split(pat, "*")
	if len(parts) == 1 {
		return pat == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, mid := range parts[1 : len(parts)-1] {
		if mid == "" {
			continue
		}
		i := strings.Index(s, mid)
		if i < 0 {
			return false
		}
		s = s[i+len(mid):]
	}
	return strings.HasSuffix(s, last)
}

// spQuote — isteğe bağlı tırnak (kesme işareti HARİÇ: Türkçe ek ayracı).
const spQuote = "[\"“”«»`]?"

// spTok — kalıp jetonu: servis adı karakterleri + yıldız.
const spTok = `([A-Za-z0-9._*-]+)`

// spName — "adı / adında / ismi / isminde" (i harfleri açık sınıf: (?i) I↔i
// katlar, ı ve İ katlanmaz).
const spName = `(?:servis\s+)?(?:ad[ıiİ]n[ıiİ]n\s+[ıiİ]ç[ıiİ]nde|ad[ıiİ]nda|adlar[ıiİ]nda|adlar[ıiİ]|ad[ıiİ]|[ıiİ]sm[ıiİ]nde|[ıiİ]s[ıiİ]mler[ıiİ]nde|[ıiİ]s[ıiİ]mler[ıiİ]|[ıiİ]sm[ıiİ])`

// spLead — jetondan önceki sınır.
const spLead = `(?:^|[\s(])`

type svcPatternRule struct {
	kind string
	re   *regexp.Regexp
	// named — deyiş bir "ad" sözcüğü taşıyor (tek başına yumuşak kabul nedeni).
	named bool
}

// svcPatternRules — deyiş kalıpları, öncelik sırasıyla. Her kuralda 1. grup =
// deyişin tamamı (sınır hariç), 2. grup = kalıp jetonu.
var svcPatternRules = []svcPatternRule{
	// "adı X içeren / adında X geçen" — içeren deyişi AD sözcüğü ister
	// ("url'inde X içeren" telemetri sorusudur, chat_telemetry_cue.go).
	{svcPatContains, regexp.MustCompile(`(?i)` + spLead + `(` + spName + `\s+` + spQuote + spTok + spQuote + `\s+(?:[ıiİ]çeren|geçen|gecen|bulunan)(?:ler[a-zçğıöşü]*)?)`), true},
	{svcPatPrefix, regexp.MustCompile(`(?i)` + spLead + `((?:` + spName + `\s+)?` + spQuote + spTok + spQuote + `(?:['’]l[ae]|\s+[ıiİ]le)\s+ba[şs]l[aıiİ]yan(?:lar[a-zçğıöşü]*)?)`), false},
	{svcPatSuffix, regexp.MustCompile(`(?i)` + spLead + `((?:` + spName + `\s+)?` + spQuote + spTok + spQuote + `(?:['’]l[ae]|\s+[ıiİ]le)\s+b[ıiİ]ten(?:ler[a-zçğıöşü]*)?)`), false},
	{svcPatPrefix, regexp.MustCompile(`(?i)` + spLead + `(` + spQuote + spTok + spQuote + `\s+(?:[öo]n\s?ekl[ıiİ]|prefixl[ıiİ]))`), false},
	{svcPatSuffix, regexp.MustCompile(`(?i)` + spLead + `(` + spQuote + spTok + spQuote + `\s+(?:son\s?ekl[ıiİ]|suffixl[ıiİ]))`), false},
	{svcPatPrefix, regexp.MustCompile(`(?i)` + spLead + `((?:start(?:s|ing)?|begin(?:s|ning)?)\s+with\s+` + spQuote + spTok + spQuote + `)`), false},
	{svcPatPrefix, regexp.MustCompile(`(?i)` + spLead + `((?:prefixed\s+(?:with|by)|with\s+(?:the\s+)?prefix)\s+` + spQuote + spTok + spQuote + `)`), false},
	{svcPatSuffix, regexp.MustCompile(`(?i)` + spLead + `((?:end(?:s|ing)?)\s+(?:with|in)\s+` + spQuote + spTok + spQuote + `)`), false},
	{svcPatSuffix, regexp.MustCompile(`(?i)` + spLead + `((?:suffixed\s+(?:with|by)|with\s+(?:the\s+)?suffix)\s+` + spQuote + spTok + spQuote + `)`), false},
	{svcPatContains, regexp.MustCompile(`(?i)` + spLead + `((?:names?\s+(?:contain(?:s|ing)?|includ(?:es?|ing)|like)|named\s+like)\s+` + spQuote + spTok + spQuote + `)`), true},
	{svcPatContains, regexp.MustCompile(`(?i)` + spLead + `(with\s+` + spQuote + spTok + spQuote + `\s+in\s+(?:the|their|its)\s+names?)`), true},
}

// spNameLeadRe — deyişin başındaki ad sözcüğü ("servis adı pay ile başlayan").
var spNameLeadRe = regexp.MustCompile(`(?i)^` + spName + `\s`)

// svcPatternQueryRe — sorgu metni (PromQL / SQL / LogQL): yıldız kalıp değildir.
var svcPatternQueryRe = regexp.MustCompile(`(?i)(\b(rate|irate|sum|avg|count|min|max|increase|topk|bottomk|histogram_quantile|select)\s*\(|\bselect\b.+\bfrom\b|\[\d+[smhdw]\])`)

// servicePatternBlocked — SAF: soru bir sorgu / URL yapıştırması; kalıp aranmaz.
func servicePatternBlocked(q string) bool {
	for _, m := range []string{"{", "}", "=~", "!~", "://", "```"} {
		if strings.Contains(q, m) {
			return true
		}
	}
	return svcPatternQueryRe.MatchString(q)
}

// validPatternLiteral — SAF: kalıbın yıldızsız parçası servis adı biçimli mi
// (en az 3 karakter, en az 2 harf; "*.go" / "*.example.com" gibi uzantı ve
// alan adı jokeri değil).
func validPatternLiteral(tok string) bool {
	if strings.Contains(tok, "**") || strings.HasPrefix(tok, "*.") {
		return false
	}
	lit := strings.ReplaceAll(tok, "*", "")
	if len(lit) < 3 {
		return false
	}
	letters := 0
	for _, r := range lit {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			letters++
		}
	}
	return letters >= 2
}

// patternFromToken — SAF: jeton + (deyişin) türü → kalıp. Yıldızlı jetonun
// türü yıldızın yerinden okunur; deyiş türü yalnız yıldızsız (ya da tek tür
// belirten) jetonda uygulanır.
func patternFromToken(tok, phraseKind string) (svcPattern, bool) {
	tok = strings.ToLower(tok)
	if !validPatternLiteral(tok) {
		return svcPattern{}, false
	}
	lit := strings.ReplaceAll(tok, "*", "")
	if phraseKind != "" {
		return svcPattern{Kind: phraseKind, Pattern: lit}, true
	}
	if !strings.Contains(tok, "*") {
		return svcPattern{}, false
	}
	head, tail := strings.HasPrefix(tok, "*"), strings.HasSuffix(tok, "*")
	inner := strings.Contains(strings.Trim(tok, "*"), "*")
	switch {
	case inner:
		return svcPattern{Kind: svcPatGlob, Pattern: tok}, true
	case head && tail:
		return svcPattern{Kind: svcPatContains, Pattern: lit}, true
	case tail:
		return svcPattern{Kind: svcPatPrefix, Pattern: lit}, true
	}
	return svcPattern{Kind: svcPatSuffix, Pattern: lit}, true
}

// Nesne adları (katlanmış önek): kalıbın NEYİ nitelediği.
var (
	svcNounStems  = []string{"servis", "service", "mikroservis", "microservice", "uygulama"}
	svcNounExact  = map[string]bool{"svc": true, "svcs": true, "app": true, "apps": true}
	svcOtherStems = []string{"endpoint", "operasyon", "operation", "rota", "route", "url", "uri", "path", "log", "trace", "span",
		"pod", "topic", "kuyruk", "queue", "metri", "namespace", "cluster", "küme", "kume", "takım", "takim", "team", "host",
		"sunucu", "server", "index", "indeks", "tablo", "table", "exception", "mesaj", "message", "deployment", "workload",
		"node", "veritaban", "database", "istek", "request", "sorgu", "query", "dosya", "file", "etiket", "label", "attribute",
		"kullanıcı", "kullanici", "user", "alarm", "alert", "dashboard", "container", "konteyner", "image", "branch", "repo"}
	svcNounFillers = map[string]bool{"olan": true, "tüm": true, "tum": true, "bütün": true, "butun": true, "all": true, "the": true,
		"of": true, "bir": true, "her": true, "şu": true, "bu": true, "o": true, "any": true, "every": true, "are": true, "is": true,
		"that": true, "which": true, "whose": true, "named": true, "name": true, "names": true, "adlı": true, "adli": true,
		"isimli": true, "adındaki": true, "adindaki": true}
)

// svcNounClass — SAF: jetonun nesne sınıfı: "svc" | "other" | "".
func svcNounClass(tok string) string {
	if svcNounExact[tok] {
		return "svc"
	}
	for _, st := range svcNounStems {
		if strings.HasPrefix(tok, st) {
			return "svc"
		}
	}
	for _, st := range svcOtherStems {
		if strings.HasPrefix(tok, st) {
			return "other"
		}
	}
	return ""
}

// nounFields — SAF: boşlukla ayrılmış alanların gövdeleri (kesme işaretinden
// sonraki Türkçe ek düşer: "url'inde" → "url").
func nounFields(text string) []string {
	var out []string
	for _, f := range strings.Fields(text) {
		if i := strings.IndexAny(f, "'’"); i > 0 {
			f = f[:i]
		}
		if toks := guidedTokens(normalizeGuidedMsg(f)); len(toks) > 0 {
			out = append(out, toks[0])
		}
	}
	return out
}

// nearestNoun — SAF: metnin (dolgu sözcükleri atlanarak) ilk / son alanının
// nesne sınıfı. last=true → sondan. Yalnız BİTİŞİK alan: "pay* topicleri"
// topic'i niteler, "harvester-* için podları göster" servis kümesini.
func nearestNoun(text string, last bool) string {
	toks := nounFields(text)
	if last {
		for i := len(toks) - 1; i >= 0; i-- {
			if svcNounFillers[toks[i]] {
				continue
			}
			return svcNounClass(toks[i])
		}
		return ""
	}
	for _, t := range toks {
		if svcNounFillers[t] {
			continue
		}
		return svcNounClass(t)
	}
	return ""
}

// patternScope — SAF: deyişin çevresi kalıbı bir servis kümesine mi bağlıyor.
// ok=false → kalıp başka bir nesneyi niteliyor (endpoint, pod, log…).
func patternScope(q string, start, end int) (explicit, ok bool) {
	after, before := nearestNoun(q[end:], false), nearestNoun(q[:start], true)
	switch {
	case after == "svc":
		return true, true
	case after == "other":
		return false, false
	case before == "svc":
		return true, true
	case before == "other":
		return false, false
	}
	return false, true
}

// trimGlobField — SAF: boşlukla ayrılmış alanın çevre işaretleri ve Türkçe eki
// soyulmuş hâli + özgün alandaki bayt aralığı. Alan yol / URL / sorgu
// karakteri taşıyorsa ok=false.
func trimGlobField(f string) (tok string, lo, hi int, ok bool) {
	lo, hi = 0, len(f)
	for lo < hi {
		r, n := utf8.DecodeRuneInString(f[lo:hi])
		if !strings.ContainsRune("\"“”«»`('‘", r) {
			break
		}
		lo += n
	}
	for lo < hi {
		r, n := utf8.DecodeLastRuneInString(f[lo:hi])
		if !strings.ContainsRune("\"“”«»`).,;:!?", r) {
			break
		}
		hi -= n
	}
	body := f[lo:hi]
	if i := strings.IndexAny(body, "'’"); i > 0 { // "harvester-*'lerde" → ek düşer
		suffix := body[i:]
		_, n := utf8.DecodeRuneInString(suffix)
		for _, r := range suffix[n:] {
			if !unicode.IsLetter(r) {
				return "", 0, 0, false
			}
		}
		body, hi = body[:i], lo+i
	}
	if body == "" || !strings.Contains(body, "*") {
		return "", 0, 0, false
	}
	for _, r := range body {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-', r == '*':
		default:
			return "", 0, 0, false // '/', ':', '=', '(' … → yol / URL / sorgu
		}
	}
	return body, lo, hi, true
}

// parseServicePattern — SAF: sorudaki İLK servis adı kalıbı. ok=false → soru
// bir küme adlandırmıyor (akış bayt bayt eski).
func parseServicePattern(q string) (svcPattern, bool) {
	if strings.TrimSpace(q) == "" || servicePatternBlocked(q) {
		return svcPattern{}, false
	}
	// 1) Deyiş kalıpları ("X ile başlayan", "adı X içeren", "starting with X").
	best := svcPattern{Start: -1}
	for _, rule := range svcPatternRules {
		m := rule.re.FindStringSubmatchIndex(q)
		if m == nil || (best.Start >= 0 && m[2] >= best.Start) {
			continue
		}
		tok := q[m[4]:m[5]]
		p, ok := patternFromToken(tok, rule.kind)
		if !ok {
			continue
		}
		explicit, scoped := patternScope(q, m[2], m[3])
		if !scoped {
			continue
		}
		lead := q[m[2]:m[4]] // deyişin jetondan önceki kısmı ("servis adı ")
		named := rule.named || spNameLeadRe.MatchString(lead)
		if nearestNoun(lead, false) == "svc" {
			explicit = true
		}
		// Deyiş servis sözcüğüne bağlı değilse yalnız ad sözcüğü, yıldız ya da
		// ayraçla biten parça ("harvester- ile başlayanlar") kabul nedenidir:
		// "deploy ile başlayan yavaşlama" bir servis kümesi değildir.
		shaped := strings.Contains(tok, "*") || strings.HasSuffix(tok, "-") || strings.HasSuffix(tok, "_") || strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "_")
		if !explicit && !named && !shaped {
			continue
		}
		p.Explicit, p.Start, p.End = explicit, m[2], m[3]
		best = p
	}
	if best.Start >= 0 {
		return best, true
	}
	// 2) Çıplak yıldızlı jeton ("harvester-*", "*-batch", "pay*-api").
	off := 0
	for _, f := range strings.Fields(q) {
		i := strings.Index(q[off:], f)
		if i < 0 {
			break
		}
		fs := off + i
		off = fs + len(f)
		tok, lo, hi, ok := trimGlobField(f)
		if !ok {
			continue
		}
		p, ok := patternFromToken(tok, "")
		if !ok {
			continue
		}
		explicit, scoped := patternScope(q, fs+lo, fs+hi)
		if !scoped {
			continue
		}
		p.Explicit, p.Start, p.End = explicit, fs+lo, fs+hi
		return p, true
	}
	return svcPattern{}, false
}

// matchServicePattern — SAF: katalogdaki eşleşen adlar (katalog sırası,
// tekrar yok).
func matchServicePattern(p svcPattern, names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] || !p.match(n) {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// patternCore — SAF: kalıbın en uzun harfli parçası (yakın ad araması için).
func patternCore(p svcPattern) string {
	best := ""
	for _, part := range strings.Split(p.Pattern, "*") {
		part = strings.Trim(part, "-_.")
		if len(part) > len(best) {
			best = part
		}
	}
	return best
}

// Küme çözümünün sonuçları.
const (
	setOutcomeNone   = "none"
	setOutcomeSingle = "single"
	setOutcomeSet    = "set"
	setOutcomeCapped = "capped"
)

// svcRank — tavanı aşan kalıpta sıralama girdisi (pencere toplamı).
type svcRank struct {
	Errors, Requests float64
}

// svcSetPlan — çözümün kararı.
type svcSetPlan struct {
	Outcome  string
	Services []string // set: eşleşenlerin tamamı; capped: ilk cap
	Total    int      // eşleşen servis sayısı
	LeftOut  int      // tavan dışında kalan
	Near     []string // none: en yakın adlar
	RankedBy string   // capped: "metric" | "span" | "name"
}

// planServiceSet — SAF karar tablosu. rank yalnız tavan aşıldığında okunur;
// nil / boş → alfabetik ilk cap ("sıra okunamadı" çağıranın notu). Sıra: hata
// azalan, sonra istek azalan, sonra ad.
func planServiceSet(p svcPattern, matched, catalogue []string, rank map[string]svcRank, rankSrc string, capN int) svcSetPlan {
	switch {
	case len(matched) == 0:
		return svcSetPlan{Outcome: setOutcomeNone, Near: nearNames(patternCore(p), catalogue, servicePatternNearMax)}
	case len(matched) == 1:
		return svcSetPlan{Outcome: setOutcomeSingle, Services: []string{matched[0]}, Total: 1}
	case len(matched) <= capN:
		return svcSetPlan{Outcome: setOutcomeSet, Services: append([]string(nil), matched...), Total: len(matched)}
	}
	order := append([]string(nil), matched...)
	sort.Strings(order)
	by := "name"
	if len(rank) > 0 {
		by = rankSrc
		sort.SliceStable(order, func(i, j int) bool {
			a, b := rank[order[i]], rank[order[j]]
			if a.Errors != b.Errors {
				return a.Errors > b.Errors
			}
			return a.Requests > b.Requests
		})
	}
	return svcSetPlan{Outcome: setOutcomeCapped, Services: order[:capN], Total: len(matched), LeftOut: len(matched) - capN, RankedBy: by}
}

// chatServiceSet — konuşmanın servis kümesi kapsamı (ChatContext.ServiceSet).
type chatServiceSet struct {
	Kind     string   `json:"kind"`
	Pattern  string   `json:"pattern"`
	Label    string   `json:"label"`
	Services []string `json:"services"`
	Total    int      `json:"total"`
	RankedBy string   `json:"rankedBy,omitempty"`
	At       int64    `json:"at"` // unix ns — son kullanım
}

func (c *chatServiceSet) leftOut() int {
	if c == nil || c.Total <= len(c.Services) {
		return 0
	}
	return c.Total - len(c.Services)
}

func (c *chatServiceSet) has(name string) bool {
	return c != nil && containsFold(c.Services, name)
}

// freshServiceSet — SAF: ömrü içindeki küme (değilse nil).
func freshServiceSet(c ChatContext, now time.Time) *chatServiceSet {
	s := c.ServiceSet
	if s == nil || len(s.Services) < 2 || s.At <= 0 || now.Sub(time.Unix(0, s.At)) > serviceSetTTL {
		return nil
	}
	return s
}

// noteServiceSet — kümeyi konuşma kapsamı yapar; tek servis öznesi (servis,
// operasyon, trace) kümeye taşınmaz. nil durum no-op.
func (st *chatContextState) noteServiceSet(set *chatServiceSet) {
	if st == nil {
		return
	}
	next := st.ctx
	next.ServiceSet = set
	if set != nil {
		next.Service, next.Services, next.Operation, next.Trace = "", nil, "", ""
	}
	if !chatContextEqual(st.ctx, next) {
		st.ctx, st.dirty = next, true
	}
}

// Küme sorusunun odağı (SetFocus).
const (
	setFocusSlow     = "slow"
	setFocusErrors   = "errors"
	setFocusTraffic  = "traffic"
	setFocusProblems = "problems"
)

// serviceSetFocus — SAF: sorunun küme içindeki odağı ("" = genel bakış).
func serviceSetFocus(norm string, toks []string) string {
	switch {
	case containsPhrase(norm, "en yavaş") || containsPhrase(norm, "en yavas") || tokenHasPrefix(toks, "yavaş", "yavas", "slow", "p99", "p95", "gecik", "latency"):
		return setFocusSlow
	case containsPhrase(norm, "en yoğun") || containsPhrase(norm, "en yogun") || containsPhrase(norm, "en çok istek") ||
		containsPhrase(norm, "en çok trafik") || tokenHasPrefix(toks, "trafi", "busiest", "throughput"):
		return setFocusTraffic
	case hasErrorSignal(toks):
		return setFocusErrors
	case containsPhrase(norm, "açık problem") || containsPhrase(norm, "acik problem") || tokenHasPrefix(toks, "problem", "alarm"):
		return setFocusProblems
	}
	return ""
}

// serviceSetOtherAsk — SAF: soru genel bakışla cevaplanamayan bir nesneyi
// (log, trace, deploy, pod, exception, endpoint, DB, kuyruk) ya da nedeni
// soruyor → v2'de küme kapsamıyla serbest döngü.
func serviceSetOtherAsk(toks []string) bool {
	return hasWhySignal(toks) || hasLogSignal(toks) || hasDeploySignal(toks) || hasPodSignal(toks) || hasExceptionWord(toks) ||
		hasDBSignal(toks) || hasMessagingSignal(toks) ||
		tokenHasPrefix(toks, "trace", "span", "endpoint", "operasyon", "operation", "rota", "route", "grafik", "chart", "metri", "cpu", "memory", "bellek", "kök", "kok")
}

// setClearPhrases — kümeyi konuşma kapsamından düşüren açık deyişler.
var setClearPhrases = []string{"tüm servisler", "tum servisler", "bütün servisler", "butun servisler", "tüm filo", "filo geneli",
	"all services", "whole fleet", "kapsamı kaldır", "kapsami kaldir", "kapsamı temizle", "kapsami temizle", "kapsamı sıfırla",
	"kapsamsız", "kapsamsiz", "clear scope"}

// clearsServiceSet — SAF: soru kümeyi açıkça bırakıyor mu.
func clearsServiceSet(norm string) bool {
	for _, p := range setClearPhrases {
		if strings.Contains(norm, p) {
			return true
		}
	}
	return false
}

// setDeixisPhrases — kümeyi işaret eden deyişler (çoğul; "bu servis" tekildir).
var setDeixisPhrases = []string{"bu servisler", "o servisler", "aynı servisler", "ayni servisler", "bunlar", "onlar", "aralarında",
	"aralarinda", "içlerinde", "iclerinde", "bu küme", "bu kume", "kümede", "kumede", "these services", "those services", "among them", "of them"}

// serviceSetFollowUp — SAF: kalıp taşımayan soru etkin kümenin devamı mı.
// lastSet: son cevaplanan rota küme bakışıydı (çapa). Çapasız turda yalnız
// kümeyi AÇIKÇA işaret eden deyiş devam sayılır ("bu servislerden hangisi…").
func serviceSetFollowUp(q string, lastSet, namesService bool) (focus string, ok bool) {
	norm := normalizeGuidedMsg(strings.TrimSpace(q))
	toks := guidedTokens(norm)
	if len(toks) == 0 || namesService || clearsServiceSet(norm) || serviceSetOtherAsk(toks) { // v0.10.2472 — uzunluk sınırı yok (kapsam devamı)
		return "", false
	}
	deixis := false
	for _, p := range setDeixisPhrases {
		if strings.Contains(norm, p) {
			deixis = true
			break
		}
	}
	focus = serviceSetFocus(norm, toks)
	which := tokenHasPrefix(toks, "hangisi", "hangiler", "which")
	switch {
	case deixis:
		return focus, true
	case lastSet && focus != "" && (which || anyToken(toks, mutationVerbs...) || anyToken(toks, onlyWords...) || tokenHasPrefix(toks, "sırala", "sirala", "olanlar", "verenler", "alanlar")):
		return focus, true
	}
	return "", false
}

// rewriteSinglePattern — SAF: tek eşleşmede kalıp deyişi servisin adıyla
// değişir; soru o servis yazılmış gibi yönlenir.
func rewriteSinglePattern(q string, p svcPattern, name string) string {
	if p.Start < 0 || p.End > len(q) || p.Start >= p.End {
		return name + " " + q
	}
	return strings.TrimSpace(q[:p.Start] + name + q[p.End:])
}

// servicePatternNoneTR — SAF: eşleşme yok cevabı + çipler. Ad UYDURULMAZ:
// yalnız katalogdaki en yakın adlar önerilir.
func servicePatternNoneTR(p svcPattern, scanned int, near []string) (string, []string) {
	var b strings.Builder
	b.WriteString("Adı «" + p.Label() + "» kalıbına (" + p.KindTR() + ") uyan servis bulamadım")
	if scanned > 0 {
		b.WriteString(" — servis kataloğundaki " + fmtThousands(uint64(scanned)) + " ad tarandı (span + metrik)")
	}
	b.WriteString(".")
	if len(near) == 0 {
		b.WriteString(" Yakın bir ad da yok; kalıbı ya da servis adını kontrol eder misin?")
		return b.String(), nil
	}
	b.WriteString(" En yakın adlar:\n")
	chips := make([]string, 0, len(near))
	for _, n := range near {
		b.WriteString("- " + n + "\n")
		chips = append(chips, n+" sağlığı nasıl?")
	}
	b.WriteString("Bunlardan birini mi kastettin, yoksa kalıbı değiştirmek ister misin?")
	return b.String(), chips
}
