package oracle

// poller.go — v0.10.601 (Oracle Aşama 2, audit §2 "periyodik ingest" +
// §3 sorgu disiplini). Lider-kapılı poll döngüsü: influx/poller.go ile
// aynı iskelet (Run/Tick/pollSource, kaynak yalıtımı, sağlık kancası,
// durum yayını), farkı VERİ: metrik değil satır — mapping.go → chstore.
//
// Watermark KALICI (audit "watermark (kalıcı)"): kaynak başına
// system_settings["oracle_poll:<id>"] — pod yeniden başlayınca ya da
// liderlik el değiştirince kaldığı yerden; bellek yalnız önbellek.
// Pencere (watermark − overlap, now]: overlap geç commit'lenen satırı
// yakalar, yeniden görülen satır aynı row_id ile RMT'de tek satıra iner
// (idempotent). Watermark yalnız YAZIM başarılıysa ilerler; hata veren tik
// hiçbir şeyi ilerletmez, bir sonraki tik aynı pencereyi yeniden dener
// (Influx'la aynı: retry/backoff yok, kaynak yalıtık).
//
// Watermark tabanı: satır gelmeyen uzun sürede pencere sınırsız büyümesin
// diye tavana ÇARPMAYAN bir tik watermark'ı en az (to − initialLookback)'e
// çeker. Tavana çarpan tik bunu YAPMAZ — arkada okunmamış satır var, taban
// onları atlardı; nextDue = now ile hemen devam eder.
//
// Bind değerleri: go-ora time.Time'ı BİLEŞENLERİYLE (duvar saati) gönderir.
// Dilimsiz TIMESTAMP kolonuna UTC anı bağlamak 3 saat kaydırır — bindTime
// kaynağın diliminde duvar saati üretir; dilimli kolonda UTC anı.
//
// Erişilemezken tarayıcı koşmaz: sağlık kancası HER sonuçta çağrılır ve
// 3 ardışık hata "kaynak düştü" Problem'i açar (588); satır olmadığı için
// sahte "iyileşti" üretecek bir seri de yoktur.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/selfobs"
)

const (
	pollTickGranularity = 5 * time.Second
	pollMinInterval     = 15 * time.Second
	pollDefaultInterval = 60 * time.Second
	// pollOverlap — pencere başı watermark'ın bu kadar gerisinden başlar.
	pollOverlap = 2 * time.Minute
	// pollInitialLookback — ilk poll'un (ve boş tiklerde watermark tabanının)
	// geriye bakışı.
	pollInitialLookback = 15 * time.Minute
	// pollRowCap — tik başına FETCH FIRST tavanı.
	pollRowCap = 5000

	// WorkerStatusKey — lider pod'un yayınladığı durum blobu (API pod'u okur).
	WorkerStatusKey    = "oracle_worker_status"
	pollStateKeyPrefix = "oracle_poll:"
)

// RowSink — chstore.Store'un InsertOracleErrors'u (test çiftleri için arayüz).
type RowSink interface {
	InsertOracleErrors(ctx context.Context, rows []chstore.OracleErrorRow) error
}

// StateStore — system_settings okuma/yazma (chstore.Store).
type StateStore interface {
	GetSetting(ctx context.Context, key string) ([]byte, error)
	PutSetting(ctx context.Context, key string, value []byte) error
}

// PollState — kalıcı watermark blobu.
type PollState struct {
	WatermarkNs int64 `json:"watermarkNs"`
	UpdatedAt   int64 `json:"updatedAt"` // unix ms
}

// PollStatus — kaynak başına son tik.
type PollStatus struct {
	SourceID        string `json:"sourceId"`
	Name            string `json:"name"`
	WatermarkNs     int64  `json:"watermarkNs"`
	LastPollAt      int64  `json:"lastPollAt"` // unix ms
	NextDueAt       int64  `json:"nextDueAt"`  // unix ms
	LastRows        int    `json:"lastRows"`
	LastMapped      int    `json:"lastMapped"`
	LastNoTimestamp int    `json:"lastNoTimestamp"`
	LastBadTraceID  int    `json:"lastBadTraceId"`
	Capped          bool   `json:"capped,omitempty"`
	LastError       string `json:"lastError,omitempty"`
}

// WorkerStatusSnapshot — paylaşılan durum blobu (influx v0.10.333 şekli).
type WorkerStatusSnapshot struct {
	Pod       string       `json:"pod"`
	UpdatedAt int64        `json:"updatedAt"`
	Sources   []PollStatus `json:"sources"`
}

func EncodeWorkerStatus(pod string, at time.Time, sources []PollStatus) ([]byte, error) {
	out := append([]PollStatus(nil), sources...)
	sort.Slice(out, func(i, j int) bool { return out[i].SourceID < out[j].SourceID })
	return json.Marshal(WorkerStatusSnapshot{Pod: pod, UpdatedAt: at.UnixMilli(), Sources: out})
}

func DecodeWorkerStatus(raw []byte) (*WorkerStatusSnapshot, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s WorkerStatusSnapshot
	if err := json.Unmarshal(raw, &s); err != nil || s.UpdatedAt == 0 {
		return nil, false
	}
	return &s, true
}

// Worker — leader-gated poll döngüsü.
type Worker struct {
	svc   *Service
	sink  RowSink
	state StateStore

	// enjekte edilebilir (testler): saat + satır okuyucu.
	now       func() time.Time
	queryRows func(ctx context.Context, src SourceConfig, sqlText string, args []any) ([]map[string]any, error)

	healthHook func(ctx context.Context, src SourceConfig, lastError string)
	// rowsHook — v0.10.893 (Aşama 3 dilim A): başarılı her poll'dan sonra
	// (satır yoksa da — aktif anahtarlara sıfır yazılsın) eşlenmiş satırlar
	// + poll penceresi. Hata poll'u/watermark'ı etkilemez; sayaç ve (dilim C)
	// dış tarayıcı buradan beslenir. main.go bağlar.
	rowsHook RowsHook
	// inFlight — Run (5 s) ve lider OnAcquire eşzamanlı Tick çağırabilir;
	// iki tik aynı kaynağı iki kez poll'layıp kancayı iki kez koşturmasın.
	inFlight atomic.Bool
	// probeTsType — v0.10.885: zaman kolonunun sözlük tipi (tsbind.go);
	// enjekte edilebilir. Sonuç kaynak başına önbellekte; hata 10 dk sonra
	// yeniden denenir, o arada "" (kutuya göre varsayılan ifade).
	probeTsType func(ctx context.Context, src SourceConfig) (string, error)

	mu        sync.Mutex
	nextDue   map[string]time.Time
	status    map[string]PollStatus
	watermark map[string]time.Time
	tsTypes   map[string]tsTypeEntry

	mPolls, mRows, mDropped, mErrors metric.Int64Counter
}

// NewWorker — sink ve state tipik olarak aynı *chstore.Store.
func NewWorker(svc *Service, sink RowSink, state StateStore) *Worker {
	w := &Worker{
		svc: svc, sink: sink, state: state,
		now:       time.Now,
		nextDue:   map[string]time.Time{},
		status:    map[string]PollStatus{},
		watermark: map[string]time.Time{},
	}
	w.queryRows = w.defaultQueryRows
	w.probeTsType = svc.probeTsType
	w.tsTypes = map[string]tsTypeEntry{}
	m := selfobs.Meter()
	var err error
	if w.mPolls, err = m.Int64Counter("oracle_polls_total", metric.WithDescription("Oracle hata tablosu poll sorguları")); err != nil {
		log.Printf("[oracle] metric oracle_polls_total: %v", err)
	}
	if w.mRows, err = m.Int64Counter("oracle_rows_total", metric.WithDescription("oracle_error_log'a yazılan satırlar")); err != nil {
		log.Printf("[oracle] metric oracle_rows_total: %v", err)
	}
	if w.mDropped, err = m.Int64Counter("oracle_rows_dropped_total", metric.WithDescription("Düşen Oracle satırları (zamansız)")); err != nil {
		log.Printf("[oracle] metric oracle_rows_dropped_total: %v", err)
	}
	if w.mErrors, err = m.Int64Counter("oracle_errors_total", metric.WithDescription("Oracle poll/eşleme/yazım hataları")); err != nil {
		log.Printf("[oracle] metric oracle_errors_total: %v", err)
	}
	return w
}

// SetHealthHook — poll-sonrası sağlık çağrısı, başarı da hata da. Start'tan ÖNCE.
// RowsHook — poll sonrası satır kancası (bkz. Worker.rowsHook).
type RowsHook func(ctx context.Context, src SourceConfig, rows []chstore.OracleErrorRow, from, to time.Time)

// SetRowsHook — v0.10.893.
func (w *Worker) SetRowsHook(h RowsHook) { w.rowsHook = h }

func (w *Worker) SetHealthHook(h func(ctx context.Context, src SourceConfig, lastError string)) {
	w.healthHook = h
}

// Run — pollTickGranularity'de döner; her tik lider kapısından geçer.
func (w *Worker) Run(ctx context.Context, isLeader func() bool) {
	t := time.NewTicker(pollTickGranularity)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if isLeader == nil || isLeader() {
				w.Tick(ctx)
			}
		}
	}
}

// pollInterval — SAF: kaynağın aralığı, kelepçeli.
func pollInterval(src SourceConfig) time.Duration {
	d := time.Duration(src.IntervalSec) * time.Second
	if d < pollMinInterval {
		return pollDefaultInterval
	}
	return d
}

// Tick — aralığı dolmuş etkin kaynakları poll'lar; kaynaklar yalıtık.
func (w *Worker) Tick(ctx context.Context) {
	if w.svc == nil {
		return
	}
	if !w.inFlight.CompareAndSwap(false, true) {
		return // v0.10.893 — eşzamanlı tik (Run + OnAcquire) atlanır
	}
	defer w.inFlight.Store(false)
	cfg := w.svc.CurrentSettings()
	now := w.now()
	polled := false
	for _, src := range cfg.Sources {
		if !src.Enabled || src.ID == "" {
			continue
		}
		w.mu.Lock()
		due, seen := w.nextDue[src.ID]
		w.mu.Unlock()
		if seen && now.Before(due) {
			continue
		}
		st, batch := w.pollSource(ctx, src, now)
		polled = true
		next := now.Add(pollInterval(src))
		if st.Capped && st.LastError == "" {
			next = now // arkada satır var — bir sonraki granülde devam
		}
		st.NextDueAt = next.UnixMilli()
		if w.healthHook != nil {
			w.healthHook(ctx, src, st.LastError)
		}
		if w.rowsHook != nil && st.LastError == "" {
			w.rowsHook(ctx, src, batch.rows, batch.from, batch.to) // v0.10.893 — yalnız başarılı poll
		}
		w.mu.Lock()
		w.nextDue[src.ID] = next
		w.status[src.ID] = st
		w.mu.Unlock()
	}
	live := map[string]bool{}
	for _, src := range cfg.Sources {
		if src.Enabled {
			live[src.ID] = true
		}
	}
	w.mu.Lock()
	for id := range w.status {
		if !live[id] {
			delete(w.status, id)
			delete(w.nextDue, id)
			delete(w.watermark, id)
		}
	}
	w.mu.Unlock()
	if polled {
		w.publishStatus(ctx, now)
	}
}

// pollWindow — SAF: (watermark − overlap, now].
func pollWindow(wm, now time.Time) (from, to time.Time) {
	return wm.Add(-pollOverlap), now
}

// initialWatermark — SAF: ilk poll'un başlangıcı.
func initialWatermark(now time.Time) time.Time {
	return now.Add(-pollInitialLookback)
}

// advanceWatermark — SAF. Yeni watermark = görülen en büyük satır zamanı;
// ASLA gerilemez (overlap penceresi watermark'tan eski satır getirir).
// Tavana çarpmayan tik ayrıca (to − initialLookback) tabanını uygular ki
// satırsız uzun sürede pencere büyümesin; tavana çarpan tikte taban YOK —
// okunmamış satırları atlardı.
func advanceWatermark(prev time.Time, rows []chstore.OracleErrorRow, to time.Time, capped bool) time.Time {
	wm := prev
	for _, r := range rows {
		if r.Time.After(wm) {
			wm = r.Time
		}
	}
	if !capped {
		if floor := to.Add(-pollInitialLookback); floor.After(wm) {
			wm = floor
		}
	}
	return wm
}

// bindTime — SAF: Oracle'a bağlanacak zaman. Dilimsiz kolon → kaynağın
// diliminde duvar saati (Location UTC etiketiyle, sürücü bileşenleri
// gönderir); dilimli kolon → UTC anı.
func bindTime(t time.Time, loc *time.Location, hasZone bool) time.Time {
	if hasZone {
		return t.UTC()
	}
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), l.Nanosecond(), time.UTC)
}

func (w *Worker) watermarkFor(ctx context.Context, id string, now time.Time) time.Time {
	w.mu.Lock()
	wm, ok := w.watermark[id]
	w.mu.Unlock()
	if ok {
		return wm
	}
	if w.state != nil {
		if raw, err := w.state.GetSetting(ctx, pollStateKeyPrefix+id); err == nil && len(raw) > 0 {
			var st PollState
			if json.Unmarshal(raw, &st) == nil && st.WatermarkNs > 0 {
				wm = time.Unix(0, st.WatermarkNs).UTC()
				w.mu.Lock()
				w.watermark[id] = wm
				w.mu.Unlock()
				return wm
			}
		}
	}
	return initialWatermark(now)
}

func (w *Worker) saveWatermark(ctx context.Context, id string, wm, now time.Time) {
	w.mu.Lock()
	w.watermark[id] = wm
	w.mu.Unlock()
	if w.state == nil {
		return
	}
	raw, _ := json.Marshal(PollState{WatermarkNs: wm.UnixNano(), UpdatedAt: now.UnixMilli()})
	if err := w.state.PutSetting(ctx, pollStateKeyPrefix+id, raw); err != nil {
		log.Printf("[oracle] %s: watermark yazılamadı: %v", id, err)
	}
}

func (w *Worker) defaultQueryRows(ctx context.Context, src SourceConfig, sqlText string, args []any) ([]map[string]any, error) {
	db, secret, err := w.svc.open(src)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	_, rows, err := runRows(ctx, db, sqlText, args, queryTimeout(src), secret)
	return rows, err
}

// pollBatch — v0.10.893: rowsHook'a giden eşlenmiş satırlar + pencere.
type pollBatch struct {
	rows     []chstore.OracleErrorRow
	from, to time.Time
}

func (w *Worker) pollSource(ctx context.Context, src SourceConfig, now time.Time) (PollStatus, pollBatch) {
	st := PollStatus{SourceID: src.ID, Name: src.Name, LastPollAt: now.UnixMilli()}
	attrs := metric.WithAttributes(attribute.String("source", selfobs.SafeAttr(src.Name)))
	var batch pollBatch
	fail := func(msg string) (PollStatus, pollBatch) {
		st.LastError = msg
		w.count(w.mErrors, ctx, 1, attrs)
		log.Printf("[oracle] %s: %s", src.Name, msg)
		return st, pollBatch{}
	}
	mapper, err := NewMapper(src)
	if err != nil {
		return fail("eşleme ayarı: " + err.Error())
	}
	wm := w.watermarkFor(ctx, src.ID, now)
	st.WatermarkNs = wm.UnixNano()
	from, to := pollWindow(wm, now)
	batch.from, batch.to = from, to
	sqlText, args, err := buildPollQuery(src, from, to, pollRowCap, w.tsTypeFor(ctx, src, now))
	if err != nil {
		return fail("sorgu: " + err.Error())
	}
	w.count(w.mPolls, ctx, 1, attrs)
	rows, err := w.queryRows(ctx, src, sqlText, args)
	if err != nil {
		return fail(err.Error()) // runRows/open zaten redakte etti
	}
	st.LastRows = len(rows)
	mapped, ms := mapper.MapAll(rows)
	batch.rows = mapped
	st.LastMapped, st.LastNoTimestamp, st.LastBadTraceID = ms.Mapped, ms.NoTimestamp, ms.BadTraceID
	if ms.NoTimestamp > 0 {
		w.count(w.mDropped, ctx, int64(ms.NoTimestamp), attrs)
	}
	if len(mapped) > 0 {
		if err := w.sink.InsertOracleErrors(ctx, mapped); err != nil {
			return fail("CH yazımı: " + err.Error())
		}
		w.count(w.mRows, ctx, int64(len(mapped)), attrs)
	}
	st.Capped = len(rows) >= pollRowCap
	newWM := advanceWatermark(wm, mapped, to, st.Capped)
	if !newWM.Equal(wm) {
		w.saveWatermark(ctx, src.ID, newWM, now)
	}
	st.WatermarkNs = newWM.UnixNano()
	if ms.NoTimestamp > 0 || ms.BadTraceID > 0 {
		log.Printf("[oracle] %s: %d satır, %d yazıldı, %d zamansız düştü, %d geçersiz trace id",
			src.Name, len(rows), len(mapped), ms.NoTimestamp, ms.BadTraceID)
	}
	return st, batch
}

func (w *Worker) count(c metric.Int64Counter, ctx context.Context, n int64, opts ...metric.AddOption) {
	if c != nil {
		c.Add(ctx, n, opts...)
	}
}

// Status — bu pod'daki işçinin kaynak başına son durumu (kimlik sırasıyla).
func (w *Worker) Status() []PollStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]PollStatus, 0, len(w.status))
	for _, st := range w.status {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceID < out[j].SourceID })
	return out
}

func podName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "unknown"
}

// publishStatus — en az bir kaynak poll'landıysa; hata yalnız loglanır.
func (w *Worker) publishStatus(ctx context.Context, at time.Time) {
	if w.state == nil {
		return
	}
	raw, err := EncodeWorkerStatus(podName(), at, w.Status())
	if err != nil {
		return
	}
	if err := w.state.PutSetting(ctx, WorkerStatusKey, raw); err != nil {
		log.Printf("[oracle] işçi durumu yayınlanamadı: %v", err)
	}
}

// String — log/hata metinleri için kısa kimlik (şifre içermez).
func (st PollStatus) String() string {
	return fmt.Sprintf("%s rows=%d mapped=%d wm=%s err=%q", st.SourceID, st.LastRows, st.LastMapped,
		time.Unix(0, st.WatermarkNs).UTC().Format(time.RFC3339), st.LastError)
}

// tsTypeEntry — v0.10.885: kaynak başına sözlük tipi önbelleği.
type tsTypeEntry struct {
	typ string
	ok  bool
	at  time.Time
}

const tsTypeRetry = 10 * time.Minute

// tsTypeFor — önbellekten; yoksa (ya da hata eskidiyse) sözlük okur. Hata
// poll'u DÜŞÜRMEZ: "" ile devam (TimestampHasZone kutusuna göre ifade), tek
// log satırı, 10 dk sonra tekrar.
func (w *Worker) tsTypeFor(ctx context.Context, src SourceConfig, now time.Time) string {
	key := src.ID + "|" + strings.ToUpper(src.Schema+"."+src.Table+"."+src.TimestampColumn)
	w.mu.Lock()
	e, ok := w.tsTypes[key]
	w.mu.Unlock()
	if ok && (e.ok || now.Sub(e.at) < tsTypeRetry) {
		return e.typ
	}
	if w.probeTsType == nil {
		return ""
	}
	typ, err := w.probeTsType(ctx, src)
	if err != nil {
		log.Printf("[oracle] %s: zaman kolonu tipi okunamadı (%s), bind ifadesi kutuya göre: %v", src.Name, src.TimestampColumn, err)
		typ = ""
	}
	w.mu.Lock()
	w.tsTypes[key] = tsTypeEntry{typ: typ, ok: err == nil, at: now}
	w.mu.Unlock()
	return typ
}
