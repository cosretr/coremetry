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
}

// add — bir model gidiş-dönüşü (hatalı tur da bir gidiş-dönüştür).
func (u *loopUsage) add(inTok uint32) {
	if u.rounds < ^uint16(0) {
		u.rounds++
	}
	if inTok > u.peakIn {
		u.peakIn = inTok
	}
}

// usage — copilot.WithLoopUsage girdisi.
func (u loopUsage) usage() copilot.LoopUsage {
	return copilot.LoopUsage{Rounds: u.rounds, PeakInputTokens: u.peakIn}
}
