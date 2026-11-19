package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	aiprov "github.com/cilcenk/coremetry/internal/ai/provider"
)

// v0.10.2375 — örnekleme alanlarının güvenli düşüşü (sampling_fallback.go).
// Saat SAHTE (softFields.now): test duvar saatine ve saat dilimine bağlı değil.

// fakeSoftClock — softFields'ın saatini test süresince sahte saatle değiştirir
// ve hafızayı boşaltır; ilerletme fonksiyonunu döndürür.
func fakeSoftClock(t *testing.T) (advance func(time.Duration)) {
	t.Helper()
	var mu sync.Mutex
	now := time.Unix(1_700_000_000, 0).UTC()
	softFields.mu.Lock()
	prevNow, prevUntil := softFields.now, softFields.until
	softFields.until = map[string]time.Time{}
	softFields.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	softFields.mu.Unlock()
	t.Cleanup(func() {
		softFields.mu.Lock()
		softFields.now, softFields.until = prevNow, prevUntil
		softFields.mu.Unlock()
	})
	return func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}
}

// pickyLLM — örnekleme alanı taşıyan isteği reject statüsüyle reddeden uç.
// rejectAll: alansız isteği de reddeder (hata alanlarla ilgili değil).
type pickyLLM struct {
	mu        sync.Mutex
	sampled   []bool // istek başına: örnekleme alanı taşıyor muydu
	status    int
	rejectAll bool
	srv       *httptest.Server
}

func newPickyLLM(t *testing.T, status int) *pickyLLM {
	t.Helper()
	l := &pickyLLM{status: status}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		_, hasP := m["top_p"]
		_, hasK := m["top_k"]
		_, hasPP := m["presence_penalty"]
		sampled := hasP || hasK || hasPP
		l.mu.Lock()
		l.sampled = append(l.sampled, sampled)
		rejectAll := l.rejectAll
		l.mu.Unlock()
		if sampled || rejectAll {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(l.status)
			_, _ = w.Write([]byte(`{"error":{"message":"istek reddedildi: gövde doğrulanamadı"}}`))
			return
		}
		stream, _ := m["stream"].(bool)
		writeTuneAnswer(w, stream)
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *pickyLLM) seen() []bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]bool(nil), l.sampled...)
}

func (l *pickyLLM) service(model string) *Service {
	base := l.srv.URL + "/v1"
	s := New(ProviderOpenAI, "", model)
	s.Configure(ProviderOpenAI, "", model, base, false, true)
	s.SetProfiles([]ModelProfile{{ID: "p", Provider: ProviderOpenAI, BaseURL: base, Model: model}}, "p", nil)
	return s
}

func sameBools(a []bool, b ...bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Uç alanları 400 ile reddeder: çağrı alansız yeniden denemeyle BAŞARIR,
// sonraki çağrı alanları hiç göndermez, hafıza softFieldTTL sonunda biter.
func TestSamplingFieldsRejectedRetryRememberExpire(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
		advance := fakeSoftClock(t)
		llm := newPickyLLM(t, status)
		s := llm.service(tuneQwen)
		ctx := context.Background()

		out, _, _, err := s.explainOpenAI(ctx, "sys", "user")
		if err != nil || out != "tamam" {
			t.Fatalf("%d: ilk çağrı düşmemeli: out=%q err=%v", status, out, err)
		}
		if got := llm.seen(); !sameBools(got, true, false) {
			t.Fatalf("%d: ilk çağrı [alanlı, alansız] iki istek olmalı: %v", status, got)
		}

		// Hatırlanıyor: sonraki çağrılar (tek-atış + akış) tek istek, alansız.
		if _, _, _, err := s.explainOpenAI(ctx, "sys", "user"); err != nil {
			t.Fatalf("%d: ikinci çağrı: %v", status, err)
		}
		if _, _, _, err := s.streamOpenAIWithUsage(ctx, "sys", "user", func(string) {}); err != nil {
			t.Fatalf("%d: akış çağrısı: %v", status, err)
		}
		if got := llm.seen(); !sameBools(got, true, false, false, false) {
			t.Fatalf("%d: hatırlanan uçta alan gönderilmemeli: %v", status, got)
		}
		// Sıcaklık çekirdek alandır: düşüşte de modelin önerisi kalır.
		if _, req, _, _, _ := s.callSnapshot(ctx); req.Temperature == nil || *req.Temperature != 1.0 || req.ExtraBody != nil {
			t.Fatalf("%d: düşüşte temperature=1 kalmalı, ek gövde boş: %+v", status, req)
		}

		// Süre dolmadan bir an önce hâlâ kapalı; dolunca yeniden yoklanır.
		advance(softFieldTTL - time.Second)
		if _, _, _, err := s.explainOpenAI(ctx, "sys", "user"); err != nil {
			t.Fatal(err)
		}
		if got := llm.seen(); !sameBools(got, true, false, false, false, false) {
			t.Fatalf("%d: süre dolmadan yeniden yoklandı: %v", status, got)
		}
		advance(time.Second)
		if _, _, _, err := s.explainOpenAI(ctx, "sys", "user"); err != nil {
			t.Fatalf("%d: süre sonrası çağrı: %v", status, err)
		}
		if got := llm.seen(); !sameBools(got, true, false, false, false, false, true, false) {
			t.Fatalf("%d: süre dolunca alanlar yeniden denenmeli (ve yine düşmeli): %v", status, got)
		}
	}
}

// Dört çağrı yolu da (tek-atış, akış, araçlı, araçlı akış) reddedilen uçta
// düşmeden cevap verir.
func TestSamplingFieldsRejectedEveryPathSucceeds(t *testing.T) {
	msgs := []ChatMessage{{Role: "user", Text: "user"}}
	calls := map[string]func(*Service) (string, error){
		"tek-atış": func(s *Service) (string, error) {
			out, _, _, err := s.explainOpenAI(context.Background(), "sys", "user")
			return out, err
		},
		"akış": func(s *Service) (string, error) {
			out, _, _, err := s.streamOpenAIWithUsage(context.Background(), "sys", "user", func(string) {})
			return out, err
		},
		"araçlı": func(s *Service) (string, error) {
			turn, err := s.chatOpenAIWithTools(context.Background(), "sys", msgs, nil)
			return turn.Text, err
		},
		"araçlı akış": func(s *Service) (string, error) {
			turn, err := s.streamToolsOpenAI(context.Background(), "sys", msgs, nil, func(TextDelta) {})
			return turn.Text, err
		},
	}
	for name, call := range calls {
		fakeSoftClock(t)
		llm := newPickyLLM(t, http.StatusBadRequest)
		s := llm.service(tuneQwen)
		out, err := call(s)
		if err != nil || out != "tamam" {
			t.Fatalf("%s: out=%q err=%v", name, out, err)
		}
		if got := llm.seen(); !sameBools(got, true, false) {
			t.Fatalf("%s: [alanlı, alansız] bekleniyordu: %v", name, got)
		}
		// Akış kararı YAZILMAZ: 400 akışla değil alanlarla ilgiliydi.
		base := llm.srv.URL + "/v1"
		if s.streamKnownUnsupported(ProviderOpenAI, base, tuneQwen) || s.streamKnownUnsupported(ProviderOpenAI+toolsVerdictSuffix, base, tuneQwen) {
			t.Fatalf("%s: alan reddi akış yeteneğini kapatmamalı", name)
		}
	}
}

// Alansız yeniden deneme de reddedilirse hata alanlarla ilgili değildir: İLK
// yanıt aynen döner (hata metni değişmez) ve karar yazılmaz.
func TestSamplingFallbackKeepsOriginalErrorWhenRetryFails(t *testing.T) {
	fakeSoftClock(t)
	llm := newPickyLLM(t, http.StatusBadRequest)
	llm.rejectAll = true
	s := llm.service(tuneQwen)

	_, _, _, err := s.explainOpenAI(context.Background(), "sys", "user")
	var he *aiprov.HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusBadRequest || !strings.Contains(he.Body, "gövde doğrulanamadı") {
		t.Fatalf("ilk yanıtın hatası aynen dönmeli: %v", err)
	}
	if got := llm.seen(); !sameBools(got, true, false) {
		t.Fatalf("tam bir yeniden deneme: %v", got)
	}
	if softFields.blocked(softFieldKey(llm.srv.URL+"/v1", tuneQwen)) {
		t.Fatal("alt deneme de düştü: karar yazılmamalı")
	}
	// Sonraki çağrı alanları yine dener.
	_, _, _, _ = s.explainOpenAI(context.Background(), "sys", "user")
	if got := llm.seen(); !sameBools(got, true, false, true, false) {
		t.Fatalf("karar yazılmadıysa alanlar yeniden gönderilir: %v", got)
	}
}

// Parametre reddi OLMAYAN statüler yeniden denenmez; alan taşımayan model
// (gemma) 400'de de tek istek atar.
func TestSamplingFallbackOnlyOnParameterRejection(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		fakeSoftClock(t)
		llm := newPickyLLM(t, status)
		s := llm.service(tuneQwen)
		if _, _, _, err := s.explainOpenAI(context.Background(), "sys", "user"); err == nil {
			t.Fatalf("%d: hata bekleniyordu", status)
		}
		if got := llm.seen(); !sameBools(got, true) {
			t.Errorf("%d: yeniden deneme olmamalı: %v", status, got)
		}
	}
	fakeSoftClock(t)
	llm := newPickyLLM(t, http.StatusBadRequest)
	llm.rejectAll = true
	s := llm.service(tuneGemma)
	if _, _, _, err := s.explainOpenAI(context.Background(), "sys", "user"); err == nil {
		t.Fatal("gemma 400: hata bekleniyordu")
	}
	if got := llm.seen(); !sameBools(got, false) {
		t.Errorf("gemma: tek, alansız istek: %v", got)
	}
}

// Hafıza anahtarı (base URL, model): başka model ya da başka uç etkilenmez;
// base'in sondaki "/" farkı anahtarı ayrıştırmaz; tavan aşılmaz.
func TestSoftFieldMemoryKeyAndBound(t *testing.T) {
	advance := fakeSoftClock(t)
	endpoint, ok := aiprov.OpenAIChatEndpointKey(mustURL(t, "http://llm.invalid:8000/v1/chat/completions"))
	if !ok {
		t.Fatal("sohbet ucu tanınmadı")
	}
	softFields.mark(endpoint + "|" + tuneQwen)
	for _, base := range []string{"http://llm.invalid:8000/v1", "http://llm.invalid:8000/v1/", "http://llm.invalid:8000/v1//"} {
		if !softFields.blocked(softFieldKey(base, tuneQwen)) {
			t.Errorf("base %q: taşıyıcının yazdığı anahtarla eşleşmeli", base)
		}
	}
	if softFields.blocked(softFieldKey("http://llm.invalid:8000/v1", "qwen38-flash-other")) {
		t.Error("karar başka modele sızdı")
	}
	if softFields.blocked(softFieldKey("http://other.invalid:8000/v1", tuneQwen)) {
		t.Error("karar başka uca sızdı")
	}
	if _, ok := aiprov.OpenAIChatEndpointKey(mustURL(t, "http://llm.invalid:8000/v1/models")); ok {
		t.Error("/models bir sohbet ucu değil")
	}

	for i := 0; i < softFieldMaxKeys*2; i++ {
		softFields.mark("k" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + time.Duration(i).String())
	}
	softFields.mu.Lock()
	n := len(softFields.until)
	softFields.mu.Unlock()
	if n > softFieldMaxKeys {
		t.Fatalf("hafıza tavanı aşıldı: %d > %d", n, softFieldMaxKeys)
	}
	advance(softFieldTTL)
	if softFields.blocked(softFieldKey("http://llm.invalid:8000/v1", tuneQwen)) {
		t.Error("süre dolunca karar bitmeli")
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
