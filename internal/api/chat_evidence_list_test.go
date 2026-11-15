package api

// v0.10.1275 — CoSRE adım 6: sinyaller arası pivot (trace → pod → JVM → log) + playbook.
//
// Çok-kanıtlı zarf (geriye uyumlu): `evidence` hep İLK kalem; `evidenceList`
// yalnız >1 kalemde; ref'ler ev<i> / ev<i>.<k>. Cevap kanıt tavanı 12; arşiv
// hop'u kırpar. Pivot aracı ekrandaki trace'i / servis sayfasının servisini
// argümana alır; ilerleme satırı etiket-yalnız `step`.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

const evPivotResult = `{"tool":"pivot_from_trace","ok":true,"partial":false,"request":{"trace_id":"0123456789abcdef0123456789abcdef"},` +
	`"hops":[],"findings":[],` +
	`"evidence":{"source":"ClickHouse","query":"GetTrace","queryKind":"summary","window":{"fromMs":1760600000000,"toMs":1760600003000},"rows":9,"durationMs":4},` +
	`"evidences":[` +
	`{"hop":"trace","as":"trace","request":{"trace_id":"0123456789abcdef0123456789abcdef"},"source":"ClickHouse","query":"GetTrace","queryKind":"summary","window":{"fromMs":1760600000000,"toMs":1760600003000},"rows":9,"durationMs":4},` +
	`{"hop":"pods","as":"pod","request":{"cluster":"c-1","namespace":"shop","pod":"co-1","service":"checkout"},"source":"Thanos","query":"sum(rate(x[5m]))","queryKind":"promql","window":{"fromMs":1760599700000,"toMs":1760600303000},"cluster":"prod-a","rows":3,"durationMs":40},` +
	`{"hop":"logs","as":"query_logs","request":{"trace_id":"0123456789abcdef0123456789abcdef"},"source":"Elasticsearch","query":"logs[es]","queryKind":"summary","window":{"fromMs":1760599700000,"toMs":1760600303000},"rows":4,"durationMs":12}],` +
	`"data":{"pods":[],"jvm":[],"logs":[],"blocks":[{"hop":"pods","type":"table","payload":{"title":"trace pod'ları","columns":[],"rows":[]}}]}}`

func TestAttachStepEvidenceList(t *testing.T) {
	t.Run("bileşik: evidence = ilk kalem, evidenceList >1, ref'ler ev<i>.<k>", func(t *testing.T) {
		ev := map[string]any{"i": 7, "tool": "pivot_from_trace", "ok": true}
		evs := attachStepEvidenceList(ev, "pivot_from_trace", evPivotResult)
		if len(evs) != 3 {
			t.Fatalf("kalem %d", len(evs))
		}
		if evs[0].Ref != "ev7" || evs[1].Ref != "ev7.1" || evs[2].Ref != "ev7.2" {
			t.Fatalf("ref'ler: %s %s %s", evs[0].Ref, evs[1].Ref, evs[2].Ref)
		}
		first, _ := ev["evidence"].(mcptools.ChatEvidence)
		if first.Hop != "trace" || first.Ref != "ev7" || !strings.HasPrefix(first.Href, "/trace?") {
			t.Fatalf("evidence ilk kalem olmalı: %+v", first)
		}
		list, _ := ev["evidenceList"].([]mcptools.ChatEvidence)
		if len(list) != 3 || list[1].Hop != "pods" || !strings.HasPrefix(list[1].Href, "/pod?") || !strings.Contains(list[1].Href, "cluster=c-1") {
			t.Fatalf("evidenceList: %+v", list)
		}
		if ev["href"] != first.Href {
			t.Fatalf("adım href'i ilk kanıttan: %v", ev["href"])
		}
		blks := mcptools.ToolResultBlocks("pivot_from_trace", evPivotResult, evs)
		if len(blks) != 1 || blks[0].Payload.(map[string]any)["evidenceRef"] != "ev7.1" {
			t.Fatalf("blok pods kanıtına bağlanmalı: %+v", blks)
		}
	})
	t.Run("atomik: tek kalem, evidenceList YOK, gövde attachStepEvidence ile aynı", func(t *testing.T) {
		a := map[string]any{"i": 3, "tool": "query_traces", "ok": true}
		b := map[string]any{"i": 3, "tool": "query_traces", "ok": true}
		evs := attachStepEvidenceList(a, "query_traces", evTracesResult)
		attachStepEvidence(b, "query_traces", evTracesResult)
		if len(evs) != 1 {
			t.Fatalf("kalem %d", len(evs))
		}
		if _, has := a["evidenceList"]; has {
			t.Fatal("tek kalemde evidenceList yazılmamalı")
		}
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) {
			t.Fatalf("atomik gövde değişti:\n%s\n%s", ja, jb)
		}
	})
	t.Run("kanıtsız araç dokunulmaz", func(t *testing.T) {
		ev := map[string]any{"i": 1}
		if attachStepEvidenceList(ev, "get_trace", evPivotResult) != nil || len(ev) != 1 {
			t.Fatalf("kanıtsız araca yazıldı: %v", ev)
		}
	})
	t.Run("ekran künyesi her kaleme", func(t *testing.T) {
		ev := map[string]any{"i": 2}
		evs := attachStepEvidenceList(ev, "pivot_from_trace", evPivotResult)
		evs = withEvidenceListScope(ev, evs, mcptools.ScopeApplied{TraceID: "0123456789abcdef0123456789abcdef"})
		for _, e := range evs {
			if e.CtxApplied == nil || e.CtxApplied.TraceID == "" {
				t.Fatalf("künye eksik: %+v", e)
			}
		}
		if l, _ := ev["evidenceList"].([]mcptools.ChatEvidence); l[2].CtxApplied == nil {
			t.Fatal("step-result listesi güncellenmedi")
		}
	})
}

func TestMergeChatEvidenceCap12(t *testing.T) {
	if chatEvidenceMax != 12 || aiChatEvidenceMax != 12 {
		t.Fatalf("tavan %d / arşiv %d — 12 bekleniyordu", chatEvidenceMax, aiChatEvidenceMax)
	}
	var acc []mcptools.ChatEvidence
	for i := 0; i < 20; i++ {
		acc = mergeChatEvidence(acc, mcptools.ChatEvidence{Source: "ClickHouse"})
	}
	if len(acc) != 12 {
		t.Fatalf("liste %d", len(acc))
	}
}

func TestSanitizeChatEvidenceHop(t *testing.T) {
	got := sanitizeChatEvidence([]mcptools.ChatEvidence{
		{Source: "Thanos", Hop: strings.Repeat("h", 100), Href: "/pod?pod=x"},
		{Source: "ClickHouse", Hop: "trace"},
	})
	if len(got) != 2 || len([]rune(got[0].Hop)) > aiChatEvidenceHopMax+1 || got[1].Hop != "trace" || got[0].Href != "/pod?pod=x" {
		t.Fatalf("hop kırpılmadı ya da kayboldu: %+v", got)
	}
}

func TestChatProgressStep(t *testing.T) {
	m := chatProgressStep("Altyapı", "pod'lar okunuyor")
	if m["label"] != "pod'lar okunuyor" || m["phase"] != "Altyapı" || len(m) != 2 {
		t.Fatalf("ilerleme olayı: %v", m)
	}
	if _, has := chatProgressStep("", "x")["phase"]; has {
		t.Fatal("boş evre yazılmamalı")
	}
	if _, has := m["tool"]; has {
		t.Fatal("ilerleme satırı araç çağrısı gibi görünmemeli")
	}
	if stepPhaseTR(mcptools.PivotFromTraceName) != "Trace" {
		t.Fatal("pivot evresi")
	}
}

func TestApplyScreenContextPivot(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	tid := strings.Repeat("ab", 16)
	schema := scopeSchema(nil, "trace_id", "service", "operation", "env", "window", "pick")
	t.Run("ekrandaki trace eklenir, env/pencere eklenmez (deixis gerekmez)", func(t *testing.T) {
		sc := toolScreenScope{Env: "prod", RangeS: 3600, TraceID: tid}
		out, ap := applyScreenContext(mcptools.PivotFromTraceName, json.RawMessage(`{}`), schema, sc, now)
		m := argMap(t, out)
		if m["trace_id"] != tid || ap.TraceID != tid {
			t.Fatalf("trace eklenmedi: %s", out)
		}
		if _, has := m["env"]; has {
			t.Fatalf("trace kimlikli pivota env eklendi: %s", out)
		}
		if _, has := m["window"]; has {
			t.Fatalf("trace kimlikli pivota pencere eklendi: %s", out)
		}
	})
	t.Run("servis sayfası, trace yok → servis + env + pencere", func(t *testing.T) {
		sc := toolScreenScope{Env: "prod", RangeS: 3600, Service: "checkout"}
		out, ap := applyScreenContext(mcptools.PivotFromTraceName, json.RawMessage(`{}`), schema, sc, now)
		m := argMap(t, out)
		if m["service"] != "checkout" || m["env"] != "prod" || m["window"] != "1h" || ap.Service != "checkout" {
			t.Fatalf("servis kapsamı: %s %+v", out, ap)
		}
	})
	t.Run("model servis verdi → trace eklenmez", func(t *testing.T) {
		sc := toolScreenScope{TraceID: tid}
		out, _ := applyScreenContext(mcptools.PivotFromTraceName, json.RawMessage(`{"service":"cart"}`), schema, sc, now)
		if _, has := argMap(t, out)["trace_id"]; has {
			t.Fatalf("servisli çağrıya trace eklendi: %s", out)
		}
	})
	t.Run("başka opsiyonel-servisli araca servis eklenmez", func(t *testing.T) {
		out, _ := applyScreenContext("list_anomalies", json.RawMessage(`{}`), scopeSchema(nil, "service"), toolScreenScope{Service: "checkout"}, now)
		if _, has := argMap(t, out)["service"]; has {
			t.Fatalf("filo sorusu daraltıldı: %s", out)
		}
	})
}

func TestResolveSpanClusterNoThanos(t *testing.T) {
	s := &Server{}
	if sc := s.resolveSpanCluster("prod-a"); sc.ThanosOn || sc.Reason != "" {
		t.Fatalf("Thanos yokken: %+v", sc)
	}
	if s.mcpTracePodsOrNil() != nil {
		t.Fatal("Thanos yokken pod okuyucusu nil olmalı")
	}
}
