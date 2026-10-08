package wiki

import (
	"context"
	"strings"
)

// diagnose.go — v0.10.1124 "Aramayı test et" (Ayarlar → Bilgi (RAG) → Azure
// DevOps Wiki): bir sorgunun yerel ve canlı yarısını AYRI ayrı gösterir —
// operatör "wiki'yi neden bulmuyor"u kodu okumadan görebilsin. İçerik
// döndürülmez: isabet başına başlık/yol/skor + ≤DiagSnippetRunes kesit.
// Geri çekilme ve "unavailable" önbelleği BU çağrıda atlanır (yönetici açıkça
// deniyor), sonucu yine paylaşılan duruma yazılır.

// DiagSnippetRunes — tanı kesitinin tavanı.
const DiagSnippetRunes = 160

// DiagHit — tanıdaki tek isabet (içerik yok, kısa kesit).
type DiagHit struct {
	Project string  `json:"project"`
	Wiki    string  `json:"wiki"`
	Path    string  `json:"path"`
	Title   string  `json:"title"`
	URL     string  `json:"url,omitempty"`
	Score   float64 `json:"score"`
	Live    bool    `json:"live,omitempty"`
	Snippet string  `json:"snippet,omitempty"`
}

// Diagnosis — Diagnose çıktısı.
type Diagnosis struct {
	Mode        string    `json:"mode"`
	Terms       []string  `json:"terms"`
	LiveQueries []string  `json:"liveQueries"`
	Stale       bool      `json:"stale"`
	Local       []DiagHit `json:"local"`
	Live        LiveDiag  `json:"live"`
	Final       []DiagHit `json:"final"`
}

func diagHits(h []Hit, terms []string) []DiagHit {
	out := make([]DiagHit, 0, len(h))
	for _, x := range h {
		out = append(out, DiagHit{Project: x.Project, Wiki: x.WikiName, Path: x.Path, Title: x.Title, URL: x.URL,
			Score: x.Score, Live: x.Live, Snippet: Snippet(x.Text, terms, DiagSnippetRunes)})
	}
	return out
}

// Diagnose — sorgunun yerel isabetleri, zorlanmış canlı denemesi ve birleşik
// son liste (sayfa başına 2 parça, en çok limit).
func (s *Service) Diagnose(ctx context.Context, query string, limit int) (Diagnosis, error) {
	if limit <= 0 || limit > 10 {
		limit = 5
	}
	cfg := s.Config()
	d := Diagnosis{Mode: cfg.EffectiveMode(), Terms: QueryTerms(query)}
	if len(d.Terms) == 0 {
		return d, ErrNoTerms
	}
	and, or := LiveSearchQueries(query)
	for _, q := range []string{and, or} {
		if q != "" {
			d.LiveQueries = append(d.LiveQueries, q)
		}
	}
	var local []Hit
	var st Stats
	if d.Mode != ModeLive {
		res, err := s.SearchWith(ctx, query, "", SearchOptions{Limit: limit, PerPage: 2, Live: LiveOff})
		if err != nil {
			return d, err
		}
		local, d.Stale = res.Hits, res.Stale
		st, _ = s.store.WikiTermStats(ctx, ExpandTerms(d.Terms), "") // v0.10.1127 (F4): DF genişlemiş jeton sırasında
	}
	d.Local = diagHits(local, d.Terms)
	var live []Hit
	if cfg.LiveSearchAllowed() {
		live, d.Live = s.liveSearch(ctx, query, "", d.Terms, st, cfg, true)
	} else {
		d.Live.Skipped = "disabled"
	}
	final := DedupePages(mergeHits(local, live), 2)
	if len(final) > limit {
		final = final[:limit]
	}
	d.Final = diagHits(final, d.Terms)
	d.Live.Note = strings.TrimSpace(d.Live.Note)
	return d, nil
}
