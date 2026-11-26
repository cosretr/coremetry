package api

// chat_loop_cue.go — v0.10.2472 (DECISIONS "2026-11-26 — v0.10.2472").
//
// Döngü işareti: soru bir SIRALAMA ("en yavaş 5 endpoint", "hangi pod en çok
// memory"), SAYMA ("kaç trace hata aldı", "how many requests"), KIYAS
// ("checkout ile payments'ı karşılaştır"), ÇOK PARÇALI ("hangi servisleri
// çağırıyor ve hangisi en yavaş") ya da HACİM NEDENİ ("neden bu kadar çok 500
// alıyoruz") soruyor. Bu şekillerin hiçbiri kalıp kademelerinin hazır
// cevabına oturmaz: endpoint seçici kartı (v0.10.1737) soruyu "hangisini
// inceleyeyim?" diye keser, guided problems / slow_traces listesi sorulan
// sıralamayı / sayıyı vermez. İşaret ateşlerse o üç kalıp turu almaz ve tur
// araç döngüsüne gider. Soruyu zaten birebir cevaplayan rotalar
// (top_error_services, metric_chart, service_health …) işaretten etkilenmez:
// işaret yalnız loopCueVetoesGuided'daki rotaları ve endpoint kartını keser.
//
// SAF; Türkçe + İngilizce, katlanmış metin (foldWords) üzerinde.

import (
	"regexp"
	"strings"
)

// loopCueKind — işaretin sınıfı (adım çipi / iz notu; boş = işaret yok).
type loopCueKind string

const (
	loopCueNone    loopCueKind = ""
	loopCueRank    loopCueKind = "rank"
	loopCueCount   loopCueKind = "count"
	loopCueCompare loopCueKind = "compare"
	loopCueMulti   loopCueKind = "multi"
	loopCueWhyVol  loopCueKind = "why_volume"
)

var (
	// loopCueRankPhrases — sıralama kalıbı (katlanmış, iki uçta boşluklu).
	loopCueRankPhrases = []string{" en yavas ", " en hizli ", " en cok ", " en fazla ", " en yuksek ", " en dusuk ",
		" en uzun ", " en agir ", " en yogun ", " slowest ", " fastest ", " most ", " highest ", " lowest ",
		" busiest ", " top "}
	// loopCueRankNouns — sıralanan varlık (jeton öneki). "trace" BİLEREK yok:
	// "en yavaş trace'ler" slow_traces rotasının birebir cevabıdır.
	loopCueRankNouns = []string{"endpoint", "operasyon", "operation", "rota", "route", "url",
		"pod", "node", "container", "konteyner", "host", "sunucu", "server", "consumer", "topic", "kuyruk", "queue",
		"lag", "sorgu", "query", "tablo", "table", "namespace", "deployment", "instance", "uclar", "ucu"}
	// loopCueRankServiceWords — sıralanan servis (tam jeton): "servisinde" gibi
	// yer eki sıralama hedefi değildir ("checkout servisinde en yavaş trace'ler"
	// slow_traces'in birebir sorusu).
	loopCueRankServiceWords = map[string]bool{"servis": true, "servisler": true, "servisleri": true,
		"servislerin": true, "service": true, "services": true}
	// loopCueCountNouns — sayılan şey (jeton öneki). "hata" BİLEREK yok:
	// "son 3 saatte kaç hata oldu" açık problemler listesinin sorusudur.
	loopCueCountNouns = []string{"trace", "istek", "request", "cagri", "call", "span", "log", "pod", "endpoint",
		"servis", "service", "kez", "kere", "defa", "times", "kullanici", "user", "mesaj", "message"}
	// loopCueComparePrefixes — kıyas fiili (jeton öneki).
	loopCueComparePrefixes = []string{"karsilastir", "kiyas", "compare", "comparison"}
	// loopCueMultiPhrases — ikinci soru bağlacı.
	loopCueMultiPhrases = []string{" ve hangisi ", " ve hangi ", " ve en ", " ve kac ", " and which ",
		" and how many ", " and what ", " ayrica hangi "}
	// loopCueWhyVolPhrases — hacim vurgusu ("bu kadar çok", "so many").
	loopCueWhyVolPhrases = []string{" bu kadar ", " so many ", " so much ", " this many "}
)

// chatLoopCue — SAF: soru araç döngüsü gerektiren bir şekil mi (bkz. başlık).
func chatLoopCue(q string) loopCueKind {
	if strings.TrimSpace(q) == "" {
		return loopCueNone
	}
	f := foldWords(q)
	toks := strings.Fields(f)
	switch {
	case loopCueContainsAny(f, loopCueMultiPhrases):
		return loopCueMulti
	case loopCueCompareAsk(toks):
		return loopCueCompare
	case loopCueCountAsk(f, toks):
		return loopCueCount
	case loopCueRankAsk(f, toks):
		return loopCueRank
	case loopCueWhyVolAsk(f, toks):
		return loopCueWhyVol
	}
	return loopCueNone
}

func loopCueContainsAny(f string, phrases []string) bool {
	for _, p := range phrases {
		if strings.Contains(f, p) {
			return true
		}
	}
	return false
}

func loopCueCompareAsk(toks []string) bool {
	for _, t := range toks {
		if t == "vs" || t == "versus" {
			return true
		}
	}
	return tokenHasPrefix(toks, loopCueComparePrefixes...)
}

func loopCueCountAsk(f string, toks []string) bool {
	counting := strings.Contains(f, " how many ") || strings.Contains(f, " number of ")
	for _, t := range toks {
		if t == "kac" || t == "kactane" {
			counting = true
		}
	}
	return counting && tokenHasPrefix(toks, loopCueCountNouns...)
}

func loopCueRankAsk(f string, toks []string) bool {
	if !loopCueContainsAny(f, loopCueRankPhrases) {
		return false
	}
	for _, t := range toks {
		if loopCueRankServiceWords[t] {
			return true
		}
	}
	return tokenHasPrefix(toks, loopCueRankNouns...)
}

func loopCueWhyVolAsk(f string, toks []string) bool {
	why := false
	for _, t := range toks {
		if t == "neden" || t == "niye" || t == "nicin" || t == "why" {
			why = true
		}
	}
	return why && loopCueContainsAny(f, loopCueWhyVolPhrases)
}

// loopCueVetoesGuided — SAF: işaret ateşlediğinde turu alamayan guided rotası.
// Liste dar tutulur: yalnız ölçülen yanlış rotalar (filo problems listesi ve
// en yavaş trace listesi); diğer rotalar sorunun şekline zaten cevap verir.
func loopCueVetoesGuided(intent guidedIntent) bool {
	return intent == guidedProblems || intent == guidedSlowTraces
}

// loopCueGuidedRoute — SAF: router sonucu + işaret → işaret veto ediyorsa
// boş rota (guided turu almaz; kademe zinciri döngüye iner).
func loopCueGuidedRoute(raw string, route guidedRoute) guidedRoute {
	if loopCueVetoesGuided(route.Intent) && chatLoopCue(raw) != loopCueNone {
		return guidedRoute{}
	}
	return route
}

// intentNoneStaysGeneral — SAF: sınıflandırıcının eşlemediği soru araçsız
// genel bilgi cevabında mı kalır (copilot.IntentOnLoop kipi). Yalnız saf
// genel bilgi / serbest sohbet ("circuit breaker nedir", "merhaba"): servis /
// ortam adı, telemetri işareti, döngü işareti ya da altyapı sözcüğü taşıyan
// soru döngüye gider.
func intentNoneStaysGeneral(question string, services, envs []string) bool {
	if chatLoopCue(question) != loopCueNone || explicitTelemetryAsk(question) {
		return false
	}
	if !generalKnowledgeShape(question, services, envs) {
		return false
	}
	toks := guidedTokens(normalizeGuidedMsg(question))
	if hasPodSignal(toks) || hasDBSignal(toks) || hasMessagingSignal(toks) || hasDeploySignal(toks) {
		return false
	}
	// belirli an ("dün gece 02:00 civarı ne oldu") telemetri sorusudur
	if loopCueClockRe.MatchString(question) {
		return false
	}
	f := foldWords(question)
	return !loopCueContainsAny(f, loopCueMomentPhrases)
}

var (
	// loopCueClockRe — saat damgası (02:00, 14.30).
	loopCueClockRe = regexp.MustCompile(`\b([01]?\d|2[0-3])[:.][0-5]\d\b`)
	// loopCueMomentPhrases — göreli an (katlanmış, iki uçta boşluklu).
	loopCueMomentPhrases = []string{" dun ", " bugun ", " bu gece ", " dun gece ", " bu sabah ", " az once ",
		" yesterday ", " today ", " tonight ", " last night ", " this morning "}
)
