package api

// v0.10.1274 — CoSRE adım 5: sayfa bağlamı + env araçlara.
//
// Ekran kapsamı (Topbar env, servis sayfası, zaman aralığı, seçili trace)
// artık yalnız prompt metni değil: sunucu, modelin vermediği argümanı araç
// çağrısına KENDİSİ koyar (applyScreenContext) ve koyduğunu step-result.
// ctxApplied + kanıt çipiyle ilan eder.

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func scopeSchema(required []string, props ...string) map[string]any {
	p := map[string]any{}
	for _, k := range props {
		switch k {
		case "range_s":
			p[k] = map[string]any{"type": "integer", "minimum": 0, "maximum": 604800}
		case "window":
			p[k] = map[string]any{"type": "string", "enum": []string{"15m", "1h", "6h", "24h", "7d"}}
		default:
			p[k] = map[string]any{"type": "string"}
		}
	}
	s := map[string]any{"type": "object", "properties": p}
	if required != nil {
		s["required"] = required
	}
	return s
}

func argMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("args çözülemedi: %s", raw)
	}
	return m
}

func TestApplyScreenContext(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	envTool := scopeSchema(nil, "service", "env", "range_s")
	svcReq := scopeSchema([]string{"service"}, "service", "env", "range_s")
	sc := toolScreenScope{Env: "prod", RangeS: 21600}

	t.Run("env eksik → eklenir", func(t *testing.T) {
		out, ap := applyScreenContext("list_anomalies", json.RawMessage(`{}`), envTool, sc, now)
		m := argMap(t, out)
		if m["env"] != "prod" || ap.Env != "prod" {
			t.Fatalf("env eklenmedi: %s %+v", out, ap)
		}
	})
	t.Run("model farklı env verdi → modelinki kalır, kayda geçer", func(t *testing.T) {
		out, ap := applyScreenContext("list_anomalies", json.RawMessage(`{"env":"uat"}`), envTool, sc, now)
		if argMap(t, out)["env"] != "uat" || ap.Env != "" || ap.EnvKept != "uat" || ap.ScreenEnv != "prod" {
			t.Fatalf("modelin env'i ezildi ya da kaydedilmedi: %s %+v", out, ap)
		}
	})
	t.Run("aynı env (harf farkı) → dokunulmaz, kayıt yok", func(t *testing.T) {
		_, ap := applyScreenContext("list_anomalies", json.RawMessage(`{"env":"PROD","range_s":60}`), envTool, sc, now)
		if !ap.Empty() {
			t.Fatalf("eşit env kayda geçti: %+v", ap)
		}
	})
	t.Run(`"all" → env eklenmez`, func(t *testing.T) {
		all := buildToolScreenScope(ChatScreenContext{Env: "all"}, nil, nil, "", "")
		out, ap := applyScreenContext("list_anomalies", json.RawMessage(`{}`), envTool, all, now)
		if _, has := argMap(t, out)["env"]; has || ap.Env != "" {
			t.Fatalf("'all' somut env sayıldı: %s", out)
		}
	})
	t.Run("env'siz, kayıtsız araç → dokunulmaz", func(t *testing.T) {
		in := json.RawMessage(`{"pattern":"x"}`)
		out, ap := applyScreenContext("list_environments", in, scopeSchema(nil, "pattern"), toolScreenScope{Env: "prod"}, now)
		if string(out) != string(in) || !ap.Empty() {
			t.Fatalf("env'siz araca dokunuldu: %s %+v", out, ap)
		}
	})
	t.Run("env-kör araç → argüman aynı, envNotApplied", func(t *testing.T) {
		in := json.RawMessage(`{"service":"a"}`)
		out, ap := applyScreenContext("get_topology", in, scopeSchema(nil, "service"), toolScreenScope{Env: "prod"}, now)
		if string(out) != string(in) || ap.EnvNotApplied == "" || ap.ScreenEnv != "prod" {
			t.Fatalf("env-kör künye yok: %s %+v", out, ap)
		}
	})
	t.Run("pencere eksik → range_s ekrandan", func(t *testing.T) {
		out, ap := applyScreenContext("list_anomalies", json.RawMessage(`{"env":"prod"}`), envTool, sc, now)
		if argMap(t, out)["range_s"] != float64(21600) || ap.RangeS != 21600 {
			t.Fatalf("pencere eklenmedi: %s %+v", out, ap)
		}
	})
	t.Run("model pencere verdi → dokunulmaz", func(t *testing.T) {
		out, ap := applyScreenContext("list_anomalies", json.RawMessage(`{"range_s":600}`), envTool, sc, now)
		if argMap(t, out)["range_s"] != float64(600) || ap.RangeS != 0 {
			t.Fatalf("modelin penceresi ezildi: %s", out)
		}
	})
	t.Run("range_s şema tavanına kırpılır", func(t *testing.T) {
		out, _ := applyScreenContext("x", json.RawMessage(`{}`), scopeSchema(nil, "range_s"), toolScreenScope{RangeS: 30 * 86400}, now)
		if argMap(t, out)["range_s"] != float64(604800) {
			t.Fatalf("tavan uygulanmadı: %s", out)
		}
	})
	t.Run("window enum tam eşleşme", func(t *testing.T) {
		out, ap := applyScreenContext("query_logs", json.RawMessage(`{"service":"a"}`), scopeSchema(nil, "service", "window", "from_ms", "to_ms"), sc, now)
		if argMap(t, out)["window"] != "6h" || ap.RangeS != 21600 {
			t.Fatalf("enum penceresi seçilmedi: %s", out)
		}
	})
	t.Run("window enum yok + ÇIPA → from_ms/to_ms çıpaya", func(t *testing.T) {
		anchor := time.Date(2026, 10, 16, 4, 0, 0, 0, time.UTC)
		asc := toolScreenScope{RangeS: 7200, AnchorTo: anchor, Anchored: true}
		out, _ := applyScreenContext("query_metrics", json.RawMessage(`{"service":"a"}`), scopeSchema(nil, "service", "window", "from_ms", "to_ms"), asc, now)
		m := argMap(t, out)
		if m["to_ms"] != float64(anchor.UnixMilli()) || m["from_ms"] != float64(anchor.Add(-2*time.Hour).UnixMilli()) {
			t.Fatalf("mutlak pencere çıpaya oturmadı: %s", out)
		}
		rel := toolScreenScope{RangeS: 7200}
		out, _ = applyScreenContext("query_metrics", json.RawMessage(`{"service":"a"}`), scopeSchema(nil, "service", "window", "from_ms", "to_ms"), rel, now)
		if argMap(t, out)["to_ms"] != float64(now.UnixMilli()) {
			t.Fatalf("göreli pencere şimdiye oturmadı: %s", out)
		}
	})
	t.Run("servis: zorunlu + servis sayfası → eklenir; zorunlu değilse eklenmez", func(t *testing.T) {
		ss := toolScreenScope{Service: "checkout"}
		out, ap := applyScreenContext("query_traces", json.RawMessage(`{}`), svcReq, ss, now)
		if argMap(t, out)["service"] != "checkout" || ap.Service != "checkout" {
			t.Fatalf("zorunlu servis eklenmedi: %s", out)
		}
		out, _ = applyScreenContext("list_anomalies", json.RawMessage(`{}`), envTool, ss, now)
		if _, has := argMap(t, out)["service"]; has {
			t.Fatalf("opsiyonel servis filo sorusunu daralttı: %s", out)
		}
		_, ap = applyScreenContext("query_traces", json.RawMessage(`{"service":"cart"}`), svcReq, ss, now)
		if ap.ServiceKept != "cart" || ap.Service != "" {
			t.Fatalf("modelin servisi kaydedilmedi: %+v", ap)
		}
	})
	t.Run("trace: query_logs yalnız soru trace'i anarsa ve servis yoksa", func(t *testing.T) {
		tid := strings.Repeat("ab", 16)
		ql := scopeSchema(nil, "service", "trace_id", "env", "window", "from_ms", "to_ms")
		asked := toolScreenScope{Env: "prod", RangeS: 3600, TraceID: tid, TraceAsked: true}
		out, ap := applyScreenContext("query_logs", json.RawMessage(`{}`), ql, asked, now)
		m := argMap(t, out)
		if m["trace_id"] != tid || ap.TraceID != tid {
			t.Fatalf("trace_id eklenmedi: %s", out)
		}
		if _, has := m["env"]; has {
			t.Fatalf("trace kimlikli çağrıya env eklendi (eşleşmeyi düşürebilir): %s", out)
		}
		if _, has := m["window"]; has {
			t.Fatalf("trace kimlikli çağrıya pencere eklendi: %s", out)
		}
		notAsked := asked
		notAsked.TraceAsked = false
		out, _ = applyScreenContext("query_logs", json.RawMessage(`{}`), ql, notAsked, now)
		if _, has := argMap(t, out)["trace_id"]; has {
			t.Fatalf("soru trace'i anmıyorken trace_id eklendi: %s", out)
		}
		out, _ = applyScreenContext("query_logs", json.RawMessage(`{"service":"a"}`), ql, asked, now)
		if _, has := argMap(t, out)["trace_id"]; has {
			t.Fatalf("servis süzgeçli çağrıya trace_id eklendi: %s", out)
		}
		out, _ = applyScreenContext("search_traces", json.RawMessage(`{}`), scopeSchema(nil, "trace_id"), asked, now)
		if _, has := argMap(t, out)["trace_id"]; has {
			t.Fatalf("izinsiz araca trace_id eklendi: %s", out)
		}
		out, _ = applyScreenContext("get_logs_for_trace", json.RawMessage(`{}`), scopeSchema([]string{"trace_id"}, "trace_id"), notAsked, now)
		if argMap(t, out)["trace_id"] != tid {
			t.Fatalf("get_logs_for_trace'e trace_id eklenmedi: %s", out)
		}
	})
	t.Run("şemasız (dış MCP) araç → dokunulmaz", func(t *testing.T) {
		in := json.RawMessage(`{}`)
		out, ap := applyScreenContext("ext_tool", in, nil, sc, now)
		if string(out) != "{}" || !ap.Empty() {
			t.Fatal("dış araca dokunuldu")
		}
	})
	t.Run("nesne olmayan argüman → aynen", func(t *testing.T) {
		in := json.RawMessage(`[1]`)
		if out, _ := applyScreenContext("list_anomalies", in, envTool, sc, now); string(out) != "[1]" {
			t.Fatal("bozuk argüman değiştirildi")
		}
	})
	// "Önbellek anahtarı" — sohbetin tekrar muhafızı yürütülen argümanı
	// anahtarlar; enjeksiyon Call'dan ÖNCE olduğu için env'i farklı iki
	// çağrı aynı anahtarı paylaşmaz.
	t.Run("tekrar muhafızı anahtarı env'e göre değişir", func(t *testing.T) {
		a, _ := applyScreenContext("list_anomalies", json.RawMessage(`{}`), envTool, toolScreenScope{Env: "prod"}, now)
		b, _ := applyScreenContext("list_anomalies", json.RawMessage(`{}`), envTool, toolScreenScope{Env: "uat"}, now)
		if agenttools.RepeatCallKey("list_anomalies", a) == agenttools.RepeatCallKey("list_anomalies", b) {
			t.Fatal("env farklı ama anahtar aynı")
		}
	})
}

func TestBuildToolScreenScope(t *testing.T) {
	tid := strings.Repeat("0f", 16)
	svcPage := &agentctx.PageContext{Page: "service", Service: "checkout"}
	sc := buildToolScreenScope(ChatScreenContext{Service: "checkout", Env: " prod ", RangeS: 3600}, svcPage, nil, "", "")
	if sc.Service != "checkout" || sc.Env != "prod" || sc.RangeS != 3600 {
		t.Fatalf("servis sayfası kapsamı: %+v", sc)
	}
	sc = buildToolScreenScope(ChatScreenContext{Service: "checkout"}, &agentctx.PageContext{Page: "traces"}, nil, "", "")
	if sc.Service != "" {
		t.Fatal("liste sayfasında servis enjeksiyonu açıldı")
	}
	// Pin kazanır.
	sc = buildToolScreenScope(ChatScreenContext{Service: "x"}, &agentctx.PageContext{Page: "traces"}, svcPage, "", "")
	if sc.Service != "x" {
		t.Fatal("sabitlenmiş servis sayfası yok sayıldı")
	}
	sc = buildToolScreenScope(ChatScreenContext{}, &agentctx.PageContext{Page: "trace", TraceID: tid}, nil, "", "bu trace neden yavaş")
	if sc.TraceID != tid || !sc.TraceAsked {
		t.Fatalf("sayfa trace'i: %+v", sc)
	}
	sc = buildToolScreenScope(ChatScreenContext{}, nil, nil, "not-hex", "trace")
	if sc.TraceID != "" {
		t.Fatal("geçersiz trace id kabul edildi")
	}
	for _, e := range []string{"", "all", "ALL", "*", "Tümü"} {
		if concreteEnv(e) != "" {
			t.Errorf("%q somut env sayıldı", e)
		}
	}
}

// v0.10.1435 — brush: sayfa grafikte bir aralık yayınladı VE soru onu işaret
// ediyor → araç penceresi brush'ın kendisi (mutlak, çıpalı); işaret yoksa ya
// da brush yoksa ekran penceresi aynen.
func TestBuildToolScreenScopeBrushDeixis(t *testing.T) {
	br := &agentctx.PageBrush{FromMs: 1_790_000_000_000, ToMs: 1_790_000_420_000}
	page := &agentctx.PageContext{Page: "dashboard", Brush: br}
	for _, c := range []struct {
		q     string
		brush bool
	}{
		{"bu tepe neden oluştu?", true},
		{"Bu sıçramanın sebebi ne", true},
		{"seçili seride ne oldu", true},
		{"bu aralıkta hangi deploy vardı", true},
		{"what caused this spike", true},
		{"servis neden yavaş", false},
		{"son 1 saatte hata oranı", false},
	} {
		sc := buildToolScreenScope(ChatScreenContext{RangeS: 3600}, page, nil, "", c.q)
		if sc.Brush != c.brush {
			t.Errorf("%q: brush=%v", c.q, sc.Brush)
			continue
		}
		if c.brush && (sc.RangeS != 420 || !sc.Anchored || sc.AnchorTo.UnixMilli() != br.ToMs) {
			t.Errorf("%q: pencere %+v", c.q, sc)
		}
		if !c.brush && sc.RangeS != 3600 {
			t.Errorf("%q: ekran penceresi bozuldu %+v", c.q, sc)
		}
	}
	if sc := buildToolScreenScope(ChatScreenContext{RangeS: 3600}, &agentctx.PageContext{Page: "dashboard"}, nil, "", "bu tepe"); sc.Brush {
		t.Error("brush yokken brush penceresi")
	}
	// Uygulama: yalnız mutlak uçlu araca iner; enum / range_s'e değil.
	sc := buildToolScreenScope(ChatScreenContext{RangeS: 3600}, page, nil, "bu tepe", "bu tepe")
	abs := map[string]any{"properties": map[string]any{"window": map[string]any{"enum": []any{"15m", "1h"}}, "from_ms": map[string]any{}, "to_ms": map[string]any{}}}
	out, ap := applyScreenContext("query_logs", nil, abs, sc, time.Now())
	if !strings.Contains(string(out), `"from_ms":1790000000000`) || !strings.Contains(string(out), `"to_ms":1790000420000`) || strings.Contains(string(out), "window") || ap.RangeS != 420 {
		t.Fatalf("mutlak uçlar: %s %+v", out, ap)
	}
	rel := map[string]any{"properties": map[string]any{"range_s": map[string]any{"type": "integer"}}}
	if out, _ := applyScreenContext("list_anomalies", nil, rel, sc, time.Now()); strings.Contains(string(out), "range_s") {
		t.Fatalf("göreli range_s'e brush enjekte edildi: %s", out)
	}
}

func TestAnnotateEnvBlind(t *testing.T) {
	got := annotateEnvBlind(`{"edges":[]}`, "sebep", "prod")
	m := map[string]any{}
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("geçersiz JSON: %s", got)
	}
	note, _ := m["env_note"].(string)
	if !strings.HasPrefix(note, "env uygulanmadı: sebep") || !strings.Contains(note, "prod") {
		t.Fatalf("not: %q", note)
	}
	if !strings.HasPrefix(got, `{"env_note":`) {
		t.Fatal("not başta değil (önizleme/kırpma baştan okur)")
	}
	if got := annotateEnvBlind(`{}`, "s", "p"); json.Unmarshal([]byte(got), &m) != nil {
		t.Fatalf("boş nesne: %s", got)
	}
	if annotateEnvBlind(`[1]`, "s", "p") != `[1]` || annotateEnvBlind(`{"a":1}`, "", "p") != `{"a":1}` {
		t.Fatal("nesne olmayan / sebepsiz içerik değişti")
	}
}

func TestGuidedEnvREDLine(t *testing.T) {
	got := guidedEnvREDLineTR("prod", 3600, chstore.ServiceSummary{SpanCount: 100, ErrorRate: 2.5, P99Ms: 840}, true, nil)
	for _, want := range []string{`"prod"`, "100 span", "%2.50", "840 ms", "tüm ortamların toplamı"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q eksik: %q", want, got)
		}
	}
	if got := guidedEnvREDLineTR("prod", 3600, chstore.ServiceSummary{}, false, nil); !strings.Contains(got, "span YOK") {
		t.Errorf("bulunamama: %q", got)
	}
	if got := guidedEnvREDLineTR("prod", 3600, chstore.ServiceSummary{}, false, errors.New("timeout")); !strings.Contains(got, "OKUNAMADI") || strings.Contains(got, "span YOK") {
		t.Errorf("hata 'veri yok' gibi yazıldı: %q", got)
	}
}

// KABLOLAMA PİNİ — enjeksiyon yürütmeden ÖNCE, yürüyen argümanla.
func TestChatWiresToolScreenScope(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	for _, must := range []string{
		"toolSchemas[t.Name] = t.InputSchema",
		"toolScope := buildToolScreenScope(screenCtx, pageCtx, pinnedCtx, scopeTrace, lastUserText(req.Messages))",
		"cargs, scopeApplied := applyScreenContext(tc.Name, tc.Input, toolSchemas[tc.Name], toolScope, time.Now())",
		"oc = exec.Call(ctx, tc.Name, cargs)", // v0.10.2355 — S1 kapsam kararından sonra (chat_scope_decision.go)
		`stepEv["ctxApplied"] = scopeApplied`,
		"annotateEnvBlind(tr.Content, scopeApplied.EnvNotApplied, scopeApplied.ScreenEnv)",
		"withEvidenceListScope(stepEv, evs, scopeApplied)", // v0.10.1275 — çok-kanıtlı zarf
	} {
		if !strings.Contains(src, must) {
			t.Errorf("kablolama eksik: %s", must)
		}
	}
	if strings.Index(src, "cargs, scopeApplied := applyScreenContext(") > strings.Index(src, "oc = exec.Call(ctx, tc.Name, cargs)") {
		t.Error("enjeksiyon yürütmeden SONRA")
	}
}
