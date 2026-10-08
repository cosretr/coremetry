package api

// chat_wiki_howto.go — v0.10.1126: geniş "nasıl yapılır / bilgi" işareti
// (ZAYIF wiki işareti; taban kapılı).
//
// Operatör (prod): "BSA cache refresh nasıl girilir", "Config cache refresh
// nasıl girilir?", "Coremetry adresleri" wiki'ye hiç uğramadı — v0.10.1124'ün
// zayıf işareti yalnız "nasıl" + yap- fiilini tanıyordu; guided "nasıl"ı sağlık
// sinyali sayıp "BSA"yı servis parçası olarak "Hangi servisi kastettin?"e
// çevirdi. Burada işaret GENİŞLER ama ZAYIF kalır: kademe yalnız en iyi isabet
// ragWikiFloor'u (0.5) geçerse cevaplar, yoksa akış bayt bayt eski yolda.
//
// Telemetri koruması (v0.10.1126 inceleme): çıplak "nasıl" İŞARET DEĞİLDİR
// ("svc-orders hata oranı nasıl", "latency nasıl", "nasıl gidiyor"). "nasıl"
// ancak ardından (≤3 sözcük içinde) EDİLGEN/KİŞİSİZ fiil gelirse işaret olur:
// ünsüz gövde + -ıl/-il/-ul/-ül + -ır/-ir/-ur/-ür (girilir, açılır, yapılır,
// çalıştırılır, görülür) ya da ünlü gövde + -n + -ır/-ir (eklenir, tanımlanır,
// yenilenir, temizlenir); ya da 1. kişi yeterlik/gereklilik (-abilirim,
// -malı/-meli) ve açık yap-/et- listesi. Telemetride geçen geçişsiz durum
// fiilleri (gelir, yükselir, düşer, düzelir, görünür, artar, azalır, değişir,
// çalışır) açıkça dışarıda. "nerede/nereden/erişim/where is" işaret DEĞİL
// ("hata nerede", "svc-orders erişim hatası"). Kademe ayrıca servis adı +
// telemetri sinyali taşıyan soruda zayıf işareti hiç kullanmaz
// (wikiWeakCueVetoed, chat_wiki_tier.go).

import "strings"

// wikiNasilModalSuffixes — 1. kişi yeterlik / gereklilik sonekleri (katlanmış).
var wikiNasilModalSuffixes = []string{
	"abilirim", "ebilirim", "abiliriz", "ebiliriz",
	"mali", "meli", "maliyim", "meliyim", "maliyiz", "meliyiz",
}

// wikiNasilExtraVerbs — açık 1. kişi yap-/et-/gir- biçimleri.
var wikiNasilExtraVerbs = []string{"ederim", "ederiz", "girerim", "gireriz"}

// wikiNasilStateVerbs — telemetri sorularında geçen geçişsiz durum fiilleri
// (katlanmış); edilgen kalıba benzese de işaret değil.
var wikiNasilStateVerbs = []string{
	"gelir", "yukselir", "duser", "duzelir", "gorunur", "artar", "azalir", "degisir", "calisir",
	"iyilesir", "dusurulur",
}

// wikiNasilVerbMinLen — en kısa fiil ("bir" gibi kısa sözcükler fiil sayılmasın).
const wikiNasilVerbMinLen = 5

// wikiHowToWordPrefixes — tek başına bilgi sorusu işaret eden katlanmış
// sözcük ÖNEKLERİ ("adımları", "adresleri").
var wikiHowToWordPrefixes = []string{"adimlar", "adres"}

// wikiHowToPhrases — katlanmış sözcük dizileri (sözcük sınırında).
var wikiHowToPhrases = []string{
	"ne yapmali", "kim sorumlu", "sahibi kim",
	"how do", "how can", "how to", "how should", "steps to", "who owns",
}

// wikiOwnershipPhrases — sahiplik soruları: servis adı çözülürse katalog kartı
// (find_entity) cevaplar; kademe bunları adlı soruda kullanmaz.
var wikiOwnershipPhrases = []string{"kim sorumlu", "sahibi kim", "who owns"}

func isVowel(b byte) bool { return strings.IndexByte("aeiou", b) >= 0 }

// wikiPassiveVerb — SAF: katlanmış sözcük edilgen/kişisiz geniş zaman mı.
// (a) ünsüz + [iu] + "l" + [iu] + "r" sonu: girilir, acilir, yapilir, gorulur;
// (b) ünlü + "n" + [iu] + "r" sonu: eklenir, tanimlanir, yenilenir.
func wikiPassiveVerb(w string) bool {
	n := len(w)
	if n < wikiNasilVerbMinLen || w[n-1] != 'r' || (w[n-2] != 'i' && w[n-2] != 'u') {
		return false
	}
	switch w[n-3] {
	case 'l':
		return (w[n-4] == 'i' || w[n-4] == 'u') && !isVowel(w[n-5])
	case 'n':
		return isVowel(w[n-4])
	}
	return false
}

// wikiNasilVerb — SAF: katlanmış sözcük "nasıl"dan sonra gelen nasıl-yapılır
// fiil biçimi mi.
func wikiNasilVerb(w string) bool {
	if len(w) < wikiNasilVerbMinLen {
		return false
	}
	for _, v := range wikiNasilStateVerbs {
		if w == v {
			return false
		}
	}
	for _, v := range wikiNasilVerbs {
		if w == v {
			return true
		}
	}
	for _, v := range wikiNasilExtraVerbs {
		if w == v {
			return true
		}
	}
	for _, suf := range wikiNasilModalSuffixes {
		if strings.HasSuffix(w, suf) {
			return true
		}
	}
	return wikiPassiveVerb(w)
}

// wikiOwnershipCue — SAF: katlanmış sözcüklerde sahiplik kalıbı var mı.
func wikiOwnershipCue(words []string) bool {
	norm := " " + strings.Join(words, " ") + " "
	for _, p := range wikiOwnershipPhrases {
		if strings.Contains(norm, " "+p+" ") {
			return true
		}
	}
	return false
}

// wikiHowToCue — SAF: katlanmış sözcük listesi nasıl-yapılır / bilgi sorusu
// mu (ZAYIF işaret).
func wikiHowToCue(words []string) bool {
	for i, w := range words {
		if w == "nasil" {
			for j := i + 1; j < len(words) && j <= i+3; j++ {
				if wikiNasilVerb(words[j]) {
					return true
				}
			}
		}
		for _, p := range wikiHowToWordPrefixes {
			if strings.HasPrefix(w, p) {
				return true
			}
		}
	}
	norm := " " + strings.Join(words, " ") + " "
	for _, p := range wikiHowToPhrases {
		if strings.Contains(norm, " "+p+" ") {
			return true
		}
	}
	return false
}
