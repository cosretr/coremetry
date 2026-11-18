package api

// chat_scope_decision.go — v0.10.2355 (docs/cosre/investigation-design.md S1,
// karar 10; sre-evidence-audit §5.5 ve karar 7: kapsam VARSAYILAN DARALTMADIR,
// yetki değil).
//
// TEK karar, TEK dikiş: copilot_chat.go'da araç argümanı kesinleştikten
// (applyScreenContext + applyUserScope) hemen sonra, yürütmeden önce
// (v2LoopPlan.scopeCheck). Karar SAF (decideScope):
//
//   - cosre_conversation.evidence = off → değerlendirilmez (bugünkü davranış).
//   - kullanıcı kapsamsız (takım yok, kaçış @/komut, anahtar kapalı) → değerlendirilmez.
//   - trace takibi → değerlendirilmez (trace sohbet kuralı, karar 20).
//   - araç kapsam taşımıyor (mcptools.V2ScopeClass muaf ya da sınıfsız) → değerlendirilmez.
//   - argümandaki her `service` / `services` değeri izinli kümedeyse geçer:
//     kullanıcının kapsam servisleri ∪ bu turda kullanıcının TAM adla andığı
//     servisler (varlık çözücüsü, explicitStateServices) ∪ ekranın servisi ∪
//     konuşma durumunun servis(ler)i. Kapsam enjeksiyonunun koyduğu servisler
//     zaten kapsamdadır.
//   - değilse (modelin ÇIKARDIĞI kapsam dışı servis): shadow → çağrı koşar,
//     çağrı başına tek `scope_would_refuse` sistem sinyali (ayrıntı tool +
//     servisler; değerlendirme koşusunda yazılmaz); on → araç koşmaz, sonuç
//     mcp.ToolError biçiminde `out_of_scope` (adım çipi skipped, neden scope).
//
// İşleyicilere dokunulmaz; sınıflandırma mcptools/scope_ctx.go.

import (
	"context"
	"encoding/json"
	"strings"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// outOfScopeError — modele giden sınıf (mcp.ToolError.Error).
const outOfScopeError = "out_of_scope"

// outOfScopeHint — modele ne yapacağı (Türkçe, emir kipi; toolerr.go doktrini).
const outOfScopeHint = "bu servis kullanıcının kapsamında değil ve kullanıcı onu anmadı — tahmin ettiğin servisi sorgulama; " +
	"kapsamdaki servislerle devam et ya da kullanıcıya hangi servisi kastettiğini sor"

// scopeDecisionIn — kararın girdileri (okumalar çağıranda).
type scopeDecisionIn struct {
	Mode    string          // cosre_conversation.evidence
	Tool    string          // araç adı
	Args    json.RawMessage // kesinleşmiş argüman
	Allowed []string        // izinli servisler (büyük/küçük harf duyarsız)
}

// scopeDecision — kararın çıktısı.
type scopeDecision struct {
	Evaluated bool     // karar koştu (off / muaf / argümansız değil)
	Outside   []string // izinli kümede olmayan servisler
	Refuse    bool     // on: araç koşmaz
	Count     bool     // shadow: yalnız sayılır
}

// decideScope — SAF karar tablosu (TestDecideScopeTable).
func decideScope(in scopeDecisionIn) scopeDecision {
	if in.Mode != chstore.CosreEvidenceShadow && in.Mode != chstore.CosreEvidenceOn {
		return scopeDecision{}
	}
	if cls, _, _ := mcptools.V2ScopeClass(in.Tool); cls != mcptools.ScopeCarrying {
		return scopeDecision{}
	}
	var m map[string]any
	if len(strings.TrimSpace(string(in.Args))) == 0 || json.Unmarshal(in.Args, &m) != nil || m == nil {
		return scopeDecision{}
	}
	svcs := mcptools.ScopeArgServices(m)
	if len(svcs) == 0 {
		return scopeDecision{}
	}
	out := scopeDecision{Evaluated: true}
	for _, v := range svcs {
		if !containsFold(in.Allowed, v) && !containsFold(out.Outside, v) {
			out.Outside = append(out.Outside, v)
		}
	}
	if len(out.Outside) > 0 {
		out.Refuse = in.Mode == chstore.CosreEvidenceOn
		out.Count = in.Mode == chstore.CosreEvidenceShadow
	}
	return out
}

// outOfScopeContent — SAF: reddin modele giden gövdesi (mcp.ToolError biçimi).
func outOfScopeContent(outside []string) string {
	b, err := json.Marshal(mcp.ToolError{Error: outOfScopeError, Retryable: false, Hint: outOfScopeHint,
		Service: strings.Join(outside, ",")})
	if err != nil {
		return `{"error":"out_of_scope","retryable":false}`
	}
	return string(b)
}

// scopeWouldRefuseDetail — SAF: gölge sinyalinin ayrıntısı (≤512 B, sinyal kırpar).
func scopeWouldRefuseDetail(tool string, outside []string) string {
	return "tool=" + tool + " services=" + strings.Join(outside, ",")
}

// scopeGate — alışverişin kapsam kararı bağlamı; nil = değerlendirilmez.
type scopeGate struct {
	mode    string
	allowed []string
	// signal — gölge sinyalinin yazıcısı (nil = yazılmaz: değerlendirme koşusu).
	signal func(tool string, outside []string)
}

// scopeGateIn — kapının alışveriş girdileri.
type scopeGateIn struct {
	Mode          string
	Scope         userScope
	TraceFollowUp bool
	Named         []string // bu turda kullanıcının tam adla andığı servisler
	Screen        []string // ekran servisi (istek + servis sayfası)
	State         []string // konuşma durumunun servis(ler)i
}

// buildScopeGate — SAF: değerlendirme koşmayacaksa nil.
func buildScopeGate(in scopeGateIn) *scopeGate {
	if in.Mode != chstore.CosreEvidenceShadow && in.Mode != chstore.CosreEvidenceOn {
		return nil
	}
	if in.Scope.Unscoped || len(in.Scope.Services) == 0 || in.TraceFollowUp {
		return nil
	}
	g := &scopeGate{mode: in.Mode}
	for _, set := range [][]string{in.Scope.Services, in.Named, in.Screen, in.State} {
		for _, v := range set {
			if v = strings.TrimSpace(v); v != "" && !containsFold(g.allowed, v) {
				g.allowed = append(g.allowed, v)
			}
		}
	}
	return g
}

// check — dikiş: (sonuç, true) = araç koşmaz (on reddi); false = yürüt.
func (g *scopeGate) check(tool string, args json.RawMessage) (agenttools.Outcome, bool) {
	if g == nil {
		return agenttools.Outcome{}, false
	}
	d := decideScope(scopeDecisionIn{Mode: g.mode, Tool: tool, Args: args, Allowed: g.allowed})
	switch {
	case d.Refuse:
		return agenttools.Outcome{Content: outOfScopeContent(d.Outside), IsError: true, Kind: agenttools.KindScope}, true
	case d.Count && g.signal != nil:
		g.signal(tool, d.Outside)
	}
	return agenttools.Outcome{}, false
}

// scopeCheck — v2 planının kapısı; v1'de (plan nil) ve kapı yokken yürüt.
func (p *v2LoopPlan) scopeCheck(tool string, args json.RawMessage) (agenttools.Outcome, bool) {
	if p == nil {
		return agenttools.Outcome{}, false
	}
	return p.Gate.check(tool, args)
}

// cosreEvidenceMode — v2 alışverişinin kanıt kipi (v1: off).
func cosreEvidenceMode(ctx context.Context) string {
	if !cosreV2Marked(ctx) {
		return chstore.CosreEvidenceOff
	}
	return chstore.CurrentCosreConversation().Evidence
}

// newScopeGate — okumalar + kapı. off'ta katalog bile okunmaz.
func (s *Server) newScopeGate(ctx context.Context, us userScope, question string, screen []string, state ChatContext, traceFU bool, xid string) *scopeGate {
	in := scopeGateIn{Mode: cosreEvidenceMode(ctx), Scope: us, TraceFollowUp: traceFU, Screen: screen,
		State: append([]string{state.Service}, state.Services...)}
	if buildScopeGate(in) == nil {
		return nil
	}
	in.Named = explicitStateServices(question, s.guidedServiceNames(ctx))
	g := buildScopeGate(in)
	if chatEvalModeFrom(ctx) == nil {
		g.signal = func(tool string, outside []string) {
			s.writeSystemSignals(ctx, xid, []chstore.CosreSignal{{ExchangeID: xid, Kind: cosrelearn.SigScopeWouldRefuse,
				UserEmail: cosrelearn.SystemSignalActor, Detail: scopeWouldRefuseDetail(tool, outside)}})
		}
	}
	return g
}
