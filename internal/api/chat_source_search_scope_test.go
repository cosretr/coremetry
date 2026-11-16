package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// v0.10.2294 — operatör: trace takibinde çağrılan servisin span'inde
// peer.service yoktu (yalnız host hedefi); search_code dosyayı buldu,
// read_source_code reddetti. Kural: AYNI alışverişte search_code deposunu
// çözdüyse (ok / no_hits — kod turunun kuralı, scopeServicesFrom) servis o
// alışverişin geri kalanında dal ucundan okunur. Trace takibinde kapsamı
// YALNIZ search_code besler; kod turunun besleyicileri burada etkisiz.

// scTraceSearchFeed — trace takibinin kapsam sarmalayıcısından sahte bir araç
// sonucu geçirir (gerçek handler yerine sabit sonuç).
func scTraceSearchFeed(t *testing.T, ctx context.Context, tool, args string, res any) {
	t.Helper()
	ts := traceSearchScopeFrom(ctx)
	if ts == nil {
		t.Fatal("trace takibi kapsamı ctx'te yok")
	}
	h := ts.wrap(tool, func(context.Context, json.RawMessage) (any, error) { return res, nil })
	if _, err := h(ctx, json.RawMessage(args)); err != nil {
		t.Fatal(err)
	}
}

func scTraceSearchCtx() context.Context {
	return withTraceSearchScope(withSourceSubject(context.Background(), tfTrace, tfSpan))
}

var scReqC = mcptools.SourceCodeRequest{Service: "svc-c", File: "ChargeHandler.java", Line: 120, ContextLines: 3}

// search_code ok / no_hits (depo çözüldü) → trace dışı, peer.service'siz
// servis okunur; dal ucu, sürüm tag'i aranmaz.
func TestReadSourceCodeAfterSearchCodeInTraceFollowUp(t *testing.T) {
	for _, outcome := range []string{"ok", "no_hits"} {
		s, git := scCalledServer(t)
		ctx := scTraceSearchCtx()
		scTraceSearchFeed(t, ctx, mcptools.SearchCodeToolName, `{"service":"svc-c","query":"ChargeHandler"}`, mcptools.CodeSearchResult{Outcome: outcome})
		sr, err := s.readSourceCode(ctx, scReqC)
		if err != nil || sr.Outcome != devops.SourceOK {
			t.Fatalf("%s: search_code'un çözdüğü servis okunmalı: %v %+v", outcome, err, sr)
		}
		if sr.RefKind != "branch" || sr.Version != "" {
			t.Fatalf("%s: dal ucundan okunmalı: %+v", outcome, sr)
		}
		for _, r := range git.requests() {
			if strings.Contains(r, "tags/") {
				t.Fatalf("%s: sürüm tag'i aranmamalı: %s", outcome, r)
			}
		}
	}
}

// Depo çözülmediyse (no_repo / error / rate_limited) kapsam DEĞİL; ret
// mesajı üç yolu anar; git'e istek gitmez.
func TestReadSourceCodeSearchCodeUnresolvedNotAdmitted(t *testing.T) {
	for _, outcome := range []string{"no_repo", "error", "rate_limited"} {
		s, git := scCalledServer(t)
		ctx := scTraceSearchCtx()
		scTraceSearchFeed(t, ctx, mcptools.SearchCodeToolName, `{"service":"svc-c","query":"x"}`, mcptools.CodeSearchResult{Outcome: outcome})
		if _, err := s.readSourceCode(ctx, scReqC); err == nil {
			t.Fatalf("%s: depo çözülmeden servis okunmamalı", outcome)
		}
		if n := len(git.requests()); n != 0 {
			t.Fatalf("%s: git'e %d istek", outcome, n)
		}
	}
}

// search_code'suz ret: mesaj trace'te olma, çağrılma ve search_code yolunu anar.
func TestReadSourceCodeTraceRefusalNamesThreeWays(t *testing.T) {
	s, _ := scCalledServer(t)
	_, err := s.readSourceCode(scTraceSearchCtx(), scReqC)
	if err == nil {
		t.Fatal("search_code'suz trace dışı servis reddedilmeli")
	}
	for _, want := range []string{"bu trace'te bulunamadı", "trace'teki", "çağırdığı", "search_code", "svc-a", "svc-b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ret mesajında %q yok: %s", want, err)
		}
	}
}

// Kod turunun öteki besleyicileri trace takibinde kapsamı GENİŞLETMEZ.
func TestTraceFollowUpScopeOnlySearchCodeFeeds(t *testing.T) {
	s, git := scCalledServer(t)
	ctx := scTraceSearchCtx()
	scTraceSearchFeed(t, ctx, "list_services", `{}`, map[string]any{"services": []chstore.ServiceSummary{{Name: "svc-c"}}, "matched_names": []string{"svc-c"}})
	scTraceSearchFeed(t, ctx, "resolve_entity", `{}`, map[string]any{"candidates": []mcptools.EntityCandidate{{Kind: "service", Name: "svc-c"}}})
	scTraceSearchFeed(t, ctx, "get_service_health", `{}`, map[string]any{"found": true, "matched_name": "svc-c"})
	scTraceSearchFeed(t, ctx, "resolve_business_code", `{}`, map[string]any{"candidates": []mcptools.BizCandidate{}})
	if _, err := s.readSourceCode(ctx, scReqC); err == nil {
		t.Fatal("search_code dışı araç sonucu trace takibinde kapsamı genişletti")
	}
	if n := len(git.requests()); n != 0 {
		t.Fatalf("git'e %d istek", n)
	}
	// Kod turu besleyicileri aynen (kod turu kapsamı değişmedi).
	for n := range scopeFeeders {
		if traceSearchFeeders[n] != (n == mcptools.SearchCodeToolName) {
			t.Errorf("trace besleyicisi yanlış: %s", n)
		}
	}
	if len(traceSearchFeeders) != 1 || len(scopeFeeders) != 5 {
		t.Fatalf("besleyiciler: trace=%v kod turu=%v", traceSearchFeeders, scopeFeeders)
	}
}

// Trace'teki servis search_code'dan sonra da trace sürümünü korur; peer.service
// yolu aynen.
func TestTraceSearchScopeKeepsTraceAndCalledRules(t *testing.T) {
	s, _ := scCalledServer(t)
	ctx := scTraceSearchCtx()
	scTraceSearchFeed(t, ctx, mcptools.SearchCodeToolName, `{"service":"svc-a","query":"x"}`, mcptools.CodeSearchResult{Outcome: "ok"})
	sr, err := s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: "svc-a", File: "ChargeHandler.java", Line: 120, ContextLines: 3})
	if err != nil || sr.Commit != scCommit || sr.VersionBasis != "sohbet öznesi (trace)" {
		t.Fatalf("trace'teki servis trace sürümünden okunmalı: %v %+v", err, sr)
	}
	sr, err = s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: "svc-b", File: "ChargeHandler.java", Line: 120, ContextLines: 3})
	if err != nil || sr.Outcome != devops.SourceOK || sr.RefKind != "branch" {
		t.Fatalf("peer.service servisi dal ucundan okunmalı: %v %+v", err, sr)
	}
}

// Okunamayan trace: search_code çözse de dürüst ıska (davranış değişmedi).
func TestTraceSearchScopeUnreadableTraceStaysMiss(t *testing.T) {
	git, dv := newSCGit(t)
	s := &Server{devops: dv}
	ctx := scTraceSearchCtx()
	scTraceSearchFeed(t, ctx, mcptools.SearchCodeToolName, `{"service":"svc-c","query":"x"}`, mcptools.CodeSearchResult{Outcome: "ok"})
	sr, err := s.readSourceCode(ctx, scReqC)
	if err != nil || sr.Outcome != devops.SourceTraceUnreadable {
		t.Fatalf("okunamayan trace dürüst ıska olmalı: %v %+v", err, sr)
	}
	if n := len(git.requests()); n != 0 {
		t.Fatalf("git'e %d istek", n)
	}
}

// Kapsam alışverişe bağlı: yeni alışverişin ctx'i boş başlar.
func TestTraceSearchScopeIsPerExchange(t *testing.T) {
	s, _ := scCalledServer(t)
	first := scTraceSearchCtx()
	scTraceSearchFeed(t, first, mcptools.SearchCodeToolName, `{"service":"svc-c","query":"x"}`, mcptools.CodeSearchResult{Outcome: "ok"})
	if _, err := s.readSourceCode(first, scReqC); err != nil {
		t.Fatalf("ilk alışveriş: %v", err)
	}
	if _, err := s.readSourceCode(scTraceSearchCtx(), scReqC); err == nil {
		t.Fatal("kapsam yeni alışverişe taşındı")
	}
}

// ── uçtan uca (gerçek copilotChat döngüsü) ───────────────────────────────

// scTraceSearchSeam — search_code sahte (depo çözüldü), read_source_code
// GERÇEK (kapsam kararı readSourceCode'da), diğerleri sahte.
func scTraceSearchSeam(t *testing.T) {
	t.Helper()
	chatLoopHandlerSeam = func(name string, h mcp.ToolHandler) mcp.ToolHandler {
		switch name {
		case mcptools.SourceCodeToolName:
			return h
		case mcptools.SearchCodeToolName:
			return func(context.Context, json.RawMessage) (any, error) {
				return mcptools.CodeSearchResult{Outcome: "ok", Repo: "svc-c", Hits: []devops.ServiceCodeHit{}}, nil
			}
		}
		return func(context.Context, json.RawMessage) (any, error) { return map[string]any{"tool": name}, nil }
	}
	t.Cleanup(func() { chatLoopHandlerSeam = nil })
}

// scToolContents — modele dönen araç mesajlarının içerikleri.
func scToolContents(body map[string]any) []string {
	var out []string
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if mm["role"] == "tool" {
			c, _ := mm["content"].(string)
			out = append(out, c)
		}
	}
	return out
}

func TestTraceFollowUpLoopSearchCodeAdmitsServiceForExchange(t *testing.T) {
	read := `{"service":"svc-c","file":"ChargeHandler.java","line":120}`
	llm := newLoopLLM(t, func(n int, body map[string]any) map[string]any {
		// Alışveriş 1: search_code → read_source_code → cevap.
		// Alışveriş 2: doğrudan read_source_code → cevap (kapsam taşınmamalı).
		switch {
		case n == 0:
			return toolCallMsg([2]string{mcptools.SearchCodeToolName, `{"service":"svc-c","query":"ChargeHandler"}`})
		case n == 1 || n == 3:
			return toolCallMsg([2]string{mcptools.SourceCodeToolName, read})
		}
		return answerMsg("tamam")
	})
	s, _ := newLoopTestServer(t, llm, "u-a")
	srvTrace, git := scCalledServer(t)
	s.devops, s.tempo = srvTrace.devops, srvTrace.tempo
	withCodeSearch(s.devops)
	scTraceSearchSeam(t)
	scPost(t, s, "u-a", auth.RoleViewer, traceDrawerBody("svc-c'nin koduna bak", anchoredToMs(), "conv-ts"))
	calls := llm.calls()
	if len(calls) < 3 {
		t.Fatalf("model %d kez çağrıldı", len(calls))
	}
	got := strings.Join(scToolContents(calls[2]), "\n")
	if !strings.Contains(got, scLeakMarker) {
		t.Fatalf("search_code'dan sonra svc-c okunmalıydı: %s", got)
	}
	before := len(git.requests())
	scPost(t, s, "u-a", auth.RoleViewer, traceDrawerBody("yine svc-c", anchoredToMs(), "conv-ts"))
	calls = llm.calls()
	if len(calls) < 5 {
		t.Fatalf("ikinci alışveriş: model %d kez çağrıldı", len(calls))
	}
	got = strings.Join(scToolContents(calls[4]), "\n")
	if strings.Contains(got, scLeakMarker) || !strings.Contains(got, "search_code") {
		t.Fatalf("kapsam yeni alışverişe taşındı ya da ret yolu söylemiyor: %s", got)
	}
	if n := len(git.requests()); n != before {
		t.Fatalf("ikinci alışverişte git'e %d istek", n-before)
	}
}

// GÜVENLİK ÖNKOŞULU (v0.10.2294): trace takibinde read_source_code ve
// search_code sunulurken katalogda dış MCP aracı ve onay-kapılı (yazan) araç
// YOKTUR — dış MCP canlı, rol admin. Okunan kod bir dış araca taşınamaz; bu
// yüzden search_code'un çözdüğü servis kapsama alınabilir. Kod turu kapsamı
// (copilot.code_turn) trace takibinde kurulmaz.
func TestTraceFollowUpCatalogNativeOnlyWithCodeTools(t *testing.T) {
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("tamam") })
	s, _ := newLoopTestServer(t, llm, "u-a")
	_, s.devops = newSCGit(t)
	withCodeSearch(s.devops)
	extCalls := withTracker(t, s)
	scPost(t, s, "u-a", auth.RoleAdmin, traceDrawerBody("Kodu oku", anchoredToMs(), "conv-sec"))
	calls := llm.calls()
	if len(calls) == 0 {
		t.Fatal("model çağrılmadı")
	}
	for i, b := range calls {
		names := llmToolNames(b)
		if !hasToolName(names, mcptools.SourceCodeToolName) || !hasToolName(names, mcptools.SearchCodeToolName) {
			t.Fatalf("çağrı %d: kod araçları sunulmuyor — önkoşul testi boşa döner: %v", i, names)
		}
		if hasExternalName(names) {
			t.Fatalf("çağrı %d: trace takibinde dış MCP aracı: %v", i, names)
		}
		if hasToolName(names, addProblemNoteToolName) {
			t.Fatalf("çağrı %d: trace takibinde yazan (onay-kapılı) araç: %v", i, names)
		}
	}
	if n := extCalls(); n != 0 {
		t.Fatalf("dış MCP çağrıldı (%d)", n)
	}
	for _, e := range drainAudit(s) {
		if e.Action == "copilot.code_turn" {
			t.Fatal("trace takibinde kod turu kapsamı kuruldu")
		}
	}
}
