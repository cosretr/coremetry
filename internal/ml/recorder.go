package ml

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// lockKey — kendi anahtarı: sre pod'u lider yarışlarına girmez (v0.10.1282),
// bu alt sistem kilidi kendisi alır; sre replikası >1 olsa da tek yazıcı.
const lockKey = "coremetry:lock:ml"

// Detector — dilim 1+ dedektör sözleşmesi. Tick, pencere sonu `now` için
// olayları döner; ES/CH okumalarını Deps üzerinden yapar ve ctx bütçesine
// uyar. Dilim 0'da kayıtlı dedektör YOK.
type Detector interface {
	Name() string
	// Enabled — bileşik dedektörler birden çok anahtarı temsil eder (log RED:
	// log_volume + error_rate + service_silent); tik yalnız açık olanı çağırır.
	Enabled(cfg chstore.MLConfig) bool
	Tick(ctx context.Context, d Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error)
}

// Deps — dedektörlerin gördüğü bağımlılıklar.
type Deps struct {
	Store *chstore.Store
	Logs  logstore.Store
	// VM — trace sinyali kaynağı (tail sampling: rollup yerine VM RED); nil = yok.
	VM anomaly.REDVM
	// ObserveES — her ES isteğinin süresi (self-obs p95) + ES tik bütçesi.
	ObserveES func(d time.Duration)
	// ObserveVM — VM sorgu süresi; ES bütçesini tüketmez.
	ObserveVM func(d time.Duration)
	// Labels — ortak etiket seti (taban hijyeni, geri bildirim); nil güvenli.
	Labels *Labels
	// Backoff — VM aile backoff'u (vm_budget.go); nil güvenli.
	Backoff *vmBackoff
	// Background — alt sistemin ömrüne bağlı context (tik context'i DEĞİL):
	// backfill gibi tikten uzun işler buna bağlanır (v0.10.1763).
	Background context.Context
	// ESLeft — tik ES bütçesinin kalanı (maxEsQueriesPerTick − bu tikte atılan); nil = sınırsız.
	ESLeft func() int
}

// Recorder — alt sistemin döngüsü. Kendi goroutine'i, kendi context +
// timeout'u; aynı süreçteki LLM drain'i ya da evaluator ile paylaşılan
// hiçbir şey yok.
type Recorder struct {
	store     *chstore.Store
	logs      logstore.Store
	vm        anomaly.REDVM
	leader    *cache.LeaderHolder
	placement string
	detectors []Detector
	runs      *tracker
	backoff   *vmBackoff
}

// New — placement etiketi /admin/stats için ("sre" | "worker").
func New(store *chstore.Store, logs logstore.Store, vm anomaly.REDVM, lock cache.Lock, placement string, detectors ...Detector) *Recorder {
	if lock == nil {
		_, lock = cache.NewNoop()
	}
	return &Recorder{store: store, logs: logs, vm: vm, placement: placement, detectors: detectors, runs: newTracker(), backoff: newVMBackoff(),
		leader: cache.NewLeaderHolder(lock, lockKey, 3*time.Minute)}
}

// Start — arka planda koşar; ctx iptaliyle döner. Tik aralığı ayardan
// (tickSec) her turda yeniden okunur; tablo yoksa bekler, çökmez.
func (r *Recorder) Start(ctx context.Context) {
	go r.run(ctx)
}

func (r *Recorder) run(ctx context.Context) {
	r.leader.Start(ctx)
	setMeta(r.placement, false, false, false)
	time.Sleep(15 * time.Second) // boot sırasında CH/ES hazır olsun
	for {
		cfg := r.store.GetMLConfig(ctx)
		r.tick(ctx, cfg)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(cfg.TickSec) * time.Second):
		}
	}
}

func (r *Recorder) tick(ctx context.Context, cfg chstore.MLConfig) {
	leader := r.leader.IsLeader()
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	ready, err := r.store.MLTableReady(probe)
	cancel()
	setMeta(r.placement, cfg.Enabled, leader, ready)
	if !cfg.Enabled { // ana anahtar kapalı: ES'e HİÇ sorgu yok
		global.skipped.Add(1)
		return
	}
	if err != nil || !ready {
		global.skipped.Add(1)
		if err != nil {
			global.setError(err)
			log.Printf("[ml] table probe failed: %v (tik atlandı)", err)
		} else {
			log.Printf("[ml] ml_events tablosu yok — api/worker boot'u DDL'i indirene dek bekleniyor")
		}
		return
	}
	if !leader {
		global.skipped.Add(1)
		return
	}
	start := time.Now()
	budget := time.Duration(cfg.TimeoutMs)*time.Millisecond*time.Duration(1+len(r.detectors)) + 10*time.Second
	tctx, tcancel := context.WithTimeout(ctx, budget)
	defer tcancel()
	// ortak etiket seti: 30 g kararlar (hijyen + geri bildirim); hata = etiketsiz devam
	labels, lerr := r.store.MLLabels(tctx, start.Add(-30*24*time.Hour))
	if lerr != nil {
		log.Printf("[ml] labels read failed: %v (etiketsiz devam)", lerr)
	}
	lb := NewLabels(labels)
	esAtStart := global.esQueries.Load()
	deps := Deps{Store: r.store, Logs: r.logs, VM: r.vm, Labels: lb, Background: ctx, Backoff: r.backoff, ObserveES: func(d time.Duration) { global.observeES(d.Milliseconds()) },
		ObserveVM: func(d time.Duration) { global.observeVM(d.Milliseconds()) },
		ESLeft:    func() int { return cfg.MaxESQueriesPerTick - int(global.esQueries.Load()-esAtStart) }}
	var firstErr error
	var written int64
	var all []chstore.MLEvent
	active := 0
	for _, det := range r.detectors {
		if !det.Enabled(cfg) {
			continue
		}
		active++
		evs, derr := det.Tick(tctx, deps, cfg, start)
		if derr != nil && firstErr == nil {
			firstErr = derr
		}
		for _, ev := range evs {
			ev.Signal = chstore.MLDetectorSignal(ev.Detector)
			all = append(all, ev)
		}
	}
	kept, sup := qualityFilter(all, cfg, lb, r.runs, start)
	global.observeSuppression(sup)
	r.runs.gc(start)
	// T1b: trace olaylarına exception / Oracle / BubbleUp referansı (koşu başına bir kez)
	r.enrichTraceContext(tctx, deps, cfg, kept, cfg.MaxESQueriesPerTick-int(global.esQueries.Load()-esAtStart))
	for _, ev := range kept {
		if werr := r.store.UpsertMLEvent(tctx, ev); werr != nil {
			if firstErr == nil {
				firstErr = werr
			}
			continue
		}
		written++
	}
	// replay kayıtları: ES bütçesinin kalanı log örneğine
	esLeft := cfg.MaxESQueriesPerTick - int(global.esQueries.Load()-esAtStart)
	if _, serr := r.writeSnapshots(tctx, deps, cfg, kept, start, esLeft); serr != nil && firstErr == nil {
		firstErr = serr
	}
	// terfi (K4): yalnız terfi anahtarı açık dedektör varsa okuma yapar
	if perr := r.promote(tctx, cfg, kept); perr != nil && firstErr == nil {
		firstErr = perr
	}
	global.events.Add(written)
	global.setError(firstErr)
	global.tickDone(start)
	// tik durumu: api pod'u karnede okur (süreç-içi sayaç bu pod'da kalır)
	status := chstore.MLTickStatus{At: start.Unix(), DurationMs: time.Since(start).Milliseconds(), Events: written, Backoff: r.backoff.snapshot(start)}
	if firstErr != nil {
		status.LastError = truncRunes(firstErr.Error(), 300)
	}
	sctx, scancel := context.WithTimeout(ctx, 5*time.Second)
	if serr := r.store.SaveMLTickStatus(sctx, status); serr != nil {
		log.Printf("[ml] tik durumu yazılamadı: %v", serr)
	}
	scancel()
	log.Printf("[ml] tick placement=%s detectors=%d events=%d suppressed=impact:%d,feedback:%d,regime:%d labels=%d dur=%dms es_p95=%dms err=%v",
		r.placement, active, written, sup.Impact, sup.Feedback, sup.Regime, len(labels), time.Since(start).Milliseconds(), Observability().ESP95Ms, firstErr)
}
