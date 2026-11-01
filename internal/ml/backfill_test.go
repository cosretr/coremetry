package ml

// v0.10.1763 — log backfill: tik context'i iptal edilince sürer (kendi
// context'i), restart'ta DoneUntil'den devam, kısmi/zaman aşımı → yarıya böl,
// yarı da düşerse saat eksik; hata "bitti" sayılmaz.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

type fakeBF struct {
	mu       sync.Mutex
	rows     []chstore.MLSeriesRow
	prog     chstore.MLBackfillProgress
	has      bool
	saves    int
	calls    []logstore.MLLogAggRequest
	failHour int64 // bu saat başı: tam saat kısmi, yarılar da hata
	partHour int64 // bu saat başı: tam saat kısmi, yarılar OK
}

func (f *fakeBF) InsertMLSeries(_ context.Context, rows []chstore.MLSeriesRow) error {
	f.mu.Lock()
	f.rows = append(f.rows, rows...)
	f.mu.Unlock()
	return nil
}
func (f *fakeBF) GetMLBackfillProgress(context.Context, string) (chstore.MLBackfillProgress, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prog, f.has, nil
}
func (f *fakeBF) SaveMLBackfillProgress(_ context.Context, p chstore.MLBackfillProgress) error {
	f.mu.Lock()
	f.prog, f.has = p, true
	f.saves++
	f.mu.Unlock()
	return nil
}
func (f *fakeBF) MLResolveField(context.Context, []string) string { return "" }
func (f *fakeBF) MLLogAggregate(_ context.Context, req logstore.MLLogAggRequest) (*logstore.MLLogAggResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()
	full := req.To.Sub(req.From) == time.Hour
	switch {
	case req.From.Unix() == f.failHour && full, req.From.Unix() == f.partHour && full:
		return &logstore.MLLogAggResult{Partial: true, PartialReason: "soft timeout"}, nil
	case f.failHour != 0 && req.From.Unix() >= f.failHour && req.From.Unix() < f.failHour+3600:
		return nil, errors.New("ES raw search: timeout")
	}
	return &logstore.MLLogAggResult{Rows: []logstore.MLLogRow{{Service: "pay", Level: "INFO", Bucket: req.From.Unix(), Count: 10}}}, nil
}

func TestBackfillSurvivesTickCancelAndSplits(t *testing.T) {
	now := time.Date(2026, 11, 1, 10, 30, 0, 0, time.UTC)
	f := &fakeBF{}
	plan := backfillHours(now, 1, 0) // 24 saat
	f.failHour, f.partHour = plan[3][0].Unix(), plan[7][0].Unix()
	cfg := chstore.DefaultMLConfig()
	b := newBackfillRunner(chstore.MLSignalLog, 1)
	b.sleep = func(time.Duration) {}
	tick, cancel := context.WithCancel(context.Background())
	cancel() // tik bitti
	bg := context.Background()
	deps := Deps{Background: bg}
	_ = tick
	if !b.execute(deps.Background, f, f, cfg, now, nil) {
		t.Fatal("koşu tamamlanmalı")
	}
	p := f.prog
	if !p.Complete || p.Total != 24 || p.Done != 23 || len(p.Missing) != 1 || p.Missing[0] != f.failHour {
		t.Fatalf("prog=%+v", p)
	}
	// part saat: tam + 2 yarı = 3 çağrı; fail saat: tam + 2 yarı (hata)
	halves := 0
	for _, c := range f.calls {
		if c.To.Sub(c.From) == 30*time.Minute {
			halves++
		}
		if c.Timeout != 30*time.Second {
			t.Fatalf("backfill zaman aşımı istekte taşınmalı: %v", c.Timeout)
		}
	}
	if halves != 4 {
		t.Fatalf("yarı çağrı=%d", halves)
	}
	// yarı kova bucket_sec 1800 ile yazıldı
	seen1800 := false
	for _, r := range f.rows {
		if r.BucketSec == 1800 {
			seen1800 = true
		}
	}
	if !seen1800 {
		t.Fatal("yarı saat kovası yazılmalı")
	}
}

func TestBackfillResumesAndStopsOnShutdown(t *testing.T) {
	now := time.Date(2026, 11, 1, 10, 30, 0, 0, time.UTC)
	plan := backfillHours(now, 1, 0)
	f := &fakeBF{has: true, prog: chstore.MLBackfillProgress{Signal: "log", Total: 24, Done: 10, DoneUntil: plan[9][0].Unix(), PlanEnd: now.Truncate(time.Hour).Unix(), Running: true}}
	cfg := chstore.DefaultMLConfig()
	b := newBackfillRunner(chstore.MLSignalLog, 1)
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	b.sleep = func(time.Duration) {
		calls++
		if calls == 5 {
			cancel() // kapanış
		}
	}
	if b.execute(ctx, f, f, cfg, now, nil) {
		t.Fatal("kapanışta bitti sayılmamalı")
	}
	if f.calls[0].From != plan[10][0] {
		t.Fatalf("resume plan[10]'dan başlamalı, got %v", f.calls[0].From)
	}
	if f.prog.Complete || f.prog.Running || f.prog.Done != 15 {
		t.Fatalf("prog=%+v", f.prog)
	}
	// ikinci boot: kaldığı yerden
	f2 := &fakeBF{has: true, prog: f.prog}
	b2 := newBackfillRunner(chstore.MLSignalLog, 1)
	b2.sleep = func(time.Duration) {}
	if !b2.execute(context.Background(), f2, f2, cfg, now, nil) || f2.prog.Done != 24 || !f2.prog.Complete {
		t.Fatalf("ikinci boot prog=%+v", f2.prog)
	}
	// yakın zamanda bitmiş → yeniden koşmaz
	f3 := &fakeBF{has: true, prog: f2.prog}
	if !newBackfillRunner(chstore.MLSignalLog, 1).execute(context.Background(), f3, f3, cfg, now, nil) || len(f3.calls) != 0 {
		t.Fatal("tamamlanmış backfill yeniden koşmamalı")
	}
}
