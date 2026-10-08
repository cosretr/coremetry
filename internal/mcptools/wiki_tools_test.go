package mcptools

// v0.10.1122 — search_wiki / read_wiki_page: sohbet-yalnız + koşullu kayıt,
// argüman kapısı, sonuç zarfı ve uzun sayfanın pencereli okunması.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/wiki"
)

type fakeWikiSource struct {
	res  wiki.SearchResult
	page *wiki.PageRecord
	err  error
	q    string
}

func (f *fakeWikiSource) Search(_ context.Context, q, _ string, _, _ int) (wiki.SearchResult, error) {
	f.q = q
	return f.res, f.err
}

func (f *fakeWikiSource) ReadPage(context.Context, string, string, string) (*wiki.PageRecord, error) {
	return f.page, f.err
}

func wikiToolNamed(ts []mcp.Tool, name string) *mcp.Tool {
	for i := range ts {
		if ts[i].Name == name {
			return &ts[i]
		}
	}
	return nil
}

func TestWikiToolsChatOnlyAndConditional(t *testing.T) {
	if wikiToolNamed(ChatToolList(Deps{}), WikiSearchToolName) != nil {
		t.Error("Deps.Wiki nil → sohbete sunulmamalı")
	}
	d := Deps{Wiki: &fakeWikiSource{}}
	if wikiToolNamed(ChatToolList(d), WikiSearchToolName) == nil || wikiToolNamed(ChatToolList(d), WikiReadToolName) == nil {
		t.Fatal("Deps.Wiki dolu → iki araç da sohbette")
	}
	srv := mcp.New("t", "v")
	Register(srv, d)
	external := 0
	for _, tl := range ToolList(d) {
		if !chatOnlyTools[tl.Name] {
			external++
		}
	}
	if srv.ToolCount() != external {
		t.Fatalf("dış MCP %d araç kaydetti, sohbet-yalnızlar hariç %d bekleniyordu", srv.ToolCount(), external)
	}
	if !chatOnlyTools[WikiSearchToolName] || !chatOnlyTools[WikiReadToolName] {
		t.Fatal("wiki araçları dış MCP'ye kaydedilmemeli (chatOnlyTools)")
	}
	for _, tl := range ToolList(d) {
		if (tl.Name == WikiSearchToolName || tl.Name == WikiReadToolName) && tl.MinRole != "" {
			t.Errorf("%s viewer tabanında olmalı", tl.Name)
		}
	}
}

func TestSearchWikiToolEnvelope(t *testing.T) {
	src := &fakeWikiSource{res: wiki.SearchResult{
		Terms: []string{"svc-orders"}, Mode: "lexical",
		Hits: []wiki.Hit{{ChunkRef: wiki.ChunkRef{Project: "Platform", WikiName: "Platform.wiki", Path: "/Runbooks/Restart svc-orders",
			Title: "Restart svc-orders", URL: "https://devops.example.test/x", Text: "kubectl rollout restart deploy/svc-orders"}, Score: 0.87654}},
	}}
	tl := wikiToolNamed(ToolList(Deps{Wiki: src}), WikiSearchToolName)
	out, err := tl.Handler(context.Background(), json.RawMessage(`{"query":"svc-orders restart","limit":50}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	s := string(b)
	for _, want := range []string{`"url":"https://devops.example.test/x"`, `"score":0.877`, `"data_note"`, `"state":"ok"`, `"path":"/Runbooks/Restart svc-orders"`} {
		if !strings.Contains(s, want) {
			t.Errorf("zarf %s içermeli: %s", want, s)
		}
	}
	if _, err := tl.Handler(context.Background(), json.RawMessage(`{"query":"x","url":"http://evil"}`)); err == nil {
		t.Error("bilinmeyen alan reddedilmeli")
	}
	if _, err := tl.Handler(context.Background(), json.RawMessage(`{"query":"  "}`)); err == nil {
		t.Error("boş query hata")
	}
	// Bağlantısız (nil) kaynak: dürüst not_configured.
	nt := wikiToolNamed(ToolList(Deps{}), WikiSearchToolName)
	out, err = nt.Handler(context.Background(), json.RawMessage(`{"query":"svc-orders"}`))
	b, _ = json.Marshal(out)
	if err != nil || !strings.Contains(string(b), "not_configured") {
		t.Errorf("yapılandırılmamış: %v %s", err, b)
	}
}

func TestReadWikiPageWindowing(t *testing.T) {
	long := strings.Repeat("a", WikiPageMaxRunes+5000)
	rec := wiki.PageRecord{Project: "P", WikiName: "W", Path: "/p", Title: "p", URL: "https://devops.example.test/p", Content: long}
	w0 := WikiPageWindow(rec, 0)
	if w0["next_offset"] != WikiWindowRunes || w0["truncated"] != true || len(w0["content"].(string)) != WikiWindowRunes {
		t.Fatalf("ilk pencere: next=%v trunc=%v", w0["next_offset"], w0["truncated"])
	}
	last := WikiPageWindow(rec, WikiPageMaxRunes-10)
	if _, ok := last["next_offset"]; ok || len(last["content"].(string)) != 10 {
		t.Fatalf("40 KB tavanında biter: %v", last["next_offset"])
	}
	// Pencere + zarf sohbetin 6000 rune'luk sonuç bütçesine sığmalı.
	b, _ := json.Marshal(w0)
	if n := len([]rune(string(b))); n > 6000 {
		t.Errorf("pencere zarfı %d rune — sohbet bütçesini aşıyor", n)
	}
	short := WikiPageWindow(wiki.PageRecord{Content: "kısa"}, 0)
	if _, ok := short["next_offset"]; ok || short["truncated"] != nil {
		t.Error("kısa sayfa tek pencere, kesilmemiş")
	}
	tl := wikiToolNamed(ToolList(Deps{Wiki: &fakeWikiSource{page: &rec}}), WikiReadToolName)
	if _, err := tl.Handler(context.Background(), json.RawMessage(`{"project":"P","wiki":"W"}`)); err == nil {
		t.Error("path zorunlu")
	}
	nf := wikiToolNamed(ToolList(Deps{Wiki: &fakeWikiSource{}}), WikiReadToolName)
	out, err := nf.Handler(context.Background(), json.RawMessage(`{"project":"P","wiki":"W","path":"/yok"}`))
	if err != nil || out.(map[string]any)["found"] != false {
		t.Errorf("olmayan sayfa found=false: %v %v", out, err)
	}
}

func TestWikiResultLinks(t *testing.T) {
	ls := WikiResultLinks(WikiSearchToolName, `{"rows":[{"title":"A","url":"https://devops.example.test/a"},{"title":"B","url":"ftp://x"}]}`)
	if len(ls) != 1 || ls[0].Href != "https://devops.example.test/a" {
		t.Fatalf("%+v", ls)
	}
	if WikiResultLinks("list_services", `{}`) != nil {
		t.Error("başka araç")
	}
}
