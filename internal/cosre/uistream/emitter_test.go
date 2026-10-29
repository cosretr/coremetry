package uistream

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// emitter_test.go — v0.10.1591 (CoSRE v2 Faz 1, R3): başlıklar, çerçeve,
// [DONE], Close yolları ve yarış (-race).

type noFlush struct{ h http.Header }

func (n *noFlush) Header() http.Header         { return n.h }
func (n *noFlush) Write(b []byte) (int, error) { return len(b), nil }
func (n *noFlush) WriteHeader(int)             {}

func TestEmitterRejectsNonFlusher(t *testing.T) {
	if e, ok := NewEmitter(&noFlush{h: http.Header{}}, Options{}); ok || e != nil {
		t.Fatal("Flusher'sız yazıcı kabul edildi")
	}
}

func TestEmitterHeadersFramingAndDone(t *testing.T) {
	rec := httptest.NewRecorder()
	e, ok := NewEmitter(rec, Options{})
	if !ok {
		t.Fatal("recorder reddedildi")
	}
	for k, want := range map[string]string{
		HeaderName: ProtocolVersion, "Content-Type": "text/event-stream",
		"Cache-Control": "no-cache", "X-Accel-Buffering": "no", "Connection": "keep-alive",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("başlık %s = %q, want %q", k, got, want)
		}
	}
	e.Emit("done", map[string]bool{"ok": true})
	e.Close()
	e.Close() // idempotent
	want := "data: {\"type\":\"start\"}\n\n" +
		"data: {\"type\":\"start-step\"}\n\n" +
		"data: {\"type\":\"finish-step\"}\n\n" +
		"data: {\"finishReason\":\"stop\",\"type\":\"finish\"}\n\n" +
		"data: [DONE]\n\n"
	if rec.Body.String() != want {
		t.Fatalf("gövde:\n%q\nwant:\n%q", rec.Body.String(), want)
	}
}

func TestEmitterCloseWithoutFramesWritesNothing(t *testing.T) {
	rec := httptest.NewRecorder()
	e, _ := NewEmitter(rec, Options{})
	e.Close()
	if rec.Body.Len() != 0 {
		t.Fatalf("boş akışta gövde: %q", rec.Body.String())
	}
	var nilE *Emitter
	nilE.Close() // nil-güvenli
}

func TestEmitterCloseMidStreamFinishes(t *testing.T) {
	rec := httptest.NewRecorder()
	e, _ := NewEmitter(rec, Options{})
	e.Emit("step", map[string]any{"i": 1, "label": "x"})
	e.Close()
	body := rec.Body.String()
	for _, want := range []string{`"class":"closed"`, `"finishReason":"error"`} {
		if !strings.Contains(body, want) {
			t.Errorf("gövde %q içermiyor", want)
		}
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Errorf("[DONE] son satır değil: %q", body)
	}
	if n := strings.Count(body, "[DONE]"); n != 1 {
		t.Errorf("[DONE] %d kez", n)
	}
}

// 8 goroutine Emit + 1 ms heartbeat + Close: recorder'a her yazım Emitter'ın
// kilidi altında (heartbeat aynı kilidi paylaşır); -race bunu ölçer.
func TestEmitterConcurrentEmitHeartbeatClose(t *testing.T) {
	rec := httptest.NewRecorder()
	e, _ := NewEmitter(rec, Options{Heartbeat: time.Millisecond})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				e.Emit("delta", map[string]string{"text": "x"})
			}
		}(g)
	}
	time.Sleep(3 * time.Millisecond)
	wg.Wait()
	e.Close()
	body := rec.Body.String()
	if n := strings.Count(body, `"type":"text-delta"`); n != 400 {
		t.Fatalf("text-delta %d, want 400", n)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatal("[DONE] son değil")
	}
	// Close sonrası heartbeat yazmaz.
	n := rec.Body.Len()
	time.Sleep(5 * time.Millisecond)
	if rec.Body.Len() != n {
		t.Fatal("Close'dan sonra yazım")
	}
}

// v0.10.1596 — regresyon (v0.10.1591'in emitter'ı): `: ping` [DONE]'dan
// sonra düşüyordu. İki boşluk: Close [DONE]'u yazıp kilidi bıraktıktan SONRA
// heartbeat'i durduruyordu (TestEmitterConcurrentEmitHeartbeatClose
// -count=50 -race'te 14/50 kırmızı), ve `done` olayı [DONE]'u Emit içinde
// yazdıktan sonra heartbeat Close'a dek tiklemeye devam ediyordu (bu test
// eski kodda 5/5 kırmızı). Mikrosaniyelik heartbeat'le: [DONE] her seferinde
// SON satır.
func TestEmitterCloseNoPingAfterDone(t *testing.T) {
	for i := 0; i < 300; i++ {
		rec := httptest.NewRecorder()
		e, _ := NewEmitter(rec, Options{Heartbeat: time.Microsecond})
		e.Emit("done", map[string]bool{"ok": true})
		time.Sleep(time.Duration(i%3) * time.Microsecond)
		e.Close()
		if body := rec.Body.String(); !strings.HasSuffix(body, "data: [DONE]\n\n") {
			t.Fatalf("tur %d: [DONE] son satır değil: ...%q", i, body[max(0, len(body)-60):])
		}
	}
}
