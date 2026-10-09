package wiki

import (
	"context"
	"sort"
	"strings"
)

// pagechunks.go — v0.10.1136: sohbetin çok-kaynaklı okuması için bir
// sayfanın parçaları (idx sırasında). İsabet parçasının komşuları ve aynı
// başlık bölümünün geri kalanı buradan genişletilir (api/chat_wiki_multi.go).
//
// Kaynak sırası:
//   - canlı mod: bellek önbelleği (arama sayfayı zaten okudu); yoksa
//     ReadPage (API, CH'ye YAZILMAZ) + BuildChunks.
//   - karma/senkron: saklı parça METİNLERİ (ChunkTextReader — tek sayfa,
//     embedding'siz, WHERE + LIMIT + max_execution_time); boşsa / depo bu
//     okumayı sunmuyorsa ReadPage + BuildChunks.
//
// Kapsam HER yolda denetlenir: proje izin listesi; wiki izin listesi wiki
// adı biliniyorsa doğrudan, bilinmiyorsa (yalnız id) saklı parça okunmaz —
// ReadPage kaydın kendi wiki adıyla denetler. Canlı önbellek kaydı da kendi
// adıyla denetlenir.
//
// BuildChunks deterministik: canlı/yeniden kurulan parçaların idx'i indeksin
// idx'iyle aynıdır (aynı içerik → aynı bölme). Embedding taşınmaz.

// ChunkTextReader — depo opsiyonel dilimi: tek sayfanın parça metinleri
// (chstore.WikiPageChunkTexts). Sohbet yolu embedding okumasın diye Store
// arayüzünden ayrı (testlerin sahte depoları uygulamak zorunda değil).
type ChunkTextReader interface {
	WikiPageChunkTexts(ctx context.Context, wikiID, path string) ([]ChunkRecord, error)
}

// PageChunks — sayfanın parçaları (idx artan). İzin listesi dışı → ErrOutOfScope.
func (s *Service) PageChunks(ctx context.Context, project, wikiID, wikiName, path string) ([]ChunkRecord, error) {
	project, wikiID, wikiName = strings.TrimSpace(project), strings.TrimSpace(wikiID), strings.TrimSpace(wikiName)
	path = NormalizePagePath(path)
	cfg := s.Config()
	if !cfg.ProjectAllowed(project) || (wikiName != "" && !cfg.WikiAllowed(project, wikiName)) {
		return nil, ErrOutOfScope
	}
	ref := wikiID
	if ref == "" {
		ref = wikiName
	}
	if cfg.EffectiveMode() == ModeLive {
		for _, k := range []string{wikiID, wikiName} {
			if k == "" {
				continue
			}
			if rec, ch, ok := s.live.get(liveKey(project, k, path), s.now()); ok && len(ch) > 0 {
				if !cfg.WikiAllowed(rec.Project, rec.WikiName) {
					return nil, ErrOutOfScope
				}
				return stripChunks(ch), nil
			}
		}
	} else if r, ok := s.store.(ChunkTextReader); ok && wikiID != "" && (wikiName != "" || len(cfg.Wikis) == 0) {
		if ch, err := r.WikiPageChunkTexts(ctx, wikiID, path); err == nil && len(ch) > 0 {
			sortChunks(ch)
			return stripChunks(ch), nil
		}
	}
	rec, err := s.ReadPage(ctx, project, ref, path) // kaydın wiki adıyla kapsam denetimi
	if err != nil || rec == nil {
		return nil, err
	}
	return stripChunks(BuildChunks(rec.Title, rec.Content)), nil
}

// stripChunks — SAF: yalnız idx/başlık/metin (jeton ve embedding bırakılır).
func stripChunks(in []ChunkRecord) []ChunkRecord {
	out := make([]ChunkRecord, len(in))
	for i, c := range in {
		out[i] = ChunkRecord{Idx: c.Idx, Heading: c.Heading, Text: c.Text}
	}
	return out
}

// sortChunks — SAF: idx artan (SQL ORDER BY'a ek güvence).
func sortChunks(c []ChunkRecord) {
	sort.SliceStable(c, func(i, j int) bool { return c[i].Idx < c[j].Idx })
}
