package main

// main_logml.go — ES log ML katmanı başlatıcısı (docs/audits/log-ml-audit.md
// §3b). main.go büyümesin diye ayrı dosya; main.go tek satır çağırır.
// Yerleşim yalnız config: COREMETRY_LOG_ML_ROLE=sre (varsayılan) | worker.
// sre pod'unda LLM drain'iyle aynı süreç ama ayrı goroutine, ayrı kilit,
// ayrı context/timeout (internal/logml.Recorder).

import (
	"context"
	"log"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/config"
	"github.com/cilcenk/coremetry/internal/logml"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func startLogML(ctx context.Context, mode runMode, cfg *config.Config, store *chstore.Store, logs logstore.Store, lock cache.Lock) {
	run, label := logml.Placement(cfg.Background.LogMLEnabled, cfg.Background.LogMLRole, logml.Roles{Worker: mode.worker, Sre: mode.sre})
	if !run {
		if cfg.Background.LogMLEnabled && (mode.worker || mode.sre) {
			log.Printf("[logml] bu pod %q rolünü taşımıyor; alt sistem burada başlamadı (COREMETRY_LOG_ML_ROLE)", label)
		}
		return
	}
	log.Printf("[logml] alt sistem başlıyor placement=%s (dedektör yok — dilim 0 iskeleti)", label)
	logml.New(store, logs, lock, label).Start(chstore.WithQueryTag(ctx, "logml:recorder"))
}
