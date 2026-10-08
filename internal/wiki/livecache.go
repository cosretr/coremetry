package wiki

import (
	"container/list"
	"strings"
	"sync"
	"time"
)

// livecache.go — v0.10.1124 canlı mod (Config.Mode = "live") sayfa önbelleği.
//
// Canlı modda senkron YOK ve okunan sayfalar ClickHouse'a YAZILMAZ; aynı
// sayfanın art arda gelen sorularda yeniden API'den okunmaması için süreç-içi,
// boyut ve süre sınırlı bir LRU tutulur. Sınırlar: en çok liveCacheMax sayfa,
// toplam içerik liveCacheBytes, kayıt ömrü liveCacheTTL. İçerik hiçbir log
// satırına yazılmaz; pod yeniden başlayınca önbellek boşalır (kalıcılık yok —
// bilinçli: canlı modu seçen operatör içeriğin Coremetry'de saklanmasını
// istemiyor ya da senkron maliyetini ödemek istemiyor).

const (
	liveCacheMax   = 64
	liveCacheBytes = 16 << 20
	liveCacheTTL   = 10 * time.Minute
)

type liveEntry struct {
	key    string
	rec    PageRecord
	chunks []ChunkRecord
	at     time.Time
	size   int
}

// liveCache — eşzamanlılığa dayanıklı LRU (en yeni ön tarafta).
type liveCache struct {
	mu    sync.Mutex
	ll    *list.List
	items map[string]*list.Element
	bytes int
	max   int
	cap   int
	ttl   time.Duration
}

func newLiveCache() *liveCache {
	return &liveCache{ll: list.New(), items: map[string]*list.Element{}, max: liveCacheMax, cap: liveCacheBytes, ttl: liveCacheTTL}
}

// liveKey — sayfa kimliği; wiki adı ya da id'si ile aranabilsin diye katlanmış,
// yol NormalizePagePath'ten geçer (put ve get aynı biçimi görür: "Runbooks/A",
// "/Runbooks/A/", "/Runbooks/A.md" aynı kayıt).
func liveKey(project, wiki, path string) string {
	if n := NormalizePagePath(path); n != "" {
		path = n
	}
	return strings.ToLower(strings.TrimSpace(project)) + "\x00" + strings.ToLower(strings.TrimSpace(wiki)) + "\x00" + path
}

func (c *liveCache) get(key string, now time.Time) (PageRecord, []ChunkRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return PageRecord{}, nil, false
	}
	e := el.Value.(*liveEntry)
	if now.Sub(e.at) > c.ttl {
		c.removeEl(el)
		return PageRecord{}, nil, false
	}
	c.ll.MoveToFront(el)
	return e.rec, e.chunks, true
}

// put — kaydı hem wiki id'si hem adıyla tutar (iki anahtar, tek içerik).
func (c *liveCache) put(rec PageRecord, chunks []ChunkRecord, now time.Time) {
	size := len(rec.Content)
	for _, ch := range chunks {
		size += len(ch.Text)
	}
	if size > c.cap {
		return
	}
	for _, key := range []string{liveKey(rec.Project, rec.WikiID, rec.Path), liveKey(rec.Project, rec.WikiName, rec.Path)} {
		c.mu.Lock()
		if el, ok := c.items[key]; ok {
			c.removeEl(el)
		}
		el := c.ll.PushFront(&liveEntry{key: key, rec: rec, chunks: chunks, at: now, size: size})
		c.items[key] = el
		c.bytes += size
		for c.ll.Len() > c.max || c.bytes > c.cap {
			c.removeEl(c.ll.Back())
		}
		c.mu.Unlock()
	}
}

func (c *liveCache) removeEl(el *list.Element) {
	if el == nil {
		return
	}
	e := el.Value.(*liveEntry)
	c.ll.Remove(el)
	delete(c.items, e.key)
	c.bytes -= e.size
}

func (c *liveCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
