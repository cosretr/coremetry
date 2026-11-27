package api

// mcp_trace_narrative.go — v0.10.2482: explain_trace aracının anlatım
// kaynağı (mcptools.TraceNarrativeSource). Kanıt ✨ açıklamasının AYNI
// kurucusundan (buildTraceExplainInput); anlatım açıklamanın önbellek
// satırından (explainCacheKey(SystemPromptTraceNarrative(), in.User, "") —
// düğme ve sohbet turuyla aynı anahtar) okunur. Model çağrısı YOK.

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

type mcpTraceNarrativeSource struct{ s *Server }

func (m mcpTraceNarrativeSource) NarrativeOn() bool { return traceExplainNarrativeOn() }

func (m mcpTraceNarrativeSource) ReadTraceNarrative(ctx context.Context, traceID string) (mcptools.TraceNarrativeRead, error) {
	in, err := m.s.buildTraceExplainInput(ctx, traceID)
	if errors.Is(err, errExplainTraceNotFound) {
		return mcptools.TraceNarrativeRead{}, nil
	}
	if err != nil {
		return mcptools.TraceNarrativeRead{}, err
	}
	out := mcptools.TraceNarrativeRead{Found: true, Evidence: mcpTraceEvidence(in.View)}
	if in.View != nil {
		out.Summary = in.View.Fallback
		if lr := in.View.LogsReviewed; lr != nil { // v0.10.2483
			out.LogsReviewed = &mcptools.LogsReviewed{Total: lr.Total, Sent: lr.Sent}
		}
	}
	if env, hit := m.s.explainCacheGetCtx(ctx, explainCacheKey(copilot.SystemPromptTraceNarrative(), in.User, "")); hit {
		n := copilot.ParseTraceNarrative(env.Text)
		if n.Karar != "" {
			out.Generated, out.GeneratedAtMs = true, env.AtMs
			out.Label, out.LabelKey, out.Karar, out.CodeFinding, out.NextStep = n.Label, copilot.TraceNarrativeLabelKeys[n.Label], n.Karar, n.Code, n.Next
		}
	}
	return out, nil
}

// mcpTraceEvidenceView — evidence alanının özü (snake_case; MCP gövdesi).
type mcpTraceEvidenceView struct {
	Origin    *traceEvOrigin   `json:"origin,omitempty"`
	Chain     []traceEvHop     `json:"chain,omitempty"` // kaynak (+ kök)
	ChainHops int              `json:"chain_hops,omitempty"`
	Critical  *traceEvCritical `json:"critical,omitempty"`
	// v0.10.2483 — log listesi yok (loglar KARAR'ın 2. paragrafında); yaygınlık satırı var.
	Prevalence *traceEvPrevalence `json:"prevalence,omitempty"`
	LogRows    int                `json:"log_rows"`
	LogState   string             `json:"log_state,omitempty"`
	Frame      *traceEvFrame      `json:"stack_top_frame,omitempty"`
	RootMs     float64            `json:"root_ms,omitempty"`
	Summary    string             `json:"summary"`
}

// mcpTraceEvidence — SAF: görünüm → ≤ EtNarrativeEvidenceBytes JSON. Sığmazsa
// sırayla kök halkası, stack, kritik yol, yaygınlık düşer; yine
// sığmazsa nil (araç alanı hiç eklemez).
func mcpTraceEvidence(v *traceEvidenceView) json.RawMessage {
	if v == nil {
		return nil
	}
	e := mcpTraceEvidenceView{Origin: v.Origin, Critical: v.Critical, Prevalence: v.Prevalence, LogRows: v.LogRows, LogState: v.LogState,
		Frame: v.Frame, RootMs: v.RootMs, Summary: v.Fallback}
	if n := len(v.Chain); n > 0 {
		e.Chain = []traceEvHop{v.Chain[0]}
		if n > 1 {
			e.Chain = append(e.Chain, v.Chain[n-1])
		}
		e.ChainHops = n + v.ChainSkip
	}
	trims := []func(){
		func() {
			if len(e.Chain) > 1 {
				e.Chain = e.Chain[:1]
			}
		},
		func() { e.Frame = nil },
		func() { e.Critical = nil },
		func() { e.Prevalence = nil },
	}
	for i := 0; ; i++ {
		b, err := json.Marshal(e)
		if err != nil {
			return nil
		}
		if len(b) <= mcptools.EtNarrativeEvidenceBytes {
			return b
		}
		if i >= len(trims) {
			return nil
		}
		trims[i]()
	}
}
