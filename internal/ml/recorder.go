package ml

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync/atomic"
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
	// meter — v0.10.1812 tik ölçümü (tick_meter.go); nil güvenli.
	meter *tickMeter
}

// observeDropped — ES servis terms tavanı dışında kalan servis (tik ölçümü).
func (d Deps) observeDropped(n int) { d.meter.observeDropped(n) }

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
	// bjob — v0.10.1814 günlük baseline işi (baseline_job.go); aynı lider kilidi.
	bjob baselineJob
	// bf — v0.10.1815 baseline geçmiş doldurma (baseline_backfill.go).
	bf backfillJob
}

// New — placement etiketi /admin/stats için ("sre" | "worker").
func New(store *chstore.Store, logs logstore.Store, vm anomaly.REDVM, lock cache.Lock, placement string, detectors ...Detector) *Recorder {
	if lock == nil {
		_, lock = cache.NewNoop()
	}
	return &Recorder{store: store, logs: logs, vm: vm, placement: placement, detectors: detectors, runs: newTracker(), backoff: newVMBackoff(),
		bf:     backfillJob{own: newVMBackoff()},
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
	// v0.10.1811 — batch kalıpları anomaly_sensitivity atomic'inden; sre
	// pod'unda API sunucusu da hidrate ediyor ama sıralamaya güvenmeden ilk
	// tikten önce bir kez burada (anomaly recorder emsali).
	r.store.LoadAnomalySensitivity(ctx)
	go r.baselineLoop(ctx) // v0.10.1814: günlük baseline (kapalı gelir; lider + anahtar kapısı içeride)
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
	// v0.10.1812 — tikin CH sorguları (tracedConn kapısında sayılır)
	var chQueries atomic.Int64
	tctx = chstore.WithQueryCounter(tctx, &chQueries)
	// ortak etiket seti: 30 g kararlar (hijyen + geri bildirim); hata = etiketsiz devam
	labels, lerr := r.store.MLLabels(tctx, start.Add(-30*24*time.Hour))
	if lerr != nil {
		log.Printf("[ml] labels read failed: %v (etiketsiz devam)", lerr)
	}
	lb := NewLabels(labels)
	steps := map[string]int64{"labelsMs": time.Since(start).Milliseconds()}
	signals := map[string]chstore.MLSignalTick{}
	esAtStart := global.esQueries.Load()
	vmAtStart := global.vmQueries.Load()
	deps := Deps{Store: r.store, Logs: r.logs, VM: r.vm, Labels: lb, Background: ctx, Backoff: r.backoff, ObserveES: func(d time.Duration) { global.observeES(d.Milliseconds()) },
		ObserveVM: func(d time.Duration) { global.observeVM(d.Milliseconds()) },
		ESLeft:    func() int { return cfg.MaxESQueriesPerTick - int(global.esQueries.Load()-esAtStart) }}
	var firstErr error
	var written int64
	var all []chstore.MLEvent
	active, partialN := 0, 0
	// v0.10.1811 — batch kuralı (batch.go): tek yüklem, tik başına bir kez;
	// doğrulanmamış ayarda kalıp listesi boş = kural kapalı.
	sens := r.store.AnomalySensitivityForDetectors()
	isBatch, loadParts := sens.IsBatchService, metricLoadParts(cfg)
	batchCut := 0
	var samples []kindSample
	for _, det := range r.detectors {
		if !det.Enabled(cfg) {
			continue
		}
		active++
		d0 := time.Now()
		es0, vm0, ch0 := global.esQueries.Load(), global.vmQueries.Load(), chQueries.Load()
		deps.meter = newTickMeter()
		evs, derr := det.Tick(tctx, deps, cfg, start)
		st := chstore.MLSignalTick{Ran: true, SourceMs: time.Since(d0).Milliseconds(), Candidates: len(evs), Services: distinctServices(evs)}
		samples = append(samples, sampleOf(signalOfDetector(r.detectors, det.Name()), deps.meter, evs,
			int(global.esQueries.Load()-es0), int(global.vmQueries.Load()-vm0), int(chQueries.Load()-ch0), st.SourceMs))
		evs, cut := batchFilter(evs, isBatch, loadParts)
		if rf, ok := det.(capRefunder); ok {
			for _, c := range cut {
				rf.refundCap(c.Detector)
			}
		}
		st.BatchCut = len(cut)
		batchCut += st.BatchCut
		if errors.Is(derr, logstore.ErrESPartial) {
			global.partial.Add(1)
			partialN++
			st.Partial = 1
		}
		if derr != nil {
			st.Error = truncRunes(derr.Error(), 200)
		}
		signals[det.Name()] = st
		if derr != nil && firstErr == nil {
			firstErr = derr
		}
		for _, ev := range evs {
			ev.Signal = chstore.MLDetectorSignal(ev.Detector)
			all = append(all, ev)
		}
	}
	deps.meter = nil // tik sonrası yazımlar dedektör ölçümüne girmez
	steps["detectorsMs"] = time.Since(start).Milliseconds() - steps["labelsMs"]
	kept, sup := qualityFilter(all, cfg, lb, r.runs, start)
	w0 := time.Now()
	global.observeSuppression(sup)
	global.supBatch.Add(int64(batchCut))
	// epizot kapanışı: 3 tik görülmeyen koşu stage=closed yazılır (B3)
	for _, ev := range r.runs.closeStale(start) {
		if werr := r.store.UpsertMLEvent(tctx, ev); werr != nil && firstErr == nil {
			firstErr = werr
		}
	}
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
	steps["writeMs"] = time.Since(w0).Milliseconds()
	for _, ev := range kept {
		for name, st := range signals {
			if signalOfDetector(r.detectors, name) == ev.Signal {
				st.Written++
				signals[name] = st
			}
		}
	}
	s0 := time.Now()
	// replay kayıtları: ES bütçesinin kalanı log örneğine
	esLeft := cfg.MaxESQueriesPerTick - int(global.esQueries.Load()-esAtStart)
	if _, serr := r.writeSnapshots(tctx, deps, cfg, kept, start, esLeft); serr != nil && firstErr == nil {
		firstErr = serr
	}
	steps["snapshotMs"] = time.Since(s0).Milliseconds()
	// terfi (K4): yalnız terfi anahtarı açık dedektör varsa okuma yapar
	if perr := r.promote(tctx, cfg, kept); perr != nil && firstErr == nil {
		firstErr = perr
	}
	global.events.Add(written)
	global.setError(firstErr)
	global.tickDone(start)
	// tik durumu: api pod'u karnede okur (süreç-içi sayaç bu pod'da kalır)
	status := chstore.MLTickStatus{At: start.Unix(), DurationMs: time.Since(start).Milliseconds(), Events: written, Backoff: r.backoff.snapshot(start),
		Partial: int64(partialN), Docs: global.docs.Load(), Steps: steps, Signals: signals, BatchCut: int64(batchCut),
		Kinds: foldKindTicks(samples), ES: global.esQueries.Load() - esAtStart, VM: global.vmQueries.Load() - vmAtStart, CH: chQueries.Load()}
	global.setLastKinds(status.Kinds)
	if firstErr != nil {
		status.LastError = truncRunes(firstErr.Error(), 300)
	}
	sctx, scancel := context.WithTimeout(ctx, 5*time.Second)
	if serr := r.store.SaveMLTickStatus(sctx, status); serr != nil {
		log.Printf("[ml] tik durumu yazılamadı: %v", serr)
	}
	scancel()
	log.Printf("[ml] tick placement=%s detectors=%d events=%d suppressed=impact:%d,feedback:%d,regime:%d,batch:%d labels=%d dur=%dms es_p95=%dms err=%v",
		r.placement, active, written, sup.Impact, sup.Feedback, sup.Regime, batchCut, len(labels), time.Since(start).Milliseconds(), Observability().ESP95Ms, firstErr)
	log.Printf("[ml] tick steps labels=%dms detectors=%dms write=%dms snapshot=%dms es_queries=%d vm_queries=%d partial=%d docs=%d",
		steps["labelsMs"], steps["detectorsMs"], steps["writeMs"], steps["snapshotMs"], global.esQueries.Load()-esAtStart, global.vmQueries.Load()-vmAtStart, partialN, global.docs.Load())
	for _, line := range kindTickLines(status.Kinds) {
		log.Print(line)
	}
	for name, st := range signals {
		log.Printf("[ml] tick %s ran=%v services=%d source=%dms candidates=%d batch_cut=%d written=%d partial=%d err=%s", name, st.Ran, st.Services, st.SourceMs, st.Candidates, st.BatchCut, st.Written, st.Partial, st.Error)
	}
}

// distinctServices — SAF.
func distinctServices(evs []chstore.MLEvent) int {
	seen := map[string]bool{}
	for _, e := range evs {
		seen[e.Service] = true
	}
	return len(seen)
}

// signalOfDetector — SAF: dedektör adı → sinyali (ilk olayın sinyali yoksa adından).
func signalOfDetector(dets []Detector, name string) string {
	switch {
	case strings.HasPrefix(name, "log"):
		return chstore.MLSignalLog
	case strings.HasPrefix(name, "trace"), name == "dependency":
		return chstore.MLSignalTrace
	case strings.HasPrefix(name, "metric"):
		return chstore.MLSignalMetric
	}
	_ = dets
	return ""
}
