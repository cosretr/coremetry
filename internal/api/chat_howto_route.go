package api

// chat_howto_route.go — v0.10.2140 (CoSRE router boşlukları D7,
// docs/cosre/router-gaps-audit.md I, J, K). Yalnız v2 (cosreV2Marked).
//
// Üç şekil deterministik kademeden kaçıyordu:
//
//   - I yol tarifi: how_to rotası yalnız LLM sınıflandırıcısından geliyordu;
//     "hangi menüden", "nereye yazacağım", "hangi adımda görebilirim",
//     "nereden bakabilirim" router'da hiçbir dala oturmuyordu. Şimdi şekil
//     how_to rotasına gider: adımlar + TEK TIK link çipi, sayfa kendiliğinden
//     AÇILMAZ (v0.10.809).
//   - K nöbet çizelgesi: veri Coremetry'de yok. Operatör kararı 1
//     (2026-11-14): kaynak bağlanmaz; cevap dürüstçe "yok" der, çip yok.
//   - J değerle trace araması ("<anahtar> no <değer> … trace ara"): operatör
//     kararı 2: en çok 24 saatlik pencere sohbette aranır (mevcut
//     trace_search demeti); daha uzun aralık aranmaz, aynı değer ve aralıkla
//     süzülmüş Traces sayfası link çipi verilir.
//
// Yeni niyet yok: how_to ve trace_search rotaları bayrak taşır. v1 yolunda
// v2HowToRoute hiç değişiklik yapmaz → bayt bayt eski.

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// /ai etkileşim satırındaki alt rota adları (guidedRouteLabel).
const (
	guidedNotAvailableRoute    = "not_available"
	guidedTraceSearchLinkRoute = "trace_search_link"
)

// chatTraceSearchMaxS — sohbette trace aramasının en uzun penceresi.
const chatTraceSearchMaxS = 24 * 3600

// traceLinkOnly — pencere 24 saati aşan değer araması: aranmaz, link verilir.
type traceLinkOnly struct {
	From, To time.Time
	Label    string // "7 Eylül – 15 Eylül" / "son 7 gün"
}

// howToNavVerbs — yol soran fiil biçimleri (birinci tekil / yeterlilik /
// edilgen geniş zaman). "görülüyor", "geliyor", "çağrılıyor" BİLEREK yok:
// "hata nerede görülüyor" ve "nereden çağrılıyor" veri sorusudur.
var howToNavVerbs = []string{
	"görürüm", "görürüz", "görebil", "göreceğ", "görülür",
	"bakarım", "bakarız", "bakabil", "bakacağ", "bakılır",
	"giderim", "gidebil", "gideceğ", "gidilir",
	"ulaşırım", "ulaşabil", "ulaşacağ", "ulaşılır",
	"erişirim", "erişebil", "erişilir",
	"bulurum", "bulabil", "bulacağ", "bulunur",
	"açarım", "açabil", "açacağ", "açılır",
}

// hasHowToNavShape — SAF: "nereden / hangi menüden / hangi adımda / nereye
// yazacağım" biçimli YOL sorusu (veri sorusu değil).
func hasHowToNavShape(norm string, toks []string) bool {
	if strings.Contains(norm, "hangi menü") || strings.Contains(norm, "hangi menu") {
		return true
	}
	verb := tokenHasPrefix(toks, howToNavVerbs...)
	for _, p := range []string{"hangi adım", "hangi sayfa", "hangi ekran", "hangi sekme"} {
		if strings.Contains(norm, p) {
			return verb
		}
	}
	if tokenHasPrefix(toks, "nereye") {
		return verb || tokenHasPrefix(toks, "yazar", "yazab", "yazac", "yazmal", "yazıl", "girer", "gireb", "girec", "girmel", "tıkla")
	}
	for _, t := range toks {
		switch t {
		case "nereden", "nerden", "nerede", "nerde":
			return verb
		case "nasıl": // "öznitelikle trace nasıl ararım", "trace detayına nasıl giderim"
			return verb || tokenHasPrefix(toks, "ararım", "arayabil", "aranır", "süzerim", "süzebil")
		}
	}
	return false
}

// hasOncallAsk — SAF: nöbet çizelgesi / nöbetçi sorusu. "gece nöbetinde ne
// oldu" (vardiya özeti) buraya girmez: kişi / çizelge sözcüğü ister.
func hasOncallAsk(norm string, toks []string) bool {
	if tokenHasPrefix(toks, "nöbetçi", "nobetci") {
		return true
	}
	who := tokenHasPrefix(toks, "kim", "çizelge", "cizelge", "liste", "takvim", "who", "schedule", "rota")
	if tokenHasPrefix(toks, "nöbet", "nobet", "oncall", "on-call") || strings.Contains(norm, "on call") {
		return who
	}
	return false
}

// oncallNotAvailableTR — K: dürüst cevap; link ve çip yok.
const oncallNotAvailableTR = "Nöbet çizelgesi Coremetry'de yok. Coremetry trace, log, metrik ve problem verisini tutar; " +
	"nöbet listesi için bağlı bir kaynak bulunmuyor, bu yüzden kimin nöbetçi olduğunu söyleyemem."

var attrValueAskRe = regexp.MustCompile(`(?i)(?:^|\s)[\p{L}][\p{L}\d._]{1,30}\s+(?:no|numarası|numarasi|numaralı|numarali|id|kodu)\s*[:=]?\s*([\p{L}\d_-]{3,40})`)

// v2AttrValueAsk — SAF: "<anahtar> no <değer> … trace ara/bul/getir" şekli;
// değer rakam taşımalı ("trace id nedir" gibi düz sözcük değer sayılmaz).
func v2AttrValueAsk(raw string, toks []string) (string, bool) {
	if !tokenHasPrefix(toks, "trace") ||
		!tokenHasPrefix(toks, "ara", "bul", "getir", "listele", "göster", "goster", "search", "find") {
		return "", false
	}
	m := attrValueAskRe.FindStringSubmatch(raw)
	if m == nil || !strings.ContainsAny(m[1], "0123456789") {
		return "", false
	}
	return m[1], true
}

// trMonthAlt — ay adları; "ocağa" / "aralığa" (ünsüz yumuşaması) dahil.
const trMonthAlt = `oca[kğ]|şubat|subat|mart|nisan|mayıs|mayis|haziran|temmuz|ağustos|agustos|eylül|eylul|ekim|kasım|kasim|aral[ıi][kğ]`

var (
	// "7 eylülden 15 eylüle", "7 eylül - 15 eylül", "7 eylül ile 15 eylül arası"
	longRangeTwoRe = regexp.MustCompile(`(\d{1,2})\s+(` + trMonthAlt + `)[\p{L}'’]*\s*(?:-|–|—|ile|ve)?\s*(\d{1,2})\s+(` + trMonthAlt + `)`)
	// "7-15 eylül"
	longRangeOneRe = regexp.MustCompile(`(\d{1,2})\s*(?:-|–|—)\s*(\d{1,2})\s+(` + trMonthAlt + `)`)
	// "son 7 gün", "son 2 hafta"
	longRangeRelRe = regexp.MustCompile(`son\s+(\d{1,3})\s+(gün|gun|hafta)`)
)

var trMonthNames = [...]string{"", "Ocak", "Şubat", "Mart", "Nisan", "Mayıs", "Haziran", "Temmuz", "Ağustos", "Eylül", "Ekim", "Kasım", "Aralık"}

// v2LongSearchWindow — SAF: sorudaki pencere 24 saati aşıyorsa o pencere.
// Gün aralığı iki ucu da kapsar (bitiş gününün sonu). Yılsız tarih en yakın
// GEÇMİŞ yıl. 24 saate sığan ya da okunamayan pencere ok=false (mevcut yol).
func v2LongSearchWindow(raw string, now time.Time, loc *time.Location) (traceLinkOnly, bool) {
	lw := strings.ToLower(raw)
	day := func(d int, mon string) (time.Time, bool) {
		if strings.HasSuffix(mon, "ğ") {
			mon = strings.TrimSuffix(mon, "ğ") + "k"
		}
		m, ok := absMonthsTR[mon]
		if !ok || d < 1 || d > 31 {
			return time.Time{}, false
		}
		t := time.Date(now.In(loc).Year(), m, d, 0, 0, 0, 0, loc)
		if t.After(now) {
			t = t.AddDate(-1, 0, 0)
		}
		return t, true
	}
	span := func(d1 int, m1 string, d2 int, m2 string) (traceLinkOnly, bool) {
		from, ok1 := day(d1, m1)
		end, ok2 := day(d2, m2)
		if !ok1 || !ok2 {
			return traceLinkOnly{}, false
		}
		if end.Before(from) { // "28 aralıktan 3 ocağa": başlangıç bir önceki yıl
			from = from.AddDate(-1, 0, 0)
		}
		to := end.AddDate(0, 0, 1)
		if to.Sub(from) <= chatTraceSearchMaxS*time.Second {
			return traceLinkOnly{}, false
		}
		label := fmt.Sprintf("%d %s – %d %s", from.Day(), trMonthNames[from.Month()], end.Day(), trMonthNames[end.Month()])
		return traceLinkOnly{From: from, To: to, Label: label}, true
	}
	if m := longRangeTwoRe.FindStringSubmatch(lw); m != nil {
		d1, _ := strconv.Atoi(m[1])
		d2, _ := strconv.Atoi(m[3])
		return span(d1, m[2], d2, m[4])
	}
	if m := longRangeOneRe.FindStringSubmatch(lw); m != nil {
		d1, _ := strconv.Atoi(m[1])
		d2, _ := strconv.Atoi(m[2])
		return span(d1, m[3], d2, m[3])
	}
	if m := longRangeRelRe.FindStringSubmatch(lw); m != nil {
		n, _ := strconv.Atoi(m[1])
		days := n
		if m[2] == "hafta" {
			days = n * 7
		}
		if days < 2 || days > 366 {
			return traceLinkOnly{}, false
		}
		return traceLinkOnly{From: now.AddDate(0, 0, -days), To: now, Label: "son " + m[1] + " " + m[2]}, true
	}
	return traceLinkOnly{}, false
}

// v2HowToGate — SAF: sıfır-maliyet kapısının D7 eki (mesaj başka guided
// sinyali taşımasa da router'a girsin).
func v2HowToGate(norm string) bool {
	toks := guidedTokens(norm)
	return hasHowToNavShape(norm, toks) || hasOncallAsk(norm, toks)
}

// howToDataRoutes — yol sorusu bu VERİ rotalarına düşmüşse (ad geçen servis
// sağlık dalını tetikler) ve kılavuzda konu varsa how_to kazanır.
var howToDataRoutes = map[guidedIntent]bool{
	guidedServiceHealth: true, guidedLogErrors: true, guidedSlowTraces: true, guidedProblems: true,
	guidedDBHealth: true, guidedDeployImpact: true, guidedPodHealth: true,
}

// v2HowToRoute — SAF: router kararının D7 düzeltmesi. svc = mesajda adı
// geçen servis ("" olabilir). changed=false → rota aynen.
func v2HowToRoute(route guidedRoute, raw, norm, svc string, v2 bool, now time.Time, loc *time.Location) (guidedRoute, bool) {
	if !v2 {
		return route, false
	}
	toks := guidedTokens(norm)
	weak := route.Intent == guidedNone || route.Intent == guidedFindEntity
	if weak && hasOncallAsk(norm, toks) {
		return guidedRoute{Intent: guidedHowTo, NotAvailable: "oncall"}, true
	}
	if weak {
		if val, ok := v2AttrValueAsk(raw, toks); ok {
			r := guidedRoute{Intent: guidedTraceSearch, Service: svc, Env: route.Env, SearchText: val}
			if w, long := v2LongSearchWindow(raw, now, loc); long {
				r.LinkOnly = &w
			}
			return r, true
		}
	}
	if !hasHowToNavShape(norm, toks) {
		// "trace detay sayfasını açar mısın": servissiz open_page servis sorardı;
		// konu v2 kılavuzundaysa yol tarifi (link çipi, sayfa AÇILMAZ).
		if route.Intent == guidedOpenPage && route.Service == "" {
			if ans, ok := mcptools.GuideForV2("", raw, "", ""); ok && mcptools.IsGuideTopicV2Only(ans.Topic) {
				return guidedRoute{Intent: guidedHowTo, Env: route.Env}, true
			}
		}
		return route, false
	}
	if weak {
		return guidedRoute{Intent: guidedHowTo, Service: svc, Env: route.Env}, true
	}
	if howToDataRoutes[route.Intent] {
		if _, ok := mcptools.GuideForV2("", raw, route.Service, ""); ok {
			return guidedRoute{Intent: guidedHowTo, Service: route.Service, Env: route.Env}, true
		}
	}
	return route, false
}

// traceSearchLinkHref — SAF: aynı değer ve aralıkla süzülmüş Traces linki.
func traceSearchLinkHref(route guidedRoute) string {
	href := "/traces?search=" + url.QueryEscape(route.SearchText)
	if route.Service != "" {
		href += "&service=" + url.QueryEscape(route.Service)
	}
	if route.LinkOnly == nil {
		return href
	}
	return linkWindowBetween(route.LinkOnly.From, route.LinkOnly.To).apply(href)
}

// traceSearchLinkOnlyTR — SAF: 24 saati aşan değer aramasının cevabı.
func traceSearchLinkOnlyTR(route guidedRoute) string {
	return fmt.Sprintf("Sohbette trace araması en çok 24 saatlik pencereyi tarar; istediğin aralık (%s) bundan uzun, bu yüzden burada aramadım. "+
		"Aşağıdaki bağlantı Traces sayfasını %q araması ve bu aralıkla açar (tıklayınca açılır; sayfa kendiliğinden değişmez).",
		route.LinkOnly.Label, route.SearchText)
}

// guidedD7DirectAnswer — nöbet ("yok") ve link-yalnız arama: LLM'siz, sorgusuz.
func (s *Server) guidedD7DirectAnswer(emit func(string, any), route guidedRoute) (handled, ok bool) {
	switch {
	case route.Intent == guidedHowTo && route.NotAvailable != "":
		emit("answer", map[string]any{"text": oncallNotAvailableTR})
		return true, true
	case route.Intent == guidedTraceSearch && route.LinkOnly != nil:
		emit("answer", map[string]any{
			"text":  traceSearchLinkOnlyTR(route),
			"links": []guidedAnswerLink{{Label: "Trace'ler (" + route.SearchText + " · " + route.LinkOnly.Label + ")", Href: traceSearchLinkHref(route)}},
		})
		return true, true
	}
	return false, false
}
