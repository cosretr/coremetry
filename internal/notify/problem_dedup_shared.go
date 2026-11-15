package notify

import (
	"context"
	"hash/fnv"
	"strconv"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
)

// v0.10.2231 (Redis R5, audit O5) — bildirim tekrar tabanının filo geneli
// yarısı.
//
// Süreç içi dedupSeen (problem_dedup.go) KURALIN TAMAMINI uygulamaya devam
// eder: iki katman, eskalasyon deliği, bastırma sayacı, log uçları — hiçbiri
// değişmedi. Yerel kapı "gönder" dediğinde, gönderimden önce paylaşımlı
// kapı sorulur: aynı (problem, kanal, durum, started_at) bu cooldown'da
// filoda bu ya da daha YÜKSEK ciddiyet basamağıyla zaten gönderildi mi?
//   - MGET rank..3 basamak anahtarlarından biri varsa → bastır (başka pod
//     gönderdi ya da gerçek bir yükselme değil — yerel kuralın aynısı);
//   - yoksa SetNX <durum>:r<basamak> (TTL = notifySameStateCooldown) →
//     kazanan gönderir, kaybeden bastırır (lider devri, çok liderli
//     pencere, worker + sre aynı anda).
//
// Çözülme (forgetProblem) problemin nesil sayacını INCR eder; anahtarlar
// nesli taşır, yeniden açılış eski damgalara takılmaz.
//
// Redis yok / Noop / herhangi bir hata → GÖNDER (K1, B sınıfı: alarm asla
// kaybolmaz, en kötü durumda bugünkü gibi çift bildirim).

const (
	notifySharedPrefix = "coremetry:notify:dedup:v1:"
	notifySharedGenPfx = "coremetry:notify:gen:v1:"
	// notifySharedTimeout — alarm yolunu Redis için bundan fazla bekletme.
	notifySharedTimeout = 300 * time.Millisecond
	notifyMaxRank       = 3
)

// SetDedupCache — paylaşımlı tekrar tabanını bağlar (main.go; Switchable).
// nil / Noop = yalnız süreç içi taban (bugünkü davranış).
func (n *Notifier) SetDedupCache(c cache.Cache) { n.dedupCache = c }

func notifyHash(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return strconv.FormatUint(h.Sum64(), 16)
}

// sharedAllow — paylaşımlı kapı; yalnız yerel kapı izin verdiyse çağrılır.
func (n *Notifier) sharedAllow(problemID, stateKey string, rank int) bool {
	c := n.dedupCache
	if c == nil || cache.IsNoop(c) {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), notifySharedTimeout)
	defer cancel()
	gen, _, err := c.Get(ctx, notifySharedGenPfx+notifyHash(problemID))
	if err != nil {
		return true
	}
	base := notifySharedPrefix + notifyHash(stateKey) + ":g" + string(gen) + ":r"
	if rank < 0 {
		rank = 0
	}
	if rank <= notifyMaxRank {
		keys := make([]string, 0, notifyMaxRank-rank+1)
		for r := rank; r <= notifyMaxRank; r++ {
			keys = append(keys, base+strconv.Itoa(r))
		}
		vals, err := c.MGet(ctx, keys)
		if err != nil {
			return true
		}
		for _, v := range vals {
			if len(v) > 0 {
				return false
			}
		}
	}
	won, err := c.SetNX(ctx, base+strconv.Itoa(rank), []byte("1"), notifySameStateCooldown)
	return err != nil || won
}

// sharedForget — çözülmede problemin paylaşımlı damgalarını geçersiz kılar.
func (n *Notifier) sharedForget(problemID string) {
	c := n.dedupCache
	if c == nil || cache.IsNoop(c) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), notifySharedTimeout)
	defer cancel()
	k := notifySharedGenPfx + notifyHash(problemID)
	// İlk INCR (1 = anahtar yoktu) milisaniyeye taşınır: sayaç TTL'le düşüp
	// yeniden kurulsa da eski bir nesil değeri geri gelmez (api R3 emsali).
	if v, err := c.Incr(ctx, k, 1, 2*notifySameStateCooldown); err == nil && v == 1 {
		_, _ = c.Incr(ctx, k, time.Now().UnixMilli(), 0)
	}
}
