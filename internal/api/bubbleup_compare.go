package api

// bubbleup_compare.go — dalga 3 (2026-11-21, Honeycomb BubbleUp
// "brush to compare") — GET /api/spans/bubbleup/compare.
//
// /api/spans/bubbleup aynı-pencere alt-küme kıyasıdır (seçim = baseline'ın
// süzgeçle daraltılmış hali, iki taraf aynı [from,to]). Grafik fırçası ise
// bir ZAMAN dilimi seçer: "bu dilimdeki span'ler, grafiğin tüm penceresine
// göre ne farklı?". Store bunu zaten destekliyor (chstore.BubbleUp ayrı
// baseFrom/baseTo + selFrom/selTo alır; ServiceBubbleUp zaman-kaydırmalı
// kıyas için kullanır) — eksik olan yalnız HTTP ucuydu. api.go büyümesin
// diye ayrı dosya.
//
//	GET /api/spans/bubbleup/compare?filters=&dsl=&selFrom=&selTo=&baseFrom=&baseTo=
//
// İki taraf da AYNI süzgeç (filters/dsl); fark yalnız pencere. Pencereler
// zaman sınırlı ve birleşimleri ≤ 24 sa (ham spans taraması; store sorgusu
// LIMIT + max_execution_time taşır). Rol kapısı yok — salt okuma.
// serveCached 60 sn; anahtar TÜM girdileri taşır (iki pencere + süzgeçler).
//
// 2026-11-21 — attribute kıyası doğası gereği span satırı ister (MV'de
// attribute dizisi yok), o yüzden ham kalır ama SINIRLI: birleşim ≤ 24 sa,
// her taraf en çok 3 dk ZAMAN okur (pencere genişse 12 × 15 sn eşit
// aralıklı dilim; chstore.bubbleUpSlices) — zaman PK'da olduğu için okunan
// bayt pencereyle değil bütçeyle orantılı (prod 24 sa ≈ 2.3B span → taraf
// başına ≈ 4.9M). Bedel: sayılar yaklaşık (1/oran ile ölçekli; yanıt
// selectionSampleRate/baselineSampleRate taşır), dilimler arasına düşen
// kısa olaylar ve seyrek değerler (örneklemde < 5 satır, HAVING) kaçabilir;
// yüzdeler ve skor günün saatine yayılmış örnekte yansız.

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const bubbleUpCompareMaxSpan = 24 * time.Hour

func init() {
	registerRoutesExtra("spans-bubbleup-compare", (*Server).registerBubbleUpCompareRoutes)
}

func (s *Server) registerBubbleUpCompareRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/spans/bubbleup/compare", s.spanBubbleUpCompare)
}

// bubbleUpCompareWindows — SAF: dört zaman parametresini doğrular.
func bubbleUpCompareWindows(selFrom, selTo, baseFrom, baseTo time.Time) error {
	if selFrom.IsZero() || selTo.IsZero() || baseFrom.IsZero() || baseTo.IsZero() {
		return fmt.Errorf("selFrom, selTo, baseFrom, baseTo are required")
	}
	if !selTo.After(selFrom) || !baseTo.After(baseFrom) {
		return fmt.Errorf("each window must have to > from")
	}
	lo, hi := baseFrom, baseTo
	if selFrom.Before(lo) {
		lo = selFrom
	}
	if selTo.After(hi) {
		hi = selTo
	}
	if hi.Sub(lo) > bubbleUpCompareMaxSpan {
		return fmt.Errorf("windows span more than %s", bubbleUpCompareMaxSpan)
	}
	return nil
}

func (s *Server) spanBubbleUpCompare(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filters, err := parseFiltersAndDSL(q.Get("filters"), q.Get("dsl"))
	if err != nil {
		http.Error(w, "invalid query DSL: "+err.Error(), http.StatusBadRequest)
		return
	}
	selFrom, selTo := parseTime(q.Get("selFrom")), parseTime(q.Get("selTo"))
	baseFrom, baseTo := parseTime(q.Get("baseFrom")), parseTime(q.Get("baseTo"))
	if err := bubbleUpCompareWindows(selFrom, selTo, baseFrom, baseTo); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// v2 (2026-11-21): yanıt örnekleme oranlarını taşır (şekil değişti).
	key := fmt.Sprintf("span-bubbleup-compare:v2:sel=%s:base=%s:f=%s:d=%s",
		cacheBucket(selFrom, selTo), cacheBucket(baseFrom, baseTo),
		q.Get("filters"), q.Get("dsl"))
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		// Seçim tarafı baseline süzgeciyle, kendi penceresinde; her taraf
		// pencere genişliğine göre trace-örneklenir (chstore.BubbleUpCompareSampled).
		return s.store.BubbleUpCompareSampled(ctx, filters, baseFrom, baseTo, selFrom, selTo)
	})
}
