package api

// v0.10.1122 — wiki araçlarının sunulma kapısı (API token'ı çağıramaz), prompt
// eki, cevap çipleri, RAG kademesindeki wiki tabanı ve Ayarlar uçlarının rol
// kapıları. Adlar sentetik (example.test, svc-orders).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/wiki"
)

func wikiCatalog() []mcp.Tool {
	return []mcp.Tool{{Name: "list_services"}, {Name: mcptools.WikiSearchToolName}, {Name: mcptools.WikiReadToolName}}
}

func TestWikiToolsForGatesAPITokens(t *testing.T) {
	names := func(ts []mcp.Tool) string {
		var s []string
		for _, x := range ts {
			s = append(s, x.Name)
		}
		return strings.Join(s, ",")
	}
	session := &auth.Claims{UserID: "u-1", Role: auth.RoleViewer}
	if got := names(wikiToolsFor(wikiCatalog(), session, false)); got != "list_services,search_wiki,read_wiki_page" {
		t.Errorf("oturum kullanıcısı (viewer dahil), dış MCP yokken wiki araçlarını görmeli: %s", got)
	}
	// Exfil kapısı: turun kataloğunda dış MCP aracı varsa wiki araçları DÜŞER.
	if got := names(wikiToolsFor(wikiCatalog(), session, true)); got != "list_services" {
		t.Errorf("dış MCP yapılandırılmış bağımsız sohbette wiki aracı sunulmamalı: %s", got)
	}
	for _, c := range []*auth.Claims{
		{UserID: "token:abc", Role: auth.RoleAdmin}, // API token'ı — rolden BAĞIMSIZ dışarıda
		{UserID: "", Role: auth.RoleAdmin},
		nil,
	} {
		if got := names(wikiToolsFor(wikiCatalog(), c, false)); got != "list_services" {
			t.Errorf("token/kimliksiz çağıran wiki aracı görmemeli (%+v): %s", c, got)
		}
	}
}

func TestChatWikiPromptOnlyWhenOffered(t *testing.T) {
	if chatWikiPromptTR([]mcp.Tool{{Name: "list_services"}}) != "" {
		t.Error("araç yokken prompt bayt bayt eski olmalı")
	}
	p := chatWikiPromptTR(wikiCatalog())
	if !strings.Contains(p, "search_wiki") || !strings.Contains(p, "Kaynak:") {
		t.Errorf("ek: %q", p)
	}
}

func TestWikiToolLinksHTTPOnly(t *testing.T) {
	content := `{"rows":[{"title":"Restart svc-orders","url":"https://devops.example.test/w?pagePath=%2FA"},` +
		`{"title":"Kötü","url":"javascript:alert(1)"},{"title":"Üçüncü","url":"https://devops.example.test/c"}]}`
	ls := wikiToolLinks(mcptools.WikiSearchToolName, content)
	if len(ls) != 1 || ls[0].Label != "Wiki · Restart svc-orders" {
		t.Fatalf("yalnız http(s), en iyi iki satırdan: %+v", ls)
	}
	read := wikiToolLinks(mcptools.WikiReadToolName, `{"title":"Mimari","url":"https://devops.example.test/m"}`)
	if len(read) != 1 || read[0].Href != "https://devops.example.test/m" {
		t.Fatalf("read_wiki_page çipi: %+v", read)
	}
	if wikiToolLinks("list_services", content) != nil && len(wikiToolLinks("list_services", content)) != 0 {
		t.Error("başka araç çip üretmemeli")
	}
}

func TestRagWikiSelectAndContext(t *testing.T) {
	h := []wiki.Hit{
		{ChunkRef: wiki.ChunkRef{WikiID: "w", Path: "/a", Title: "Gizli Başlık", Heading: "Adımlar", Text: "svc-orders restart", URL: "https://devops.example.test/a"}, Score: 0.8},
		{ChunkRef: wiki.ChunkRef{WikiID: "w", Path: "/a", Idx: 1, URL: "https://devops.example.test/a"}, Score: 0.6},
		{ChunkRef: wiki.ChunkRef{WikiID: "w", Path: "/b", URL: "https://devops.example.test/b"}, Score: 0.2},
	}
	got := ragWikiSelect(h)
	if len(got) != 2 {
		t.Fatalf("taban/2 altındaki kuyruk düşmeli: %d", len(got))
	}
	if ragWikiSelect([]wiki.Hit{{Score: ragWikiFloor - 0.01}}) != nil {
		t.Error("taban altı en iyi skor → wiki dayanağı yok (telemetri sorusu kaçırılmaz)")
	}
	ctx, _ := buildWikiMultiContext(wikiGroupPages(got, 0, ragWikiMaxPages), nil, wikiBudgetDefault/2, false, numberSources([]chatSource{{Doc: "a.pdf"}, {Doc: "b.pdf"}}))
	if !strings.HasPrefix(ctx, "[3] Wiki · Gizli Başlık\n<wiki_data>\nSayfa: Gizli Başlık\n## Adımlar\nsvc-orders restart") || strings.Count(ctx, "<wiki_data>") != 1 {
		t.Errorf("v0.10.1136: kaynak [n] + sayfa başlığıyla, sayfa başına tek çit: %q", ctx)
	}
	links := ragWikiLinks(h)
	if len(links) != 2 {
		t.Errorf("sayfa başına tek çip: %+v", links)
	}
}

// Kaynak pinleri: kapı katalogdan ÖNCE uygulanır (spec ve byName ondan kurulur),
// prompt eki döngü prompt'una girer, RAG kademesi wiki yarısını çağırır.
func TestWikiGateWiredIntoChat(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	gate := strings.Index(src, "tools = wikiToolsFor(tools, c, extConfigured || len(extTools) > 0)")
	if !strings.Contains(src, "extConfigured := !isTraceFollowUp && s.mcpClient != nil && s.mcpClient.Configured()") {
		t.Error("wiki kapısı dış MCP YAPILANDIRMASINA da bakmalı (tur arası taşıma)")
	}
	// Dış katalog wiki kararından ÖNCE kurulmalı (yoksa karar dış aracı göremez).
	ext := strings.Index(src, "extTools = toolsForRole(ext, role)")
	if ext < 0 || gate < 0 || ext > gate {
		t.Errorf("dış MCP kataloğu wikiToolsFor'dan ÖNCE kurulmalı (ext=%d gate=%d)", ext, gate)
	}
	if !strings.Contains(src, "for _, t := range extTools {") {
		t.Error("dış araçlar kurulan extTools'tan byName/spec'e eklenmeli")
	}
	srcGate := strings.Index(src, "tools = sourceCodeToolsFor(")
	byName := strings.Index(src, "byName[t.Name] = t.Handler")
	if gate < 0 || srcGate < 0 || byName < 0 || gate < srcGate || gate > byName {
		t.Errorf("wikiToolsFor sourceCodeToolsFor'dan sonra, byName'den ÖNCE olmalı (gate=%d src=%d byName=%d)", gate, srcGate, byName)
	}
	if !strings.Contains(src, "chatWikiPromptTR(tools)") || !strings.Contains(src, "wikiToolLinks(tc.Name, tr.Content)") {
		t.Error("prompt eki ve wiki çipleri döngüye bağlı olmalı")
	}
	rb, err := os.ReadFile("rag.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripGoCommentsAPI(string(rb)), "s.ragWikiHits(ctx, question)") {
		t.Error("RAG kademesi wiki dayanağını çağırmalı")
	}
	wb, err := os.ReadFile("chat_wiki.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripGoCommentsAPI(string(wb)), "sourceCodeCallerAllowed(auth.FromContext(ctx))") {
		t.Error("RAG kademesindeki wiki yarısı da token'ı dışlamalı")
	}
	wr, _ := os.ReadFile("wiki.go")
	ws := stripGoCommentsAPI(string(wr))
	for _, want := range []string{`s.audit(r, "settings.wiki.update"`, `s.audit(r, "wiki.sync"`} {
		if !strings.Contains(ws, want) {
			t.Errorf("admin yazması audit'siz: %s", want)
		}
	}
}

func TestRagAnswerLinksKeepsRequestIDLinksUntouched(t *testing.T) {
	req := []guidedAnswerLink{
		{Label: "r1", Href: "/logs?q=1"}, {Label: "r2", Href: "/logs?q=2"}, {Label: "r3", Href: "/logs?q=3"},
		{Label: "r4", Href: "/logs?q=4"}, {Label: "r5", Href: "/logs?q=5"},
	}
	// Wiki kapalı → request-ID listesi AYNEN (5 çip, tavanı kesilmez; nil nil kalır).
	got := ragAnswerLinks(nil, req)
	if len(got) != 5 || &got[0] != &req[0] {
		t.Fatalf("wiki yokken liste bayt bayt aynı olmalı: %+v", got)
	}
	if ragAnswerLinks(nil, nil) != nil {
		t.Error("wiki ve request-ID yokken nil (eski JSON: null)")
	}
	wl := []guidedAnswerLink{{Label: "Wiki · A", Href: "https://devops.example.test/a"}, {Label: "dup", Href: "/logs?q=1"}}
	got = ragAnswerLinks(wl, req)
	if len(got) != 6 || got[0].Href != "https://devops.example.test/a" || got[1].Label != "dup" || got[5].Label != "r5" {
		t.Fatalf("wiki önce, request-ID çiplerinin hepsi ardında (href tekil): %+v", got)
	}
}

func TestWikiWritesRejectAPITokens(t *testing.T) {
	prev := wikiKB()
	SetWiki(nil)
	t.Cleanup(func() { SetWiki(prev) })
	mux := (&Server{}).buildMux()
	for _, c := range []struct{ method, path string }{{"PUT", "/api/wiki/config"}, {"POST", "/api/wiki/sync"}} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`))
		req = req.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "token:t1", Role: auth.RoleAdmin}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s admin API token'ı: %d (403 bekleniyordu)", c.method, c.path, rec.Code)
		}
	}
}

func TestWikiRoutesRoleGates(t *testing.T) {
	prev := wikiKB()
	SetWiki(nil)
	t.Cleanup(func() { SetWiki(prev) })
	mux := (&Server{}).buildMux()
	do := func(method, path, role string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(`{"enabled":true}`))
		req = req.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-1", Role: role}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	for _, c := range []struct{ method, path string }{{"PUT", "/api/wiki/config"}, {"POST", "/api/wiki/sync"}} {
		for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
			if rec := do(c.method, c.path, role); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s rol %s: %d (403 bekleniyordu)", c.method, c.path, role, rec.Code)
			}
		}
	}
	rec := do("GET", "/api/wiki/config", auth.RoleViewer)
	var body map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["available"] != false {
		t.Errorf("viewer GET config (servis yok): %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("POST", "/api/wiki/sync", auth.RoleAdmin); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("servis yokken admin sync 503: %d", rec.Code)
	}
}
