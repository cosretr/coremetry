package api

// chat_wiki.go — v0.10.1122 ("karma"): wiki araçlarının ve RAG kademesindeki
// wiki dayanağının SUNUCU kararları. Araçlar mcptools/wiki_tools.go'da, motor
// internal/wiki'de.
//
// ── NEREDE SUNULUR (read_source_code ile karşılaştırma) ─────────────────────
//
//   - SOHBET-YALNIZ: dış MCP sunucusunda kayıtlı değil (mcptools.chatOnlyTools).
//   - YALNIZ oturum kullanıcısına: cmk_ API token'ı da /api/copilot/chat'e
//     girer; wiki içeriği token'la dışarı alınamasın diye token ve kimliksiz
//     çağıran ROLDEN BAĞIMSIZ dışarıda — sourceCodeCallerAllowed'ın AYNISI.
//     Aynı kapı RAG kademesindeki wiki dayanağına da uygulanır (token'lı
//     sohbet doküman RAG'ını görür, wiki'yi görmez).
//   - YALNIZ wiki bilgisi açık + DevOps bağlıyken (Deps.Wiki nil → katalogdan düşer).
//   - BİLİNÇLİ FARK: read_source_code yalnız panel trace takibinde sunulur;
//     wiki araçları bağımsız sohbette de sunulur — özelliğin amacı tam olarak
//     "runbook / nasıl yapılır" sorusunu bağımsız sohbette cevaplamak. AMA
//     turun kataloğunda dış MCP aracı varsa wiki araçları DÜŞER (onaysız
//     döngüde wiki metni dış araca taşınamasın); o turda wiki yalnız RAG
//     kademesinden gelir (araçsız tek anlatım çağrısı, dış araç yok).
//   Sunulmadığında katalog ve döngü prompt'u bayt bayt eski.

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/wiki"
)

// wikiSourceOrNil — Deps.Wiki: wiki bilgisi kapalıysa ya da DevOps bağlı
// değilse nil (araçlar sohbete sunulmaz). Arayüze nil *wiki.Service SARILMAZ.
func (s *Server) wikiSourceOrNil() mcptools.WikiSource {
	w := wikiKB()
	if w == nil || !w.Enabled() {
		return nil
	}
	return w
}

// isWikiTool — SAF.
func isWikiTool(name string) bool {
	return name == mcptools.WikiSearchToolName || name == mcptools.WikiReadToolName
}

// wikiToolsFor — SAF: wiki araçları ADIYLA düşer (katalog, spec ve prompt eki
// ondan önce kurulmaz) eğer çağıran oturum kullanıcısı değilse (API token'ı,
// kimliksiz) YA DA bu turun kataloğunda dış MCP aracı varsa (hasExternal).
// İkincisi exfil kapısı: bağımsız döngüde onay adımı yok; ekilmiş bir log
// satırı modeli wiki metnini bir dış aracın argümanına koymaya yönlendirebilirdi.
// O turda wiki bilgisi yalnız RAG kademesinden (araçsız anlatım) gelir.
func wikiToolsFor(tools []mcp.Tool, c *auth.Claims, hasExternal bool) []mcp.Tool {
	if sourceCodeCallerAllowed(c) && !hasExternal {
		return tools
	}
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if !isWikiTool(t.Name) {
			out = append(out, t)
		}
	}
	return out
}

// chatWikiPromptTR — wiki araçları bu turun kataloğundaysa döngü prompt'una
// giren kısa ek; değilse "" ve döngü prompt'u bayt bayt eskisi.
func chatWikiPromptTR(tools []mcp.Tool) string {
	for _, t := range tools {
		if t.Name == mcptools.WikiSearchToolName {
			return copilot.WikiChatAddendum() + "\n\n"
		}
	}
	return ""
}

// wikiToolLinks — SAF: wiki araç sonucundaki sayfa url'leri → cevap çipleri.
func wikiToolLinks(tool, content string) []guidedAnswerLink {
	ls := mcptools.WikiResultLinks(tool, content)
	out := make([]guidedAnswerLink, 0, len(ls))
	for _, l := range ls {
		out = append(out, guidedAnswerLink{Label: l.Label, Href: l.Href})
	}
	return out
}

// ── RAG kademesi: wiki dayanağı ─────────────────────────────────────────────

const (
	// ragWikiFloor — RAG kademesinde wiki parçasının cevap dayanağı sayılması
	// için en iyi skorun tabanı (kapsama ağırlıklı lexical/hibrit skor). Doküman
	// köprüsünün ragKeywordFloor'uyla aynı ruh: tesadüfi tek terim eşleşmesi
	// telemetri sorusunu wiki'ye kaçırmasın.
	ragWikiFloor = 0.5
	// ragWikiMaxChunks — bağlama giren en çok wiki parçası; sayfa başına 2.
	ragWikiMaxChunks = 3
	// ragWikiChunkRunes — bağlamdaki parça başına tavan.
	ragWikiChunkRunes = 1500
)

// ragWikiHits — RAG kademesinin wiki yarısı. Yalnız oturum kullanıcısına;
// canlı yedek yalnız yerel indeks boş/bayatsa (her serbest sorunun önüne
// canlı arama gecikmesi eklenmez — wiki.LiveOnStale). Yalnız lexical
// (NoSemantic): ikinci bir soru embed'i ve FINAL kosinüs tam taraması bu
// kademede yok — hibrit sıralama search_wiki aracında kalır.
func (s *Server) ragWikiHits(ctx context.Context, question string) []wiki.Hit {
	w := wikiKB()
	if w == nil || !w.Enabled() || !sourceCodeCallerAllowed(auth.FromContext(ctx)) {
		return nil
	}
	res, err := w.SearchWith(ctx, question, "", wiki.SearchOptions{Limit: ragWikiMaxChunks, PerPage: 2, Live: wiki.LiveOnStale, NoSemantic: true})
	if err != nil {
		return nil
	}
	return ragWikiSelect(res.Hits)
}

// ragWikiSelect — SAF: taban altındaysa hiç; değilse tavanlı ilk N.
func ragWikiSelect(h []wiki.Hit) []wiki.Hit {
	if len(h) == 0 || h[0].Score < ragWikiFloor {
		return nil
	}
	out := make([]wiki.Hit, 0, ragWikiMaxChunks)
	for _, x := range h {
		if x.Score < ragWikiFloor/2 { // uzak kuyruk bağlamı kirletmesin
			break
		}
		out = append(out, x)
		if len(out) >= ragWikiMaxChunks {
			break
		}
	}
	return out
}

// ragWikiContext — SAF: wiki parçasının bağlam bloğu. Sayfa ADI modele
// verilmez (systemRAGChat'in "dosya adı anma" kuralı, v0.9.515) — başlık
// yolu bilgi taşır, kaynağı arayüz çiplerle gösterir.
func ragWikiContext(n int, h wiki.Hit) string {
	text := h.Text
	if r := []rune(text); len(r) > ragWikiChunkRunes {
		text = string(r[:ragWikiChunkRunes]) + "…"
	}
	head := ""
	if h.Heading != "" {
		head = " — " + h.Heading
	}
	return fmt.Sprintf("[%d] wiki%s\n%s\n\n", n, head, fenceWikiData(text))
}

// wikiDataCloseRe — içerideki kapanış etiketi (büyük/küçük harf, boşluklu
// yazımlar dahil) — çit dışına taşma girişimi.
var wikiDataCloseRe = regexp.MustCompile(`(?i)<\s*/?\s*wiki_data\s*>`)

// fenceWikiData — SAF (v0.10.1124): wiki metni <wiki_data>…</wiki_data>
// çitinde verilir; içerideki açılış/kapanış etiketleri silinir ki sayfa
// metni çitten "çıkıp" talimat gibi görünemesin.
func fenceWikiData(text string) string {
	return "<wiki_data>\n" + strings.TrimSpace(wikiDataCloseRe.ReplaceAllString(text, "")) + "\n</wiki_data>"
}

// ragWikiLinks — SAF: kullanılan wiki sayfalarının çipleri (sayfa başına bir).
func ragWikiLinks(h []wiki.Hit) []guidedAnswerLink {
	seen := map[string]bool{}
	var out []guidedAnswerLink
	for _, x := range h {
		u := strings.TrimSpace(x.URL)
		if u == "" || seen[u] || !(strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")) {
			continue
		}
		seen[u] = true
		out = append(out, guidedAnswerLink{Label: "Wiki · " + x.Title, Href: u})
	}
	return out
}

// ragAnswerLinks — SAF: RAG cevabının çipleri. Wiki çipi yoksa request-ID
// listesi AYNEN döner (wiki kapalı kurulumda bayt bayt eski davranış, kendi
// tavanı dahil); varsa wiki çipleri önce, request-ID çipleri kendi tavanıyla
// ardına (href'e göre tekil — wiki çipi request-ID çipini EZMEZ, kesmez).
func ragAnswerLinks(wikiLinks, reqLinks []guidedAnswerLink) []guidedAnswerLink {
	if len(wikiLinks) == 0 {
		return reqLinks
	}
	seen := map[string]bool{}
	out := make([]guidedAnswerLink, 0, len(wikiLinks)+len(reqLinks))
	for _, l := range append(append([]guidedAnswerLink(nil), wikiLinks...), reqLinks...) {
		if seen[l.Href] {
			continue
		}
		seen[l.Href] = true
		out = append(out, l)
	}
	return out
}
