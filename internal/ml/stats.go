package ml

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// stats — süreç-içi self-obs (Behavior motoruyla aynı kablo). ES süreleri
// son 64 örnekten p95; bellek sayacı yalnız bilgi amaçlı.
type stats struct {
	ticks, skipped, lastUnix, lastMs, lastESMs, esQueries, events, series atomic.Int64
	mu                                                                    sync.Mutex
	esSamples                                                             []int64
	lastErr                                                               string
}

const esSampleCap = 64

func (s *stats) observeES(ms int64) {
	s.esQueries.Add(1)
	s.lastESMs.Store(ms)
	s.mu.Lock()
	s.esSamples = append(s.esSamples, ms)
	if len(s.esSamples) > esSampleCap {
		s.esSamples = s.esSamples[len(s.esSamples)-esSampleCap:]
	}
	s.mu.Unlock()
}

func (s *stats) setError(err error) {
	s.mu.Lock()
	if err != nil {
		s.lastErr = err.Error()
	} else {
		s.lastErr = ""
	}
	s.mu.Unlock()
}

func (s *stats) tickDone(start time.Time) {
	s.ticks.Add(1)
	s.lastUnix.Store(start.Unix())
	s.lastMs.Store(time.Since(start).Milliseconds())
}

// snapshot — /admin/stats görüntüsü. p95 = sıralı örneklerin 0.95 indeksi.
func (s *stats) snapshot(placement string, enabled, leader, tableReady bool) chstore.MLStats {
	s.mu.Lock()
	var p95 int64
	if n := len(s.esSamples); n > 0 {
		cp := append([]int64(nil), s.esSamples...)
		sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
		idx := int(float64(n-1) * 0.95)
		p95 = cp[idx]
	}
	lastErr := s.lastErr
	s.mu.Unlock()
	return chstore.MLStats{
		Placement: placement, Enabled: enabled, Leader: leader, TableReady: tableReady,
		Ticks: s.ticks.Load(), Skipped: s.skipped.Load(), LastUnix: s.lastUnix.Load(),
		LastDurationMs: s.lastMs.Load(), LastESMs: s.lastESMs.Load(), ESP95Ms: p95,
		ESQueries: s.esQueries.Load(), Events: s.events.Load(), SeriesInMemory: s.series.Load(),
		LastError: lastErr,
	}
}

// global — tek alt sistem / süreç; Observability() api'ye okutur.
var (
	global     = &stats{}
	globalMeta struct {
		mu                          sync.Mutex
		placement                   string
		enabled, leader, tableReady bool
	}
)

func setMeta(placement string, enabled, leader, tableReady bool) {
	globalMeta.mu.Lock()
	globalMeta.placement, globalMeta.enabled, globalMeta.leader, globalMeta.tableReady = placement, enabled, leader, tableReady
	globalMeta.mu.Unlock()
}

// Observability — /admin/stats `ml` bloğu. Alt sistem bu pod'da
// koşmuyorsa placement "off" ve sayaçlar sıfır (doğru cevap).
func Observability() chstore.MLStats {
	globalMeta.mu.Lock()
	p, e, l, t := globalMeta.placement, globalMeta.enabled, globalMeta.leader, globalMeta.tableReady
	globalMeta.mu.Unlock()
	if p == "" {
		p = "off"
	}
	return global.snapshot(p, e, l, t)
}
