package oracle

import (
	"context"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cespare/xxhash/v2"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// counter.go — Oracle Aşama 3 dilim A (v0.10.893; spec Onay 2026-09-23,
// audit §6.1/6.3): her başarılı poll'dan sonra eşlenmiş satırlar
// (operation.code, error.code, channel.code, error.qualifier) anahtarında
// DAKİKA kovalarına sayılır ve metric_points'e `ext:error_count` gauge'ı
// olarak yazılır — dış tarayıcının (anomaly.ExternalScanner) okuduğu şekil:
// service_name = kaynak adı, GroupBy = attr anahtarları, 60 s adım, max.
//
// DENSE SIFIR (jüri bulgusu): yalnız pencerede görülen anahtar değil, son
// 4 saatte AKTİF olan her anahtar için her dakikaya 0 yazılır — hata
// kesilince sıfır kovaları gelmezse Problem hiç kapanmaz (dış hatta 0 =
// gözlem, external.go). Aktif pencere externalSlots (240 dk) ile aynı.
//
// ANAHTAR TAVANI: kaynak başına ≤ CounterMaxKeys (200). Dış tarayıcının
// QueryMetric'i 240 dk × anahtar satırını LIMIT 50k ile okur (≈208 anahtar);
// üstü sessizce kırpılırdı. Aktif anahtarlar önce (sıfır alabilsinler),
// yeni anahtarlar sayıya göre; taşanlar tek "diğer" serisinde toplanır.
//
// Qualifier bu dilimde hep "-" (jenerik kod ayırt edicisi dilim F).
// Sayaç saftır (BucketRows); yazım Counter.Handle'da, hata poll'u ve
// watermark'ı ETKİLEMEZ (satırlar zaten oracle_error_log'da).

const (
	// CounterQuery — ExternalTarget.Query; metrik = "ext:" + CounterQuery.
	CounterQuery  = "error_count"
	counterMetric = "ext:" + CounterQuery
	// CounterMaxKeys — kaynak başına seri tavanı (bkz. üst yorum).
	CounterMaxKeys = 200
	// counterActiveWindow — bu kadar süredir görülmeyen anahtar sıfır almaz,
	// düşer (dış tarayıcı penceresi 240 dk).
	counterActiveWindow  = 4 * time.Hour
	counterQualifierNone = "-"
	counterOtherCode     = "_other"
)

// CounterGroupBy — ExternalTarget.GroupBy ile birebir aynı sıra (attr adları).
var CounterGroupBy = []string{"operation.code", "error.code", "channel.code", "error.qualifier"}

// CounterKey — bir seri.
type CounterKey struct {
	Op, Code, Channel, Qualifier string
}

func (k CounterKey) values() []string { return []string{k.Op, k.Code, k.Channel, k.Qualifier} }

// counterOtherKey — tavanı aşan yeni anahtarların toplandığı seri.
var counterOtherKey = CounterKey{Op: "-", Code: counterOtherCode, Channel: "-", Qualifier: counterQualifierNone}

// BucketResult — BucketRows çıktısı (sayaç istatistikleri kanıt notuna gider).
type BucketResult struct {
	Points   []*chstore.MetricPoint
	Seen     map[CounterKey]time.Time // pencerede görülen anahtar → son satır zamanı
	Keys     int                      // yazılan seri sayısı
	Overflow int                      // "diğer"e katlanan anahtar sayısı
	Ignored  int                      // ignore listesiyle sayılmayan satır
	Minutes  int
}

// BucketRows — SAF. rows: bir poll'un eşlenmiş satırları; [from, to] poll
// penceresi (dakikaya yuvarlanır, to'nun dakikası da yazılır — overlap'te
// max ile tamamlanır); active: kaynağın aktif anahtarları (sıfır alırlar);
// ignore: sayılmayacak hata kodları; maxKeys: seri tavanı (≤0 → tavan yok).
func BucketRows(source string, rows []chstore.OracleErrorRow, from, to time.Time, active map[CounterKey]time.Time, ignore map[string]bool, maxKeys int) BucketResult {
	res := BucketResult{Seen: map[CounterKey]time.Time{}}
	if source == "" || !to.After(from) && !to.Equal(from) {
		return res
	}
	m0, m1 := from.UTC().Truncate(time.Minute), to.UTC().Truncate(time.Minute)
	counts := map[CounterKey]map[time.Time]float64{}
	for _, r := range rows {
		if ignore[r.ErrorCode] {
			res.Ignored++
			continue
		}
		t := r.Time.UTC().Truncate(time.Minute)
		if t.Before(m0) || t.After(m1) {
			continue // pencere dışı (overlap dışında geç gelen satır — bir sonraki poll)
		}
		k := CounterKey{Op: r.OperationCode, Code: r.ErrorCode, Channel: r.ChannelCode, Qualifier: counterQualifierNone}
		if counts[k] == nil {
			counts[k] = map[time.Time]float64{}
		}
		counts[k][t]++
		if r.Time.After(res.Seen[k]) {
			res.Seen[k] = r.Time
		}
	}
	// Yazılacak anahtar kümesi: aktifler + görülenler; tavan.
	keys := make([]CounterKey, 0, len(active)+len(counts))
	inSet := map[CounterKey]bool{}
	for k := range active {
		if !inSet[k] {
			inSet[k] = true
			keys = append(keys, k)
		}
	}
	fresh := make([]CounterKey, 0, len(counts))
	for k := range counts {
		if !inSet[k] {
			fresh = append(fresh, k)
		}
	}
	sort.Slice(fresh, func(i, j int) bool {
		ci, cj := sumCounts(counts[fresh[i]]), sumCounts(counts[fresh[j]])
		if ci != cj {
			return ci > cj
		}
		return fresh[i].values()[0]+fresh[i].values()[1] < fresh[j].values()[0]+fresh[j].values()[1]
	})
	for _, k := range fresh {
		if maxKeys > 0 && len(keys) >= maxKeys {
			// taşan: sayıları "diğer"e katla, kendi serisi yazılmaz
			if counts[counterOtherKey] == nil {
				counts[counterOtherKey] = map[time.Time]float64{}
			}
			for t, v := range counts[k] {
				counts[counterOtherKey][t] += v
			}
			delete(res.Seen, k)
			res.Overflow++
			continue
		}
		inSet[k] = true
		keys = append(keys, k)
	}
	if res.Overflow > 0 && !inSet[counterOtherKey] {
		keys = append(keys, counterOtherKey)
		inSet[counterOtherKey] = true
		res.Seen[counterOtherKey] = to
	}
	sort.Slice(keys, func(i, j int) bool { return keyLess(keys[i], keys[j]) })
	res.Keys = len(keys)
	for t := m0; !t.After(m1); t = t.Add(time.Minute) {
		res.Minutes++
		for _, k := range keys {
			v := counts[k][t] // yok → 0 (bilinen sıfır)
			res.Points = append(res.Points, &chstore.MetricPoint{
				Metric: counterMetric, Instrument: "gauge", Unit: "1", Description: "Oracle error rows per minute (Coremetry poller)",
				ServiceName: source, Time: t, StartTime: t, Value: v,
				AttrKeys: CounterGroupBy, AttrValues: k.values(),
				SeriesFingerprint: counterFingerprint(source, k),
			})
		}
	}
	return res
}

func sumCounts(m map[time.Time]float64) float64 {
	s := 0.0
	for _, v := range m {
		s += v
	}
	return s
}

func keyLess(a, b CounterKey) bool {
	av, bv := a.values(), b.values()
	for i := range av {
		if av[i] != bv[i] {
			return av[i] < bv[i]
		}
	}
	return false
}

// counterFingerprint — otlp.SeriesFingerprint ile aynı şema (metrik \x00
// sıralı k=v \x1f … \x00 service.name=…), protobuf'suz. Aynı seri her
// poll'da aynı parmak izini alsın (cross-signal pivot, rollup kimliği).
func counterFingerprint(source string, k CounterKey) uint64 {
	d := xxhash.New()
	_, _ = d.WriteString(counterMetric)
	_, _ = d.Write([]byte{0x00})
	vals := k.values()
	type kv struct{ k, v string }
	pairs := make([]kv, len(CounterGroupBy))
	for i, key := range CounterGroupBy {
		pairs[i] = kv{key, vals[i]}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	for i, p := range pairs {
		if i > 0 {
			_, _ = d.Write([]byte{0x1f})
		}
		_, _ = d.WriteString(p.k)
		_, _ = d.WriteString("=")
		_, _ = d.WriteString(p.v)
	}
	_, _ = d.Write([]byte{0x00})
	_, _ = d.WriteString("service.name=")
	_, _ = d.WriteString(source)
	return d.Sum64()
}

// MetricSink — chstore.Store.InsertMetrics.
type MetricSink interface {
	InsertMetrics(ctx context.Context, pts []*chstore.MetricPoint) error
}

// Counter — kaynak başına aktif anahtar belleği + yazım. Tek goroutine
// (poller tiki) çağırır; mu yalnız durum okuma (Stats) için.
type Counter struct {
	sink   MetricSink
	now    func() time.Time
	mu     sync.Mutex
	active map[string]map[CounterKey]time.Time // source id → anahtar → son görülme
	last   map[string]CounterStats
	errors atomic.Int64
}

// CounterStats — son tikin özeti (Settings durum kartı / kanıt notu).
type CounterStats struct {
	At       int64  `json:"at"`
	Points   int    `json:"points"`
	Keys     int    `json:"keys"`
	Overflow int    `json:"overflow"`
	Ignored  int    `json:"ignored"`
	Error    string `json:"error,omitempty"`
}

func NewCounter(sink MetricSink) *Counter {
	return &Counter{sink: sink, now: time.Now, active: map[string]map[CounterKey]time.Time{}, last: map[string]CounterStats{}}
}

// Handle — poll sonrası kanca gövdesi (satır yoksa da çağrılır: aktif
// anahtarlara sıfır yazılır, kapanış mümkün olsun). Hata poll'u düşürmez.
func (c *Counter) Handle(ctx context.Context, src SourceConfig, rows []chstore.OracleErrorRow, from, to time.Time, ignore map[string]bool) BucketResult {
	if c == nil || c.sink == nil || src.ID == "" {
		return BucketResult{}
	}
	now := c.now()
	c.mu.Lock()
	active := c.active[src.ID]
	if active == nil {
		active = map[CounterKey]time.Time{}
		c.active[src.ID] = active
	}
	cutoff := now.Add(-counterActiveWindow)
	for k, seen := range active {
		if seen.Before(cutoff) {
			delete(active, k)
		}
	}
	snapshot := make(map[CounterKey]time.Time, len(active))
	for k, v := range active {
		snapshot[k] = v
	}
	c.mu.Unlock()

	res := BucketRows(src.Name, rows, from, to, snapshot, ignore, CounterMaxKeys)
	st := CounterStats{At: now.Unix(), Points: len(res.Points), Keys: res.Keys, Overflow: res.Overflow, Ignored: res.Ignored}
	if len(res.Points) > 0 {
		if err := c.sink.InsertMetrics(ctx, res.Points); err != nil {
			c.errors.Add(1)
			st.Error = err.Error()
			log.Printf("[oracle/counter] %s: ext:error_count yazımı düştü (%d nokta; watermark ilerledi, satırlar oracle_error_log'da): %v", src.Name, len(res.Points), err)
		}
	}
	c.mu.Lock()
	for k, seen := range res.Seen {
		if seen.After(active[k]) {
			active[k] = seen
		}
	}
	c.last[src.ID] = st
	c.mu.Unlock()
	return res
}

// Stats — kaynak başına son tik özeti (kopya).
func (c *Counter) Stats(sourceID string) (CounterStats, bool) {
	if c == nil {
		return CounterStats{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.last[sourceID]
	return s, ok
}

// Forget — kaynak silinince aktif anahtarları bırakır.
func (c *Counter) Forget(sourceID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.active, sourceID)
	delete(c.last, sourceID)
	c.mu.Unlock()
}

// ActiveKeys — test/teşhis: kaynağın aktif anahtar sayısı.
func (c *Counter) ActiveKeys(sourceID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active[sourceID])
}
