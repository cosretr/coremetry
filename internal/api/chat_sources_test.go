package api

// v0.10.1127 — operatör: tek cevabın altında birden çok özdeş "Kaynak §1"
// çipi. Kaynak listesi parça başınaydı; artık hedef (sayfa bağlantısı ya da
// doküman) başına tek çip, "Kaynak N" etiketiyle. Adlar sentetik.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/wiki"
)

func TestDedupeChatSourcesOnePerTarget(t *testing.T) {
	const a = "https://devops.example.test/DefaultCollection/Platform/_wiki/wikis/Platform.wiki?pagePath=%2FSbox"
	const b = "https://devops.example.test/DefaultCollection/Platform/_wiki/wikis/Platform.wiki?pagePath=%2FRedis"
	in := []chatSource{
		{Doc: "Wiki · Sbox", Ref: a, Chunk: 1, Score: 0.8},
		{Doc: "Wiki · Sbox", Ref: a, Chunk: 2, Score: 0.9}, // aynı sayfa, başka parça
		{Doc: "Wiki · Redis", Ref: b, Chunk: 1, Score: 0.5},
		{Doc: "Wiki · Sbox", Ref: strings.ToUpper(a[:8]) + a[8:], Chunk: 1, Score: 0.4}, // harf farkı
		{Doc: "kanal_kodlari.pdf", Chunk: 1, Score: 0.6},                                // bağlantısız doküman
		{Doc: "kanal_kodlari.pdf", Chunk: 3, Score: 0.3},
	}
	got := dedupeChatSources(in)
	var labels, refs []string
	for _, s := range got {
		labels = append(labels, s.Label)
		refs = append(refs, s.Ref+"|"+s.Doc)
	}
	if !reflect.DeepEqual(labels, []string{"Kaynak 1", "Kaynak 2", "Kaynak 3"}) {
		t.Fatalf("hedef başına tek çip, sıralı etiket: %q", labels)
	}
	if !reflect.DeepEqual(refs, []string{a + "|Wiki · Sbox", b + "|Wiki · Redis", "|kanal_kodlari.pdf"}) {
		t.Errorf("ilk görülme sırası korunmalı: %q", refs)
	}
	if got[0].Score != 0.9 || got[0].Chunk != 1 {
		t.Errorf("birleşen kaynak en büyük skoru taşımalı: %+v", got[0])
	}
	if !reflect.DeepEqual(got[0].Sections, []uint32{1, 2}) || !reflect.DeepEqual(got[2].Sections, []uint32{1, 3}) {
		t.Errorf("birleşen bölümler ipucu için taşınmalı (F5): %v / %v", got[0].Sections, got[2].Sections)
	}
	raw, _ := json.Marshal(dedupeChatSources(nil))
	if string(raw) != "[]" {
		t.Errorf("boş kaynak listesi JSON'da [] olmalı: %s", raw)
	}
}

// İnceleme F6: bağlam bloğunun [n] numarası çipin "Kaynak n"iyle aynı —
// aynı sayfanın parçaları aynı numara, sıra ilk görülme.
func TestContextNumbersMatchSourceChips(t *testing.T) {
	const a = "https://devops.example.test/DefaultCollection/Platform/_wiki/wikis/Platform.wiki?pagePath=%2FSbox"
	const b = "https://devops.example.test/DefaultCollection/Platform/_wiki/wikis/Platform.wiki?pagePath=%2FRedis"
	h := []wiki.Hit{
		{ChunkRef: wiki.ChunkRef{WikiID: "w", Path: "/Sbox", Title: "Sbox", URL: a, Idx: 0, Heading: "Sunucular", Text: "t1"}, Score: 0.8},
		{ChunkRef: wiki.ChunkRef{WikiID: "w", Path: "/Redis", Title: "Redis", URL: b, Idx: 0, Heading: "Kurulum", Text: "t2"}, Score: 0.7},
		{ChunkRef: wiki.ChunkRef{WikiID: "w", Path: "/Sbox", Title: "Sbox", URL: a, Idx: 2, Heading: "IP", Text: "t3"}, Score: 0.65},
	}
	// RAG yolu: önce bir doküman parçası, sonra wiki isabetleri.
	src := []chatSource{{Doc: "kanal_kodlari.pdf", Chunk: 1}}
	for _, x := range h {
		src = append(src, wikiHitSource(x))
	}
	chips := dedupeChatSources(src)
	// v0.10.1136: sayfa başına TEK blok ("[n] Wiki · başlık"), aynı sayfanın parçaları o blokta.
	ctx, _ := buildWikiMultiContext(wikiGroupPages(h, 0, wikiMaxPages), nil, wikiBudgetDefault, false, numberSources(src))
	for _, want := range []string{"[2] Wiki · Sbox\n<wiki_data>\nSayfa: Sbox\n## Sunucular\nt1", "## IP\nt3", "[3] Wiki · Redis\n<wiki_data>\nSayfa: Redis\n## Kurulum\nt2"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("bağlam numarası çiple aynı olmalı: %q eksik\n%s", want, ctx)
		}
	}
	if chips[1].Label != "Kaynak 2" || chips[1].Ref != a || chips[2].Label != "Kaynak 3" || chips[2].Ref != b {
		t.Errorf("çipler: %+v", chips)
	}
}
