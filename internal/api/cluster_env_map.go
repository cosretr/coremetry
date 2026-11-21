package api

import (
	"context"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// cluster_env_map.go — follow-up 2026-11-21 (operatör onayı): bazı prod
// serilerinde deployment_environment_name yalnız KÜME adı ("clusterB"),
// diğerlerinde "prod-clusterA". Ortam "prod" seçimi yalnız-küme değerlerini
// de kapsasın diye MatchEnvRole'a küme → ortam haritası verilir; harita
// cluster VARLIĞININ span ortamlarından (service_env_summary_5m, filo geneli,
// son 24 sa; vmetrics.ClusterEnvMap — belirsiz küme haritaya girmez).
// Okuma 10 dk bellekte tutulur (*Server anahtarlı; api.go büyümez); okuma
// düşerse nil (bugünkü davranış: yalnız `<env>-<küme>` kademesi).

const (
	clusterEnvMapTTL    = 10 * time.Minute
	clusterEnvMapErrTTL = time.Minute
)

type clusterEnvMemo struct {
	mu  sync.Mutex
	m   map[string]string
	exp time.Time
}

var clusterEnvMemos sync.Map // *Server → *clusterEnvMemo

// clusterEnvMap — küçük harf küme → ortam (nil = eşleme yok).
func (s *Server) clusterEnvMap(ctx context.Context, to time.Time) map[string]string {
	if s == nil || s.store == nil {
		return nil
	}
	v, _ := clusterEnvMemos.LoadOrStore(s, &clusterEnvMemo{})
	memo := v.(*clusterEnvMemo)
	memo.mu.Lock()
	defer memo.mu.Unlock()
	now := time.Now()
	if now.Before(memo.exp) {
		return memo.m
	}
	pairs, err := s.store.ClusterEnvPairs(ctx, to.Add(-serviceClusterEnvsLookback), to)
	if err != nil {
		memo.m, memo.exp = nil, now.Add(clusterEnvMapErrTTL)
		return nil
	}
	in := make([][2]string, 0, len(pairs))
	for _, p := range pairs {
		in = append(in, [2]string{p.Cluster, p.Env})
	}
	memo.m, memo.exp = vmetrics.ClusterEnvMap(in), now.Add(clusterEnvMapTTL)
	return memo.m
}
