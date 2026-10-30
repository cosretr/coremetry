package api

// chat_prepare_step.go — CoSRE agent-core S3 (docs/cosre/agent-core-ideas.md §3):
// model çağrısından ÖNCE adım ayarı tek kapıdan (AI SDK `prepareStep`
// fikri, Go'da).
//
// Döngüde üç el yapımı ayar vardı: normal tur (ayar yok), tavan turu
// (ChatRoundCapAddendum + WithNoToolCalls) ve yineleme düşüşü
// (ChatDegenRetryAddendum + WithNoToolCalls + WithMaxTokensCap). Üçü artık
// bir stepPlan'dır; defaultPrepare bugünkü baytı üretir (kimlik planı).
//
// Güvenlik sözleşmesi: plan yalnız DARALTIR. Plan.Specs girdideki spec
// listesinin alt kümesine kırpılır (araç EKLEYEMEZ, toolsForRole üst
// sınırdır) ve konuşmada çağrılmış bir aracın tanımı düşürülemez —
// sağlayıcı geçmişte geçen aracın tanımını ister (Anthropic 400).
// SystemAddendum yalnız internal/copilot/prompts.go accessor'larından gelir.

import (
	"context"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// stepKind — modelin hangi tür turu koşacağı.
type stepKind int

const (
	stepNormal     stepKind = iota // araçlı serbest tur
	stepCap                        // tavan turu: elindekiyle cevapla, çağrı yasak
	stepDegenRetry                 // yineleme düşüşü: araçsız, token tavanlı tek deneme
)

// stepInput — planın gördüğü durum.
type stepInput struct {
	State loopState
	Kind  stepKind
	Conv  []copilot.ChatMessage
	Specs []copilot.ToolSpec
	Role  string
	// V2 — CoSRE v2 araç seti dilim 2 (chat_user_scope.go): v2 döngüsünün
	// alışveriş planı; v1'de nil (zincir bayt bayt eski).
	V2 *v2LoopPlan
}

// stepPlan — bir turun ayarı. Sıfır değer = aynen (dış ayar).
type stepPlan struct {
	// Specs — nil = girdideki liste aynen; nil-olmayan = daraltılmış liste
	// (narrowSpecs ile alt kümeye + çağrılmışlara zorlanır).
	Specs []copilot.ToolSpec
	// SystemAddendum — döngü prompt'unun SONUNA (önek önbelleği korunur).
	SystemAddendum string
	// Ctx — çağrı ctx'ine seçenek (WithNoToolCalls, WithMaxTokensCap…); nil = aynen.
	Ctx func(context.Context) context.Context
}

// prepareStep — bir plan adımı: girdi + önceki adımın planı → yeni plan.
type prepareStep func(in stepInput, prev stepPlan) stepPlan

// prepareChain — adımları sırayla uygular; sonraki öncekinin çıktısını görür.
func prepareChain(fs ...prepareStep) func(stepInput) stepPlan {
	return func(in stepInput) stepPlan {
		var p stepPlan
		for _, f := range fs {
			if f != nil {
				p = f(in, p)
			}
		}
		return p
	}
}

// withCtx — iki ctx seçeneğini sırayla birleştirir (önce a, sonra b).
func withCtx(a, b func(context.Context) context.Context) func(context.Context) context.Context {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	return func(ctx context.Context) context.Context { return b(a(ctx)) }
}

// capTurnPlan — tavan turu: ek + çağrı yasağı (tanımlar KALIR, bkz.
// copilot_chat.go tavan dalı notu).
func capTurnPlan(in stepInput, p stepPlan) stepPlan {
	if in.Kind != stepCap {
		return p
	}
	p.SystemAddendum += copilot.ChatRoundCapAddendum()
	p.Ctx = withCtx(p.Ctx, copilot.WithNoToolCalls)
	return p
}

// degenRetryPlan — yineleme düşüşü: ek + çağrı yasağı + token tavanı.
func degenRetryPlan(in stepInput, p stepPlan) stepPlan {
	if in.Kind != stepDegenRetry {
		return p
	}
	p.SystemAddendum += copilot.ChatDegenRetryAddendum()
	p.Ctx = withCtx(p.Ctx, func(ctx context.Context) context.Context {
		return copilot.WithMaxTokensCap(copilot.WithNoToolCalls(ctx), chatDegenRetryMaxTokens)
	})
	return p
}

// defaultPrepare — bugünkü döngünün üç ayarı; normal tur boş plan.
var defaultPrepare = prepareChain(capTurnPlan, degenRetryPlan)

// planCtx — planın ctx seçeneği uygulanmış ctx.
func (p stepPlan) planCtx(ctx context.Context) context.Context {
	if p.Ctx == nil {
		return ctx
	}
	return p.Ctx(ctx)
}

// planSpecs — turun gerçek spec listesi: plan nil ise girdi aynen, değilse
// narrowSpecs.
func (p stepPlan) planSpecs(in stepInput) []copilot.ToolSpec {
	if p.Specs == nil {
		return in.Specs
	}
	return narrowSpecs(in.Specs, p.Specs, in.Conv)
}

// narrowSpecs — SAF: sonuç `full` sırasını korur ve yalnız ondan seçer
// (araç eklenemez); `subset`te olan ya da konuşmada çağrılmış her araç
// kalır (keepReferenced: çağrılmış aracın tanımı düşürülemez).
//
// Araç seti dilim 2 — `subset`teki araç SUBSET'in tanımıyla gelir: plan bir
// aracın şemasını DARALTABİLİR (v2: explain_log_spike.query düşer). Ad yine
// `full`dan gelmek zorunda (ekleme yok); yalnız konuşmada çağrılmış, subsette
// olmayan araç `full` tanımını korur.
func narrowSpecs(full, subset []copilot.ToolSpec, conv []copilot.ChatMessage) []copilot.ToolSpec {
	inSub := make(map[string]copilot.ToolSpec, len(subset))
	for _, s := range subset {
		inSub[s.Name] = s
	}
	called := map[string]bool{}
	for _, m := range conv {
		for _, tc := range m.ToolCalls {
			called[tc.Name] = true
		}
	}
	out := make([]copilot.ToolSpec, 0, len(subset))
	for _, s := range full {
		if sub, ok := inSub[s.Name]; ok {
			out = append(out, sub)
		} else if called[s.Name] {
			out = append(out, s)
		}
	}
	return out
}

// loopTurn — döngünün TEK model-çağrı kapısı: plan → ctx, prompt eki, spec.
// Turun gerçek sistem prompt'u da döner (cevap süzgeci/düşüş onu yeniden kullanır).
func (s *Server) loopTurn(ctx context.Context, prompt string, in stepInput, emit func(string, any)) (copilot.ChatTurn, string, error) {
	prep := defaultPrepare
	if in.V2 = v2LoopPlanFrom(ctx); in.V2 != nil {
		prep = v2Prepare // araç seti dilim 2 — v2 alt kümesi + kapsam eki
	}
	plan := prep(in)
	sys := prompt + plan.SystemAddendum
	turn, err := s.chatLoopTurn(plan.planCtx(ctx), sys, in.Conv, plan.planSpecs(in), emit)
	return turn, sys, err
}
