package api

// explain_span.go — span ✨ Explain (POST /api/copilot/explain-span/{traceId}).
//
// v0.10.1148: api.go'dan taşındı (gövde aynen) + hedef span'in SPAN
// EVENT'LERİ bloğu. v0.10.1147 trace explain'e span exception event'lerini
// ekledi; span explain hâlâ yalnız ad/süre/status taşıyordu, Kafka producer
// "… publish" span'ine tıklayan operatör aynı genel "error statüsü" özetini
// alırdı. Blok explain_trace_events.go'nun AYNI builder'ı (tek span için):
// exception önce, tip + mesaj + kırpılmış stack, sınırlı. Ek okuma yok —
// resolveTraceSpans zaten Events'i taşıyor. Event'i olmayan span'de prompt
// (ve explain cache anahtarı) bayt-bayt eskisi.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/promptfmt"
)

// spanExplainEventsBlock — SAF: hedef span'in event bloğu ("" = event yok).
// Trace explain'in builder'ı tek span'lik dilimle: seçim/tavan/stack
// kırpması ve exception yönergesi aynı.
func spanExplainEventsBlock(target chstore.SpanRow) string {
	return buildTraceEventDigest([]chstore.SpanRow{target}).Block()
}

// copilotExplainSpan focuses the LLM on ONE span instead of the
// whole trace: target span + parent + direct children + any
// error spans in the same trace. Tighter prompt + cheaper round-
// trip than re-summarising the entire waterfall. Works with any
// configured backend — Anthropic, OpenAI, or a local LLM via
// OpenAI-compatible base URL (Ollama / vLLM / LM Studio).
func (s *Server) copilotExplainSpan(w http.ResponseWriter, r *http.Request) {
	traceID := r.PathValue("traceId")
	spanID := strings.TrimSpace(r.URL.Query().Get("span"))
	if spanID == "" {
		http.Error(w, "span query param required", http.StatusBadRequest)
		return
	}
	spans, _, err := s.resolveTraceSpans(r.Context(), traceID) // v0.9.632 — Tempo fallback dahil
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(spans) == 0 {
		http.Error(w, "trace not found", http.StatusNotFound)
		return
	}
	// Locate target + build the neighbourhood subset. O(n) on a
	// trace's span list which is bounded by the existing 100-span
	// cap on the trace-fetch path.
	var target *chstore.SpanRow
	for i := range spans {
		if spans[i].SpanID == spanID {
			target = &spans[i]
			break
		}
	}
	if target == nil {
		http.Error(w, "span not found in trace", http.StatusNotFound)
		return
	}
	// Keep: target, its parent (if any), direct children, any
	// error spans elsewhere in the trace (high signal for "why
	// did this fail" hints). Dedupe via span-id set.
	keep := map[string]bool{target.SpanID: true}
	if target.ParentSpanID != "" {
		keep[target.ParentSpanID] = true
	}
	for i := range spans {
		sp := &spans[i]
		if sp.ParentSpanID == target.SpanID {
			keep[sp.SpanID] = true
		}
		if sp.StatusCode == "error" {
			keep[sp.SpanID] = true
		}
	}
	type lite struct {
		Role       string  `json:"role"` // target | parent | child | error-elsewhere
		Name       string  `json:"name"`
		Service    string  `json:"service"`
		Kind       string  `json:"kind"`
		SpanID     string  `json:"id"`
		ParentID   string  `json:"parent,omitempty"`
		DurationMs float64 `json:"durMs"`
		Status     string  `json:"status,omitempty"`
		StatusMsg  string  `json:"statusMsg,omitempty"`
	}
	role := func(sp *chstore.SpanRow) string {
		switch {
		case sp.SpanID == target.SpanID:
			return "target"
		case sp.SpanID == target.ParentSpanID:
			return "parent"
		case sp.ParentSpanID == target.SpanID:
			return "child"
		default:
			return "error-elsewhere"
		}
	}
	compact := make([]lite, 0, len(keep))
	for i := range spans {
		sp := &spans[i]
		if !keep[sp.SpanID] {
			continue
		}
		dur := float64(sp.EndTime-sp.StartTime) / 1e6
		l := lite{
			Role: role(sp), Name: sp.Name, Service: sp.ServiceName,
			Kind: sp.Kind, SpanID: sp.SpanID, ParentID: sp.ParentSpanID,
			DurationMs: dur,
		}
		if sp.StatusCode == "error" {
			l.Status = "error"
			l.StatusMsg = sp.StatusMessage
		}
		compact = append(compact, l)
	}
	payload, _ := json.Marshal(compact)
	user := fmt.Sprintf("Span %s (target) in trace %s — %d spans in context:\n```json\n%s\n```",
		spanID, traceID, len(compact), promptfmt.FenceSafe(string(payload))) // v0.10.404 — çit kaçışı
	user += spanExplainEventsBlock(*target) // v0.10.1148 — hedef span'in event'leri (exception önce)
	r, xid := withExchange(r)
	s.deliverExplain(w, r, xid, nil, s.explainPrompt(r, copilot.SystemPromptSpan(), user), "", explainCacheKey(copilot.SystemPromptSpan(), user, ""))
}
