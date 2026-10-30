package api

// cosre_system_signal.go — v0.10.1637 (CoSRE kritik yol takibi): SUNUCU
// kaynaklı cosre_signals satırları. İlk kullanıcı trace açıklamasının "dış
// bağımlılık bekleme" satırı (trace_explain_latency_owner.go): kural
// uygulandığında prefix_applied (payda), sunucu cevabı değiştirdiğinde
// prefix_corrected (action=added|corrected). Haftalık oran GET /api/ai/quality
// "prefix" alanında (/ai → CoSRE kalite paneli).
//
// Fire-and-forget: yazım isteğin bağlamından AYRI, zaman aşımlı goroutine'de;
// hata yalnız log satırıdır — cevap metni yazımı hiç beklemez, sonucunu hiç
// okumaz. Kapalı tür kümesi cosrelearn.SystemSignalKinds; aktör sabit
// cosrelearn.SystemSignalActor (kullanıcı e-postası değil). İstemci POST
// /api/ai/signal bu türleri gönderemez (ClientSignalKinds dışı).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

const systemSignalWriteTimeout = 5 * time.Second

// systemSignalWriterSeam — TEST DİKİŞİ (prod'da boş → s.store).
var systemSignalWriterSeam atomic.Pointer[func(context.Context, chstore.CosreSignal) error]

func setSystemSignalWriter(fn func(context.Context, chstore.CosreSignal) error) {
	if fn == nil {
		systemSignalWriterSeam.Store(nil)
		return
	}
	systemSignalWriterSeam.Store(&fn)
}

// validateSystemSignal — SAF: kapalı tür kümesi, sistem aktörü, kimlik ve
// ayrıntı tavanı (istemci yolu validateCosreSignal'ın aynı sınırları).
func validateSystemSignal(g chstore.CosreSignal) (chstore.CosreSignal, error) {
	switch {
	case !cosrelearn.SystemSignalKinds[g.Kind]:
		return g, fmt.Errorf("unknown system signal kind %q", g.Kind)
	case g.UserEmail != cosrelearn.SystemSignalActor:
		return g, errors.New("system signal must carry the system actor")
	case len(g.ExchangeID) > aiFeedbackMaxIDLen:
		return g, errors.New("exchangeId too long")
	}
	g.Detail = clipBytes(g.Detail, cosreSignalDetailMax)
	return g, nil
}

// latencyOwnerSignals — SAF: olay → yazılacak satırlar (uygulanmadıysa yok).
func latencyOwnerSignals(xid string, ev latencyOwnerEvent) []chstore.CosreSignal {
	if !ev.Applied {
		return nil
	}
	out := []chstore.CosreSignal{{ExchangeID: xid, Kind: cosrelearn.SigPrefixApplied,
		UserEmail: cosrelearn.SystemSignalActor, Detail: "trace=" + ev.TraceID}}
	if ev.Action != "" {
		out = append(out, chstore.CosreSignal{ExchangeID: xid, Kind: cosrelearn.SigPrefixCorrected,
			UserEmail: cosrelearn.SystemSignalActor,
			Detail:    "action=" + ev.Action + " trace=" + ev.TraceID + " target=" + ev.Target})
	}
	return out
}

// recordLatencyOwner — log satırı (değişiklik başına bir) + sinyaller; asla
// bloklamaz, asla hata döndürmez.
func (s *Server) recordLatencyOwner(ctx context.Context, ev latencyOwnerEvent) {
	if !ev.Applied {
		return
	}
	xid := copilot.MetaFromContext(ctx).ExchangeID
	if ev.Action != "" {
		log.Printf("cosre prefix_corrected action=%s exchange=%s trace=%s target=%q computed=%q model=%q",
			ev.Action, xid, ev.TraceID, ev.Target, ev.Computed, ev.ModelLine)
	}
	sigs := latencyOwnerSignals(xid, ev)
	write := func(c context.Context, g chstore.CosreSignal) error { return s.store.InsertCosreSignal(c, g) }
	if p := systemSignalWriterSeam.Load(); p != nil {
		write = *p
	} else if s.store == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("cosre system signal: panic: %v", r)
			}
		}()
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), systemSignalWriteTimeout)
		defer cancel()
		for _, g := range sigs {
			g, err := validateSystemSignal(g)
			if err == nil {
				err = write(c, g)
			}
			if err != nil {
				log.Printf("cosre system signal: kind=%s exchange=%s: %s", g.Kind, xid, strings.TrimSpace(err.Error()))
			}
		}
	}()
}
