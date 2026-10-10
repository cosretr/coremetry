package api

import (
	"net/http"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// GET /api/ai/exchanges — v0.10.1153 (operatör, prod: "/ai her CoSRE
// etkileşimini göstermiyor"). /ai'ın "CoSRE etkileşimleri" paneli: her
// kullanıcı turu TEK satır (soru, kademe/rota, Derin, profil, süre, token —
// LLM kullanıldıysa —, sonuç, 👍/👎) ve altında turun model çağrıları
// (anlatım, sayfa seçimi, sınıflandırıcı …). LLM'siz cevaplar llmCalls=0.
// Yazım tarafı chat_exchange.go, okuma chstore/ai_exchanges.go.
//
// Admin kapılı (kardeş /api/ai/* okumaları gibi: soru/cevap örnekleri
// başka kullanıcıların sohbetidir). Önbelleksiz — /api/ai/calls emsali:
// operatör az önce sorduğu turu hemen görmeli; okuma üç küçük sorgu.
func (s *Server) listAIExchanges(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := chstore.ListAIExchangesParams{
		From:  parseTime(q.Get("from")),
		To:    parseTime(q.Get("to")),
		Limit: parseInt(q.Get("limit"), 100),
	}
	rows, err := s.store.ListAIExchanges(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	if rows == nil {
		rows = []chstore.AIExchange{}
	}
	writeJSON(w, rows)
}
