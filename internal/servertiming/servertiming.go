// Package servertiming — W3C `Server-Timing` yanıt başlığı için istek-başı
// süre kaydedici (v0.10.1817, Log Search Faz 1 dilim 1.1; audit §5).
//
// Neden: /api/logs* uçlarında "yavaş" şikâyetinin iki yarısı var — arka uç
// (Elasticsearch) mı, yoksa Coremetry'nin kendisi mi. X-Cache (cache.go)
// yalnız katmanı söyler; bu paket ES'te geçen süreyi aynı yanıtın
// başlığına yazar, tarayıcının Network › Timing sekmesi doğrudan gösterir.
//
// Kapsam bilinçli dar: kaydedici YALNIZ çağıran uç onu bağlama koyduğunda
// vardır (api.withServerTiming). Kaydedicisiz bağlamda Add no-op'tur —
// diğer uçlar ve arka-plan işleri (SWR tazelemesi, worker) hiçbir şey
// ödemez, başlık da basılmaz.
//
// Bağımlılığı yok: logstore (ES transport) ve api ikisi de import eder.
package servertiming

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type ctxKey struct{}

// Recorder — bir isteğin metrik toplamları. Eşzamanlı Add güvenli (ES
// histogramı ve sayımı paralel koşabilir).
type Recorder struct {
	mu  sync.Mutex
	dur map[string]time.Duration
	n   map[string]int
}

// With — ctx'e taze bir kaydedici bağlar.
func With(ctx context.Context) (context.Context, *Recorder) {
	r := &Recorder{dur: map[string]time.Duration{}, n: map[string]int{}}
	return context.WithValue(ctx, ctxKey{}, r), r
}

// From — ctx'teki kaydedici; yoksa nil.
func From(ctx context.Context) *Recorder {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value(ctxKey{}).(*Recorder)
	return r
}

// Add — ctx'te kaydedici varsa name metriğine d ekler; yoksa no-op.
func Add(ctx context.Context, name string, d time.Duration) {
	r := From(ctx)
	if r == nil {
		return
	}
	r.mu.Lock()
	r.dur[name] += d
	r.n[name]++
	r.mu.Unlock()
}

// Header — SAF biçimleyici: `cache;desc="MISS", es;dur=12.3;desc="2 req", total;dur=15.0`.
// Metrikler ada göre sıralı (deterministik, tablo testli); total en sonda.
// tier boşsa cache girdisi yazılmaz. dur milisaniye, bir ondalık.
func (r *Recorder) Header(total time.Duration, tier string) string {
	var parts []string
	if tier != "" {
		parts = append(parts, fmt.Sprintf("cache;desc=%q", tier))
	}
	if r != nil {
		r.mu.Lock()
		names := make([]string, 0, len(r.dur))
		for k := range r.dur {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			p := fmt.Sprintf("%s;dur=%s", k, ms(r.dur[k]))
			if c := r.n[k]; c > 1 {
				p += fmt.Sprintf(";desc=\"%d req\"", c)
			}
			parts = append(parts, p)
		}
		r.mu.Unlock()
	}
	parts = append(parts, "total;dur="+ms(total))
	return strings.Join(parts, ", ")
}

func ms(d time.Duration) string {
	return fmt.Sprintf("%.1f", float64(d.Microseconds())/1000)
}

// Transport — base'i saran RoundTripper: her isteğin yanıt başlıklarına
// kadar geçen süresi isteğin bağlamındaki kaydediciye `name` olarak eklenir.
// ES gövdeyi arama bittikten sonra gönderdiği için başlık anı ≈ ES süresi.
// Kaydedicisiz bağlamda yalnız base çağrılır.
func Transport(name string, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &timingRT{name: name, base: base}
}

type timingRT struct {
	name string
	base http.RoundTripper
}

func (t *timingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if From(req.Context()) == nil {
		return t.base.RoundTrip(req)
	}
	start := time.Now()
	res, err := t.base.RoundTrip(req)
	Add(req.Context(), t.name, time.Since(start))
	return res, err
}
