package ml

// vm_budget.go — VictoriaMetrics bütçesi (v0.10.1764; prod gözlemi 01.11.2026:
// `histogram_quantile(... by (service, server_address, le) ...)` 20 dk
// query_range tüm servisler için → HTTP 422 "maxSamplesPerQuery=1e9", sonra
// her tikte Client.Timeout; metric_http_client aynı histogramı sorguluyordu).
//   - Ham bucket'lar tüm servisler için tek seferde SORULMAZ: önce servis
//     başına topk hedef (count serisi, ucuz), sonra p95 servis grupları
//     halinde ve yalnız seçili hedeflerle (cfg.VMTargetCardinality,
//     cfg.VMServiceBatch).
//   - query_range yerine yalnız son kova için instant sorgu; dwell geçmişi
//     CH ml_series'ten (mergeHistory).
//   - 422 / zaman aşımı alan aile artan backoff'a girer (5 dk × 2ⁿ, en çok
//     1 sa); beklemedeki aileye sorgu HİÇ gönderilmez; durum tik blobunda
//     (karne "VM bekleme").

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	vmBackoffBase = 5 * time.Minute
	vmBackoffMax  = time.Hour
)

type backoffEntry struct {
	until    time.Time
	failures int
	lastErr  string
}

// vmBackoff — aile → bekleme durumu (süreç-içi; blobla karneye çıkar).
type vmBackoff struct {
	mu sync.Mutex
	m  map[string]*backoffEntry
}

func newVMBackoff() *vmBackoff { return &vmBackoff{m: map[string]*backoffEntry{}} }

// heavyVMError — SAF: 422 (maxSamplesPerQuery vb.) ya da zaman aşımı → backoff.
// Ağ/5xx hatası backoff'a girmez (sonraki tik yeniden dener).
func heavyVMError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, k := range []string{"422", "maxsamplesperquery", "client.timeout", "deadline exceeded", "timeout"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// backoffFor — SAF: n. başarısızlığın bekleme süresi.
func backoffFor(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	d := vmBackoffBase * time.Duration(math.Pow(2, float64(failures-1)))
	if d > vmBackoffMax || d <= 0 {
		return vmBackoffMax
	}
	return d
}

func (b *vmBackoff) skipping(family string, now time.Time) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.m[family]
	return e != nil && now.Before(e.until)
}

func (b *vmBackoff) fail(family string, err error, now time.Time) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.m[family]
	if e == nil {
		e = &backoffEntry{}
		b.m[family] = e
	}
	e.failures++
	d := backoffFor(e.failures)
	e.until, e.lastErr = now.Add(d), truncRunes(err.Error(), 200)
	return d
}

func (b *vmBackoff) ok(family string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	delete(b.m, family)
	b.mu.Unlock()
}

// snapshot — beklemedeki aileler (karne).
func (b *vmBackoff) snapshot(now time.Time) []chstore.MLBackoffEntry {
	out := []chstore.MLBackoffEntry{}
	if b == nil {
		return out
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for fam, e := range b.m {
		if now.Before(e.until) {
			out = append(out, chstore.MLBackoffEntry{Family: fam, Until: e.until.Unix(), Failures: e.failures, LastError: e.lastErr})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Family < out[j].Family })
	return out
}

// vmInstant — backoff'lu anlık sorgu. skipped=true: aile beklemede, VM'e
// gidilmedi. Ağır hata → aile beklemeye; başarı → bekleme sıfırlanır.
func vmInstant(ctx context.Context, deps Deps, family, expr string, at, now time.Time) (rows []chstore.SpanMetricSeries, skipped bool, err error) {
	if deps.Backoff.skipping(family, now) {
		return nil, true, nil
	}
	t0 := time.Now()
	rows, err = deps.VM.QueryPromQLInstant(ctx, expr, at)
	if deps.ObserveVM != nil {
		deps.ObserveVM(time.Since(t0))
	}
	if err != nil {
		if deps.Backoff != nil && heavyVMError(err) {
			d := deps.Backoff.fail(family, err, now)
			return nil, false, fmt.Errorf("%w (aile %s %s beklemeye alındı)", err, family, d.Round(time.Minute))
		}
		return nil, false, err
	}
	deps.Backoff.ok(family)
	return rows, false, nil
}

// instantValue — SAF: anlık sonucun değeri (sonlu değilse ok=false).
func instantValue(r chstore.SpanMetricSeries) (float64, bool) {
	if len(r.Points) == 0 {
		return 0, false
	}
	v := r.Points[len(r.Points)-1].Value
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// mergeHistory — SAF: CH'deki önceki kovaları bu tikin serilerine ekler;
// yalnız bu tikte görülen anahtarlar, yalnız [from, before) ve yalnız eksik
// kovalar (eksik kalan = eksik veri, sıfır değil).
func mergeHistory(series map[string]map[int64]float64, hist map[string][]chstore.MLPoint, from, before int64) {
	for k, byB := range series {
		for _, p := range hist[k] {
			if p.T >= from && p.T < before {
				if _, ok := byB[p.T]; !ok {
					byB[p.T] = p.V
				}
			}
		}
	}
}

// svcBatch — bir servis grubunun p95 sorgusu için etiket regex'leri ve
// izinli (servis → hedef) kümesi.
type svcBatch struct {
	svcRe, tgtRe string
	allowed      map[string]map[string]bool
}

// svcBatches — SAF: servisler sıralı, n'li gruplar; grup regex'i QuoteMeta.
func svcBatches(bySvc map[string][]string, n int) []svcBatch {
	if n < 1 {
		n = 1
	}
	svcs := make([]string, 0, len(bySvc))
	for s := range bySvc {
		svcs = append(svcs, s)
	}
	sort.Strings(svcs)
	var out []svcBatch
	for i := 0; i < len(svcs); i += n {
		chunk := svcs[i:min(i+n, len(svcs))]
		b := svcBatch{allowed: map[string]map[string]bool{}}
		var sre, tre []string
		tseen := map[string]bool{}
		for _, s := range chunk {
			sre = append(sre, regexp.QuoteMeta(s))
			b.allowed[s] = map[string]bool{}
			for _, t := range bySvc[s] {
				b.allowed[s][t] = true
				if !tseen[t] {
					tseen[t] = true
					tre = append(tre, regexp.QuoteMeta(t))
				}
			}
		}
		sort.Strings(tre)
		b.svcRe, b.tgtRe = strings.Join(sre, "|"), strings.Join(tre, "|")
		out = append(out, b)
	}
	return out
}

// plannedExpr — metrik ailesinin bir sorgusu (SAF plan; test "kapalı aile
// sorgu göndermez" buradan).
type plannedExpr struct {
	family, id, expr, direction string
}

// metricExprs — SAF: yalnız açık ailelerin ifadeleri.
func metricExprs(cfg chstore.MLConfig, svcLabel string, winSec int) []plannedExpr {
	var out []plannedExpr
	for _, fam := range chstore.MLMetricDetectorNames {
		if !cfg.DetectorOn(fam) {
			continue
		}
		for _, e := range cfg.MetricFamilies[fam] {
			out = append(out, plannedExpr{family: fam, id: e.ID, expr: renderExpr(e.Expr, svcLabel, winSec), direction: e.Direction})
		}
	}
	return out
}
