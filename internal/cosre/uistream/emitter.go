package uistream

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
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
	// heartbeat — kendi döngüsü (blocks.StartHeartbeat değil): tik [DONE]'dan
	// sonra yazmamalı ve bunu yazım kilidi altında doneWritten'a bakarak bilir.
	hbStop, hbDone chan struct{}
	hbOnce         sync.Once
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
		e.hbStop, e.hbDone = make(chan struct{}), make(chan struct{})
		go e.heartbeat(o.Heartbeat)
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

// Close — ÖNCE heartbeat'i durdurup bitmesini bekler, sonra akış başladıysa
// borçlu parçalar (Translator.Finish) + [DONE]. İdempotent, nil-güvenli.
//
// v0.10.1595 — [DONE] protokolün SON satırıdır; iki boşluk `: ping`i ondan
// sonraya düşürüyordu: (1) Close [DONE]'u yazıp kilidi bıraktıktan SONRA
// heartbeat'i durduruyordu; (2) `done` olayı [DONE]'u Emit içinde yazar ve
// handler'ın ertelenmiş işleri (kayıt) bitene dek heartbeat tiklemeye devam
// ediyordu. Çare: tik yazım kilidi altında doneWritten'a bakar; Close
// heartbeat'i kilidi TUTMADAN durdurur (tik kilidi bekliyorsa alır, yazar,
// çıkar — kilitlenme yok).
func (e *Emitter) Close() {
	if e == nil {
		return
	}
	e.stopHeartbeat()
	e.mu.Lock()
	if e.tr.Started() {
		e.writeLocked(e.tr.Finish())
		e.writeDoneLocked()
	}
	e.mu.Unlock()
}

func (e *Emitter) heartbeat(every time.Duration) {
	defer close(e.hbDone)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-e.hbStop:
			return
		case <-t.C:
			e.mu.Lock()
			if !e.doneWritten {
				_, _ = io.WriteString(e.w, ": ping\n\n")
				e.f.Flush()
			}
			e.mu.Unlock()
		}
	}
}

// stopHeartbeat — durdurur ve goroutine'in bittiğini bekler; idempotent.
func (e *Emitter) stopHeartbeat() {
	if e.hbStop == nil {
		return
	}
	e.hbOnce.Do(func() { close(e.hbStop) })
	<-e.hbDone
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
