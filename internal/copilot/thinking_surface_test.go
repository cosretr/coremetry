package copilot

// thinking_surface_test.go — v0.10.2458: yüzey başına düşünme kipi.
// Çözüm (off varsayılan / inherit / on), sınıflama, gövde (Qwen3 vs gemma vs
// profil beyanı), kalıcılık, öz-gözlem sayacı ve kip açıkken <think> soyumu.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// inheritThinking — bütün yüzeyler inherit: profil thinking anlamını pinleyen
// (v0.10.534 / v0.10.2375) eski testler için.
func inheritThinking(s *Service) *Service {
	m := map[string]string{}
	for _, i := range ThinkSurfaces() {
		m[string(i.ID)] = ThinkModeInherit
	}
	s.SetThinkingModes(m)
	return s
}

func surfaceCtx(label string) context.Context {
	return WithMeta(context.Background(), CallMeta{Surface: label})
}

func TestClassifyThinkSurface(t *testing.T) {
	cases := map[string]ThinkSurface{
		"chat-intent":            ThinkIntent,
		"chat-intent-none":       ThinkIntent,
		"chat-resource-classify": ThinkIntent,
		"wiki-select":            ThinkIntent,
		"chat-guided":            ThinkGuided,
		"chat-drawer":            ThinkGuided,
		"explain-trace":          ThinkTrace,
		"explain-trace:chat":     ThinkTrace,
		"explain-trace:nudge":    ThinkTrace,
		"compare-traces":         ThinkTrace,
		"explain-span":           ThinkExplain,
		"explain-problem:chat":   ThinkExplain,
		"runbook":                ThinkExplain,
		"runbook:chat":           ThinkExplain,
		"insight-service":        ThinkExplain,
		"problem-auto-explain":   ThinkExplain,
		"exception-auto-explain": ThinkExplain,
		"chat":                   ThinkInvestigation,
		"chat-research":          ThinkInvestigation,
		"sre-investigation":      ThinkInvestigation,
		"rootcause-verdict":      ThinkInvestigation,
		"sre-replay-judge":       ThinkOther,
		"chat-general":           ThinkOther,
		"wiki-chat":              ThinkOther,
		"nl-to-query":            ThinkOther,
		"settings-probe":         ThinkOther,
		"":                       ThinkOther,
	}
	for label, want := range cases {
		if got := ClassifyThinkSurface(label); got != want {
			t.Errorf("%q → %s, want %s", label, got, want)
		}
	}
	for _, i := range ThinkSurfaces() {
		if !knownThinkSurface(i.ID) || i.Label == "" {
			t.Errorf("surface info %+v", i)
		}
	}
}

// Açık ctx yüzeyi etiket sınıflamasını ezer (sağlık raporu, doğrulayıcı).
func TestThinkSurfaceCtxOverride(t *testing.T) {
	ctx := surfaceCtx("chat-guided")
	if got := ThinkSurfaceOf(ctx); got != ThinkGuided {
		t.Fatalf("label: %s", got)
	}
	if got := ThinkSurfaceOf(WithThinkingSurface(ctx, ThinkHealthReport)); got != ThinkHealthReport {
		t.Fatalf("override: %s", got)
	}
	if got := ThinkSurfaceOf(WithThinkingSurface(ctx, "")); got != ThinkGuided {
		t.Fatalf("empty override must not change: %s", got)
	}
}

func TestResolveSurfaceThinking(t *testing.T) {
	for _, c := range []struct{ mode, profile, want string }{
		{"", "", "off"}, {"", "on", "off"}, {"bogus", "on", "off"},
		{ThinkModeOff, "on", "off"},
		{ThinkModeOn, "", "on"}, {ThinkModeOn, "off", "on"},
		{ThinkModeInherit, "", ""}, {ThinkModeInherit, "on", "on"}, {ThinkModeInherit, "off", "off"},
	} {
		if got := ResolveSurfaceThinking(c.mode, c.profile); got != c.want {
			t.Errorf("mode=%q profile=%q → %q, want %q", c.mode, c.profile, got, c.want)
		}
	}
	// v0.10.2471 (operatör 2026-11-26, v0.10.2458 varsayılanını tersine çevirir):
	// kayıtsız yüzey AÇIK; tek istisna intent (tek satır JSON sınıflayıcı) kapalı.
	s := New(ProviderOpenAI, "", "qwen3-8b")
	for k, v := range s.ThinkingModes() {
		want := ThinkModeOn
		if k == string(ThinkIntent) {
			want = ThinkModeOff
		}
		if v != want {
			t.Errorf("fresh service: %s=%s, want %s (operator default 2026-11-26)", k, v, want)
		}
	}
	if len(s.ThinkingModes()) != len(ThinkSurfaces()) {
		t.Fatalf("modes must list every surface")
	}
	if !reflect.DeepEqual(s.ThinkingModes(), DefaultThinkingModes()) {
		t.Fatalf("fresh service must equal DefaultThinkingModes: %v", s.ThinkingModes())
	}
}

// v0.10.2471 — kayıtlı ayar varsayılanı ezer; yalnız kaydı olmayan yüzey yeni
// varsayılanı alır. Kayıt seyrek: dokunulmayan yüzey depoya yazılmaz.
func TestThinkingStoredModesWinOverDefault(t *testing.T) {
	store := newMemStore()
	store.m[ThinkingSettingsKey] = []byte(`{"guided":"off","intent":"on","trace":"inherit"}`)
	s := New(ProviderOpenAI, "", "qwen3-8b")
	if err := s.LoadPersisted(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	got := s.ThinkingModes()
	for k, want := range map[string]string{"guided": "off", "intent": "on", "trace": "inherit", // kayıtlı
		"health_report": "on", "explain": "on", "investigation": "on", "verifier": "on", "other": "on"} { // kayıtsız → varsayılan
		if got[k] != want {
			t.Errorf("%s=%s, want %s", k, got[k], want)
		}
	}
	if _, err := s.SaveThinkingModes(context.Background(), store, map[string]string{"explain": "off"}); err != nil {
		t.Fatal(err)
	}
	var blob map[string]string
	if err := json.Unmarshal(store.m[ThinkingSettingsKey], &blob); err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"guided": "off", "intent": "on", "trace": "inherit", "explain": "off"}; !reflect.DeepEqual(blob, want) {
		t.Fatalf("stored blob must hold only explicit surfaces: %v, want %v", blob, want)
	}
}

// v0.10.2471 — bütçe / cevap tavanı varsayılanı izler (mekanik v0.10.2458 ile aynı):
// kayıtsız guided çağrısı düşünen bütçeyle, kayıtsız intent çağrısı düşünmesiz.
func TestThinkingDefaultDrivesBudgetAndBody(t *testing.T) {
	llm := newTuneLLM(t)
	s := llm.service(ModelProfile{Model: "qwen3-8b"})
	s.SetThinkingModes(nil) // gerçek varsayılan
	on := map[string]any{"enable_thinking": true}
	off := map[string]any{"enable_thinking": false}
	for _, c := range []struct {
		label string
		want  any
	}{{"chat-guided", on}, {"chat", on}, {"explain-problem", on}, {"chat-intent", off}} {
		if _, err := s.Explain(surfaceCtx(c.label), "sys", "user"); err != nil {
			t.Fatalf("%s: %v", c.label, err)
		}
		if got := thinkingKwargs(t, llm); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: chat_template_kwargs=%v, want %v", c.label, got, c.want)
		}
	}
	caps := capsForProfile(ModelProfile{Model: "qwen3-8b"})
	thOn, _ := s.effectiveThinking(surfaceCtx("chat-guided"), ModelProfile{Model: "qwen3-8b"})
	thOff, _ := s.effectiveThinking(surfaceCtx("chat-intent"), ModelProfile{Model: "qwen3-8b"})
	if a, b := resolveMaxTokens(0, 0, caps, thOn, 0), resolveMaxTokens(0, 0, caps, thOff, 0); b != defaultMaxTokens || (caps.DefaultMaxTokens > defaultMaxTokens && a <= b) {
		t.Errorf("budget must follow the surface default: guided=%d intent=%d (model default %d)", a, b, caps.DefaultMaxTokens)
	}
}

func thinkingKwargs(t *testing.T, l *tuneLLM) any {
	t.Helper()
	body, _ := l.last(t)
	return body["chat_template_kwargs"]
}

// Gövde: Qwen3 profilinde varsayılan (v0.10.2471: on, intent off) enable_thinking
// true / intent'te false gönderir, kayıtlı off false, inherit profilin değerini
// (boşsa hiç); gemma'da hiçbir kipte anahtar yok; profil thinkingSwitch beyanı
// adı tanınmayan modelde anahtarı açar.
func TestProviderBodyPerSurfaceThinking(t *testing.T) {
	off := map[string]any{"enable_thinking": false}
	on := map[string]any{"enable_thinking": true}
	type step struct {
		modes map[string]string
		label string
		want  any
	}
	cases := []struct {
		name    string
		profile ModelProfile
		steps   []step
	}{
		{"qwen3", ModelProfile{Model: tuneQwen}, []step{
			{nil, "explain-trace", on},
			{nil, "chat-intent", off},
			{map[string]string{"trace": "off"}, "explain-trace", off},
			{map[string]string{"trace": "on"}, "explain-trace", on},
			{map[string]string{"trace": "on"}, "chat-intent", off},
			{map[string]string{"intent": "on"}, "chat-intent", on},
			{map[string]string{"intent": "inherit"}, "chat-intent", nil},
		}},
		{"qwen3 profile thinking=off, inherit", ModelProfile{Model: "qwen3-30b", Thinking: "off"}, []step{
			{nil, "chat", on}, // varsayılan on profilin off'unu ezer; profil yalnız inherit'te
			{map[string]string{"investigation": "inherit"}, "chat", off},
		}},
		{"gemma never gets the key", ModelProfile{Model: tuneGemma, Thinking: "on"}, []step{
			{nil, "explain-span", nil},
			{map[string]string{"explain": "on"}, "explain-span", nil},
			{map[string]string{"explain": "inherit"}, "explain-span", nil},
		}},
		{"alias with profile thinkingSwitch", ModelProfile{Model: "cosre-default", ThinkingSwitch: true}, []step{
			{nil, "chat-guided", on},
			{map[string]string{"guided": "off"}, "chat-guided", off},
		}},
		{"alias without flag", ModelProfile{Model: "cosre-default"}, []step{
			{map[string]string{"guided": "on"}, "chat-guided", nil},
		}},
	}
	for _, c := range cases {
		llm := newTuneLLM(t)
		s := llm.service(c.profile)
		s.SetThinkingModes(nil) // service() inherit kurar; bu test gerçek varsayılandan başlar
		for i, st := range c.steps {
			s.SetThinkingModes(st.modes)
			if _, err := s.Explain(surfaceCtx(st.label), "sys", "user"); err != nil {
				t.Fatalf("%s #%d: %v", c.name, i, err)
			}
			if got := thinkingKwargs(t, llm); !reflect.DeepEqual(got, st.want) {
				t.Errorf("%s #%d (%s, %v): chat_template_kwargs=%v, want %v", c.name, i, st.label, st.modes, got, st.want)
			}
			// araçlı yol aynı kararı verir
			if _, err := s.ChatWithTools(surfaceCtx(st.label), "sys", []ChatMessage{{Role: "user", Text: "u"}}, nil); err != nil {
				t.Fatalf("%s #%d tools: %v", c.name, i, err)
			}
			if got := thinkingKwargs(t, llm); !reflect.DeepEqual(got, st.want) {
				t.Errorf("%s #%d tools: chat_template_kwargs=%v, want %v", c.name, i, got, st.want)
			}
		}
	}
}

// Kalıcılık: kısmi PUT birleşir, ayrı anahtara yazılır, LoadPersisted (ai_copilot
// blobu boşken de) okur; bilinmeyen yüzey / kip reddedilir, store'a yazılmaz.
func TestThinkingModesPersistRoundTrip(t *testing.T) {
	store := newMemStore()
	s := New(ProviderOpenAI, "", "qwen3-8b")
	got, err := s.SaveThinkingModes(context.Background(), store, map[string]string{"trace": "on", "investigation": "inherit"})
	if err != nil {
		t.Fatal(err)
	}
	if got["trace"] != "on" || got["investigation"] != "inherit" || got["intent"] != "off" {
		t.Fatalf("merged %v", got)
	}
	var blob map[string]string
	if err := json.Unmarshal(store.m[ThinkingSettingsKey], &blob); err != nil || blob["trace"] != "on" {
		t.Fatalf("stored %s: %v", store.m[ThinkingSettingsKey], err)
	}
	if _, ok := store.m[settingsKey]; ok {
		t.Fatal("ai_copilot blob must not be written")
	}
	s2 := New(ProviderOpenAI, "", "qwen3-8b")
	if err := s2.LoadPersisted(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s2.ThinkingModes(), got) {
		t.Fatalf("reload %v, want %v", s2.ThinkingModes(), got)
	}
	for _, bad := range []map[string]string{{"nope": "on"}, {"trace": "auto"}} {
		if _, err := s.SaveThinkingModes(context.Background(), store, bad); err == nil {
			t.Errorf("%v must be rejected", bad)
		}
	}
	if s.ThinkingModes()["trace"] != "on" {
		t.Fatal("rejected PUT changed live modes")
	}
	// Bozuk blob yok sayılır (son iyi değer kalır).
	store.m[ThinkingSettingsKey] = []byte(`{"trace":"maybe"}`)
	if err := s2.LoadPersisted(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if s2.ThinkingModes()["trace"] != "on" {
		t.Fatal("invalid blob must be ignored")
	}
}

// Öz-gözlem: çağrı yüzey × düşünme etiketiyle sayılır.
func TestThinkingStatsPerSurface(t *testing.T) {
	llm := newTuneLLM(t)
	s := llm.service(ModelProfile{Model: tuneQwen})
	s.SetThinkingModes(map[string]string{"trace": "on"})
	for _, l := range []string{"explain-trace", "explain-trace", "chat-intent"} {
		if _, err := s.Explain(surfaceCtx(l), "sys", "user"); err != nil {
			t.Fatal(err)
		}
	}
	gl := newTuneLLM(t) // anahtarı olmayan model → unsupported
	gs := gl.service(ModelProfile{Model: tuneGemma})
	gs.SetThinkingModes(nil)
	if _, err := gs.Explain(surfaceCtx("explain-span"), "sys", "user"); err != nil {
		t.Fatal(err)
	}
	stats := s.ThinkingStats()
	if len(stats) != 2 || stats[0].Surface != "intent" || stats[0].Thinking != "off" || stats[0].Calls != 1 ||
		stats[1].Surface != "trace" || stats[1].Thinking != "on" || stats[1].Calls != 2 {
		t.Fatalf("stats %+v", stats)
	}
	if g := gs.ThinkingStats(); len(g) != 1 || g[0].Thinking != "unsupported" || g[0].Surface != "explain" {
		t.Fatalf("gemma stats %+v", g)
	}
}

// Kip açıkken model <think> bloğu üretirse cevap (buffered, akış, araçlı)
// soyulmuş gelir — katı JSON yüzeyi (niyet) dahil.
func TestThinkBlockStrippedWhenSurfaceOn(t *testing.T) {
	const answer = `{"intent":"service_health"}`
	raw := "<think>kullanıcı sağlık soruyor…</think>\n" + answer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if st, _ := req["stream"].(bool); st {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, part := range []string{"<think>kullanıcı ", "sağlık soruyor…</think>\n", answer} {
				b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": part}}}})
				_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
			}
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": raw}, "finish_reason": "stop"}}})
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	p := ModelProfile{ID: "p", Provider: ProviderOpenAI, BaseURL: srv.URL + "/v1", Model: tuneQwen}
	s := New(ProviderOpenAI, "", p.Model)
	s.Configure(ProviderOpenAI, "", p.Model, p.BaseURL, false, true)
	s.SetProfiles([]ModelProfile{p}, "p", nil)
	s.SetThinkingModes(map[string]string{"intent": "on", "guided": "on", "investigation": "on"})

	if out, err := s.Explain(WithJSONMode(surfaceCtx("chat-intent")), "sys", "u"); err != nil || out != answer {
		t.Fatalf("buffered: %q %v", out, err)
	}
	var deltas strings.Builder
	out, err := s.StreamText(surfaceCtx("chat-guided"), "sys", "u", func(d string) { deltas.WriteString(d) })
	if err != nil || out != answer || strings.Contains(deltas.String(), "think") {
		t.Fatalf("stream: out=%q deltas=%q err=%v", out, deltas.String(), err)
	}
	turn, err := s.ChatWithTools(surfaceCtx("chat"), "sys", []ChatMessage{{Role: "user", Text: "u"}}, nil)
	if err != nil || turn.Text != answer {
		t.Fatalf("tools: %q %v", turn.Text, err)
	}
}
