package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
)

// v0.10.2375 — model-farkında bütçe / sıcaklık / örnekleme. Gövdeler UÇTAN
// UCA (gerçek callSnapshot + sağlayıcı + taşıyıcı, httptest ucu) doğrulanır;
// gemma ve bilinmeyen model gövdesi bu sürümden ÖNCEKİ şekliyle bayt bayt aynı.

const (
	tuneQwen  = "qwen38-flash-next-fp8"
	tuneGemma = "gemma-4-31b-it"
)

func TestResolveMaxTokens(t *testing.T) {
	q, g := modelcaps.For(tuneQwen), modelcaps.For(tuneGemma)
	cases := []struct {
		name            string
		profile, global int
		caps            modelcaps.Caps
		thinking        string
		window, want    int
	}{
		{"gemma: varsayılan aynen", 0, 0, g, "", 131072, 4096},
		{"bilinmeyen: varsayılan aynen", 0, 0, modelcaps.Caps{}, "", 0, 4096},
		{"gemma: küresel aynen", 0, 8192, g, "", 131072, 8192},
		{"gemma: profil aynen", 16384, 8192, g, "", 131072, 16384},
		{"gemma: profil 32768 pencereyle KISILMAZ (eski davranış)", 32768, 0, g, "", 8192, 32768},
		{"qwen3.8: model varsayılanı", 0, 0, q, "", 262144, 32768},
		{"qwen3.8: düşünme açık da aynı", 0, 0, q, "on", 262144, 32768},
		{"qwen3.8: düşünme kapalı → 4096", 0, 0, q, "off", 262144, 4096},
		{"qwen3.8: küresel operatör değeri kazanır", 0, 8192, q, "", 262144, 8192},
		{"qwen3.8: profil kazanır", 6000, 8192, q, "", 262144, 6000},
		{"qwen3.8: sunucu 32k açmış → yarısı", 0, 0, q, "", 32768, 16384},
		{"qwen3.8: çok küçük pencere 4096'nın altına inmez", 0, 0, q, "", 4096, 4096},
		{"qwen3.8: profil 100000 tavanın içinde", 100000, 0, q, "", 262144, 100000},
		{"qwen3.8: profil 131072 = çıktı tavanı", 131072, 0, q, "", 262144, 131072},
		{"qwen3.8: profil 100000, sunucu 128k → yarısı", 100000, 0, q, "", 131072, 65536},
		{"kayıtsız model 32768 üstü (elle bozulmuş blob) → genel tavan", 100000, 0, modelcaps.Caps{}, "", 0, 32768},
	}
	for _, c := range cases {
		if got := resolveMaxTokens(c.profile, c.global, c.caps, c.thinking, c.window); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}

func TestResolveTemperature(t *testing.T) {
	rec := modelcaps.Sampling{Temperature: 1.0}
	cases := []struct {
		name            string
		profile, global *float64
		hasRec          bool
		want            float64
	}{
		{"hiçbiri → 0.2", nil, nil, false, 0.2},
		{"öneri", nil, nil, true, 1.0},
		{"küresel operatör değeri öneriyi ezer", nil, f64(0.4), true, 0.4},
		{"profil hepsini ezer", f64(0), f64(0.4), true, 0},
		{"öneri yokken küresel", nil, f64(0.4), false, 0.4},
	}
	for _, c := range cases {
		if got := resolveTemperature(c.profile, c.global, rec, c.hasRec); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// Profil maxTokens doğrulaması: çıktı tavanı bilinen modelde 131072'ye kadar;
// diğer modellerde ve küresel ayarda 32768 aynen.
func TestProfileMaxTokensLimitIsModelAware(t *testing.T) {
	ok := func(model string, n int) error {
		return ValidateProfile(ModelProfile{ID: "p", Provider: ProviderOpenAI, Model: model, MaxTokens: n})
	}
	for _, c := range []struct {
		model string
		n     int
		valid bool
	}{
		{tuneQwen, 32768, true}, {tuneQwen, 32769, true}, {tuneQwen, 131072, true}, {tuneQwen, 131073, false},
		{tuneGemma, 32768, true}, {tuneGemma, 32769, false},
		{"qwen3-30b", 32769, false}, {"my-model", 32769, false}, {"", 32769, false},
	} {
		if err := ok(c.model, c.n); (err == nil) != c.valid {
			t.Errorf("%s maxTokens=%d: err=%v, geçerli=%v bekleniyordu", c.model, c.n, err, c.valid)
		}
	}
	if err := ok(tuneQwen, 131073); err == nil || !strings.Contains(err.Error(), "131072") {
		t.Errorf("hata metni model tavanını söylemeli: %v", err)
	}
	if err := ValidateTuning(32769, nil, 0); err == nil || !strings.Contains(err.Error(), "between 256 and 32768") {
		t.Errorf("küresel tavan 32768 kalmalı: %v", err)
	}
}

// Derin kip: tavan model-farkında ve pencere yürürlükteki pencere.
func TestDeepMaxTokensForModelAware(t *testing.T) {
	deep := WithDeepAnswer(context.Background())
	cases := []struct {
		name         string
		base         int
		model        string
		window, want int
	}{
		{"qwen3.8 varsayılan taban", 32768, tuneQwen, 262144, 49152},
		{"qwen3.8 büyük taban çıktı tavanında durur", 100000, tuneQwen, 262144, 131072},
		{"qwen3.8 sunucu 64k açmış → yarısı, tabanın altına inmez", 32768, tuneQwen, 65536, 32768},
		{"gemma: eski tavan 32768", 30000, tuneGemma, 131072, 32768},
		{"gemma: eski sayı", 4096, tuneGemma, 131072, 6144},
		{"bilinmeyen: eski sayı", 4096, "my-model", 0, 6144},
	}
	for _, c := range cases {
		if got := deepMaxTokensFor(deep, c.base, c.model, c.window); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	if got := deepMaxTokensFor(context.Background(), 32768, tuneQwen, 262144); got != 32768 {
		t.Errorf("Derin kapalıyken taban aynen: %d", got)
	}
}

// cachedContextWindow: sunucudan okunmuş kayıt (bayat da olsa) ad tablosunu
// ezer; kayıt yoksa ad tablosu. Ağ çağrısı yok (uç adresi çözülemez bir ad).
func TestCachedContextWindow(t *testing.T) {
	cfg := ModelProfile{ID: "p", Provider: ProviderOpenAI, BaseURL: "http://llm.invalid/v1/", Model: tuneQwen}
	key := "http://llm.invalid/v1|" + tuneQwen
	t.Cleanup(func() {
		ctxProbeMu.Lock()
		delete(ctxProbeCache, key)
		ctxProbeMu.Unlock()
	})
	if got := cachedContextWindow(cfg); got != 262144 {
		t.Fatalf("kayıt yokken ad tablosu: %d", got)
	}
	ctxProbeMu.Lock()
	ctxProbeCache[key] = ctxProbeEntry{info: ContextWindowInfo{Tokens: 65536, Source: "server"}, at: time.Time{}} // sıfır zaman = çok bayat
	ctxProbeMu.Unlock()
	if got := cachedContextWindow(cfg); got != 65536 {
		t.Fatalf("sunucu kaydı kazanmalı: %d", got)
	}
	if got := cachedContextWindow(ModelProfile{Provider: ProviderOpenAI, Model: "my-model"}); got != 0 {
		t.Fatalf("bilinmeyen model: %d", got)
	}
}

// ─── uçtan uca gövdeler ─────────────────────────────────────────────

// tuneLLM — istek gövdelerini yakalayan openai-uyumlu sahte uç. stream:true
// isteğe SSE, diğerine tek-atış JSON döner.
type tuneLLM struct {
	mu     sync.Mutex
	bodies [][]byte
	srv    *httptest.Server
}

func newTuneLLM(t *testing.T) *tuneLLM {
	t.Helper()
	l := &tuneLLM{}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		l.mu.Lock()
		l.bodies = append(l.bodies, b)
		l.mu.Unlock()
		writeTuneAnswer(w, requestWantsStream(t, b))
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func writeTuneAnswer(w http.ResponseWriter, stream bool) {
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"tamam"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, chunk := range []string{
		`{"choices":[{"delta":{"content":"tamam"},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
		`[DONE]`,
	} {
		fmt.Fprintf(w, "data: %s\n\n", chunk)
	}
}

// last — son isteğin gövdesi (çözülmüş) ve o ana kadarki istek sayısı.
func (l *tuneLLM) last(t *testing.T) (map[string]any, int) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.bodies) == 0 {
		t.Fatal("uç hiç istek almadı")
	}
	var m map[string]any
	if err := json.Unmarshal(l.bodies[len(l.bodies)-1], &m); err != nil {
		t.Fatalf("gövde JSON değil: %v", err)
	}
	return m, len(l.bodies)
}

// service — tek profilli servis (profil = çağrının modeli).
func (l *tuneLLM) service(p ModelProfile) *Service {
	p.ID, p.Provider, p.BaseURL = "p", ProviderOpenAI, l.srv.URL+"/v1"
	s := New(ProviderOpenAI, "", p.Model)
	s.Configure(ProviderOpenAI, "", p.Model, p.BaseURL, false, true)
	s.SetProfiles([]ModelProfile{p}, "p", nil)
	return s
}

// tuningKeys — gövdenin bu dilimle ilgili alanları (messages / tools gibi
// taşıyıcı alanları hariç). Olmayan anahtar haritada da olmaz.
func tuningKeys(body map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"max_tokens", "temperature", "top_p", "top_k", "presence_penalty",
		"chat_template_kwargs", "reasoning_effort", "response_format"} {
		if v, ok := body[k]; ok {
			out[k] = v
		}
	}
	return out
}

func TestQwen38FlashNextBodiesPerMode(t *testing.T) {
	thinkingOn := map[string]any{"enable_thinking": true}
	thinkingOff := map[string]any{"enable_thinking": false}
	cases := []struct {
		name    string
		profile ModelProfile
		global  *float64
		ctx     func(context.Context) context.Context
		want    map[string]any
	}{
		{"sıradan çağrı, düşünme varsayılan", ModelProfile{Model: tuneQwen}, nil, nil,
			map[string]any{"max_tokens": 32768.0, "temperature": 1.0, "top_p": 0.95, "top_k": 20.0, "presence_penalty": 0.0}},
		{"sıradan çağrı, düşünme açık", ModelProfile{Model: tuneQwen, Thinking: "on"}, nil, nil,
			map[string]any{"max_tokens": 32768.0, "temperature": 1.0, "top_p": 0.95, "top_k": 20.0, "presence_penalty": 0.0, "chat_template_kwargs": thinkingOn}},
		{"sıradan çağrı, düşünme kapalı", ModelProfile{Model: tuneQwen, Thinking: "off"}, nil, nil,
			map[string]any{"max_tokens": 4096.0, "temperature": 0.7, "top_p": 0.8, "top_k": 20.0, "presence_penalty": 1.5, "chat_template_kwargs": thinkingOff}},
		{"Derin kip", ModelProfile{Model: tuneQwen}, nil, WithDeepAnswer,
			map[string]any{"max_tokens": 49152.0, "temperature": 1.0, "top_p": 0.95, "top_k": 20.0, "presence_penalty": 0.0}},
		{"profil sıcaklığı kazanır, örnekleme alanları iner", ModelProfile{Model: tuneQwen, Temperature: f64(0.3)}, nil, nil,
			map[string]any{"max_tokens": 32768.0, "temperature": 0.3, "top_p": 0.95, "top_k": 20.0, "presence_penalty": 0.0}},
		{"küresel operatör sıcaklığı kazanır", ModelProfile{Model: tuneQwen}, f64(0.4), nil,
			map[string]any{"max_tokens": 32768.0, "temperature": 0.4, "top_p": 0.95, "top_k": 20.0, "presence_penalty": 0.0}},
		{"profil bütçesi kazanır", ModelProfile{Model: tuneQwen, MaxTokens: 8000}, nil, nil,
			map[string]any{"max_tokens": 8000.0, "temperature": 1.0, "top_p": 0.95, "top_k": 20.0, "presence_penalty": 0.0}},
		{"deterministik alt görev (katı JSON): sıcaklık 0, örnekleme alanı yok", ModelProfile{Model: tuneQwen}, nil, WithJSONMode,
			map[string]any{"max_tokens": 32768.0, "temperature": 0.0, "response_format": map[string]any{"type": "json_object"}}},
		{"deterministik alt görev, düşünme kapalı", ModelProfile{Model: tuneQwen, Thinking: "off"}, nil, WithJSONMode,
			map[string]any{"max_tokens": 4096.0, "temperature": 0.0, "response_format": map[string]any{"type": "json_object"}, "chat_template_kwargs": thinkingOff}},
	}
	for _, c := range cases {
		llm := newTuneLLM(t)
		s := llm.service(c.profile)
		if c.global != nil {
			s.ConfigureTuning(0, c.global, 0)
		}
		ctx := context.Background()
		if c.ctx != nil {
			ctx = c.ctx(ctx)
		}
		if _, _, _, err := s.explainOpenAI(ctx, "sys", "user"); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		body, n := llm.last(t)
		if n != 1 {
			t.Errorf("%s: %d istek, want 1", c.name, n)
		}
		if got := tuningKeys(body); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, got, c.want)
		}
	}
}

// Yetenek kaydı olmayan modellerin gövdesi bu sürümden ÖNCEKİ şekliyle aynı:
// tam bayt karşılaştırması (anahtar kümesi + değerler + sıra).
func TestOtherModelBodiesUnchanged(t *testing.T) {
	for _, model := range []string{tuneGemma, "my-private-model", "qwen3-30b"} {
		llm := newTuneLLM(t)
		s := llm.service(ModelProfile{Model: model})

		if _, _, _, err := s.explainOpenAI(context.Background(), "sys", "user"); err != nil {
			t.Fatalf("%s explain: %v", model, err)
		}
		want := `{"max_tokens":4096,"messages":[{"content":"sys","role":"system"},{"content":"user","role":"user"}],"model":"` + model + `","temperature":0.2}`
		if got := string(llm.bodies[0]); got != want {
			t.Errorf("%s tek-atış gövdesi değişti:\n got  %s\n want %s", model, got, want)
		}

		if _, _, _, err := s.explainOpenAI(WithDeepAnswer(context.Background()), "sys", "user"); err != nil {
			t.Fatalf("%s derin: %v", model, err)
		}
		want = `{"max_tokens":6144,"messages":[{"content":"sys","role":"system"},{"content":"user","role":"user"}],"model":"` + model + `","temperature":0.2}`
		if got := string(llm.bodies[1]); got != want {
			t.Errorf("%s Derin gövdesi değişti:\n got  %s\n want %s", model, got, want)
		}

		if _, err := s.chatOpenAIWithTools(context.Background(), "sys", []ChatMessage{{Role: "user", Text: "user"}}, nil); err != nil {
			t.Fatalf("%s tools: %v", model, err)
		}
		want = `{"max_tokens":4096,"messages":[{"content":"sys","role":"system"},{"content":"user","role":"user"}],"model":"` + model + `","temperature":0.2,"tools":[]}`
		if got := string(llm.bodies[2]); got != want {
			t.Errorf("%s araçlı gövde değişti:\n got  %s\n want %s", model, got, want)
		}
	}
}

// Örnekleme alanları ÜÇ gövde kurucusuna da iner: tek-atış, akış, araçlı
// (akışlı araç döngüsü dâhil).
func TestQwen38FlashNextSamplingReachesAllBuilders(t *testing.T) {
	want := map[string]any{"max_tokens": 32768.0, "temperature": 1.0, "top_p": 0.95, "top_k": 20.0, "presence_penalty": 0.0}
	msgs := []ChatMessage{{Role: "user", Text: "user"}}
	calls := map[string]func(*Service) error{
		"tek-atış": func(s *Service) error {
			_, _, _, err := s.explainOpenAI(context.Background(), "sys", "user")
			return err
		},
		"akış": func(s *Service) error {
			_, _, _, err := s.streamOpenAIWithUsage(context.Background(), "sys", "user", func(string) {})
			return err
		},
		"araçlı": func(s *Service) error {
			_, err := s.chatOpenAIWithTools(context.Background(), "sys", msgs, nil)
			return err
		},
		"araçlı akış": func(s *Service) error {
			_, err := s.streamToolsOpenAI(context.Background(), "sys", msgs, nil, func(TextDelta) {})
			return err
		},
	}
	for name, call := range calls {
		llm := newTuneLLM(t)
		if err := call(llm.service(ModelProfile{Model: tuneQwen})); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body, n := llm.last(t)
		if n != 1 {
			t.Errorf("%s: %d istek, want 1", name, n)
		}
		if got := tuningKeys(body); !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got  %v\n want %v", name, got, want)
		}
	}
}

// MaxTokensFor (wiki bağlam bütçesi pencereden bunu düşer) callSnapshot ile
// aynı sayıyı verir.
func TestMaxTokensForMatchesSnapshot(t *testing.T) {
	for _, c := range []struct {
		model, thinking string
		want, deep      int
	}{
		{tuneQwen, "", 32768, 49152}, {tuneQwen, "off", 4096, 6144}, {tuneGemma, "", 4096, 6144}, {"my-model", "", 4096, 6144},
	} {
		s := New(ProviderOpenAI, "", c.model)
		s.SetProfiles([]ModelProfile{{ID: "p", Provider: ProviderOpenAI, BaseURL: "http://llm.invalid/v1", Model: c.model, Thinking: c.thinking}}, "p", nil)
		if got := s.MaxTokensFor(context.Background()); got != c.want {
			t.Errorf("%s thinking=%q: %d, want %d", c.model, c.thinking, got, c.want)
		}
		if got := s.MaxTokensFor(WithDeepAnswer(context.Background())); got != c.deep {
			t.Errorf("%s thinking=%q Derin: %d, want %d", c.model, c.thinking, got, c.deep)
		}
		_, req, _, _, _ := s.callSnapshot(context.Background())
		if req.MaxTokens != c.want {
			t.Errorf("%s: callSnapshot %d, MaxTokensFor %d", c.model, req.MaxTokens, c.want)
		}
	}
}
