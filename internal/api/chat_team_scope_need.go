package api

// chat_team_scope_need.go — v0.10.1723 (operatör: CoSRE v2'de composer
// bağlamı trace + servis + env + pencere taşırken kullanıcının takımı
// bilinmiyordu (data-scope no_team); model "Hangi takımdasınız?" diye
// durup 183 takımdan 30'unu metin olarak sıraladı. Trace / servis
// açıklaması takım kapsamı istemez).
//
// Kök neden: kapsamsız v2 alışverişinde userScopeAddendum her zaman
// "bir kez Hangi takım? diye sor" ekini (ChatUserScopeAskTeamAddendum)
// döngüye koyuyordu; ekin "belirli servis sorusunda sormadan devam et"
// cümlesi küçük modele yetmiyordu. Karar artık SAF needsTeamScope'ta:
// açık çapa (trace, servis, açıklama) varsa ve soru takım biçimli değilse
// takım istenmez, ek "çapadan cevapla, takım sorma" olur. data-scope
// olayı değişmez (başlık rozeti yine "Kapsamsız").

import (
	"strings"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// teamScopeCtx — isteğin çapa alanları (needsTeamScope girdisi).
type teamScopeCtx struct {
	Trace      string // context.trace / page.traceId / pinnedPage.traceId
	Service    string // context.service / page.service / pinnedPage.service
	Explain    bool   // context.explain / subject / explainAsk dolu
	Problem    string // page.problemId / page.exceptionId (Explain bağlamı)
	BriefingID string // brifing "Sor" satırı — takım biçimli
}

// teamShapedMarkers — katlanmış (Türkçe küçük harf) soru metninde takım
// kapsamı isteyen ifadeler. Ön ek eşleşmesi: "ekibim" → ekibimde, ekibimin.
var teamShapedMarkers = []string{
	"servislerim", "servisim", "ekibim", "takımım", "benim servis", "bana ait",
	"sahibi olduğum", "sahip olduğum", "my service", "my team", "our service", "our team",
	"brifing", "briefing",
}

var trFold = strings.NewReplacer("İ", "i", "I", "ı")

// teamShapedQuestion — SAF: soru kullanıcının KENDİ takımını/servislerini mi soruyor.
func teamShapedQuestion(q string) bool {
	f := strings.ToLower(trFold.Replace(q))
	for _, m := range teamShapedMarkers {
		if strings.Contains(f, m) {
			return true
		}
	}
	return false
}

// needsTeamScope — SAF: bu soru takım kapsamı gerektiriyor mu. Takım
// biçimli soru ya da brifing satırı → evet (bugünkü davranış). Açık çapa
// (trace, servis, açıklama/problem) → hayır, döngü çapadan cevaplar.
// Çapasız düz soru → evet (bugünkü davranış).
func needsTeamScope(ctx teamScopeCtx, question string) bool {
	if strings.TrimSpace(ctx.BriefingID) != "" || teamShapedQuestion(question) {
		return true
	}
	anchored := ctx.Explain ||
		strings.TrimSpace(ctx.Trace) != "" ||
		strings.TrimSpace(ctx.Service) != "" ||
		strings.TrimSpace(ctx.Problem) != ""
	return !anchored
}

// teamScopeCtxFrom — isteğin bağlam alanlarından çapa girdisi (okuma yok).
func teamScopeCtxFrom(trace, service string, explain bool, briefingID string, pages ...*agentctx.PageContext) teamScopeCtx {
	c := teamScopeCtx{Trace: trace, Service: service, Explain: explain, BriefingID: briefingID}
	for _, p := range pages {
		if p == nil {
			continue
		}
		if c.Trace == "" {
			c.Trace = p.TraceID
		}
		if c.Service == "" {
			c.Service = p.Service
		}
		if c.Problem == "" {
			c.Problem = p.ProblemID
		}
		if c.Problem == "" {
			c.Problem = p.ExceptionID
		}
	}
	return c
}

// scopeAddendumFor — SAF: v2 döngüsünün kapsam eki. Kapsamsız alışverişte
// takım gerekmiyorsa (çapalı soru) "takım sorma" eki; aksi halde
// userScopeAddendum aynen (kapsamlı / kaçış / kapalı / sorulmuş dalları).
func scopeAddendumFor(u userScope, msgs []copilot.ChatMessage, need bool) string {
	base := userScopeAddendum(u, msgs)
	if !u.Unscoped || base == "" || need {
		return base
	}
	return copilot.ChatUserScopeAnchoredAddendum()
}
