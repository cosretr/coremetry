package ml

import (
	"context"
	"log"
	"time"

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
	// ObserveES — her ES isteğinin süresi (self-obs p95).
	ObserveES func(d time.Duration)
}

// Recorder — alt sistemin döngüsü. Kendi goroutine'i, kendi context +
// timeout'u; aynı süreçteki LLM drain'i ya da evaluator ile paylaşılan
// hiçbir şey yok.
type Recorder struct {
	store     *chstore.Store
	logs      logstore.Store
	leader    *cache.LeaderHolder
	placement string
	detectors []Detector
}

// New — placement etiketi /admin/stats için ("sre" | "worker").
func New(store *chstore.Store, logs logstore.Store, lock cache.Lock, placement string, detectors ...Detector) *Recorder {
	if lock == nil {
		_, lock = cache.NewNoop()
	}
	return &Recorder{store: store, logs: logs, placement: placement, detectors: detectors,
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
	deps := Deps{Store: r.store, Logs: r.logs, ObserveES: func(d time.Duration) { global.observeES(d.Milliseconds()) }}
	var firstErr error
	var written int64
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
			if werr := r.store.UpsertMLEvent(tctx, ev); werr != nil {
				if firstErr == nil {
					firstErr = werr
				}
				continue
			}
			written++
		}
	}
	global.events.Add(written)
	global.setError(firstErr)
	global.tickDone(start)
	log.Printf("[ml] tick placement=%s detectors=%d events=%d dur=%dms es_p95=%dms err=%v",
		r.placement, active, written, time.Since(start).Milliseconds(), Observability().ESP95Ms, firstErr)
}
