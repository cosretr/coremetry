package api

// chat_allowed_links.go — v0.10.1137: cevap metnindeki bağlantıların
// doğrulama listesi (allowedLinks).
//
// Operatör (prod): CoSRE wiki cevabındaki Jenkins adresi düz metin çiziliyordu
// — sohbet balonu bilinçli olarak hiç link basmıyordu (chatMarkdown.ts). Link
// açmanın riski prompt-injection: wiki sayfasına gömülü bir talimat modele
// "şu adrese ?q=<gizli> ekle" dedirtebilir ve tıklanır bir link veri sızdırır.
//
// Karar: arayüz bir http(s) bağlantısını YALNIZ sunucunun "bu adres modele
// verilen kaynakta gerçekten vardı" dediği listede (ya da kaynak çiplerinin
// host'unda, ya da aynı-köken yolda) tıklanır çizer; gerisi düz metin +
// "doğrulanmamış bağlantı" ipucu. Liste: cevabın çip href'leri + kaynak
// ref'leri + modele GERÇEKTEN verilen wiki/doküman bağlamında geçen URL'ler
// (önceki soru/cevap bloğu HARİÇ — orası model çıktısı, kaynak değil).
// Katı regex, tekil, en çok allowedLinksMax.

import (
	"regexp"
	"strings"
)

// allowedLinksMax — cevap başına liste tavanı (yük şişmesin; bağlam zaten
// bütçeli, 50 adres bir cevabın atıf yapabileceğinden çok fazla).
const allowedLinksMax = 50

// allowedLinkRe — katı http(s) URL: host harf/rakam/nokta/tire, isteğe bağlı
// port, yol/sorgu/parça boşluk, tırnak, açı/köşeli/normal parantez, ters
// tırnak ve süslü parantez İÇERMEZ (markdown `[x](url)` içinde `)` URL'yi
// bitirir; HTML özniteliğinde tırnak bitirir).
var allowedLinkRe = regexp.MustCompile(`(?i)\bhttps?://[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?(?:[/?#][^\s<>"'` + "`" + `()\[\]{}|\\^]*)?`)

// trimURLPunct — SAF: cümle sonu noktalaması URL'nin parçası sayılmaz.
func trimURLPunct(u string) string {
	return strings.TrimRight(u, ".,;:!?*_~")
}

// urlHost — SAF: "şema://" sonrası ilk "/?#"e dek (port dahil), küçük harf.
func urlHost(u string) string {
	i := strings.Index(u, "://")
	if i < 0 {
		return ""
	}
	rest := u[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	return strings.ToLower(rest)
}

// allowedLinkKey — SAF: tekilleştirme anahtarı (şema + host harf-duyarsız,
// yol olduğu gibi; sondaki tek "/" yok sayılır).
func allowedLinkKey(u string) string {
	i := strings.Index(u, "://")
	if i < 0 {
		return u
	}
	rest := u[i+3:]
	j := strings.IndexAny(rest, "/?#")
	host, tail := rest, ""
	if j >= 0 {
		host, tail = rest[:j], rest[j:]
	}
	if tail == "/" {
		tail = ""
	}
	return strings.ToLower(u[:i+3]+host) + tail
}

// extractContextURLs — SAF: metindeki http(s) URL'leri (ilk görülme sırası,
// tekil, en çok max).
func extractContextURLs(text string, max int) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range allowedLinkRe.FindAllString(text, -1) {
		if len(out) >= max {
			break
		}
		u := trimURLPunct(m)
		if h := urlHost(u); !strings.Contains(h, ".") && h != "localhost" && !strings.HasPrefix(h, "localhost:") {
			continue // tek etiketli host ("http://x") — katı: yalnız FQDN / localhost
		}
		k := allowedLinkKey(u)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, u)
	}
	return out
}

// isSameOriginPath — SAF: kök-göreli uygulama yolu ("/service?…"), "//host" değil.
func isSameOriginPath(h string) bool {
	return strings.HasPrefix(h, "/") && !strings.HasPrefix(h, "//") && !strings.ContainsAny(h, " \t\r\n\\")
}

func isHTTPURL(h string) bool {
	l := strings.ToLower(h)
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://")
}

// buildAllowedLinks — SAF: cevabın doğrulanmış bağlantı listesi. Sıra: çip
// href'leri (http(s) ya da aynı-köken yol), kaynak ref'leri, bağlam URL'leri.
// Tekil (allowedLinkKey), en çok allowedLinksMax; boş girdi boş dilim (JSON []).
func buildAllowedLinks(contextText string, links []guidedAnswerLink, sources []chatSource) []string {
	out := make([]string, 0, 8)
	seen := map[string]bool{}
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" || len(out) >= allowedLinksMax {
			return
		}
		if !isHTTPURL(u) && !isSameOriginPath(u) {
			return
		}
		k := allowedLinkKey(u)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, u)
	}
	for _, l := range links {
		add(l.Href)
	}
	for _, s := range sources {
		add(s.Ref)
	}
	for _, u := range extractContextURLs(contextText, allowedLinksMax) {
		add(u)
	}
	return out
}

// withAllowedLinks — cevap yüküne allowedLinks yazar: mevcut liste korunur,
// yükteki çip/kaynak href'leri ve contextText'in URL'leri eklenir. Çipler
// sonradan değişen yollarda (netleştirme çipleri, takip sayfası çipi) tekrar
// çağrılabilir — idempotent.
func withAllowedLinks(ans map[string]any, contextText string) {
	links, _ := ans["links"].([]guidedAnswerLink)
	sources, _ := ans["sources"].([]chatSource)
	prev, _ := ans["allowedLinks"].([]string)
	all := make([]guidedAnswerLink, 0, len(prev)+len(links))
	for _, u := range prev {
		all = append(all, guidedAnswerLink{Href: u})
	}
	all = append(all, links...)
	ans["allowedLinks"] = buildAllowedLinks(contextText, all, sources)
}
