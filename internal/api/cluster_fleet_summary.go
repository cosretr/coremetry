package api

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// v0.10.2185 — GET /api/clusters/fleet-summary: /clusters genel görünümünün
// TÜM kartları tek yanıtta, Redis'teki paylaşılan anlık görüntüden.
//
// Operatör-bildirimi (prod, 20 cluster): ilk sayfa yavaş açılıyordu. Kök
// neden: istemci kart başına /api/clusters/summary atıyordu (N istek, her
// biri soğukta ~10 sorguluk Thanos turu; tarayıcı bağlantı tavanı istekleri
// sıraya diziyor, en yavaş cluster ilk boyamayı belirliyordu).
//
// Redis varken (operatör kararı: replikalar arası paylaşımlı):
//   - görüntü Redis'te yaşar, uç onu HEMEN döner (generated_at ile);
//   - her replikanın arka plan döngüsü clusterFleetRefreshEvery'de bir
//     SetNX hakkı dener; turu YALNIZ hakkı kazanan replika hesaplar
//     (warmDepsClaimKey emsali — kilit değil, TTL'li tur hakkı), ötekiler
//     Redis'ten okur;
//   - görüntü yoksa ilk istek hakkı dener: kazanırsa hesaplar, kaybederse
//     kazananın yazmasını Redis'te bekler (süreç içi singleflight'lı).
// Redis yoksa (Noop): istek üstünde singleflight'lı hesap + süreç-içi kopya.
//
// Boşta tasarruf: döngü son istekten clusterFleetIdleAfter sonra tur
// denemez (kimse bakmıyorken Thanos'a yük yok); sonraki istek eski görüntüyü
// döner ve tazelemeyi tetikler — istemci bayatlığı generated_at'ten okur.
// Ayar/düğme yok.
//
// Kart sözleşmesi aynı: bir cluster'ın metrikleri okunamazsa kaydı `error`
// taşır, kart eskisi gibi "yanıt vermiyor" der. Kapasite tahmini (forecast)
// burada YOK — yalnız detay görünümü kullanır (/api/clusters/summary).

func init() {
	registerRoutesExtra("cluster-fleet-summary", (*Server).registerClusterFleetRoutes)
}

func (s *Server) registerClusterFleetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/clusters/fleet-summary", s.getClusterFleetSummary)
}

const (
	clusterFleetRefreshEvery   = 60 * time.Second // eski kart başına serveCached TTL'iyle aynı
	clusterFleetClaimTTL       = clusterFleetRefreshEvery - 5*time.Second
	clusterFleetIdleAfter      = 10 * time.Minute
	clusterFleetClusterTimeout = 10 * time.Second // getClusterSummary ile aynı tavan
	clusterFleetRoundTimeout   = 45 * time.Second
	clusterFleetWaitPoll       = 500 * time.Millisecond
	clusterFleetParallel       = 8
	clusterFleetRedisTTL       = 30 * time.Minute
	clusterFleetRedisTimeout   = 2 * time.Second
	clusterFleetCacheKeyPrefix = "coremetry:cluster-fleet:v1:"
)

type clusterFleetEntry struct {
	Name    string                 `json:"name"`
	Summary *thanos.ClusterSummary `json:"summary,omitempty"`
	Error   string                 `json:"error,omitempty"`
}

type clusterFleetSnapshot struct {
	Digest         string              `json:"digest"`
	GeneratedAt    time.Time           `json:"generated_at"`
	RefreshSeconds int                 `json:"refresh_seconds"`
	Clusters       []clusterFleetEntry `json:"clusters"`
}

type clusterFleetMemo struct {
	mu      sync.Mutex
	local   *clusterFleetSnapshot // yalnız Noop kipinde
	lastHit time.Time
	started bool
	sf      singleflight.Group
}

var clusterFleetMemos sync.Map // *Server → *clusterFleetMemo (api.go büyümez)

func (s *Server) clusterFleetMemo() *clusterFleetMemo {
	if m, ok := clusterFleetMemos.Load(s); ok {
		return m.(*clusterFleetMemo)
	}
	m, _ := clusterFleetMemos.LoadOrStore(s, &clusterFleetMemo{})
	return m.(*clusterFleetMemo)
}

// clusterFleetConfigs — etkin cluster'lar /api/clusters/sources sırasıyla +
// TÜM girdilerin digest'i (ad + URL + namespace süzgeci; cache anahtarı
// kuralı). Admin düzenlemesi digest'i değiştirir → eski görüntü kullanılmaz.
func (s *Server) clusterFleetConfigs() ([]thanos.ClusterConfig, string) {
	names, _ := clusterSourcesPayload(s.thanos.Snapshot())["clusters"].([]string)
	cfgs := make([]thanos.ClusterConfig, 0, len(names))
	h := fnv.New64a()
	for _, n := range names {
		cfg, ok := s.thanos.ClusterByRef(n)
		if !ok {
			continue
		}
		cfgs = append(cfgs, cfg)
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write([]byte(clusterCfgDigest(cfg)))
		h.Write([]byte{0})
	}
	return cfgs, fmt.Sprintf("%x", h.Sum64())
}

func (s *Server) getClusterFleetSummary(w http.ResponseWriter, r *http.Request) {
	if s.thanos == nil || !s.thanos.HasEnabledClusters() {
		http.Error(w, "no thanos clusters configured", http.StatusNotFound)
		return
	}
	m := s.clusterFleetMemo()
	_, digest := s.clusterFleetConfigs()
	shared := !cache.IsNoop(s.cache)

	var snap *clusterFleetSnapshot
	if shared {
		m.touch(s)
		snap = s.clusterFleetLoad(r.Context(), digest)
	} else {
		snap = m.localSnap(digest)
	}
	if snap == nil {
		// İlk istek: bu süreçte tek tur (eşzamanlı istekler aynı sonucu bekler).
		v, _, _ := m.sf.Do("first:"+digest, func() (any, error) { return s.clusterFleetFirst(m, shared), nil })
		snap, _ = v.(*clusterFleetSnapshot)
		if snap == nil {
			http.Error(w, "cluster fleet summary unavailable", http.StatusServiceUnavailable)
			return
		}
	} else if time.Since(snap.GeneratedAt) >= clusterFleetRefreshEvery {
		go m.sf.Do("refresh", func() (any, error) { return s.clusterFleetTryRound(m, shared), nil }) //nolint:errcheck // arka plan tazeleme
	}
	writeJSON(w, snap)
}

// touch — son istek anı; Redis kipinde döngüyü (süreç başına bir kez) başlatır.
func (m *clusterFleetMemo) touch(s *Server) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastHit = time.Now()
	if m.started {
		return
	}
	m.started = true
	go m.loop(s)
}

func (m *clusterFleetMemo) loop(s *Server) {
	tk := time.NewTicker(clusterFleetRefreshEvery)
	defer tk.Stop()
	for range tk.C {
		m.mu.Lock()
		idle := time.Since(m.lastHit) > clusterFleetIdleAfter
		m.mu.Unlock()
		if idle {
			continue
		}
		m.sf.Do("refresh", func() (any, error) { return s.clusterFleetTryRound(m, true), nil }) //nolint:errcheck // fn hata dönmez
	}
}

func (m *clusterFleetMemo) localSnap(digest string) *clusterFleetSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.local == nil || m.local.Digest != digest {
		return nil
	}
	return m.local
}

func (m *clusterFleetMemo) setLocal(snap *clusterFleetSnapshot) {
	m.mu.Lock()
	m.local = snap
	m.mu.Unlock()
}

func (s *Server) clusterFleetLoad(ctx context.Context, digest string) *clusterFleetSnapshot {
	rctx, cancel := context.WithTimeout(ctx, clusterFleetRedisTimeout)
	defer cancel()
	raw, ok, err := s.cache.Get(rctx, clusterFleetCacheKeyPrefix+digest)
	if err != nil || !ok {
		return nil
	}
	var snap clusterFleetSnapshot
	if json.Unmarshal(raw, &snap) != nil || snap.Digest != digest {
		return nil
	}
	return &snap
}

// clusterFleetClaim — bu tur hesabı bu replikaya mı düştü (SetNX, TTL'li).
func (s *Server) clusterFleetClaim(ctx context.Context, digest string) bool {
	rctx, cancel := context.WithTimeout(ctx, clusterFleetRedisTimeout)
	defer cancel()
	ok, err := s.cache.SetNX(rctx, clusterFleetCacheKeyPrefix+digest+":claim", []byte("1"), clusterFleetClaimTTL)
	return err == nil && ok
}

// clusterFleetTryRound — tazeleme turu. Redis kipinde yalnız hakkı kazanan
// hesaplar (kaybeden nil döner; görüntüyü kazanan yazar). Noop kipinde düz
// hesap. İstek ctx'inden bağımsız: istemci kopsa da tur biter.
func (s *Server) clusterFleetTryRound(m *clusterFleetMemo, shared bool) *clusterFleetSnapshot {
	ctx, cancel := context.WithTimeout(context.Background(), clusterFleetRoundTimeout)
	defer cancel()
	cfgs, digest := s.clusterFleetConfigs()
	if shared && !s.clusterFleetClaim(ctx, digest) {
		return nil
	}
	return s.clusterFleetComputeAndPublish(ctx, m, shared, cfgs, digest)
}

// clusterFleetFirst — görüntü yokken: hakkı kazanırsa hesapla; kaybederse
// kazananın Redis'e yazmasını tur tavanı kadar bekle, gelmezse kendin hesapla
// (kazanan replika ölmüş olabilir — sayfa boş kalmasın).
func (s *Server) clusterFleetFirst(m *clusterFleetMemo, shared bool) *clusterFleetSnapshot {
	ctx, cancel := context.WithTimeout(context.Background(), clusterFleetRoundTimeout)
	defer cancel()
	cfgs, digest := s.clusterFleetConfigs()
	if shared && !s.clusterFleetClaim(ctx, digest) {
		tk := time.NewTicker(clusterFleetWaitPoll)
		defer tk.Stop()
		for {
			if snap := s.clusterFleetLoad(ctx, digest); snap != nil {
				return snap
			}
			select {
			case <-ctx.Done():
				ctx2, cancel2 := context.WithTimeout(context.Background(), clusterFleetRoundTimeout)
				defer cancel2()
				return s.clusterFleetComputeAndPublish(ctx2, m, shared, cfgs, digest)
			case <-tk.C:
			}
		}
	}
	return s.clusterFleetComputeAndPublish(ctx, m, shared, cfgs, digest)
}

func (s *Server) clusterFleetComputeAndPublish(ctx context.Context, m *clusterFleetMemo, shared bool, cfgs []thanos.ClusterConfig, digest string) *clusterFleetSnapshot {
	snap := &clusterFleetSnapshot{
		Digest:         digest,
		RefreshSeconds: int(clusterFleetRefreshEvery / time.Second),
		Clusters:       s.clusterFleetCompute(ctx, cfgs),
		GeneratedAt:    time.Now().UTC(),
	}
	if !shared {
		m.setLocal(snap)
		return snap
	}
	if raw, err := json.Marshal(snap); err == nil {
		rctx, cancel := context.WithTimeout(context.Background(), clusterFleetRedisTimeout)
		_ = s.cache.Set(rctx, clusterFleetCacheKeyPrefix+digest, raw, clusterFleetRedisTTL)
		cancel()
	}
	return snap
}

// clusterFleetCompute — cluster başına Summary, en çok clusterFleetParallel
// eşzamanlı; her biri kendi 10 sn tavanında (yavaş cluster ötekileri
// bekletmez, yalnız kendi kaydına hata yazar).
func (s *Server) clusterFleetCompute(ctx context.Context, cfgs []thanos.ClusterConfig) []clusterFleetEntry {
	out := make([]clusterFleetEntry, len(cfgs))
	sem := make(chan struct{}, clusterFleetParallel)
	var wg sync.WaitGroup
	for i, cfg := range cfgs {
		wg.Add(1)
		go func(i int, cfg thanos.ClusterConfig) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			qctx, cancel := context.WithTimeout(ctx, clusterFleetClusterTimeout)
			defer cancel()
			sum, err := s.thanos.Summary(qctx, cfg)
			e := clusterFleetEntry{Name: cfg.Name}
			if err != nil {
				e.Error = err.Error()
			} else {
				e.Summary = &sum
			}
			out[i] = e
		}(i, cfg)
	}
	wg.Wait()
	return out
}
