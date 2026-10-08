// Package wiki — v0.10.1122 ("karma"): on-prem Azure DevOps wiki'lerini
// Coremetry içine indeksleyip CoSRE sohbetine kaynak atıflı bilgi olarak
// sunan katman.
//
// Parçalar:
//
//	text.go   — Türkçe-duyarlı katlama + teknik terim korumalı jetonlayıcı (SAF)
//	chunk.go  — Markdown başlık-duyarlı parçalayıcı (SAF)
//	rank.go   — BM25 benzeri lexical skor + semantik harman (SAF)
//	config.go — wiki_knowledge ayar blobu + wiki_sync_status durum blobu
//	sync.go   — artımlı senkron motoru (lider pod'da; sınırlı eşzamanlılık + hız)
//	search.go — yerel arama + gerekirse Azure DevOps Search canlı yedeği + sayfa okuma
//
// Wiki içeriği Coremetry'nin içinde kalır: hiçbir log satırına sayfa metni
// yazılmaz, PAT hiçbir yere taşınmaz (bağlantı devops paketinin).
package wiki

import (
	"strings"
	"unicode"
)

// Fold — arama için TEK normalizasyon: küçük harf + Türkçe-duyarlı katlama.
//
// İ/I/ı → i ve ş/ğ/ü/ö/ç → s/g/u/o/c. Neden ASCII'ye katlıyoruz: kurumsal
// klavyelerde aynı sözcük iki yazımla aranıyor ("şifre" / "sifre") ve
// teknik metin İngilizce büyük harf taşıyor ("INFO", "ID") — Türkçe
// küçültme kuralı "I"yı "ı" yapıp İngilizce terimleri bozardı. Hem indeks
// hem sorgu bu fonksiyondan geçtiği için iki taraf her zaman aynı biçimi
// görür. Saf; tablo-testli.
func Fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 'İ', 'I', 'ı', 'î', 'Î', 'ï':
			b.WriteRune('i')
			continue
		case 'Ş', 'ş':
			b.WriteRune('s')
			continue
		case 'Ğ', 'ğ':
			b.WriteRune('g')
			continue
		case 'Ü', 'ü', 'û', 'Û':
			b.WriteRune('u')
			continue
		case 'Ö', 'ö':
			b.WriteRune('o')
			continue
		case 'Ç', 'ç':
			b.WriteRune('c')
			continue
		case 'Â', 'â':
			b.WriteRune('a')
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// isWordRune — harf ya da rakam (katlamadan sonra).
func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// isJoiner — teknik tanımlayıcıların iç ayraçları: svc-orders, ERR_1042,
// orders.v2, payments/api. Yalnız İKİ kelime parçasının ARASINDA anlamlı.
func isJoiner(r rune) bool { return r == '-' || r == '_' || r == '.' || r == '/' || r == ':' }

const (
	tokenMaxRunes    = 64 // daha uzunu (base64, hash) arama değeri taşımaz
	compoundMaxParts = 6
)

// Tokens — metni arama jetonlarına böler (SAF, sıra korunur, tekrarlar
// KALIR — terim frekansı için).
//
// İki tür jeton üretilir:
//   - kelime parçaları: katlanmış harf/rakam koşuları ("svc", "orders",
//     "1042"); tek karakterlik parçalar yalnız rakamsa tutulur.
//   - bileşik tanımlayıcılar: iç ayraçla bağlı ≥2 parça ("svc-orders",
//     "err-1042", "orders.v2"). Teknik terimin TAM eşleşmesi böylece kendi
//     (yüksek idf'li) jetonuna sahip olur; ClickHouse hasToken bu yazımı
//     ayraçlardan bölerdi.
func Tokens(s string) []string {
	f := Fold(s)
	rs := []rune(f)
	var out []string
	var parts []string // geçerli bileşiğin parçaları
	var joiners []rune // parçalar arası ayraçlar
	flushCompound := func() {
		if len(parts) >= 2 && len(parts) <= compoundMaxParts {
			var b strings.Builder
			for i, p := range parts {
				if i > 0 {
					b.WriteRune(joiners[i-1])
				}
				b.WriteString(p)
			}
			if c := b.String(); len([]rune(c)) <= tokenMaxRunes {
				out = append(out, c)
			}
		}
		parts, joiners = parts[:0], joiners[:0]
	}
	i := 0
	for i < len(rs) {
		if !isWordRune(rs[i]) {
			// Ayraç yalnız iki parça arasında bileşiği sürdürür.
			if len(parts) > 0 && isJoiner(rs[i]) && i+1 < len(rs) && isWordRune(rs[i+1]) {
				joiners = append(joiners, rs[i])
				i++
				continue
			}
			flushCompound()
			i++
			continue
		}
		j := i
		for j < len(rs) && isWordRune(rs[j]) {
			j++
		}
		w := string(rs[i:j])
		if keepWord(w) {
			out = append(out, w)
		}
		if len(parts) > len(joiners) {
			// Önceki parça ayraçsız bitişikti (olamaz — koşu bütün) ya da
			// ayraç düşmüştü; güvenlik için bileşiği kapat.
			flushCompound()
		}
		parts = append(parts, w)
		i = j
	}
	flushCompound()
	return out
}

// keepWord — tek kelime parçası jetonlanır mı.
func keepWord(w string) bool {
	n := len([]rune(w))
	if n == 0 || n > tokenMaxRunes {
		return false
	}
	if n == 1 {
		return unicode.IsDigit([]rune(w)[0])
	}
	return true
}

// stopwords — katlanmış biçimde (Fold sonrası). Soru kalıpları ve bağlaçlar;
// teknik terimler ASLA buraya girmez. v0.10.1124: soru ekleri / soru
// fiilleri / "wiki'de anlatılıyor" gibi meta sözcükler genişletildi — her biri
// kapsama paydasını şişirip gerçek isabeti tabanın altına itiyordu.
var stopwords = map[string]bool{
	// TR
	"ve": true, "ile": true, "icin": true, "bir": true, "bu": true, "su": true, "o": true,
	"nasil": true, "nedir": true, "neden": true, "niye": true, "hangi": true, "var": true, "yok": true,
	"mi": true, "mu": true, "ama": true, "veya": true, "ya": true, "da": true, "de": true,
	"ne": true, "kim": true, "kimin": true, "nerede": true, "nereden": true, "olan": true, "olarak": true,
	"gibi": true, "daha": true, "cok": true, "en": true, "her": true, "ki": true, "ise": true,
	"yapilir": true, "yapariz": true, "yapmali": true, "yapmaliyim": true, "lazim": true, "gerek": true,
	"nelerdir": true, "anlat": true, "acikla": true, "goster": true, "bana": true, "bize": true,
	"wiki": true, "wikide": true, "wikideki": true, "sayfa": true, "sayfasi": true, "sayfada": true,
	"midir": true, "mudur": true, "misin": true, "musun": true, "miyim": true, "miyiz": true,
	"neler": true, "kac": true, "kimdir": true, "neresi": true, "nereye": true, "hangisi": true,
	"olur": true, "olmali": true, "oluyor": true, "zaman": true, "acaba": true, "lutfen": true,
	"hakkinda": true, "ilgili": true, "bilgi": true, "bilgisi": true, "mevcut": true,
	"edilir": true, "ederim": true, "edilmeli": true, "yapabilirim": true, "yapilmali": true, "yapilmasi": true,
	"wikiye": true, "wikiden": true, "wikidir": true, "dokuman": true, "dokumanda": true, "dokumantasyon": true,
	"dokumantasyonda": true, "anlatiliyor": true, "anlatilir": true, "anlatilmis": true, "yaziyor": true,
	"geciyor": true, "bul": true, "soyle": true, "soyler": true, "bahset": true,
	// EN
	"the": true, "and": true, "for": true, "with": true, "how": true, "what": true, "is": true,
	"why": true, "which": true, "does": true, "do": true, "this": true, "that": true, "are": true,
	"to": true, "of": true, "in": true, "on": true, "an": true, "or": true, "can": true, "we": true,
	"be": true, "where": true, "when": true, "who": true, "my": true, "our": true, "should": true,
	"about": true, "from": true, "it": true, "as": true, "at": true, "by": true, "there": true,
	"any": true, "you": true, "me": true, "please": true, "tell": true, "explain": true, "show": true,
	"did": true, "was": true, "were": true, "will": true, "would": true, "could": true,
	"docs": true, "documentation": true, "page": true,
}

// queryTermsMax — sorgu başına terim tavanı (CH sorgusundaki dizi boyu).
const queryTermsMax = 12

// liveWordsMax — canlı arama sorgusundaki sözcük tavanı.
const liveWordsMax = 8

// LiveSearchQueries — SAF (v0.10.1124): Azure DevOps Search'e gidecek iki
// sorgu metni. Kök neden: canlı yedek operatörün SORU CÜMLESİNİ olduğu gibi
// gönderiyordu; ADO Search çok terimli sorguyu varsayılan AND ile birleştirir,
// "nasıl", "ederim", "nedir" gibi soru sözcükleri sayfalarda geçmediği için
// tipik bir Türkçe soru SIFIR sonuç döndürüyordu.
//
//   - and: stopword'süz ÖZGÜN yazımlı sözcükler (katlanmaz — sunucunun kendi
//     çözümleyicisi "şifre"yi "sifre"den ayırabilir), boşlukla (AND).
//   - or:  aynı sözcükler " OR " ile; AND boş dönerse denenir (Türkçe ek
//     uyuşmazlığı: "servisini" geçen soru "servis" geçen sayfayı AND ile
//     kaçırır). Tek sözcükte "" (ikinci istek gereksiz).
//
// ADO sorgu sözdizimi kaçışı: tırnak/parantez/joker zaten bölücü; büyük
// harfli AND/OR/NOT/NEAR operatör sayılmasın diye küçük harfe çevrilir; ":"
// alan süzgeci sayılmasın diye bölücüdür.
func LiveSearchQueries(q string) (and, or string) {
	words := strings.FieldsFunc(q, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' && r != '/'
	})
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		w = strings.Trim(w, "-_./")
		if w == "" {
			continue
		}
		f := Fold(w)
		if stopwords[f] || seen[f] || !keepWord(f) {
			continue
		}
		seen[f] = true
		switch strings.ToUpper(w) {
		case "AND", "OR", "NOT", "NEAR":
			w = strings.ToLower(w)
		}
		out = append(out, w)
		if len(out) >= liveWordsMax {
			break
		}
	}
	if len(out) == 0 {
		return "", ""
	}
	and = strings.Join(out, " ")
	if len(out) > 1 {
		or = strings.Join(out, " OR ")
	}
	return and, or
}

// QueryTerms — sorgunun arama terimleri: Tokens, stopword'süz, tekrarsız,
// en çok queryTermsMax. Bileşikler ÖNCE gelir (tavana takılınca kaybolan
// genel parça olsun, tam tanımlayıcı değil). SAF.
func QueryTerms(q string) []string {
	toks := Tokens(q)
	seen := map[string]bool{}
	var compounds, words []string
	for _, t := range toks {
		if stopwords[t] || seen[t] {
			continue
		}
		seen[t] = true
		if strings.IndexFunc(t, isJoiner) >= 0 {
			compounds = append(compounds, t)
		} else {
			words = append(words, t)
		}
	}
	out := append(compounds, words...)
	if len(out) > queryTermsMax {
		out = out[:queryTermsMax]
	}
	return out
}
