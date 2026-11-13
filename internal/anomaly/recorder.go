package anomaly

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// Recorder is the persistence side of the anomaly system. The
// existing detectors (log_patterns.go, trace_ops.go) are pure
// readers that compute "what looks anomalous in the last 5
// minutes". This recorder runs them on a tick and upserts an
// AnomalyEvent row per detection so the operator can later
// answer "what fired in the last hour, even if it cleared".
//
// Events go through ReplacingMergeTree keyed on the
// (kind, pattern, service) fingerprint — same pattern firing
// across two consecutive ticks updates the same row, advancing
// last_seen and tracking peak_ratio. "Cleared" status is
// derived in the query layer from last_seen freshness, so we
// don't need a separate sweep job.
type Recorder struct {
	store *chstore.Store
	logs  logstore.Store // v0.5.241 — drives DetectLogPatterns so the log-anomaly recorder
	// works against whichever backend is wired (CH or ES).
	interval time.Duration
	window   time.Duration
	lock     cache.Lock          // for multi-replica deployments
	leader   *cache.LeaderHolder // v0.5.426 — true leader designation
}

const recorderLockKey = "coremetry:lock:anomaly-recorder"

// NewRecorder builds a recorder that ticks every `interval` and
// each tick scans `window` of recent data. Default 60s tick is
// fine for the human-grade "anomalies in the last hour" UX —
// faster ticks just multiply CH load with no operator benefit.
func NewRecorder(store *chstore.Store, logs logstore.Store, interval, window time.Duration, lock cache.Lock) *Recorder {
	if interval == 0 {
		interval = 60 * time.Second
	}
	if window == 0 {
		window = 5 * time.Minute
	}
	return &Recorder{
		store: store, logs: logs,
		interval: interval, window: window,
		lock:   lock,
		leader: cache.NewLeaderHolder(lock, recorderLockKey, 3*interval),
	}
}

// Start kicks the recorder into a goroutine. Caller cancels via
// the supplied context. Multi-replica deployments use the lock
// to elect a single writer per tick — the lock TTL is generous
// (the interval × 2) so a slow CH doesn't kill liveness.
func (r *Recorder) Start(ctx context.Context) {
	r.leader.Start(ctx)
	go func() {
		// First tick after a short stagger so the system has
		// time to ingest some logs / spans before the first
		// detection runs against an empty window.
		time.Sleep(15 * time.Second)
		// v0.10.1039 — trace_op'un batch kalıpları anomaly_sensitivity
		// atomic'inden okunuyor (tik başına CH okuması YOK). API sunucusu da
		// hidrate ediyor ama sıralamaya güvenmeden ilk tikten önce bir kez
		// burada: operatörün listesi ilk tikte de geçerli olsun.
		r.store.LoadAnomalySensitivity(ctx)
		r.tick(ctx)

		t := time.NewTicker(r.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.tick(ctx)
			}
		}
	}()
}

func (r *Recorder) tick(ctx context.Context) {
	// v0.5.426 — true leader designation via LeaderHolder; only
	// the leader pod does work, non-leaders skip cleanly.
	// Multi-replica installs still see Upsert idempotency under
	// ReplacingMergeTree(version) as a safety net.
	if !r.leader.IsLeader() {
		return
	}

	now := time.Now()

	// ── Log-pattern anomalies ─────────────────────────────────
	// v0.8.3 — bound JUST the ES-backed pattern detection with its own
	// deadline (the rest of the tick is CH work that self-caps). Stops a
	// slow significant_text/_msearch on the worker pod hanging against
	// the process-lifetime ctx and double-loading the ES cluster.
	if r.store.NativeDetectorOn(ctx, chstore.NativeLogPattern) { // v0.10.1746 — Settings anahtarı (varsayılan kapalı)
		logCtx, logCancel := context.WithTimeout(ctx, 30*time.Second)
		// v0.10.1586 (Davis paritesi FAZ 0.5 / C) — olay yazan yol sürdürmeli:
		// desen ardışık logPatternDwellBuckets (vars. 2) pencerede eşiği geçmeli;
		// olayı aktif desenler muaf (tek okuma, yalnız dwell ≥ 2).
		lpDwell := r.store.AnomalySensitivityForDetectors().LogPatternDwell()
		logHits, err := DetectLogPatternsSustained(logCtx, r.logs, r.window, lpDwell, logPatternActiveSet(ctx, r.store, lpDwell))
		logCancel()
		if err != nil {
			log.Printf("[anomaly-recorder] log patterns: %v", err)
		}
		for _, a := range logHits {
			ev := logPatternEvent(a, now)
			if err := r.store.UpsertAnomalyEvent(ctx, ev); err != nil {
				log.Printf("[anomaly-recorder] upsert log-pattern %s: %v", a.Pattern, err)
			}
		}

		// v0.10.2085 — eski "yeni log şablonu" (log_template_new) adımı silindi;
		// aynı karar ML new_template dedektöründe (ml/new_template.go,
		// anomaly.NewLogTemplateFamilies).
	}

	// ── Trace-op anomalies ────────────────────────────────────
	// v0.10.1586 — sürdürmeli yol (traceOpDwellBuckets, vars. 2; aktif olay muaf).
	if r.store.NativeDetectorOn(ctx, chstore.NativeTraceOp) { // v0.10.1746
		traceHits, err := DetectTraceOpAnomaliesSustained(ctx, r.store, r.window)
		if err != nil {
			log.Printf("[anomaly-recorder] trace ops: %v", err)
		}
		for _, a := range traceHits {
			ev := traceOpEvent(a, now)
			if err := r.store.UpsertAnomalyEvent(ctx, ev); err != nil {
				log.Printf("[anomaly-recorder] upsert trace-op %s/%s: %v", a.Service, a.Operation, err)
			}
		}

		// ── Operasyon-gecikme anomalileri (v0.9.1064, Faz 2.3 / G5) ──────
		// trace_ops yalnız hata sayar; bu dedektör p99 sıçramasını yakalar
		// ("hangi endpoint yavaşladı" bugüne dek cevapsızdı). Ratio = p99
		// oranı; CurrentCount = cari çağrı hacmi (terfi kapılarının hacim
		// sezgisiyle uyumlu); Sample = en yavaş cari span'in trace'i.
		//
		// v0.10.1056 — anahtar (anomaly_sensitivity.opLatency; operatör: "Trace op
		// latency false pozitif geliyor, gerek yok gelmelerine bence.").
		// Anahtar tik başına atomic'ten (CH okuması yok); kapalıyken adım dedektörü
		// HİÇ çağırmaz. v0.10.1085 — varsayılan yeniden AÇIK, ama yalnız sürdürme
		// kuralıyla (ardışık opLatencyDwellBuckets kova, vars. 2 — dedektörde);
		// olay şekli değişmedi.
	}
	recordOpLatency(ctx, r.store.AnomalySensitivityForDetectors().OpLatencyOn(), now,
		func(ctx context.Context) ([]OpLatencyAnomaly, error) {
			return DetectOpLatencyAnomalies(ctx, r.store, r.window)
		},
		r.store.UpsertAnomalyEvent)
}

// recordOpLatency — recorder'ın operasyon-gecikme adımı (v0.10.1056'te
// tick'ten çıkarıldı ki anahtar sahte dedektör/upsert ile sınanabilsin).
// on=false → detect ÇAĞRILMAZ: MV sorgusu, v0.10.1046 batch kapısının
// aktif-olay okuması ve upsert yok; açık olaylar yazılmadıkları için aktif
// yaştan sonra düşer. on=true → v0.10.1056 öncesi gövdenin aynısı.
func recordOpLatency(ctx context.Context, on bool, now time.Time,
	detect func(context.Context) ([]OpLatencyAnomaly, error),
	upsert func(context.Context, chstore.AnomalyEvent) error) {
	if !on {
		return
	}
	latHits, err := detect(ctx)
	if err != nil {
		log.Printf("[anomaly-recorder] op latency: %v", err)
	}
	for _, a := range latHits {
		ev := opLatencyEvent(a, now)
		if err := upsert(ctx, ev); err != nil {
			log.Printf("[anomaly-recorder] upsert op-latency %s/%s: %v", a.Service, a.Operation, err)
		}
	}
}

// logPatternActiveSet — v0.10.1586: olayı zaten AKTİF log_pattern
// desenlerinin FingerprintAnomaly kümesi (sürdürme muafiyeti; yalnız
// açılış kapılı). dwell ≤ 1 → okuma yok. Okuma hatası → boş küme (aktif desen
// de iki pencere ister — sessiz yön), geçişte bir kez log. Tavan
// batchLatActiveCap; ötesi muaf değil.
func logPatternActiveSet(ctx context.Context, store *chstore.Store, dwell int) map[string]bool {
	if dwell <= 1 || store == nil {
		return nil
	}
	keys, err := store.ListActiveAnomalyKeys(ctx, "log_pattern", "", opLatActiveAge, "", nil, batchLatActiveCap+1)
	logPatternActiveReadLatch.report(err != nil,
		"[anomaly-recorder] log patterns: aktif olay okunamadı (%v) — sürdürme muafiyeti bu okuma düzelene dek UYGULANMIYOR", err)
	if err != nil {
		return nil
	}
	keys, _ = batchLatCapKeys(keys)
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[chstore.FingerprintAnomaly("log_pattern", k.Pattern, k.Service)] = true
	}
	return out
}

// ── Olay kurucuları (v0.10.1792, Davis F1.4) ─────────────────────────────
//
// Recorder'ın dört yazımı SAF kuruculardan geçer ki yön / taban kolonları
// her yolda pinlenebilsin (recorder_events_test.go). Bu dedektörler yalnız
// ARTIŞ yakalar (direction up). Taban ardışık penceredir (consecutive);
// oran yazılamayan (ilk görülme, taban 0'a yuvarlanan) olay "none"
// (chstore.CountOnlyRatio ile birlikte — eski okuyucular ratio=0'ı okur).

// recorderBaselineKind — SAF: oran 0 (sayım-yalnız) → none, aksi consecutive.
func recorderBaselineKind(ratio float64) string {
	if ratio == chstore.CountOnlyRatio {
		return chstore.AnomalyBaselineNone
	}
	return chstore.AnomalyBaselineConsecutive
}

func logPatternEvent(a LogPatternAnomaly, now time.Time) chstore.AnomalyEvent {
	return chstore.AnomalyEvent{
		ID:           chstore.FingerprintAnomaly("log_pattern", a.Pattern, a.Service),
		Kind:         "log_pattern",
		Pattern:      a.Pattern,
		Service:      a.Service,
		StartedAt:    now.UnixNano(),
		LastSeen:     a.LastSeenNs,
		CurrentRatio: a.Ratio,
		CurrentCount: a.CurrentCount,
		Sample:       a.Sample,
		// v0.10.1080 — ES örneklem doğrulaması (0 = örneklenmedi / CH).
		VerifiedRatio: a.VerifiedRatio,
		Direction:     chstore.AnomalyDirectionUp,
		BaselineValue: float64(a.BaselineCount),
		BaselineKind:  recorderBaselineKind(a.Ratio),
		SubjectKind:   chstore.AnomalySubjectService,
	}
}

func traceOpEvent(a TraceOpAnomaly, now time.Time) chstore.AnomalyEvent {
	return chstore.AnomalyEvent{
		ID:            chstore.FingerprintAnomaly("trace_op", a.Operation, a.Service),
		Kind:          "trace_op",
		Pattern:       a.Operation,
		Service:       a.Service,
		StartedAt:     now.UnixNano(),
		LastSeen:      a.LastSeenNs,
		CurrentRatio:  a.Ratio,
		CurrentCount:  a.CurrentErrors,
		Sample:        a.SampleTraceID,
		Direction:     chstore.AnomalyDirectionUp,
		BaselineValue: float64(a.BaselineErrors),
		BaselineKind:  recorderBaselineKind(a.Ratio),
		SubjectKind:   chstore.AnomalySubjectService,
	}
}

func opLatencyEvent(a OpLatencyAnomaly, now time.Time) chstore.AnomalyEvent {
	return chstore.AnomalyEvent{
		ID:            chstore.FingerprintAnomaly(opLatencyKind, a.Operation, a.Service),
		Kind:          opLatencyKind,
		Pattern:       a.Operation,
		Service:       a.Service,
		StartedAt:     now.UnixNano(),
		LastSeen:      a.LastSeenNs,
		CurrentRatio:  a.Ratio,
		CurrentCount:  a.CurCalls,
		Sample:        a.SampleTraceID,
		Direction:     chstore.AnomalyDirectionUp,
		BaselineValue: a.BaseP99Ms,
		BaselineKind:  recorderBaselineKind(a.Ratio),
		SubjectKind:   chstore.AnomalySubjectService,
	}
}
