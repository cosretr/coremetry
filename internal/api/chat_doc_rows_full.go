package api

// chat_doc_rows_full.go — v0.10.1554 — Operator-reported ("Hepsini getirsin
// istiyorum"): satır kademesi eşleşen PARÇALARI okuyordu; parça sınırında
// ikiye bölünen satır (her ~10 satırda bir, ölçüm: wiki.BuildChunks ~3000
// rune / 10 satır) kesilen hücreleriyle kaçabiliyordu. Artık eşleşen
// parçaların sayfaları TAM metinle (wiki deposundaki sayfa kaydı) okunur ve
// satırlar oradan süzülür; okunamayan sayfa parçalarıyla kalır.

import (
	"context"
	"strings"

	"github.com/cilcenk/coremetry/internal/wiki"
)

// docRowsMaxPages — tam metni okunacak sayfa tavanı (envanter belgeleri az sayıda).
const docRowsMaxPages = 5

// docRowsFullPages — eşleşen parçaların sayfalarını tam metinle değiştirir.
func (s *Server) docRowsFullPages(ctx context.Context, chunks []docRowChunk) []docRowChunk {
	w := wikiKB()
	if w == nil || len(chunks) == 0 {
		return chunks
	}
	pages := map[string]string{}
	for _, c := range chunks {
		if c.Path == "" {
			continue
		}
		if _, seen := pages[c.Path]; seen {
			continue
		}
		if len(pages) >= docRowsMaxPages {
			break
		}
		pages[c.Path] = ""
		if rec, err := w.ReadPage(ctx, wiki.LocalProject, wiki.LocalWikiName, c.Path); err == nil && rec != nil {
			pages[c.Path] = rec.Content
		}
	}
	return docRowsMergeFull(chunks, pages)
}

// docRowsMergeFull — SAF: tam metni olan sayfanın parçaları tek tam-metin
// parçayla (ilk parçanın başlığı / url'si) değişir; diğerleri aynen.
func docRowsMergeFull(chunks []docRowChunk, pages map[string]string) []docRowChunk {
	out := make([]docRowChunk, 0, len(chunks))
	done := map[string]bool{}
	for _, c := range chunks {
		full := pages[c.Path]
		if c.Path == "" || strings.TrimSpace(full) == "" {
			out = append(out, c)
			continue
		}
		if done[c.Path] {
			continue
		}
		done[c.Path] = true
		out = append(out, docRowChunk{Title: c.Title, URL: c.URL, Path: c.Path, Text: full})
	}
	return out
}
