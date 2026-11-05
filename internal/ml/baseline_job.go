package ml

// baseline_job.go — v0.10.1814, öğrenme döngüsü Faz 1 dilim 1.3 (audit
// docs/anomaly/learning-loop-audit.md §4.2): günlük baseline gözlem işi.
//
//   - Yer: ML alt sisteminin pod'u (varsayılan sre), ML lider kilidi
//     (Recorder.leader) — ayrı kilit yok, tek yazıcı.
//   - Anahtar: `ml.baseline.enabled` (MLConfig.BaselineOn: ana anahtar VE
//     baseline; varsayılan KAPALI). Kapalıyken ml_series okunmaz (yalnız
//     30 sn'de bir ayar blobu, lider pod'da).
//   - Takvim: yerel gün takvim diliminden (calendarFor — dedektörlerle aynı
//     kaynak, yeni dilim yok). 03:00 yerel sonrası dün; kaçırılan günler en
//     yeni 7'ye kadar, eskiden yeniye (baselinePendingDays). Son işlenen gün
//     `ml_baseline_status` blobunda.
//   - Okuma: günün ml_series satırları servis karmasına göre 16 dilimde,
//     akışla (chstore.MLSeriesDaySlice: zaman sınırlı, FINAL, LIMIT,
//     max_execution_time 25 — havuzun 30 sn ReadTimeout'u altında);
//     bellekte tek seri + trace servis toplamı.
//   - Yazma: aynı anahtar = RMT üzerine yazar; gün yeniden koşulabilir.
//   - Elle koşu: admin `POST /api/ml/baseline/run` kontrol blobuna istek
//     yazar (`ml_baseline_control`); lider 30 sn yoklamada alır. Durum ve
//     kontrol AYRI anahtarlar: ml_tick_status her tikte baştan yazılır (aynı
//     blobu iki yazıcı ezerdi), durumun tek yazıcısı lider.
//   - Tespit DEĞİŞMEZ: dedektörler bu tabloyu okumaz (dilim 1.6).

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	baselineSlices      = 16
	baselineMaxCatchUp  = 7
	baselinePoll        = 30 * time.Second
	baselineDayTimeout  = 30 * time.Minute
	baselineRetryAfter  = 15 * time.Minute
	baselineLabelsSlack = 24 * time.Hour
)

// baselineStore — CH dikişi (testte sahte).
type baselineStore interface {
	MLSeriesDaySlice(ctx context.Context, from, to time.Time, slice, slices int, fn func(chstore.MLSeriesRow) error) (int, bool, error)
	InsertMLBaselineObs(ctx context.Context, rows []chstore.MLBaselineObs) error
}

// baselineDayResult — bir günün koşusu.
type baselineDayResult struct {
	Rows, Series, Slices, Truncated int
	Duration                        time.Duration
}

// runBaselineDay — bir yerel gün: 16 dilim, akışla oku → hesapla → yaz.
// İlk hatada döner (kısmi yazım idempotent; yeniden koşu tamamlar).
func runBaselineDay(ctx context.Context, st baselineStore, in baselineInput, day string) (baselineDayResult, error) {
	t0 := time.Now()
	var res baselineDayResult
	from, to, err := baselineDayBounds(day, in.Loc)
	if err != nil {
		return res, err
	}
	for slice := 0; slice < baselineSlices; slice++ {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		b := newObsBuilder(in, func(rows []chstore.MLBaselineObs) error {
			if err := st.InsertMLBaselineObs(ctx, rows); err != nil {
				return err
			}
			res.Rows += len(rows)
			return nil
		})
		_, truncated, err := st.MLSeriesDaySlice(ctx, from, to, slice, baselineSlices, b.add)
		if err == nil {
			err = b.finish()
		}
		if err != nil {
			res.Duration = time.Since(t0)
			return res, fmt.Errorf("dilim %d/%d: %w", slice+1, baselineSlices, err)
		}
		if truncated {
			res.Truncated++
		}
		res.Series += b.series
		res.Slices++
	}
	res.Duration = time.Since(t0)
	return res, nil
}

// baselineWork — sıradaki iş.
type baselineWork struct {
	day     string
	trigger string // auto | manual
	reqID   string
}

// baselineJob — süreç-içi durum (lider pod).
type baselineJob struct {
	running   atomic.Bool
	nextRetry atomic.Int64 // unix sn: otomatik koşu hatasından sonra bekleme
}

// baselineLoop — lider yoklaması; iş ayrı goroutine'de (tik döngüsünü bloklamaz).
func (r *Recorder) baselineLoop(ctx context.Context) {
	t := time.NewTicker(baselinePoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r.baselinePoll(ctx)
	}
}

func (r *Recorder) baselinePoll(ctx context.Context) {
	if r.bjob.running.Load() || !r.leader.IsLeader() {
		return
	}
	cfg := r.store.GetMLConfig(ctx)
	if !cfg.BaselineOn() {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, _, err := r.store.GetMLBaselineStatus(pctx)
	if err != nil {
		return // durum okunamadı: kör koşma (son gün bilinmiyor)
	}
	ctl, _, _ := r.store.GetMLBaselineControl(pctx)
	_, loc := calendarFor(r.store.AnomalySensitivityForDetectors())
	now := time.Now()
	work := planBaselineWork(st, ctl, now, loc, r.bjob.nextRetry.Load())
	if len(work) == 0 {
		return
	}
	r.bjob.running.Store(true)
	go func() {
		defer r.bjob.running.Store(false)
		r.runBaselineWork(ctx, st, work)
	}()
}

// planBaselineWork — SAF: önce bekleyen elle istek, sonra otomatik günler
// (hata sonrası bekleme süresi dolmadıysa otomatik yok).
func planBaselineWork(st chstore.MLBaselineStatus, ctl chstore.MLBaselineControl, now time.Time, loc *time.Location, nextRetry int64) []baselineWork {
	var work []baselineWork
	if ctl.Run != nil && ctl.Run.ID != "" && ctl.Run.ID != st.ManualDone {
		work = append(work, baselineWork{day: ctl.Run.Day, trigger: "manual", reqID: ctl.Run.ID})
	}
	if now.Unix() >= nextRetry {
		for _, d := range baselinePendingDays(now, loc, st.LastDay, baselineMaxCatchUp) {
			work = append(work, baselineWork{day: d, trigger: "auto"})
		}
	}
	return work
}

func (r *Recorder) runBaselineWork(ctx context.Context, st chstore.MLBaselineStatus, work []baselineWork) {
	for _, w := range work {
		cfg := r.store.GetMLConfig(ctx)
		if !cfg.BaselineOn() || !r.leader.IsLeader() || ctx.Err() != nil {
			return // anahtar kapandı / liderlik gitti: temiz dur, kalan gün sırada
		}
		cal, loc := calendarFor(r.store.AnomalySensitivityForDetectors())
		dctx, cancel := context.WithTimeout(chstore.WithQueryTag(ctx, "ml:baseline"), baselineDayTimeout)
		in := baselineInput{Loc: loc, Cal: cal, TargetSec: cfg.BucketSec, CfgHash: baselineCfgHash(cfg)}
		from, _, ferr := baselineDayBounds(w.day, loc)
		if ferr == nil {
			labels, lerr := r.store.MLLabels(dctx, from.Add(-baselineLabelsSlack))
			if lerr != nil {
				log.Printf("[ml] baseline etiketler okunamadı: %v (hijyensiz devam)", lerr)
			}
			in.Exclude = NewLabels(labels).Exclude(cfg)
		}
		st.Running, st.UpdatedAt = w.day, time.Now().Unix()
		r.saveBaselineStatus(ctx, st)
		res, err := runBaselineDay(dctx, r.store, in, w.day)
		cancel()
		run := &chstore.MLBaselineRun{Day: w.day, Trigger: w.trigger, Rows: res.Rows, Series: res.Series, Slices: res.Slices,
			Truncated: res.Truncated, DurationMs: res.Duration.Milliseconds(), At: time.Now().Unix()}
		if err != nil {
			run.Error = truncRunes(err.Error(), 300)
		}
		st.Running = ""
		if w.trigger == "manual" {
			st.ManualDone, st.Manual = w.reqID, run
		} else {
			st.Last = run
			if err == nil {
				st.LastDay = w.day
			} else {
				r.bjob.nextRetry.Store(time.Now().Add(baselineRetryAfter).Unix())
			}
		}
		st.UpdatedAt = time.Now().Unix()
		r.saveBaselineStatus(ctx, st)
		log.Printf("[ml] baseline day=%s trigger=%s rows=%d series=%d slices=%d/%d truncated=%d dur=%dms err=%v",
			w.day, w.trigger, res.Rows, res.Series, res.Slices, baselineSlices, res.Truncated, res.Duration.Milliseconds(), err)
		if err != nil && w.trigger == "auto" {
			return // sıradaki günler bekleme sonrası
		}
	}
}

func (r *Recorder) saveBaselineStatus(ctx context.Context, st chstore.MLBaselineStatus) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := r.store.SaveMLBaselineStatus(sctx, st); err != nil {
		log.Printf("[ml] baseline durumu yazılamadı: %v", err)
	}
}
