package wiki

import (
	"regexp"
	"strings"

	"github.com/cilcenk/coremetry/internal/rag"
)

// chunk.go — Markdown başlık-duyarlı parçalayıcı (SAF).
//
// Wiki sayfaları başlıklarla yapılanır ("## Geri alma", "### Adımlar") ve
// bir runbook sorusunun cevabı çoğu zaman TEK bir başlığın altındadır.
// Parçayı başlık sınırından kesmek, cevabın iki parçaya bölünüp ikisinin de
// yarım skor almasını önler; başlık YOLU ("Kurulum › Geri alma") parçaya
// bağlam olarak eklenir — "Adımlar" başlığı tek başına hiçbir şey söylemez.
// Uzun bölümler rag.ChunkText'in paragraf-duyarlı bölücüsünden geçer (TEK
// bölücü; RAG ile aynı boy hedefi).

// Chunk — bir sayfanın tek parçası.
type Chunk struct {
	Heading string // başlık yolu, "A › B"; başlıksız giriş bölümünde ""
	Text    string // bölüm gövdesi (Markdown)
}

var (
	atxHeading = regexp.MustCompile(`^(#{1,6})[ \t]+(.+?)[ \t#]*$`)
	// wikiMacro — Azure DevOps wiki makroları ([[_TOC_]], [[_TOSP_]]); arama değeri yok.
	wikiMacro   = regexp.MustCompile(`\[\[_[A-Z]+_\]\]`)
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
)

// HeadingSep — başlık yolu ayracı.
const HeadingSep = " › "

// minChunkRunes — bundan kısa (yalnız başlık / tek satır) bölümler komşusuna
// eklenir; tek başına parça olmaz.
const minChunkRunes = 40

// ChunkMarkdown — sayfayı başlık sınırlarından parçalara böler.
func ChunkMarkdown(md string) []Chunk {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	md = htmlComment.ReplaceAllString(md, "")
	md = wikiMacro.ReplaceAllString(md, "")

	type section struct {
		path []string
		body strings.Builder
	}
	var sections []*section
	var stack []string // seviye başına başlık
	cur := &section{}
	sections = append(sections, cur)
	inFence := false
	fenceMark := ""
	for _, line := range strings.Split(md, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") || strings.HasPrefix(trim, ":::") {
			mark := trim[:3]
			if !inFence {
				inFence, fenceMark = true, mark
			} else if mark == fenceMark {
				inFence, fenceMark = false, ""
			}
			cur.body.WriteString(line)
			cur.body.WriteByte('\n')
			continue
		}
		if !inFence {
			if m := atxHeading.FindStringSubmatch(trim); m != nil && !strings.HasPrefix(line, "    ") {
				level := len(m[1])
				for len(stack) >= level {
					stack = stack[:len(stack)-1]
				}
				for len(stack) < level-1 {
					stack = append(stack, "")
				}
				stack = append(stack, strings.TrimSpace(m[2]))
				cur = &section{path: compactPath(stack)}
				sections = append(sections, cur)
				continue
			}
		}
		cur.body.WriteString(line)
		cur.body.WriteByte('\n')
	}

	var out []Chunk
	var carry string // kısa bölümün gövdesi — sonraki bölüme taşınır
	for _, s := range sections {
		body := strings.TrimSpace(s.body.String())
		heading := strings.Join(s.path, HeadingSep)
		if carry != "" {
			if body == "" {
				body = carry
			} else {
				body = carry + "\n\n" + body
			}
			carry = ""
		}
		if body == "" {
			continue
		}
		if len([]rune(body)) < minChunkRunes {
			if heading != "" {
				carry = heading + ": " + body
			} else {
				carry = body
			}
			continue
		}
		for _, piece := range rag.ChunkText(body) {
			out = append(out, Chunk{Heading: heading, Text: piece})
		}
	}
	if carry != "" {
		if n := len(out); n > 0 {
			out[n-1].Text += "\n\n" + carry
		} else {
			out = append(out, Chunk{Text: carry})
		}
	}
	return out
}

// compactPath — boş ara seviyeleri (### önce ## yoksa) atar.
func compactPath(stack []string) []string {
	out := make([]string, 0, len(stack))
	for _, h := range stack {
		if h != "" {
			out = append(out, h)
		}
	}
	return out
}

// PageTitle — sayfa yolunun son parçası ("/Runbooks/Restart svc" → "Restart svc").
func PageTitle(path string) string {
	p := strings.TrimRight(strings.TrimSpace(path), "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	if p == "" {
		return "/"
	}
	return p
}

// ChunkContext — aramaya ve modele giden bağlamlı metin: sayfa başlığı +
// başlık yolu + gövde. Jetonlar ve embedding bu metinden üretilir.
func ChunkContext(title, heading, text string) string {
	h := title
	if heading != "" {
		h += HeadingSep + heading
	}
	return h + "\n" + text
}
