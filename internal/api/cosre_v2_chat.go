package api

// cosre_v2_chat.go — v0.10.1593 (CoSRE v2 Faz 1, R5): POST /api/cosre/v2/chat,
// UI Message Stream taşımalı sohbet ucu.
//
// Neden ayrı uç ve ayrı gövde DEĞİL: kademeler, araç döngüsü, prompt'lar ve
// sarım zinciri v1 ile birebir aynı kalmalı (operatör kuralı: döngü mantığı
// değişmez). Bu yüzden handler v1'in gövdesini (copilotChatWith) aynen
// çağırır; tek fark en alttaki yazıcı (chat_sink.go dikişi) —
// internal/cosre/uistream.Emitter.
//
// Kapı sırası: admin bayrağı EN DIŞTA (kapalıyken copilot durumundan bağımsız
// 404 — yüzey yokmuş gibi), sonra requireCopilot (503), sonra gövde tavanı
// (256 KiB, ai_conversations.go ile aynı duvar) — reddedilen istek SSE
// başlığı yazılmadan 400 alır. Yüzey etiketi /ai'da "chat" (v1 ile aynı;
// aiSurfaceFromPath).

import (
	"net/http"

	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

func init() { registerRoutesExtra("cosre-v2-chat", (*Server).registerCosreV2ChatRoutes) }

func (s *Server) registerCosreV2ChatRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/cosre/v2/chat", s.requireCosreV2(s.requireCopilot(s.cosreV2Chat)))
}

// requireCosreV2 — bayrak kapalıysa 404 (JSON).
//
// v0.10.1602 — operatör kararı 2026-10-29 §2: bayrak YALNIZ burada, istek
// başında okunur. Açık bir akış bayrak kapatılınca kesilmez, sonuna dek
// sürer; yalnız yeni istekler 404 alır (podlar arası ≤30 s memo yayılımı
// kabul). Gövdeye (copilotChatWith) bayrak okuması EKLEME —
// TestCosreV2ChatFlagOffMidStreamDoesNotCutStream pinler.
func (s *Server) requireCosreV2(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.cosreV2Enabled(r.Context()) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		h(w, r)
	}
}

func (s *Server) cosreV2Chat(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, aiChatMaxRequestBytes)
	s.copilotChatWith(w, r, v2ChatSink)
}

// v2MessageIDSeam — test dikişi: golden'lar `start` messageId'sini sabitler
// (v0.10.1603). Üretimde nil → uistream.NewMessageID (monotonik ULID).
var v2MessageIDSeam func() string

// v2ChatSink — UI Message Stream yazıcısı (heartbeat v1 ile aynı aralık).
func v2ChatSink(w http.ResponseWriter) (chatSink, bool) {
	em, ok := uistream.NewEmitter(w, uistream.Options{Heartbeat: sseHeartbeatEvery, MessageID: v2MessageIDSeam})
	if !ok {
		return nil, false // açık nil: nil *Emitter'ı arayüze sarmak nil-olmayan arayüz verir
	}
	return em, true
}
