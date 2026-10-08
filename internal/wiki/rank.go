package wiki

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// rank.go — skor (SAF, tablo-testli).
//
// LEXICAL: BM25 (k1=1.2, b=0.75) + başlık bonusu, ClickHouse'un döndürdüğü
// terim frekanslarından Go'da hesaplanır (SQL yalnız sayar: countEqual;
// şekil chstore/wiki.go'da pinli). Mutlak eşik için BM25 tek başına uygun
// değil (ölçeği korpusa bağlı), o yüzden skor iki bileşenli:
//
//	coverage = Σ idf(eşleşen terim) / Σ idf(tüm terimler)       ∈ [0,1]
//	lexical  = coverage × (0.6 + 0.4 × bm25 / max_bm25)          ∈ [0,1]
//
// coverage "sorunun ne kadarı bu parçada" der (nadir teknik terim — svc-orders,
// ERR-1042 — yüksek idf'iyle baskın); bm25 aynı coverage'taki parçaları
// sıralar. Eşikler (RAG kademesi, canlı arama tetiği) coverage-ağırlıklı bu
// mutlak skora uygulanır.
//
// HİBRİT: embedding varsa semantik kosinüs ile harmanlanır:
//
//	score = wLex × lexical + (1-wLex) × max(0, cos)
//
// Embedding yoksa score = lexical (her şey embedding'siz çalışır).

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	// headWeight — başlık/başlık-yolu eşleşmesinin terim frekansına eklenen ağırlığı.
	headWeight = 1.5
	// DefaultLexicalWeight — hibrit harmanda lexical payı.
	DefaultLexicalWeight = 0.6
)

// ChunkRef — bir parçanın kimliği + gösterim alanları.
type ChunkRef struct {
	Project  string `json:"project"`
	WikiID   string `json:"wikiId"`
	WikiName string `json:"wiki"`
	Path     string `json:"path"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Heading  string `json:"heading,omitempty"`
	Idx      uint32 `json:"chunk"`
	Text     string `json:"-"`
}

// Key — parça kimliği (wiki/yol/sıra).
func (c ChunkRef) Key() string {
	return c.WikiID + "\x00" + c.Path + "\x00" + strconv.FormatUint(uint64(c.Idx), 10)
}

// PageKey — sayfa kimliği.
func (c ChunkRef) PageKey() string { return c.WikiID + "\x00" + c.Path }

// Candidate — CH'den gelen aday: terim başına gövde ve başlık frekansı.
type Candidate struct {
	ChunkRef
	TF  []uint32 // terms ile aynı sıra
	HTF []uint32
	DL  uint32 // jeton sayısı
}

// Stats — korpus istatistikleri (aynı terim sırası).
type Stats struct {
	N     uint64
	AvgDL float64
	DF    []uint64
}

// SemHit — semantik aday.
type SemHit struct {
	ChunkRef
	Cos float64
}

// Hit — sıralanmış sonuç.
type Hit struct {
	ChunkRef
	Score    float64 `json:"score"`
	Lexical  float64 `json:"lexical"`
	Semantic float64 `json:"semantic,omitempty"`
	Live     bool    `json:"live,omitempty"`
	// Coverage — canlı isabette sorgu terimlerinin parçadaki KÖK kapsamı
	// (stemCoverage, 0..1; v0.10.1126). Canlı sıra skoru tek başına kanıt
	// değil (AND sorgusunun ilk sonucu kapsamdan bağımsız liveTopScore alır):
	// zayıf işaretli sohbet kademeleri bunu ayrıca ister. Yerel isabette 0.
	Coverage float64 `json:"-"`
	// LocalScore — canlıyla birleşen yerel isabetin YEREL skoru (mergeHits;
	// birleşmeyen yerel isabette Score zaten yereldir, Live=false).
	LocalScore float64 `json:"-"`
}

// EvidencedAt — v0.10.1126: isabet floor'u KANITLA mı geçiyor: yerel skor
// ≥ floor, ya da canlı isabet skor ≥ floor VE kök kapsamı ≥ floor. Canlı
// sıra skoru tek başına yetmez.
func (h Hit) EvidencedAt(floor float64) bool {
	switch {
	case !h.Live:
		return h.Score >= floor
	case h.LocalScore >= floor:
		return true
	}
	return h.Score >= floor && h.Coverage >= floor
}

// idf — BM25+ biçimi (her zaman >0). df=0 (korpusta hiç yok) terim en
// yüksek idf'i alır: sorunun bilinmeyen kavramı kapsamayı haklı olarak düşürür.
func idf(n, df uint64) float64 {
	N := float64(n)
	if N < 1 {
		N = 1
	}
	return math.Log(1 + (N-float64(df)+0.5)/(float64(df)+0.5))
}

// RankLexical — adayları skorlar ve azalan sıralar; sıfır kapsamlılar düşer.
func RankLexical(cands []Candidate, st Stats, terms []string) []Hit {
	if len(terms) == 0 || len(cands) == 0 {
		return nil
	}
	idfs := make([]float64, len(terms))
	var idfSum float64
	for i := range terms {
		var df uint64
		if i < len(st.DF) {
			df = st.DF[i]
		}
		idfs[i] = idf(st.N, df)
		idfSum += idfs[i]
	}
	avg := st.AvgDL
	if avg <= 0 {
		avg = 1
	}
	type scored struct {
		c       Candidate
		bm, cov float64
	}
	rows := make([]scored, 0, len(cands))
	maxBM := 0.0
	for _, c := range cands {
		var bm, matched float64
		dl := float64(c.DL)
		for i := range terms {
			var tf, htf float64
			if i < len(c.TF) {
				tf = float64(c.TF[i])
			}
			if i < len(c.HTF) {
				htf = float64(c.HTF[i])
			}
			if tf == 0 && htf == 0 {
				continue
			}
			matched += idfs[i]
			f := tf + headWeight*math.Min(htf, 2)
			bm += idfs[i] * (f * (bm25K1 + 1)) / (f + bm25K1*(1-bm25B+bm25B*dl/avg))
		}
		if matched == 0 {
			continue
		}
		cov := 0.0
		if idfSum > 0 {
			cov = matched / idfSum
		}
		if bm > maxBM {
			maxBM = bm
		}
		rows = append(rows, scored{c: c, bm: bm, cov: cov})
	}
	out := make([]Hit, 0, len(rows))
	for _, r := range rows {
		rel := 0.0
		if maxBM > 0 {
			rel = r.bm / maxBM
		}
		lex := r.cov * (0.6 + 0.4*rel)
		out = append(out, Hit{ChunkRef: r.c.ChunkRef, Score: lex, Lexical: lex})
	}
	sortHits(out)
	return out
}

// sortHits — azalan skor; eşitlikte deterministik (yol, sıra).
func sortHits(h []Hit) {
	sort.SliceStable(h, func(i, j int) bool {
		if h[i].Score != h[j].Score {
			return h[i].Score > h[j].Score
		}
		if h[i].Path != h[j].Path {
			return h[i].Path < h[j].Path
		}
		return h[i].Idx < h[j].Idx
	})
}

// Blend — lexical ve semantik sonuçları birleştirir. sem boşsa lex AYNEN
// döner (embedding'siz yol bayt bayt lexical). wLex ∈ (0,1].
func Blend(lex []Hit, sem []SemHit, wLex float64) []Hit {
	if len(sem) == 0 {
		return lex
	}
	if wLex <= 0 || wLex > 1 {
		wLex = DefaultLexicalWeight
	}
	by := map[string]*Hit{}
	order := []string{}
	for _, h := range lex {
		hh := h
		by[h.Key()] = &hh
		order = append(order, h.Key())
	}
	for _, s := range sem {
		cos := s.Cos
		if cos < 0 {
			cos = 0
		}
		if cos > 1 {
			cos = 1
		}
		k := s.Key()
		if h, ok := by[k]; ok {
			h.Semantic = cos
			continue
		}
		by[k] = &Hit{ChunkRef: s.ChunkRef, Semantic: cos}
		order = append(order, k)
	}
	out := make([]Hit, 0, len(order))
	for _, k := range order {
		h := by[k]
		h.Score = wLex*h.Lexical + (1-wLex)*h.Semantic
		out = append(out, *h)
	}
	sortHits(out)
	return out
}

// DedupePages — sayfa başına en çok perPage parça (en iyileri), sıra korunur.
func DedupePages(h []Hit, perPage int) []Hit {
	if perPage <= 0 {
		perPage = 1
	}
	cnt := map[string]int{}
	out := make([]Hit, 0, len(h))
	for _, x := range h {
		k := x.PageKey()
		if cnt[k] >= perPage {
			continue
		}
		cnt[k]++
		out = append(out, x)
	}
	return out
}

// Snippet — ilk eşleşen terimin çevresinden ≤max rune'luk kesit (katlanmış
// eşleşme, özgün metin döner).
func Snippet(text string, terms []string, max int) string {
	r := []rune(text)
	if len(r) <= max {
		return strings.TrimSpace(text)
	}
	folded := []rune(Fold(text))
	start := 0
	if len(folded) == len(r) { // katlama rune sayısını korur (tek-rune eşlemeler)
		fs := string(folded)
		for _, t := range terms {
			if i := strings.Index(fs, t); i >= 0 {
				start = len([]rune(fs[:i])) - max/3
				break
			}
		}
	}
	if start < 0 {
		start = 0
	}
	end := start + max
	if end > len(r) {
		end = len(r)
		start = end - max
		if start < 0 {
			start = 0
		}
	}
	out := strings.TrimSpace(string(r[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}
