package api

import (
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/wiki"
)

// chat_sources.go — v0.10.1127: sohbet cevabının kaynak çipleri.
//
// Operatör: tek cevabın altında birden çok özdeş "Kaynak §1" çipi. Kök neden:
// RAG / wiki kademesi kaynak listesini PARÇA başına kuruyordu (sayfa başına
// 3 parçaya dek, her biri ayrı çip) ve çip yalnız parça numarasını
// gösteriyordu (doküman adı v0.9.515'ten beri ipucunda) — farklı sayfaların
// ilk parçaları da, aynı sayfanın parçaları da operatöre aynı "Kaynak §1"
// olarak görünüyordu. Artık çip HEDEF başına tek (bağlantı; yoksa doküman
// adı), sıra ilk görülme (skor sırası), etiket "Kaynak 1", "Kaynak 2" …

// chatSource — cevap yükündeki kaynak (frontend RagSource).
type chatSource struct {
	Doc   string  `json:"doc"`
	Ref   string  `json:"ref,omitempty"`
	Chunk uint32  `json:"chunk"`
	Score float64 `json:"score"`
	Label string  `json:"label"`
	// Sections — birleşen parçaların bölüm numaraları (ilk görülme sırası).
	Sections []uint32 `json:"sections,omitempty"`
}

func containsU32(xs []uint32, v uint32) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// wikiHitSource — SAF: wiki isabetinin kaynak kaydı (çip + bağlam numarası
// AYNI anahtardan).
func wikiHitSource(h wiki.Hit) chatSource {
	return chatSource{Doc: "Wiki · " + h.Title, Ref: h.URL, Chunk: h.Idx + 1, Score: h.Score}
}

// wikiHitSources — SAF: isabetlerin kaynak kayıtları (sıra korunur).
func wikiHitSources(h []wiki.Hit) []chatSource {
	out := make([]chatSource, 0, len(h))
	for _, x := range h {
		out = append(out, wikiHitSource(x))
	}
	return out
}

// sourceNumbers — v0.10.1127 (inceleme F6): bağlam bloklarının [n] numarası
// = çipin "Kaynak n" numarası. Numaralar dedupeChatSources ile AYNI sırada
// (ilk görülme) ve AYNI anahtarla (sourceKey) verilir; model "[2]" diye atıf
// yaptığında operatör "Kaynak 2" çipini görür. Aynı sayfanın parçaları aynı
// numarayı taşır.
type sourceNumbers struct{ at map[string]int }

// numberSources — SAF: kaynak listesinden numara tablosu.
func numberSources(in []chatSource) sourceNumbers {
	n := sourceNumbers{at: map[string]int{}}
	for _, s := range in {
		n.of(s)
	}
	return n
}

// of — kaynağın numarası; tabloda yoksa sona eklenir.
func (n sourceNumbers) of(s chatSource) int {
	k := sourceKey(s)
	if i, ok := n.at[k]; ok {
		return i
	}
	n.at[k] = len(n.at) + 1
	return n.at[k]
}

// sourceKey — SAF: çipin hedefi (bağlantı; yoksa doküman adı), harf-duyarsız.
func sourceKey(s chatSource) string {
	if r := strings.TrimSpace(s.Ref); r != "" {
		return "ref:" + strings.ToLower(r)
	}
	return "doc:" + strings.ToLower(strings.TrimSpace(s.Doc))
}

// dedupeChatSources — SAF: hedef başına tek kaynak (ilk görülen; skor en
// büyüğü taşınır; bölüm numaraları Sections'ta birikir — ipucu "§1, §3"),
// "Kaynak N" etiketiyle. Boş girdi boş dilim (nil değil — JSON'da [] kalır).
func dedupeChatSources(in []chatSource) []chatSource {
	out := make([]chatSource, 0, len(in))
	at := map[string]int{}
	for _, s := range in {
		k := sourceKey(s)
		if i, ok := at[k]; ok {
			if s.Score > out[i].Score {
				out[i].Score = s.Score
			}
			if !containsU32(out[i].Sections, s.Chunk) {
				out[i].Sections = append(out[i].Sections, s.Chunk)
			}
			continue
		}
		at[k] = len(out)
		s.Sections = []uint32{s.Chunk}
		out = append(out, s)
	}
	for i := range out {
		out[i].Label = fmt.Sprintf("Kaynak %d", i+1)
	}
	return out
}
