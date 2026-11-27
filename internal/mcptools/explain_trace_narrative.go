package mcptools

// explain_trace_narrative.go — v0.10.2482 (operatör onaylı): explain_trace
// aracı trace açıklaması biçim ayarını izler
// (cosre_conversation.traceExplainStyle). anlatim iken veri gövdesine (eski
// alanlar bayt bayt aynı) şunlar EKLENİR:
//
//   - explanation_style "anlatim";
//   - narrative {label, label_key, karar, code_finding?, next_step,
//     generated_at_ms} — Coremetry'nin ✨ trace açıklamasının (aynı kanıt, aynı
//     istem) önbellekteki son cevabından ayrıştırılır. Araç MODEL ÇAĞIRMAZ
//     (bir araç içinden ikinci model çağrısı döngüyü ikiye katlardı): cevap
//     yoksa narrative null + narrative_status "not_generated";
//   - evidence — sunucunun hesapladığı sınırlı kanıt görünümünün özü (UI'ın
//     hata zinciri / Diğer kanıt / yaygınlık satırlarını çizdiği veri; ≤ EtNarrativeEvidenceBytes);
//   - logs_reviewed {total, sent} (v0.10.2483) — KARAR'ın log paragrafının sayımı;
//   - text — düz metin hâli (anlatım + deterministik kanıt özeti; cevap yoksa
//     "AI yazmadı" diye işaretli özet).
//
// bolumlu iken (ya da kaynak nil) gövde eskisinin aynısı. Ek okuma (kanıt
// kurucusu: trace + loglar) Deps.Memo'da etCacheTTL kadar tutulur.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cilcenk/coremetry/internal/mcp"
)

// TraceNarrativeRead — api'nin anlatım okuması (model çağrısı yok).
type TraceNarrativeRead struct {
	Found         bool            // trace kanıtı kurulabildi
	Generated     bool            // önbellekte bu biçimde üretilmiş açıklama var
	Label         string          // kapalı kümeden ("" = model etiket yazmadı)
	LabelKey      string          // dil-bağımsız anahtar (business, dependency …)
	Karar         string          // KARAR metni
	CodeFinding   string          // yalnız kod dalı cevabında
	NextStep      string          // Sonraki adım
	GeneratedAtMs int64           // önbellek satırının üretim anı
	Evidence      json.RawMessage // sınırlı kanıt özü (nil = yok)
	Summary       string          // deterministik tek satır (AI değil)
	// LogsReviewed — v0.10.2483: KARAR'ın log paragrafının dayandığı sayım
	// (okunan satır / modele giden satır); nil = log yok ya da okunamadı.
	LogsReviewed *LogsReviewed
}

// LogsReviewed — logs_reviewed {total, sent}.
type LogsReviewed struct {
	Total int `json:"total"`
	Sent  int `json:"sent"`
}

// TraceNarrativeSource — api doldurur (mcp_trace_narrative.go). nil = ek yok.
type TraceNarrativeSource interface {
	NarrativeOn() bool
	ReadTraceNarrative(ctx context.Context, traceID string) (TraceNarrativeRead, error)
}

// EtNarrativeEvidenceBytes — evidence alanının tavanı (api kırpar; aşan eklenmez).
const EtNarrativeEvidenceBytes = 1536

// ExplainTraceNarrativeDescription — açıklamanın biçime bağlı eki (yalnız kaynak bağlıyken).
const ExplainTraceNarrativeDescription = " When the operator's trace explanation style is 'anlatim' (narrative, the default) the body ALSO carries: " +
	"explanation_style; narrative {label (one of: Business hata, Bağımlılık hatası, Zaman aşımı, Yavaşlık, Yetki hatası, Altyapı hatası, Başarılı, Belirsiz), " +
	"label_key (business|dependency|timeout|slow|auth|infra|ok|unclear), karar (the single explanation: paragraph 1 = what happened and why, paragraph 2 = what the trace's logs show, cited by time and service; paragraphs separated by a blank line), next_step, generated_at_ms} " +
	"and logs_reviewed {total (log rows read for the trace), sent (rows the explanation was given)} when the trace has logs; " +
	"from Coremetry's cached one-click Explain of this trace — this tool never runs a model: narrative is null with narrative_status=not_generated when the trace was not explained yet; " +
	"evidence (server-computed, ≤1.5 KB: error-chain origin and root, critical-path top step, prevalence of the failing / root operation in the hour before the trace, stack top frame, deterministic summary); " +
	"text (plain-text rendering; marked 'AI yazmadı' when narrative is null). Quote evidence values verbatim. In the 'bolumlu' style these fields are absent."

// withNarrativeTool — explain_trace'e anlatım eki (kaynak nil → aynen).
func withNarrativeTool(d Deps, base mcp.Tool) mcp.Tool {
	if d.TraceNarrative == nil {
		return base
	}
	t := base
	t.Description = base.Description + ExplainTraceNarrativeDescription
	t.Handler = func(ctx context.Context, raw json.RawMessage) (any, error) {
		out, err := base.Handler(ctx, raw)
		if err != nil || !d.TraceNarrative.NarrativeOn() {
			return out, err
		}
		body, ok := decodeBody(out)
		if !ok || body["found"] != true {
			return out, nil
		}
		id, _ := body["trace_id"].(string)
		extra, merr := v2Memo(ctx, d, "mcp:explain_trace:nar:v1:"+id, etCacheTTL, func(c context.Context) (map[string]any, error) {
			nr, err := d.TraceNarrative.ReadTraceNarrative(c, id)
			if err != nil {
				return nil, err
			}
			return narrativeFields(nr), nil
		})
		if merr != nil {
			body["explanation_style"] = "anlatim"
			body["narrative"] = nil
			body["narrative_status"] = "unavailable"
			body["narrative_reason"] = merr.Error()
			return body, nil
		}
		if m, ok := decodeBody(extra); ok {
			for k, v := range m {
				body[k] = v
			}
		}
		return body, nil
	}
	return t
}

// decodeBody — memo gövdesi (json.RawMessage) ya da harita → harita.
func decodeBody(out any) (map[string]any, bool) {
	s, err := bodyString(out)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) != nil || m == nil {
		return nil, false
	}
	return m, true
}

// narrativeFields — SAF: okumadan gövdeye eklenecek alanlar.
func narrativeFields(nr TraceNarrativeRead) map[string]any {
	out := map[string]any{"explanation_style": "anlatim"}
	if nr.LogsReviewed != nil {
		out["logs_reviewed"] = nr.LogsReviewed
	}
	if !nr.Found {
		out["narrative"] = nil
		out["narrative_status"] = "unavailable"
		return out
	}
	if len(nr.Evidence) > 0 && len(nr.Evidence) <= EtNarrativeEvidenceBytes {
		out["evidence"] = nr.Evidence
	}
	if !nr.Generated {
		out["narrative"] = nil
		out["narrative_status"] = "not_generated"
		out["text"] = "Otomatik özet — AI yazmadı: " + nr.Summary
		return out
	}
	n := map[string]any{"label": nr.Label, "label_key": nr.LabelKey, "karar": nr.Karar, "next_step": nr.NextStep, "generated_at_ms": nr.GeneratedAtMs}
	if nr.CodeFinding != "" {
		n["code_finding"] = nr.CodeFinding
	}
	out["narrative"] = n
	out["narrative_status"] = "cached"
	out["text"] = narrativePlainText(nr)
	return out
}

// narrativePlainText — SAF: düz metin hâli (copilot.TraceNarrative.PlainText
// biçimi + sunucunun kanıt özeti satırı).
func narrativePlainText(nr TraceNarrativeRead) string {
	var lines []string
	add := func(prefix, v string) {
		if v != "" {
			lines = append(lines, prefix+v)
		}
	}
	add("", nr.Label)
	add("KARAR: ", nr.Karar)
	add("Kod bulgusu: ", nr.CodeFinding)
	add("Sonraki adım: ", nr.NextStep)
	add("Kanıt (sunucu): ", nr.Summary)
	return strings.Join(lines, "\n")
}
