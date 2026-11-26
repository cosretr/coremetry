package api

// chat_loop_shape.go — CoSRE ölçüm: serbest döngünün tur başına künyesi
// (durma nedeni, tur / çağrı, modele giden araç kataloğu boyu + JSON baytı,
// bağlam taşması). Tur bitince TEK loop_stop sistem sinyali yazılır
// (cosrelearn/loop_stop.go; cosre_signals — yeni tablo / kolon yok).
// Başarısız tur dedektörü bundan round_cap / context_overflow okur.
// Döngüye girmeyen kademeler (guided, niyet, LLM'siz) sinyal YAZMAZ.

import (
	"encoding/json"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

// toolCatalogBytes — SAF: modele gönderilen kataloğun JSON baytı (ad +
// açıklama + giriş şeması; sağlayıcı zarfı hariç — karşılaştırılabilir ölçü).
func toolCatalogBytes(specs []copilot.ToolSpec) int {
	n := 0
	for _, sp := range specs {
		raw, err := json.Marshal(struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"input_schema"`
		}{sp.Name, sp.Description, sp.InputSchema})
		if err == nil {
			n += len(raw)
		}
	}
	return n
}

// setToolCatalog — döngü kataloğu kurulunca (tur başına bir kez).
func (x *chatExchange) setToolCatalog(specs []copilot.ToolSpec) {
	if x == nil {
		return
	}
	b := toolCatalogBytes(specs)
	x.mu.Lock()
	defer x.mu.Unlock()
	x.loopRan = true
	x.loopShape.Tools, x.loopShape.SchemaBytes = len(specs), b
}

// noteOverflow — bağlam taştı: final=false geçmiş küçültülüp yeniden
// deneniyor, final=true yine sığmadı (son durum kazanır).
func (x *chatExchange) noteOverflow(final bool) {
	if x == nil {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.loopRan = true
	if final {
		x.loopShape.Overflow = cosrelearn.OverflowFailed
	} else if x.loopShape.Overflow == "" {
		x.loopShape.Overflow = cosrelearn.OverflowRetried
	}
}

// noteStopEvent — tap: döngünün "stop" olayı (chatStopEvent).
func (x *chatExchange) noteStopEvent(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	intOf := func(k string) int {
		switch n := m[k].(type) {
		case int:
			return n
		case float64:
			return int(n)
		}
		return 0
	}
	reason, _ := m["reason"].(string)
	x.mu.Lock()
	defer x.mu.Unlock()
	x.loopRan = true
	x.loopShape.Stop, x.loopShape.Calls, x.loopShape.Rounds = reason, intOf("calls"), intOf("rounds")
	x.loopShape.Free = intOf("free") // v0.10.2480 — harcanan (calls) ≠ ücretsiz (free)
}

// loopStopSignals — SAF (x kilidi altında okur): turun loop_stop satırı
// (döngü koşmadıysa yok).
func loopStopSignals(x *chatExchange) []chstore.CosreSignal {
	if x == nil {
		return nil
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.loopRan || x.root == "" {
		return nil
	}
	return []chstore.CosreSignal{{ExchangeID: x.root, Kind: cosrelearn.SigLoopStop,
		UserEmail: cosrelearn.SystemSignalActor, Detail: cosrelearn.LoopStopDetail(x.loopShape)}}
}
