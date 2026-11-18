package mcptools

// evidence_parts.go — v0.10.2360 (docs/cosre/investigation-design.md S3, karar
// 18; sre-evidence-audit §5.1–§5.2): kanıt blokları.
//
// Dört yeni blok tipi (`exception`, `trace`, `logs`, `anomaly`) + `find_traces`
// için mevcut `trace_list`. Hepsi `data-block-<tip>` olarak akar (çevirici
// değişmez). Yük YALNIZ araç sonucundan kurulur; model blok seçmez, yazmaz.
//
//	get_exception_samples → exception   find_traces      → trace_list
//	explain_trace         → trace       search_logs (v2) → logs
//	explain_ml_event      → anomaly
//
// Model görünümü: blok kurulan sonucun modele giden hâli kısa özettir
// (≤ EvModelViewMax B); satır listesi ve yığın modele dönmez.
//
// Ek alanlar (explain_trace hatalı span kimliği + start_ms,
// get_exception_samples grup satırı + ilk çerçeveler) yalnız ctx'te
// WithEvidenceParts varken yazılır: kanıt kapalıyken araç metni bayt aynı.

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
)

type evidencePartsKey struct{}

// WithEvidenceParts — ctx: araçlar kanıt bloğu için ek alanları yazar.
func WithEvidenceParts(ctx context.Context) context.Context {
	return context.WithValue(ctx, evidencePartsKey{}, true)
}

// EvidencePartsOn — ctx'te kanıt ek alanları açık mı.
func EvidencePartsOn(ctx context.Context) bool {
	on, _ := ctx.Value(evidencePartsKey{}).(bool)
	return on
}

// Tavanlar.
const (
	EvModelViewMax  = 600 // model görünümü (B)
	evPartFrames    = 5   // exception bloğunda çerçeve
	evViewFrames    = 3   // model görünümünde çerçeve
	evPartMsgRunes  = 160
	evPartLogRows   = 10
	evViewSamples   = 3
	evViewBodyRunes = 80
)

// EvWindowMs — mutlak pencere (unix ms).
type EvWindowMs struct {
	FromMs int64 `json:"fromMs"`
	ToMs   int64 `json:"toMs"`
}

// ExceptionPart — `exception` bloğu.
type ExceptionPart struct {
	Ref           string      `json:"ref"`
	Fingerprint   string      `json:"fingerprint"`
	Type          string      `json:"type"`
	Message       string      `json:"message,omitempty"`
	Count         uint64      `json:"count"`
	FirstSeenMs   int64       `json:"firstSeenMs,omitempty"`
	LastSeenMs    int64       `json:"lastSeenMs,omitempty"`
	State         string      `json:"state,omitempty"`
	SampleTraceID string      `json:"sampleTraceId,omitempty"`
	SampleSpanID  string      `json:"sampleSpanId,omitempty"`
	Frames        []string    `json:"frames,omitempty"`
	Entity        *ServiceRef `json:"entity,omitempty"`
	Window        *EvWindowMs `json:"window,omitempty"`
	Href          string      `json:"href"`
}

// TraceErrorSpan — `trace` bloğunun hatalı span'i.
type TraceErrorSpan struct {
	SpanID        string `json:"spanId,omitempty"`
	Service       string `json:"service"`
	Name          string `json:"name"`
	ExceptionType string `json:"exceptionType,omitempty"`
	Message       string `json:"message,omitempty"`
}

// TracePart — `trace` bloğu.
type TracePart struct {
	Ref        string          `json:"ref"`
	TraceID    string          `json:"traceId"`
	Service    string          `json:"service"`
	Operation  string          `json:"operation"`
	StartMs    int64           `json:"startMs,omitempty"`
	DurationMs float64         `json:"durationMs"`
	SpanCount  int             `json:"spanCount"`
	ErrorSpan  *TraceErrorSpan `json:"errorSpan,omitempty"`
	Entity     *ServiceRef     `json:"entity,omitempty"`
	Href       string          `json:"href"`
}

// LogsPartRow — `logs` bloğunun satırı.
type LogsPartRow struct {
	Ts      int64  `json:"ts"`
	Sev     string `json:"sev"`
	Service string `json:"service,omitempty"`
	Body    string `json:"body"`
	TraceID string `json:"traceId,omitempty"`
}

// LogsPart — `logs` bloğu.
type LogsPart struct {
	Ref       string        `json:"ref"`
	Query     string        `json:"query"`
	Total     int64         `json:"total"`
	Rows      []LogsPartRow `json:"rows"`
	Truncated bool          `json:"truncated,omitempty"`
	Entity    *ServiceRef   `json:"entity,omitempty"`
	Window    *EvWindowMs   `json:"window,omitempty"`
	Href      string        `json:"href"`
}

// AnomalyPart — `anomaly` bloğu.
type AnomalyPart struct {
	Ref            string   `json:"ref"`
	ID             string   `json:"id"`
	Detector       string   `json:"detector"`
	Signal         string   `json:"signal"`
	Service        string   `json:"service,omitempty"`
	Part           string   `json:"part,omitempty"`
	StartedMs      int64    `json:"startedMs,omitempty"`
	LastSeenMs     int64    `json:"lastSeenMs,omitempty"`
	Observed       float64  `json:"observed"`
	Baseline       float64  `json:"baseline"`
	SampleTraceIDs []string `json:"sampleTraceIds,omitempty"`
	Href           string   `json:"href"`
}

// EvidencePart — aday blok + model görünümü + cevapla eşleşme anahtarları.
type EvidencePart struct {
	Block     EvidenceBlock
	ModelView string
	// Keys — cevapta geçerse iddiayı bu bloğa bağlayan kimlikler (trace /
	// span kimliği, istisna tipi, ML olay kimliği, operasyon). Sayı değil.
	Keys []string
}

// EvidencePartTools — blok kuran araçlar.
var EvidencePartTools = map[string]string{
	"get_exception_samples": blocks.TypeException,
	ExplainTraceName:        blocks.TypeTrace,
	"search_logs":           blocks.TypeLogs,
	ExplainMLEventName:      blocks.TypeAnomaly,
	FindTracesName:          blocks.TypeTraceList,
}

// BuildEvidencePart — SAF: aracın HAM JSON sonucu → aday blok. Veri yok,
// bulunamadı ya da çözülemeyen sonuç → ok=false (blok yok, metin aynen).
func BuildEvidencePart(tool, content string) (EvidencePart, bool) {
	switch EvidencePartTools[tool] {
	case blocks.TypeException:
		return exceptionPart(content)
	case blocks.TypeTrace:
		return tracePart(content)
	case blocks.TypeLogs:
		return logsPart(content)
	case blocks.TypeAnomaly:
		return anomalyPart(content)
	case blocks.TypeTraceList:
		return traceListPart(content)
	}
	return EvidencePart{}, false
}

// ─── exception ───────────────────────────────────────────────

func exceptionPart(content string) (EvidencePart, bool) {
	var d struct {
		Fingerprint string `json:"fingerprint"`
		Service     string `json:"service"`
		ExType      string `json:"ex_type"`
		Occurrences uint64 `json:"occurrences"`
		LastSeenISO string `json:"last_seen_iso"`
		Samples     []struct {
			Message    string `json:"message"`
			Stacktrace string `json:"stacktrace"`
			TraceID    string `json:"trace_id"`
			SpanID     string `json:"span_id"`
		} `json:"samples"`
		Group *struct {
			Message     string `json:"message"`
			State       string `json:"state"`
			FirstSeenMs int64  `json:"first_seen_ms"`
			LastSeenMs  int64  `json:"last_seen_ms"`
		} `json:"group"`
		Frames  []string `json:"frames"`
		WindowS int      `json:"window_s"`
	}
	if json.Unmarshal([]byte(content), &d) != nil || d.Fingerprint == "" || d.ExType == "" {
		return EvidencePart{}, false
	}
	p := ExceptionPart{Ref: "exception:" + d.Fingerprint, Fingerprint: d.Fingerprint, Type: d.ExType, Count: d.Occurrences,
		Href: "/problems?exc=" + url.QueryEscape(d.Fingerprint)}
	if d.Service != "" {
		p.Entity = &ServiceRef{Service: d.Service}
	}
	if t, err := time.Parse(time.RFC3339, d.LastSeenISO); err == nil {
		p.LastSeenMs = t.UnixMilli()
	}
	if g := d.Group; g != nil {
		p.Message, p.State, p.FirstSeenMs = truncateRunes(g.Message, evPartMsgRunes), g.State, g.FirstSeenMs
		if g.LastSeenMs > 0 {
			p.LastSeenMs = g.LastSeenMs
		}
		if g.FirstSeenMs > 0 && p.LastSeenMs >= g.FirstSeenMs {
			p.Window = &EvWindowMs{FromMs: g.FirstSeenMs, ToMs: p.LastSeenMs}
		}
	}
	if len(d.Samples) > 0 {
		s := d.Samples[0]
		p.SampleTraceID, p.SampleSpanID = s.TraceID, s.SpanID
		if p.Message == "" {
			p.Message = truncateRunes(s.Message, evPartMsgRunes)
		}
		p.Frames = d.Frames
		if len(p.Frames) == 0 {
			p.Frames = StackFirstFrames(s.Stacktrace, evPartFrames)
		}
	}
	if len(p.Frames) > evPartFrames {
		p.Frames = p.Frames[:evPartFrames]
	}
	if p.Count == 0 && len(d.Samples) == 0 {
		return EvidencePart{}, false
	}
	view := map[string]any{"evidence": blocks.TypeException, "type": p.Type, "count": p.Count, "fingerprint": p.Fingerprint}
	if p.Message != "" {
		view["message"] = truncateRunes(p.Message, 120)
	}
	if p.FirstSeenMs > 0 {
		view["first_seen"] = isoMs(p.FirstSeenMs)
	}
	if p.LastSeenMs > 0 {
		view["last_seen"] = isoMs(p.LastSeenMs)
	}
	if p.SampleTraceID != "" {
		view["sample_trace_id"] = p.SampleTraceID
	}
	if len(p.Frames) > 0 {
		fr := p.Frames
		if len(fr) > evViewFrames {
			fr = fr[:evViewFrames]
		}
		short := make([]string, len(fr))
		for i, f := range fr {
			short[i] = truncateRunes(f, 100)
		}
		view["frames"] = short
	}
	keys := []string{p.Type, shortTypeName(p.Type), p.SampleTraceID, p.Fingerprint}
	return EvidencePart{Block: EvidenceBlock{Type: blocks.TypeException, Payload: p}, ModelView: fitView(view, "frames", "message"), Keys: cleanKeys(keys)}, true
}

// StackFirstFrames — SAF: yığının ilk n çerçeve satırı (başlık / Caused by /
// "... n more" satırları atlanır), her biri ≤160 rune. Dil bağımsız: ilk
// satır başlıktır; sonraki dolu satırlar çerçevedir.
func StackFirstFrames(stack string, n int) []string {
	lines := strings.Split(strings.ReplaceAll(stack, "\r\n", "\n"), "\n")
	var out []string
	for i, l := range lines {
		l = strings.TrimSpace(l)
		if i == 0 || l == "" || strings.HasPrefix(l, "Caused by") || strings.HasPrefix(l, "...") || strings.HasPrefix(l, "Traceback") {
			continue
		}
		out = append(out, truncateRunes(strings.TrimPrefix(l, "at "), evPartMsgRunes))
		if len(out) >= n {
			break
		}
	}
	return out
}

// ─── trace ───────────────────────────────────────────────────

func tracePart(content string) (EvidencePart, bool) {
	var d struct {
		TraceID string  `json:"trace_id"`
		Found   bool    `json:"found"`
		WallMs  float64 `json:"wall_ms"`
		StartMs int64   `json:"start_ms"`
		Root    struct {
			Service string `json:"service"`
			Name    string `json:"name"`
		} `json:"root"`
		SpanCount      int    `json:"span_count"`
		ErrorSpanCount int    `json:"error_span_count"`
		PageURL        string `json:"page_url"`
		CriticalPath   []struct {
			Service  string  `json:"service"`
			Name     string  `json:"name"`
			SharePct float64 `json:"share_pct"`
			Top      bool    `json:"top"`
		} `json:"critical_path"`
		ErrorSpans []etErr `json:"error_spans"`
	}
	if json.Unmarshal([]byte(content), &d) != nil || !d.Found || d.TraceID == "" {
		return EvidencePart{}, false
	}
	p := TracePart{Ref: "trace:" + d.TraceID, TraceID: d.TraceID, Service: d.Root.Service, Operation: d.Root.Name,
		StartMs: d.StartMs, DurationMs: d.WallMs, SpanCount: d.SpanCount, Href: ExplainTracePageURL(d.TraceID)}
	if d.Root.Service != "" {
		p.Entity = &ServiceRef{Service: d.Root.Service}
	}
	view := map[string]any{"evidence": blocks.TypeTrace, "trace_id": d.TraceID, "wall_ms": d.WallMs, "span_count": d.SpanCount,
		"error_span_count": d.ErrorSpanCount, "root": d.Root.Service + " · " + truncateRunes(d.Root.Name, 60), "page_url": p.Href}
	keys := []string{d.TraceID, d.Root.Name}
	if len(d.ErrorSpans) > 0 {
		e := d.ErrorSpans[0]
		msg := e.ExMsg
		if msg == "" {
			msg = e.Status
		}
		p.ErrorSpan = &TraceErrorSpan{SpanID: e.SpanID, Service: e.Service, Name: e.Name, ExceptionType: e.ExType, Message: truncateRunes(msg, evPartMsgRunes)}
		es := e.Service + " · " + truncateRunes(e.Name, 60)
		if e.ExType != "" {
			es += " · " + e.ExType
		}
		view["error_span"] = es
		keys = append(keys, e.SpanID, e.ExType, shortTypeName(e.ExType), e.Name)
	}
	for _, st := range d.CriticalPath {
		if st.Top {
			view["top_step"] = st.Service + " · " + truncateRunes(st.Name, 60)
			view["top_share_pct"] = st.SharePct
			break
		}
	}
	return EvidencePart{Block: EvidenceBlock{Type: blocks.TypeTrace, Payload: p}, ModelView: fitView(view, "top_step", "top_share_pct", "root"), Keys: cleanKeys(keys)}, true
}

// ─── logs ────────────────────────────────────────────────────

func logsPart(content string) (EvidencePart, bool) {
	var d struct {
		Total     int64   `json:"total"`
		HasMore   bool    `json:"has_more"`
		Query     string  `json:"query"`
		PageURL   string  `json:"page_url"`
		Rows      []slRow `json:"rows"`
		Truncated bool    `json:"truncated"`
		WindowS   int     `json:"window_s"`
		Window    *struct {
			FromMs int64 `json:"from_ms"`
			ToMs   int64 `json:"to_ms"`
		} `json:"window"`
		Entity *ServiceRef `json:"entity"`
	}
	if json.Unmarshal([]byte(content), &d) != nil || len(d.Rows) == 0 || d.PageURL == "" {
		return EvidencePart{}, false
	}
	p := LogsPart{Ref: "logs:" + fnvHex(d.Query, d.PageURL), Query: truncateRunes(d.Query, 240), Total: d.Total,
		Truncated: d.Truncated || d.HasMore, Entity: d.Entity, Href: d.PageURL}
	if d.Window != nil {
		p.Window = &EvWindowMs{FromMs: d.Window.FromMs, ToMs: d.Window.ToMs}
	}
	sev := map[string]int{}
	var samples []string
	var keys []string
	for i, r := range d.Rows {
		if i >= evPartLogRows {
			p.Truncated = true
			break
		}
		p.Rows = append(p.Rows, LogsPartRow{Ts: r.Ts, Sev: r.Sev, Service: r.Svc, Body: truncateRunes(r.Body, evPartMsgRunes), TraceID: r.TraceID})
		sev[r.Sev]++
		if len(samples) < evViewSamples {
			samples = append(samples, strings.TrimSpace(isoMs(r.Ts)+" "+r.Sev+" "+r.Svc+" "+truncateRunes(r.Body, evViewBodyRunes)))
		}
		keys = append(keys, r.TraceID)
	}
	view := map[string]any{"evidence": blocks.TypeLogs, "total": d.Total, "shown": len(p.Rows), "severity": sev,
		"query": truncateRunes(d.Query, 120), "page_url": d.PageURL, "sample": samples}
	return EvidencePart{Block: EvidenceBlock{Type: blocks.TypeLogs, Payload: p}, ModelView: fitView(view, "sample", "query"), Keys: cleanKeys(keys)}, true
}

// ─── anomaly ─────────────────────────────────────────────────

func anomalyPart(content string) (EvidencePart, bool) {
	var d struct {
		ID         string   `json:"id"`
		Signal     string   `json:"signal"`
		Detector   string   `json:"detector"`
		Service    string   `json:"service"`
		Part       string   `json:"part"`
		Detected   string   `json:"detected"`
		LastSeen   string   `json:"last_seen"`
		Baseline   float64  `json:"baseline_median"`
		Peak       float64  `json:"series_peak"`
		SampleTIDs []string `json:"sample_trace_ids"`
		PageURL    string   `json:"page_url"`
	}
	if json.Unmarshal([]byte(content), &d) != nil || d.ID == "" || d.Detector == "" {
		return EvidencePart{}, false
	}
	p := AnomalyPart{Ref: "anomaly:" + d.ID, ID: d.ID, Detector: d.Detector, Signal: d.Signal, Service: d.Service, Part: truncateRunes(d.Part, 80),
		Observed: d.Peak, Baseline: d.Baseline, Href: mlEventPageURL(d.ID)}
	if t, err := time.Parse(time.RFC3339, d.Detected); err == nil {
		p.StartedMs = t.UnixMilli()
	}
	if t, err := time.Parse(time.RFC3339, d.LastSeen); err == nil {
		p.LastSeenMs = t.UnixMilli()
	}
	if len(d.SampleTIDs) > 3 {
		d.SampleTIDs = d.SampleTIDs[:3]
	}
	p.SampleTraceIDs = d.SampleTIDs
	view := map[string]any{"evidence": blocks.TypeAnomaly, "id": d.ID, "detector": d.Detector, "signal": d.Signal, "service": d.Service,
		"detected": d.Detected, "observed_peak": d.Peak, "baseline": d.Baseline, "page_url": p.Href}
	if len(p.SampleTraceIDs) > 0 {
		view["sample_trace_ids"] = p.SampleTraceIDs
	}
	keys := append([]string{d.ID}, p.SampleTraceIDs...)
	return EvidencePart{Block: EvidenceBlock{Type: blocks.TypeAnomaly, Payload: p}, ModelView: fitView(view, "sample_trace_ids", "service"), Keys: cleanKeys(keys)}, true
}

// ─── trace_list (find_traces) ────────────────────────────────

func traceListPart(content string) (EvidencePart, bool) {
	var d struct {
		WindowS int    `json:"window_s"`
		HasMore bool   `json:"has_more"`
		Query   string `json:"query"`
		PageURL string `json:"page_url"`
		Window  *struct {
			FromMs int64 `json:"from_ms"`
			ToMs   int64 `json:"to_ms"`
		} `json:"window"`
		Traces    []ftRow `json:"traces"`
		Truncated bool    `json:"truncated"`
	}
	if json.Unmarshal([]byte(content), &d) != nil || len(d.Traces) == 0 || d.PageURL == "" {
		return EvidencePart{}, false
	}
	p := EvTraceListPayload{Query: d.Query, DeepLink: d.PageURL, Truncated: d.HasMore || d.Truncated, EvidenceRef: "trace_list:" + fnvHex(d.Query, d.PageURL)}
	if d.Window != nil {
		p.Window = EvBlockWindow{FromNs: d.Window.FromMs * 1e6, ToNs: d.Window.ToMs * 1e6}
	}
	var samples, keys []string
	errs := 0
	for _, t := range d.Traces {
		p.Traces = append(p.Traces, EvTraceRow{TraceID: t.ID, StartTime: t.StartMs * 1e6, ServiceName: t.EntrySvc, RootName: t.RootOp,
			DurationMs: t.Ms, SpanCount: t.Spans, HasError: t.Error})
		if t.Error {
			errs++
		}
		if len(samples) < evViewSamples {
			s := t.ID + " " + formatMs(t.Ms)
			if t.Error {
				s += " error"
			}
			samples = append(samples, s)
		}
		keys = append(keys, t.ID, t.RootOp)
	}
	view := map[string]any{"evidence": blocks.TypeTraceList, "count": len(d.Traces), "error_traces": errs, "has_more": d.HasMore,
		"window_s": d.WindowS, "query": truncateRunes(d.Query, 120), "page_url": d.PageURL, "sample": samples}
	return EvidencePart{Block: EvidenceBlock{Type: blocks.TypeTraceList, Payload: p}, ModelView: fitView(view, "query", "sample"), Keys: cleanKeys(keys)}, true
}

// ─── yardımcılar ─────────────────────────────────────────────

// fitView — SAF: özet ≤ EvModelViewMax olana dek verilen anahtarları sırayla düşürür.
func fitView(view map[string]any, drop ...string) string {
	for i := 0; ; i++ {
		b, err := json.Marshal(view)
		if err != nil {
			return ""
		}
		if len(b) <= EvModelViewMax || i >= len(drop) {
			if len(b) > EvModelViewMax {
				b, _ = json.Marshal(map[string]any{"evidence": view["evidence"], "note": "özet sığmadı; page_url'e bak"})
			}
			return string(b)
		}
		delete(view, drop[i])
	}
}

func isoMs(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func formatMs(v float64) string {
	b, _ := json.Marshal(round4(v))
	return string(b) + "ms"
}

// shortTypeName — "java.net.SocketTimeoutException" → "SocketTimeoutException".
func shortTypeName(t string) string {
	if i := strings.LastIndexAny(t, ".:"); i >= 0 && i+1 < len(t) {
		return t[i+1:]
	}
	return t
}

// cleanKeys — boş ve 4 rune'dan kısa anahtarlar düşer, tekil.
func cleanKeys(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range in {
		k = strings.TrimSpace(k)
		if len([]rune(k)) < 4 || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}
