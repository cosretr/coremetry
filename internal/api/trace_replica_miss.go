package api

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// traceAgedOut — v0.10.810: Settings override (retention.spans) > config
// varsayılanı; okuma hatası varsayılana düşer (kararı "ıraksama" tarafına
// eğmez: yaşlıysa yaşlı der).
func (s *Server) traceAgedOut(ctx context.Context, startNs int64) bool {
	ret := ""
	if spec, err := s.store.GetRetention(ctx); err == nil {
		ret = spec.Spans
	} else {
		log.Printf("[trace] retention okunamadı, config varsayılanı: %v", err)
	}
	return chstoreTraceAgedOut(startNs, time.Now(), ret, s.store.DefaultSpansDays())
}

// chstoreTraceAgedOut — test çift değişkeni (saf çekirdek chstore'da).
var chstoreTraceAgedOut = chstore.TraceAgedOut
