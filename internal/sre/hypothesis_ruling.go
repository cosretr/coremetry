package sre

// hypothesis_ruling.go — ekip kararı (insan hükmü) hipotez üzerinde.
//
// Karar = HumanState (confirmed "Doğrulandı" / dismissed "Reddedildi"),
// araştırmanın KENDİ kaydında (saved_views sre-investigation; yeni şema yok),
// editor+ yazar, audit sre.hypothesis.mark / sre.hypothesis.unmark.
//
//   - Skoru, bandı, otomatik durumu (State) ve SAKLI sırayı DEĞİŞTİRMEZ:
//     otomatik durum her koşuda yine hesaplanır ve kararın yanında görünür.
//   - Yeniden koşuda (elle / doğrulama) carryHuman taşır; karar ancak insan
//     kaldırınca gider.
//   - SONUÇ seçimi kararlara uyar (rulingOrder): doğrulanan sonuçtur,
//     reddedilen asla en olası neden olarak söylenmez — anlatım, sohbet
//     özeti, kanıt kapısı, bildirim özeti aynı sırayı kullanır.
//   - Tekrar (Kind=replay, değerlendirme) kararları GÖRMEZ:
//     canlı karar notlama cevabını sızdırırdı (orchestrator: replay'de
//     carryHuman yok; tekrar kendi anahtarında taze kayıttır).
//   - Oy / triyaj düğmeleri karar DEĞİLDİR (ayakta kural).

import "context"

// markClear — ApplyHypothesisMark: kararı kaldır (yalnız iç; API DELETE).
const markClear = "clear"

func ruling(h Hypothesis) string {
	if h.HumanState == nil {
		return ""
	}
	return h.HumanState.State
}

// rulingOrder — SAF: görünüm sırası; doğrulanan(lar) önce, kararsızlar skor
// sırasıyla, reddedilenler en sonda (kararlı sıralama; girdi değişmez).
func rulingOrder(hs []Hypothesis) []Hypothesis {
	out := make([]Hypothesis, 0, len(hs))
	for _, pass := range []string{MarkConfirmed, "", MarkDismissed} {
		for _, h := range hs {
			r := ruling(h)
			if r != MarkConfirmed && r != MarkDismissed {
				r = ""
			}
			if r == pass {
				out = append(out, h)
			}
		}
	}
	return out
}

// hasRulings — SAF: kayıtta en az bir ekip kararı var mı.
func hasRulings(hs []Hypothesis) bool {
	for _, h := range hs {
		if h.HumanState != nil {
			return true
		}
	}
	return false
}

// ClearHypothesisMark — ekip kararını kaldırır (DELETE ucu); otomatik durum
// yeniden tek başına geçerli olur. Kuyrukta / koşan kayıt ErrMarkBusy.
func ClearHypothesisMark(ctx context.Context, st QueueStore, problemID, kind string) (Hypothesis, error) {
	return ApplyHypothesisMark(ctx, st, problemID, MarkRequest{Kind: kind, State: markClear})
}
