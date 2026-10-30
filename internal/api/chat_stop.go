package api

// chat_stop.go — CoSRE agent-core S1 (docs/cosre/agent-core-ideas.md §2):
// serbest araç döngüsünün DURMA KARARI adlandırılmış, birleşebilir
// koşullara iner (AI SDK `stopWhen` fikri, Go'da).
//
// Eskiden döngü iki örtük dal taşıyordu: `cancelled || ctx.Err() != nil`
// ve `round == maxRounds-1 || callsLeft <= 0`. Artık tek değerlendirme:
// `loopStop(loopState)` bir NEDEN döner; boş neden = devam. Koşullar yalnız
// DURDURUR — hiçbir koşul tavanı büyütemez, devam ettiremez (tip düzeyinde:
// stopAny ilk boş-olmayan nedeni döner, "devam" diye bir değer yok).
//
// S1 tel/etiket DEĞİŞTİRMEZ: neden döngüde yerel değişkendir.

import "github.com/cilcenk/coremetry/internal/copilot"

// stopReason — döngünün neden durduğu. "" = durma.
type stopReason string

const (
	stopNone        stopReason = ""
	stopRoundCap    stopReason = "round_cap"
	stopCallCap     stopReason = "call_cap"
	stopCancelled   stopReason = "cancelled"
	stopTokenBudget stopReason = "token_budget"
	// stopToolCalledPrefix — `tool_called:<ad>` (stopOnToolCall).
	stopToolCalledPrefix = "tool_called:"
)

// loopState — tur sonunda (araç sonuçları toplandıktan sonra) döngünün
// gözlemlenen durumu. Round 0 tabanlı (döngü sayacı).
type loopState struct {
	Round, MaxRounds    int
	CallsLeft, MaxCalls int
	Executed, Skipped   int
	TokensIn, TokensOut uint32
	// LastCalls — bu turda modelin çağırdığı araç adları (sırayla).
	LastCalls []string
	// Cancelled — executor bu turda bir çağrıyı KindCancelled ile reddetti.
	Cancelled bool
	// CtxErr — alışveriş ctx'inin hatası (istemci iptali / alışveriş tavanı).
	CtxErr error
	// ApprovalPending — agent-core S4: bu turda bir çağrı operatör onayı
	// bekliyor (Executor KindApproval). Bugün hiçbir araç onay istemez.
	ApprovalPending bool
}

// stopCond — saf koşul: durma nedeni ya da "".
type stopCond func(loopState) stopReason

// stopAny — koşulları SIRAYLA dener, ilk boş-olmayan neden kazanır (AI SDK
// `stopWhen` dizisi = OR; sıra hangi nedenin raporlandığını belirler).
func stopAny(conds ...stopCond) stopCond {
	return func(s loopState) stopReason {
		for _, c := range conds {
			if c == nil {
				continue
			}
			if r := c(s); r != stopNone {
				return r
			}
		}
		return stopNone
	}
}

// stopOnCtxDone — tur ortasında iptal ya da alışveriş ctx'i bitti.
func stopOnCtxDone() stopCond {
	return func(s loopState) stopReason {
		if s.Cancelled || s.CtxErr != nil {
			return stopCancelled
		}
		return stopNone
	}
}

// stopAtRound — n turluk tavan: son tur (Round == n-1) durur. Döngü
// `round < maxRounds` ile döndüğünden Round n-1'i aşmaz.
func stopAtRound(n int) stopCond {
	return func(s loopState) stopReason {
		if s.Round == n-1 {
			return stopRoundCap
		}
		return stopNone
	}
}

// stopWhenCallsExhausted — çağrı hakkı bitti.
func stopWhenCallsExhausted() stopCond {
	return func(s loopState) stopReason {
		if s.CallsLeft <= 0 {
			return stopCallCap
		}
		return stopNone
	}
}

// stopOnToolCall — bu turda adı geçen araç çağrıldıysa dur (AI SDK
// `hasToolCall`). Varsayılan zincirde YOK; ileriki planlar için.
func stopOnToolCall(name string) stopCond {
	return func(s loopState) stopReason {
		for _, c := range s.LastCalls {
			if c == name {
				return stopReason(stopToolCalledPrefix + name)
			}
		}
		return stopNone
	}
}

// stopAtTokens — giriş+çıkış token toplamı n'e ulaştı. n == 0 kapalı.
// Varsayılan zincirde YOK.
func stopAtTokens(n uint64) stopCond {
	return func(s loopState) stopReason {
		if n > 0 && uint64(s.TokensIn)+uint64(s.TokensOut) >= n {
			return stopTokenBudget
		}
		return stopNone
	}
}

// defaultStop — bugünkü döngünün kararı, aynı dal sırasıyla: önce iptal
// (model yeniden çağrılmaz), sonra tur tavanı, sonra çağrı tavanı (ikisi
// birlikteyse tur tavanı raporlanır — eski `||` zincirinin sırası).
func defaultStop(maxRounds int) stopCond {
	// agent-core S4 — onay bekleyen tur iptalden sonra, tavanlardan ÖNCE:
	// model yeniden çağrılmaz (tavan turu da yok); ApprovalPending bugün hep false.
	return stopAny(stopOnCtxDone(), stopOnApprovalPending(), stopAtRound(maxRounds), stopWhenCallsExhausted())
}

// isCapStop — neden "tavan turuna geç" mi (elindekiyle cevapla turu). İptal
// ve "" değil; onay bekleme de değil (S4: model yeniden çağrılmaz); diğer
// her neden tavan turu ister.
func isCapStop(r stopReason) bool {
	return r != stopNone && r != stopCancelled && r != stopApprovalPending
}

// toolCallNames — turun çağrı adları (loopState.LastCalls); boş tur nil.
func toolCallNames(calls []copilot.ToolCall) []string {
	if len(calls) == 0 {
		return nil
	}
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Name
	}
	return out
}
