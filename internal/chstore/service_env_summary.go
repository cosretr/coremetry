package chstore

import (
	"context"
	"sync"
	"time"
)

// service_env_summary.go — v0.10.881 (Dynatrace paritesi #8, dilim 1).
// service_env_summary_5m kapsama probu: MV geriye dolmaz (kurulduğu andan
// itibaren), bu yüzden bir pencereyi MV'den okumadan önce MV'nin ilk kovasının
// pencere başını kapsadığı ÖLÇÜLÜR; kapsamıyorsa okuyucu ham spans yoluna
// (bugünkü davranış) düşer — dürüst geçiş, iki-boot gerekmez. Probe 60 s
// önbellekli; okunamıyor (MV yok / DDL ertelenmiş is_local-kör host) ya da
// boş → false. Bağlantı s.conn (dosya read-pool allowlist'inde değil).

const envSummaryProbeTTL = 60 * time.Second

type envSummaryProbe struct {
	mu  sync.Mutex
	at  time.Time
	min time.Time
	ok  bool
}

// envSummaryCovers — SAF: MV'nin ilk kovası pencere başından önce ya da eşitse
// pencere MV'den okunabilir. ok=false (probe hatası / boş MV) → asla.
func envSummaryCovers(minBucket time.Time, ok bool, from time.Time) bool {
	return ok && !minBucket.IsZero() && !minBucket.After(from)
}

// EnvSummaryCovers — [from, …) service_env_summary_5m'den okunabilir mi.
func (s *Store) EnvSummaryCovers(ctx context.Context, from time.Time) bool {
	if s == nil {
		return false
	}
	p := &s.envSummary
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Since(p.at) > envSummaryProbeTTL {
		var minBucket time.Time
		var n uint64
		// count() ile: boş tabloda min() epoch döner, "kapsıyor" sanılırdı.
		err := s.conn.QueryRow(ctx, "SELECT min(time_bucket), count() FROM service_env_summary_5m SETTINGS max_execution_time = 5").Scan(&minBucket, &n)
		p.min, p.ok, p.at = minBucket, err == nil && n > 0, time.Now()
	}
	return envSummaryCovers(p.min, p.ok, from)
}
