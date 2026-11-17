package api

import "github.com/cilcenk/coremetry/internal/copilot"

// chat_loop_usage.go — v0.10.2297: serbest döngünün model gidiş-dönüşleri.
// totalIn'e eklenen HER tur (ana tur, bağlam taşması yeniden denemesi,
// tavan turu, bozuk-cevap yeniden denemesi) burada da sayılır; satır
// copilot.WithLoopUsage ile tur sayısını ve en büyük tek turu taşır.

// loopUsage — SAF birikimci (sıfır değer kullanılabilir).
type loopUsage struct {
	rounds uint16
	peakIn uint32
	// v0.10.2318 — modelin istediği araç çağrısı (toplam + tek turda en çok)
	// ve cevap turunun ilk token'ı.
	toolCalls     uint16
	maxRoundCalls uint16
	answerTTFT    uint32
}

// addCalls — bir normal turda modelin döndürdüğü araç çağrısı sayısı
// (yürütülsün ya da bütçe/tekrar/bilinmeyen yüzünden atlansın).
func (u *loopUsage) addCalls(n int) {
	if n <= 0 {
		return
	}
	n16 := uint16(min(n, int(^uint16(0))))
	if u.toolCalls > ^uint16(0)-n16 {
		u.toolCalls = ^uint16(0)
	} else {
		u.toolCalls += n16
	}
	u.maxRoundCalls = max(u.maxRoundCalls, n16)
}

// answer — cevabı veren turun ilk token süresi (sonraki çağrı öncekini ezer:
// bozuk-cevap yeniden denemesi cevabı verirse onun TTFT'si).
func (u *loopUsage) answer(ttftMs uint32) { u.answerTTFT = ttftMs }

// add — bir model gidiş-dönüşü (hatalı tur da bir gidiş-dönüştür).
func (u *loopUsage) add(inTok uint32) {
	if u.rounds < ^uint16(0) {
		u.rounds++
	}
	if inTok > u.peakIn {
		u.peakIn = inTok
	}
}

// usage — copilot.WithLoopUsage girdisi (süreler chatSpan.withLoopTimes ile).
func (u loopUsage) usage() copilot.LoopUsage {
	return copilot.LoopUsage{Rounds: u.rounds, PeakInputTokens: u.peakIn,
		AnswerTTFTMs: u.answerTTFT, ToolCalls: u.toolCalls, MaxRoundToolCalls: u.maxRoundCalls}
}
