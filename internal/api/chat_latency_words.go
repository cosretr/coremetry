package api

// chat_latency_words.go — v0.10.2136 (CoSRE router boşlukları D3,
// docs/cosre/router-gaps-audit.md E). YALNIZ v2 (cosreV2Marked); v1 yolu
// bu dosyanın hiçbir yardımcısını çağırmaz → bayt bayt eski.
//
// Prod (7 gün): yavaşlama "neden" soruları 10 kez router boşluğuna düştü.
// Kök sözcükler hiçbir sinyal listesinde yoktu: "uzun süren", "uzuyor",
// "uzama", "süre artışı", "gecikiyor" ve "hafta". Çözüm tek liste
// (hasLatencyGrowthSignal) ve onu okuyan iki yer:
//   1. v2RouterQuestion — router'a giden metne mevcut sözlüğün KANONİK
//      karşılığı EKLENİR (metin silinmez/değişmez): "uzun sür*" → "en uzun"
//      (yavaş trace listesi), diğer büyüme sözcükleri → "yavaşlama" (sağlık
//      alanı + gecikme sorusu). Router'ın kendisi değişmez; anlatıma giden
//      soru operatörün yazdığı metindir.
//   2. runGuidedRoute'ta kök-neden demetinin gecikme sorusu (slowAsk).
// Ayrıca: karar 5 (why > report: "nedenini inceler misin" kök nedene),
// karar 3 ("hafta" = 7 gün yalnız servis sağlığı / kök neden RED'inde; MV
// okumaları, yavaş trace kırılımı 24 sa'da kalır).

import "strings"

// latencyGrowthStems — tek başına gecikme büyümesi anlatan jeton önekleri.
// "uza" DEĞİL: "uzak" (uzak sunucu) yakalanırdı; "uzay" da dışarıda.
var latencyGrowthStems = []string{"uzam", "uzuyor", "uzad", "gecik"}

// longRunVerbStems — "uzun" ardından gelen sür- fiil biçimleri. "sürü" DEĞİL:
// "sürüm" (deploy) yakalanırdı; "sürüyo" sürüyor/sürüyormuş'u kapsar.
var longRunVerbStems = []string{"süren", "sürüyo", "sürd", "sürmü", "sürmek", "sürer"}

// hasLongRunPhrase — SAF: "uzun" + sür- fiili yan yana ("uzun süren",
// "uzun sürüyor", "uzun sürdü").
func hasLongRunPhrase(toks []string) bool {
	for i := 0; i+1 < len(toks); i++ {
		if toks[i] == "uzun" && tokenHasPrefix(toks[i+1:i+2], longRunVerbStems...) {
			return true
		}
	}
	return false
}

// hasDurationRisePhrase — SAF: "süre"/"süresi"/"süreleri" + art- ("süre
// artışı", "süreleri arttı", "sürelerinde artış").
func hasDurationRisePhrase(toks []string) bool {
	for i := 0; i+1 < len(toks); i++ {
		if strings.HasPrefix(toks[i], "süre") && !strings.HasPrefix(toks[i], "süren") && strings.HasPrefix(toks[i+1], "art") {
			return true
		}
	}
	return false
}

// hasLatencyGrowthSignal — SAF, TEK liste: soru gecikmenin büyümesini
// anlatıyor mu. norm = normalizeGuidedMsg çıktısı.
func hasLatencyGrowthSignal(norm string) bool {
	toks := guidedTokens(norm)
	return tokenHasPrefix(toks, latencyGrowthStems...) || hasLongRunPhrase(toks) || hasDurationRisePhrase(toks)
}

// v2RouterQuestion — SAF: v2'de router'a giden metin. Gecikme büyümesi
// sözcüğü taşıyan soruya mevcut sözlüğün kanonik karşılığı eklenir; v1'de
// ya da sözcük yoksa metin AYNEN döner.
func v2RouterQuestion(question string, v2 bool) string {
	if !v2 {
		return question
	}
	norm := normalizeGuidedMsg(question)
	toks := guidedTokens(norm)
	var add []string
	if hasLongRunPhrase(toks) {
		add = append(add, "en uzun")
	}
	if tokenHasPrefix(toks, latencyGrowthStems...) || hasDurationRisePhrase(toks) || hasLongRunPhrase(toks) {
		add = append(add, "yavaşlama")
	}
	if len(add) == 0 {
		return question
	}
	return question + " " + strings.Join(add, " ")
}

// v2WhyOverReport — SAF, karar 5: v2'de neden sinyali taşıyan servis raporu
// isteği kök nedene gider ("svc-a nedenini inceler misin"). Servissiz
// netleştirmede (ask_service) sorulacak niyet de kök neden olur.
func v2WhyOverReport(r guidedRoute, norm string, v2 bool) guidedRoute {
	if !v2 || !hasWhySignal(guidedTokens(norm)) {
		return r
	}
	switch {
	case r.Intent == guidedServiceReport:
		r.Intent = guidedRootCause
	case r.Intent == guidedAskService && r.AskIntent == guidedServiceReport:
		r.AskIntent = guidedRootCause
	}
	return r
}

// guidedWeekRangeS — 7 gün (karar 3: haftalık soru tavanı; "2 hafta" da 7 gün).
const guidedWeekRangeS = 7 * 86400

// guidedDrillMaxRangeS — karar 3: yavaş trace kırılımı (ham span) 24 sa'da kalır.
const guidedDrillMaxRangeS = 86400

// v2WeekRange — SAF: v2'de "hafta/week" soran servis sağlığı / kök neden
// sorusunun penceresi. Açık gün/saat/dakika penceresi varsa (explicit) ya da
// soru belirli bir haftayı anıyorsa ("geçen hafta", "önceki hafta" — 7 günlük
// geriye bakış değil) dokunulmaz.
func v2WeekRange(r guidedRoute, norm string, explicit, v2 bool) (int64, bool) {
	if !v2 || explicit || (r.Intent != guidedServiceHealth && r.Intent != guidedRootCause) {
		return 0, false
	}
	if !hasWeekLookback(guidedTokens(norm)) { // v0.10.2137 — tek gövde (chat_operation_why.go)
		return 0, false
	}
	return guidedWeekRangeS, true
}

// drillWindowS — SAF: kök-neden demetinin yavaş trace kırılımı penceresi.
// v2'de 24 sa'yı aşmaz (karar 3); v1 aynen.
func drillWindowS(rangeS int64, v2 bool) int64 {
	if v2 && rangeS > guidedDrillMaxRangeS {
		return guidedDrillMaxRangeS
	}
	return rangeS
}
