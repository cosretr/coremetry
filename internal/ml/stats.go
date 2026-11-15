package ml

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// stats — süreç-içi self-obs (Behavior motoruyla aynı kablo). ES süreleri
// son 64 örnekten p95; bellek sayacı yalnız bilgi amaçlı.
type stats struct {
	ticks, skipped, lastUnix, lastMs, lastESMs, esQueries, events, series, lastCHMs atomic.Int64
	supImpact, supRegime, supBatch, vmQueries, partial, docs                        atomic.Int64
	mu                                                                              sync.Mutex
	esSamples                                                                       []int64
	lastKinds                                                                       map[string]chstore.MLKindTick // v0.10.1812: son tik ölçümü
	recordInvalid                                                                   map[string]int64              // v0.10.2199: doğrulayıcıdan kalan kayıt, dedektör başına (kümülatif)
	lastErr                                                                         string
}

const esSampleCap = 64

// observeVM — VM sorgu süresi: aynı p95 örneklemine girer (ortak gecikme
// sayacı) ama ES tik bütçesini (esQueries) TÜKETMEZ (v0.10.1758).
func (s *stats) observeVM(ms int64) {
	s.vmQueries.Add(1)
	s.sample(ms)
}

func (s *stats) observeES(ms int64) {
	s.esQueries.Add(1)
	s.sample(ms)
}

func (s *stats) sample(ms int64) {
	s.lastESMs.Store(ms)
	s.mu.Lock()
	s.esSamples = append(s.esSamples, ms)
	if len(s.esSamples) > esSampleCap {
		s.esSamples = s.esSamples[len(s.esSamples)-esSampleCap:]
	}
	s.mu.Unlock()
}

// observeSuppression — kalite süzgeci bastırma sayaçları (kümülatif).
func (s *stats) observeSuppression(sup suppression) {
	s.supImpact.Add(sup.Impact)
	s.supRegime.Add(sup.Regime)
}

// observeRecordInvalid — v0.10.2199 (anomali kartı H1, ölçüt 11):
// doğrulayıcıdan kalan kayıt sayacı, dedektör başına (kümülatif, süreç-içi).
func (s *stats) observeRecordInvalid(detector string) {
	s.mu.Lock()
	if s.recordInvalid == nil {
		s.recordInvalid = map[string]int64{}
	}
	s.recordInvalid[detector]++
	s.mu.Unlock()
}

// recordInvalidCopy — sayacın kopyası (boşsa nil).
func (s *stats) recordInvalidCopy() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.recordInvalid) == 0 {
		return nil
	}
	out := make(map[string]int64, len(s.recordInvalid))
	for k, v := range s.recordInvalid {
		out[k] = v
	}
	return out
}

// recordInvalidLine — SAF: gölge rapor satırı "doğrulayıcıdan kalan kayıt
// (dedektör başına)", sabit sıra.
func recordInvalidLine(m map[string]int64) string {
	names := make([]string, 0, len(m))
	var total int64
	for k, v := range m {
		names = append(names, k)
		total += v
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s:%d", k, m[k]))
	}
	return fmt.Sprintf("[ml] tick record_invalid total=%d by_detector=%s", total, strings.Join(parts, ","))
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

func (s *stats) setLastKinds(k map[string]chstore.MLKindTick) {
	s.mu.Lock()
	s.lastKinds = k
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
	lastKinds := s.lastKinds
	s.mu.Unlock()
	recInvalid := s.recordInvalidCopy()
	return chstore.MLStats{
		Placement: placement, Enabled: enabled, Leader: leader, TableReady: tableReady,
		Ticks: s.ticks.Load(), Skipped: s.skipped.Load(), LastUnix: s.lastUnix.Load(),
		LastDurationMs: s.lastMs.Load(), LastESMs: s.lastESMs.Load(), ESP95Ms: p95,
		ESQueries: s.esQueries.Load(), Events: s.events.Load(), SeriesInMemory: s.series.Load(), LastCHMs: s.lastCHMs.Load(),
		VMQueries: s.vmQueries.Load(), Partial: s.partial.Load(), LastDocs: s.docs.Load(), SuppressedImpact: s.supImpact.Load(), SuppressedRegime: s.supRegime.Load(), SuppressedBatch: s.supBatch.Load(),
		LastError: lastErr, LastKinds: lastKinds, RecordInvalid: recInvalid,
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
