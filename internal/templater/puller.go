package templater

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/selfobs"
)

// Puller is the background goroutine that drives the Drain
// templater. Each tick (default 5 min) it samples N=1000 recent
// logs from whichever backend is wired, feeds them to Drain,
// then flushes the resulting clusters into chstore.LogTemplate.
//
// Sample-based on purpose — at billion-log/day a full pass per
// tick would dominate the cluster's load. 1000 samples / 5 min
// is enough to surface every template emitting at ≥10/min
// without significant under-sampling (Drain merges on
// similarity so a slightly truncated input still produces the
// same template).
//
// Lock-gated for HA: only one replica per tick writes; the
// others skip cleanly. The lease TTL is generous (2× tick) so
// a slow tick doesn't get a second writer fighting it.
type Puller struct {
	store    *chstore.Store
	logs     logstore.Store
	interval time.Duration
	sample   int // docs per tick
	lock     cache.Lock
	leader   *cache.LeaderHolder // v0.5.429
	drain    *Drain
	// maskerChecked — v0.10.1829: bu süreç maske sürümünü (system_settings
	// `log_masker_version`) doğruladı/yazdı mı. İlk lider tikinde, defter
	// yazımından ÖNCE (ensureMaskerVersion).
	maskerChecked bool
	// maskerStore — testte sahte; nil → store.
	maskerStore maskerStateStore
}

// maskerStateStore — ensureMaskerVersion'ın tek bağımlılığı (*chstore.Store
// karşılar).
type maskerStateStore interface {
	GetLogMaskerState(ctx context.Context) (chstore.LogMaskerState, bool, error)
	PutLogMaskerState(ctx context.Context, st chstore.LogMaskerState) error
}

const pullerLockKey = "coremetry:lock:templater-puller"

// New returns a puller with sane defaults. interval defaults
// to 5min; sample defaults to 1000 docs.
func New(store *chstore.Store, logs logstore.Store, interval time.Duration, sample int, lock cache.Lock) *Puller {
	if interval == 0 {
		interval = 5 * time.Minute
	}
	if sample <= 0 {
		sample = 1000
	}
	return &Puller{
		store:    store,
		logs:     logs,
		interval: interval,
		sample:   sample,
		lock:     lock,
		leader:   cache.NewLeaderHolder(lock, pullerLockKey, cache.LeaderTTL(interval)),
		drain:    NewDrain(),
	}
}

// Start runs until ctx is cancelled. Each tick:
//  1. Try the HA lock; skip if another replica got it.
//  2. Pull ~sample docs from the last `interval` window.
//  3. Feed every doc to the Drain templater.
//  4. Snapshot the resulting clusters; upsert each into CH.
//  5. Reset Drain — next tick starts cold against the next
//     window. Persistent state lives in log_templates; the
//     in-memory tree is just a per-batch scratchpad.
//
// The pre-cancel select gives ctx a fair shake on shutdown so
// a long-running pull doesn't block ProcessExit.
func (p *Puller) Start(ctx context.Context) {
	if p.logs == nil || p.store == nil {
		log.Printf("[templater] no logs/store wired, puller disabled")
		return
	}
	p.leader.Start(ctx)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	p.tick(ctx) // immediate first run so a fresh deploy sees results within 5min, not 10
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

func (p *Puller) tick(parent context.Context) {
	if !p.leader.IsLeader() {
		return
	}
	defer selfobs.ObserveJob("templater", time.Now(), nil) // v0.10.1422 — worker RED

	// v0.8.3 — per-tick deadline so a slow/hung ES Search on the worker
	// pod can't pile up against the process-lifetime ctx (no-op on CH,
	// which self-terminates; bounds the ES hang that double-loads the
	// cluster alongside api-side traffic).
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	now := time.Now()
	page, err := p.logs.Search(ctx, logstore.Filter{
		From:  now.Add(-p.interval),
		To:    now,
		Limit: p.sample,
		// v0.9.286 — stated, not inherited: this sample never pages,
		// it drops page.NextCursor on the floor. It is also the worst
		// place to retain a Point-in-Time — a timer firing every
		// `interval` with a full 1000-row page, i.e. a leaked PIT on
		// every tick for as long as the process lives.
		WantCursor: false,
	})
	if err != nil {
		log.Printf("[templater] pull: %v", err)
		return
	}
	if len(page.Logs) == 0 {
		return
	}

	skipped := 0
	for _, r := range page.Logs {
		body := r.Body
		if body == "" {
			continue
		}
		// Operator preference (v0.5.336): bearer / JWT / api-key
		// carrying lines pollute the templates view — every
		// request has a unique token, so they either fragment
		// into singleton templates or collapse under aggressive
		// masking. Drop them at the source; the raw log search
		// still surfaces them.
		if IsSensitiveLine(body) {
			skipped++
			continue
		}
		// v0.10.2040 — JSON zarfında şablon `message` alanından (newness.go).
		c := p.drain.Add(TemplateSource(body), r.ServiceName, r.Timestamp)
		if IsErrorSeverity(r.Severity, r.SeverityText) { // v0.10.2029 — kalıbın seviyesi
			p.drain.NoteErrorPlus(c)
		}
	}

	// v0.10.1829 — maske sürümü geçişi defter yazımından ÖNCE: yeni maskenin
	// kimlikleri yazılmadan sessiz pencere kayıtlı olmalı (recorder başka
	// pod'da koşabilir). Yazılamazsa bu tik defter YAZILMAZ — korumasız bir
	// "yeni şablon" seli yerine bir tik gecikme.
	if !p.maskerChecked {
		ms := p.maskerStore
		if ms == nil {
			ms = p.store
		}
		if err := ensureMaskerVersion(ctx, ms, logstore.MaskerVersion, time.Now()); err != nil {
			log.Printf("[templater] maske sürümü doğrulanamadı, bu tik defter yazılmadı: %v", err)
			p.drain.Reset()
			return
		}
		p.maskerChecked = true
	}

	clusters := p.drain.Snapshot()
	saved := 0
	// v0.10.1157 — toplu yazım + ayrı bütçe: ES örneği 30 sn'lik tik
	// bağlamını tüketse de yazım kendi 20 sn'siyle koşar (kökten iptal
	// yine geçer); küme başına 2 gidiş-dönüş yerine iki sorgu.
	ts := make([]chstore.LogTemplate, 0, len(clusters))
	sevAt := time.Now()
	for _, c := range clusters {
		RecordTemplateSeverity(c.ID, c.ErrPlus, c.Count, sevAt) // v0.10.2029 — süreç içi seviye kaydı
		ts = append(ts, chstore.LogTemplate{
			ID:         c.ID,
			Template:   c.TemplateString(),
			FirstSeen:  c.FirstSeen,
			LastSeen:   c.LastSeen,
			TotalCount: c.Count,
			Services:   c.Services,
			Sample:     c.Sample,
		})
	}
	wctx, wcancel := context.WithTimeout(parent, 20*time.Second)
	if err := p.store.UpsertLogTemplates(wctx, ts); err != nil {
		log.Printf("[templater] upsert %d templates: %v", len(ts), err)
	} else {
		saved = len(ts)
	}
	wcancel()
	stats := p.drain.Stats()
	log.Printf("[templater] tick: sampled=%d skipped_sensitive=%d clusters=%d saved=%d nodes=%d overflow_hits=%d reset_count=%d",
		len(page.Logs), skipped, len(clusters), saved,
		stats.NodeCount, stats.OverflowHits, stats.ResetCount)

	// v0.5.345 — pathology guard. If the tree grew past
	// drainNodeBudget within a single tick, log loudly + drop
	// the tree immediately to clamp memory. Threshold is
	// generous (10x the typical clusters-per-tick observed in
	// production); crossing it means the input stream has more
	// distinct shapes than the (depth, MaxChildren) tree can
	// represent, and the wildcard fallback is over-collapsing
	// anyway.
	const drainNodeBudget = 50_000
	if stats.NodeCount > drainNodeBudget {
		log.Printf("[templater] WARNING: drain tree exceeded budget (%d > %d) — forcing reset; consider lowering sample size or raising MaxChildren",
			stats.NodeCount, drainNodeBudget)
	}

	// Drop in-memory tree so the next tick processes the next
	// window cold. The persistent ledger (log_templates) carries
	// continuity — first_seen is sticky on upsert.
	p.drain.Reset()
}

// ensureMaskerVersion — v0.10.1829: kayıtlı maske sürümü çalışan sürümden
// farklıysa (blob yok = v1) yeni sürümü + 24 sa sessiz pencereyi yazar ve
// TEK satır loglar. Karar chstore.NextLogMaskerState'te (SAF).
func ensureMaskerVersion(ctx context.Context, ms maskerStateStore, current int, now time.Time) error {
	stored, found, err := ms.GetLogMaskerState(ctx)
	if err != nil {
		return err
	}
	next, write := chstore.NextLogMaskerState(stored, found, current, now)
	if !write {
		return nil
	}
	if err := ms.PutLogMaskerState(ctx, next); err != nil {
		return err
	}
	if next.QuietUntil > 0 {
		log.Printf("[templater] masker v%d→v%d: yeni şablon uyarıları %d sa susturuldu",
			next.From, next.Version, int(chstore.LogMaskerQuietWindow/time.Hour))
	}
	return nil
}
