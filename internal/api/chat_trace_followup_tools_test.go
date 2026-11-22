package api

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/wiki"
)

// chat_trace_followup_tools_test.go — v0.10.2296: trace takibinin dar kataloğu.

type tfuWiki struct{}

func (tfuWiki) Search(context.Context, string, string, int, int) (wiki.SearchResult, error) {
	return wiki.SearchResult{}, nil
}
func (tfuWiki) ReadPage(context.Context, string, string, string) (*wiki.PageRecord, error) {
	return nil, nil
}

type tfuOracle struct{}

func (tfuOracle) Enabled() bool { return true }
func (tfuOracle) ResolveOracleOperation(context.Context, string) (mcptools.OracleOpResolution, error) {
	return mcptools.OracleOpResolution{}, nil
}

// tfuAllDeps — koşullu araçların hepsi açık (kod, kod araması, wiki, Oracle).
func tfuAllDeps() mcptools.Deps {
	return mcptools.Deps{
		SourceCode: func(context.Context, mcptools.SourceCodeRequest) (mcptools.SourceCodeRead, error) {
			return mcptools.SourceCodeRead{}, nil
		},
		CodeSearch: func(context.Context, string, string, int) (mcptools.CodeSearchResult, error) {
			return mcptools.CodeSearchResult{}, nil
		},
		Wiki:      tfuWiki{},
		OracleOps: tfuOracle{},
	}
}

// tfuAllToolNames — sınıflandırılması gereken her yerli ad: toolset bölümü
// (her araç tam bir sette) + v2-yalnız araçlar.
func tfuAllToolNames() []string {
	var out []string
	for _, ts := range mcptools.Toolsets {
		out = append(out, ts.Tools...)
	}
	for _, t := range mcptools.V2OnlyTools(tfuAllDeps()) {
		out = append(out, t.Name)
	}
	return out
}

// Yeni bir araç eklendiğinde trace takibinde kalıp kalmayacağı BİLİNÇLİ seçilsin.
func TestTraceFollowUpToolsClassified(t *testing.T) {
	for _, n := range tfuAllToolNames() {
		k, d, p := traceFollowUpKeepTools[n], traceFollowUpDropTools[n], traceFollowUpPromptPinned[n] != ""
		cnt := 0
		for _, b := range []bool{k, d, p} {
			if b {
				cnt++
			}
		}
		if cnt == 0 {
			t.Errorf("%q trace takibi için sınıflandırılmamış: internal/api/chat_trace_followup_tools.go içinde "+
				"traceFollowUpKeepTools'a (bu trace'i açıklamaya yardım eder) ya da traceFollowUpDropTools'a ekle", n)
		}
		if cnt > 1 {
			t.Errorf("%q birden çok listede (keep=%v drop=%v promptPinned=%v)", n, k, d, p)
		}
	}
	known := map[string]bool{}
	for _, n := range tfuAllToolNames() {
		known[n] = true
	}
	for _, m := range []map[string]bool{traceFollowUpKeepTools, traceFollowUpDropTools} {
		for n := range m {
			if !known[n] {
				t.Errorf("%q artık katalogda yok — listeden sil", n)
			}
		}
	}
	for n := range traceFollowUpPromptPinned {
		if !known[n] {
			t.Errorf("%q artık katalogda yok — traceFollowUpPromptPinned'dan sil", n)
		}
	}
}

func tfuNames(tools []mcp.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

// Kalan küme AYNEN pinli (tam katalog, koşullu araçlar açık).
func TestTraceFollowUpToolsExactKeptSet(t *testing.T) {
	want := []string{
		// services
		"get_service_health", "get_operation_health", "get_topology", "get_db_health", "get_messaging_health",
		"compare_periods", "compare_baseline", "render_chart", "resolve_business_code", "list_slo_status",
		// traces
		"describe_attributes", "find_attribute_by_value", "get_trace", "search_traces", "query_traces", "trace_stats",
		"find_trace_by_span", "pivot_from_trace", "get_exemplar_traces", "get_linked_traces", "get_metrics_for_span",
		// v0.10.2449
		"trace_where", "endpoint_lookup", "messaging_flow", "downstream_calls", "repo_lookup",
		"dependency_path", // v0.10.2453
		// exceptions
		"list_exception_groups", "get_exception_samples",
		// changes
		"get_correlated_changes", "get_deploy_diff", "list_deploys", "list_deployments",
		// metrics
		"get_pod_health", "query_metrics", "query_metric", "list_metric_names", "list_metric_labels", "cluster_metric",
		// logs
		"query_logs", "search_logs", "list_log_fields", "get_log_histogram", "explain_log_spike",
		"find_trace_by_request_id", "log_patterns", "get_logs_for_trace",
		// k8s / knowledge / meta — çekirdek prompt adıyla çağırtıyor
		"resolve_entity", "product_guide", "build_link", "set_context", "get_context",
		// oracle / kod / wiki
		"resolve_oracle_operation", "search_code", "read_source_code", "search_wiki", "read_wiki_page",
	}
	sort.Strings(want)
	full := mcptools.ChatToolListFor(tfuAllDeps(), modelcaps.ToolsetFull)
	got := tfuNames(traceFollowUpToolsFor(full, true))
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("trace takibi kataloğu değişti:\n got=%v\nwant=%v", got, want)
	}
	// simple: aynı küme eksi simple'da gizli serbest-sorgu araçları.
	simple := tfuNames(traceFollowUpToolsFor(mcptools.ChatToolListFor(tfuAllDeps(), modelcaps.ToolsetSimple), true))
	for _, n := range simple {
		if traceFollowUpDropTools[n] {
			t.Errorf("simple varyantta düşmesi gereken araç kaldı: %s", n)
		}
	}
	// v2 birleşimi: v2 araçları da aynı süzgeçten geçer.
	v2 := tfuNames(traceFollowUpToolsFor(mcptools.V2MergeTools(full, mcptools.V2OnlyTools(tfuAllDeps())), true))
	for _, want := range []string{"find_traces", "explain_trace", "endpoint_metrics", "get_log_anomalies", "list_my_services", "open_problems"} {
		if !hasToolName(v2, want) {
			t.Errorf("v2 trace takibinde %s yok", want)
		}
	}
	if hasToolName(v2, "explain_ml_event") {
		t.Error("v2 trace takibinde explain_ml_event kaldı")
	}
}

// Takip değilse dilim AYNEN döner (bağımsız sohbet / kod turu bayt bayt).
func TestTraceFollowUpToolsNoopOutsideFollowUp(t *testing.T) {
	full := mcptools.ChatToolListFor(tfuAllDeps(), modelcaps.ToolsetFull)
	got := traceFollowUpToolsFor(full, false)
	if len(got) != len(full) || &got[0] != &full[0] {
		t.Fatal("takip dışında katalog kopyalandı ya da değişti")
	}
}

// tfuCatalogBytes — modele giden tools JSON'u (OpenAI biçimi; copilot_chat.go spec kurulumu).
func tfuCatalogBytes(t *testing.T, tools []mcp.Tool, simple bool) int {
	t.Helper()
	wire := make([]any, 0, len(tools))
	for _, tl := range tools {
		wire = append(wire, map[string]any{"type": "function", "function": map[string]any{
			"name": tl.Name, "description": tl.ChatDescriptionFor(simple),
			"parameters": mcp.RenderSchemaToolsetMarkers(tl.InputSchema, simple),
		}})
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return len(raw)
}

// Ölçüm: katalog baytı önce → sonra (tam ve simple). Daralma en az %20
// (v0.10.2296: full 73 araç 67202 B → 50 araç 49718 B; simple 69 → 46, 60682 → 43199 B).
func TestTraceFollowUpToolsCatalogBytes(t *testing.T) {
	for _, ts := range []string{modelcaps.ToolsetFull, modelcaps.ToolsetSimple} {
		simple := ts == modelcaps.ToolsetSimple
		all := mcptools.ChatToolListFor(tfuAllDeps(), ts)
		kept := traceFollowUpToolsFor(all, true)
		before, after := tfuCatalogBytes(t, all, simple), tfuCatalogBytes(t, kept, simple)
		t.Logf("%s: %d araç %d B → %d araç %d B", ts, len(all), before, len(kept), after)
		if after*10 > before*8 {
			t.Errorf("%s: katalog yeterince daralmadı: %d → %d B", ts, before, after)
		}
	}
}

// tfuToolNameRE — prompt metninde araç adı (kelime sınırlı; get_problem ≠ get_problem_root_cause).
func tfuToolNameRE(name string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^a-z0-9_])` + regexp.QuoteMeta(name) + `($|[^a-z0-9_])`)
}

// Trace takibinde sunulabilen hiçbir prompt düşmüş bir aracı ADIYLA anmaz.
// (ChatUserScopeAnchoredAddendum "list_teams çağırma" der — yasak, çağrı
// talimatı değil; bilinçli dışarıda.)
func TestTraceFollowUpPromptsDoNotNameDroppedTools(t *testing.T) {
	tf := traceFollowUp{TraceID: tfTrace}
	prompts := map[string]string{}
	for _, simple := range []bool{false, true} {
		sfx := map[bool]string{false: "/full", true: "/simple"}[simple]
		prompts["SystemPromptChatAgentLoopFor"+sfx] = copilot.SystemPromptChatAgentLoopFor(simple)
		prompts["TraceFollowUpAddendumFor"+sfx] = copilot.TraceFollowUpAddendumFor(simple)
		tf.Simple = simple
		prompts["traceFollowUpPromptTR"+sfx] = traceFollowUpPromptTR(&tf, "önceki açıklama")
	}
	prompts["SystemPromptChat"] = copilot.SystemPromptChat()
	prompts["SourceCodeChatAddendum"] = copilot.SourceCodeChatAddendum()
	prompts["WikiChatAddendum"] = copilot.WikiChatAddendum()
	prompts["CodeResolveChatAddendum"] = copilot.CodeResolveChatAddendum(true)
	prompts["ChatDeepAddendum"] = copilot.ChatDeepAddendum()
	prompts["ChatRoundCapAddendum"] = copilot.ChatRoundCapAddendum()
	prompts["ChatDegenRetryAddendum"] = copilot.ChatDegenRetryAddendum()
	prompts["ChatGeneralQuestionAddendum"] = copilot.ChatGeneralQuestionAddendum()
	prompts["ChatUserScopeAddendum"] = copilot.ChatUserScopeAddendum("team-a", 3)
	for name := range traceFollowUpDropTools {
		re := tfuToolNameRE(name)
		for pn, p := range prompts {
			if re.MatchString(p) {
				t.Errorf("%s trace takibinde düşen %s aracını adıyla anıyor", pn, name)
			}
		}
	}
	// Ters yön: çekirdeğin andığı araç gerçekten anılıyor (pin listesi bayatlamasın).
	core := prompts["SystemPromptChatAgentLoopFor/full"] + prompts["ChatUserScopeAddendum"]
	for name, where := range traceFollowUpPromptPinned {
		if !tfuToolNameRE(name).MatchString(core) {
			t.Errorf("%s artık anılmıyor (%s) — traceFollowUpPromptPinned'dan drop'a taşı", name, where)
		}
	}
}

// Düşen aracı model yine de isterse bugünkü dürüst "unknown tool" yolu; araç
// koşmaz; katalogda yok; sistem mesajı düşen araçları anmaz.
func TestTraceFollowUpLoopDroppedToolIsUnknown(t *testing.T) {
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		if n == 0 {
			return toolCallMsg([2]string{"list_problems", `{}`})
		}
		return answerMsg("**Bulgu** — tamam.")
	})
	s, _ := newLoopTestServer(t, llm, "u-a")
	ft := &fakeTools{}
	ft.install(t)
	postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Neden yavaş?", anchoredToMs(), "conv-1"))
	calls := llm.calls()
	if len(calls) < 2 {
		t.Fatalf("model %d kez çağrıldı, ≥2 bekleniyordu", len(calls))
	}
	for i, b := range calls {
		names := llmToolNames(b)
		for _, n := range names {
			if traceFollowUpDropTools[n] {
				t.Errorf("çağrı %d: düşen araç katalogda: %s", i, n)
			}
		}
		if !hasToolName(names, "get_logs_for_trace") {
			t.Errorf("çağrı %d: get_logs_for_trace yok", i)
		}
	}
	if !strings.Contains(llmMessagesText(calls[1]), "unknown tool") {
		t.Fatal("düşen araç çağrısı \"unknown tool\" ile dönmedi")
	}
	for _, c := range ft.snapshot() {
		if c.name == "list_problems" {
			t.Fatal("düşen araç yürütüldü")
		}
	}
}
