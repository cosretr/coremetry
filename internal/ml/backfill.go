package ml

// backfill.go — ML log geçmiş doldurma (v0.10.1763; prod gözlemi 01.11.2026:
// eski backfill tik context'ine bağlıydı, tik bitince "context canceled",
// tek-seferlik bayrak yüzünden bir daha denenmedi → log tabanı hiç oluşmadı).
//   - Kendi context'i: Deps.Background (alt sistem ömrü), tik bitince sürer.
//   - Saatlik pencereler (21 × 24 = 504 istek), backfill'e özel ES zaman aşımı
//     (cfg.BackfillTimeoutMs) ve hız sınırı (cfg.BackfillIntervalMs).
//   - Kısmi sonuç (timed_out / shard failed) ya da hata → pencere yarıya
//     bölünür, iki yarı ayrı denenir (yarı kova bucket_sec=1800 ile yazılır;
//     taban normalize eder); yarı da düşerse o saat "eksik".
//   - Hata "bitti" sayılmaz; ilerleme CH'de (system_settings), restart'ta
//     DoneUntil'den devam. İlerleme logda (gün başına) ve karnede.

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// backfillStore — CH dikişi (testte sahte).
type backfillStore interface {
	InsertMLSeries(ctx context.Context, rows []chstore.MLSeriesRow) error
	GetMLBackfillProgress(ctx context.Context, signal string) (chstore.MLBackfillProgress, bool, error)
	SaveMLBackfillProgress(ctx context.Context, p chstore.MLBackfillProgress) error
}

// backfillRunner — bir sinyalin geçmiş doldurucusu; aynı anda tek koşu.
type backfillRunner struct {
	signal string
	days   int
	mu     sync.Mutex
	run    bool
	done   bool
	sleep  func(time.Duration) // test: no-op
}

func newBackfillRunner(signal string, days int) *backfillRunner {
	return &backfillRunner{signal: signal, days: days, sleep: time.Sleep}
}

// backfillHours — SAF: [end − days, end) saatlik pencereler, eskiden yeniye,
// `after`'dan (dahil) öncekiler atlanır (resume).
func backfillHours(end time.Time, days int, after int64) [][2]time.Time {
	end = end.Truncate(time.Hour)
	var out [][2]time.Time
	for t := end.Add(-time.Duration(days) * 24 * time.Hour); t.Before(end); t = t.Add(time.Hour) {
		if t.Unix() <= after {
			continue
		}
		out = append(out, [2]time.Time{t, t.Add(time.Hour)})
	}
	return out
}

// maybeStart — tikten çağrılır; koşu yoksa ve tamamlanmadıysa arka planda başlatır.
func (b *backfillRunner) maybeStart(deps Deps, cfg chstore.MLConfig, agg logstore.MLLogAggregator, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.run || b.done || deps.Background == nil {
		return
	}
	b.run = true
	go func() {
		complete := b.execute(deps.Background, deps.Store, agg, cfg, now, deps.ObserveES)
		b.mu.Lock()
		b.run, b.done = false, complete
		b.mu.Unlock()
	}()
}

// execute — koşu; complete = plan bitti (eksikler sayılmış). Bağlam iptali
// (kapanış) → false, sonraki boot kaldığı yerden sürer.
func (b *backfillRunner) execute(ctx context.Context, store backfillStore, agg logstore.MLLogAggregator, cfg chstore.MLConfig, now time.Time, observe func(time.Duration)) bool {
	if observe == nil {
		observe = func(time.Duration) {}
	}
	prog, ok, err := store.GetMLBackfillProgress(ctx, b.signal)
	end := now.Truncate(time.Hour)
	if err != nil {
		log.Printf("[ml] %s backfill ilerleme okunamadı: %v (baştan)", b.signal, err)
	}
	if ok && prog.Complete && end.Unix()-prog.PlanEnd < 48*3600 {
		return true // yakın zamanda bitmiş
	}
	if !ok || prog.Complete || end.Unix()-prog.PlanEnd > 7*24*3600 {
		prog = chstore.MLBackfillProgress{Signal: b.signal, StartedAt: now.Unix()}
	}
	windows := backfillHours(end, b.days, prog.DoneUntil)
	prog.PlanEnd, prog.Running, prog.LastError = end.Unix(), true, ""
	prog.Total = prog.Done + len(prog.Missing) + len(windows)
	_ = store.SaveMLBackfillProgress(ctx, prog)
	log.Printf("[ml] %s backfill başlıyor: %d saat (%d/%d önceden), zaman aşımı %d ms, aralık %d ms",
		b.signal, len(windows), prog.Done, prog.Total, cfg.BackfillTimeoutMs, cfg.BackfillIntervalMs)
	timeout := time.Duration(cfg.BackfillTimeoutMs) * time.Millisecond
	interval := time.Duration(cfg.BackfillIntervalMs) * time.Millisecond
	lastDay := ""
	for i, w := range windows {
		if ctx.Err() != nil {
			prog.Running = false
			_ = store.SaveMLBackfillProgress(context.Background(), prog)
			return false
		}
		ferr := b.tryHour(ctx, store, agg, cfg, w, timeout, observe)
		if ctx.Err() != nil {
			prog.Running = false
			_ = store.SaveMLBackfillProgress(context.Background(), prog)
			return false
		}
		if ferr != nil {
			prog.Missing = append(prog.Missing, w[0].Unix())
			prog.LastError = truncRunes(ferr.Error(), 200)
		} else {
			prog.Done++
		}
		prog.DoneUntil = w[0].Unix()
		if d := w[0].Format("2006-01-02"); d != lastDay {
			lastDay = d
			log.Printf("[ml] %s backfill %d/%d saat (eksik %d) · %s", b.signal, prog.Done+len(prog.Missing), prog.Total, len(prog.Missing), d)
		}
		if err := store.SaveMLBackfillProgress(ctx, prog); err != nil && ctx.Err() == nil {
			log.Printf("[ml] %s backfill ilerleme yazılamadı: %v", b.signal, err)
		}
		if i < len(windows)-1 {
			b.sleep(interval)
		}
	}
	prog.Complete, prog.Running = true, false
	_ = store.SaveMLBackfillProgress(ctx, prog)
	log.Printf("[ml] %s backfill bitti: %d/%d saat, eksik %d", b.signal, prog.Done, prog.Total, len(prog.Missing))
	return true
}

// fetch — tam saat denemesi: başarılıysa yazar ve nil döner; kısmi/hata →
// yazmaz, hatayı döner (çağıran yarıya böler).
func (b *backfillRunner) fetch(ctx context.Context, store backfillStore, agg logstore.MLLogAggregator, cfg chstore.MLConfig, from, to time.Time, timeout time.Duration, observe func(time.Duration)) error {
	bctx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()
	t0 := time.Now()
	res, err := agg.MLLogAggregate(bctx, logstore.MLLogAggRequest{From: from, To: to, BucketSec: int(to.Sub(from).Seconds()),
		MaxServices: cfg.MaxServices, ErrorLevels: cfg.ErrorLevels, Timeout: timeout})
	observe(time.Since(t0))
	if err != nil {
		return err
	}
	if res.Partial {
		return fmt.Errorf("kısmi sonuç: %s", res.PartialReason)
	}
	sec := uint32(to.Sub(from).Seconds())
	var rows []chstore.MLSeriesRow
	for k, byB := range seriesFromRows(res.Rows, cfg.ErrorLevels) {
		svc, part, _ := strings.Cut(k, "\x00")
		for bk, c := range byB {
			rows = append(rows, chstore.MLSeriesRow{Signal: b.signal, Service: svc, Part: part, Bucket: time.Unix(bk, 0), BucketSec: sec, Count: c})
		}
	}
	return store.InsertMLSeries(bctx, rows)
}

// tryHour — tam saat → düşerse yarılar. Dönen hata = saat eksik.
func (b *backfillRunner) tryHour(ctx context.Context, store backfillStore, agg logstore.MLLogAggregator, cfg chstore.MLConfig, w [2]time.Time, timeout time.Duration, observe func(time.Duration)) error {
	if err := b.fetch(ctx, store, agg, cfg, w[0], w[1], timeout, observe); err == nil || ctx.Err() != nil {
		return err
	}
	mid := w[0].Add(w[1].Sub(w[0]) / 2)
	var firstErr error
	for _, h := range [][2]time.Time{{w[0], mid}, {mid, w[1]}} {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		b.sleep(time.Duration(cfg.BackfillIntervalMs) * time.Millisecond)
		if err := b.fetch(ctx, store, agg, cfg, h[0], h[1], timeout, observe); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
