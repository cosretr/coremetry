package logstore

// es_meter.go — v0.10.1878 (operatör, 2026-11-06 ES aşırı yük olayı:
// "Elastic'e giden sorgu sayısını takip edeyim, artarsa anlarım; şu an körüm").
//
// Coremetry'nin Elasticsearch'e gönderdiği HER HTTP isteği tek noktada, ES
// istemcisinin taşıma katmanında sayılır: (dakika, işlem türü, yüzey) hücresi
// başına istek sayısı, hata sayısı ve toplam süre. Sayaç süreç genelidir
// (paket düzeyi): ES ayarı değişip depo yeniden kurulsa da sürer.
//
// Yüzey = isteği başlatan yer; chstore.QueryTag ile aynı etiket
// (route:<kalıp> / worker:<ad>). Etiketsiz istek "diğer".
//
// Kardinalite sınırlı: işlem türü kapalı küme (ESOpKind); yüzey dakika başına
// en çok esMeterMaxSurfaces, fazlası "diğer"e katlanır. Hücreler boşaltılmazsa
// (yazıcı koşmuyorsa) en eski dakikalar atılır — bellek büyümez.

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	esMeterMaxSurfaces = 64
	esMeterMaxMinutes  = 180
	esSurfaceOther     = "diğer"
	esSurfaceMaxLen    = 96
)

// ESRequestStat — bir (dakika, işlem, yüzey) hücresi.
type ESRequestStat struct {
	Minute  time.Time
	Op      string
	Surface string
	Count   uint64
	Errors  uint64
	DurMs   float64
}

type esMeterKey struct {
	minute  int64 // unix dakika başı
	op      string
	surface string
}

type esMeter struct {
	mu       sync.Mutex
	cells    map[esMeterKey]*ESRequestStat
	surfaces map[int64]map[string]struct{}
}

var esRequests = &esMeter{cells: map[esMeterKey]*ESRequestStat{}, surfaces: map[int64]map[string]struct{}{}}

// ESOpKind — SAF: istek yolu → işlem türü (kapalı küme).
func ESOpKind(path string) string {
	p := strings.ToLower(path)
	switch {
	case strings.Contains(p, "/_msearch"):
		return "msearch"
	case strings.Contains(p, "/_async_search"):
		return "async_search"
	case strings.Contains(p, "/_pit"), strings.Contains(p, "/point_in_time"):
		return "pit"
	case strings.Contains(p, "/_search"):
		return "search"
	case strings.Contains(p, "/_count"):
		return "count"
	case strings.Contains(p, "/_field_caps"):
		return "field_caps"
	case strings.Contains(p, "/_mapping"):
		return "mapping"
	case strings.Contains(p, "/_sql"):
		return "sql"
	case p == "" || p == "/", strings.HasPrefix(p, "/_cat"), strings.HasPrefix(p, "/_cluster"),
		strings.HasPrefix(p, "/_nodes"), strings.HasPrefix(p, "/_resolve"):
		return "meta"
	}
	return "other"
}

// esSurfaceLabel — SAF: sorgu etiketi → yüzey adı.
func esSurfaceLabel(tag string) string {
	t := strings.TrimSpace(tag)
	if t == "" {
		return esSurfaceOther
	}
	if len(t) > esSurfaceMaxLen {
		t = t[:esSurfaceMaxLen]
	}
	return t
}

func (m *esMeter) record(now time.Time, op, surface string, dur time.Duration, failed bool) {
	minute := now.Truncate(time.Minute).Unix()
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := m.surfaces[minute]
	if seen == nil {
		seen = map[string]struct{}{}
		m.surfaces[minute] = seen
		m.trimLocked()
	}
	if _, ok := seen[surface]; !ok {
		// Tavan "diğer" DAHİL: son yuva ona ayrılır.
		if surface != esSurfaceOther && len(seen) >= esMeterMaxSurfaces-1 {
			surface = esSurfaceOther
		}
		seen[surface] = struct{}{}
	}
	k := esMeterKey{minute: minute, op: op, surface: surface}
	c := m.cells[k]
	if c == nil {
		c = &ESRequestStat{Minute: time.Unix(minute, 0).UTC(), Op: op, Surface: surface}
		m.cells[k] = c
	}
	c.Count++
	if failed {
		c.Errors++
	}
	c.DurMs += float64(dur) / float64(time.Millisecond)
}

// trimLocked — boşaltılmayan sayaçta en eski dakikaları atar.
func (m *esMeter) trimLocked() {
	if len(m.surfaces) <= esMeterMaxMinutes {
		return
	}
	mins := make([]int64, 0, len(m.surfaces))
	for mn := range m.surfaces {
		mins = append(mins, mn)
	}
	sort.Slice(mins, func(i, j int) bool { return mins[i] < mins[j] })
	drop := map[int64]bool{}
	for _, mn := range mins[:len(mins)-esMeterMaxMinutes] {
		drop[mn] = true
		delete(m.surfaces, mn)
	}
	for k := range m.cells {
		if drop[k.minute] {
			delete(m.cells, k)
		}
	}
}

func (m *esMeter) drain(before time.Time) []ESRequestStat {
	cut := before.Truncate(time.Minute).Unix()
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ESRequestStat
	for k, c := range m.cells {
		if k.minute < cut {
			out = append(out, *c)
			delete(m.cells, k)
		}
	}
	for mn := range m.surfaces {
		if mn < cut {
			delete(m.surfaces, mn)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.Minute.Equal(b.Minute) {
			return a.Minute.Before(b.Minute)
		}
		if a.Op != b.Op {
			return a.Op < b.Op
		}
		return a.Surface < b.Surface
	})
	return out
}

// DrainESRequestStats — `before`in dakika başından ÖNCE tamamlanmış hücreleri
// döndürür ve sayaçtan siler (içinde bulunulan dakika kalır).
func DrainESRequestStats(before time.Time) []ESRequestStat { return esRequests.drain(before) }

// esMeterTransport — her ES isteğini sayan taşıma sarmalayıcısı.
func esMeterTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &esMeterRT{base: base, meter: esRequests}
}

type esMeterRT struct {
	base  http.RoundTripper
	meter *esMeter
}

func (t *esMeterRT) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	res, err := t.base.RoundTrip(req)
	// İstemcinin iptal ettiği istek (yeni arama / sayfadan çıkış) SAYILIR ama
	// hata değildir.
	failed := (err != nil && !errors.Is(err, context.Canceled)) || (res != nil && res.StatusCode >= 400)
	t.meter.record(start, ESOpKind(req.URL.Path), esSurfaceLabel(chstore.QueryTag(req.Context())), time.Since(start), failed)
	return res, err
}
