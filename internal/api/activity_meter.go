package api

// activity_meter.go — v0.10.1902 (login özeti, docs/frontend/login-redesign.md §5.2).
//
// Anonim login özetinin "son 24 saatte" satırındaki giriş adedi. Self-obs
// span'lerinden türetilemez (kök örnekleme %10; başarılı/başarısız giriş
// ayrımı span'de yok), bu yüzden olay burada pod-yerel sayılır ve ES istek
// sayacının (es_request_meter.go) eşi biçimde 30 sn'de bir kalıcı seriye
// yazılır (chstore/self_activity_series.go). Okuma (özet kurucusu) tüm
// pod'ların toplamını okur.
//
//   - noteLogin: iki başarılı giriş yolunun (yerel+LDAP, OIDC) ortak noktası;
//     son-login damgasını basar ve sayacı artırır. Damga hatası girişi asla
//     bloklamaz.
//
// v0.10.1915 — "trace görüntüleme" sayacı KALDIRILDI (operatör: satırda
// yazmasına gerek yok): tekilleştirme tablosu, `X-Coremetry-Purpose` başlığı
// ve getTrace çağrısı da silindi. Sayaç yalnız ADET taşır.

import (
	"context"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	activityFlushEvery = 30 * time.Second
	// activityMaxMinutes — yazıcı koşmuyorsa en eski dakikalar atılır.
	activityMaxMinutes = 180
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
	if len(c.cells) <= activityMaxMinutes { // tek tür: hücre sayısı = dakika sayısı
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

// ── süreç geneli örnekler + çağrı noktaları ─────────────────────────────

var activityStats = newActivityCounter()

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

// ── yazıcı ──────────────────────────────────────────────────────────────

// StartActivityMeter — dakikalık giriş hücrelerini kalıcı
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
