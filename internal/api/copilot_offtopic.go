package api

// copilot_offtopic.go — v0.10.819 (operatör kararı 2026-09-19, prod kullanıcı
// soruları: "Amerikan başkanı kimdir", "Türkiye nüfusu kaçtır", bilmece…):
// konu dışı soru MODEL cevabı almaz — deterministik kibar sınır + ürün yol
// tarifi. v0.10.194'ün "eşleştiremese de cevap versin" direktifi yalnız
// KALAN none sınıfı (muğlak ama telemetriye yakın soru) için sürer.
//
// Akış: sınıflandırıcı (copilot_intent.go) `off_topic` ÜRETİR, sunucu
// VETOLAR (offTopicVeto): mesajda canlı bir servis/ortam/takım adı, bir
// kimlik ya da telemetri sözcüğü varsa off_topic değildir → none'a düşer
// (mevcut yol). Yanlış off_topic = gerçek telemetri sorusunu reddetmek;
// yanlış veto = eski genel cevap. Veto o yüzden bol keseden.
//
// Router'da regex erken-çıkış YOK (bilinçli): "bugün hava nasıl" → none pinleri
// (6 test) ve generic "nasıl" sorunu; bedeli konu dışı soru başına bir
// sınıflandırıcı çağrısı.
//
// Cevap şekli how_to ile aynı (copilot_howto.go): text + suggestions + links,
// `open` YOK (tek tık), exchangeId YOK (deterministik metin oylanmaz).

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/cilcenk/coremetry/internal/reqid"
)

// offTopicBoundaryTR — kibar sınır. Prompt açılışı gibi ("Sen …") BAŞLAMAZ:
// prompt çıpası kapısı (prompt_language_test.go) api dosyalarını tarar.
const offTopicBoundaryTR = "Bu soru Coremetry'nin izlediği telemetriyle ilgili değil. Ben yalnız servislerin, trace'lerin, logların, metriklerin ve problemlerin sorularını cevaplarım; genel bilgi, güncel olay ya da sohbet sorularına cevap vermiyorum."

// offTopicFirmTR — zararlı/uygunsuz istek: aynı sınır, daha kesin.
const offTopicFirmTR = "Bu istek Coremetry'nin kapsamı dışında; bu konuda yardımcı olamam. Ben yalnız servislerin, trace'lerin, logların, metriklerin ve problemlerin sorularını cevaplarım."

// offTopicHarmfulStems — küçük harf KÖK; yalnız METNİN tonunu değiştirir
// (firm) ve genel-soru yolunu kapatır (chat_offtopic_general.go).
//
// v0.10.2113 (operatör: "hackathon nedir", "sızma testi nedir",
// "silahsızlanma antlaşması", "şifremi çaldılar ne yapayım" ret alıyordu) —
// eşleşme ALT-DİZE değil SÖZCÜK: kök bir jetonla (guidedTokens) eşleşir ya
// jetonun tamamıysa ya da jeton = kök + yalnız ÇEKİM/kısa ek zinciri ise
// (offTopicSuffixRE: "bombayı", "silahla", "hacklemek", "ddos'u" — kesme
// işareti zaten jeton sınırı). Türetilmiş başka sözcük ("hackathon",
// "silahsızlanma") eşleşmez: kalan kısım ek zincirine ayrışmaz. Çok sözcüklü
// kök ("zararlı yazılım") ardışık jetonlarla. Liste içeriği DEĞİŞMEDİ.
// Kararsız ekler (-cı "bombacı", -lı "silahlı") zincirde: mevcut ret korunur.
var offTopicHarmfulStems = []string{
	"hack", "sızma", "sizma", "exploit", "zararlı yazılım", "malware", "keylogger",
	"ddos", "bomba", "silah", "intihar", "uyuşturucu",
}

// offTopicCompoundOK — v0.10.2113: kökün bir sonraki jetonla kurduğu ZARARSIZ
// terim (yalnız kök jetonun TAMAMIYSA): "sızma testi" = penetrasyon testi
// (savunma mesleği). Sonraki jeton bu öneklerden biriyle başlamalı.
var offTopicCompoundOK = map[string][]string{
	"sızma": {"test"},
	"sizma": {"test"},
}

// offTopicSuffixRE — v0.10.2113: kökten sonra kalan kısmın ÇEKİM / kısa ek
// zinciri olup olmadığı (çoğul, iyelik, hâl, -ki, -cı, -lı, ek-fiil, fiil
// yapma -la/-le + mastar, zaman, kip, kişi). "-sız" (yokluk: silahsız,
// silahsızlanma) BİLİNÇLİ YOK; tek harf "z" yok (aksi hâlde "sı"+"z" yoluyla
// geri gelirdi); tek "s" yalnız EN SONDA. Zincir boş da olabilir (jeton = kök).
var offTopicSuffixRE = regexp.MustCompile(`^(?:` +
	`lar|ler|ları|leri|` +
	`ım|im|um|üm|ın|in|un|ün|ı|i|u|ü|m|n|k|` +
	`sı|si|su|sü|yı|yi|yu|yü|` +
	`a|e|ya|ye|da|de|ta|te|dan|den|tan|ten|` +
	`la|le|yla|yle|nın|nin|nun|nün|ki|` +
	`cı|ci|cu|cü|çı|çi|çu|çü|lı|li|lu|lü|` +
	`mız|miz|muz|müz|nız|niz|nuz|nüz|ız|iz|uz|üz|` +
	`dır|dir|dur|dür|tır|tir|tur|tür|` +
	`mak|mek|ma|me|r|ar|er|ır|ir|ur|ür|` +
	`dı|di|du|dü|tı|ti|tu|tü|mış|miş|muş|müş|yor|` +
	`acak|ecek|yacak|yecek|sa|se|ken|ebil|abil|yebil|yabil|` +
	`ıl|il|ul|ül|en|an|yen|yan|` +
	`ing|ed` + // İngilizce kök ("hacking", "hacked") — ret korunur
	`)*s?$`) // sondaki İngilizce çoğul ("hacks", "exploits"); ortada tek "s" YOK (silahsız)

// offTopicStemMatch — SAF: jeton kökü taşıyor mu (tam ya da kök + ek zinciri).
func offTopicStemMatch(tok, stem string) bool {
	if !strings.HasPrefix(tok, stem) {
		return false
	}
	return offTopicSuffixRE.MatchString(tok[len(stem):])
}

// offTopicHarmfulStemHit — SAF: jetonlarda bir zararlı kök var mı.
func offTopicHarmfulStemHit(toks []string) bool {
	for _, stem := range offTopicHarmfulStems {
		parts := strings.Fields(stem)
		n := len(parts)
		for i := 0; i+n <= len(toks); i++ {
			ok := true
			for j := 0; j < n-1; j++ { // çok sözcüklü kökte baştaki sözcükler TAM
				if toks[i+j] != parts[j] {
					ok = false
					break
				}
			}
			last := toks[i+n-1]
			if !ok || !offTopicStemMatch(last, parts[n-1]) {
				continue
			}
			if last == parts[n-1] && i+n < len(toks) && offTopicCompoundNext(parts[n-1], toks[i+n]) {
				continue
			}
			return true
		}
	}
	return false
}

func offTopicCompoundNext(stem, next string) bool {
	for _, p := range offTopicCompoundOK[stem] {
		if strings.HasPrefix(next, p) {
			return true
		}
	}
	return false
}

var offTopicSecretWords = []string{"şifre", "sifre", "parola", "password", "wifi"}

// offTopicCrackStems — jeton ÖNEKİ ("kırarım", "çalınır", "cracklemek"); "çalış-"
// (çalışır/çalışmıyor) hariç — Türkçenin en sık fiili, "çal" önekini taşır
// (inceleme 2026-09-19: "parola yöneticisi nasıl çalışır" zararlı sayılıyordu).
var offTopicCrackStems = []string{"kır", "kir", "crack", "çal", "cal"}
var offTopicCrackExcl = []string{"çalış", "calis", "calc", "kira", "kirl", "kirp"}

// offTopicCrackPastRE — v0.10.2113: kırma/çalma fiilinin GEÇMİŞ zamanı
// ("çaldılar", "kırdım", "çalındı", "kırıldı") olanı bildirir (mağdur:
// "şifremi çaldılar ne yapayım"), nasıl yapılacağını sormaz → kırma sayılmaz.
// Edilgen ek (-ıl/-ın) isteğe bağlı; ardından -dı/-tı ailesi.
var offTopicCrackPastRE = regexp.MustCompile(`^(?:[ıiuü][ln])?[dt][ıiuü]`)

// offTopicIsHarmful — SAF: normalize edilmiş mesaj.
func offTopicIsHarmful(norm string) bool {
	toks := guidedTokens(norm)
	if offTopicHarmfulStemHit(toks) {
		return true
	}
	secret := false
	for _, s := range offTopicSecretWords {
		if strings.Contains(norm, s) {
			secret = true
			break
		}
	}
	if !secret {
		return false
	}
	if strings.Contains(norm, "ele geçir") {
		return true
	}
	for _, t := range toks {
		excluded := false
		for _, x := range offTopicCrackExcl {
			if strings.HasPrefix(t, x) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		for _, s := range offTopicCrackStems {
			if strings.HasPrefix(t, s) && !offTopicCrackPastRE.MatchString(t[len(s):]) {
				return true
			}
		}
	}
	return false
}

// hasTelemetryVocab — SAF: off_topic vetosunun sözcük yarısı. hasGuidedSignal'ın
// telemetri sinyalleri (generic "nasıl/durum/iyi" HARİÇ — hasHealthDomainSignal)
// + p50/p75/p90, süre/gecikme/throughput/timeout/cpu/bellek, metrik, namespace,
// cluster, slo, istek/request, servis sözcüğü, exception sözcüğü.
func hasTelemetryVocab(msg string, toks []string) bool {
	return hasSlowTraceSignal(msg) || hasDeploySignal(toks) || hasLogSignal(toks) ||
		hasErrorSignal(toks) || hasProblemSignal(toks) || hasHealthDomainSignal(toks) ||
		hasTeamSelfSignal(toks) || hasPodSignal(toks) || hasShiftSignal(msg, toks) ||
		hasDBSignal(toks) || hasMessagingSignal(toks) || hasPeriodSignal(toks) ||
		hasEndpointRequestSignal(toks) || hasServiceListWord(toks) || hasExceptionWord(toks) ||
		tokenHasPrefix(toks, "trace", "span", "metrik", "metric", "p50", "p75", "p90",
			"throughput", "rps", "timeout", "cpu", "bellek", "memory", "namespace", "cluster",
			"slo", "gecikme", "süre", "sure", "latency", "istek", "request", "endpoint",
			"operation", "operasyon", "alarm", "anomali", "anomaly", "incident", "exception")
}

// offTopicVeto — SAF: sınıflandırıcı off_topic dediyse sunucunun son sözü.
// Mesajda canlı servis/ortam/takım adı, bir kimlik (trace/span/istek) ya da
// telemetri sözlüğü varsa vetolar (why dolu). Katalog CANLI listedir
// (guidedServiceNames); boş katalogda basamak zaten atlanır (copilot_intent.go).
func offTopicVeto(question string, services, envs, teams []string) (why string, vetoed bool) {
	msg := normalizeGuidedMsg(question)
	toks := guidedTokens(msg)
	if extractTraceID(msg) != "" || extractSpanID(msg) != "" {
		return "hex kimlik", true
	}
	if hasStructuredRequestID(msg) {
		return "istek kimliği", true
	}
	if tok, ok := reqid.FindLooseToken(question); ok {
		return "kimlik benzeri: " + tok, true
	}
	if svc := extractServiceEntity(msg, services, envs); svc != "" {
		return "servis adı: " + svc, true
	}
	if c := serviceCandidates(msg, services, envs, 8); len(c) > 0 {
		return "servis adayı: " + c[0], true
	}
	if fam := extractServiceFamily(msg, services, envs); len(fam) > 0 {
		return "servis ailesi", true
	}
	if env := extractEnvEntity(msg, envs); env != "" {
		return "ortam adı: " + env, true
	}
	// v0.10.1380 — belirsiz kısa ortam anması ("eu-a" → prod-eu-a + dr-eu-a)
	// tek ortama çözülmese de konu dışı değildir.
	if ms := findEnvMentions(msg, envs); len(ms) > 0 {
		return "ortam adı: " + strings.Join(ms[0].Values, ", "), true
	}
	if team := extractTeamEntity(msg, teams); team != "" {
		return "takım adı: " + team, true
	}
	if hasTelemetryVocab(msg, toks) {
		return "telemetri sözcüğü", true
	}
	return "", false
}

// offTopicGuideTopics — sınır cevabının altındaki yol tarifi çipleri (Ask
// metinleri, product_guide.go); sıra = tablo sırası.
var offTopicGuideTopics = []string{"error_traces", "slow_traces", "problems", "ask_cosre"}

// offTopicChips — SAF: ekranda servis varsa servis kapsamlı sorular önce,
// ardından yol tarifi çipleri; ≤6, tekrarsız.
func offTopicChips(ctxService string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s == "" || seen[s] || len(out) >= 6 {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	if ctxService != "" {
		for _, s := range guidedSuggestions(guidedRoute{Intent: guidedServiceHealth, Service: ctxService}) {
			add(s)
		}
	}
	for _, s := range howToTopicChips(offTopicGuideTopics) {
		add(s)
	}
	return out
}

// offTopicGuidanceTR — SAF: sınır metninin altındaki ürün yol tarifi.
func offTopicGuidanceTR(ctxService string) string {
	svc := "<servis>"
	if ctxService != "" {
		svc = ctxService
	}
	return "Şöyle sorabilirsin:\n" +
		"- Servis sağlığı: \"" + svc + " son 1 saatte nasıl?\"\n" +
		"- Hatalı ya da yavaş trace'ler: \"" + svc + " en yavaş trace'ler?\"\n" +
		"- Açık problemler: \"açık problemler neler?\"\n" +
		"- Yol tarifi: \"hatalı trace'lere nasıl ulaşırım?\""
}

// offTopicAnswerText — SAF: ton + yol tarifi.
func offTopicAnswerText(question, ctxService string) string {
	head := offTopicBoundaryTR
	if offTopicIsHarmful(normalizeGuidedMsg(question)) {
		head = offTopicFirmTR
	}
	return head + "\n\n" + offTopicGuidanceTR(ctxService)
}

// offTopicLinks — SAF: tek tık bağlantılar; ekrandaki servis varsa önce o.
func offTopicLinks(ctxService string) []guidedAnswerLink {
	var links []guidedAnswerLink
	if ctxService != "" {
		links = append(links, guidedAnswerLink{Label: ctxService + " · Overview", Href: "/service?name=" + url.QueryEscape(ctxService)})
	}
	links = append(links,
		guidedAnswerLink{Label: "Servisler", Href: "/services"},
		guidedAnswerLink{Label: "Problemler", Href: "/problems"},
		guidedAnswerLink{Label: "Trace'ler", Href: "/traces"},
	)
	return dedupLinksByHref(links)
}

// guidedOffTopicAnswer — LLM yok; adım çipi + kanıt (metnin kendisi) + answer.
// `open` YAZILMAZ (sayfa kendiliğinden değişmez), exchangeId YOK.
func (s *Server) guidedOffTopicAnswer(emit func(string, any), question, ctxService string) (bool, bool) {
	args, _ := json.Marshal(map[string]string{"q": clipRunes(question, 120), "service": ctxService})
	n := emitGuidedStep(emit, "off_topic", string(args))
	text := offTopicAnswerText(question, ctxService)
	emitGuidedStepResult(emit, n, "off_topic", text, nil)
	emit("answer", map[string]any{
		"text":        text,
		"suggestions": offTopicChips(ctxService),
		"links":       offTopicLinks(ctxService),
	})
	return true, true
}

// clipRunes — SAF: rune güvenli kırpma (adım çipi argümanı).
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
