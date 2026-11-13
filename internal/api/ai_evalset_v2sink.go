package api

// ai_evalset_v2sink.go — v0.10.2106 (CoSRE kalite döngüsü D1,
// docs/cosre/quality-loop-audit.md §3.1; onaylı sıra madde 4).
//
// Evalset ajan vakası artık CoSRE v2 gövdesinden koşar: sunucu ajanı
// (evalsetServerAgent) sentetik isteği copilotChatWith'e v2 işaretiyle
// (withCosreV2Loop — cosreV2Chat'in yaptığının aynısı) ve bu dosyanın
// yazıcısıyla verir. Yazıcı HTTP'ye bir şey yazmaz: uistream çeviricisinin
// ürettiği UI Message Stream parçalarını BELLEKTE toplar ("part sink").
// Böylece ölçülen akış üretimin /api/cosre/v2/chat'te yazdığı parça
// dizisinin aynısıdır (Emitter de aynı Translator'ı çağırır).
//
// Bilinçli olarak:
//   - Admin v2 bayrağı (requireCosreV2) okunmaz: bayrak kullanıcı ucunu
//     açar/kapar; koşucu v1 emekliliğinden önce v2'yi ölçmek için var.
//   - Cevap metni (rubrik girdisi) bugünkü gibi chatEvalMode.Sink'ten gelir
//     (evalAgentOutcome.Answer — iki yol aynı chatExchange'i okur); parçalar
//     ek veridir. Rubriğe akış sözleşmesi olarak girmeleri D2'nin işi.
//   - Parçalar saklanmaz (cases JSON'u değişmez); evalAgentStats.Parts'ta
//     vaka ömrünce yaşar. Tavan evalPartsMax: aşan parça sayılır, düşer.
//   - v1 yolu (copilotChat) silinmedi; koşucu artık onu çağırmıyor (D7).

import (
	"net/http"
	"sync"

	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

// evalPartsMax — vaka başına bellekte tutulan parça tavanı. Uzun bir cevap
// yüzlerce text-delta üretir; kart/araç parçaları azdır.
const evalPartsMax = 4096

// evalPartSink — chatSink: v1 olaylarını uistream parçalarına çevirip toplar.
type evalPartSink struct {
	mu      sync.Mutex
	tr      *uistream.Translator
	parts   []uistream.Part
	dropped int
	closed  bool
	max     int
}

func newEvalPartSink() *evalPartSink {
	return &evalPartSink{tr: uistream.NewTranslator(), max: evalPartsMax}
}

func (p *evalPartSink) appendLocked(ps []uistream.Part) {
	for _, part := range ps {
		if len(p.parts) >= p.max {
			p.dropped++
			continue
		}
		p.parts = append(p.parts, part)
	}
}

// Emit — Emitter.Emit'in bellek karşılığı ([DONE] işareti parça değildir).
func (p *evalPartSink) Emit(kind string, payload any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.tr.Done() {
		return
	}
	p.appendLocked(p.tr.Translate(kind, payload))
}

// Close — akış başladıysa borçlu parçalar (Translator.Finish). İdempotent.
func (p *evalPartSink) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	// Emitter'la aynı: `done` olayı akışı kapattıysa ([DONE] yazıldı) Finish
	// parçaları yazılmaz.
	if p.tr.Started() && !p.tr.Done() {
		p.appendLocked(p.tr.Finish())
	}
}

// snapshot — toplanan parçaların kopyası + tavanı aşan sayısı.
func (p *evalPartSink) snapshot() ([]uistream.Part, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]uistream.Part(nil), p.parts...), p.dropped
}

// evalV2SinkFactory — copilotChatWith'in yazıcı fabrikası: her zaman bu sink.
func evalV2SinkFactory(p *evalPartSink) chatSinkFactory {
	return func(http.ResponseWriter) (chatSink, bool) { return p, true }
}
