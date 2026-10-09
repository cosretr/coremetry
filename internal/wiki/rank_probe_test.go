package wiki

// v0.10.1143 — RankLexical yerel isabete idf-ağırlıklı terim kapsamını
// (TermCoverage) ve başlık/başlık yolu eşleşmesini (HeadMatch) yazar; sohbetin
// içerik yoklaması (api/chat_wiki_probe.go) bunlara bakar. Skor değişmez.

import (
	"math"
	"testing"
)

func TestRankLexicalCoverageAndHeadMatch(t *testing.T) {
	terms := []string{"cache", "refresh"}
	toks := ExpandTerms(terms)
	cand := func(path string, tf, htf map[string]uint32) Candidate {
		c := Candidate{ChunkRef: ChunkRef{WikiID: "w1", Path: path}, TF: make([]uint32, len(toks)), HTF: make([]uint32, len(toks)), DL: 20}
		for i, tk := range toks {
			c.TF[i], c.HTF[i] = tf[tk], htf[tk]
		}
		return c
	}
	st := Stats{N: 10, AvgDL: 20, DF: make([]uint64, len(toks))}
	for i := range st.DF {
		st.DF[i] = 2
	}
	hits := RankLexical([]Candidate{
		cand("/a", map[string]uint32{"cache": 2, "refresh": 1}, map[string]uint32{"cache": 1}),
		cand("/b", map[string]uint32{"cache": 3}, nil),
	}, st, terms)
	by := map[string]Hit{}
	for _, h := range hits {
		by[h.Path] = h
	}
	cases := []struct {
		path string
		cov  float64
		head bool
	}{
		{"/a", 1, true},
		{"/b", 0.5, false},
	}
	for _, c := range cases {
		h, ok := by[c.path]
		if !ok {
			t.Fatalf("%s isabeti yok", c.path)
		}
		if math.Abs(h.TermCoverage-c.cov) > 1e-9 || h.HeadMatch != c.head {
			t.Errorf("%s: cov=%v head=%v, want %v %v", c.path, h.TermCoverage, h.HeadMatch, c.cov, c.head)
		}
		if h.Score > h.TermCoverage+1e-9 {
			t.Errorf("%s: skor kapsamı aşamaz (%v > %v)", c.path, h.Score, h.TermCoverage)
		}
	}
}
