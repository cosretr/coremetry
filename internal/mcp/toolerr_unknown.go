package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// toolerr_unknown.go — v0.10.2320: bilinmeyen araç adı → en yakın GEÇERLİ adlar.
//
// Model katalogda olmayan bir ad uydurduğunda (yazım hatası, camelCase,
// tekil/çoğul, sözcük sırası) eskiden çıplak `unknown tool "x"` alıyordu ve
// çoğu kez aynı adı tekrar deniyor ya da tur yakıyordu. Sözleşme diğer araç
// hatalarıyla AYNI biçim (ToolError: not_found, retryable=false, ipucu,
// detail); ek olarak `did_you_mean` en çok üç ad taşır.
//
// Adaylar YALNIZ çağıranın verdiği katalogdan (bu alışverişte modele sunulan
// adlar) gelir: daraltılmış katalogdan bilerek düşürülen bir araç asla
// önerilmez. Kural deterministik ve model ailesinden bağımsızdır:
//
//  1. Ad normalize edilir: küçük harf, camelCase → snake, '-', '.', ' ' → '_'.
//  2. Düzenleme mesafesi (Levenshtein, rune) ≤ max(1, len/4) olan aday YAKIN.
//  3. Değilse SÖZCÜK eşleşmesi: genel fiiller (get/list/set …) atılınca bir
//     tarafın anlamlı sözcüklerinin hepsi öbüründe varsa (sondaki "s" yok
//     sayılır) aday yakın.
//  4. Mesafe eşleşmesi varsa YALNIZ onlar (sözcük eşleşmesi gürültü olur);
//     yoksa sözcük eşleşmeleri. Sıra: mesafe artan, eşitlikte ad
//     (alfabetik). En çok üç.
//
// Hiçbiri yakın değilse öneri YOK (uydurma bir yön göstermek, hiç
// göstermemekten kötü).

// maxToolSuggestions — öneri tavanı.
const maxToolSuggestions = 3

// toolNameStopWords — tek başına anlam taşımayan genel sözcükler.
var toolNameStopWords = map[string]bool{
	"get": true, "list": true, "set": true, "fetch": true, "find": true,
	"for": true, "by": true, "of": true, "to": true, "the": true, "a": true,
}

// normalizeToolName — karşılaştırma biçimi (bkz. kural 1).
func normalizeToolName(s string) string {
	var b strings.Builder
	prevLower := false
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r == '-' || r == '.' || r == ' ' || r == '/':
			b.WriteByte('_')
			prevLower = false
		case unicode.IsUpper(r):
			if prevLower {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			prevLower = false
		default:
			b.WriteRune(r)
			prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
		}
	}
	return b.String()
}

// toolNameWords — anlamlı sözcük kümesi (çoğul eki "s" atılmış).
func toolNameWords(norm string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Split(norm, "_") {
		if w == "" || toolNameStopWords[w] {
			continue
		}
		if len(w) > 3 && strings.HasSuffix(w, "s") {
			w = strings.TrimSuffix(w, "s")
		}
		out[w] = true
	}
	return out
}

func wordsSubset(a, b map[string]bool) bool {
	if len(a) == 0 {
		return false
	}
	for w := range a {
		if !b[w] {
			return false
		}
	}
	return true
}

// levenshtein — rune tabanlı düzenleme mesafesi.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// NearestToolNames — name'e en yakın en çok üç ad, YALNIZ catalog'dan
// (kural için dosya başı). Saf ve deterministik; catalog sırası sonucu
// değiştirmez. Birebir eşleşen ad (zaten geçerli) önerilmez.
func NearestToolNames(name string, catalog []string) []string {
	q := normalizeToolName(name)
	if q == "" {
		return nil
	}
	qWords := toolNameWords(q)
	limit := max(1, len([]rune(q))/4)
	type cand struct {
		name  string
		group int // 0 = mesafe, 1 = sözcük
		dist  int
	}
	var cs []cand
	seen := map[string]bool{}
	for _, c := range catalog {
		if c == "" || c == name || seen[c] {
			continue
		}
		seen[c] = true
		n := normalizeToolName(c)
		d := levenshtein(q, n)
		switch {
		case d <= limit:
			cs = append(cs, cand{c, 0, d})
		case wordsSubset(qWords, toolNameWords(n)) || wordsSubset(toolNameWords(n), qWords):
			cs = append(cs, cand{c, 1, d})
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].group != cs[j].group {
			return cs[i].group < cs[j].group
		}
		if cs[i].dist != cs[j].dist {
			return cs[i].dist < cs[j].dist
		}
		return cs[i].name < cs[j].name
	})
	out := make([]string, 0, maxToolSuggestions)
	for _, c := range cs {
		if len(out) == maxToolSuggestions || c.group != cs[0].group {
			break
		}
		out = append(out, c.name)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// UnknownToolJSON — bilinmeyen araç çağrısının modele giden sonucu: diğer
// araç hatalarıyla aynı ToolError biçimi (not_found, tekrar denenmez),
// detail = eski `unknown tool "x"` metni, öneri varsa did_you_mean.
func UnknownToolJSON(name string, suggestions []string) string {
	te := ToolError{
		Error:     ToolErrNotFound,
		Retryable: false,
		Hint:      "bu adla araç yok — yalnız sunulan araç listesindeki adları çağır ya da eldeki veriyle cevap ver",
		Detail:    capRunes(fmt.Sprintf("unknown tool %q", name), toolErrDetailMaxRunes),
	}
	if len(suggestions) > 0 {
		te.Hint = "bu adla araç yok — yakın adlar: " + strings.Join(suggestions, ", ") +
			"; bunlardan doğru olanı çağır ya da eldeki veriyle cevap ver"
		te.Suggest = suggestions
	}
	b, err := json.Marshal(te)
	if err != nil {
		return `{"error":"not_found","retryable":false,"hint":"bu adla araç yok"}`
	}
	return string(b)
}
