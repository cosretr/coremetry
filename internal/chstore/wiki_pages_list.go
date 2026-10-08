package chstore

import (
	"context"
	"strings"
	"time"
)

// wiki_pages_list.go — v0.10.1124: Ayarlar → Bilgi (RAG) → Azure DevOps Wiki
// "İndeksteki sayfalar" listesi (operatör: "wiki içeriğini sayfada
// göremiyorum"). Sunucu-sayfalı (≤100 satır), başlık/yol süzgeci bağlı
// argümanla; önizleme (içeriğin ilk WikiPreviewRunes karakteri) yalnız
// çağıran istediğinde (yönetici) seçilir. FINAL + deleted=0 + LIMIT +
// max_execution_time.

const (
	// WikiListMax — sayfa başına satır tavanı.
	WikiListMax = 100
	// WikiPreviewRunes — önizleme tavanı.
	WikiPreviewRunes = 1000
)

// WikiPageRow — liste satırı.
type WikiPageRow struct {
	Project   string    `json:"project"`
	Wiki      string    `json:"wiki"`
	WikiID    string    `json:"wikiId"`
	Path      string    `json:"path"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Chunks    uint32    `json:"chunks"`
	UpdatedAt time.Time `json:"updatedAt"`
	Preview   string    `json:"preview,omitempty"`
}

// wikiListFilter — SAF: opsiyonel başlık/yol süzgeci (harf-duyarsız alt dize).
func wikiListFilter(q string) (string, []any) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", nil
	}
	if r := []rune(q); len(r) > 200 {
		q = string(r[:200])
	}
	return " AND (positionCaseInsensitiveUTF8(title, ?) > 0 OR positionCaseInsensitiveUTF8(path, ?) > 0)", []any{q, q}
}

// wikiListSQL — SAF: sayfa listesi + sayım sorgusu. preview=false iken içerik
// kolonu HİÇ okunmaz (sabit ”).
func wikiListSQL(q string, preview bool) (list, count string, args []any) {
	fc, fargs := wikiListFilter(q)
	pv := "''"
	if preview {
		pv = "substringUTF8(content, 1, 1000)"
	}
	list = `SELECT project, wiki_name, wiki_id, path, title, url, chunk_count, updated_at, ` + pv + ` AS preview
		FROM wiki_pages FINAL
		WHERE deleted = 0` + fc + `
		ORDER BY project, wiki_name, path
		LIMIT ? OFFSET ?
		SETTINGS max_execution_time = 10`
	count = `SELECT count() FROM wiki_pages FINAL
		WHERE deleted = 0` + fc + `
		LIMIT 1
		SETTINGS max_execution_time = 10`
	return list, count, fargs
}

// WikiListPages — indeksteki sayfalar (sunucu-sayfalı) + süzgece uyan toplam.
func (s *Store) WikiListPages(ctx context.Context, q string, offset, limit int, preview bool) ([]WikiPageRow, uint64, error) {
	if limit <= 0 || limit > WikiListMax {
		limit = WikiListMax
	}
	if offset < 0 {
		offset = 0
	}
	list, count, fargs := wikiListSQL(q, preview)
	var total uint64
	if err := s.conn.QueryRow(ctx, count, fargs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args := append(append([]any{}, fargs...), limit, offset)
	rows, err := s.conn.Query(ctx, list, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]WikiPageRow, 0, limit)
	for rows.Next() {
		var r WikiPageRow
		if err := rows.Scan(&r.Project, &r.Wiki, &r.WikiID, &r.Path, &r.Title, &r.URL, &r.Chunks, &r.UpdatedAt, &r.Preview); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// wikiPurgeTables — v0.10.1124 "İndeksi temizle" (yönetici, nadir): iki wiki
// tablosunun TÜM satırları. Mezar taşı yerine mutasyon, çünkü amaç içeriğin
// diskten gitmesi (canlı moda geçen operatör "içerik Coremetry'de kalmasın"
// diyor); sıradan senkron yolu mutasyonsuz kalır.
var wikiPurgeTables = []string{"wiki_pages", "wiki_chunks"}

// WikiPurge — indeksteki tüm wiki sayfalarını ve parçalarını siler. Küme
// modunda mutasyon rag_chunks emsaliyle mutationTarget'a gider (_local ON CLUSTER).
func (s *Store) WikiPurge(ctx context.Context) error {
	for _, t := range wikiPurgeTables {
		if err := s.conn.Exec(ctx, `ALTER TABLE `+s.mutationTarget(t)+` DELETE WHERE 1 = 1`); err != nil {
			return err
		}
	}
	return nil
}
