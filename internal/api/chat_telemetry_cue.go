package api

// chat_telemetry_cue.go — v0.10.2465 (operator-reported, prod 2026-11-22:
// "Son 1 saat içerisinde url'inde ocp5 içeren bir servis çağrımı veya trace'i
// var mı?" → kaynak sorusu "Bu soru yüklü envanter tablosunda 200 satırla
// eşleşiyor (ocp5). Bunu nerede arayayım: telemetride…, wikide, envanterde…").
//
// Kök neden: doküman kademeleri (chatDocOrEntityAskTier → keşif → satır)
// guided'dan ÖNCE koşar ve yüklü tabloda eşleşen her soru kaynak sorusuna
// düşer; telemetriyi bu kademelerden ÖNCE zorlayan tek kapı servis sağlığı
// sorusuydu (isServiceHealthAsk, v0.10.1942) ve kullanıcının "telemetride"
// demesi (applyNamedSource). Soru telemetriyi açıkça adlandırsa da (trace,
// çağrı, url'inde, son 1 saat) envanterdeki "ocp5" hücreleri kazanıyordu.
//
// Kural: explicitTelemetryAsk — SAF, modelden bağımsız. Soru telemetri
// nesnesini adlandırıyorsa (trace / span / trace id, çağrı / call, gecikme /
// hata oranı / p95, "son N dakika|saat|gün", URL / yol biçimi) ya da URL /
// endpoint / rota sözcüğünü bir ikinci işaretle birlikte taşıyorsa ("url'inde
// X içeren") telemetri zorlanır: doküman kademeleri atlanır, soru lookup
// router'a (chat_lookup_route.go: endpoint_lookup url=parça, range_s=pencere)
// iner. Kaynağı adıyla anan soru (envanter / excel / wiki / tablo / doküman)
// ve nasıl-yapılır / "X nedir" biçimli genel soru bu kapıdan GEÇMEZ; çıplak
// envanter terimi ("ocp5 hangi ekipte") eskisi gibi kaynak sorusunu alır.

import (
	"regexp"
	"strings"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

var (
	// telemetryStrongStems — tek başına telemetri sorusu: trace / span, çağrı,
	// gecikme ve hata oranı sözcükleri (katlanmamış, normalizeGuidedMsg).
	telemetryStrongStems = []string{"trace", "span", "çağrı", "çağrım", "call", "latency", "gecikme", "p95", "p99",
		"throughput", "istek-sayı", "hata-oran", "error-rate"}
	// telemetryWeakStems — URL / endpoint / rota sözcüğü: envanterde de sütun
	// olabilir, o yüzden ikinci bir işaretle birlikte telemetridir.
	telemetryWeakStems = []string{"url", "endpoint", "route", "rota", "path"}
	// telemetryContainsWords — "X içeren / X geçen / containing X" parça araması.
	telemetryContainsWords = []string{"içeren", "içerir", "içeriyor", "içersin", "geçen", "containing", "contains", "including"}
	// telemetryURLPathRe — "/api/v1/orders" biçimli yol (en az iki parça).
	telemetryURLPathRe = regexp.MustCompile(`(^|\s)/[A-Za-z0-9._~-]+/[A-Za-z0-9._~/{}-]*`)
	// telemetryWindowRe — "son 1 saat", "last 30 min", "1 saattir", "15 dk içinde".
	telemetryWindowRe = regexp.MustCompile(`(son|last|past|geçen)\s+\d+\s*(gün|gun|days?|saat|hours?|hrs?|dakika|dk|minutes?|mins?)|\d+\s*(saat|dakika|dk)\s*(tir|tır|dir|dır|içinde|içerisinde|boyunca)`)
)

// telemetryDocCueStems — kaynağı adıyla anan soru telemetriye zorlanmaz
// (katlanmış önekler). "tablo" BİLEREK yok (namedSourceKind ile aynı):
// veritabanı tablosu telemetri sorusudur.
var telemetryDocCueStems = []string{"envanter", "excel", "dokuman", "wiki", "belge", "runbook", "dosya", "csv"}

// explicitTelemetryAsk — SAF: soru telemetriyi açıkça soruyor mu.
func explicitTelemetryAsk(q string) bool {
	q = strings.TrimSpace(q)
	if q == "" || namedSourceKind(q) != "" {
		return false
	}
	norm := normalizeGuidedMsg(q)
	toks := guidedTokens(lookupJoinPhrases(norm))
	if len(toks) == 0 || hasHowToShape(toks) {
		return false
	}
	for _, t := range strings.Fields(foldWords(norm)) {
		for _, st := range telemetryDocCueStems {
			if strings.HasPrefix(t, st) {
				return false
			}
		}
	}
	clues := mcptools.ExtractLogClues(q)
	literal := len(clues.TraceIDs) > 0 || len(clues.URLs) > 0 || telemetryURLPathRe.MatchString(q)
	window := telemetryWindowRe.MatchString(norm)
	if literal || window {
		return true
	}
	// "trace nedir" / "span ne demek" genel bilgi sorusudur.
	if telemetryDefinitionShape(norm) {
		return false
	}
	if tokenHasPrefix(toks, telemetryStrongStems...) {
		return true
	}
	return tokenHasPrefix(toks, telemetryWeakStems...) && lookupURLFragment(toks) != ""
}

// telemetryDefinitionShape — SAF: "X nedir / ne demek / what is" tanım sorusu.
func telemetryDefinitionShape(norm string) bool {
	for _, p := range []string{"nedir", "ne demek", "ne anlama", "ne işe yarar", "what is", "what are", "explain"} {
		if strings.Contains(norm, p) {
			return true
		}
	}
	// "trace nasıl eklenir / açılır / kurulur", "how to enable tracing".
	toks := guidedTokens(norm)
	return tokenHasPrefix(toks, "nasıl", "how") &&
		tokenHasPrefix(toks, "ekle", "eklen", "kur", "yapılandır", "açıl", "etkinleştir", "enable", "configure", "instrument", "add")
}

// lookupURLFragmentQ — SAF: içerme kalıbının parçası (lookupURLFragment)
// yol biçimli bir değerin son parçasıysa ("url'i /v1/orders içeren" →
// belirteçler "/"'ı böler, parça "orders") tam yol döner; içerme kalıbı yoksa
// çıplak yol parça SAYILMAZ ("/api/x endpoint'i neden yavaş" guided'da kalır).
func lookupURLFragmentQ(q, frag string) string {
	if frag == "" {
		return ""
	}
	for _, m := range telemetryURLPathRe.FindAllString(q, -1) {
		m = strings.TrimRight(strings.TrimSpace(m), ".,;:!?'’")
		if strings.HasSuffix(strings.ToLower(m), frag) {
			return m
		}
	}
	return frag
}

// lookupJoinPhrases — SAF: iki sözcüklü telemetri ifadelerini tek belirtece
// birleştirir ("hata oranı" → "hata-oranı", "servis çağrısı" korunur).
func lookupJoinPhrases(norm string) string {
	for _, p := range [][2]string{{"hata oran", "hata-oran"}, {"error rate", "error-rate"}, {"istek sayı", "istek-sayı"}} {
		norm = strings.ReplaceAll(norm, p[0], p[1])
	}
	return norm
}

// lookupURLFragment — SAF: "url'inde X içeren", "X geçen endpoint", "X içeren
// trace" kalıbındaki X (içerme fiilinden hemen önceki belirteç). "geçen
// hafta/ay" pencere ifadesidir, parça değil. Sayı, soru sözcüğü ve telemetri
// sözcüğünün kendisi parça sayılmaz.
func lookupURLFragment(toks []string) string {
	for i, t := range toks {
		if i == 0 || !hasExact([]string{t}, telemetryContainsWords...) {
			continue
		}
		if t == "geçen" && i+1 < len(toks) && tokenHasPrefix(toks[i+1:i+2], "hafta", "ay", "gün", "yıl", "sefer", "süre", "sürel") {
			continue
		}
		cand := strings.Trim(toks[i-1], ".-_")
		if len(cand) < 2 || isAllDigits(cand) || lookupFragmentStop[cand] || tokenHasPrefix([]string{cand}, telemetryWeakStems...) ||
			tokenHasPrefix([]string{cand}, telemetryStrongStems...) {
			continue
		}
		return cand
	}
	return ""
}

// lookupFragmentStop — parça olamayan ek / bağlaç belirteçleri ("url'inde" →
// "url", "inde").
var lookupFragmentStop = map[string]bool{
	"inde": true, "ında": true, "unda": true, "ünde": true, "de": true, "da": true, "te": true, "ta": true,
	"in": true, "ın": true, "un": true, "ün": true, "i": true, "ı": true, "bir": true, "ve": true, "veya": true,
	"ya": true, "ile": true, "mi": true, "mı": true, "bu": true, "şu": true, "the": true, "a": true, "an": true,
}

// lookupLocValue — SAF (v0.10.2465): "X'te / X'de / X cluster'ında" kalıbındaki
// X — bulunma ekinden (apostrofla ayrılmış belirteç) hemen önceki belirteç.
func lookupLocValue(toks []string) string {
	for i := 0; i+1 < len(toks); i++ {
		if !lookupLocSuffix[toks[i+1]] {
			continue
		}
		cand := strings.Trim(toks[i], ".-_")
		if lookupDimNouns[cand] && i > 0 { // "c1 cluster'ında" → c1
			cand = strings.Trim(toks[i-1], ".-_")
		}
		if len(cand) < 2 || isAllDigits(cand) || lookupFragmentStop[cand] || lookupDimNouns[cand] || tokenHasPrefix([]string{cand}, telemetryWeakStems...) ||
			tokenHasPrefix([]string{cand}, telemetryStrongStems...) || tokenHasPrefix([]string{cand}, "son", "saat", "dakika", "gün", "servis", "pod") {
			continue
		}
		return cand
	}
	return ""
}

// lookupLocSuffix — bulunma ekleri ("c1'de", "ocp'te", "cluster'ında").
var lookupLocSuffix = map[string]bool{"de": true, "da": true, "te": true, "ta": true, "inde": true, "ında": true, "unda": true, "ünde": true,
	"nde": true, "nda": true, "deki": true, "daki": true, "teki": true, "taki": true}

// lookupDimNouns — boyut adları: ekin taşıyıcısıysa değer bir önceki belirteç.
var lookupDimNouns = map[string]bool{"cluster": true, "küme": true, "ortam": true, "env": true, "environment": true, "namespace": true, "ns": true}
