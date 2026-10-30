package mcptools

// toolset_catalog_golden_test.go — v0.10.1434 (CoSRE araştırma programı PR 1,
// docs/audits/cosre-investigation-model-audit.md §6.1 "değişmezlik sözleşmesi").
//
// Toolset katmanı modele ve MCP istemcilerine giden araç listesini, HER ŞEY
// AÇIKKEN, bayt-bayt değiştirmez. Kanıt iki katmanlı:
//
//  1. ALTIN: testdata/toolset_catalog_golden.json, toolset katmanı yazılmadan
//     ÖNCE (HEAD v0.10.1431 kodu) üretildi — her varyantın ad listesi + seri
//     hâlinin SHA-256'sı. Katman eklendikten sonra nil / boş / yalnız
//     kapatılamaz adları taşıyan ToolsetsDisabled ile aynı özet çıkmalı.
//  2. Varyantlar: ToolList, ChatToolList, ChatToolListFor(full|simple) ve dış
//     MCP tools/list ham gövdesi; iki Deps biçimiyle — sıfır (koşullu araçlar
//     sunulmaz) ve koşullu bağımlılıkları dolu (yalnız-sohbet read_source_code /
//     search_code / search_wiki / read_wiki_page ve resolve_oracle_operation
//     sohbete girer). Rol başına (viewer/editor/admin) kanıt api paketinde
//     (ai_toolsets_test.go TestChatCatalogPerRoleMatchesGolden).
//
// Bir aracın metni/şeması BİLİNÇLİ değiştiğinde:
//   go test ./internal/mcptools -run TestToolsetCatalogGolden -update
// ve diff'i incele (ad listesi okunur; özet değişimi yalnız o aracın metni).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/wiki"
)

var updateToolsetGolden = flag.Bool("update", false, "toolset katalog altınını yeniden yaz")

const toolsetGoldenPath = "testdata/toolset_catalog_golden.json"

type catalogGoldenEntry struct {
	Count  int      `json:"count"`
	SHA256 string   `json:"sha256"`
	Names  []string `json:"names"`
}

// serializeCatalog — modelin / istemcinin okuduğu her alan: ad, tam açıklama,
// kompakt metin, iki varyantın işlenmiş sohbet metni, şema (ham), MinRole.
// encoding/json harita anahtarlarını sıralar → deterministik.
func serializeCatalog(t *testing.T, tools []mcp.Tool) []byte {
	t.Helper()
	type row struct {
		Name       string `json:"name"`
		Desc       string `json:"description"`
		Short      string `json:"short"`
		ChatFull   string `json:"chatFull"`
		ChatSimple string `json:"chatSimple"`
		MinRole    string `json:"minRole"`
		Schema     any    `json:"schema"`
	}
	rows := make([]row, 0, len(tools))
	for _, tl := range tools {
		rows = append(rows, row{tl.Name, tl.Description, tl.ShortDescription, tl.ChatDescriptionFor(false), tl.ChatDescriptionFor(true), tl.MinRole, tl.InputSchema})
	}
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("katalog serileştirilemedi: %v", err)
	}
	return b
}

func goldenEntry(t *testing.T, tools []mcp.Tool) catalogGoldenEntry {
	sum := sha256.Sum256(serializeCatalog(t, tools))
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	return catalogGoldenEntry{Count: len(tools), SHA256: hex.EncodeToString(sum[:]), Names: names}
}

func goldenRaw(raw string) catalogGoldenEntry {
	sum := sha256.Sum256([]byte(raw))
	return catalogGoldenEntry{Count: -1, SHA256: hex.EncodeToString(sum[:])}
}

// Koşullu bağımlılıkların sahteleri — yalnız "nil değil" olmaları önemli
// (chatOffered). Adlar sentetik.
type goldenWiki struct{}

func (goldenWiki) Search(context.Context, string, string, int, int) (wiki.SearchResult, error) {
	return wiki.SearchResult{}, nil
}
func (goldenWiki) ReadPage(context.Context, string, string, string) (*wiki.PageRecord, error) {
	return nil, nil
}

type goldenOracle struct{}

func (goldenOracle) Enabled() bool { return true }
func (goldenOracle) ResolveOracleOperation(context.Context, string) (OracleOpResolution, error) {
	return OracleOpResolution{}, nil
}

func goldenConditionalDeps() Deps {
	return Deps{
		SourceCode: func(context.Context, SourceCodeRequest) (SourceCodeRead, error) { return SourceCodeRead{}, nil },
		CodeSearch: func(context.Context, string, string, int) (CodeSearchResult, error) { return CodeSearchResult{}, nil },
		Wiki:       goldenWiki{},
		OracleOps:  goldenOracle{},
	}
}

// catalogVariants — altının anahtarları → o varyantın üretimi.
func catalogVariants(t *testing.T, d Deps, prefix string) map[string]catalogGoldenEntry {
	srv := mcp.New("coremetry-test", "v0.0.0-test")
	Register(srv, d)
	return map[string]catalogGoldenEntry{
		prefix + "/tool_list":      goldenEntry(t, ToolList(d)),
		prefix + "/chat":           goldenEntry(t, ChatToolList(d)),
		prefix + "/chat_full":      goldenEntry(t, ChatToolListFor(d, "full")),
		prefix + "/chat_auto":      goldenEntry(t, ChatToolListFor(d, "")),
		prefix + "/chat_simple":    goldenEntry(t, ChatToolListFor(d, "simple")),
		prefix + "/mcp_tools_list": goldenRaw(mcpToolsListRaw(t, srv)),
		// CoSRE v2 araç seti dilim 2 (tools-audit §10 G10) — v2 modelinin
		// gördüğü alt küme; eski anahtarlar bu eklemeyle DEĞİŞMEZ.
		prefix + "/v2": goldenEntry(t, V2ToolList(d, "full")),
	}
}

func readToolsetGolden(t *testing.T) map[string]catalogGoldenEntry {
	t.Helper()
	b, err := os.ReadFile(toolsetGoldenPath)
	if err != nil {
		t.Fatalf("altın okunamadı (-update ile üret): %v", err)
	}
	var want map[string]catalogGoldenEntry
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatalf("altın bozuk: %v", err)
	}
	return want
}

func writeToolsetGolden(t *testing.T, got map[string]catalogGoldenEntry) {
	t.Helper()
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(toolsetGoldenPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toolsetGoldenPath, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func compareGolden(t *testing.T, label string, got, want map[string]catalogGoldenEntry) {
	t.Helper()
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(got) != len(want) {
		t.Errorf("%s: varyant sayısı %d, altın %d", label, len(got), len(want))
	}
	for _, k := range keys {
		g, ok := got[k]
		if !ok {
			t.Errorf("%s: %s varyantı üretilmedi", label, k)
			continue
		}
		w := want[k]
		if g.SHA256 != w.SHA256 || g.Count != w.Count {
			t.Errorf("%s: %s katalog bayt-bayt DEĞİŞTİ (sayı %d → %d, sha %s → %s)", label, k, w.Count, g.Count, w.SHA256[:12], g.SHA256[:12])
		}
		if len(w.Names) > 0 && len(g.Names) == len(w.Names) {
			for i := range w.Names {
				if g.Names[i] != w.Names[i] {
					t.Errorf("%s: %s sıra sapıyor: %d. %q ≠ %q", label, k, i, g.Names[i], w.Names[i])
					break
				}
			}
		}
	}
}

func TestToolsetCatalogGolden(t *testing.T) {
	got := map[string]catalogGoldenEntry{}
	for k, v := range catalogVariants(t, Deps{}, "zero") {
		got[k] = v
	}
	for k, v := range catalogVariants(t, goldenConditionalDeps(), "conditional") {
		got[k] = v
	}
	if *updateToolsetGolden {
		writeToolsetGolden(t, got)
		return
	}
	want := readToolsetGolden(t)
	compareGolden(t, "ToolsetsDisabled=nil", got, want)

	// v0.10.1434 — toolset katmanı "hepsi açık" iken no-op: boş küme, yalnız
	// kapatılamaz adlar, bilinmeyen adlar ve false değerli girdiler kataloğu
	// bayt-bayt DEĞİŞTİRMEZ.
	for label, set := range map[string]map[string]bool{
		"empty":      {},
		"lockedOnly": {ToolsetMeta: true, ToolsetServices: true, ToolsetTraces: true, ToolsetProblems: true},
		"unknown":    {"no-such-toolset": true, "": true},
		"falseVals":  {ToolsetLogs: false, ToolsetDevOpsCode: false},
	} {
		set := set
		with := func(d Deps) Deps { d.ToolsetsDisabled = func() map[string]bool { return set }; return d }
		got := map[string]catalogGoldenEntry{}
		for k, v := range catalogVariants(t, with(Deps{}), "zero") {
			got[k] = v
		}
		for k, v := range catalogVariants(t, with(goldenConditionalDeps()), "conditional") {
			got[k] = v
		}
		compareGolden(t, "ToolsetsDisabled="+label, got, want)
	}
}
