package uistream

// tool_result.go — CoSRE v2 araç seti dilim 1 (docs/cosre/tools-audit.md
// §10 G1, Q5): `data-tool-result{id:"s<i>"}` — araç adımının YAPISAL sonucu.
//
// TEK parça türü, `kind` içeride (Q5: 12 parça adı DEĞİL; fixture'lar ve
// protocol.ts kapalı küçük küme kalır). Kaynak, aynı adımın v1 step-result
// gövdesi: yapısal şekli olan sonuç (kanıt zarfı → `evidence`, yürütülmeyen
// çağrı → `skipped`, hata → `error`) eşlenir, gerisi en küçük `text`.
// Yeni araç yok; tool-output-available aynen kalır (bu parça yanına eklenir).
//
// Tavan: data JSON'u ≤ ToolResultMaxBytes (2 KB, brief "tool text ≤ 2 KB");
// fazlası `page_url` ile sayfada. page_url yalnız kök-göreli href'ten.

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// ToolResultMaxBytes — data-tool-result `data` JSON'unun bayt tavanı.
const ToolResultMaxBytes = 2048

// data-tool-result `kind` değerleri (kapalı küme; FE bilinmeyeni yok sayar).
const (
	ToolResultKindText     = "text"
	ToolResultKindEvidence = "evidence"
	ToolResultKindError    = "error"
	ToolResultKindSkipped  = "skipped"
)

// ToolResultKinds — kapalı küme (protocol.ts TOOL_RESULT_KINDS ile eşit; test pinler).
var ToolResultKinds = []string{ToolResultKindText, ToolResultKindEvidence, ToolResultKindError, ToolResultKindSkipped}

// evidenceQueryMax — kanıt sorgu metninin tavanı (sorgu kartta okunur, tamamı sayfada).
const evidenceQueryMax = 512

// toolResultPart — step-result gövdesinden data-tool-result (SAF).
func toolResultPart(id string, m map[string]any) Part {
	return Part{"type": "data-tool-result", "id": id, "data": ToolResultData(m)}
}

// ToolResultData — step-result gövdesi → ≤ 2 KB yapısal sonuç (SAF).
func ToolResultData(m map[string]any) map[string]any {
	d := map[string]any{"kind": ToolResultKindText}
	if tool, _ := m["tool"].(string); tool != "" {
		d["tool"] = tool
	}
	ok, _ := m["ok"].(bool)
	d["ok"] = ok
	if href, _ := m["href"].(string); rootRelative(href) {
		d["page_url"] = href
	}
	if n, has := asInt(m["durationMs"]); has {
		d["duration_ms"] = n
	}
	if n, has := asInt(m["bytes"]); has {
		d["bytes"] = n
	}
	truncated, _ := m["truncated"].(bool)
	preview, _ := m["preview"].(string)
	textKey := "text"
	switch skipped, _ := m["skipped"].(bool); {
	case skipped:
		d["kind"] = ToolResultKindSkipped
		if r, _ := m["reason"].(string); r != "" {
			d["reason"] = r
		}
	case !ok:
		d["kind"] = ToolResultKindError
	default:
		if ev, isMap := m["evidence"].(map[string]any); isMap {
			d["kind"] = ToolResultKindEvidence
			for _, k := range []string{"source", "queryKind", "window", "rows", "series", "hop", "env", "cluster"} {
				if v, has := ev[k]; has && v != nil && v != "" {
					d[snake(k)] = v
				}
			}
			if q, _ := ev["query"].(string); q != "" {
				d["query"] = q
				textKey = "query"
				preview = q
			}
			if _, has := d["page_url"]; !has {
				if href, _ := ev["href"].(string); rootRelative(href) {
					d["page_url"] = href
				}
			}
			if list, isList := m["evidenceList"].([]any); isList && len(list) > 1 {
				d["evidence_count"] = len(list)
			}
		}
	}
	if d["kind"] == ToolResultKindEvidence {
		if len(preview) > evidenceQueryMax {
			preview, truncated = clipBytes(preview, evidenceQueryMax), true
		}
	}
	if preview != "" {
		d[textKey] = preview
	}
	if truncated {
		d["truncated"] = true
	}
	return fitToolResult(d, textKey)
}

// fitToolResult — data ≤ ToolResultMaxBytes olana dek metin alanını kırpar
// (JSON kaçışı baytı büyütür: ölçü marshal'dan). Metin yetmezse metin düşer.
func fitToolResult(d map[string]any, textKey string) map[string]any {
	for i := 0; i < 32; i++ {
		b, err := json.Marshal(d)
		if err != nil || len(b) <= ToolResultMaxBytes {
			return d
		}
		s, _ := d[textKey].(string)
		if s == "" {
			break
		}
		over := len(b) - ToolResultMaxBytes
		keep := len(s) - over - 8
		if keep > len(s)*3/4 {
			keep = len(s) * 3 / 4
		}
		if keep <= 0 {
			delete(d, textKey)
		} else {
			d[textKey] = clipBytes(s, keep)
		}
		d["truncated"] = true
	}
	// Metinsiz hâlâ büyükse (aşırı uzun araç adı / pencere) yalnız çekirdek kalır.
	if b, _ := json.Marshal(d); len(b) > ToolResultMaxBytes {
		core := map[string]any{"kind": d["kind"], "ok": d["ok"], "truncated": true}
		if u, has := d["page_url"].(string); has && len(u) <= 1024 {
			core["page_url"] = u
		}
		return core
	}
	return d
}

// clipBytes — en çok n bayt, rune sınırında.
func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// rootRelative — yalnız kök-göreli href (`/…`, `//` ve `/\` değil).
func rootRelative(h string) bool {
	return strings.HasPrefix(h, "/") && !strings.HasPrefix(h, "//") && !strings.HasPrefix(h, "/\\")
}

// snake — camelCase kanıt alanı → snake_case (queryKind → query_kind).
func snake(k string) string {
	var b strings.Builder
	for _, r := range k {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
