package api

// chat_endpoint_delta.go — v0.10.1783 (operator-reported: endpoint ✨ Açıkla
// cevabı yanıt süresi artışını söylemiyordu). Ön-getirmenin ölçtüğü RED'den
// (opPrefetch.Current / Previous, spanmetrics_1m) DETERMİNİSTİK tek paragraf;
// operasyon kartının (blocks.TypeOperation) deltaText alanı olarak gider ve
// FE cevabın ÜSTÜNDE çizer — modelin sayıyı söyleyip söylemediğinden bağımsız.
// CH okuması yok: veri zaten ön-getirmede.

import (
	"fmt"
	"math"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// endpointDeltaUnchanged — mutlak göreli değişim bu oranın altındaysa "değişmedi".
const endpointDeltaUnchanged = 0.10

// endpointDeltaTR — SAF: "Son 1 saatte yanıt süresi p99 420 ms → 1,9 sn
// (+%350), hata oranı %0,2 → %4,8, trafik 1.200 → 1.180 istek (değişmedi);
// önceki eşit pencereye göre." Ölçülmediyse false. Önceki pencerede trafik
// yoksa kıyas yazılmaz ("önceki pencerede trafik yok"). p50 ölçüldüyse
// yanıt süresi "p50 … / p99 …" ikisini de taşır.
func endpointDeltaTR(cur, prev chstore.OpRED, rangeS int64) (string, bool) {
	if !cur.Measured {
		return "", false
	}
	loc := trRangeLocative(rangeS)
	head := strings.ToUpper(loc[:1]) + loc[1:]
	if !prev.Measured || prev.Calls == 0 {
		lat := "p99 " + endpointDurTR(cur.P99Ms)
		if cur.P50Ms > 0 {
			lat = "p50 " + endpointDurTR(cur.P50Ms) + " / " + lat
		}
		return fmt.Sprintf("%s yanıt süresi %s, hata oranı %s, trafik %s istek; önceki pencerede trafik yok.",
			head, lat, fmtTRErrRate(cur), fmtTRCount(cur.Calls)), true
	}
	lat := endpointDeltaPair("p99 ", cur.P99Ms, prev.P99Ms, endpointDurTR)
	if cur.P50Ms > 0 {
		lat = endpointDeltaPair("p50 ", cur.P50Ms, prev.P50Ms, endpointDurTR) + " / " + lat
	}
	errs := "hata oranı " + fmtTRErrRate(prev) + " → " + fmtTRErrRate(cur)
	if endpointErrUnchanged(cur.ErrorRate(), prev.ErrorRate()) {
		errs += " (değişmedi)"
	}
	calls := "trafik " + fmtTRCount(prev.Calls) + " → " + fmtTRCount(cur.Calls) + " istek"
	if tag := endpointChangeTag(float64(cur.Calls), float64(prev.Calls)); tag != "" {
		calls += " (" + tag + ")"
	}
	return fmt.Sprintf("%s yanıt süresi %s, %s, %s; önceki eşit pencereye göre.", head, lat, errs, calls), true
}

// endpointDeltaPair — SAF: "<lbl>önce → şimdi (+%N | değişmedi)".
func endpointDeltaPair(lbl string, cur, prev float64, f func(float64) string) string {
	s := lbl + f(prev) + " → " + f(cur)
	if tag := endpointChangeTag(cur, prev); tag != "" {
		s += " (" + tag + ")"
	}
	return s
}

// endpointChangeTag — SAF: göreli değişim; |Δ| < %10 → "değişmedi"; önceki 0
// ve şimdi > 0 → "" (oran tanımsız, iki sayı kendini söyler).
func endpointChangeTag(cur, prev float64) string {
	if prev <= 0 {
		if cur <= 0 {
			return "değişmedi"
		}
		return ""
	}
	rel := (cur - prev) / prev
	if math.Abs(rel) < endpointDeltaUnchanged {
		return "değişmedi"
	}
	sign := "+"
	if rel < 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s%%%.0f", sign, math.Abs(rel)*100)
}

// endpointErrUnchanged — SAF: hata oranı (0..1) değişmedi mi: göreli |Δ| < %10
// ya da gösterilen basamakta (%0,1) fark yok — %0,01 → %0,02 "+%100" değildir.
func endpointErrUnchanged(cur, prev float64) bool {
	if math.Abs(cur-prev)*100 < 0.05 {
		return true
	}
	return prev > 0 && math.Abs(cur-prev)/prev < endpointDeltaUnchanged
}

// endpointDurTR — SAF: ms → "420 ms" / "3,4 ms" / "1,9 sn".
func endpointDurTR(ms float64) string {
	if ms >= 1000 {
		return trNum(ms/1000) + " sn"
	}
	return fmtMsTR(ms)
}

// routeTurnRangeS — SAF (v0.10.1783): rota turunun pencere girdisi. Kartın
// İncele turu (skipPick) ve endpoint ✨ turu (görünür soru "… endpointine
// bak" ile biter; FE window = sayfanın aralığı) sayfanın penceresini taşır;
// diğer sorular 0 (planRouteTurn: sorudaki açık süre, yoksa son 1 saat).
func routeTurnRangeS(question string, ctxRangeS int64, skipPick bool) int64 {
	if ctxRangeS <= 0 {
		return 0
	}
	if skipPick || endpointChipShape(normalizeGuidedMsg(question)) {
		return ctxRangeS
	}
	return 0
}
