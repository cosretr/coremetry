package wiki

// v0.10.1122 — Markdown başlık-duyarlı parçalayıcı + BM25/kapsama skoru +
// hibrit harman (SAF).

import (
	"math"
	"strings"
	"testing"
)

const runbookMD = `[[_TOC_]]
Bu sayfa svc-orders servisinin işletim kılavuzudur ve nöbetçi ekip için hazırlanmıştır.

# Yeniden başlatma
## Ön koşullar
Bakım penceresi açılmalı, trafik ikinci bölgeye yönlendirilmiş olmalı ve izleme panosu açık tutulmalıdır.

## Adımlar
1. kubectl rollout restart deploy/svc-orders -n orders komutunu çalıştırın.
2. Hata oranı ERR-1042 için panoyu izleyin; beş dakika içinde düşmeli.

` + "```bash\n# yorum başlık değildir\nkubectl get pods -n orders\n```" + `

# Sahiplik
Kısa.
`

func TestChunkMarkdownHeadingPaths(t *testing.T) {
	ch := ChunkMarkdown(runbookMD)
	if len(ch) < 3 {
		t.Fatalf("en az 3 parça bekleniyordu, %d: %+v", len(ch), ch)
	}
	if ch[0].Heading != "" || !strings.Contains(ch[0].Text, "işletim kılavuzudur") {
		t.Errorf("giriş parçası başlıksız olmalı: %+v", ch[0])
	}
	if strings.Contains(ch[0].Text, "[[_TOC_]]") {
		t.Error("wiki makrosu soyulmalı")
	}
	var steps *Chunk
	for i := range ch {
		if ch[i].Heading == "Yeniden başlatma"+HeadingSep+"Adımlar" {
			steps = &ch[i]
		}
	}
	if steps == nil {
		t.Fatalf("'Yeniden başlatma › Adımlar' başlık yolu yok: %+v", ch)
	}
	// Kod çiti içindeki '#' satırı başlık sayılmaz — çit parçada kalır.
	if !strings.Contains(steps.Text, "# yorum başlık değildir") {
		t.Errorf("kod çiti içi '#' satırı parçada kalmalı: %q", steps.Text)
	}
	// Kısa "Sahiplik" bölümü kendi parçası olmaz, öncekine katılır.
	for _, c := range ch {
		if c.Heading == "Sahiplik" {
			t.Errorf("40 rune altı bölüm ayrı parça olmamalı: %+v", c)
		}
	}
	if last := ch[len(ch)-1]; !strings.Contains(last.Text, "Sahiplik: Kısa.") {
		t.Errorf("kısa bölüm başlığıyla birlikte son parçaya taşınmalı: %q", last.Text)
	}
}

func TestChunkMarkdownSplitsLongSection(t *testing.T) {
	long := "# Mimari\n" + strings.Repeat("Bu paragraf mimariyi anlatır ve uzundur. ", 60) + "\n\n" +
		strings.Repeat("İkinci paragraf da uzundur ve bölünmelidir. ", 60)
	ch := ChunkMarkdown(long)
	if len(ch) < 2 {
		t.Fatalf("uzun bölüm bölünmeli: %d parça", len(ch))
	}
	for _, c := range ch {
		if c.Heading != "Mimari" {
			t.Errorf("bölünen her parça başlık yolunu taşımalı: %q", c.Heading)
		}
	}
}

func TestPageTitleAndNormalizePath(t *testing.T) {
	if PageTitle("/Runbooks/Restart svc-orders") != "Restart svc-orders" || PageTitle("/") != "/" {
		t.Error("PageTitle")
	}
	for in, want := range map[string]string{
		"Runbooks/Restart": "/Runbooks/Restart", "/A/B/": "/A/B", "/A.md": "/A", "/": "", "": "",
	} {
		if got := NormalizePagePath(in); got != want {
			t.Errorf("NormalizePagePath(%q)=%q want %q", in, got, want)
		}
	}
}

// candidate — bellekte CH'nin countEqual'ının ikizi.
func candidate(path, title, heading, text string, terms []string) Candidate {
	c := BuildChunks(title, "# "+heading+"\n"+text)
	if len(c) == 0 {
		return Candidate{}
	}
	return candidateFromChunk(PageRecord{WikiID: "w", Path: path, Title: title}, c[0], ExpandTerms(terms))
}

// statsOf — terms ÖZGÜN terimler; DF genişlemiş jeton sırasında (v0.10.1127).
func statsOf(cands []Candidate, terms []string) Stats {
	toks := ExpandTerms(terms)
	st := Stats{N: uint64(len(cands)), DF: make([]uint64, len(toks))}
	var dl float64
	for _, c := range cands {
		dl += float64(c.DL)
		for i := range toks {
			if c.TF[i] > 0 {
				st.DF[i]++
			}
		}
	}
	st.AvgDL = dl / float64(len(cands))
	return st
}

func TestRankLexicalExactTechnicalTokenWins(t *testing.T) {
	terms := QueryTerms("svc-orders restart")
	cands := []Candidate{
		// "orders" ve "svc" ayrı ayrı geçiyor ama bileşik YOK.
		candidate("/Genel", "Genel", "Notlar", "Orders tablosu ve svc listesi burada; restart nadiren gerekir. Bu sayfa genel bilgidir ve uzun uzun anlatır.", terms),
		candidate("/Runbooks/Orders", "Restart svc-orders", "Adımlar", "svc-orders için restart: kubectl rollout restart deploy/svc-orders.", terms),
		candidate("/Baska", "Başka", "Konu", "Bu sayfa tamamen alakasız bir konudan, faturalandırmadan söz eder.", terms),
	}
	hits := RankLexical(cands, statsOf(cands, terms), terms)
	if len(hits) != 2 {
		t.Fatalf("alakasız sayfa düşmeli: %d hit", len(hits))
	}
	if hits[0].Path != "/Runbooks/Orders" {
		t.Fatalf("tam teknik terim taşıyan sayfa önde olmalı: %+v", hits)
	}
	if hits[0].Score <= hits[1].Score || hits[0].Score > 1 || hits[0].Score < WeakScore {
		t.Errorf("skorlar [0,1] ve sıralı olmalı, en iyi eşiğin üstünde: %v / %v", hits[0].Score, hits[1].Score)
	}
}

func TestRankLexicalTurkishFoldingMatches(t *testing.T) {
	terms := QueryTerms("sifre sifirlama") // klavyede Türkçe karakter yok
	cands := []Candidate{
		candidate("/IAM/Şifre", "Şifre Sıfırlama", "Adımlar", "Şifre sıfırlama için kimlik portalına gidin.", terms),
	}
	hits := RankLexical(cands, statsOf(cands, terms), terms)
	if len(hits) != 1 || hits[0].Score < 0.9 {
		t.Fatalf("katlanmış yazım tam eşleşmeli: %+v", hits)
	}
}

func TestRankLexicalCoverageDropsWithUnknownTerm(t *testing.T) {
	terms := []string{"restart", "zzqqxx"}
	cands := []Candidate{candidate("/A", "A", "B", "restart restart prosedürü burada anlatılır ve uzundur.", terms)}
	st := statsOf(cands, terms)
	hits := RankLexical(cands, st, terms)
	if len(hits) != 1 || hits[0].Score >= WeakScore {
		t.Fatalf("korpusta hiç olmayan terim kapsamayı düşürmeli (zayıf): %+v", hits)
	}
}

func TestBlendWithAndWithoutEmbeddings(t *testing.T) {
	lex := []Hit{
		{ChunkRef: ChunkRef{WikiID: "w", Path: "/a"}, Score: 0.9, Lexical: 0.9},
		{ChunkRef: ChunkRef{WikiID: "w", Path: "/b"}, Score: 0.5, Lexical: 0.5},
	}
	// Embedding'siz: lexical AYNEN.
	if got := Blend(lex, nil, DefaultLexicalWeight); len(got) != 2 || got[0].Path != "/a" || got[0].Score != 0.9 {
		t.Fatalf("embedding'siz yol lexical'ı aynen döndürmeli: %+v", got)
	}
	sem := []SemHit{
		{ChunkRef: ChunkRef{WikiID: "w", Path: "/b"}, Cos: 0.95},
		{ChunkRef: ChunkRef{WikiID: "w", Path: "/c"}, Cos: 0.9}, // yalnız semantik aday
		{ChunkRef: ChunkRef{WikiID: "w", Path: "/d"}, Cos: -0.2},
	}
	got := Blend(lex, sem, 0.6)
	by := map[string]Hit{}
	for _, h := range got {
		by[h.Path] = h
	}
	if math.Abs(by["/a"].Score-0.54) > 1e-9 || math.Abs(by["/b"].Score-(0.6*0.5+0.4*0.95)) > 1e-9 {
		t.Errorf("harman: a=%v b=%v", by["/a"].Score, by["/b"].Score)
	}
	if math.Abs(by["/c"].Score-0.36) > 1e-9 || by["/d"].Score != 0 {
		t.Errorf("yalnız-semantik ve negatif kosinüs: c=%v d=%v", by["/c"].Score, by["/d"].Score)
	}
	if got[0].Path != "/b" {
		t.Errorf("harman sonrası sıralama: %+v", got)
	}
}

func TestDedupePagesAndSnippet(t *testing.T) {
	h := []Hit{
		{ChunkRef: ChunkRef{WikiID: "w", Path: "/a", Idx: 0}, Score: 0.9},
		{ChunkRef: ChunkRef{WikiID: "w", Path: "/a", Idx: 1}, Score: 0.8},
		{ChunkRef: ChunkRef{WikiID: "w", Path: "/b", Idx: 0}, Score: 0.7},
	}
	if got := DedupePages(h, 1); len(got) != 2 || got[1].Path != "/b" {
		t.Errorf("sayfa başına 1: %+v", got)
	}
	text := strings.Repeat("dolgu ", 100) + "Şifre sıfırlama adımı burada. " + strings.Repeat("son ", 100)
	sn := Snippet(text, []string{"sifirlama"}, 80)
	if !strings.Contains(sn, "sıfırlama") || len([]rune(sn)) > 82 {
		t.Errorf("kesit eşleşmenin çevresinden olmalı: %q", sn)
	}
}
