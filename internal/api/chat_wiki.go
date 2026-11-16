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
	// ragWikiMaxPages — v0.10.1136: bağlama giren en çok wiki SAYFASI (eski
	// ≤3 çıplak parça × 1500 yerine sayfa başına genişletilmiş bölümler,
	// bütçenin wikiRAGShare'i; chat_wiki_multi.go ragWikiContextFor).
	ragWikiMaxPages = 3
	// ragWikiSearchLimit / ragWikiPerPage — arama: ≤3 sayfaya yetecek isabet.
	ragWikiSearchLimit = 6
	ragWikiPerPage     = 2
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
	res, err := w.SearchWith(ctx, question, "", wiki.SearchOptions{Limit: ragWikiSearchLimit, PerPage: ragWikiPerPage, Live: wiki.LiveOnStale, NoSemantic: true})
	if err != nil {
		return nil
	}
	hits := res.Hits
	// v0.10.1175 — yüklenen belgeler (envanter tabloları, JSON/YAML kayıtları):
	// ayrı yerel arama, belge başına daha çok satır (genel arama sayfa başına 2).
	if loc, lerr := w.SearchWith(ctx, question, wiki.LocalProject, wiki.SearchOptions{Limit: ragLocalDocLimit, PerPage: ragLocalDocLimit, Live: wiki.LiveOff, NoSemantic: true}); lerr == nil {
		hits = appendNewHits(hits, loc.Hits)
	}
	return ragWikiSelectIDs(hits, identifierTerms(question))
}

// v0.10.1175 — Operator-reported: RAG'e yüklenen Excel'den "hostname ↔
// appname" soruları bulunamıyordu. Kök neden (yerel ölçüm): "hstqzwx01 hangi
// uygulama" sorgusunun terimleri [hstqzwx01 uygulama]; satırda kolon adı
// "AppName" olduğu için "uygulama" geçmez, terim kapsamı ~yarıda kalır ve
// skor ragWikiFloor (0.5) altına düşer — satır hostname'i birebir taşısa da
// atılır. Ayrıca genel arama sayfa başına 2 parça aldığından bir envanter
// belgesinden en çok 2 parça gelirdi. Çözüm: (1) yüklenen belgeler için ayrı
// yerel arama, belge başına ragLocalDocLimit parça; (2) KİMLİK BİÇİMLİ terim
// (rakam ya da - . _ içeren, ≥4 harf: hostname, uygulama kodu, IP) parçada
// birebir geçiyorsa isabet kapsam tabanına takılmaz.
const (
	ragLocalDocLimit = 12
	ragIDHitsMax     = 12
)

// identifierTerms — SAF: sorgudaki kimlik biçimli terimler (küçük harf).
func identifierTerms(question string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range wiki.QueryTerms(question) {
		t = strings.ToLower(t)
		if len([]rune(t)) < 4 || seen[t] || !strings.ContainsAny(t, "0123456789-._") {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// appendNewHits — SAF: b'deki, a'da olmayan (parça kimliğiyle) isabetler sona.
func appendNewHits(a, b []wiki.Hit) []wiki.Hit {
	seen := make(map[string]bool, len(a))
	for _, x := range a {
		seen[x.Key()] = true
	}
	out := append([]wiki.Hit(nil), a...)
	for _, x := range b {
		if !seen[x.Key()] {
			seen[x.Key()] = true
			out = append(out, x)
		}
	}
	return out
}

// ragWikiSelectIDs — SAF: kimliği birebir taşıyan isabetler önce (taban
// uygulanmaz, en çok ragIDHitsMax), ardından normal seçim.
func ragWikiSelectIDs(h []wiki.Hit, ids []string) []wiki.Hit {
	var idHits []wiki.Hit
	if len(ids) > 0 {
		for _, x := range h {
			low := strings.ToLower(x.Text)
			for _, id := range ids {
				if strings.Contains(low, id) {
					idHits = append(idHits, x)
					break
				}
			}
			if len(idHits) >= ragIDHitsMax {
				break
			}
		}
	}
	sel := ragWikiSelect(h)
	if len(idHits) == 0 {
		return sel
	}
	return appendNewHits(idHits, sel)
}

// ragWikiSelect — SAF: en iyi skor taban altındaysa hiç; değilse taban/2
// üstündeki isabetler, en çok ragWikiMaxPages farklı sayfadan (sayfa sırası).
func ragWikiSelect(h []wiki.Hit) []wiki.Hit {
	if len(h) == 0 || h[0].Score < ragWikiFloor {
		return nil
	}
	// ragWikiFloor/2: uzak kuyruk bağlamı kirletmesin.
	return wikiPagesHits(wikiGroupPages(h, ragWikiFloor/2, ragWikiMaxPages))
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
