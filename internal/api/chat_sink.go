package api

import (
	"errors"
	"net/http"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
)

// chat_sink.go — v0.10.1590 (CoSRE v2 Faz 1, R2): sohbet SSE dikişi.
//
// copilotChatWith'in gövdesindeki her sarmalayıcı (withStepIDs →
// turnBlockCapture → withBlockSeq → xch.tap → armDisambigRescue) v1
// sözlüğünde `(kind, payload)` konuşur. Taşımayı değiştirmenin TEK doğru
// yeri zincirin en altıdır: orada çerçeveler tamamen süslenmiştir (i,
// bN/seq/final, callId/turnId). v1 ve v2 aynı gövdeyi paylaşır; yalnız bu
// fabrika farklıdır. Kanıt: chat_stream_golden_test.go (v1 bayt altını).

// chatSink — handler'ın en alt yazıcısı (blocks.Emitter ile aynı iki metot).
type chatSink interface {
	Emit(kind string, payload any)
	Close()
}

// chatSinkFactory — ok=false: yazıcı akış yapamıyor (Flusher yok).
type chatSinkFactory func(w http.ResponseWriter) (chatSink, bool)

// v1ChatSink — `event:`/`data:` çerçeveli v1 SSE yazıcısı.
func v1ChatSink(w http.ResponseWriter) (chatSink, bool) {
	em, ok := blocks.NewEmitter(w, blocks.Options{EagerHeaders: true, Heartbeat: sseHeartbeatEvery})
	if !ok {
		return nil, false // açık nil: nil *Emitter'ı arayüze sarmak nil-olmayan arayüz verir
	}
	return em, true
}

// copilotChat is the SSE chat endpoint (v1 transport). Runs the agentic
// loop and streams progress + the final answer. Gövde duvarı (256 KiB,
// 413) copilotChatWith'in başında — v1 ve v2 için tek yer (v0.10.1604).
func (s *Server) copilotChat(w http.ResponseWriter, r *http.Request) {
	s.copilotChatWith(w, r, v1ChatSink)
}

// chatBodyErrStatus — SAF: gövde çözme hatasının HTTP durumu. 256 KiB
// duvarı (MaxBytesReader) 413, diğer her şey 400 (v0.10.1604).
func chatBodyErrStatus(err error) int {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}
