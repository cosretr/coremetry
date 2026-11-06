package api

// activity_meter.go — v0.10.1902 (login özeti, docs/frontend/login-redesign.md §5.2).
//
// Anonim login özetinin "son 24 saatte" satırı iki sayıyı ister: giriş adedi
// ve görüntülenen trace adedi. Self-obs span'lerinden türetilemez (kök
// örnekleme %10; başarılı/başarısız giriş ayrımı ve kullanıcı kimliği span'de
// yok), bu yüzden iki olay burada pod-yerel sayılır ve ES istek sayacının
// (es_request_meter.go) eşi biçimde 30 sn'de bir kalıcı seriye yazılır
// (chstore/self_activity_series.go). Okuma (özet kurucusu) tüm pod'ların
// toplamını okur.
//
//   - noteLogin: iki başarılı giriş yolunun (yerel+LDAP, OIDC) ortak noktası;
//     son-login damgasını basar ve sayacı artırır. Damga hatası girişi asla
//     bloklamaz.
//   - noteTraceView: GET /api/traces/{id}. `X-Coremetry-Purpose: prefetch`
//     başlığı (Traces listesi hover ön-yüklemesi) varsa SAYILMAZ. Aynı
//     (kullanıcı, trace) çifti 10 dk içinde bir kez sayılır; tekilleştirme
//     tablosu pod-yereldir ve en çok 20 000 girdi taşır (dolunca en eski girdi
//     atılır; bellek büyümez). Aynı kullanıcı 10 dk içinde iki pod'a düşerse
//     çift sayılabilir — bilinen, kabul edilmiş hata payı.
//
// Sayaç yalnız ADET taşır: kullanıcı kimliği yalnız tekilleştirme anahtarında,
// bellekte durur ve hiçbir yere yazılmaz.

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	activityFlushEvery = 30 * time.Second
	// activityMaxMinutes — yazıcı koşmuyorsa en eski dakikalar atılır.
	activityMaxMinutes = 180

	traceViewDedupWindow = 10 * time.Minute
	traceViewDedupMax    = 20000

	// purposeHeader — istemcinin "bu istek kullanıcı niyeti değil" bildirimi.
	purposeHeader   = "X-Coremetry-Purpose"
	purposePrefetch = "prefetch"
)

// ── dakikalık sayaç ─────────────────────────────────────────────────────

type activityKey struct {
	minute int64 // unix dakika başı
	kind   string
}

type activityCounter struct {
	mu    sync.Mutex
	cells map[activityKey]uint64
}

func newActivityCounter() *activityCounter {
	return &activityCounter{cells: map[activityKey]uint64{}}
}

func (c *activityCounter) add(now time.Time, kind string) {
	minute := now.Truncate(time.Minute).Unix()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cells[activityKey{minute: minute, kind: kind}]++
	c.trimLocked()
}

// trimLocked — boşaltılmayan sayaçta en eski dakikaları atar.
func (c *activityCounter) trimLocked() {
	if len(c.cells) <= activityMaxMinutes*2 { // 2 tür × 180 dk
		return
	}
	mins := map[int64]struct{}{}
	for k := range c.cells {
		mins[k.minute] = struct{}{}
	}
	if len(mins) <= activityMaxMinutes {
		return
	}
	order := make([]int64, 0, len(mins))
	for m := range mins {
		order = append(order, m)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	drop := map[int64]bool{}
	for _, m := range order[:len(order)-activityMaxMinutes] {
		drop[m] = true
	}
	for k := range c.cells {
		if drop[k.minute] {
			delete(c.cells, k)
		}
	}
}

// drain — `before`un dakika başından ÖNCE tamamlanmış hücreleri döndürür ve
// siler (içinde bulunulan dakika kalır).
func (c *activityCounter) drain(before time.Time) []chstore.SelfActivityCell {
	cut := before.Truncate(time.Minute).Unix()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []chstore.SelfActivityCell
	for k, n := range c.cells {
		if k.minute < cut {
			out = append(out, chstore.SelfActivityCell{Minute: time.Unix(k.minute, 0).UTC(), Kind: k.kind, Count: n})
			delete(c.cells, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Minute.Equal(out[j].Minute) {
			return out[i].Minute.Before(out[j].Minute)
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// ── (kullanıcı, trace) tekilleştirme ────────────────────────────────────

type dedupEntry struct {
	key string
	at  time.Time
}

// traceViewDedup — pencere içinde aynı anahtarı bir kez geçirir. Ekleme
// sırası = süre sırası olduğundan FIFO kuyruk süresi dolanları baştan atar;
// tavan dolunca en eski girdi atılır.
type traceViewDedup struct {
	mu     sync.Mutex
	window time.Duration
	max    int
	seen   map[string]time.Time
	queue  []dedupEntry
	head   int
}

func newTraceViewDedup(window time.Duration, max int) *traceViewDedup {
	return &traceViewDedup{window: window, max: max, seen: map[string]time.Time{}}
}

// firstSeen — anahtar pencere içinde ilk kez görülüyorsa true (ve kaydeder).
func (d *traceViewDedup) firstSeen(key string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.expireLocked(now)
	if _, ok := d.seen[key]; ok {
		return false
	}
	for len(d.seen) >= d.max && d.head < len(d.queue) {
		old := d.queue[d.head]
		d.head++
		if at, ok := d.seen[old.key]; ok && at.Equal(old.at) {
			delete(d.seen, old.key)
		}
	}
	d.seen[key] = now
	d.queue = append(d.queue, dedupEntry{key: key, at: now})
	d.compactLocked()
	return true
}

func (d *traceViewDedup) expireLocked(now time.Time) {
	for d.head < len(d.queue) {
		e := d.queue[d.head]
		if now.Sub(e.at) < d.window {
			break
		}
		d.head++
		if at, ok := d.seen[e.key]; ok && at.Equal(e.at) {
			delete(d.seen, e.key)
		}
	}
	d.compactLocked()
}

// compactLocked — kuyruğun tüketilmiş önekini ara sıra atar (bellek sınırı).
func (d *traceViewDedup) compactLocked() {
	if d.head > 1024 && d.head*2 > len(d.queue) {
		d.queue = append([]dedupEntry(nil), d.queue[d.head:]...)
		d.head = 0
	}
}

func (d *traceViewDedup) size() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}

// ── süreç geneli örnekler + çağrı noktaları ─────────────────────────────

var (
	activityStats = newActivityCounter()
	traceViewSeen = newTraceViewDedup(traceViewDedupWindow, traceViewDedupMax)
)

// noteLogin — başarılı girişte son-login damgası (v0.8.450; başarısız damga
// geçerli girişi asla bloklamaz) + giriş sayacı. tag: günlük öneki
// ("auth" / "oidc"). api.go'daki iki giriş yolunun TEK satırı.
func (s *Server) noteLogin(r *http.Request, userID, email, tag string) {
	if s.store != nil {
		if err := s.store.TouchUserLogin(r.Context(), userID); err != nil {
			log.Printf("[%s] last-login stamp failed for %q: %v", tag, email, err)
		}
	}
	activityStats.add(time.Now(), chstore.ActivityKindLogin)
}

// isPrefetchRequest — SAF: istek "ön-yükleme" mi.
func isPrefetchRequest(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get(purposeHeader)), purposePrefetch)
}

// noteTraceView — GET /api/traces/{id} girişinde: ön-yükleme değilse ve
// (kullanıcı, trace) çifti 10 dk içinde ilk kez görülüyorsa sayar.
func (s *Server) noteTraceView(r *http.Request, traceID string) {
	recordTraceView(activityStats, traceViewSeen, r, traceID, time.Now())
}

// recordTraceView — SAF çekirdek (örnekler ve saat enjekte edilir).
func recordTraceView(c *activityCounter, d *traceViewDedup, r *http.Request, traceID string, now time.Time) bool {
	if traceID == "" || isPrefetchRequest(r) {
		return false
	}
	user := "anon"
	if cl := auth.FromContext(r.Context()); cl != nil && cl.UserID != "" {
		user = cl.UserID
	}
	if !d.firstSeen(user+"\x00"+traceID, now) {
		return false
	}
	c.add(now, chstore.ActivityKindTraceView)
	return true
}

// ── yazıcı ──────────────────────────────────────────────────────────────

// StartActivityMeter — dakikalık giriş / trace görüntüleme hücrelerini kalıcı
// seriye yazar; ctx bitene dek koşar. Her rolde çağrılır (olayı gören pod
// sayar). Yazılamayan hücreler ATILIR (birikmez).
func StartActivityMeter(ctx context.Context, sink esMetricSink, pod string) {
	if sink == nil {
		return
	}
	ctx = chstore.WithQueryTag(ctx, "worker:activity-meter")
	tk := time.NewTicker(activityFlushEvery)
	defer tk.Stop()
	var lastErrLog time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tk.C:
			if err := flushActivityStats(ctx, sink, activityStats, pod, now); err != nil && time.Since(lastErrLog) > 5*time.Minute {
				lastErrLog = time.Now()
				log.Printf("[activity-meter] sayaç yazılamadı (hücreler atıldı): %v", err)
			}
		}
	}
}

// flushActivityStats — tamamlanmış dakikaları boşaltıp yazar.
func flushActivityStats(ctx context.Context, sink esMetricSink, c *activityCounter, pod string, now time.Time) error {
	cells := c.drain(now)
	if len(cells) == 0 {
		return nil
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return sink.InsertMetrics(wctx, chstore.SelfActivityPoints(cells, pod))
}
