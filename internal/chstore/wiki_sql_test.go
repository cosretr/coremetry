package chstore

// v0.10.1122 — wiki deposunun SQL şekli pinli: her okuma FINAL + deleted=0 +
// LIMIT + max_execution_time; arama hasAny (bloom_filter) + countEqual; DDL
// ReplacingMergeTree(version), mutasyonsuz mezar taşı kolonu.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/wiki"
)

func assertBounded(t *testing.T, name, q string) {
	t.Helper()
	for _, want := range []string{"FINAL", "deleted = 0", "LIMIT", "max_execution_time"} {
		if !strings.Contains(q, want) {
			t.Errorf("%s: %q eksik:\n%s", name, want, q)
		}
	}
}

func TestWikiSQLShapesBounded(t *testing.T) {
	assertBounded(t, "index", wikiPageIndexSQL)
	assertBounded(t, "counts", wikiCountsSQL)
	assertBounded(t, "page", wikiGetPageSQL)
	// v0.10.1127 — yeniden jetonlamanın parça okuması: tek sayfa, jetonsuz.
	assertBounded(t, "page-chunks", wikiPageChunksSQL)
	// v0.10.1136 — sohbet okuması: tek sayfa, embedding/jeton YOK, FINAL (RMT + mezar taşı).
	assertBounded(t, "page-chunk-texts", wikiPageChunkTextsSQL)
	if !strings.Contains(wikiPageChunkTextsSQL, "wiki_id = ? AND path = ?") || !strings.Contains(wikiPageChunkTextsSQL, "FINAL") ||
		!strings.Contains(wikiPageChunkTextsSQL, "deleted = 0") || strings.Contains(wikiPageChunkTextsSQL, "embedding") ||
		strings.Contains(wikiPageChunkTextsSQL, "tokens") {
		t.Errorf("page-chunk-texts şekli: %s", wikiPageChunkTextsSQL)
	}
	if !strings.Contains(wikiPageChunksSQL, "wiki_id = ? AND path = ?") || strings.Contains(wikiPageChunksSQL, "tokens") {
		t.Errorf("page-chunks şekli: %s", wikiPageChunksSQL)
	}

	q, args := wikiStatsSQL(3, "")
	assertBounded(t, "stats", q)
	if strings.Count(q, "countIf(has(tokens, ?))") != 3 || len(args) != 0 || strings.Contains(q, "project = ?") {
		t.Errorf("stats şekli: %s %v", q, args)
	}
	q, args = wikiStatsSQL(2, "Platform")
	if !strings.Contains(q, "AND project = ?") || len(args) != 1 || args[0] != "Platform" {
		t.Errorf("proje süzgeci bağlı argüman olmalı: %s %v", q, args)
	}

	q, args = wikiCandidatesSQL(2, 1, "Payments")
	assertBounded(t, "candidates", q)
	for _, want := range []string{"hasAny(tokens, ?)", "countEqual(tokens, ?), countEqual(tokens, ?)",
		"countEqual(head_tokens, ?), countEqual(head_tokens, ?)", "length(tokens) AS dl", "LIMIT ?"} {
		if !strings.Contains(q, want) {
			t.Errorf("candidates: %q eksik:\n%s", want, q)
		}
	}
	// v0.10.1127 (F3): öncelik özgün terim başına idf ağırlıklı kapsama.
	ord := strings.Index(q, "ORDER BY (? * hasAny(tokens, ?) + ? * has(tokens, ?)) DESC")
	if ord < 0 || ord > strings.Index(q, "LIMIT ?") || ord < strings.Index(q, "project = ?") {
		t.Errorf("adaylar LIMIT'ten ÖNCE terim kapsamasına göre sıralanmalı:\n%s", q)
	}
	if strings.Contains(q, "arrayIntersect") {
		t.Errorf("farklı-jeton sayısı sıralaması kalmamalı (kök biçimleri tanımlayıcıyı iter):\n%s", q)
	}
	if len(args) != 1 || strings.Index(q, "project = ?") > strings.Index(q, "LIMIT ?") {
		t.Errorf("argüman sırası (terimler, terimler, dizi, proje, öncelik, limit): %s", q)
	}
	q12, _ := wikiCandidatesSQL(36, 12, "")
	if strings.Count(q12, "?") != 36*2+1+12*4+1 {
		t.Errorf("tavanlı şekil: %d yer tutucu", strings.Count(q12, "?"))
	}
	// Argümanlar yer tutucularla birebir (proje süzgeçli).
	cq := wiki.CandidateQuery{Tokens: []string{"wsbxakfp01", "sunucusu", "sunucu"},
		Groups: [][]string{{"wsbxakfp01"}, {"sunucusu", "sunucu"}}, Weights: []float64{2.5, 0.3}}
	q, pargs := wikiCandidatesSQL(len(cq.Tokens), len(cq.Groups), "Platform")
	all := wikiCandidateArgs(cq, pargs, 300)
	if strings.Count(q, "?") != len(all) || all[len(all)-1] != 300 || all[7] != "Platform" || all[8] != 2.5 {
		t.Errorf("argüman/yer tutucu uyuşmazlığı: %d ? / %d arg: %v", strings.Count(q, "?"), len(all), all)
	}

	q, _ = wikiSemanticSQL("")
	assertBounded(t, "semantic", q)
	if !strings.Contains(q, "cosineDistance(embedding, ?)") || !strings.Contains(q, "length(embedding) = length(?)") {
		t.Errorf("semantic: %s", q)
	}
}

func TestWikiDDLShape(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	for name, ddl := range map[string]string{"pages": wikiPagesDDL, "chunks": wikiChunksDDL} {
		for _, want := range []string{"ReplacingMergeTree(version)", "deleted UInt8 DEFAULT 0", "version UInt64"} {
			if !strings.Contains(norm(ddl), want) {
				t.Errorf("%s DDL: %q eksik", name, want)
			}
		}
		if strings.Contains(ddl, "LowCardinality") {
			t.Errorf("%s: serbest metin/kimlik kolonu LowCardinality olmamalı (C2–C3)", name)
		}
	}
	if !strings.Contains(norm(wikiChunksDDL), "tokens TYPE bloom_filter") || !strings.Contains(wikiChunksDDL, "ORDER BY (wiki_id, path, chunk_idx)") {
		t.Errorf("chunks DDL atlama indeksi / sıralama anahtarı: %s", wikiChunksDDL)
	}
	for name, ddl := range map[string]string{"pages": wikiPagesDDL, "chunks": wikiChunksDDL} {
		if !strings.Contains(ddl, "TTL toDateTime(updated_at) + INTERVAL 30 DAY DELETE WHERE deleted = 1") {
			t.Errorf("%s: mezar taşı TTL'i (30 gün, yalnız deleted=1) eksik", name)
		}
	}
	if q, _ := wikiCandidatesSQL(1, 1, ""); strings.Contains(q, "use_skip_indexes_if_final") {
		t.Error("use_skip_indexes_if_final sorguda zorlanmamalı (24.x'te exact_mode yok — silinen sayfa geri gelebilir)")
	}
	if !strings.Contains(wikiPagesDDL, "ORDER BY (wiki_id, path)") {
		t.Error("pages sıralama anahtarı")
	}
}

func TestWikiTablesProtectedFromPurge(t *testing.T) {
	keep := map[string]bool{}
	for _, n := range configPreserveTables {
		keep[n] = true
	}
	for _, n := range telemetryPurgeTables {
		if n == "wiki_pages" || n == "wiki_chunks" {
			t.Errorf("%s purge listesinde olmamalı", n)
		}
	}
	if !keep["wiki_pages"] || !keep["wiki_chunks"] {
		t.Error("wiki tabloları korunan listede olmalı")
	}
}
