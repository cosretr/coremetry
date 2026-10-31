package api

// cosre_briefing_phrase.go — CoSRE Login Briefing Faz 1, LLM başlık cümlesi (Q8).
//
// Model YALNIZ başlık kurar; satırlar ve sayılar deterministik kalır. Çağrı
// koşulları briefing.NeedsLLM'de (ayar llm_phrasing açık + model hazır + ≥3
// bulgu; ≤2 bulgu şablon). Girdi briefing.CompactInput (metin/URL yok, ≤5 KB
// ≈ ≤1500 tok), completion tavanı WithMaxTokensCap(300). Cevap
// briefing.AcceptHeadline'dan geçmezse (rakam, markdown, uzun) şablon kalır.
// Önbellek: aynı bulgular (FindingsHash) 1 sa yeniden cümleleştirilmez.
// Atıf: copilotExplainSurface → ai_calls surface "cosre-briefing" (token
// kullanımı /ai'da), ayrıca tek log satırı.

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosre/briefing"
)

const (
	briefingLLMSurface = "cosre-briefing"
	briefingLLMTTL     = time.Hour
	briefingLLMTimeout = 30 * time.Second
)

// briefingLLMKey — SAF: bulgu özeti (kimlik/zaman hariç) + istem sürümü.
func briefingLLMKey(b briefing.Briefing) string {
	return "briefing:llm:v1:" + briefing.FindingsHash(b)
}

// phraseBriefing — başlığı LLM ile kurar; her başarısızlıkta şablon kalır.
func (s *Server) phraseBriefing(ctx context.Context, b *briefing.Briefing, cfg briefing.Config) {
	if !briefing.NeedsLLM(*b, cfg.LLMPhrasing, s.copilotReady()) {
		return
	}
	key := briefingLLMKey(*b)
	if s.cache != nil {
		if raw, ok, _ := s.cache.Get(ctx, key); ok && len(raw) > 0 {
			if h, ok := briefing.AcceptHeadline(string(raw)); ok {
				b.Summary, b.Phrasing = h, "llm"
				return
			}
		}
	}
	input := briefing.CompactInput(*b)
	cctx, cancel := context.WithTimeout(copilot.WithMaxTokensCap(ctx, briefing.LLMMaxTokens), briefingLLMTimeout)
	defer cancel()
	t0 := time.Now()
	out, err := s.copilotExplainSurface(cctx, briefingLLMSurface, copilot.SystemPromptBriefingHeadline(), string(input))
	if err != nil {
		log.Printf("[briefing] başlık modeli düştü (%s/%q, %d B girdi): %v — şablon", b.TeamKey, b.Env, len(input), err)
		return
	}
	h, ok := briefing.AcceptHeadline(out)
	log.Printf("[briefing] başlık %s/%q: %d B girdi, %s, kabul=%v", b.TeamKey, b.Env, len(input), time.Since(t0).Round(time.Millisecond), ok)
	if !ok {
		return
	}
	b.Summary, b.Phrasing = h, "llm"
	if s.cache != nil {
		_ = s.cache.Set(ctx, key, []byte(h), briefingLLMTTL)
	}
}
