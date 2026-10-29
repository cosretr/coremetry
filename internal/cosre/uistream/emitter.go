package uistream

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
)

// Options — Heartbeat > 0: `: ping\n\n` yorumu bu aralıkla (yazım kilidi paylaşılır).
type Options struct {
	Heartbeat time.Duration
}

// Emitter — v1 olaylarını UI Message Stream SSE olarak yazar. blocks.Emitter
// ile aynı iki metot (Emit, Close): sohbet gövdesinin chatSink'i.
type Emitter struct {
	w           http.ResponseWriter
	f           http.Flusher
	mu          sync.Mutex
	tr          *Translator
	doneWritten bool
	hb          *blocks.Heartbeat
}

// NewEmitter — ok=false: yazıcı Flush edemiyor. Başlıklar kurucuda yazılır
// (eager): ilk LLM çağrısı uzun sürebilir, proxy sessiz bağlantıyı keser.
func NewEmitter(w http.ResponseWriter, o Options) (*Emitter, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	h.Set(HeaderName, ProtocolVersion)
	e := &Emitter{w: w, f: f, tr: NewTranslator()}
	if o.Heartbeat > 0 {
		e.hb = blocks.StartHeartbeat(&e.mu, w, f, o.Heartbeat)
	}
	return e, true
}

// Emit — olayı çevirir, her parçayı `data: <json>\n\n` yazar ve flush eder.
func (e *Emitter) Emit(kind string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.writeLocked(e.tr.Translate(kind, payload))
	if e.tr.Done() {
		e.writeDoneLocked()
	}
}

// Close — akış başladıysa borçlu parçalar (Translator.Finish) + [DONE];
// sonra heartbeat'i durdurup bitmesini bekler. İdempotent, nil-güvenli.
func (e *Emitter) Close() {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.tr.Started() {
		e.writeLocked(e.tr.Finish())
		e.writeDoneLocked()
	}
	e.mu.Unlock()
	e.hb.Stop()
}

func (e *Emitter) writeLocked(parts []Part) {
	if len(parts) == 0 || e.doneWritten {
		return
	}
	for _, p := range parts {
		b, err := json.Marshal(p)
		if err != nil {
			continue
		}
		_, _ = io.WriteString(e.w, "data: ")
		_, _ = e.w.Write(b)
		_, _ = io.WriteString(e.w, "\n\n")
	}
	e.f.Flush()
}

func (e *Emitter) writeDoneLocked() {
	if e.doneWritten {
		return
	}
	_, _ = io.WriteString(e.w, "data: "+DoneMarker+"\n\n")
	e.f.Flush()
	e.doneWritten = true
}
