package copilot

import (
	"strings"
	"testing"
)

// CoSRE faz D — "ne değişti / neden yavaşladı" sorusu compare_baseline'a
// yönlenir; v1, v2 ve simple/full çekirdeklerinin hepsinde.
func TestChatAgentLoopOffersCompareBaseline(t *testing.T) {
	for name, p := range map[string]string{
		"v1":        SystemPromptChatAgentLoop(),
		"v1/simple": SystemPromptChatAgentLoopFor(true),
		"v2/full":   SystemPromptChatAgentLoopV2For(false),
		"v2/simple": SystemPromptChatAgentLoopV2For(true),
	} {
		if !strings.Contains(p, "compare_baseline") || !strings.Contains(p, "neden yavaşladı") {
			t.Errorf("%s: compare_baseline yönlendirmesi yok", name)
		}
	}
}

// v0.10.2449 — endpoint / trace-nerede / Kafka / aşağı bağımlılık / repo
// soruları yeni araçlara yönlenir; v1, v2 ve simple/full çekirdeklerinin hepsinde.
func TestChatAgentLoopOffersLookupTools(t *testing.T) {
	for name, p := range map[string]string{
		"v1":        SystemPromptChatAgentLoop(),
		"v1/simple": SystemPromptChatAgentLoopFor(true),
		"v2/full":   SystemPromptChatAgentLoopV2For(false),
		"v2/simple": SystemPromptChatAgentLoopV2For(true),
	} {
		for _, tool := range []string{"endpoint_lookup", "trace_where", "messaging_flow", "downstream_calls", "repo_lookup", "dependency_path"} { // v0.10.2453 dependency_path
			if !strings.Contains(p, tool) {
				t.Errorf("%s: %s yönlendirmesi yok", name, tool)
			}
		}
	}
}

// v0.10.482 — ajan çekirdek döngüsü: kataloğun gerçek tool adlarını anar,
// Ek A düzeltmeleri (N1 cluster attribute'tan; N5 OR yerine anahtar başına
// arama) uygulanmış; systemChat'i yeniden YAZMAZ (ayrı const, öne eklenir).
func TestChatAgentLoopPrompt(t *testing.T) {
	p := SystemPromptChatAgentLoop()
	for _, tool := range []string{"resolve_entity", "describe_attributes", "find_attribute_by_value", "search_traces", "trace_stats", "search_logs", "build_link", "set_context", "get_context", "list_slo_status"} {
		if !strings.Contains(p, tool) {
			t.Errorf("tool adı %q prompt'ta yok", tool)
		}
	}
	for _, bad := range []string{"prod-<cluster", "OR ile", " OR "} {
		if strings.Contains(p, bad) {
			t.Errorf("Ek A düzeltmesi uygulanmamış: %q", bad)
		}
	}
	if strings.Contains(SystemPromptChat(), "ÇEKİRDEK DÖNGÜ") {
		t.Error("çekirdek döngü systemChat'e gömülmemeli (ayrı const, öne eklenir)")
	}
	if !strings.Contains(p, "telemetri yok") || !strings.Contains(p, "hangi cluster") {
		t.Error("dürüstlük kuralları (telemetri yok ≠ workload yok; hangi cluster) prompt'ta olmalı")
	}
}
