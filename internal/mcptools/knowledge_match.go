package mcptools

// knowledge_match.go — Log Search Faz 6 dilim 6.4: search_knowledge'ın runbook
// sözcük sıralamasının (knowledgeTerms + rankRunbooks) HTTP yüzeyleri için
// dışa açık hâli. Yalnız runbook'lar (RAG belgeleri yok): sonuç RAG açık /
// kapalı fark etmeksizin aynı. Saf.

import "github.com/cilcenk/coremetry/internal/chstore"

// RunbookMatch — bir runbook eşleşmesi (log satırı açıklamasının runbook eki).
type RunbookMatch struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Href    string  `json:"href"`
	Snippet string  `json:"snippet,omitempty"`
	Score   float64 `json:"score"`
}

// MatchRunbooks — SAF: query'nin terimleriyle etkin runbook'ları sıralar;
// skoru minScore altındakiler düşer, ≤max satır. Terim yoksa boş.
func MatchRunbooks(books []chstore.Runbook, query string, minScore float64, max int) []RunbookMatch {
	terms := knowledgeTerms(query)
	if len(terms) == 0 || max <= 0 {
		return nil
	}
	var out []RunbookMatch
	for _, r := range rankRunbooks(books, terms) {
		if r.Score < minScore {
			continue
		}
		out = append(out, RunbookMatch{ID: r.ID, Title: r.Title, Href: r.Href, Snippet: r.Snippet, Score: r.Score})
		if len(out) >= max {
			break
		}
	}
	return out
}
