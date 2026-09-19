package api

import (
	"log"
	"sync/atomic"
)

// v0.10.813 — "şerit sayıyor, liste boş" sayacı. v0.10.329'un emptyDiag'ı bu
// uyuşmazlığı yanıta ve pod loguna yazıyordu; operatör yine ekran
// görüntüsüyle geldi (v0.10.812: aday tavanı seçici süzgeçten önce). Sayaç
// /api/health'e (`traces_empty_mismatch`) düşer: 0'dan büyükse liste sorgusu
// eşleşen span'i olan bir şekilde boş dönmüş demektir — bug sınıfı, veri
// değil. Sıfır olması beklenir; artıyorsa explain logu okunur.
var tracesEmptyMismatch atomic.Uint64

// TracesEmptyMismatch — /api/health.
func TracesEmptyMismatch() uint64 { return tracesEmptyMismatch.Load() }

// newEmptyDiag — SAF dışı tek yan etki sayaç: eşleşen span > 0 iken liste
// boşsa capMismatch=true + sayaç + uyarı satırı.
func newEmptyDiag[N int | int64 | uint64](matching N) map[string]any {
	diag := map[string]any{"matchingSpans": matching}
	if matching > 0 {
		diag["capMismatch"] = true
		tracesEmptyMismatch.Add(1)
		log.Printf("[traces] EMPTY-LIST MISMATCH: %d span süzgece uyuyor ama liste boş — aday tavanı / sorgu şekli hatası sınıfı (explain satırını oku)", matching)
	}
	return diag
}
