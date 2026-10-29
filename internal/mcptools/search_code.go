package mcptools

// search_code — v0.10.1208: servisin deposunda Azure DevOps Code Search.
// Depo YALNIZ servisten çözülür (api: devops.ResolveRepo — service_metadata
// pini ya da adlandırma kuralı); model depo seçemez. Çıktı dosya + (sunucu
// verirse) satır + kısa parça; kodu okumak için read_source_code. Koşullu
// sohbet aracı: Deps.CodeSearch nil (DevOps / Code Search kapalı) → sunulmaz.
//
// v0.10.1509 (plan-operation-card, operatör kararı 2026-10-23 gece madde 3) —
// `kind: def|ref|class|text` kipi: Azure Code Search kod öğesi filtreleri
// (def: tanım, ref: kullanım/çağrı, class: sınıf tanımı). Araç sayısı 74
// DEĞİŞMEZ, yalnız şema genişler. def/ref/class'ta query TEK tanımlayıcı
// olmalı; sorgu devops.BuildCodeQuery ile kurulur (enjeksiyon-güvenli, aksi
// hâlde error + ipucu, istek atılmaz). kind yok / text → bugünkü düz metin
// bayt bayt aynı.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/mcp"
)

const (
	SearchCodeToolName = "search_code"
	searchCodeMax      = 10
)

// CodeSearchResult — api tarafının cevabı.
type CodeSearchResult struct {
	Outcome    string                  `json:"outcome"` // ok | no_repo | no_hits | error | rate_limited (v0.10.1563)
	Repo       string                  `json:"repo,omitempty"`
	RepoSource string                  `json:"repo_source,omitempty"`
	Hits       []devops.ServiceCodeHit `json:"hits"`
	Hint       string                  `json:"hint,omitempty"`
}

// CodeSearcher — api/chat_code_search.go; nil = araç sunulmaz.
type CodeSearcher func(ctx context.Context, service, query string, limit int) (CodeSearchResult, error)

type searchCodeArgs struct {
	Service string `json:"service"`
	Query   string `json:"query"`
	Kind    string `json:"kind,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// searchCodeKinds — kind → Code Search terim türü ("" / text = düz metin).
var searchCodeKinds = map[string]devops.CodeQueryKind{
	"def": devops.CodeQueryDef, "ref": devops.CodeQueryRef, "class": devops.CodeQueryClass,
}

func searchCodeTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             SearchCodeToolName,
		ShortDescription: "Servisin deposunda kod ara (Code Search): dosya, satır, kısa parça.",
		Description: "Search the source code of ONE service with Azure DevOps Code Search. The repository is derived from the service name (never pass a repo). " +
			"Returns file paths with line and a short snippet when the server provides them; then call read_source_code with service + file (+ line) to read the code. " +
			"Use exact identifiers (class, method, error code, log text) as query. " +
			"kind narrows to code elements: def = where a method/symbol is DEFINED, ref = where it is USED/called (e.g. who calls approve), class = the class definition; " +
			"with def/ref/class the query must be ONE identifier (no dots, spaces or quotes). Default kind text = plain search. " +
			"Matches are by NAME, not type: another method with the same name also matches. limit default 5, max 10. Read-only.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service": map[string]any{"type": "string", "description": "Service name (resolve it first if unsure)."},
				"query":   map[string]any{"type": "string", "description": "Text to search: identifier, error code, log message fragment. With kind def/ref/class: a single identifier."},
				"kind":    map[string]any{"type": "string", "enum": []string{"text", "def", "ref", "class"}, "description": "text (default) | def (definition) | ref (usages / call sites) | class (class definition)."},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": searchCodeMax, "description": "Hits. Default 5."},
			},
			"required": []string{"service", "query"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a searchCodeArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("decode args: %w", err)
			}
			a.Service, a.Query = strings.TrimSpace(a.Service), strings.TrimSpace(a.Query)
			if a.Service == "" || a.Query == "" {
				return nil, fmt.Errorf("service and query required")
			}
			if d.CodeSearch == nil {
				return CodeSearchResult{Outcome: "error", Hits: []devops.ServiceCodeHit{}, Hint: "kod arama yapılandırılmamış"}, nil
			}
			q, hint := searchCodeQuery(a.Kind, a.Query)
			if hint != "" {
				return CodeSearchResult{Outcome: "error", Hits: []devops.ServiceCodeHit{}, Hint: hint}, nil
			}
			return d.CodeSearch(ctx, a.Service, q, clampLimit(a.Limit, 5, searchCodeMax))
		},
	}
}

// searchCodeQuery — SAF: kind + query → Code Search metni ya da ipucu
// (istek atılmaz). "" / text → query AYNEN (v0.10.1208 davranışı).
func searchCodeQuery(kind, query string) (string, string) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || kind == "text" {
		return query, ""
	}
	k, ok := searchCodeKinds[kind]
	if !ok {
		return "", "kind yalnız text | def | ref | class olabilir"
	}
	q, err := devops.BuildCodeQuery([]devops.CodeTerm{{Kind: k, Value: query}}, "")
	if err != nil {
		return "", "kind=" + kind + " için query tek bir tanımlayıcı olmalı (nokta, boşluk, tırnak yok) — ör. approve, OrderService"
	}
	return q, ""
}
