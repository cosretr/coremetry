package api

// answer_verify.go — v0.10.2327 (docs/cosre/conversation-audit.md §6.1 E1,
// §7 kararları 6 ve 7): CoSRE cevap doğrulayıcısı, SAF çekirdek.
//
// İlke (operatör): BULGULAR SABİT, CÜMLELER SERBEST — model istediği gibi
// anlatır; kod, cevaptaki her sayının, yüzdenin, saatin, tarihin ve
// servis / istemci / pod / operasyon / exception adının o turun kanıtında
// bulunup bulunmadığını sayar. Bu dosya yalnız hesaplar; cevabı değiştirmez,
// akışa dokunmaz (bağlantı answer_verify_turn.go'da, GÖLGE kipte).
//
// Çekirdek numeric_claims.go'dan gelir (aynı jeton okuyucu numTokenEnd, aynı
// ayraç kuralları numCandidates, aynı kanıt sayıları evidenceNumbers). O
// dosya ve trace takibinin uyarısı DEĞİŞMEDİ; burası onun genişletilmiş
// ikizidir:
//
//   - birim dönüşümü: ms / s / sn / saniye / dk / dakika / sa / saat → ms;
//     yüzde ↔ oran (%12 ↔ 0.12);
//   - büyüklük sözcüğü (bin / milyon / milyar), sayı sözcüğü (bir…on,
//     yarım / yarı; YALNIZ ardından büyüklük ya da süre birimi gelirse —
//     "bir servis" iddia değildir), Türkçe ondalık virgül ve binlik ayraç;
//   - tolerans (karar 7): yazılan son basamağın yarım birimi ("74 bin" →
//     73.500–74.499; "3,6" → 3,55–3,65; düz tamsayı kesin); "yaklaşık",
//     "civarı", "kadar", "~" varsa ayrıca ±%5;
//   - türetilmiş değer: doğrudan kanıtta yok ama kanıttaki iki değerin
//     toplamı / farkı / oranı (yüzde değişim dahil) → "derived"; ne
//     doğrulandı ne uyuşmadı sayılır. Havuz dar tutulur (cevabın doğrulanan
//     değerleri + defter + kanıtta en çok 24 değer varsa hepsi): geniş
//     havuzda her küçük sayı bir orana denk gelir ve uyuşmazlık kaybolur;
//   - saat ve tarih: kanıttaki damgalar (ISO, epoch s/ms/µs/ns, kanıt
//     metnindeki yerel "HH:MM") kullanıcının saat dilimine çevrilir — dilim
//     kuralı çözücüyle AYNI (chatLocationNamed: tarayıcı adı → ofset →
//     kurulum varsayılanı); tolerans ±1 dk ya da kanıt kovalıysa bir kova;
//   - adlar: ters tırnaklı tanımlayıcı, exception biçimi (…Exception /
//     …Error), "GET /yol" operasyonu, pod biçimi ve katalogdaki servis adı;
//     kanıt metni ∪ konuşma durumu ∪ operatörün kendi yazdığıyla eşlenir.
//
// Kademe: "general" (araçsız genel cevap) canlı veri iddiası taşımamalı —
// orada yalnız yüzde, süre, saat, tarih ve katalog servis adı denetlenir;
// düz sayı (HTTP 503, port 8080) genel bilgidir, sayılmaz.

import (
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// İddia türleri (sayaç anahtarları; sıra raporda sabit).
const (
	verifyKindNumber   = "number"
	verifyKindPercent  = "percent"
	verifyKindDuration = "duration"
	verifyKindTime     = "time"
	verifyKindDate     = "date"
	verifyKindName     = "name"
)

var verifyKinds = []string{verifyKindNumber, verifyKindPercent, verifyKindDuration, verifyKindTime, verifyKindDate, verifyKindName}

// Sonuçlar.
const (
	verifyVerified = "verified"
	verifyDerived  = "derived"
	verifyMismatch = "mismatch"
)

// Anlatım kademeleri.
const (
	verifyTierLoop    = "loop"
	verifyTierGuided  = "guided"
	verifyTierGeneral = "general"
)

// verifyDerivedPoolMax — türetme havuzunun tavanı (çift sayısı ≈ n²).
const verifyDerivedPoolMax = 24

// verifyClaim — cevaptaki bir iddia ve sonucu.
type verifyClaim struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Status string `json:"status"`
}

// verifyEvidence — turun kanıtı (çağıran toplar).
type verifyEvidence struct {
	Texts   []string    // araç argümanı + TAM çıktı, guided kanıt bloğu, önsözler, operatör metni
	Numbers []float64   // ek sayılar: defter değerleri, pencere saniyesi
	Names   []string    // durum adları (servis, operasyon, trace …)
	Times   []time.Time // pencere başı / sonu, turun anı
	Loc     *time.Location
}

// verifyCounts — tür başına sayaç.
type verifyCounts struct{ Checked, Verified, Derived, Mismatch int }

// verifyResult — turun doğrulama sonucu.
type verifyResult struct {
	Tier   string        `json:"tier"`
	Claims []verifyClaim `json:"claims"`
}

// totals — SAF: toplam ve tür başına sayaç.
func (r verifyResult) totals() (verifyCounts, map[string]verifyCounts) {
	var t verifyCounts
	by := map[string]verifyCounts{}
	for _, c := range r.Claims {
		k := by[c.Kind]
		k.Checked++
		t.Checked++
		switch c.Status {
		case verifyVerified:
			k.Verified++
			t.Verified++
		case verifyDerived:
			k.Derived++
			t.Derived++
		case verifyMismatch:
			k.Mismatch++
			t.Mismatch++
		}
		by[c.Kind] = k
	}
	return t, by
}

// mismatches — SAF: uyuşmayan iddialar (geliş sırası).
func (r verifyResult) mismatches() []verifyClaim {
	var out []verifyClaim
	for _, c := range r.Claims {
		if c.Status == verifyMismatch {
			out = append(out, c)
		}
	}
	return out
}

// compact — SAF: kayıt satırının kısa biçimi (≤ ~200 B):
// "v1 tier=loop number=3/2/1/0 time=1/1/0/0" (denetlenen/doğrulanan/türetilen/uyuşmayan).
func (r verifyResult) compact() string {
	_, by := r.totals()
	var b strings.Builder
	b.WriteString("v1 tier=")
	b.WriteString(r.Tier)
	for _, k := range verifyKinds {
		c, ok := by[k]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, " %s=%d/%d/%d/%d", k, c.Checked, c.Verified, c.Derived, c.Mismatch)
	}
	return b.String()
}

// ── giriş ────────────────────────────────────────────────────────────────

// verifyAnswer — SAF: cevabın iddiaları ve kanıta göre sonuçları. catalog
// yalnız ad TESPİTİ içindir (hangi sözcük bir servis adı); kanıt değildir.
func verifyAnswer(tier, answer string, ev verifyEvidence, catalog []string) verifyResult {
	res := verifyResult{Tier: tier}
	text := verifyStripFences(answer)
	if strings.TrimSpace(text) == "" {
		return res
	}
	p := prepareVerifyEvidence(ev)
	general := tier == verifyTierGeneral

	names, text := verifyNameClaims(text, catalog, general)
	for _, n := range names {
		st := verifyMismatch
		if p.hasName(n) {
			st = verifyVerified
		}
		res.Claims = append(res.Claims, verifyClaim{Kind: verifyKindName, Text: n, Status: st})
	}
	text = verifyMaskLinks(text)
	text = verifyMaskIPs(text) // v0.10.2356 — istemci IP'si sayı / saat iddiası değildir
	dates, text := verifyDateClaims(text)
	for _, d := range dates {
		st := verifyMismatch
		if p.hasDate(d) {
			st = verifyVerified
		}
		res.Claims = append(res.Claims, verifyClaim{Kind: verifyKindDate, Text: d.text, Status: st})
	}
	times, text := verifyTimeClaims(text)
	for _, t := range times {
		st := verifyMismatch
		if p.hasMinute(t.minute) {
			st = verifyVerified
		}
		res.Claims = append(res.Claims, verifyClaim{Kind: verifyKindTime, Text: t.text, Status: st})
	}
	nums := verifyNumberClaims(text, general)
	status := make([]string, len(nums))
	var pool []float64
	for i, c := range nums {
		if e, ok := p.matchNumber(c); ok {
			status[i] = verifyVerified
			pool = append(pool, e)
		}
	}
	pool = append(pool, ev.Numbers...)
	if len(p.nums) <= verifyDerivedPoolMax {
		pool = append(pool, p.nums...)
	}
	pool = dedupeFloats(pool, verifyDerivedPoolMax*2)
	for i, c := range nums {
		if status[i] != "" {
			continue
		}
		status[i] = verifyMismatch
		if verifyDerivedMatch(c, pool) {
			status[i] = verifyDerived
		}
	}
	for i, c := range nums {
		res.Claims = append(res.Claims, verifyClaim{Kind: c.kind, Text: c.text, Status: status[i]})
	}
	return res
}

// ── kanıt hazırlığı ──────────────────────────────────────────────────────

type verifyPrepared struct {
	nums    []float64 // tekil mutlak değerler
	lower   string    // küçük harf kanıt metni + durum adları
	minutes []int     // yerel gün dakikası (tekil, sıralı)
	bucket  int       // kanıt kovası (dk); 0 = kovasız
	dates   map[[3]int]bool
}

var (
	verifyISORe   = regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{1,2}):(\d{2})(?::(\d{2})(?:[.,]\d+)?)?(Z|[+-]\d{2}:?\d{2})?)?`)
	verifyClockRe = regexp.MustCompile(`\d{1,2}:\d{2}(?::\d{2})?`)
)

func prepareVerifyEvidence(ev verifyEvidence) *verifyPrepared {
	loc := ev.Loc
	if loc == nil {
		loc = time.UTC
	}
	joined := strings.Join(ev.Texts, "\n")
	p := &verifyPrepared{dates: map[[3]int]bool{}}
	var b strings.Builder
	b.WriteString(strings.ToLower(joined))
	for _, n := range ev.Names {
		b.WriteByte('\n')
		b.WriteString(strings.ToLower(n))
	}
	p.lower = b.String()

	mins := map[int]bool{}
	var inst []time.Time // damgalı anlar (kova hesabı)
	addInstant := func(t time.Time, bucketed bool) {
		lt := t.In(loc)
		mins[lt.Hour()*60+lt.Minute()] = true
		p.addDate(lt.Year(), int(lt.Month()), lt.Day())
		if bucketed {
			inst = append(inst, lt)
		}
	}
	// ISO damgaları: bölgeli → anı; bölgesiz → UTC anı VE yazıldığı gibi yerel.
	masked := verifyISORe.ReplaceAllStringFunc(joined, func(m string) string {
		sm := verifyISORe.FindStringSubmatch(m)
		y, _ := strconv.Atoi(sm[1])
		mo, _ := strconv.Atoi(sm[2])
		d, _ := strconv.Atoi(sm[3])
		p.addDate(y, mo, d)
		if sm[4] == "" {
			return " "
		}
		h, _ := strconv.Atoi(sm[4])
		mi, _ := strconv.Atoi(sm[5])
		sec, _ := strconv.Atoi(sm[6])
		if sm[7] == "" {
			mins[h*60+mi] = true
			addInstant(time.Date(y, time.Month(mo), d, h, mi, sec, 0, time.UTC), true)
			return " "
		}
		if t, ok := verifyParseZoned(sm); ok {
			addInstant(t, true)
		}
		return " "
	})
	// Kanıt metnindeki yerel "HH:MM" (sunucunun kullanıcı diliminde yazdığı).
	for _, m := range verifyClockRe.FindAllStringIndex(masked, -1) {
		if mm, ok := verifyClockAt(masked, m[0], m[1]); ok {
			mins[mm] = true
		}
	}
	for _, t := range ev.Times {
		if !t.IsZero() {
			addInstant(t, false)
		}
	}
	nums := evidenceNumbers(joined)
	nums = append(nums, ev.Numbers...)
	seen := map[float64]bool{}
	for _, v := range nums {
		v = math.Abs(v)
		if seen[v] {
			continue
		}
		seen[v] = true
		p.nums = append(p.nums, v)
		if t, ok := verifyEpoch(v); ok {
			addInstant(t, true)
		}
	}
	for m := range mins {
		p.minutes = append(p.minutes, m)
	}
	sort.Ints(p.minutes)
	p.bucket = verifyBucketMinutes(inst)
	// Pencere günleri: pencerenin kapsadığı her gün (en çok 400).
	var lo, hi time.Time
	for _, t := range ev.Times {
		if t.IsZero() {
			continue
		}
		if lo.IsZero() || t.Before(lo) {
			lo = t
		}
		if t.After(hi) {
			hi = t
		}
	}
	if !lo.IsZero() {
		d, end := lo.In(loc), hi.In(loc)
		for i := 0; i < 400 && !d.After(end); i++ {
			p.addDate(d.Year(), int(d.Month()), d.Day())
			d = d.AddDate(0, 0, 1)
		}
		p.addDate(end.Year(), int(end.Month()), end.Day())
	}
	return p
}

func (p *verifyPrepared) addDate(y, m, d int) {
	p.dates[[3]int{y, m, d}] = true
	p.dates[[3]int{0, m, d}] = true
}

// verifyParseZoned — bölgeli ISO alt eşleşmesi → an.
func verifyParseZoned(sm []string) (time.Time, bool) {
	z := sm[7]
	if z != "Z" && len(z) == 5 { // +0300 → +03:00
		z = z[:3] + ":" + z[3:]
	}
	sec := sm[6]
	if sec == "" {
		sec = "00"
	}
	s := fmt.Sprintf("%s-%s-%sT%02s:%s:%s%s", sm[1], sm[2], sm[3], sm[4], sm[5], sec, z)
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

// verifyEpoch — makul aralıktaki epoch sayısı (s / ms / µs / ns) → an.
func verifyEpoch(v float64) (time.Time, bool) {
	switch {
	case v >= 1.5e9 && v < 4.2e9:
		return time.Unix(int64(v), 0), true
	case v >= 1.5e12 && v < 4.2e12:
		return time.UnixMilli(int64(v)), true
	case v >= 1.5e15 && v < 4.2e15:
		return time.UnixMicro(int64(v)), true
	case v >= 1.5e18 && v < 4.2e18:
		return time.Unix(0, int64(v)), true
	}
	return time.Time{}, false
}

// verifyBucketMinutes — SAF: damgalı anların en sık ardışık aralığı (dk);
// en az iki kez görülmeli ve ≤ 1 gün. Yoksa 0.
func verifyBucketMinutes(ts []time.Time) int {
	if len(ts) < 3 {
		return 0
	}
	ms := make([]int64, 0, len(ts))
	for _, t := range ts {
		ms = append(ms, t.Unix()/60)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
	freq := map[int64]int{}
	best, bestN := int64(0), 0
	for i := 1; i < len(ms); i++ {
		g := ms[i] - ms[i-1]
		if g <= 1 || g > 1440 {
			continue
		}
		freq[g]++
		if freq[g] > bestN || (freq[g] == bestN && g < best) {
			best, bestN = g, freq[g]
		}
	}
	if bestN < 2 {
		return 0
	}
	return int(best)
}

// hasMinute — gün dakikası kanıtta ±1 dk (kovalıysa ±1 kova) içinde mi.
func (p *verifyPrepared) hasMinute(m int) bool {
	tol := 1
	if p.bucket > tol {
		tol = p.bucket
	}
	for _, e := range p.minutes {
		d := m - e
		if d < 0 {
			d = -d
		}
		if d > 720 {
			d = 1440 - d
		}
		if d <= tol {
			return true
		}
	}
	return false
}

func (p *verifyPrepared) hasDate(d verifyDate) bool {
	return p.dates[[3]int{d.y, d.m, d.d}]
}

// hasName — ad kanıtta sözcük sınırıyla geçiyor mu. Noktalı exception adı
// son parçasıyla, "GET /yol" yol kısmıyla (sondaki sayısal parça atılarak) da denenir.
func (p *verifyPrepared) hasName(n string) bool {
	l := strings.ToLower(n)
	if containsBounded(p.lower, l) {
		return true
	}
	if i := strings.LastIndexByte(l, '.'); i > 0 && i < len(l)-1 && !strings.Contains(l, "/") {
		if containsBounded(p.lower, l[i+1:]) {
			return true
		}
	}
	if sp := strings.IndexByte(l, ' '); sp > 0 && strings.HasPrefix(l[sp+1:], "/") {
		path := l[sp+1:]
		if containsBounded(p.lower, path) {
			return true
		}
		if k := strings.LastIndexByte(path, '/'); k > 0 && isAllDigits(path[k+1:]) && containsBounded(p.lower, path[:k]) {
			return true
		}
	}
	return false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// containsBounded — hay içinde needle, iki yanı tanımlayıcı karakteri değil.
func containsBounded(hay, needle string) bool {
	if needle == "" {
		return false
	}
	from := 0
	for {
		i := strings.Index(hay[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		j := i + len(needle)
		before, _ := utf8.DecodeLastRuneInString(hay[:i])
		after, _ := utf8.DecodeRuneInString(hay[j:])
		if (i == 0 || !nameRune(before)) && (j == len(hay) || !nameRune(after)) {
			return true
		}
		from = i + 1
	}
}

func nameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

// ── sayılar ──────────────────────────────────────────────────────────────

// verifyNum — bir sayı iddiası: olası okumalar (kanonik birimde değer ± yarı tolerans).
type verifyNum struct {
	kind string
	text string
	vals []verifyVal
}

type verifyVal struct{ v, half float64 }

// verifyUnit — sayıdan sonra gelebilen birim. long: Türkçe ek bitişik gelebilir ("saniyede").
type verifyUnit struct {
	word   string
	kind   string
	factor float64 // süre: ms'ye çarpan
	long   bool
}

// verifyUnits — uzun olan önce.
var verifyUnits = []verifyUnit{
	{"milisaniye", verifyKindDuration, 1, true},
	{"saniye", verifyKindDuration, 1000, true},
	{"dakika", verifyKindDuration, 60000, true},
	{"saat", verifyKindDuration, 3600000, true},
	{"req/s", verifyKindNumber, 1, false},
	{"istek/s", verifyKindNumber, 1, false},
	{"spans", verifyKindNumber, 1, false},
	{"span", verifyKindNumber, 1, false},
	{"rps", verifyKindNumber, 1, false},
	{"kat", verifyKindNumber, 1, false},
	{"ms", verifyKindDuration, 1, false},
	{"sn", verifyKindDuration, 1000, false},
	{"dk", verifyKindDuration, 60000, false},
	{"sa", verifyKindDuration, 3600000, false},
	{"s", verifyKindDuration, 1000, false},
}

// verifyMagnitudes — büyüklük sözcükleri ve izinli ekleri.
var verifyMagnitudes = []struct {
	word string
	mag  float64
}{{"milyar", 1e9}, {"milyon", 1e6}, {"bin", 1e3}}

var verifyNumberWords = map[string]float64{
	"bir": 1, "iki": 2, "üç": 3, "dört": 4, "beş": 5, "altı": 6, "yedi": 7, "sekiz": 8, "dokuz": 9, "on": 10,
	"yarım": 0.5, "yarı": 0.5,
}

var verifyApproxBefore = []string{"yaklaşık", "takriben", "aşağı yukarı", "~", "≈"}
var verifyApproxAfter = []string{"civar", "kadar", "dolay"}

// wordAt — rs[k:]'deki harf dizisi (küçük harf) ve bittiği indeks.
func wordAt(rs []rune, k int) (string, int) {
	j := k
	for j < len(rs) && (unicode.IsLetter(rs[j]) || rs[j] == '/') {
		j++
	}
	return strings.ToLower(string(rs[k:j])), j
}

func skipSpace(rs []rune, k int) int {
	if k < len(rs) && (rs[k] == ' ' || rs[k] == ' ') {
		return k + 1
	}
	return k
}

// magnitudeAt — k'de büyüklük sözcüğü var mı ("bin" yalnız tam sözcük ya da ekli: bini, binden…).
func magnitudeAt(rs []rune, k int) (float64, int) {
	w, end := wordAt(rs, k)
	for _, m := range verifyMagnitudes {
		if w == m.word || (strings.HasPrefix(w, m.word) && m.word != "bin") ||
			(m.word == "bin" && (w == "bini" || w == "bine" || w == "binden" || w == "binin" || w == "binlik")) {
			return m.mag, end
		}
	}
	return 0, k
}

// unitAt — k'de birim var mı.
func unitAt(rs []rune, k int) (verifyUnit, int, bool) {
	if k < len(rs) && rs[k] == '%' {
		return verifyUnit{word: "%", kind: verifyKindPercent, factor: 1}, k + 1, true
	}
	w, end := wordAt(rs, k)
	if w == "" {
		return verifyUnit{}, k, false
	}
	for _, u := range verifyUnits {
		if u.long {
			if strings.HasPrefix(w, u.word) {
				return u, end, true
			}
			continue
		}
		if w == u.word {
			return u, k + utf8.RuneCountInString(u.word), true
		}
	}
	return verifyUnit{}, k, false
}

// approxAround — sayının önünde ya da birimin ardında yaklaşıklık sözcüğü.
func approxAround(rs []rune, start, end int) bool {
	lo := max(0, start-14)
	before := strings.ToLower(string(rs[lo:start]))
	for _, w := range verifyApproxBefore {
		if strings.Contains(before, w) {
			return true
		}
	}
	w, _ := wordAt(rs, skipSpace(rs, end))
	for _, a := range verifyApproxAfter {
		if strings.HasPrefix(w, a) {
			return true
		}
	}
	return false
}

// verifyNumberClaims — SAF: maskelenmiş metindeki sayı iddiaları.
func verifyNumberClaims(s string, general bool) []verifyNum {
	var out []verifyNum
	rs := []rune(s)
	for i := 0; i < len(rs); {
		if !unicode.IsDigit(rs[i]) {
			i++
			continue
		}
		start := i
		j := numTokenEnd(rs, i)
		i = j
		if start > 0 && (unicode.IsLetter(rs[start-1]) || rs[start-1] == '_' || rs[start-1] == '#' || rs[start-1] == '-' && start > 1 && unicode.IsLetter(rs[start-2])) {
			continue // kimlik: p95, [T1], v2, checkout-2
		}
		if verifyListMarker(rs, start, j) || verifyHTTPCode(rs, start) {
			continue
		}
		tok := string(rs[start:j])
		cands := numCandidates(tok)
		if len(cands) == 0 {
			continue
		}
		k := skipSpace(rs, j)
		mag := 1.0
		if m, e := magnitudeAt(rs, k); m > 0 {
			mag, k = m, skipSpace(rs, e)
		}
		u, uEnd, hasUnit := unitAt(rs, k)
		if !hasUnit && j < len(rs) && (unicode.IsLetter(rs[j]) || rs[j] == '_') && mag == 1 {
			continue // bitişik harf: 5xx, 2B, hex kuyruğu
		}
		end := j
		if mag > 1 {
			end = k
		}
		kind := verifyKindNumber
		factor := 1.0
		if hasUnit {
			kind, factor, end = u.kind, u.factor, uEnd
		}
		if start > 0 && rs[start-1] == '%' {
			kind, factor = verifyKindPercent, 1
		}
		if kind == verifyKindNumber && !hasUnit && mag == 1 && numDigitCount(tok) < 2 {
			continue // tek basamaklı düz sayı: "3 servis"
		}
		if general && kind == verifyKindNumber {
			continue
		}
		approx := approxAround(rs, start, end)
		text := strings.TrimSpace(string(rs[start:end]))
		if start > 0 && rs[start-1] == '%' {
			text = "%" + text
		}
		n := verifyNum{kind: kind, text: text}
		for _, c := range cands {
			v := c.V * mag * factor
			half := 0.5 * math.Pow10(-c.Dec) * mag * factor
			if approx {
				half = math.Max(half, 0.05*v)
			}
			n.vals = append(n.vals, verifyVal{v: v, half: half})
		}
		out = append(out, n)
	}
	return append(out, verifyWordNumberClaims(rs, general)...)
}

var verifyWordNumRe = regexp.MustCompile(`(?i)(^|[^\p{L}\p{N}])(bir|iki|üç|dört|beş|altı|yedi|sekiz|dokuz|on|yarım|yarı)\s+(\p{L}+)`)

// verifyWordNumberClaims — "iki saniye", "yarım dakika", "on bin", "bir milyon".
func verifyWordNumberClaims(rs []rune, general bool) []verifyNum {
	s := string(rs)
	var out []verifyNum
	for _, m := range verifyWordNumRe.FindAllStringSubmatchIndex(s, -1) {
		word := strings.ToLower(s[m[4]:m[5]])
		base := verifyNumberWords[word]
		nrs := []rune(s[m[6]:])
		mag, e := magnitudeAt(nrs, 0)
		factor := 1.0
		kind := verifyKindNumber
		end := 0
		if mag > 0 {
			end = e
			if u, ue, ok := unitAt(nrs, skipSpace(nrs, e)); ok && u.kind == verifyKindDuration {
				kind, factor, end = u.kind, u.factor, ue
			}
		} else if u, ue, ok := unitAt(nrs, 0); ok && u.kind == verifyKindDuration && u.long {
			mag, kind, factor, end = 1, u.kind, u.factor, ue
		} else {
			continue
		}
		if general && kind == verifyKindNumber {
			continue
		}
		dec := 0
		if base < 1 {
			dec = 1
		}
		v := base * mag * factor
		half := 0.5 * math.Pow10(-dec) * mag * factor
		if approxAround(rs, utf8.RuneCountInString(s[:m[4]]), utf8.RuneCountInString(s[:m[6]])+end) {
			half = math.Max(half, 0.05*v)
		}
		text := strings.TrimSpace(s[m[4]:m[6]] + string(nrs[:end]))
		out = append(out, verifyNum{kind: kind, text: text, vals: []verifyVal{{v: v, half: half}}})
	}
	return out
}

// verifyListMarker — satır başı "10." / "2)" numaralı liste imi.
func verifyListMarker(rs []rune, start, j int) bool {
	if j >= len(rs) || (rs[j] != '.' && rs[j] != ')') || (j+1 < len(rs) && rs[j+1] != ' ') {
		return false
	}
	for k := start - 1; k >= 0; k-- {
		switch rs[k] {
		case ' ', '\t':
			continue
		case '\n':
			return true
		default:
			return false
		}
	}
	return true
}

// verifyHTTPCode — "HTTP 503": durum kodu ölçüm değil, ad.
func verifyHTTPCode(rs []rune, start int) bool {
	lo := max(0, start-5)
	return strings.HasSuffix(strings.ToUpper(string(rs[lo:start])), "HTTP ")
}

// verifyScales — kanıt değerinin iddia birimine olası çevrimleri.
func verifyScales(kind string) []float64 {
	switch kind {
	case verifyKindPercent:
		return []float64{1, 100} // yüzde ya da oran
	case verifyKindDuration:
		return []float64{1, 1000, 60000, 1e-3, 1e-6} // ms, s, dk, µs, ns
	}
	return []float64{1}
}

func (v verifyVal) holds(x float64) bool {
	eps := 1e-9 * math.Max(1, math.Abs(v.v))
	return x >= v.v-v.half-eps && x < v.v+v.half-eps || math.Abs(x-v.v) <= eps
}

// matchNumber — doğrudan eşleşme; eşleşen ham kanıt değeriyle.
func (p *verifyPrepared) matchNumber(c verifyNum) (float64, bool) {
	scales := verifyScales(c.kind)
	for _, v := range c.vals {
		for _, e := range p.nums {
			for _, sc := range scales {
				if v.holds(e * sc) {
					return e, true
				}
			}
		}
	}
	return 0, false
}

// verifyDerivedMatch — havuzdaki iki değerin toplamı / farkı / oranı / yüzde değişimi.
func verifyDerivedMatch(c verifyNum, pool []float64) bool {
	scales := verifyScales(c.kind)
	try := func(x float64) bool {
		for _, v := range c.vals {
			for _, sc := range scales {
				if v.holds(x * sc) {
					return true
				}
			}
		}
		return false
	}
	for i, a := range pool {
		for j, b := range pool {
			if i == j {
				continue
			}
			if try(a+b) || try(math.Abs(a-b)) {
				return true
			}
			if b != 0 {
				if try(a/b) || try(a/b*100) || try(math.Abs(a-b)/b*100) {
					return true
				}
			}
		}
	}
	return false
}

func dedupeFloats(in []float64, limit int) []float64 {
	seen := map[float64]bool{}
	var out []float64
	for _, v := range in {
		v = math.Abs(v)
		if seen[v] || v == 0 {
			continue
		}
		seen[v] = true
		out = append(out, v)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// ── saat ve tarih ────────────────────────────────────────────────────────

type verifyTime struct {
	text   string
	minute int
}

type verifyDate struct {
	text    string
	y, m, d int // y=0: yılsız ("18 Kasım")
}

var (
	verifySaatDotRe = regexp.MustCompile(`(?i)saat\s+(\d{1,2})\.(\d{2})`)
	verifyDMYRe     = regexp.MustCompile(`(\d{1,2})\.(\d{1,2})\.(\d{4})`)
	verifyDayMonRe  = regexp.MustCompile(`(?i)(\d{1,2})\s+(ocak|şubat|mart|nisan|mayıs|haziran|temmuz|ağustos|eylül|ekim|kasım|aralık)(\s+\d{4})?`)
)

var verifyMonths = map[string]int{"ocak": 1, "şubat": 2, "mart": 3, "nisan": 4, "mayıs": 5, "haziran": 6,
	"temmuz": 7, "ağustos": 8, "eylül": 9, "ekim": 10, "kasım": 11, "aralık": 12}

// verifyClockAt — s[i:j] bağımsız bir "HH:MM(:SS)" mi (önünde/ardında rakam ya da ':' yok); gün dakikası.
func verifyClockAt(s string, i, j int) (int, bool) {
	if i > 0 && (s[i-1] >= '0' && s[i-1] <= '9' || s[i-1] == ':' || s[i-1] == '.' || s[i-1] == ',') {
		return 0, false
	}
	if j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ':') {
		return 0, false
	}
	parts := strings.Split(s[i:j], ":")
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	if h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// verifyDateClaims — ISO tarih (saatli ISO'nun saati ayrıca iddia), GG.AA.YYYY, "18 Kasım [2026]".
func verifyDateClaims(s string) ([]verifyDate, string) {
	var out []verifyDate
	s = verifyISORe.ReplaceAllStringFunc(s, func(m string) string {
		sm := verifyISORe.FindStringSubmatch(m)
		y, _ := strconv.Atoi(sm[1])
		mo, _ := strconv.Atoi(sm[2])
		d, _ := strconv.Atoi(sm[3])
		out = append(out, verifyDate{text: sm[1] + "-" + sm[2] + "-" + sm[3], y: y, m: mo, d: d})
		if sm[4] != "" && sm[7] == "" {
			return " " + sm[4] + ":" + sm[5] + " " // saat ayrı iddia olarak kalır
		}
		return " "
	})
	s = verifyDMYRe.ReplaceAllStringFunc(s, func(m string) string {
		sm := verifyDMYRe.FindStringSubmatch(m)
		d, _ := strconv.Atoi(sm[1])
		mo, _ := strconv.Atoi(sm[2])
		y, _ := strconv.Atoi(sm[3])
		if mo < 1 || mo > 12 || d < 1 || d > 31 {
			return m
		}
		out = append(out, verifyDate{text: m, y: y, m: mo, d: d})
		return " "
	})
	s = verifyDayMonRe.ReplaceAllStringFunc(s, func(m string) string {
		sm := verifyDayMonRe.FindStringSubmatch(m)
		d, _ := strconv.Atoi(sm[1])
		mo := verifyMonths[strings.ToLower(sm[2])]
		y := 0
		if t := strings.TrimSpace(sm[3]); t != "" {
			y, _ = strconv.Atoi(t)
		}
		if mo == 0 || d < 1 || d > 31 {
			return m
		}
		out = append(out, verifyDate{text: strings.TrimSpace(m), y: y, m: mo, d: d})
		return " "
	})
	return out, s
}

// verifyTimeClaims — "14:32", "14:32:05", "saat 14.32"; maskelenmiş metni döndürür.
func verifyTimeClaims(s string) ([]verifyTime, string) {
	var out []verifyTime
	s = verifySaatDotRe.ReplaceAllStringFunc(s, func(m string) string {
		sm := verifySaatDotRe.FindStringSubmatch(m)
		h, _ := strconv.Atoi(sm[1])
		mi, _ := strconv.Atoi(sm[2])
		if h > 23 || mi > 59 {
			return m
		}
		out = append(out, verifyTime{text: sm[1] + "." + sm[2], minute: h*60 + mi})
		return " "
	})
	idx := verifyClockRe.FindAllStringIndex(s, -1)
	if len(idx) == 0 {
		return out, s
	}
	var b strings.Builder
	last := 0
	for _, m := range idx {
		mm, ok := verifyClockAt(s, m[0], m[1])
		if !ok {
			continue
		}
		out = append(out, verifyTime{text: s[m[0]:m[1]], minute: mm})
		b.WriteString(s[last:m[0]])
		b.WriteByte(' ')
		last = m[1]
	}
	b.WriteString(s[last:])
	return out, b.String()
}

// ── adlar ────────────────────────────────────────────────────────────────

var (
	verifyBacktickRe   = regexp.MustCompile("`([^`\n]{2,120})`")
	verifyIdentRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]*$`)
	verifyHTTPOpFullRe = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+/\S*$`)
	verifyHTTPOpRe     = regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+(/[^\s` + "`" + `'"),;]*)`)
	verifyExceptionRe  = regexp.MustCompile(`\b(?:[a-z][a-z0-9_]*\.)*[A-Z][A-Za-z0-9]*[a-z0-9](?:Exception|Error)\b`)
	verifyPodRe        = regexp.MustCompile(`\b[a-z0-9]+(?:-[a-z0-9]+)*-[a-z0-9]{8,10}-[a-z0-9]{5}\b`)
	verifyTokenRe      = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._-]*[A-Za-z0-9]`)
	verifyMetricTokRe  = regexp.MustCompile(`^(?i:p\d{2,3}|\d+(?:[.,]\d+)?\s*(?:ms|s|sn|dk|%)?)$`)
)

// verifyNameClaims — SAF: ad iddiaları (tekil, küçük harfe göre) ve ters
// tırnaklı adları maskelenmiş metin. general=true → yalnız katalog servis adları.
func verifyNameClaims(s string, catalog []string, general bool) ([]string, string) {
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		n = strings.TrimSpace(strings.TrimRight(n, ".,;:"))
		l := strings.ToLower(n)
		if len(l) < 3 || seen[l] {
			return
		}
		seen[l] = true
		out = append(out, n)
	}
	if !general {
		s = verifyBacktickRe.ReplaceAllStringFunc(s, func(m string) string {
			inner := strings.TrimSpace(m[1 : len(m)-1])
			switch {
			case verifyHTTPOpFullRe.MatchString(inner):
				add(inner)
			case verifyIdentRe.MatchString(inner) && strings.IndexFunc(inner, unicode.IsLetter) >= 0 && !verifyMetricTokRe.MatchString(inner):
				add(inner)
			default:
				return m
			}
			return " "
		})
		for _, m := range verifyHTTPOpRe.FindAllString(s, -1) {
			add(m)
		}
		for _, m := range verifyExceptionRe.FindAllString(s, -1) {
			add(m)
		}
		for _, m := range verifyPodRe.FindAllString(s, -1) {
			add(m)
		}
	}
	if len(catalog) > 0 {
		cat := make(map[string]bool, len(catalog))
		for _, c := range catalog {
			l := strings.ToLower(c)
			if len(l) >= 5 || strings.ContainsAny(l, "-._") {
				cat[l] = true
			}
		}
		for _, m := range verifyTokenRe.FindAllString(s, -1) {
			if cat[strings.ToLower(m)] {
				add(m)
			}
		}
	}
	return out, s
}

// ── metin hazırlığı ──────────────────────────────────────────────────────

// verifyStripFences — çitli kod bloklarını (grafik dahil) atar.
func verifyStripFences(s string) string {
	if !strings.Contains(s, "```") {
		return s
	}
	var b strings.Builder
	in := false
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			in = !in
			continue
		}
		if !in {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// verifyIPRe — IPv4 dörtlüsü ya da iki+ iki noktalı onaltılık grup (IPv6 adayı).
var verifyIPRe = regexp.MustCompile(`(?i)\b\d{1,3}(?:\.\d{1,3}){3}\b|[0-9a-f]{0,4}(?::[0-9a-f]{0,4}){2,7}`)

// verifyMaskIPs — SAF (v0.10.2356, investigation-design S2): geçerli IPv4 /
// IPv6 adresi boşluğa çevrilir — "2001:db8::1" 2001 sayısı, "198.51.100.7"
// ondalık sayı iddiası sayılmaz. Saat ("14:05:30") adres olarak ayrışmaz, kalır.
func verifyMaskIPs(s string) string {
	return verifyIPRe.ReplaceAllStringFunc(s, func(m string) string {
		if a, err := netip.ParseAddr(m); err == nil && (a.Is4() || strings.Count(m, ":") >= 2) {
			return " "
		}
		return m
	})
}

// verifyMaskLinks — numericClaims ile aynı bağlantı maskesi.
func verifyMaskLinks(s string) string {
	return numClaimLinkRe.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "http") || strings.ContainsAny(m, "?=") || strings.Count(m, "/") >= 2 {
			return " "
		}
		return m
	})
}
