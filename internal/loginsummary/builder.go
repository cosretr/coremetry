package loginsummary

// builder.go — tur döngüsü + devre kesici (audit §5.1–§5.6).
//
// Yalnız LİDER çalıştırır (main.go: cache.NewLeaderHolder + isLeader). Her
// Step: CH sayıları (her tur, ~60 sn); VM halkası + topoloji + CH boyutu
// (yavaş tur, ~5 dk). Arka uçlara yalnız arayüzle bağlıdır — testte sahteler.
// İstek yolu bu paketin hiçbir arka uç çağrısına gitmez.
//
// Başarısızlık kuralı: sayılar (hızlı) başarısızsa o alan nil olur ve cevaptan
// düşer; birikimli yapılar (halka, topoloji, boyut) son iyi hâlini korur.
// `state` yalnız "CH sayıları başarılı ve (VM kapalı ya da Q1 başarılı)" ise ok.

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// HTTPFamily — varsayılan histogram ailesi (chstore.ThroughputMetricDefault'ın kökü).
	HTTPFamily = "http_server_request_duration_seconds"

	DefaultFastEvery = 60 * time.Second
	DefaultSlowEvery = 5 * time.Minute

	vmQueryTimeout   = 5 * time.Second
	vmRecentWindow   = 15 * time.Minute
	vmChunksPerRound = 4
	breakerFailures  = 3
	breakerSkip      = 30 * time.Minute
	activityWindow   = 24 * time.Hour
	topoEdgeLimit    = 5000
)

// Queries — VM'e giden ÜÇ sabit ifade (kullanıcı girdisi yok).
func Queries(family string) (q1, q2, q3 string) {
	q1 = "sum by (http_response_status_code) (rate(" + family + "_count[5m]))"
	q2 = "sum(rate(" + family + "_sum[5m]))"
	q3 = "histogram_quantile(0.95, sum by (le, vmrange) (rate(" + family + "_bucket[5m])))"
	return
}

// Store — Builder'ın CH okuma yüzeyi (*chstore.Store bunu sağlar).
type Store interface {
	LoginSummaryCounts(ctx context.Context) (chstore.LoginCounts, error)
	CountCosreQuestions(ctx context.Context, since time.Time) (uint64, error)
	ReadSelfActivityTotals(ctx context.Context, from, to time.Time) (chstore.SelfActivityTotals, error)
	TotalUncompressedBytes(ctx context.Context) (uint64, error)
	ReadServiceTopologyAgg(ctx context.Context, from, to time.Time, limit int) ([]chstore.ServiceTopologyEdge, error)
}

var _ Store = (*chstore.Store)(nil)

// VM — VictoriaMetrics okuma yüzeyi.
type VM interface {
	Configured() bool
	AllowUnfilteredPercentiles() bool
	QueryPromQLRange(ctx context.Context, query string, from, to time.Time, stepSeconds, maxDataPoints int) ([]chstore.SpanMetricSeries, error)
}

// RolloutsFunc — [from, to) penceresinde izlenen rollout adedi; tracked=false
// → rollout izleme kapalı (alan döndürülmez).
type RolloutsFunc func(ctx context.Context, from, to time.Time) (total uint64, tracked bool, err error)

// Deps — Builder bağımlılıkları. Zorunlu: Store, Publish. Diğerleri isteğe bağlı.
type Deps struct {
	Store    Store
	VM       VM
	Rollouts RolloutsFunc
	Now      func() time.Time
	Load     func() *Snapshot // mevcut anlık görüntü (Redis/bellek): tuz ve halka buradan devralınır
	Publish  func(*Snapshot)
	NewSalt  func() string
	Logf     func(format string, args ...any)

	Family    string        // boşsa HTTPFamily
	FastEvery time.Duration // boşsa 60 sn
	SlowEvery time.Duration // boşsa 5 dk
}

// breaker — art arda 3 hatada 30 dk atlar.
type breaker struct {
	fails int
	until time.Time
}

func (b *breaker) allow(now time.Time) bool { return !now.Before(b.until) }

func (b *breaker) record(now time.Time, err error) {
	if err == nil {
		b.fails = 0
		return
	}
	b.fails++
	if b.fails >= breakerFailures {
		b.until = now.Add(breakerSkip)
		b.fails = 0
	}
}

// Builder — tek goroutine'den çağrılır (Run ya da test).
type Builder struct {
	d        Deps
	snap     *Snapshot
	lastSlow time.Time
	vmOK     bool // son yavaş turda VM Q1 başarılı mı (VM açıksa)
	br       map[string]*breaker
	timings  map[string]time.Duration
}

// New — Builder kurar.
func New(d Deps) *Builder {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewSalt == nil {
		d.NewSalt = NewSalt
	}
	if d.Logf == nil {
		d.Logf = log.Printf
	}
	if d.Family == "" {
		d.Family = HTTPFamily
	}
	if d.FastEvery <= 0 {
		d.FastEvery = DefaultFastEvery
	}
	if d.SlowEvery <= 0 {
		d.SlowEvery = DefaultSlowEvery
	}
	return &Builder{d: d, br: map[string]*breaker{"q1": {}, "q2": {}, "q3": {}}}
}

// Reset — liderlik kaybında çağrılır; sonraki Step anlık görüntüyü yeniden
// yükler (tuz ve halka başka pod'un yayınından devralınır).
func (b *Builder) Reset() {
	b.snap = nil
	b.lastSlow = time.Time{}
}

// Run — lider olduğu sürece her FastEvery'de Step; ctx bitene dek.
func (b *Builder) Run(ctx context.Context, isLeader func() bool) {
	tick := time.NewTicker(b.d.FastEvery)
	defer tick.Stop()
	for {
		if isLeader == nil || isLeader() {
			b.Step(ctx)
		} else {
			b.Reset()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (b *Builder) timed(name string, start time.Time) {
	b.timings[name] += time.Since(start)
}

// Step — bir tur: anlık görüntüyü günceller ve yayınlar.
func (b *Builder) Step(ctx context.Context) {
	now := b.d.Now()
	b.timings = map[string]time.Duration{}
	if b.snap == nil {
		if b.d.Load != nil {
			b.snap = b.d.Load()
		}
		if b.snap == nil || b.snap.V != SnapshotVersion {
			b.snap = &Snapshot{V: SnapshotVersion}
		}
	}
	s := b.snap
	if s.Salt == "" {
		s.Salt = b.d.NewSalt()
	}

	// ── hızlı: CH sayıları (her tur) ──
	countsOK := true
	t0 := time.Now()
	counts, err := b.d.Store.LoginSummaryCounts(ctx)
	b.timed("counts", t0)
	if err != nil {
		countsOK = false
	}
	s.SpansToday, s.TracesToday, s.Services = counts.Spans, counts.Traces, counts.Services

	s.Activity = nil
	t0 = time.Now()
	totals, aerr := b.d.Store.ReadSelfActivityTotals(ctx, now.Add(-activityWindow), now)
	b.timed("activity", t0)
	t0 = time.Now()
	cosre, cerr := b.d.Store.CountCosreQuestions(ctx, now.Add(-activityWindow))
	b.timed("cosre", t0)
	if aerr == nil && cerr == nil {
		s.Activity = &SnapActivity{Logins: totals.Logins, TracesViewed: totals.TracesViewed, CosreQuestions: cosre}
		if b.d.Rollouts != nil {
			t0 = time.Now()
			if n, tracked, rerr := b.d.Rollouts(ctx, now.Add(-activityWindow), now); rerr == nil && tracked {
				s.Activity.Rollouts = &n
			}
			b.timed("rollouts", t0)
		}
	}

	// ── yavaş: VM halkası + topoloji + CH boyutu (≈5 dk) ──
	if b.lastSlow.IsZero() || now.Sub(b.lastSlow) >= b.d.SlowEvery {
		b.lastSlow = now
		b.slowRound(ctx, s, now)
	}

	state := StateOK
	vmOn := b.d.VM != nil && b.d.VM.Configured()
	if !countsOK || (vmOn && !b.vmOK) {
		state = StateDegraded
	}
	s.State, s.CheckedAt, s.GeneratedAt = state, now.UnixMilli(), now.UnixMilli()

	pub := *s
	pub.Ring = s.Ring.Clone()
	if b.d.Publish != nil {
		b.d.Publish(&pub)
	}
	b.logRound(state)
}

func (b *Builder) slowRound(ctx context.Context, s *Snapshot, now time.Time) {
	// VM halkası
	if b.d.VM != nil && b.d.VM.Configured() {
		if s.Ring == nil {
			s.Ring = NewRing()
		}
		allowP95 := b.d.VM.AllowUnfilteredPercentiles()
		if !allowP95 {
			s.Ring.DropP95()
		}
		nowSec := now.Unix()
		recentFrom := nowSec - nowSec%ringStepS - int64(vmRecentWindow/time.Second)
		q1ok := b.fetch(ctx, s.Ring, time.Unix(recentFrom, 0), now, allowP95, now)
		b.vmOK = q1ok
		if q1ok {
			for _, c := range s.Ring.MissingChunks(nowSec, recentFrom, vmChunksPerRound) {
				if b.fetch(ctx, s.Ring, time.Unix(c, 0), time.Unix(c+ringChunkS, 0), allowP95, now) {
					s.Ring.MarkFilled(c)
				}
			}
		}
		s.Ring.Trim(nowSec)
	} else {
		s.Ring = nil
		b.vmOK = false
	}

	// topoloji
	t0 := time.Now()
	edges, err := b.d.Store.ReadServiceTopologyAgg(ctx, now.Add(-topoWindowS*time.Second), now, topoEdgeLimit)
	b.timed("topology", t0)
	if err == nil {
		s.Topo = BuildTopology(PickSilhouette(edges, topoMaxNodes), s.Salt)
	}

	// CH boyutu
	t0 = time.Now()
	n, berr := b.d.Store.TotalUncompressedBytes(ctx)
	b.timed("bytes", t0)
	if berr == nil {
		s.CHBytes = &n
	}
}

// fetch — bir pencere için Q1 (+ Q2, Q3) çalıştırıp halkaya birleştirir.
// q1ok: Q1 hatasız döndü (boş sonuç da başarıdır: aile yok → kart gizlenir).
func (b *Builder) fetch(ctx context.Context, ring *Ring, from, to time.Time, p95 bool, now time.Time) bool {
	q1, q2, q3 := Queries(b.d.Family)
	s1, ok := b.runQuery(ctx, "q1", q1, from, to, now)
	if !ok {
		return false
	}
	if len(s1) == 0 {
		return true
	}
	ring.MergeStatus(s1)
	if s2, ok := b.runQuery(ctx, "q2", q2, from, to, now); ok {
		ring.MergeSum(s2)
	}
	if p95 {
		if s3, ok := b.runQuery(ctx, "q3", q3, from, to, now); ok {
			ring.MergeP95(s3)
		}
	}
	return true
}

func (b *Builder) runQuery(ctx context.Context, name, query string, from, to, now time.Time) ([]chstore.SpanMetricSeries, bool) {
	br := b.br[name]
	if !br.allow(now) {
		return nil, false
	}
	qctx, cancel := context.WithTimeout(ctx, vmQueryTimeout)
	defer cancel()
	t0 := time.Now()
	res, err := b.d.VM.QueryPromQLRange(qctx, query, from, to, ringStepS, 0)
	b.timed("vm_"+name, t0)
	br.record(now, err)
	if err != nil {
		return nil, false
	}
	return res, true
}

func (b *Builder) logRound(state string) {
	keys := make([]string, 0, len(b.timings))
	for k := range b.timings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&sb, " %s=%dms", k, b.timings[k].Milliseconds())
	}
	b.d.Logf("[login-summary] state=%s%s", state, sb.String())
}
