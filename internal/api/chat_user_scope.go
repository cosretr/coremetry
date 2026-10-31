package api

// chat_user_scope.go — CoSRE v2 araç seti dilim 2 (docs/cosre/tools-audit.md
// §3, §9, §10 G9/G10, Q1/Q2/Q7; operatör onayı 2026-10-30).
//
// YALNIZ v2 sohbeti (POST /api/cosre/v2/chat). İşaret istek ctx'inde
// (cosreV2Chat koyar); v1 ucu işaretsiz → bu dosyanın hiçbir dalı koşmaz,
// v1 akışı ve kataloğu bayt bayt aynı.
//
//  1. user_scope — kullanıcının KENDİ servisleri, alışveriş başına bir kez:
//     oturum kimliği → users.team → team_aliases → service_metadata
//     ownerTeam VEYA sreTeam (mcptools.TeamServiceNames — sohbet emsali, G9;
//     Inbox'ın AND süzgeci DEĞİL, DECISIONS 2026-10-30). Kapsam `services[]`
//     (+ `team`) argümanı olarak, aracın şeması alıyorsa ve model vermediyse
//     çağrıdan ÖNCE enjekte edilir (applyUserScope; applyScreenContext'in
//     yanındaki dikiş). Model açıkça servis/takım verdiyse onunki kalır.
//  2. Q1 — takım boş / takım servis sahibi değil / kimlik yok / okuma hatası:
//     kapsam SESSİZCE genişlemez. data-scope{unscoped:true, reason} her
//     alışverişte; model bu konuşmada henüz sormadıysa BİR KEZ "Hangi takım?"
//     diye sorar (prompts.go ChatUserScopeAskTeamAddendum), sorduysa bir daha
//     sormaz ve kapsamsız çalışır.
//  3. Q2 — kaçış: istekte @-kapsam ya da /-komut varsa bu alışveriş kapsamsız
//     (reason "escape"); system_settings `ai.userScope.enabled=false` globalde
//     kapatır (reason "disabled"; ai_user_scope_settings.go).
//  4. G10/Q7 — v2 model alt kümesi prepareStep planıyla (v2SubsetPlan):
//     mcptools.V2Hidden araçları modelin spec listesinden düşer,
//     explain_log_spike şemasından serbest `query` çıkar (çağrı argümanından da
//     — V2StripArgs). Registry ve dış MCP sunucusu aynen.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// data-scope reason değerleri (uistream.ScopeReasons ile eşit; test pinler).
const (
	scopeReasonTeam       = "team"
	scopeReasonNoIdentity = "no_identity"
	scopeReasonNoTeam     = "no_team"
	scopeReasonNoServices = "no_services"
	scopeReasonDisabled   = "disabled"
	scopeReasonEscape     = "escape"
	scopeReasonError      = "error"
)

// chatScopeEventKind — v1 olay türü; yalnız v2 yazıcısına ulaşır (v1OnlySink düşürür).
const chatScopeEventKind = "scope"

// scopeEventMaxServices — data-scope'ta adıyla taşınan servis tavanı.
const scopeEventMaxServices = 20

// userScope — alışverişin kullanıcı kapsamı.
type userScope struct {
	Unscoped bool
	Reason   string
	Team     string
	Services []string // TeamServiceNames sırası (alfabetik), MaxTeamServices tavanlı
	Trimmed  int
}

// event — data-scope yükü (SAF): {unscoped, reason, count, team?, services?, trimmed?}.
func (u userScope) event() map[string]any {
	out := map[string]any{"unscoped": u.Unscoped, "reason": u.Reason, "count": len(u.Services) + u.Trimmed}
	if u.Team != "" {
		out["team"] = u.Team
	}
	if len(u.Services) > 0 {
		svcs := u.Services
		if len(svcs) > scopeEventMaxServices {
			svcs = svcs[:scopeEventMaxServices]
		}
		out["services"] = append([]string(nil), svcs...)
	}
	if u.Trimmed > 0 {
		out["trimmed"] = u.Trimmed
	}
	return out
}

// userScopeInput — çözümlemenin girdileri (okumalar çağıranda).
type userScopeInput struct {
	Enabled, Escape bool
	UserID          string
	Team            string
	LookupErr       error
	TeamServices    []string // TeamServiceNames(aliases, metadata, Team)
}

// resolveUserScopeCore — SAF karar tablosu (sıra önemli: anahtar → kaçış →
// kimlik → okuma hatası → takım → servis).
func resolveUserScopeCore(in userScopeInput) userScope {
	switch {
	case !in.Enabled:
		return userScope{Unscoped: true, Reason: scopeReasonDisabled}
	case in.Escape:
		return userScope{Unscoped: true, Reason: scopeReasonEscape}
	case in.UserID == "":
		return userScope{Unscoped: true, Reason: scopeReasonNoIdentity}
	case in.LookupErr != nil:
		return userScope{Unscoped: true, Reason: scopeReasonError}
	case strings.TrimSpace(in.Team) == "":
		return userScope{Unscoped: true, Reason: scopeReasonNoTeam}
	case len(in.TeamServices) == 0:
		return userScope{Unscoped: true, Reason: scopeReasonNoServices, Team: strings.TrimSpace(in.Team)}
	}
	svcs, trimmed := in.TeamServices, 0
	if len(svcs) > mcptools.MaxTeamServices {
		trimmed = len(svcs) - mcptools.MaxTeamServices
		svcs = svcs[:mcptools.MaxTeamServices]
	}
	return userScope{Reason: scopeReasonTeam, Team: strings.TrimSpace(in.Team), Services: append([]string(nil), svcs...), Trimmed: trimmed}
}

// requestScopeEscape — SAF: istek açık @-kapsam ya da /-komut taşıyor mu (Q2).
func requestScopeEscape(scope *chatScope, command string) bool {
	return chatScopeActive(scope, command)
}

// resolveUserScope — okumalar + karar. teamOverride (yalnız evalset fikstürü,
// chatEvalMode.UserTeam) doluysa users okunmaz.
func (s *Server) resolveUserScope(ctx context.Context, escape bool, teamOverride string) userScope {
	in := userScopeInput{Enabled: s.aiUserScopeEnabled(ctx), Escape: escape}
	if !in.Enabled || in.Escape {
		return resolveUserScopeCore(in)
	}
	if teamOverride != "" {
		in.UserID, in.Team = "evalset", teamOverride
	} else if uid := copilot.MetaFromContext(ctx).UserID; uid != "" && s.store != nil {
		u, err := s.store.GetUserByID(ctx, uid)
		switch {
		case err != nil:
			in.UserID, in.LookupErr = uid, err
		case u != nil: // nil = API token / silinmiş hesap → kimlik yok
			in.UserID, in.Team = uid, u.Team
		}
	}
	if in.UserID != "" && in.LookupErr == nil && strings.TrimSpace(in.Team) != "" {
		mds, err := s.store.ListServiceMetadata(ctx)
		if err != nil {
			in.LookupErr = err
		} else {
			in.TeamServices = mcptools.TeamServiceNames(s.teamAliasesCtx(ctx), mds, strings.TrimSpace(in.Team))
		}
	}
	return resolveUserScopeCore(in)
}

// teamAlreadyAsked — SAF: konuşmada bir asistan turu takımı sordu mu (Q1 "bir kez").
func teamAlreadyAsked(msgs []copilot.ChatMessage) bool {
	for _, m := range msgs {
		if m.Role == "assistant" && strings.Contains(strings.ToLower(m.Text), copilot.ChatUserScopeAskMarker) {
			return true
		}
	}
	return false
}

// userScopeAddendum — SAF: kapsamın döngü eki (prompts.go accessor'larından).
// Kaçış ve kapalı anahtar ek ÜRETMEZ (operatörün açık seçimi / eski davranış).
func userScopeAddendum(u userScope, msgs []copilot.ChatMessage) string {
	switch {
	case !u.Unscoped:
		return copilot.ChatUserScopeAddendum(u.Team, len(u.Services)+u.Trimmed)
	case u.Reason == scopeReasonEscape || u.Reason == scopeReasonDisabled:
		return ""
	case teamAlreadyAsked(msgs):
		return copilot.ChatUserScopeUnscopedAddendum()
	}
	return copilot.ChatUserScopeAskTeamAddendum()
}

// ── v2 döngü işareti + planı ───────────────────────────────────────────

type cosreV2LoopKey struct{}
type v2LoopPlanKey struct{}

// withCosreV2Loop — istek v2 ucundan geldi (cosreV2Chat).
func withCosreV2Loop(ctx context.Context) context.Context {
	return context.WithValue(ctx, cosreV2LoopKey{}, true)
}

// cosreV2Marked — ctx v2 işaretli mi (v1: false).
func cosreV2Marked(ctx context.Context) bool {
	on, _ := ctx.Value(cosreV2LoopKey{}).(bool)
	return on
}

// v2LoopPlan — v2 döngüsünün alışveriş sabiti (kapsam + ek).
type v2LoopPlan struct {
	Scope    userScope
	Addendum string
	// Budget — araç seti dilim 4 (ai_loop_budget_settings.go): `ai.loopBudget`
	// ile daraltılmış alışveriş bütçesi. Sıfır değer = sabitler (eski yol).
	Budget loopBudget
}

func withV2LoopPlan(ctx context.Context, p *v2LoopPlan) context.Context {
	return context.WithValue(ctx, v2LoopPlanKey{}, p)
}

// v2LoopPlanFrom — döngü turunun v2 planı; v1'de nil.
func v2LoopPlanFrom(ctx context.Context) *v2LoopPlan {
	p, _ := ctx.Value(v2LoopPlanKey{}).(*v2LoopPlan)
	return p
}

// v2SubsetPlan — prepareStep adımı: v2 model alt kümesi + kapsam eki. v1 (V2 nil) aynen.
func v2SubsetPlan(in stepInput, p stepPlan) stepPlan {
	if in.V2 == nil {
		return p
	}
	sub := make([]copilot.ToolSpec, 0, len(in.Specs))
	for _, sp := range in.Specs {
		if mcptools.V2Hidden(sp.Name) {
			continue
		}
		sp.InputSchema = mcptools.V2NarrowSchema(sp.Name, sp.InputSchema)
		sub = append(sub, sp)
	}
	p.Specs = sub
	p.SystemAddendum += in.V2.Addendum
	return p
}

// v2Prepare — v2 zinciri: alt küme + ek önce, sonra tavan / yineleme ayarları.
var v2Prepare = prepareChain(v2SubsetPlan, capTurnPlan, degenRetryPlan)

// applyUserScope — SAF: v2 çağrı argümanı. (1) v2'de düşen argümanlar
// silinir; (2) kapsamlıysa ve şema `services` (dizi) alıyorsa ve model boş
// bıraktıysa kapsam servisleri, `team` alıyorsa ve boşsa takım konur.
// Model verdiyse modelinki kalır. applied = kapsam enjekte edildi mi.
func applyUserScope(tool string, args json.RawMessage, schema map[string]any, u userScope) (json.RawMessage, bool) {
	args, _ = mcptools.V2StripArgs(tool, args)
	if u.Unscoped || len(u.Services) == 0 || schema == nil {
		return args, false
	}
	props, _ := schema["properties"].(map[string]any)
	sp, hasSvcs := props["services"].(map[string]any)
	if !hasSvcs || sp["type"] != "array" {
		return args, false
	}
	m := map[string]any{}
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &m); err != nil || m == nil {
			return args, false
		}
	}
	if list, _ := m["services"].([]any); len(list) > 0 {
		return args, false
	}
	if t := argStr(m, "team"); t != "" && !strings.EqualFold(t, u.Team) {
		return args, false // model başka bir takımı adlandırdı: araç o takımı çözer
	}
	m["services"] = u.Services
	if _, hasTeam := props["team"]; hasTeam && u.Team != "" && argStr(m, "team") == "" {
		m["team"] = u.Team
	}
	b, err := json.Marshal(m)
	if err != nil {
		return args, false
	}
	return b, true
}
