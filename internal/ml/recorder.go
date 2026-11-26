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
	// Backoff — VM aile backoff'u (vm_budget.go); nil güvenli.
	Backoff *vmBackoff
	// Background — alt sistemin ömrüne bağlı context (tik context'i DEĞİL):
	// backfill gibi tikten uzun işler buna bağlanır (v0.10.1763).
	Background context.Context
	// ESLeft — tik ES bütçesinin kalanı (maxEsQueriesPerTick − bu tikte atılan); nil = sınırsız.
	ESLeft func() int
	// meter — v0.10.1812 tik ölçümü (tick_meter.go); nil güvenli.
	meter *tickMeter
	// openRun — v0.10.2358: taban kimliğin (MLEventID) açık koşusu var mı;
	// nil güvenli (= yok). Günlük tavan (DailyEventCap) YENİ olay sayar.
	openRun func(baseID string) bool
	// runStart — v0.10.2373 (I3): taban kimliğin süren epizodunun yazılmış
	// başlangıcı (unix ns; 0 = yok); nil güvenli.
	runStart func(baseID string) int64
}

// startedAt — v0.10.2373: adayın süren epizodunun yazılmış başlangıcı (0 = yok).
func (d Deps) startedAt(det, svc, part string) int64 {
	if d.runStart == nil {
		return 0
	}
	return d.runStart(chstore.MLEventID(det, svc, part))
}

// extends — v0.10.2358: aday açık bir olayı mı uzatıyor? Uzatma günlük
// tavanı harcamaz: tavan tik başına tüketilince (200 tik ≈ 16 sa / 5 dk)
// koşul sürerken aday düşüyor, koşu 3 tik sonra "bitti" kapanıyordu.
func (d Deps) extends(det, svc, part string) bool {
	return d.openRun != nil && d.openRun(chstore.MLEventID(det, svc, part))
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
	// notifier — v0.10.2052 (D4): grup Problem'inin açılış bildirimi (mevcut hat).
	notifier ProblemNotifier
	// pub — v0.10.2343 (A2): olay id → son yazımın yayın kararı; yalnız A2
	// açıkken dolar (publish_gate_enforce.go). Tik goroutine'i tek kullanıcı.
	pub pubVerdicts
	// dedup — v0.10.2173: paylaşılan önbellek; D5 yükseliş bildiriminin
	// süreçler arası tekilleştirmesi (anomaly.EscalationAlert). nil = yok.
	dedup cache.Cache
	// leaderTicks — art arda lider ZAMANLI tik sayısı (erken tik saymaz,
	// v0.10.2377); grup Problem'i kapanışı yalnız > episodeCloseMisses sonra
	// (yeni liderin boş koşu durumu).
	leaderTicks int
	// v0.10.2377 (I2, impact_probe.go) — ready: son tikin tablo yoklaması
	// (yoklama CH'ye sormaz); probeCool: erken tik açan aday → an (bekleme);
	// lastScheduled: son zamanlı tikin başlangıcı (tik durumunda görünür).
	// Döngü goroutine'i tek kullanıcı.
	ready         bool
	probeCool     map[string]time.Time
	lastScheduled time.Time
	// breadth — v0.10.2086: yaygın yavaşlamanın servis başına ardışık koşul
	// durumu (svc_breadth.go; süreç-içi, lider değişince sıfır).
	breadth map[string]breadthRun
	// breadthH — v0.10.2237 (H12): svc_breadth kaydının sayım geçmişi (süreç-içi).
	breadthH breadthHist
	// v0.10.2359 — veri-yok ayrımı (no_data.go): olay dedektörü → aile ve
	// seri sayan aileler (süreç-içi; tik goroutine'i tek kullanıcı).
	classOf      map[string]string
	countsSeries map[string]bool
}

// WithNotifier — v0.10.2052: grup Problem'i bildirimi için mevcut Problem
// bildirim hattı. nil = bildirim yok (Problem yine açılır).
func (r *Recorder) WithNotifier(n ProblemNotifier) *Recorder {
	r.notifier = n
	return r
}

// WithDedup — v0.10.2173: D5 yükseliş bildirimi için paylaşılan önbellek
// (worker ile sre aynı sahibi aynı anda P1'e çıkarırsa tek bildirim).
func (r *Recorder) WithDedup(c cache.Cache) *Recorder {
	r.dedup = c
	return r
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
	// v0.10.2377 (I2) — zamanlı tikler arasında dakikalık etki yoklaması ve
	// gerekirse erken tam tik (impact_probe.go tickLoop). Zamanlı takvim aynı.
	tickLoop{
		now: time.Now,
		wait: func(d time.Duration) bool {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(d):
				return true
			}
		},
		cfg: func() chstore.MLConfig {
			// v0.10.2074 — op_latency + metric_kafka kayıtlı ayarda bir kez açılır
			// (işaretli göç; yalnız lider, başarıya dek her tur dener).
			if r.leader.IsLeader() {
				chstore.MigrateMLForceOnOnce(ctx, r.store)
			}
			return r.store.GetMLConfig(ctx)
		},
		tick: func(cfg chstore.MLConfig, early bool) { r.tick(ctx, cfg, early) },
		probe: func(cfg chstore.MLConfig, now time.Time) bool {
			return r.probeImpact(ctx, cfg, r.leader.IsLeader(), now)
		},
	}.run()
}

// tick — tam tik. early=true: yoklamanın istediği ERKEN tik (v0.10.2377, I2).
// Erken tik zamanlı tikle AYNI hattı koşar; tek fark TİK sayan durumları
// ilerletmemesidir (fazladan tik "bir tik geçti" anlamına gelmez):
//   - lider tik sayacı artmaz (admit);
//   - koşu kaçırma sayacı işlemez: closeStale atlanır (kapanış, veri-yok
//     tutması ve kapsam kararı zamanlı tikin işidir);
//   - yaygın yavaşlamanın ardışıklık sayacı ilerlemez (breadthHoldEvents);
//   - yazılmayan adayın günlük tavan hakkı geri verilir (refundEarlyCaps).
//
// Koşu izleyicisinin geri kalanı (openRun, fresh, rejim, teyit) duvar saati
// ve AYARLI tik süresiyle çalışır; erken tikten etkilenmez.
func (r *Recorder) tick(ctx context.Context, cfg chstore.MLConfig, early bool) {
	leader := r.leader.IsLeader()
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	ready, err := r.store.MLTableReady(probe)
	cancel()
	setMeta(r.placement, cfg.Enabled, leader, ready)
	if !r.admit(cfg, leader, ready, err, early) {
		return
	}
	start := time.Now()
	if !early {
		r.lastScheduled = start
	}
	budget := time.Duration(cfg.TimeoutMs)*time.Millisecond*time.Duration(1+len(r.detectors)) + 10*time.Second
	tctx, tcancel := context.WithTimeout(ctx, budget)
	defer tcancel()
	// v0.10.1812 — tikin CH sorguları (tracedConn kapısında sayılır)
	var chQueries atomic.Int64
	tctx = chstore.WithQueryCounter(tctx, &chQueries)
	// v0.10.2269 — operatör kararları (anomaly_verdicts / MLLabels) tikte
	// OKUNMAZ: kararlar yalnız karne etiketidir (vote_runtime_guard_test.go).
	steps := map[string]int64{}
	signals := map[string]chstore.MLSignalTick{}
	esAtStart := global.esQueries.Load()
	vmAtStart := global.vmQueries.Load()
	deps := Deps{Store: r.store, Logs: r.logs, VM: r.vm, Background: ctx, Backoff: r.backoff, ObserveES: func(d time.Duration) { global.observeES(d.Milliseconds()) },
		ObserveVM: func(d time.Duration) { global.observeVM(d.Milliseconds()) },
		ESLeft:    func() int { return cfg.MaxESQueriesPerTick - int(global.esQueries.Load()-esAtStart) },
		openRun:   func(id string) bool { return r.runs.openRun(id, start, time.Duration(cfg.TickSec)*time.Second) },
		runStart:  func(id string) int64 { return r.runs.startedOf(id, start, time.Duration(cfg.TickSec)*time.Second) }}
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
	if r.classOf == nil {
		r.classOf, r.countsSeries = map[string]string{}, map[string]bool{}
	}
	cov := tickCoverage{classOf: r.classOf, fam: map[string]famCoverage{}}
	var spends []capSpend // erken tik: tavandan hak harcayan adaylar (refundEarlyCaps)
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
				if deps.openRun(c.ID) { // uzatma tavan harcamadı (extends)
					continue
				}
				rf.refundCap(c.Detector)
			}
		}
		if rf, ok := det.(capRefunder); ok && early {
			for _, ev := range evs {
				if !deps.openRun(ev.ID) { // uzatma tavan harcamadı
					spends = append(spends, capSpend{rf: rf, det: ev.Detector, id: ev.ID})
				}
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
		// v0.10.2359: aile bu tik veri değerlendirebildi mi (no_data.go).
		judged := deps.meter.judgedSeries()
		cov.fam[det.Name()] = famCoverage{blind: familyBlind(derr, deps.meter.wasSkipped(), r.countsSeries[det.Name()], judged)}
		if judged > 0 {
			r.countsSeries[det.Name()] = true
		}
		for _, ev := range evs {
			r.classOf[ev.Detector] = det.Name()
		}
		// v0.10.2063: kaynağı olmayan dedektör "ölçülemedi" (dependency_error_rate).
		if u, ok := det.(interface{ Unmeasured() map[string]string }); ok {
			st.Unmeasured = u.Unmeasured()
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
	deps.meter = nil                       // tik sonrası yazımlar dedektör ölçümüne girmez
	cov.runBlind = runBlindOf(r.detectors) // v0.10.2385 (I4): koşu düzeyi "ölçülemedi"
	// v0.10.2065 (Davis S5): evidence.baselineKind — tek yer, saf takvim hesabı, sorgu yok.
	if cal, loc := calendarFor(sens); len(all) > 0 {
		for i := range all {
			all[i] = withBaselineKind(all[i], cfg.BaselineDays, cal, loc)
		}
	}
	// v0.10.2045 — log ML olayı yalnız katalogdaki servise (log_service_identity.go).
	all, logSvc := resolveLogServices(tctx, deps, all, start, 2*time.Duration(cfg.BucketSec)*time.Second)
	steps["detectorsMs"] = time.Since(start).Milliseconds()
	kept, sup := qualityFilter(all, cfg, r.runs, start)
	refundEarlyCaps(spends, func(id string) bool { return r.runs.seenAt(id, start) })
	// v0.10.2086 — yaygın yavaşlama: bu tikin op_latency olaylarından
	// türetilir (sorgu yok); aynı kalite süzgecinden (epizot / teyit) geçer.
	if cfg.DetectorOn(detSvcBreadth) {
		var bevs []chstore.MLEvent
		if early { // ardışıklık sayacı tik sayar: erken tik ilerletmez
			bevs = breadthHoldEvents(kept, isBatch, r.breadth, start, cfg)
		} else {
			bevs, r.breadth = breadthEvents(kept, isBatch, r.breadth, start, cfg)
		}
		r.breadthH.observe(breadthOpsBySvc(kept, isBatch), start) // v0.10.2237 (H12)
		st := chstore.MLSignalTick{Ran: true, Candidates: len(bevs), Services: distinctServices(bevs)}
		bk, bsup := qualityFilter(bevs, cfg, r.runs, start)
		sup.Impact, sup.Regime = sup.Impact+bsup.Impact, sup.Regime+bsup.Regime
		kept = append(kept, bk...)
		st.Written = len(bk)
		signals[detSvcBreadth] = st
	} else {
		r.breadth = nil
		r.breadthH = breadthHist{}
	}
	w0 := time.Now()
	global.observeSuppression(sup)
	global.supBatch.Add(int64(batchCut))
	// epizot kapanışı: 3 tik görülmeyen koşu stage=closed yazılır (B3)
	// v0.10.2215 (H2) — consumer lag olay kaydı: kapanış satırı ended_at ile.
	lagBudget := lagRecordMaxPerTick
	if cls, ok := r.classOf[detOpLatency]; ok { // yaygın yavaşlama op_latency'den türer
		r.classOf[detSvcBreadth] = cls
	}
	// v0.10.2377 (I2): kaçırma sayacı TİK sayar (episodeCloseMisses tik
	// görülmeyen koşu kapanır). Erken tik fazladan tiktir: kaçırma saymaz,
	// koşu kapatmaz, veri-yok tutması başlatmaz; hepsi zamanlı tikte.
	var closing, held []chstore.MLEvent
	if !early {
		closing, held = r.runs.closeStale(start, cov)
	}
	for _, ev := range held { // v0.10.2359: veri-yok tutması kanıta (kart/liste "açık")
		if werr := r.store.UpsertMLEvent(tctx, ev); werr != nil && firstErr == nil {
			firstErr = werr
		}
	}
	attachLagRecords(tctx, deps, cfg, closing, true, r.runs.firstOf, nil, &lagBudget, start)
	latBudget := latRecordMaxPerTick // v0.10.2219 (H3): op_latency kaydı
	attachLatencyRecords(tctx, deps, cfg, closing, true, r.runs.firstOf, nil, &latBudget, start)
	metBudget := metricRecordMaxPerTick // v0.10.2220 (H4): VM metrik kaydı
	attachMetricRecords(tctx, deps, cfg, closing, true, r.runs.firstOf, nil, &metBudget, start)
	errBudget := errRecordMaxPerTick // v0.10.2221 (H5): hata oranı kaydı
	attachErrorRateRecords(tctx, deps, cfg, closing, true, r.runs.firstOf, nil, &errBudget, start)
	rtBudget := rtRecordMaxPerTick // v0.10.2385 (I4): servis yanıt süresi kaydı
	attachSvcRTRecords(tctx, deps, cfg, closing, true, r.runs.firstOf, nil, &rtBudget, start)
	tpBudget := tpRecordMaxPerTick // v0.10.2222 (H6): throughput_drop kaydı
	attachThroughputRecords(tctx, deps, cfg, closing, true, r.runs.firstOf, nil, &tpBudget, start)
	terrBudget := terrRecordMaxPerTick // v0.10.2223 (H7): trace_error_count kaydı
	attachTraceErrorRecords(tctx, deps, cfg, closing, true, r.runs.firstOf, nil, &terrBudget, start)
	thRanger, thBudget := r.thanosRanger(), thanosRecordMaxPerTick // v0.10.2224 (H8): Thanos metrik kaydı
	attachThanosMetricRecords(tctx, thRanger, cfg, closing, true, r.runs.firstOf, nil, &thBudget, start)
	depBudget := depRecordMaxPerTick // v0.10.2234 (H9): dependency_* kaydı
	attachDependencyRecords(tctx, deps, cfg, closing, true, r.runs.firstOf, nil, &depBudget, start)
	attachChangePointRecords(closing, true, r.runs.firstOf, nil, start) // v0.10.2235 (H10): sorgusuz
	// v0.10.2236 (H11): log RED kaydı (ml_series, ES yok)
	logBudget := logRecordMaxPerTick
	attachLogREDRecords(tctx, deps, cfg, closing, true, r.runs.firstOf, nil, &logBudget, start)
	// v0.10.2253 (H13): şablon (ml_series, ES yok) + oluşum (sorgusuz) kaydı
	tplBudget := tplRecordMaxPerTick
	attachTemplateRecords(tctx, deps, cfg, closing, true, r.runs.firstOf, nil, &tplBudget, start)
	attachOccurrenceRecords(cfg, closing, true, r.runs.firstOf, nil, start)
	podBudget := podRecordMaxPerTick // v0.10.2237 (H12): trace_pod_outlier + svc_breadth kaydı
	attachPodOutlierRecords(tctx, deps, cfg, closing, true, r.runs.firstOf, nil, &podBudget, start)
	attachBreadthRecords(&r.breadthH, cfg, closing, true, r.runs.firstOf, start)
	vol, volBudget, volVM := r.volume(), volRecordMaxPerTick, volVMMaxPerTick // v0.10.2242 (B): hacim kaydı
	vol.attachVolumeRecords(tctx, deps, cfg, closing, true, r.runs.firstOf, nil, &volBudget, &volVM, start)
	gateMode := cfg.PublishGatesMode() // v0.10.2473: gürültü kapısı kipi (ayar; off | shadow | on)
	for _, ev := range closing {
		if _, nd := NoDataOf(ev.Evidence); nd { // v0.10.2359: kayıt üstüne (attach* sonrası)
			ev = stampRecordEnd(ev)
		}
		ev = applyRecordContract(ev, gateMode) // v0.10.2199 H1: kaydı olmayan olay aynen geçer
		r.observePublish(ev, start)
		if werr := r.store.UpsertMLEvent(tctx, ev); werr != nil && firstErr == nil {
			firstErr = werr
		}
	}
	r.runs.gc(start)
	// T1b: trace olaylarına exception / Oracle / BubbleUp referansı (koşu başına bir kez)
	r.enrichTraceContext(tctx, deps, cfg, kept, cfg.MaxESQueriesPerTick-int(global.esQueries.Load()-esAtStart))
	// v0.10.2037 — suçlu seri: yeni açılan (ya da tepesi 1,5 kat büyüyen)
	// metrik olayına TEK kırılımlı anlık sorgu; sonuç kanıta (contrib.go).
	c0 := time.Now()
	contribQ := r.contribSource().enrichContributors(tctx, deps, cfg, kept, r.runs.prevEvent, start)
	steps["contribMs"] = time.Since(c0).Milliseconds()
	// v0.10.2015 (anomali tekilleştirme D3) — grup kararı her tik; karar
	// üyenin kanıtına (groupShadow) yazılır. v0.10.2052 (D4) — canlı kip
	// (ml.groupProblems, varsayılan açık) AYNI kararla Problem açar /
	// tazeler / kapatır (group_live.go); kapalıyken yalnız gölge kaydı.
	// Girdi süreç-içi koşu durumu; canlıda tek ek okuma açık Problem
	// anlık görüntüsü. Kapanış satırı (closeStale) koşunun son halini
	// yazdığı için damgalı hal koşuya da geri verilir.
	// v0.10.2343 / v0.10.2473: kip on iken terfi kapısı son yazımda gürültü
	// kapısında kalan (held) olayı saymaz; etki kuralı olayları muaf
	// (publish_gate_enforce.go). pubWarming: karar haritası bu tik kuruldu →
	// bu tik Problem kapatılmaz.
	pub, pubWarming := r.publishVerdicts(start, gateMode)
	groups, shadow := decideGroups(kept, r.runs.events(), GroupSlack(cfg),
		GroupGateInput{IsBatch: isBatch, LoadParts: loadParts, Open: r.runs.openIDs(kept), Published: pub}, start)
	// v0.10.2089 — servis düzeyi kapı (spec dilim 3): karar kanıta, sayaç
	// tik durumuna. v0.10.2130 (dilim 4, operatör kararı 2026-11-14) — aynı
	// karar canlı yola girer: grup kararlarıyla TEK planda, servis başına tek
	// ML Problem'i (anahtar kapalıyken yalnız gölge kaydı).
	svcDec := decideSvcShadow(publishedCandidates(kept, pub), r.runs.openIDs(kept), groups, chstore.CurrentProblemPriority(), start)
	var live liveCounts
	if cfg.GroupProblemsOn() {
		loc := OperatorLocation(sens)
		desc := func(g decidedGroup) string {
			if g.D.SingleWouldOpen { // v0.10.2092: tek sinyal kolu canlı
				return singleProblemDescription(g, func(e chstore.MLEvent) CardInput { return SummaryInput(e, cfg, start, loc) })
			}
			return groupProblemDescription(g, func(e chstore.MLEvent) string { return ExplainSummary(SummaryInput(e, cfg, start, loc)) })
		}
		lc, lerr := r.liveGroupProblems(tctx, groups, kept, svcDec, r.leaderTicks > episodeCloseMisses && !pubWarming, desc, start)
		live = lc
		if lerr != nil {
			log.Printf("[ml] group problems: %v", lerr)
			if firstErr == nil {
				firstErr = lerr
			}
		}
	}
	stampGroupDecisions(kept, groups)
	stampSvcShadow(kept, svcDec)
	r.markGateLinks(tctx, groups, svcDec) // v0.10.2343: K16 raporu (yalnız bellek + memo'lu anlık görüntü)
	svcOpen, svcP1 := svcShadowCounts(svcDec)
	confirmWin := time.Duration(cfg.Quality.ConfirmWindowMin) * time.Minute
	attachLagRecords(tctx, deps, cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, &lagBudget, start)
	attachLatencyRecords(tctx, deps, cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, &latBudget, start)
	attachMetricRecords(tctx, deps, cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, &metBudget, start)
	attachErrorRateRecords(tctx, deps, cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, &errBudget, start)
	attachSvcRTRecords(tctx, deps, cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, &rtBudget, start)
	attachThroughputRecords(tctx, deps, cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, &tpBudget, start)
	attachTraceErrorRecords(tctx, deps, cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, &terrBudget, start)
	attachThanosMetricRecords(tctx, thRanger, cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, &thBudget, start)
	attachDependencyRecords(tctx, deps, cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, &depBudget, start)
	attachChangePointRecords(kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, start)
	attachLogREDRecords(tctx, deps, cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, &logBudget, start)
	attachTemplateRecords(tctx, deps, cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, &tplBudget, start)
	attachOccurrenceRecords(cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, start)
	attachPodOutlierRecords(tctx, deps, cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, &podBudget, start)
	attachBreadthRecords(&r.breadthH, cfg, kept, false, r.runs.firstOf, start)
	vol.attachVolumeRecords(tctx, deps, cfg, kept, false, r.runs.firstOf, func(ev chstore.MLEvent) []string {
		ids, _ := r.runs.confirmers(ev, start, confirmWin)
		return ids
	}, &volBudget, &volVM, start)
	for _, ev := range kept {
		if ev.Stage != chstore.MLStageChronic {
			r.runs.setEvent(ev)
		}
	}
	for _, ev := range kept {
		ev = applyRecordContract(ev, gateMode) // v0.10.2199 H1: kaydı olmayan olay aynen geçer
		r.observePublish(ev, start)
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
	global.events.Add(written)
	global.setError(firstErr)
	global.tickDone(start, early)
	// tik durumu: api pod'u karnede okur (süreç-içi sayaç bu pod'da kalır)
	status := chstore.MLTickStatus{At: start.Unix(), DurationMs: time.Since(start).Milliseconds(), Events: written, Backoff: r.backoff.snapshot(start),
		Partial: int64(partialN), Docs: global.docs.Load(), Steps: steps, Signals: signals, BatchCut: int64(batchCut),
		Kinds: foldKindTicks(samples), ES: global.esQueries.Load() - esAtStart, VM: global.vmQueries.Load() - vmAtStart, CH: chQueries.Load(),
		ShadowGroups: shadow.Groups, ShadowWouldOpen: shadow.WouldOpen, ShadowSingleWouldOpen: shadow.SingleWouldOpen, ContribQueries: int64(contribQ),
		GroupLive: cfg.GroupProblemsOn(), GroupOpened: live.Opened, GroupRefreshed: live.Refreshed, GroupResolved: live.Resolved, GroupSingleOpened: live.SingleOpened, SvcOpened: live.SvcOpened,
		LogSvcUnmatched: int64(logSvc.Unmatched), LogSvcViaPod: int64(logSvc.ViaPod), ShadowSvcWouldOpen: svcOpen, ShadowSvcP1: svcP1,
		// v0.10.2377 (I2): bu tik erken mi, son ZAMANLI tik ne zamandı, kaç erken tik / yoklama koştu
		Early: early, ScheduledAt: unixOrZero(r.lastScheduled), EarlyTicks: global.earlyTicks.Load(), Probes: global.probes.Load()}
	// v0.10.2241 (A1) — yayın kapısı gölge raporu: /api/ml/scorecard tick.publishGates
	status.PublishGates, status.PublishGatesWindowH = global.gateShadowReport(start), int(gateShadowWindow/time.Hour)
	status.PublishGatesMode, status.PublishGatesEnforced, status.PublishGatesCriterion = gateMode, gatesEnforced(gateMode), PublishGatesCriterion
	// v0.10.2019 — metrik aile kaynakları + Thanos ölçümü (MetricThanos)
	for _, det := range r.detectors {
		if ts, ok := det.(interface {
			tickStatus() (map[string]chstore.MLMetricSource, *chstore.MLThanosTick)
		}); ok {
			status.MetricSources, status.Thanos = ts.tickStatus()
		}
	}
	global.setLastKinds(status.Kinds)
	if firstErr != nil {
		status.LastError = truncRunes(firstErr.Error(), 300)
	}
	sctx, scancel := context.WithTimeout(ctx, 5*time.Second)
	if serr := r.store.SaveMLTickStatus(sctx, status); serr != nil {
		log.Printf("[ml] tik durumu yazılamadı: %v", serr)
	}
	scancel()
	log.Printf("[ml] tick placement=%s early=%v detectors=%d events=%d suppressed=impact:%d,regime:%d,batch:%d dur=%dms es_p95=%dms err=%v",
		r.placement, early, active, written, sup.Impact, sup.Regime, batchCut, time.Since(start).Milliseconds(), Observability().ESP95Ms, firstErr)
	log.Printf("[ml] tick group_problems live=%v opened=%d refreshed=%d resolved=%d", cfg.GroupProblemsOn(), live.Opened, live.Refreshed, live.Resolved)
	log.Printf("[ml] tick group_shadow groups=%d would_open=%d single_would_open=%d svc_would_open=%d svc_p1=%d contrib_queries=%d log_svc_unmatched=%d log_svc_via_pod=%d", shadow.Groups, shadow.WouldOpen, shadow.SingleWouldOpen, svcOpen, svcP1, contribQ, logSvc.Unmatched, logSvc.ViaPod)
	log.Printf("[ml] tick steps detectors=%dms write=%dms snapshot=%dms es_queries=%d vm_queries=%d partial=%d docs=%d",
		steps["detectorsMs"], steps["writeMs"], steps["snapshotMs"], global.esQueries.Load()-esAtStart, global.vmQueries.Load()-vmAtStart, partialN, global.docs.Load())
	for _, line := range kindTickLines(status.Kinds) {
		log.Print(line)
	}
	log.Print(recordInvalidLine(global.recordInvalidCopy()))
	log.Print(gateShadowLine(status.PublishGates, gateMode))
	for name, st := range signals {
		log.Printf("[ml] tick %s ran=%v services=%d source=%dms candidates=%d batch_cut=%d written=%d partial=%d err=%s", name, st.Ran, st.Services, st.SourceMs, st.Candidates, st.BatchCut, st.Written, st.Partial, st.Error)
	}
}

// runBlindOf — dedektörlerin koşu düzeyi "ölçülemedi" kancaları tek yüklemde
// (no_data.go tickCoverage.runBlind); kancası olan dedektör yoksa nil.
func runBlindOf(dets []Detector) func(chstore.MLEvent) bool {
	var fs []func(chstore.MLEvent) bool
	for _, det := range dets {
		if b, ok := det.(interface{ runBlind(chstore.MLEvent) bool }); ok {
			fs = append(fs, b.runBlind)
		}
	}
	if len(fs) == 0 {
		return nil
	}
	return func(ev chstore.MLEvent) bool {
		for _, f := range fs {
			if f(ev) {
				return true
			}
		}
		return false
	}
}

// unixOrZero — SAF: sıfır an → 0 (JSON'da omitempty).
func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// volume — v0.10.2242 (B): kayıtlı hacim dedektörü (yoksa nil; attach nil güvenli).
func (r *Recorder) volume() *Volume {
	for _, d := range r.detectors {
		if v, ok := d.(*Volume); ok {
			return v
		}
	}
	return nil
}

// contribSource — kırılım sorgusunun kaynakları: VM deps'ten; Thanos
// MetricThanos dedektöründen (querier + backoff paylaşılır).
func (r *Recorder) contribSource() contribSource {
	for _, d := range r.detectors {
		if mt, ok := d.(*MetricThanos); ok && mt.q != nil {
			return contribSource{thanos: mt.q, backoff: mt.backoff}
		}
	}
	return contribSource{}
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
	case strings.HasPrefix(name, "trace"), name == "dependency", name == detTelemetryVolume:
		return chstore.MLSignalTrace
	case strings.HasPrefix(name, "metric"):
		return chstore.MLSignalMetric
	}
	_ = dets
	return ""
}
