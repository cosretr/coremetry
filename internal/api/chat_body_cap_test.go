package api

// chat_body_cap_test.go — v0.10.1604 (operatör kararı 2026-10-29 §4): v1
// sohbet ucu (POST /api/copilot/chat) da v2 ile aynı 256 KiB gövde duvarını
// taşır; aşan gövde SSE başlığı yazılmadan 413 alır (eskiden v1'de duvar
// yoktu, v2'de MaxBytesReader hatası 400 "invalid body" dönüyordu).

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func oversizeChatBody() string {
	return `{"messages":[{"role":"user","text":"` + strings.Repeat("a", 257<<10) + `"}]}`
}

func TestChatOversizeBodyIs413(t *testing.T) {
	for _, path := range []string{"/api/copilot/chat", "/api/cosre/v2/chat"} {
		s, _ := newLoopTestServer(t, noLLM(t), "u-golden")
		setCosreV2(t, s, true)
		r := chatGoldenRequest(path, nil, false)
		r.Body = httptestBody(oversizeChatBody())
		w := serveV2(s, r)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: 257 KiB gövde → %d, 413 bekleniyordu", path, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
			t.Errorf("%s: reddedilen istekte akış başlığı yazıldı", path)
		}
	}
}

func TestChatBodyErrStatus(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want int
	}{
		{"tavan", &http.MaxBytesError{Limit: aiChatMaxRequestBytes}, http.StatusRequestEntityTooLarge},
		{"sarılı tavan", fmt.Errorf("decode: %w", &http.MaxBytesError{Limit: 1}), http.StatusRequestEntityTooLarge},
		{"bozuk JSON", errors.New("unexpected EOF"), http.StatusBadRequest},
	} {
		if got := chatBodyErrStatus(c.err); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}
