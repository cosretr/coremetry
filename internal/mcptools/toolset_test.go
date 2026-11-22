package mcptools

// v0.10.1302 — CoSRE: modele göre araç seti (küçük modelde serbest-sorgu
// araçları gizli). simple varyant dört aracı sohbetten çıkarır; full varyant,
// ChatToolList ve dış MCP (Register → tools/list) onları taşımaya devam eder.
// simple katalogdaki hiçbir kompakt metin gizli bir aracı anmaz ve hiçbir
// işlenmiş metinde işaret (⟦ ⟧) kalmaz.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcp"
)

var simpleHiddenNames = []string{"query_metric", "search_logs", "search_traces", "list_metric_labels"}

func toolNames(ts []mcp.Tool) map[string]bool {
	out := map[string]bool{}
	for _, t := range ts {
		out[t.Name] = true
	}
	return out
}

func TestChatToolListForHidesFreeFormToolsOnlyInSimple(t *testing.T) {
	if len(simpleHiddenTools) != len(simpleHiddenNames) {
		t.Fatalf("simpleHiddenTools %d ad, beklenen %d", len(simpleHiddenTools), len(simpleHiddenNames))
	}
	chat := ChatToolList(Deps{})
	full := ChatToolListFor(Deps{}, "full")
	auto := ChatToolListFor(Deps{}, "")
	simple := ChatToolListFor(Deps{}, "simple")
	inChat, inFull, inAuto, inSimple := toolNames(chat), toolNames(full), toolNames(auto), toolNames(simple)
	for _, n := range simpleHiddenNames {
		if !inChat[n] || !inFull[n] || !inAuto[n] {
			t.Errorf("%s ChatToolList / full / boş varyantta olmalı", n)
		}
		if inSimple[n] {
			t.Errorf("%s simple varyantta GİZLİ olmalı", n)
		}
	}
	if len(full) != len(chat) || len(simple) != len(chat)-len(simpleHiddenNames) {
		t.Fatalf("boylar: chat %d full %d simple %d", len(chat), len(full), len(simple))
	}
	// Yapısal karşılıklar simple'da kalır.
	for _, n := range []string{"query_metrics", "query_logs", "query_traces", "list_metric_names", "trace_stats"} {
		if !inSimple[n] {
			t.Errorf("%s simple varyantta olmalı", n)
		}
	}
	// Sıra korunur: simple, ChatToolList'in alt dizisi.
	i := 0
	for _, tl := range chat {
		if simpleHiddenTools[tl.Name] {
			continue
		}
		if simple[i].Name != tl.Name {
			t.Fatalf("simple sıra sapıyor: %d. %q ≠ %q", i, simple[i].Name, tl.Name)
		}
		i++
	}
	for i := range chat {
		if full[i].Name != chat[i].Name {
			t.Fatalf("full sıra sapıyor: %d", i)
		}
	}
}

// Dış MCP etkilenmez: tools/list gizli dördü de servis eder.
func TestRegisterStillServesHiddenTools(t *testing.T) {
	srv := mcp.New("coremetry-test", "v0.0.0-test")
	Register(srv, Deps{})
	var out struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(mcpToolsListRaw(t, srv)), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tl := range out.Result.Tools {
		got[tl.Name] = true
		if strings.ContainsAny(tl.Description, "⟦⟧") {
			t.Errorf("%s: tools/list açıklamasında araç seti işareti", tl.Name)
		}
	}
	for _, n := range simpleHiddenNames {
		if !got[n] {
			t.Errorf("dış MCP tools/list %s'i servis etmeli", n)
		}
	}
}

var hiddenToolNameRe = regexp.MustCompile(`\b(query_metric|search_logs|search_traces|list_metric_labels)\b`)

func TestSimpleChatDescriptionsNeverNameHiddenTools(t *testing.T) {
	marked := 0
	for _, tl := range ToolList(Deps{}) {
		if strings.Contains(tl.ShortDescription, "⟦") {
			marked++
		}
		for _, simple := range []bool{false, true} {
			if txt := tl.ChatDescriptionFor(simple); strings.ContainsAny(txt, "⟦⟧") {
				t.Errorf("%s (simple=%v): işlenmiş metinde işaret kaldı (bozuk ⟦full|simple⟧?): %q", tl.Name, simple, txt)
			}
		}
		if simpleHiddenTools[tl.Name] || externalOnlyTools[tl.Name] {
			continue
		}
		if m := hiddenToolNameRe.FindString(tl.ChatDescriptionFor(true)); m != "" {
			t.Errorf("%s: simple kompakt metin gizli aracı anıyor (%s): %q", tl.Name, m, tl.ChatDescriptionFor(true))
		}
	}
	// query_metrics / query_logs / query_traces / list_metric_names /
	// get_log_histogram / list_clusters — altı işaretli metin.
	if marked != 6 {
		t.Errorf("işaretli kompakt metin %d, beklenen 6", marked)
	}
}

// mcpToolsListRaw — dış MCP tools/list cevabının ham gövdesi (Streamable-HTTP).
func mcpToolsListRaw(t *testing.T, srv *mcp.Server) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/mcp", srv.HandleStreamable)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/mcp", "application/json",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ── v0.10.1434 — kaynak bazlı toolset'ler (CoSRE araştırma PR 1, §6.1) ──

// Her araç TAM BİR toolset'te; birleşim = ToolList ad kümesi (75; CoSRE faz D compare_baseline); 12 set,
// sabit sıra ve §6.1 tablosundaki sayılar.
func TestToolsetsPartitionCatalog(t *testing.T) {
	catalog := ToolList(goldenConditionalDeps())
	if len(catalog) != 80 {
		t.Fatalf("katalog %d araç, 80 bekleniyordu", len(catalog))
	}
	owner := map[string]string{}
	for _, ts := range Toolsets {
		for _, n := range ts.Tools {
			if prev, dup := owner[n]; dup {
				t.Errorf("%s iki sette: %s ve %s", n, prev, ts.Name)
			}
			owner[n] = ts.Name
		}
	}
	for _, tl := range catalog {
		if owner[tl.Name] == "" {
			t.Errorf("%s hiçbir toolset'te değil", tl.Name)
		}
		if ToolsetOfTool(tl.Name) != owner[tl.Name] {
			t.Errorf("ToolsetOfTool(%s) = %q", tl.Name, ToolsetOfTool(tl.Name))
		}
	}
	if len(owner) != len(catalog) {
		t.Errorf("toolset'lerde %d ad, katalogda %d — bayat ad var", len(owner), len(catalog))
	}
	want := []struct {
		name     string
		n        int
		required bool
	}{
		{ToolsetServices, 22, true}, {ToolsetTraces, 13, true}, {ToolsetProblems, 10, true}, {ToolsetChanges, 4, false},
		{ToolsetMetrics, 6, false}, {ToolsetLogs, 8, false}, {ToolsetK8s, 4, false}, {ToolsetOracle, 1, false},
		{ToolsetDevOpsCode, 2, false}, {ToolsetDevOpsWiki, 2, false}, {ToolsetKnowledge, 3, false}, {ToolsetMeta, 5, true},
	}
	if len(Toolsets) != len(want) {
		t.Fatalf("%d toolset, %d bekleniyordu", len(Toolsets), len(want))
	}
	for i, w := range want {
		ts := Toolsets[i]
		if ts.Name != w.name || len(ts.Tools) != w.n || ts.Required != w.required || ts.Health == nil || ts.Source == "" {
			t.Errorf("%d. toolset %s (%d araç, required=%v, health=%v) — beklenen %s (%d, %v)",
				i, ts.Name, len(ts.Tools), ts.Required, ts.Health != nil, w.name, w.n, w.required)
		}
	}
}

func TestToolsetFilterKeepsOrder(t *testing.T) {
	all := ToolList(Deps{})
	if got := filterByToolsets(all, nil); len(got) != len(all) || &got[0] != &all[0] {
		t.Fatal("boş küme girdi dilimini AYNEN döndürmeli (kopya bile yok)")
	}
	got := filterByToolsets(all, map[string]bool{ToolsetLogs: true, ToolsetK8s: true})
	i := 0
	for _, tl := range all {
		ts := ToolsetOfTool(tl.Name)
		if ts == ToolsetLogs || ts == ToolsetK8s {
			continue
		}
		if got[i].Name != tl.Name {
			t.Fatalf("sıra sapıyor: %d. %q ≠ %q", i, got[i].Name, tl.Name)
		}
		i++
	}
	if i != len(got) || len(got) != len(all)-12 {
		t.Fatalf("süzülmüş %d araç, beklenen %d", len(got), len(all)-12)
	}
}

func TestMetaToolsetCannotBeDisabled(t *testing.T) {
	for _, name := range []string{ToolsetMeta, ToolsetServices, ToolsetTraces, ToolsetProblems} {
		if !ToolsetLocked(name) {
			t.Errorf("%s kapatılamaz olmalı", name)
		}
		raw := map[string]bool{name: true}
		if effectiveDisabled(raw) != nil {
			t.Errorf("%s etkin kapalı kümeye girdi", name)
		}
		d := Deps{ToolsetsDisabled: func() map[string]bool { return raw }}
		if len(ChatToolList(d)) != len(ChatToolList(Deps{})) {
			t.Errorf("%s kapatma denemesi sohbet kataloğunu değiştirdi", name)
		}
		ts, _ := ToolsetOf(name)
		for _, tool := range ts.Tools {
			if _, off := ToolDisabledBy(tool, raw); off {
				t.Errorf("%s/%s kapı reddi üretti", name, tool)
			}
		}
	}
	if ToolsetLocked("no-such-toolset") || ToolsetLocked(ToolsetLogs) {
		t.Error("bilinmeyen / isteğe bağlı set kilitli sayılmamalı")
	}
}

// Kapalı toolset yalnız KENDİ araçlarını düşürür: sohbet kataloğunda (full +
// simple) yok, diğer araçlar aynı sırada; ToolList ve dış MCP tools/list
// değişmez (kapı reddeder — api/mcp_gate.go); get_capabilities enabled:false.
func TestDisabledToolsetHidesOnlyItsTools(t *testing.T) {
	base := goldenConditionalDeps()
	baseTools := ToolList(base)
	srv0 := mcp.New("coremetry-test", "v0.0.0-test")
	Register(srv0, base)
	baseList := mcpToolsListRaw(t, srv0)
	for _, ts := range Toolsets {
		if ts.Required {
			continue
		}
		raw := map[string]bool{ts.Name: true}
		d := base
		d.ToolsetsDisabled = func() map[string]bool { return raw }
		mine := map[string]bool{}
		for _, n := range ts.Tools {
			mine[n] = true
		}
		for _, variant := range []string{"full", "simple"} {
			before, after := ChatToolListFor(base, variant), ChatToolListFor(d, variant)
			var want []string
			for _, tl := range before {
				if !mine[tl.Name] {
					want = append(want, tl.Name)
				}
			}
			if len(after) != len(want) {
				t.Fatalf("%s/%s: %d araç, beklenen %d", ts.Name, variant, len(after), len(want))
			}
			for i := range want {
				if after[i].Name != want[i] {
					t.Fatalf("%s/%s: %d. %q ≠ %q", ts.Name, variant, i, after[i].Name, want[i])
				}
			}
		}
		for _, n := range ts.Tools {
			if got, off := ToolDisabledBy(n, raw); !off || got != ts.Name {
				t.Errorf("%s: kapı %s'i reddetmeli", ts.Name, n)
			}
		}
		if len(ToolList(d)) != len(baseTools) {
			t.Errorf("%s: ToolList süzülmemeli (dış MCP kayıt defteri)", ts.Name)
		}
		srv := mcp.New("coremetry-test", "v0.0.0-test")
		Register(srv, d)
		if mcpToolsListRaw(t, srv) != baseList {
			t.Errorf("%s: dış MCP tools/list boot anlık görüntüsü değişmemeli", ts.Name)
		}
		caps := toolByName(t, ToolList(d), "get_capabilities")
		out, err := caps.Handler(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, st := range out.(map[string]any)["toolsets"].([]ToolsetState) {
			if st.Name == ts.Name {
				found = true
				if st.Enabled {
					t.Errorf("%s: get_capabilities enabled:false göstermeli", ts.Name)
				}
			} else if !st.Enabled {
				t.Errorf("%s kapatılınca %s de kapalı göründü", ts.Name, st.Name)
			}
		}
		if !found {
			t.Errorf("%s get_capabilities.toolsets'te yok", ts.Name)
		}
	}
}

// get_capabilities eski üç alanı aynen taşır; toolsets ek alan, 12 satır.
func TestGetCapabilitiesToolsetsAdditive(t *testing.T) {
	out, err := toolByName(t, ToolList(Deps{}), "get_capabilities").Handler(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	for _, k := range []string{"capabilities", "enabled", "disabled", "toolsets"} {
		if _, ok := m[k]; !ok {
			t.Errorf("get_capabilities %q alanı yok", k)
		}
	}
	if sts := m["toolsets"].([]ToolsetState); len(sts) != 12 {
		t.Errorf("toolsets %d satır, 12 bekleniyordu", len(sts))
	}
}
