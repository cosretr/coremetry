package api

// chat_code_search.go — v0.10.1208: search_code okuyucusu. Depo YALNIZ
// servisten (read_source_code ile aynı karar: service_metadata pini fail-
// closed, yoksa adlandırma kuralı — devops.ResolveRepo). Code Search kapalı
// ya da DevOps yoksa araç sohbete hiç sunulmaz (codeSearcherOrNil).

import (
	"context"
	"strings"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// chatCodeResolvePromptTR — SAF: iş kodu / kod arama eki; resolve_business_code
// sunulmuyorsa "" (rol süzgeci), search_code satırları yalnız o sunuluyorsa.
func chatCodeResolvePromptTR(tools []mcp.Tool) string {
	if !hasTool(tools, "resolve_business_code") {
		return ""
	}
	return copilot.CodeResolveChatAddendum(hasTool(tools, mcptools.SearchCodeToolName)) + "\n\n"
}

func (s *Server) codeSearcherOrNil() mcptools.CodeSearcher {
	if s.devops == nil || !s.devops.CodeSearchEnabled() {
		return nil
	}
	return s.searchServiceCode
}

func (s *Server) searchServiceCode(ctx context.Context, service, query string, limit int) (mcptools.CodeSearchResult, error) {
	out := mcptools.CodeSearchResult{Hits: []devops.ServiceCodeHit{}}
	pin := ""
	if s.store != nil {
		md, err := s.store.GetServiceMetadataStrict(ctx, service)
		mdRepo := ""
		if md != nil {
			mdRepo = md.Repository
		}
		p, abort := pinReadDecision(mdRepo, md != nil, err)
		if abort != "" {
			out.Outcome, out.Hint = "error", abort
			return out, nil
		}
		pin = p
	}
	res := devops.ResolveRepo(service, pin, s.devops.ResolveConfig())
	out.Repo, out.RepoSource = res.Repo, res.Source
	if res.Repo == "" {
		out.Outcome, out.Hint = "no_repo", strings.TrimSpace("servisin deposu çözülemedi: "+res.Reason)
		return out, nil
	}
	// v0.10.1563 — kullanıcı başına git çağrı sınırı (chat_source_rate.go).
	if ok, retry := sourceRateAllow(ctx, sourceRateSearch); !ok {
		out.Outcome, out.Hint = "rate_limited", sourceRateReason(sourceRateSearch, retry)
		return out, nil
	}
	hits, err := s.devops.SearchServiceCode(ctx, res.Repo, query, limit)
	if err != nil {
		out.Outcome, out.Hint = "error", err.Error()
		return out, nil
	}
	out.Hits = hits
	if len(hits) == 0 {
		out.Outcome, out.Hint = "no_hits", "depoda eşleşme yok — daha kısa ya da tam bir tanımlayıcıyla dene"
		return out, nil
	}
	out.Outcome, out.Hint = "ok", "kodu okumak için read_source_code(service, file, line)"
	return out, nil
}
