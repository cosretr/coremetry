package api

// chat_problems_root_cause.go — v0.10.2135 (CoSRE router boşlukları D2,
// docs/cosre/router-gaps-audit.md A1). Yalnız v2 (cosreV2Marked).
//
// Sohbetin statik takip çipi "Açık problemlerin kök nedeni?"
// (frontend drawerChat.ts FOLLOWUPS) prod'da 7 günde 6 kez router boşluğuna
// düştü; çip hipotez vaat ediyor ama hiçbir rota filo geneli hipotez
// vermiyordu. Operatör kararı 4 (2026-11-14): çip KALIR; rota = açık
// problemler + varsa kök neden hipotezi özeti.
//
// Rota yeni bir niyet değil: router servissiz problem sorusunu zaten
// `problems`'a götürüyor ve demet hipotezleri zaten okuyor
// (guidedProblemsRead → EnrichProblemsWithRootCause). Eklenen:
//   - neden sinyali taşıyan servissiz problem sorusu ProblemsRootCause
//     bayrağı alır (/ai rota etiketi problems_root_cause);
//   - açık problem yoksa cevap MODELSİZ ("kök neden hipotezi de yok");
//   - varsa kanıtın başına hipotez sayımı ve "uydurma" kuralı, cevabın
//     sonuna sunucunun sayım cümlesi (model düşüremez): hipotez yoksa
//     "Kök neden hipotezi yok".
// v1 yolunda bayrak hiç kurulmaz → bayt bayt eski.

import (
	"context"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// guidedProblemsRootCauseRoute — /ai etkileşim satırındaki alt rota adı.
const guidedProblemsRootCauseRoute = "problems_root_cause"

// promoteProblemsRootCause — SAF: v2'de servissiz problem rotası, mesaj hem
// problem hem neden sinyali taşıyorsa hipotez çerçevesine yükselir. Servisli
// soru (ekran servisi dahil) root_cause'ta kalır; "açık problemler neler?"
// (neden yok) ve "neden hata alıyoruz" (problem sözcüğü yok) değişmez.
func promoteProblemsRootCause(r guidedRoute, norm string, v2 bool) guidedRoute {
	if !v2 || r.Intent != guidedProblems || r.Service != "" {
		return r
	}
	toks := guidedTokens(norm)
	if hasProblemSignal(toks) && hasWhySignal(toks) {
		r.ProblemsRootCause = true
	}
	return r
}

// guidedRouteLabel — SAF: /ai etkileşim satırının rota adı.
func guidedRouteLabel(r guidedRoute) string {
	if r.Intent == guidedProblems && r.ProblemsRootCause {
		return guidedProblemsRootCauseRoute
	}
	if r.Intent == guidedRootCause && r.opWhy != nil { // v0.10.2137 (chat_operation_why.go)
		return guidedOperationRootCauseRoute
	}
	return string(r.Intent)
}

// problemsNoneRootCauseTR — açık problem yokken modelsiz cevap.
func problemsNoneRootCauseTR(env string) string {
	scope := ""
	if env != "" {
		scope = " (ortam: " + env + ")"
	}
	return "Şu an açık problem yok" + scope + "; dolayısıyla kök neden hipotezi de yok."
}

// problemsRootCauseFrame — SAF: listelenen (≤50) açık problemlerden kanıt
// başlığı ve sunucu soneki. Hipotez = RootCause dolu satır.
func problemsRootCauseFrame(probs []chstore.Problem) (header, tail string) {
	n, k := len(probs), 0
	for i := range probs {
		if probs[i].RootCause != nil {
			k++
		}
	}
	if k == 0 {
		header = fmt.Sprintf("SORU: açık problemlerin kök nedeni. Listelenen %d açık problemin HİÇBİRİNDE kayıtlı kök-neden hipotezi YOK. "+
			"Problemleri öncelik sırasıyla kısaca anlat; kök neden için sebep UYDURMA, hipotez olmadığını açıkça söyle.\n\n", n)
		tail = fmt.Sprintf("\n\nKök neden hipotezi yok: listelenen %d açık problemin hiçbiri için kayıtlı hipotez bulunmuyor.", n)
		return header, tail
	}
	header = fmt.Sprintf("SORU: açık problemlerin kök nedeni. Listelenen %d açık problemin %d tanesinde kök-neden hipotezi var. "+
		"Önce hipotezi olan problemleri hipotez özetiyle (şüpheli + güven) anlat; hipotezi olmayanlar için 'kök neden hipotezi yok' de, sebep UYDURMA.\n\n", n, k)
	if k < n {
		tail = fmt.Sprintf("\n\nKök neden hipotezi: listelenen %d açık problemin %d tanesinde var, %d tanesinde yok.", n, k, n-k)
	}
	return header, tail
}

// guidedProblemsRootCauseBundle — problems demeti (aynı okuma, aynı adımlar)
// + hipotez çerçevesi. Açık problem yoksa route.DirectAnswer (modelsiz).
func (s *Server) guidedProblemsRootCauseBundle(ctx context.Context, emit func(string, any), route *guidedRoute, asOf time.Time) (string, string, guidedSummaryFacts, error) {
	evidence, src, facts, probs, err := s.guidedProblemsRead(ctx, emit, route.Service, route.Env, asOf)
	if err != nil {
		return "", "", facts, err
	}
	if len(probs) == 0 {
		route.DirectAnswer = problemsNoneRootCauseTR(route.Env)
		return evidence, src, facts, nil
	}
	header, tail := problemsRootCauseFrame(probs)
	route.AnswerTail += tail
	return header + evidence, src, facts, nil
}
