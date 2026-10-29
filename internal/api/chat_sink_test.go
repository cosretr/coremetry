package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// chat_sink_test.go — v0.10.1590 (CoSRE v2 Faz 1, R2): sohbet SSE dikişi.
// copilotChat'in gövdesi copilotChatWith'e taşındı; en alttaki yazıcı bir
// fabrika parametresi (chatSink). v1 çıktısının bayt bayt aynı kaldığının
// kanıtı R1 altınları (chat_stream_golden_test.go); burası dikişin TEK
// olduğunu pinler — ikinci bir NewEmitter çağrısı v2'nin göremeyeceği bir
// yazım yolu açar.

func TestChatSinkSeamIsTheOnlyEmitterConstruction(t *testing.T) {
	body, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	if strings.Contains(src, "blocks.NewEmitter(") {
		t.Error("copilot_chat.go blocks.NewEmitter çağırıyor — yazıcı yalnız chatSink fabrikasından gelir")
	}
	if n := strings.Count(src, "newSink(w)"); n != 1 {
		t.Errorf("copilot_chat.go'da newSink(w) %d kez, tam 1 bekleniyor", n)
	}
	if !strings.Contains(src, "func (s *Server) copilotChatWith(w http.ResponseWriter, r *http.Request, newSink chatSinkFactory)") {
		t.Error("copilotChatWith imzası bulunamadı")
	}
	sink, err := os.ReadFile("chat_sink.go")
	if err != nil {
		t.Fatal(err)
	}
	ssrc := string(sink)
	if n := strings.Count(ssrc, "blocks.NewEmitter("); n != 1 {
		t.Errorf("chat_sink.go'da blocks.NewEmitter( %d kez, tam 1 bekleniyor", n)
	}
	re := regexp.MustCompile(`func \(s \*Server\) copilotChat\(w http\.ResponseWriter, r \*http\.Request\) \{\s*s\.copilotChatWith\(w, r, v1ChatSink\)\s*\}`)
	if !re.MatchString(ssrc) {
		t.Error("copilotChat tek satır s.copilotChatWith(w, r, v1ChatSink) olmalı")
	}
}

// noFlushWriter — http.Flusher OLMAYAN yazıcı (ara katman sarımı).
type noFlushWriter struct{ h http.Header }

func (n *noFlushWriter) Header() http.Header         { return n.h }
func (n *noFlushWriter) Write(b []byte) (int, error) { return len(b), nil }
func (n *noFlushWriter) WriteHeader(int)             {}

func TestV1ChatSinkRejectsNonFlusher(t *testing.T) {
	sink, ok := v1ChatSink(&noFlushWriter{h: http.Header{}})
	if ok {
		t.Fatal("Flusher'sız yazıcıda ok=true")
	}
	// nil *Emitter'ı nil-olmayan arayüze sarmak klasik tuzak: arayüz nil olmalı.
	if sink != nil {
		t.Fatalf("ok=false iken sink nil değil: %#v", sink)
	}
	if sink, ok := v1ChatSink(httptest.NewRecorder()); !ok || sink == nil {
		t.Fatal("recorder (Flusher) reddedildi")
	}
}
