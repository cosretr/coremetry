package api

// evidence_scale_tools_test.go — v0.10.2384: araçların kendi alan tavanları
// döngünün kanıt ölçeğiyle büyür (mcptools.WithEvidenceScale). Döngü düzeyi:
// gerçek araç handler'ı + yürütücü + api tavanı + tur bütçesi. Ağsız, saat ve
// dilimden bağımsız (pencere ISO damgalı / çıpalı).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// estLogStore — sahte log arka ucu: n satır, her gövde bodyRunes rune.
type estLogStore struct {
	logstore.Store
	n, bodyRunes int
	limits       []int
}

func (s *estLogStore) Search(_ context.Context, f logstore.Filter) (*logstore.Page, error) {
	s.limits = append(s.limits, f.Limit)
	p := &logstore.Page{Total: s.n}
	for i := 0; i < s.n && (f.Limit <= 0 || i < f.Limit); i++ {
		p.Logs = append(p.Logs, &logstore.LogRecord{Timestamp: 1_700_000_000_000_000_000 + int64(i), Severity: 17, ServiceName: "orders-api",
			Body: fmt.Sprintf("%04d ", i) + strings.Repeat("ş", s.bodyRunes-5)})
	}
	return p, nil
}
func (s *estLogStore) Histogram(context.Context, logstore.Filter, int, string) ([]logstore.LogSeries, error) {
	return nil, nil
}
func (s *estLogStore) Backend() string { return "stub" }

func estTool(t *testing.T, tools []mcp.Tool, name string) mcp.Tool {
	t.Helper()
	for _, tl := range tools {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("araç yok: %s", name)
	return mcp.Tool{}
}

// estExec — döngünün yürütücüsü (gerçek handler, kancasız).
func estExec(tl mcp.Tool) *agenttools.Executor {
	return agenttools.NewExecutor(map[string]agenttools.Handler{tl.Name: tl.Handler}, nil, agenttools.Hooks{})
}

const estV1Args = `{"limit":10,"from_iso":"2026-09-26T10:00:00Z","to_iso":"2026-09-26T11:00:00Z"}`

func TestToolEvidenceScaleAgreesWithLoopCeiling(t *testing.T) {
	v2 := func(bytes int) *v2LoopPlan { return &v2LoopPlan{Budget: loopBudget{ToolTextBytes: bytes}} }
	for _, c := range []struct {
		name  string
		plan  *v2LoopPlan
		scale int
		want  int
	}{
		{"v1 ölçek 1", nil, 1, 1}, {"v1 ölçek 0", nil, 0, 1}, {"v1 ölçek 8", nil, 8, 8},
		{"v2 ölçek 1", v2(2048), 1, 1}, {"v2 ölçek 8 tam tavan", v2(16384), 8, 8},
		{"v2 operatör 4096'ya daraltmış", v2(4096), 8, 2}, {"v2 operatör 1024'e daraltmış", v2(1024), 8, 1},
		{"v2 ölçek 1 daraltılmış", v2(1024), 1, 1}, {"v2 bayt tavanı yok", v2(0), 8, 8},
	} {
		got := toolEvidenceScale(c.plan, c.scale)
		if got != c.want {
			t.Errorf("%s: %d, beklenen %d", c.name, got, c.want)
		}
		// araç bütçesi (iç sınır) ≤ api tavanı (dış sınır)
		if c.plan != nil && c.plan.Budget.ToolTextBytes >= chatV2ToolTextMaxBytes && mcptools.V2ToolMaxBytes*got > c.plan.Budget.ToolTextBytes {
			t.Errorf("%s: araç bütçesi %d > plan tavanı %d", c.name, mcptools.V2ToolMaxBytes*got, c.plan.Budget.ToolTextBytes)
		}
	}
	// v1 araç bütçeleri aynı çarpanla api tavanının altında.
	for _, scale := range []int{1, 4, 8} {
		if mcptools.SourceResultMaxRunes*scale >= scaleCap(chatToolResultMaxRunes, scale) || mcptools.WikiWindowRunes*scale >= scaleCap(chatToolResultMaxRunes, scale) {
			t.Errorf("ölçek %d: araç bütçesi api tavanını aşıyor", scale)
		}
		if mcptools.V2ToolMaxBytes*scale != scaleCap(chatV2ToolTextMaxBytes, scale) {
			t.Errorf("ölçek %d: v2 araç bütçesi api v2 tavanına eşit olmalı", scale)
		}
	}
}

// v1 döngüsü: ölçek 8'de büyük sonuç api tavanından KESİLMEDEN geçer; ölçeksiz
// çağrı bugünkü gövdeyi verir; tavanı aşan sonuç notla kesilir.
func TestLoopToolResultAtScale8PassesApiCeiling(t *testing.T) {
	st := &estLogStore{n: 10, bodyRunes: 3000}
	tl := estTool(t, mcptools.ToolList(mcptools.Deps{LogStore: st}), "search_logs")

	base := estExec(tl).Call(context.Background(), tl.Name, json.RawMessage(estV1Args))
	if base.IsError || strings.Contains(base.Content, strings.Repeat("ş", 501)) {
		t.Fatalf("ölçeksiz çağrı gövdeyi 500 rune'da kesmeli: err=%v", base.IsError)
	}
	baseClamped, baseCut := clampToolResultForLoopScaled(nil, base.Content, 1)
	if n := esRunes(base.Content); (n > chatToolResultMaxRunes) != baseCut || (baseCut && !strings.HasSuffix(baseClamped, sprintfTruncNote(chatToolResultMaxRunes, n-chatToolResultMaxRunes))) {
		t.Fatalf("taban: %d rune, kesildi=%v", n, baseCut)
	}

	ctx := mcptools.WithEvidenceScale(context.Background(), toolEvidenceScale(nil, 8))
	big := estExec(tl).Call(ctx, tl.Name, json.RawMessage(estV1Args))
	if big.IsError || strings.Count(big.Content, strings.Repeat("ş", 2995)) != 10 || strings.Contains(big.Content, `"body_truncated":true`) {
		t.Fatal("ölçek 8: 10 satırın 3000 rune'luk gövdesi tam taşınmalı")
	}
	out, cut := clampToolResultForLoopScaled(nil, big.Content, 8)
	if cut || out != big.Content || esRunes(out) <= chatToolResultMaxRunes {
		t.Fatalf("ölçek 8: %d rune'luk sonuç api tavanından (%d) kesilmeden geçmeli", esRunes(big.Content), scaleCap(chatToolResultMaxRunes, 8))
	}

	// Tavanı aşan sonuç: api tek kez, notla ve gerçek sayılarla keser.
	huge := &estLogStore{n: 200, bodyRunes: 4000}
	htl := estTool(t, mcptools.ToolList(mcptools.Deps{LogStore: huge}), "search_logs")
	hr := estExec(htl).Call(ctx, htl.Name, json.RawMessage(`{"limit":200,"from_iso":"2026-09-26T10:00:00Z","to_iso":"2026-09-26T11:00:00Z"}`))
	hOut, hCut := clampToolResultForLoopScaled(nil, hr.Content, 8)
	n := esRunes(hr.Content)
	if !hCut || !strings.HasSuffix(hOut, sprintfTruncNote(48000, n-48000)) {
		t.Fatalf("tavanı aşan sonuç 48000 rune + doğru sayılı notla kesilmeli (%d rune)", n)
	}
}

// v2 döngüsü: aracın kendi gövde bütçesi planın tavanıyla aynı sayıya bakar —
// api kesmez. Operatör tavanı daraltmışsa araç da o çarpanla çalışır.
func TestV2LoopToolBudgetIsInsideApiCeiling(t *testing.T) {
	anchor := time.Date(2026, 9, 26, 11, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name     string
		setting  chstore.AILoopBudget
		wantLim  int
		wantBody int // satır gövdesinin rune tavanı
	}{
		{"tam tavan", chstore.AILoopBudget{}, 80, 1280},
		{"operatör 4096", chstore.AILoopBudget{ToolTextBytes: 4096}, 20, 320},
	} {
		st := &estLogStore{n: 200, bodyRunes: 1500}
		tl := estTool(t, mcptools.V2OnlyTools(mcptools.Deps{LogStore: st}), mcptools.SearchLogsV2Name)
		plan := &v2LoopPlan{Budget: narrowLoopBudget(c.setting, v2LoopCeilings(deepMode{}, time.Minute, 8))}
		ctx := mcptools.WithAnchor(mcptools.WithEvidenceScale(context.Background(), toolEvidenceScale(plan, 8)), anchor)
		oc := estExec(tl).Call(ctx, tl.Name, json.RawMessage(`{"service":"orders-api"}`))
		if oc.IsError {
			t.Fatalf("%s: %s", c.name, oc.Content)
		}
		if got := st.limits[0]; got != c.wantLim {
			t.Errorf("%s: arka uç LIMIT %d, beklenen %d", c.name, got, c.wantLim)
		}
		if len(oc.Content) > plan.Budget.ToolTextBytes {
			t.Fatalf("%s: araç gövdesi %d B > plan tavanı %d B", c.name, len(oc.Content), plan.Budget.ToolTextBytes)
		}
		out, cut := clampToolResultForLoopScaled(plan, oc.Content, 8)
		if cut || out != oc.Content {
			t.Fatalf("%s: api, aracın kendi bütçesine sığdırdığı gövdeyi kesmemeli", c.name)
		}
		// araç kesti → kendi bayrağıyla söyledi (satır düştü, gövde tavanda)
		var body struct {
			Truncated bool `json:"truncated"`
			Count     int  `json:"count"`
			Rows      []struct {
				Body string `json:"body"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(oc.Content), &body); err != nil {
			t.Fatal(err)
		}
		if !body.Truncated || body.Count != len(body.Rows) || body.Count == 0 || esRunes(body.Rows[0].Body) != c.wantBody+1 {
			t.Fatalf("%s: truncated=%v count=%d gövde=%d rune", c.name, body.Truncated, body.Count, esRunes(body.Rows[0].Body))
		}
	}
	// ölçek 1: bugünkü okuma ve bugünkü gövde.
	st := &estLogStore{n: 200, bodyRunes: 1500}
	tl := estTool(t, mcptools.V2OnlyTools(mcptools.Deps{LogStore: st}), mcptools.SearchLogsV2Name)
	plan := &v2LoopPlan{Budget: narrowLoopBudget(chstore.AILoopBudget{}, v2LoopCeilings(deepMode{}, time.Minute, 1))}
	oc := estExec(tl).Call(mcptools.WithAnchor(mcptools.WithEvidenceScale(context.Background(), toolEvidenceScale(plan, 1)), anchor), tl.Name, json.RawMessage(`{"service":"orders-api"}`))
	if st.limits[0] != 10 || len(oc.Content) > chatV2ToolTextMaxBytes || strings.Contains(oc.Content, strings.Repeat("ş", 161)) {
		t.Fatalf("ölçek 1: LIMIT %d, %d B", st.limits[0], len(oc.Content))
	}
}

// Patolojik konuşma: altı turun her biri ölçek 8'de gerçek araçtan gelen ve
// api tavanında kesilmiş sonucu taşır — bütün-istem koruması yine tutar
// (konuşma taban bütçeye iner, notlar özgün sayılarla).
func TestWholePromptGuardHoldsWithScaledToolResults(t *testing.T) {
	st := &estLogStore{n: 200, bodyRunes: 4000}
	tl := estTool(t, mcptools.ToolList(mcptools.Deps{LogStore: st}), "search_logs")
	ctx := mcptools.WithEvidenceScale(context.Background(), toolEvidenceScale(nil, 8))
	var conv []copilot.ChatMessage
	for i := 0; i < 31; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		conv = append(conv, copilot.ChatMessage{Role: role, Text: esText(fmt.Sprintf("tur%02d", i), 1500)})
	}
	conv = append(conv, copilot.ChatMessage{Role: "user", Text: "hepsini birlikte yorumla"})
	var calls []copilot.ToolCall
	var results []copilot.ToolResult
	var fullRunes int
	for i := 0; i < 6; i++ {
		args := json.RawMessage(fmt.Sprintf(`{"limit":200,"query":"q%d","from_iso":"2026-09-26T10:00:00Z","to_iso":"2026-09-26T11:00:00Z"}`, i))
		oc := estExec(tl).Call(ctx, tl.Name, args)
		if oc.IsError {
			t.Fatal(oc.Content)
		}
		fullRunes = esRunes(oc.Content)
		content, cut := clampToolResultForLoopScaled(nil, oc.Content, 8)
		if !cut {
			t.Fatal("fikstür api tavanını aşmalıydı")
		}
		id, name := fmt.Sprintf("c%d", i), fmt.Sprintf("custom_tool_%d", i) // her biri kendi ailesi → hepsi korunur
		calls = append(calls, copilot.ToolCall{ID: id, Name: name, Input: args})
		results = append(results, copilot.ToolResult{CallID: id, Name: name, Content: content})
	}
	conv = append(conv, copilot.ChatMessage{Role: "assistant", ToolCalls: calls}, copilot.ChatMessage{Role: "user", ToolResults: results})

	s := &Server{copilot: esCop(esQwen)} // ağsız: pencere ad tablosundan (262144), ölçek 8
	sys := esText("sistem", 20000)
	maxTok := s.roundBudgetMaxTokens(context.Background())
	lim := roundBudgetLimit(262144, maxTok)
	if est := roundEstimate(len(sys)+chatConvBytes(conv), 0, 0, 0); est <= lim {
		t.Fatalf("fikstür patolojik değil: tahmin %d ≤ sınır %d", est, lim)
	}
	out := s.fitRoundBudget(withRoundBudget(context.Background(), &roundBudget{}), func(string, any) {}, sys, conv, nil)
	if est := roundEstimate(len(sys)+chatConvBytes(out), 0, 0, 0); est > lim || est+maxTok > 262144 {
		t.Fatalf("bütün-istem koruması tutmadı: tahmin %d > sınır %d", est, lim)
	}
	pairsIntact(t, out)
	last := out[len(out)-1].ToolResults
	if len(last) != 6 {
		t.Fatalf("son turun 6 sonucu durmalı: %d", len(last))
	}
	want := sprintfTruncNote(chatToolResultMaxRunes, fullRunes-chatToolResultMaxRunes)
	for _, tr := range last {
		if !strings.HasSuffix(tr.Content, want) {
			t.Fatalf("sonuç taban tavana inmeli, not ÖZGÜN sonucun sayılarıyla: …%s", tr.Content[len(tr.Content)-140:])
		}
	}
}

// Bağ pini: ölçek araç ctx'ine döngünün üç yürütme noktasında yazılır;
// exception kod takibi döngüsü (tur bütçesinden geçmez) tabanda kalır.
func TestEvidenceScaleReachesToolContext(t *testing.T) {
	for file, want := range map[string]string{
		"copilot_chat.go":             "ctx := mcptools.WithEvidenceScale(ctx, toolEvidenceScale(v2Plan, evScale))",
		"chat_suggestions.go":         "exec.Call(mcptools.WithEvidenceScale(ctx, toolEvidenceScale(v2LoopPlanFrom(ctx), s.evidenceScale(ctx))), tool, args)",
		"chat_approval_resume.go":     "exec.Call(mcptools.WithEvidenceScale(ctx, toolEvidenceScale(v2LoopPlanFrom(ctx), s.evidenceScale(ctx))), ans.Tool, ans.Args)",
		"copilot_exception_oracle.go": "anomaly.BuildExceptionExplainInputScaled(ctx, s.store, s.logs, g, loc, s.evidenceScale(ctx))",
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s: %q yok — ölçek araca ulaşmıyor", file, want)
		}
	}
	b, err := os.ReadFile("chat_exception_followup.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "WithEvidenceScale") {
		t.Error("exception kod takibi döngüsü tur bütçesinden geçmez: araç sonucu tabanda kalmalı")
	}
}
