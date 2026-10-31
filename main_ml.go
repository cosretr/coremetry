package main

// main_ml.go — ES log ML katmanı başlatıcısı (docs/audits/ml-audit.md
// §3b). main.go büyümesin diye ayrı dosya; main.go tek satır çağırır.
// Yerleşim yalnız config: COREMETRY_ML_ROLE=sre (varsayılan) | worker.
// sre pod'unda LLM drain'iyle aynı süreç ama ayrı goroutine, ayrı kilit,
// ayrı context/timeout (internal/ml.Recorder).

import (
	"context"
	"log"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/config"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func startML(ctx context.Context, mode runMode, cfg *config.Config, store *chstore.Store, logs logstore.Store, vm anomaly.REDVM, lock cache.Lock) {
	run, label := ml.Placement(cfg.Background.MLEnabled, cfg.Background.MLRole, ml.Roles{Worker: mode.worker, Sre: mode.sre})
	if !run {
		if cfg.Background.MLEnabled && (mode.worker || mode.sre) {
			log.Printf("[ml] bu pod %q rolünü taşımıyor; alt sistem burada başlamadı (COREMETRY_ML_ROLE)", label)
		}
		return
	}
	log.Printf("[ml] alt sistem başlıyor placement=%s dedektörler=log_red(log_volume,error_rate,service_silent) trace_red(op_latency,op_error_rate,throughput_drop; VM configured=%v)", label, vm != nil && vm.Configured())
	ml.New(store, logs, vm, lock, label, ml.NewLogRED(), ml.NewLogTemplates(), ml.NewLogBreakdown(), ml.NewTraceRED(), ml.NewMetricML()).Start(chstore.WithQueryTag(ctx, "ml:recorder"))
}
