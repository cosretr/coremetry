package api

// anomaly_subjects_flip.go — v0.10.2308: Davis özne varsayılanının
// (endpoint + queue açık, terfi gölge) mevcut kurulumlara tek seferlik
// geçişinin koşturucusu. Mantık + işaret chstore.MigrateSubjectsDefaultFlip'te
// (minBaselineRate göçü emsali). Koşturucu topology_hidden_seed.go gibi
// LİDER-YALNIZ: aynı anda açılan podlardan yalnız kilit sahibi yazar, işaret
// tek kez düşer. Ayar okuma yolunda (LoadAnomalySensitivity, 30 sn tur)
// DEĞİL — o yol her podda koşar. Diğer podlar yeni blobu bir sonraki
// tazeleme turunda görür.

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const subjectsFlipRetry = time.Minute

// StartSubjectsDefaultFlip — main (worker kipi) çağırır. Lider kilidi
// alındığında koşar; başarıya dek dakikada bir dener, sonra döner.
func (s *Server) StartSubjectsDefaultFlip(ctx context.Context, lock cache.Lock) {
	if s.store == nil {
		return
	}
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	leader := cache.NewLeaderHolder(lock, "coremetry:lock:anomaly-subjects-flip", cache.LeaderTTL(subjectsFlipRetry))
	leader.Start(lctx)
	t := time.NewTicker(subjectsFlipRetry)
	defer t.Stop()
	for {
		if leader.IsLeader() {
			out, err := chstore.MigrateSubjectsDefaultFlip(lctx, s.store, time.Now())
			if err == nil {
				if out != "" {
					log.Printf("[settings] özne varsayılan geçişi: %s", out)
				}
				if out == chstore.SubjectsFlipApplied {
					s.LoadAnomalySensitivity(lctx) // bu pod hemen yayınlasın
				}
				return
			}
			log.Printf("[settings] özne varsayılan geçişi (sonraki turda yeniden): %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
