package anomaly

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// heap_source.go — v0.10.891 (paritesi #4 dilim 2): dedektörün heap okuma
// kaynağı. Detector'da VM tutamağı yoktu; kart api'nin metricSource'unu
// kullanıyor. Burada iki backend'in ORTAK üç metodu + VM'nin Configured()
// kapısı yapısal arayüzle alınır (anomaly → vmetrics import'u açılmaz;
// main.go somut *vmetrics.Service ve *chstore.Store'u geçer). Seçim ÇAĞRI
// ANINDA (vmetrics.RuntimePodsOr/pick emsali): ayar değişince restart yok.

// HeapBackend — chstore.Store ve vmetrics.Service aynı imzayı taşır.
type HeapBackend interface {
	MetricExists(ctx context.Context, name string) (bool, error)
	QueryMetric(ctx context.Context, f chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error)
	MetricLabelValues(ctx context.Context, metric, key string, since time.Duration, q string, limit int) ([]string, error)
}

// HeapVM — VM backend'i + yapılandırılmış mı kapısı.
type HeapVM interface {
	HeapBackend
	Configured() bool
}

// HeapSourceOr — kip: auto → VM yapılandırılmışsa VM, değilse CH; "vm"/"ch" zorlar.
type HeapSourceOr struct {
	vm HeapVM
	ch HeapBackend
}

func NewHeapSource(vm HeapVM, ch HeapBackend) *HeapSourceOr {
	return &HeapSourceOr{vm: vm, ch: ch}
}

// heapPicked — seçilmiş backend + adı (HeapSource arayüzünü tamamlar).
type heapPicked struct {
	HeapBackend
	name string
}

func (p heapPicked) Name() string { return p.name }

// pick — SAF karar (tablo testli). vm nil ya da yapılandırılmamışken auto → ch;
// "vm" zorlanmış ama VM yoksa yine ch (dürüst: Name "ch").
func (s *HeapSourceOr) pick(mode string) heapPicked {
	if s == nil {
		return heapPicked{}
	}
	useVM := false
	switch mode {
	case chstore.HeapSourceVM:
		useVM = s.vm != nil
	case chstore.HeapSourceCH:
		useVM = false
	default:
		useVM = s.vm != nil && s.vm.Configured()
	}
	if useVM {
		return heapPicked{HeapBackend: s.vm, name: chstore.HeapSourceVM}
	}
	if s.ch == nil {
		return heapPicked{}
	}
	return heapPicked{HeapBackend: s.ch, name: chstore.HeapSourceCH}
}

// heapWiring — Start'tan SONRA da güvenle takılabilsin diye atomik
// (main.go dedektörü VM servisinden önce kuruyor; rcSynth emsali).
type heapWiring struct {
	source atomic.Pointer[HeapSourceOr]
	cache  atomic.Pointer[cacheBox]
}

type cacheBox struct{ c cache.Cache }

// SetHeapSource — main.go: det.SetHeapSource(anomaly.NewHeapSource(vmSvc, store)).
func (d *Detector) SetHeapSource(s *HeapSourceOr) { d.heapWire.source.Store(s) }

// SetHeapCache — gölge/canlı verdict'lerin kart için Redis'e yazılacağı önbellek
// (Noop kurulumda yazım kaybolur; rozet yalnız lider süreçte görünmez — dürüst).
func (d *Detector) SetHeapCache(c cache.Cache) {
	if c == nil {
		return
	}
	d.heapWire.cache.Store(&cacheBox{c: c})
}
