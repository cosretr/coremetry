package chstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/wiki"
)

// wiki.go — v0.10.1122 Azure DevOps wiki bilgi deposu (internal/wiki'nin Store'u).
//
// İki state tablosu, ikisi de ReplacingMergeTree(version) + FINAL okuma
// (invariant #4). saved_views'a gitmiyor — rag_chunks'ın aynı savunması:
// içerik verisi + dizi kolonları kendi şeklini ister, kullanıcı view state'i
// değil.
//
//	wiki_pages  ORDER BY (wiki_id, path)             — sayfa + değişim künyesi
//	wiki_chunks ORDER BY (wiki_id, path, chunk_idx)  — başlık-duyarlı parça + jetonlar
//
// SİLME MUTASYONSUZ: kaynakta silinen sayfa / kısalan sayfanın kuyruk
// parçaları `deleted=1` mezar taşı satırıyla (yeni version) yazılır; her okuma
// `deleted = 0` süzer. ALTER DELETE yok — senkron binlerce sayfayı gezerken
// mutation kuyruğu doldurmaz. Mezar taşları 30 gün sonra TTL ile düşer
// (`DELETE WHERE deleted = 1`): o vakte dek RMT birleşmeleri eski canlı
// sürümü çoktan eritmiş olur; canlı satırların TTL'i YOK.
//
// ARAMA: jetonlar Go'da üretilir (wiki.Tokens — Türkçe katlama + teknik
// bileşikler: "svc-orders", "err-1042"), `tokens Array(String)` kolonuna
// yazılır. Aday sorgusu `hasAny(tokens, terimler)` ile süzer (bloom_filter
// atlama indeksi), terim başına frekansı `countEqual` ile SAYAR; BM25 skoru Go'da
// (wiki.RankLexical — saf, tablo-testli). Her okuma WHERE + LIMIT +
// max_execution_time taşır.

const wikiPagesDDL = `
CREATE TABLE IF NOT EXISTS wiki_pages (
    wiki_id      String,
    path         String,
    project      String,
    wiki_name    String,
    title        String,
    url          String DEFAULT '',
    git_path     String DEFAULT '',
    etag         String DEFAULT '',
    content_hash String DEFAULT '',
    content      String CODEC(ZSTD(3)),
    chunk_count  UInt32 DEFAULT 0,
    updated_at   DateTime64(3) DEFAULT now64(3),
    deleted      UInt8 DEFAULT 0,
    version      UInt64
) ENGINE = ReplacingMergeTree(version)
ORDER BY (wiki_id, path)
TTL toDateTime(updated_at) + INTERVAL 30 DAY DELETE WHERE deleted = 1`

const wikiChunksDDL = `
CREATE TABLE IF NOT EXISTS wiki_chunks (
    wiki_id     String,
    path        String,
    chunk_idx   UInt32,
    project     String,
    wiki_name   String,
    title       String,
    url         String DEFAULT '',
    heading     String DEFAULT '',
    text        String CODEC(ZSTD(3)),
    tokens      Array(String),
    head_tokens Array(String),
    embedding   Array(Float32),
    page_hash   String DEFAULT '',
    updated_at  DateTime64(3) DEFAULT now64(3),
    deleted     UInt8 DEFAULT 0,
    version     UInt64,
    INDEX idx_tokens tokens TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = ReplacingMergeTree(version)
ORDER BY (wiki_id, path, chunk_idx)
TTL toDateTime(updated_at) + INTERVAL 30 DAY DELETE WHERE deleted = 1`

// Sorgu şekilleri — testler pinler (wiki_sql_test.go).
const (
	wikiPageIndexSQL = `SELECT wiki_id, path, project, wiki_name, etag, content_hash, chunk_count
		FROM wiki_pages FINAL
		WHERE deleted = 0
		LIMIT 200000
		SETTINGS max_execution_time = 20`

	wikiCountsSQL = `SELECT
		(SELECT count() FROM wiki_pages FINAL WHERE deleted = 0) AS pages,
		(SELECT count() FROM wiki_chunks FINAL WHERE deleted = 0) AS chunks
		LIMIT 1
		SETTINGS max_execution_time = 10`

	wikiGetPageSQL = `SELECT project, wiki_id, wiki_name, path, title, url, git_path, etag, content_hash,
		       content, chunk_count, updated_at
		FROM wiki_pages FINAL
		WHERE deleted = 0 AND project = ? AND (wiki_name = ? OR wiki_id = ?) AND path = ?
		LIMIT 1
		SETTINGS max_execution_time = 5`
)

// wikiProjectClause — opsiyonel proje süzgeci (bağlı argüman).
func wikiProjectClause(project string) (string, []any) {
	if strings.TrimSpace(project) == "" {
		return "", nil
	}
	return " AND project = ?", []any{strings.TrimSpace(project)}
}

// wikiStatsSQL — SAF: terim başına belge frekansı + N + ortalama uzunluk.
func wikiStatsSQL(nTerms int, project string) (string, []any) {
	parts := make([]string, nTerms)
	for i := range parts {
		parts[i] = "countIf(has(tokens, ?))"
	}
	df := "[]"
	if nTerms > 0 {
		df = "[" + strings.Join(parts, ", ") + "]"
	}
	pc, pargs := wikiProjectClause(project)
	q := `SELECT count() AS n, avg(length(tokens)) AS avgdl, ` + df + ` AS df
		FROM wiki_chunks FINAL
		WHERE deleted = 0` + pc + `
		LIMIT 1
		SETTINGS max_execution_time = 5`
	return q, pargs
}

// wikiCandidatesSQL — SAF: hasAny süzgeçli adaylar + terim başına frekans.
// LIMIT'ten ÖNCE eşleşen farklı terim sayısına göre sıralanır: sık bir terim
// yüzlerce parçada geçse bile tüm terimleri taşıyan parça tavanın dışında
// kalmaz (deterministik kuyruk: wiki_id, path, chunk_idx).
//
// FINAL + atlama indeksi: CH 25.x+ varsayılanı use_skip_indexes_if_final=1 +
// use_skip_indexes_if_final_exact_mode=1 (doğru sonuç) — indeks orada kullanılır.
// 24.x'te varsayılan 0 ve exact_mode YOK; orada 1'e zorlamak yeni sürüm
// (mezar taşı) satırını taşımayan granülü atlayıp silinmiş sayfayı geri
// getirebilirdi. Bu yüzden ayar SORGUDA verilmez: eski sürümde FINAL tablo
// taraması (küçük state tablosu, 5 sn tavanlı), yeni sürümde indeksli ve doğru.
func wikiCandidatesSQL(nTerms int, project string) (string, []any) {
	tf := make([]string, nTerms)
	htf := make([]string, nTerms)
	for i := range tf {
		tf[i] = "countEqual(tokens, ?)"
		htf[i] = "countEqual(head_tokens, ?)"
	}
	pc, pargs := wikiProjectClause(project)
	q := `SELECT project, wiki_id, wiki_name, path, title, url, heading, chunk_idx, text,
		       [` + strings.Join(tf, ", ") + `] AS tf,
		       [` + strings.Join(htf, ", ") + `] AS htf,
		       length(tokens) AS dl
		FROM wiki_chunks FINAL
		WHERE deleted = 0 AND hasAny(tokens, ?)` + pc + `
		ORDER BY length(arrayIntersect(tokens, ?)) DESC, wiki_id, path, chunk_idx
		LIMIT ?
		SETTINGS max_execution_time = 5`
	return q, pargs
}

// wikiSemanticSQL — SAF: kosinüs top-k (embedding'li parçalar).
func wikiSemanticSQL(project string) (string, []any) {
	pc, pargs := wikiProjectClause(project)
	q := `SELECT project, wiki_id, wiki_name, path, title, url, heading, chunk_idx, text,
		       1 - cosineDistance(embedding, ?) AS cos
		FROM wiki_chunks FINAL
		WHERE deleted = 0 AND length(embedding) = length(?)` + pc + `
		ORDER BY cos DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`
	return q, pargs
}

const wikiPageInsert = `INSERT INTO wiki_pages
	(wiki_id, path, project, wiki_name, title, url, git_path, etag, content_hash, content, chunk_count, updated_at, deleted, version)`

const wikiChunkInsert = `INSERT INTO wiki_chunks
	(wiki_id, path, chunk_idx, project, wiki_name, title, url, heading, text, tokens, head_tokens, embedding, page_hash, updated_at, deleted, version)`

// WikiPageIndex — değişim tespiti için tüm canlı sayfaların künyesi.
func (s *Store) WikiPageIndex(ctx context.Context) ([]wiki.PageMeta, error) {
	rows, err := s.conn.Query(ctx, wikiPageIndexSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []wiki.PageMeta
	for rows.Next() {
		var m wiki.PageMeta
		if err := rows.Scan(&m.WikiID, &m.Path, &m.Project, &m.WikiName, &m.Version, &m.Hash, &m.Chunks); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpsertWikiPage — sayfa satırı + parçalar + (kısalan sayfada) kuyruk mezar
// taşları, tek version damgasıyla.
func (s *Store) UpsertWikiPage(ctx context.Context, p wiki.PageRecord, chunks []wiki.ChunkRecord, oldChunks uint32) error {
	version := uint64(time.Now().UnixNano())
	at := p.UpdatedAt
	if at.IsZero() {
		at = time.Now()
	}
	ictx := asyncInsertCtx(ctx)
	pb, err := s.conn.PrepareBatch(ictx, wikiPageInsert)
	if err != nil {
		return err
	}
	if err := pb.Append(p.WikiID, p.Path, p.Project, p.WikiName, p.Title, p.URL, p.GitPath, p.Version, p.Hash,
		p.Content, uint32(len(chunks)), at.UTC(), uint8(0), version); err != nil {
		return err
	}
	if err := pb.Send(); err != nil {
		return err
	}
	cb, err := s.conn.PrepareBatch(ictx, wikiChunkInsert)
	if err != nil {
		return err
	}
	for _, c := range chunks {
		emb := c.Embedding
		if emb == nil {
			emb = []float32{}
		}
		if err := cb.Append(p.WikiID, p.Path, c.Idx, p.Project, p.WikiName, p.Title, p.URL, c.Heading, c.Text,
			nonNilStrings(c.Tokens), nonNilStrings(c.HeadTokens), emb, p.Hash, at.UTC(), uint8(0), version); err != nil {
			return err
		}
	}
	for i := uint32(len(chunks)); i < oldChunks; i++ {
		if err := cb.Append(p.WikiID, p.Path, i, p.Project, p.WikiName, "", "", "", "",
			[]string{}, []string{}, []float32{}, "", at.UTC(), uint8(1), version); err != nil {
			return err
		}
	}
	return cb.Send()
}

// DeleteWikiPage — sayfa + parçaları için mezar taşı satırları.
func (s *Store) DeleteWikiPage(ctx context.Context, m wiki.PageMeta) error {
	version := uint64(time.Now().UnixNano())
	now := time.Now().UTC()
	ictx := asyncInsertCtx(ctx)
	pb, err := s.conn.PrepareBatch(ictx, wikiPageInsert)
	if err != nil {
		return err
	}
	if err := pb.Append(m.WikiID, m.Path, m.Project, m.WikiName, "", "", "", "", "", "", uint32(0), now, uint8(1), version); err != nil {
		return err
	}
	if err := pb.Send(); err != nil {
		return err
	}
	if m.Chunks == 0 {
		return nil
	}
	cb, err := s.conn.PrepareBatch(ictx, wikiChunkInsert)
	if err != nil {
		return err
	}
	for i := uint32(0); i < m.Chunks; i++ {
		if err := cb.Append(m.WikiID, m.Path, i, m.Project, m.WikiName, "", "", "", "",
			[]string{}, []string{}, []float32{}, "", now, uint8(1), version); err != nil {
			return err
		}
	}
	return cb.Send()
}

// WikiTermStats — korpus istatistiği (terim sırası korunur).
func (s *Store) WikiTermStats(ctx context.Context, terms []string, project string) (wiki.Stats, error) {
	q, pargs := wikiStatsSQL(len(terms), project)
	args := make([]any, 0, len(terms)+len(pargs))
	for _, t := range terms {
		args = append(args, t)
	}
	args = append(args, pargs...)
	var st wiki.Stats
	var avg float64
	if err := s.conn.QueryRow(ctx, q, args...).Scan(&st.N, &avg, &st.DF); err != nil {
		return wiki.Stats{}, err
	}
	if avg != avg { // NaN (boş tablo)
		avg = 0
	}
	st.AvgDL = avg
	return st, nil
}

// WikiCandidates — terimlerden en az birini taşıyan parçalar (tavanlı).
func (s *Store) WikiCandidates(ctx context.Context, terms []string, project string, limit int) ([]wiki.Candidate, error) {
	if len(terms) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	q, pargs := wikiCandidatesSQL(len(terms), project)
	args := make([]any, 0, 2*len(terms)+3)
	for _, t := range terms {
		args = append(args, t)
	}
	for _, t := range terms {
		args = append(args, t)
	}
	args = append(args, terms)
	args = append(args, pargs...)
	args = append(args, terms) // ORDER BY: eşleşen FARKLI terim sayısı (LIMIT'ten önce)
	args = append(args, limit)
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []wiki.Candidate
	for rows.Next() {
		var c wiki.Candidate
		var tf, htf []uint64
		var dl uint64
		if err := rows.Scan(&c.Project, &c.WikiID, &c.WikiName, &c.Path, &c.Title, &c.URL, &c.Heading,
			&c.Idx, &c.Text, &tf, &htf, &dl); err != nil {
			return nil, err
		}
		c.TF, c.HTF, c.DL = toU32(tf), toU32(htf), uint32(dl)
		out = append(out, c)
	}
	return out, rows.Err()
}

// WikiSemantic — embedding'li parçalarda kosinüs top-k.
func (s *Store) WikiSemantic(ctx context.Context, emb []float32, project string, k int) ([]wiki.SemHit, error) {
	if len(emb) == 0 {
		return nil, fmt.Errorf("empty query embedding")
	}
	if k < 1 || k > 50 {
		k = 20
	}
	q, pargs := wikiSemanticSQL(project)
	args := []any{emb, emb}
	args = append(args, pargs...)
	args = append(args, k)
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []wiki.SemHit
	for rows.Next() {
		var h wiki.SemHit
		if err := rows.Scan(&h.Project, &h.WikiID, &h.WikiName, &h.Path, &h.Title, &h.URL, &h.Heading,
			&h.Idx, &h.Text, &h.Cos); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// GetWikiPage — tek sayfa (wiki adı ya da id'si). Yoksa nil, nil.
func (s *Store) GetWikiPage(ctx context.Context, project, wikiRef, path string) (*wiki.PageRecord, error) {
	rows, err := s.conn.Query(ctx, wikiGetPageSQL, project, wikiRef, wikiRef, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var p wiki.PageRecord
	if err := rows.Scan(&p.Project, &p.WikiID, &p.WikiName, &p.Path, &p.Title, &p.URL, &p.GitPath,
		&p.Version, &p.Hash, &p.Content, &p.Chunks, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// WikiCounts — canlı sayfa ve parça sayısı (durum kartı).
func (s *Store) WikiCounts(ctx context.Context) (pages, chunks uint64, err error) {
	err = s.conn.QueryRow(ctx, wikiCountsSQL).Scan(&pages, &chunks)
	return pages, chunks, err
}

func toU32(in []uint64) []uint32 {
	out := make([]uint32, len(in))
	for i, v := range in {
		out[i] = uint32(v)
	}
	return out
}
