package api

// cosre_operation_catalog.go — CoSRE operasyon tespiti Faz 1: katalog işi.
// Worker'da 10 dk'da bir, Redis lider kilidiyle cosre_operation_catalog'u
// yeniden yazar (chstore.RefreshOperationCatalog: MV + oracle_error_log,
// sunucu-içi INSERT…SELECT). Oracle kapsamı (`code` alanı fonksiyon kodu
// olan kaynaklar) oracleOpScope'tan — resolve_oracle_operation ile aynı.

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const operationCatalogInterval = 10 * time.Minute

// StartOperationCatalog — main (worker kipi) çağırır.
func (s *Server) StartOperationCatalog(ctx context.Context, lock cache.Lock) {
	leader := cache.NewLeaderHolder(lock, "coremetry:lock:cosre-operation-catalog", cache.LeaderTTL(operationCatalogInterval))
	leader.Start(ctx)
	tick := func() {
		if !leader.IsLeader() || s.store == nil {
			return
		}
		sc := s.oracleOpScope()
		tctx, cancel := context.WithTimeout(chstore.WithQueryTag(ctx, "worker:operation-catalog"), 2*time.Minute)
		spanErr, oraErr := s.store.RefreshOperationCatalog(tctx, sc.CodeSources, time.Now())
		cancel()
		if spanErr != nil {
			log.Printf("[op-catalog] span: %v", spanErr)
		}
		if oraErr != nil && sc.Enabled {
			log.Printf("[op-catalog] oracle: %v", oraErr)
		}
	}
	tick()
	t := time.NewTicker(operationCatalogInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
