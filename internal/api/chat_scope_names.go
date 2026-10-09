package api

// chat_scope_names.go — v0.10.1138: composer'daki `@team:` tamamlamasının
// sunucu araması.
//
//	GET /api/copilot/scope-names?kind=team&q=<önek>   → {names: [...]}
//
// Picker kuralı: istemci kataloğu çekip süzmez; sunucu en çok 20 aday döner.
// Kaynak, sohbetin kapsamı doğruladığı AYNI liste (guidedTeamNames, 60 s
// Redis) — tamamlamanın önerdiği her ad sunucuda geçerli sayılır. Env
// tamamlaması mevcut /api/environments?q= aramasını kullanır (ikinci uç yok).
// Kayıt ai_routes.go'da (requireCopilot).

import (
	"net/http"
	"strings"
)

const scopeNamesLimit = 20

// filterScopeNames — SAF: önek eşleşmeleri önce, sonra içerenler; en çok n.
func filterScopeNames(names []string, q string, n int) []string {
	lq := strings.ToLower(strings.TrimSpace(q))
	var pre, sub []string
	for _, name := range names {
		ln := strings.ToLower(name)
		switch {
		case lq == "" || strings.HasPrefix(ln, lq):
			pre = append(pre, name)
		case strings.Contains(ln, lq):
			sub = append(sub, name)
		}
	}
	out := append(pre, sub...)
	if len(out) > n {
		out = out[:n]
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func (s *Server) copilotScopeNames(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "team" {
		writeJSONError(w, http.StatusBadRequest, "kind yalnız 'team' olabilir")
		return
	}
	q := r.URL.Query().Get("q")
	if len(q) > 80 {
		q = q[:80]
	}
	writeJSON(w, map[string]any{"names": filterScopeNames(s.guidedTeamNames(r.Context()), q, scopeNamesLimit)})
}
