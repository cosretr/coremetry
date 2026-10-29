package api

// chat_source_rate.go — v0.10.1563 (kuyruk: "git sunucusu için kullanıcı
// başına hız sınırı yok"). Sohbetin kod araçları (read_source_code,
// search_code) her çağrıda Azure DevOps'a gider; bir model döngüsü ya da
// aynı kullanıcının açık birkaç sekmesi git sunucusunu dakikada yüzlerce
// istekle doldurabiliyordu. Sınır KULLANICI başına, sabit dakika penceresi:
//
//   read_source_code  sourceReadsPerMin   (dosya okuması)
//   search_code       sourceSearchesPerMin (kod araması)
//
// Sayım yalnız git'e GERÇEKTEN gidecek çağrıda (depo çözüldükten sonra):
// kapsam reddi, çözülemeyen depo ya da katalog hatası kotayı yemez. Sınır
// dolunca araç DevOps'a gitmeden rate_limited döner ve model bunu okur
// (yeniden deneme süresiyle) — sessiz boş sonuç değil.
//
// Kimliksiz bağlam sayılmaz (sourceRateAllow) — prod'da ulaşılamaz.
//
// Pod-yerel (promql_console_guard.go promqlLimiter ile aynı duruş, karar 3):
// küme-geneli sayaç cache.Cache'te INCR olmadığı için v2 işi. N pod'da üst
// sınır N × limit; amaç tek kullanıcının kaçak döngüsünü kesmek.

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
)

const (
	sourceReadsPerMin    = 30
	sourceSearchesPerMin = 15
	sourceRateWindow     = time.Minute
)

// sourceRateKind — sayılan çağrı türü.
type sourceRateKind int

const (
	sourceRateRead sourceRateKind = iota
	sourceRateSearch
)

func (k sourceRateKind) limit() int {
	if k == sourceRateSearch {
		return sourceSearchesPerMin
	}
	return sourceReadsPerMin
}

type sourceRateLimiter struct {
	now func() time.Time

	mu    sync.Mutex
	win   int64                             // geçerli pencere başı (unix sn)
	count map[sourceRateKind]map[string]int // tür → kullanıcı → sayaç
}

func newSourceRateLimiter(now func() time.Time) *sourceRateLimiter {
	if now == nil {
		now = time.Now
	}
	return &sourceRateLimiter{now: now, count: map[sourceRateKind]map[string]int{}}
}

// sourceRate — süreç-genelinde tek limiter.
var sourceRate = newSourceRateLimiter(time.Now)

// allow — kullanıcının bu türde bir çağrı hakkı var mı; varsa TÜKETİR.
// Yoksa pencerenin bitişine kalan süre (≥1 sn). Pencere değişince tüm
// sayaçlar atılır (harita boyu = son dakikadaki kullanıcı sayısı).
func (l *sourceRateLimiter) allow(user string, k sourceRateKind) (bool, time.Duration) {
	now := l.now()
	sec := now.Unix()
	w := int64(sourceRateWindow / time.Second)
	start := sec - sec%w
	l.mu.Lock()
	defer l.mu.Unlock()
	if start != l.win {
		l.win = start
		l.count = map[sourceRateKind]map[string]int{}
	}
	m := l.count[k]
	if m == nil {
		m = map[string]int{}
		l.count[k] = m
	}
	if m[user] >= k.limit() {
		retry := time.Unix(start+w, 0).Sub(now)
		if retry < time.Second {
			retry = time.Second
		}
		return false, retry.Round(time.Second)
	}
	m[user]++
	return true, 0
}

// sourceRateUser — ctx'teki oturum kullanıcısı; "" = kimliksiz bağlam.
// Araç yalnız oturum kullanıcısına sunulur (sourceCodeCallerAllowed) ve döngü
// isteğin bağlamında koşar, yani prod'da "" gelmez; gelirse (iç test/araç
// çağrısı) sayılmaz — ortak bir kova birbirinden bağımsız çağıranları
// birbirine kilitlerdi.
func sourceRateUser(ctx context.Context) string {
	if c := auth.FromContext(ctx); c != nil {
		return strings.TrimSpace(c.UserID)
	}
	return ""
}

// sourceRateAllow — sınır kapısı: kimliksiz bağlam sayılmaz.
func sourceRateAllow(ctx context.Context, k sourceRateKind) (bool, time.Duration) {
	user := sourceRateUser(ctx)
	if user == "" {
		return true, 0
	}
	return sourceRate.allow(user, k)
}

// sourceRateReason — modele ve sohbet çipine giden gerekçe.
func sourceRateReason(k sourceRateKind, retry time.Duration) string {
	what := "kod okuma"
	if k == sourceRateSearch {
		what = "kod arama"
	}
	return what + " sınırı doldu (kullanıcı başına dakikada " + strconv.Itoa(k.limit()) + ") — " +
		strconv.Itoa(int(retry/time.Second)) + " sn sonra yeniden deneyin; git sunucusuna istek gönderilmedi"
}
