package api

// numeric_claims.go — v0.10.948 (CoSRE araştırma asistanı, Faz B): cevaptaki
// sayısal iddiaların KANITTA geçip geçmediğinin deterministik denetimi.
//
// Neden sunucuda: istem "her sayı kanıttan AYNEN gelir" diyor ama küçük model
// bunu her zaman tutmuyor — iki p95'i ortalıyor, span sürelerini topluyor ya da
// bir yüzdeyi kendisi hesaplıyor. Kanıt bloğu sunucunun kendi render ettiği
// metin olduğundan "bu sayı kanıtta var mı" sorusu ucuz ve kesin cevaplanır;
// bulunamayanlar cevabın sonuna uyarı olarak eklenir (cevap SİLİNMEZ — sayı
// doğru da olabilir, yalnız dayanağı gösterilemiyor).
//
// Neden kaba bir sayı taraması değil: kimlikler ([T1], trace id'leri, p95),
// tarih/saat, sürüm dizgeleri ve bağlantılar sayı İDDİASI değildir; onları
// saymak her cevaba sahte uyarı basardı ve uyarı körlüğü denetimi öldürür.
// Türkçe yazım da gerçek: "%3,6", "1.234 span", "12'si" — ondalık virgül ve
// binlik nokta iki yönde de denenir.
//
// v0.10.948 — aynı ayrım KANIT tarafında da (numInIdentifier + tarih maskesi):
// yoksa trace id'deki, pod hash'teki ya da zaman damgasındaki bir rakam dizisi
// uydurma sayıya dayanak olur ve denetim tam kaçırmayı yakalamaz.

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// numericClaimMaxListed — uyarıda adıyla listelenen en çok değer (kalanı "…").
const numericClaimMaxListed = 8

var (
	// Tarih / saat / ISO zaman damgası — iddia değil, maskelenir.
	numClaimDateTimeRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}(?:[T ]\d{1,2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)?|\b\d{1,2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?\b`)
	// Bağlantı / yol / sorgu dizgesi — içindeki sayılar kimlik ya da pencere.
	numClaimLinkRe = regexp.MustCompile(`(?:https?://|/)[^\s)\]]*`)
)

// numClaimUnits — sayının hemen ardından (boşluklu ya da bitişik) gelebilen
// birimler; uzun olan önce (req/s, "s"den önce denenmeli).
var numClaimUnits = []string{"milisaniye", "saniye", "req/s", "istek/s", "spans", "span", "rps", "ms", "sn", "s"}

// numClaim — cevapta bulunan bir sayısal iddia.
type numClaim struct {
	Text  string    // operatöre gösterilen biçim ("480 ms", "%12")
	Cands []numCand // olası okumalar (Türkçe/İngilizce ayraç belirsizliği)
}

// numCand — bir okuma: değer + yazılan ondalık basamak sayısı.
type numCand struct {
	V   float64
	Dec int
}

// ungroundedNumbers — SAF: answer'daki sayısal iddialardan evidence'ta
// dayanağı OLMAYANLAR (yazıldığı biçimde, tekil, geliş sırasıyla).
//
// İddia sayılan: birimli sayı (ms, s/sn, %, span, req/s) ya da en az iki
// basamaklı düz sayı. Dayanak: aynı değer kanıtta geçiyor — Türkçe ondalık
// virgül, binlik ayraç ve kanıttaki değerin 0–2 ondalığa yuvarlanmış hâli
// kabul. Birim dönüşümü (1200 ms → 1,2 s) KABUL EDİLMEZ: istem sayıların
// birimiyle AYNEN aktarılmasını istiyor.
func ungroundedNumbers(answer, evidence string) []string {
	claims := numericClaims(answer)
	if len(claims) == 0 {
		return nil
	}
	ev := evidenceNumbers(evidence)
	var out []string
	seen := map[string]bool{}
	for _, c := range claims {
		if seen[c.Text] || numClaimGrounded(c, ev) {
			continue
		}
		seen[c.Text] = true
		out = append(out, c.Text)
	}
	return out
}

// numericClaimWarningTR — cevabın sonuna eklenen uyarı satırı ("" = uyarı yok).
func numericClaimWarningTR(ungrounded []string) string {
	if len(ungrounded) == 0 {
		return ""
	}
	list := ungrounded
	more := ""
	if len(list) > numericClaimMaxListed {
		list = list[:numericClaimMaxListed]
		more = ", …"
	}
	return "⚠ Kanıtta bulunamayan sayı(lar): " + strings.Join(list, ", ") + more +
		" — bu değerler çalıştırılan okumaların sonuçlarında yok; doğrulamadan kullanmayın."
}

// numClaimGrounded — iddianın herhangi bir okuması kanıttaki bir değerle eşleşiyor mu.
func numClaimGrounded(c numClaim, ev []float64) bool {
	for _, a := range c.Cands {
		for _, e := range ev {
			if numClaimMatches(a, e) {
				return true
			}
		}
	}
	return false
}

// numClaimMatches — a, e'nin kendisi ya da e'nin a'nın ondalık basamağına
// (≤2) yuvarlanmışı mı. İşaret yok sayılır (kanıt "+34.2", cevap "%34,2 arttı").
func numClaimMatches(a numCand, e float64) bool {
	av, ev := math.Abs(a.V), math.Abs(e)
	tol := 1e-9 * math.Max(1, av)
	if math.Abs(av-ev) <= tol {
		return true
	}
	if a.Dec > 2 {
		return false
	}
	p := math.Pow10(a.Dec)
	return math.Abs(math.Round(ev*p)/p-av) <= tol
}

// numericClaims — SAF: cevap metnindeki sayısal iddialar.
func numericClaims(s string) []numClaim {
	s = numClaimDateTimeRe.ReplaceAllString(s, " ")
	s = numClaimLinkRe.ReplaceAllStringFunc(s, func(m string) string {
		// Tek "/" (ör. "req/s", "3/5") bağlantı değil; yalnız yol/sorgu biçimi maskelenir.
		if strings.HasPrefix(m, "http") || strings.ContainsAny(m, "?=") || strings.Count(m, "/") >= 2 {
			return " "
		}
		return m
	})
	var out []numClaim
	rs := []rune(s)
	for i := 0; i < len(rs); {
		if !unicode.IsDigit(rs[i]) {
			i++
			continue
		}
		start := i
		j := numTokenEnd(rs, i)
		i = j
		tok := string(rs[start:j])
		prev := rune(0)
		if start > 0 {
			prev = rs[start-1]
		}
		// Kimliğin parçası: [T1], p95, 4bf92f…, v2, snake_case_1; 5xx, 2B, 3rd, hex kuyruğu.
		if numInIdentifier(rs, start, j) {
			continue
		}
		unit, uEnd := numClaimUnitAfter(rs, j)
		if unit == "" && prev == '%' {
			unit = "%"
		}
		cands := numCandidates(tok)
		if len(cands) == 0 {
			continue // sürüm/IP dizgesi (1.4.2, 10.0.0.1)
		}
		if unit == "" && numDigitCount(tok) < 2 {
			continue // tek basamaklı düz sayı: "3 servis" — iddia sayılmaz
		}
		text := tok
		switch {
		case unit == "%" && prev == '%':
			text = "%" + tok
		case unit != "":
			text = strings.TrimSpace(string(rs[start:uEnd]))
		}
		out = append(out, numClaim{Text: text, Cands: cands})
	}
	return out
}

// numInIdentifier — v0.10.948: [start,j) rakam dizisi bir kimliğin parçası mı
// (önünde harf/_/#: [T1], p95, 4bf92f…, v2; ardında birimsiz harf/_: 5xx, 2B,
// hex kuyruğu, pod hash). İddia ve kanıt tarafı AYNI tanımı kullanır — ayrışırsa
// kimliğe gömülü rakam (trace id, pod hash) uydurma sayıya dayanak sayılır.
func numInIdentifier(rs []rune, start, j int) bool {
	if start > 0 && (unicode.IsLetter(rs[start-1]) || rs[start-1] == '_' || rs[start-1] == '#') {
		return true
	}
	unit, _ := numClaimUnitAfter(rs, j)
	return unit == "" && j < len(rs) && (unicode.IsLetter(rs[j]) || rs[j] == '_') // "480ms" birim olduğu için KALIR
}

// numTokenEnd — rakam dizisinin sonu; '.' ve ',' yalnız ardından rakam
// geliyorsa tokene dahil (cümle sonu noktası/virgülü değil).
func numTokenEnd(rs []rune, i int) int {
	j := i
	for j < len(rs) {
		if unicode.IsDigit(rs[j]) {
			j++
			continue
		}
		if (rs[j] == '.' || rs[j] == ',') && j+1 < len(rs) && unicode.IsDigit(rs[j+1]) {
			j++
			continue
		}
		break
	}
	return j
}

// numClaimUnitAfter — j'den itibaren (en çok bir boşlukla) bir birim var mı;
// varsa birim ve bittiği indeks. Birimden sonra harf gelmemeli ("span" evet,
// "spanning" hayır); Türkçe ek (kesme işaretiyle) serbest: "12 span'de".
func numClaimUnitAfter(rs []rune, j int) (string, int) {
	k := j
	if k < len(rs) && rs[k] == '%' {
		return "%", k + 1
	}
	if k < len(rs) && rs[k] == ' ' {
		k++
	}
	rest := string(rs[k:min(len(rs), k+12)])
	low := strings.ToLower(rest)
	for _, u := range numClaimUnits {
		if !strings.HasPrefix(low, u) {
			continue
		}
		end := k + utf8.RuneCountInString(u)
		if end < len(rs) && (unicode.IsLetter(rs[end]) || unicode.IsDigit(rs[end])) {
			continue
		}
		return u, end
	}
	return "", j
}

// numCandidates — SAF: yazılı sayının olası değerleri. Ayraç kuralları:
//
//	ayraç yok            → tamsayı
//	'.' ve ',' birlikte   → sondaki ondalık, öncekiler binlik
//	tek tür, ≥2 kez       → binlik (gruplar 3 basamak değilse sürüm → nil)
//	tek tür, 1 kez, 3 basamak arkası → belirsiz: binlik VE ondalık ikisi de
//	tek tür, 1 kez, diğer → ondalık
func numCandidates(tok string) []numCand {
	dots, commas := strings.Count(tok, "."), strings.Count(tok, ",")
	parse := func(intPart, frac string) (numCand, bool) {
		str := intPart
		if frac != "" {
			str += "." + frac
		}
		v, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return numCand{}, false
		}
		return numCand{V: v, Dec: len(frac)}, true
	}
	var out []numCand
	add := func(c numCand, ok bool) {
		if ok {
			out = append(out, c)
		}
	}
	switch {
	case dots == 0 && commas == 0:
		add(parse(tok, ""))
	case dots > 0 && commas > 0:
		last := max(strings.LastIndex(tok, "."), strings.LastIndex(tok, ","))
		intPart := strings.NewReplacer(".", "", ",", "").Replace(tok[:last])
		add(parse(intPart, tok[last+1:]))
	default:
		sep := "."
		if commas > 0 {
			sep = ","
		}
		parts := strings.Split(tok, sep)
		if len(parts) > 2 {
			for _, p := range parts[1:] {
				if len(p) != 3 {
					return nil // 1.4.2 / 10.0.0.1 — sayı değil
				}
			}
			if len(parts[0]) > 3 {
				return nil
			}
			add(parse(strings.Join(parts, ""), ""))
			return out
		}
		if len(parts[1]) == 3 && len(parts[0]) <= 3 {
			add(parse(parts[0]+parts[1], "")) // binlik okuma
		}
		add(parse(parts[0], parts[1])) // ondalık okuma
	}
	return out
}

// numDigitCount — tokendeki rakam sayısı (ayraçlar hariç).
func numDigitCount(tok string) int {
	n := 0
	for _, r := range tok {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// evidenceNumbers — SAF: kanıttaki sayısal değerler; iddia tarafıyla AYNI
// ayrım (v0.10.948 — tarih/saat maskelenir, kimliğe gömülü rakam dizisi —
// trace/span id, pod hash, p95, [T1] — değer sayılmaz). Eskiden kanıtta her
// rakam dizisi sayılıyordu: trace id'nin "3577"si ya da zaman damgasının
// saniyesi uydurma bir sayıya dayanak oluyordu. Bağlantı maskesi BİLEREK yok:
// takip kanıtı sıkışık JSON; yol regex'i boşluğa kadar yutar. Sürüm/IP
// parçaları da artık değer değil — iddia tarafı onları zaten üretmez
// (numCandidates nil).
func evidenceNumbers(s string) []float64 {
	s = numClaimDateTimeRe.ReplaceAllString(s, " ")
	var out []float64
	seen := map[float64]bool{}
	add := func(v float64) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); {
		if !unicode.IsDigit(rs[i]) {
			i++
			continue
		}
		start := i
		j := numTokenEnd(rs, i)
		i = j
		if numInIdentifier(rs, start, j) {
			continue
		}
		tok := string(rs[start:j])
		for _, c := range numCandidates(tok) {
			add(c.V)
		}
		// v0.10.948 — sıkışık JSON sayı dizisi ("[120,340,560]", "[12.5,34.25]")
		// numTokenEnd'de tek jeton okunur; öğeleri AYRICA değer. Yalnız dizi
		// bağlamında ('[' önce ya da ']' sonra): düzyazıdaki "%3,6" / "1,234"
		// parçaları (3, 6, 234) uydurma sayıya dayanak olmaz.
		if strings.Contains(tok, ",") && ((start > 0 && rs[start-1] == '[') || (j < len(rs) && rs[j] == ']')) {
			for _, p := range strings.Split(tok, ",") {
				for _, c := range numCandidates(p) {
					add(c.V)
				}
			}
		}
	}
	return out
}
