package oracle

// exgroups.go — v0.10.1092 (operatör: "Oracle hataları Exceptions gibi
// görünsün, hatta Exceptions altında da olabilir.")
//
// Oracle hata tablosu satırlarını Exceptions'ın grup modeline taşıyan
// TAZELEYİCİ. Oracle satırları durgun bir akış (dakikada binlerce); sıçrama
// anomalisi yolu tasarım gereği onlarda ateşlemez — operatör onları
// exception gibi triyaj etmek istiyor.
//
//   - Anahtar (kaynak, hata kodu, operasyon kodu) → chstore.OracleGroupFingerprint
//     (`ora:` + sha1). Kanal / host / servis KIRILIMDIR (state blob'u).
//   - Yalnız KAPANMIŞ dakikalar sayılır: özel SQL kipinde her poll son
//     WindowMin dakikayı yeniden okur (aynı satır RMT'de son sürüme iner,
//     Adet geç commit'te değişebilir); dakika ancak sorgunun penceresinden
//     ÇIKINCA kesinleşir. Tazeleyici [imleç, kapanış) aralığını
//     oracle_error_log FINAL'dan bir kez toplar ve imleci kapanışa taşır.
//     İmleç + kırılım kalıcı: system_settings `oracle_exgroups:<kaynak>`.
//   - İmleç HER turda system_settings'ten yeniden okunur (tek küçük okuma):
//     liderlik el değiştirip geri gelince eski bellek imleci, arada öteki
//     liderin saydığı dakikaları ikinci kez saydırmasın. Bellek yalnız kendi
//     son yazımımızın imleç kaydı düşmüşse devreye girer (ikisinden İLERİDE
//     olan kazanır).
//   - Yazım chstore.UpsertExceptionGroups — TEK batch INSERT (ya hepsi ya
//     hiçbiri; tam-satır taşıma: durum, atanan, AI özeti, resolve anlık
//     görüntüsü korunur; occurrences ARTIM toplanır).
//   - Kip kapısı (kaynak ayarı problemMode): off = grup YAZILMAZ, imleç
//     ilerlemez (açılınca en çok exGroupMaxCatchUp geriye bakar); shadow =
//     gruplar yazılır, bildirim YOK (GroupNotifyGate); live = olağan
//     exception bildirim hattı.
//   - Hata yönü: imleç okuması, toplama ya da batch yazım düşerse imleç
//     İLERLEMEZ ve hiçbir artım yazılmamıştır (batch atomik) — sonraki tur
//     aynı aralığı yeniden dener, çift sayım yok. Batch yazıldı ama imleç
//     kaydı düştüyse: aynı süreçte bellek imleci ileride olduğu için yeniden
//     sayılmaz; o arada liderlik el değiştirirse yeni lider eski imleçten
//     başlar ve o aralık BİR KEZ daha sayılır. Gerçek ayrık beyin (iki pod
//     aynı anda lider sanır, Redis kilidi bölünür) de aynı aralığı iki kez
//     sayabilir — kilit bunu önlemek için var; tazeleyici ayrıca kilitlenmez.
//   - Toplama tavanı (chstore.OracleGroupAggLimit): satırlar dakika sırasıyla
//     gelir; tavana çarpan turda son (yarım olabilecek) dakika atılır, imleç
//     yalnız TAM okunan son dakikaya kadar ilerler (ilerleyen yakalama).

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	exGroupStateKeyPrefix = "oracle_exgroups:"
	// exGroupInitialLookback — imleç hiç yokken ilk turun geriye bakışı. 2 sa:
	// öncelik kuralı "son 1 sa vs önceki 1 sa" ilk turda önceki saati de görsün
	// (yoksa önceki 0 → her büyük grup ilk saatte "patlama" P1 olurdu).
	exGroupInitialLookback = 2 * time.Hour
	// exGroupMaxCatchUp — kip kapalıyken / uzun kesintide geriye bakış tavanı
	// (oracle_error_log 30 gün tutar; toplama dakika tavanıyla ilerler).
	exGroupMaxCatchUp = 6 * time.Hour
	// exGroupClosedMargin — Oracle SYSDATE ile Coremetry saati arasındaki kayma payı.
	exGroupClosedMargin = time.Minute
	// Kırılım tavanları (blob büyümesin).
	exGroupMaxGroups   = 1000
	exGroupMaxBreakdwn = 12
	// exGroupSlot — saatlik pencerelerin tanesi (Oracle P1 kuralı: son 1 sa
	// vs önceki 1 sa); blob'da grup başına en çok 2 sa / 10 dk = 12 dilim.
	exGroupSlot      = 10 * time.Minute
	exGroupSlotKeep  = 2 * time.Hour
	exGroupHourRange = time.Hour
)

// ExGroupBreakdown — bir grubun kırılımı (Exceptions satırı + detay paneli +
// Oracle öncelik kuralının saatlik toplamları).
type ExGroupBreakdown struct {
	Code     string            `json:"code"`
	Op       string            `json:"op"`
	Channels map[string]uint64 `json:"channels,omitempty"` // kanal → ağırlık (kapanmış dakikalar)
	Services map[string]uint64 `json:"services,omitempty"` // servis → trace/pod oyu (birikimli)
	// Slots — 10 dk dilim başı (unix sn) → ağırlık, son 2 sa (HourStats okur).
	Slots    map[int64]uint64 `json:"slots,omitempty"`
	LastSeen int64            `json:"lastSeen"` // unix ns
}

// ExGroupState — kaynak başına kalıcı blob: imleç + kırılımlar.
type ExGroupState struct {
	V         int                          `json:"v"`
	CursorNs  int64                        `json:"cursorNs"` // ilk SAYILMAMIŞ dakika (yarı açık aralığın başı)
	UpdatedAt int64                        `json:"updatedAt"`
	Groups    map[string]*ExGroupBreakdown `json:"groups"`
}

// ExGroupKey — system_settings anahtarı.
func ExGroupKey(sourceID string) string { return exGroupStateKeyPrefix + sourceID }

// DecodeExGroupState — SAF: boş / bozuk → boş durum (imleç 0).
func DecodeExGroupState(raw []byte) ExGroupState {
	var st ExGroupState
	if len(raw) > 0 && json.Unmarshal(raw, &st) == nil && st.Groups != nil {
		return st
	}
	return ExGroupState{V: 1, Groups: map[string]*ExGroupBreakdown{}}
}

// HourStats — SAF: kapanış ucundan (endNs, blob imleci) geriye son 1 sa ve
// önceki 1 sa ağırlıkları. 10 dk dilimler başlangıcına göre pencereye girer
// (≤ 10 dk yuvarlama). b nil → 0, 0.
func HourStats(b *ExGroupBreakdown, endNs int64) (last, prev uint64) {
	if b == nil || endNs <= 0 {
		return 0, 0
	}
	end := endNs / int64(time.Second)
	h := int64(exGroupHourRange / time.Second)
	for start, w := range b.Slots {
		switch {
		case start >= end-h && start < end:
			last += w
		case start >= end-2*h && start < end-h:
			prev += w
		}
	}
	return last, prev
}

// ExGroupStore — chstore.Store'un tazeleyicinin kullandığı yüzü.
type ExGroupStore interface {
	OracleGroupAggregates(ctx context.Context, sourceID string, from, to time.Time) ([]chstore.OracleGroupAgg, error)
	UpsertExceptionGroups(ctx context.Context, gs []chstore.ExceptionGroup) error
}

// ServiceVotesFn — SubjectResolver.ServiceVotes (nil → servis çözümü yok).
type ServiceVotesFn func(ctx context.Context, sourceID, op, code string) map[string]int

// ExGroupRefresher — tek goroutine (poller kancası, lider) çağırır.
type ExGroupRefresher struct {
	store ExGroupStore
	state StateStore
	votes ServiceVotesFn
	now   func() time.Time

	mu sync.Mutex
	// written — bu sürecin SON başarılı turunun durumu (kaynak başına). Kalıcı
	// blob her turda yeniden okunur; bellek yalnız blob'dan İLERİDEYSE (kendi
	// imleç kaydımız düştüyse) kazanır — bkz. dosya başı.
	written map[string]*ExGroupState
}

func NewExGroupRefresher(store ExGroupStore, state StateStore, votes ServiceVotesFn) *ExGroupRefresher {
	return &ExGroupRefresher{store: store, state: state, votes: votes, now: time.Now, written: map[string]*ExGroupState{}}
}

// ExGroupClosedEnd — SAF: bu kaynakta kesinleşmiş son dakikanın SONU
// (yarı açık üst sınır). Özel SQL: sorgu son WindowMin dakikayı yeniden okur
// → trunc(now) − WindowMin − pay. Tablo kipi: geç commit overlap'i + bir
// aralık → trunc(now) − (overlap + aralık) − pay.
func ExGroupClosedEnd(src SourceConfig, now time.Time) time.Time {
	lag := pollOverlap + pollInterval(src)
	if IsCustom(src) {
		lag = time.Duration(windowMinOf(src)) * time.Minute
	}
	return now.UTC().Truncate(time.Minute).Add(-lag - exGroupClosedMargin)
}

// exGroupRange — SAF: [from, to) sayılacak aralık. cursor 0 → to −
// ilk geriye bakış; tavan exGroupMaxCatchUp; boş aralık → ok=false.
func exGroupRange(cursor, closedEnd time.Time) (from, to time.Time, ok bool) {
	to = closedEnd
	from = cursor
	if from.IsZero() {
		from = to.Add(-exGroupInitialLookback)
	}
	if lo := to.Add(-exGroupMaxCatchUp); from.Before(lo) {
		from = lo
	}
	return from, to, to.After(from)
}

// clipAggsAtLimit — SAF (toplama tavanı): satır sayısı tavana ulaştıysa son
// dakika yarım olabilir → atılır, üst sınır o dakikanın başına çekilir. Tek
// dakika tek başına tavanı aşıyorsa (ilerleme yolu yok) o dakika olduğu gibi
// sayılır ve kesik ilan edilir (log).
func clipAggsAtLimit(aggs []chstore.OracleGroupAgg, from, to time.Time, limit int) ([]chstore.OracleGroupAgg, time.Time, bool) {
	if limit <= 0 || len(aggs) < limit {
		return aggs, to, false
	}
	lastMin := aggs[len(aggs)-1].Minute
	if !lastMin.After(from) {
		end := lastMin.Add(time.Minute)
		if end.After(to) {
			end = to
		}
		return aggs, end, true
	}
	kept := make([]chstore.OracleGroupAgg, 0, len(aggs))
	for _, a := range aggs {
		if a.Minute.Before(lastMin) {
			kept = append(kept, a)
		}
	}
	return kept, lastMin, true
}

// buildOracleGroups — SAF çekirdek: toplamlar → exception_groups artımları
// + kırılım güncellemesi (kanal, servis oyları, 10 dk dilimler). Yoksayılan
// kodlar (kaynak ayarı ignoreCodes) sayılmaz (sayaçla aynı kural). Servis:
// birikimli oyların baskını, yoksa `oracle:<kaynak adı>`. Occurrences = bu
// aralığın ARTIMI (merge toplar). end = bu turun kapanış ucu (dilim budaması).
func buildOracleGroups(src SourceConfig, aggs []chstore.OracleGroupAgg, ignore map[string]bool,
	votes func(op, code string) map[string]int, st *ExGroupState, end time.Time) []chstore.ExceptionGroup {
	type acc struct {
		g      chstore.ExceptionGroup
		rawOp  string
		rawCod string
	}
	byFP := map[string]*acc{}
	order := []string{}
	for _, a := range aggs {
		code, op := strings.TrimSpace(a.Code), strings.TrimSpace(a.Op)
		if code == "" && op == "" {
			continue
		}
		if ignore[strings.ToUpper(code)] {
			continue
		}
		fp := chstore.OracleGroupFingerprint(src.ID, code, op)
		x := byFP[fp]
		if x == nil {
			x = &acc{g: chstore.ExceptionGroup{Fingerprint: fp, Type: code, Message: op,
				FirstSeen: a.First.UnixNano(), LastSeen: a.Last.UnixNano()}, rawOp: a.Op, rawCod: a.Code}
			byFP[fp] = x
			order = append(order, fp)
		}
		x.g.Occurrences += a.Weight
		if n := a.First.UnixNano(); n < x.g.FirstSeen {
			x.g.FirstSeen = n
		}
		if n := a.Last.UnixNano(); n > x.g.LastSeen {
			x.g.LastSeen = n
		}
		b := st.Groups[fp]
		if b == nil {
			b = &ExGroupBreakdown{Code: code, Op: op}
			st.Groups[fp] = b
		}
		if ch := strings.TrimSpace(a.Channel); ch != "" {
			if b.Channels == nil {
				b.Channels = map[string]uint64{}
			}
			b.Channels[ch] += a.Weight
		}
		if b.Slots == nil {
			b.Slots = map[int64]uint64{}
		}
		m := a.Minute
		if m.IsZero() {
			m = a.First
		}
		b.Slots[m.UTC().Truncate(exGroupSlot).Unix()] += a.Weight
	}
	out := make([]chstore.ExceptionGroup, 0, len(order))
	for _, fp := range order {
		x := byFP[fp]
		if x.g.Occurrences == 0 {
			continue
		}
		b := st.Groups[fp]
		if b.LastSeen < x.g.LastSeen {
			b.LastSeen = x.g.LastSeen
		}
		if votes != nil {
			for s, n := range votes(x.rawOp, x.rawCod) {
				if s == "" || n <= 0 {
					continue
				}
				if b.Services == nil {
					b.Services = map[string]uint64{}
				}
				b.Services[s] += uint64(n)
			}
		}
		b.Channels = topBreakdown(b.Channels, exGroupMaxBreakdwn)
		b.Services = topBreakdown(b.Services, exGroupMaxBreakdwn)
		x.g.Service = chstore.OracleGroupFallbackService(src.Name)
		if top := sortedBreakdown(b.Services); len(top) > 0 {
			x.g.Service = top[0].Name
		}
		out = append(out, x.g)
	}
	pruneSlots(st, end)
	pruneExGroupState(st, exGroupMaxGroups)
	return out
}

// pruneSlots — SAF: kapanış ucundan 2 sa + bir dilim eski dilimler düşer.
func pruneSlots(st *ExGroupState, end time.Time) {
	cut := end.Add(-exGroupSlotKeep - exGroupSlot).Unix()
	for _, b := range st.Groups {
		for k := range b.Slots {
			if k < cut {
				delete(b.Slots, k)
			}
		}
	}
}

// BreakdownEntry — sıralı kırılım satırı.
type BreakdownEntry struct {
	Name  string `json:"name"`
	Count uint64 `json:"count"`
}

// sortedBreakdown — SAF: azalan sayı, eşitlikte ad.
func sortedBreakdown(m map[string]uint64) []BreakdownEntry {
	out := make([]BreakdownEntry, 0, len(m))
	for k, v := range m {
		out = append(out, BreakdownEntry{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// SortedBreakdown — API ikizi.
func SortedBreakdown(m map[string]uint64) []BreakdownEntry { return sortedBreakdown(m) }

// topBreakdown — SAF: en büyük n girdi kalır (blob tavanı).
func topBreakdown(m map[string]uint64, n int) map[string]uint64 {
	if len(m) <= n {
		return m
	}
	out := make(map[string]uint64, n)
	for _, e := range sortedBreakdown(m)[:n] {
		out[e.Name] = e.Count
	}
	return out
}

// pruneExGroupState — SAF: grup tavanı aşılırsa en eski son-görülmeler düşer.
func pruneExGroupState(st *ExGroupState, max int) {
	if len(st.Groups) <= max {
		return
	}
	type kv struct {
		fp   string
		last int64
	}
	all := make([]kv, 0, len(st.Groups))
	for fp, b := range st.Groups {
		all = append(all, kv{fp, b.LastSeen})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].last != all[j].last {
			return all[i].last < all[j].last
		}
		return all[i].fp < all[j].fp
	})
	for _, e := range all[:len(all)-max] {
		delete(st.Groups, e.fp)
	}
}

// loadState — kalıcı blob HER turda okunur; okuma hatası turu düşürür (imleç
// bilinmeden sayım yok). Bellekteki son yazımımız blob'dan ilerideyse (kendi
// imleç kaydımız düşmüştü) o kazanır.
func (r *ExGroupRefresher) loadState(ctx context.Context, sourceID string) (*ExGroupState, error) {
	var st ExGroupState
	if r.state != nil {
		raw, err := r.state.GetSetting(ctx, ExGroupKey(sourceID))
		if err != nil {
			return nil, err
		}
		st = DecodeExGroupState(raw)
	} else {
		st = DecodeExGroupState(nil)
	}
	r.mu.Lock()
	mem := r.written[sourceID]
	r.mu.Unlock()
	if mem != nil && mem.CursorNs > st.CursorNs {
		return cloneExGroupState(mem), nil
	}
	return &st, nil
}

// ExGroupResult — bir turun özeti (log / test).
type ExGroupResult struct {
	From, To time.Time
	Groups   int
	Clipped  bool   // toplama tavanı: imleç tam okunan son dakikaya kadar
	Skipped  string // "" = koştu; aksi nedeni (kip kapalı / boş aralık)
}

// Refresh — poller kancasından, başarılı poll sonrası. Kip kapısı burada.
func (r *ExGroupRefresher) Refresh(ctx context.Context, src SourceConfig) (ExGroupResult, error) {
	if r == nil || r.store == nil || src.ID == "" {
		return ExGroupResult{Skipped: "yok"}, nil
	}
	if ProblemModeOf(src) == ProblemModeOff {
		return ExGroupResult{Skipped: "problem kipi kapalı"}, nil
	}
	work, err := r.loadState(ctx, src.ID)
	if err != nil {
		return ExGroupResult{}, err // imleç bilinmiyor — sayım yok
	}
	var cursor time.Time
	if work.CursorNs > 0 {
		cursor = time.Unix(0, work.CursorNs).UTC()
	}
	from, to, ok := exGroupRange(cursor, ExGroupClosedEnd(src, r.now()))
	res := ExGroupResult{From: from, To: to}
	if !ok {
		res.Skipped = "kapanmış yeni dakika yok"
		return res, nil
	}
	aggs, err := r.store.OracleGroupAggregates(ctx, src.ID, from, to)
	if err != nil {
		return res, err // imleç ilerlemez
	}
	aggs, to, res.Clipped = clipAggsAtLimit(aggs, from, to, chstore.OracleGroupAggLimit)
	res.To = to
	if res.Clipped {
		log.Printf("[oracle/exgroups] %s: toplama tavanı (%d satır) — imleç tam okunan son dakikaya (%s) kadar; kalan sonraki turda",
			src.Name, chstore.OracleGroupAggLimit, to.Format(time.RFC3339))
	}
	var votes func(op, code string) map[string]int
	if r.votes != nil {
		votes = func(op, code string) map[string]int { return r.votes(ctx, src.ID, op, code) }
	}
	groups := buildOracleGroups(src, aggs, IgnoreSet(src), votes, work, to)
	if len(groups) > 0 {
		// TEK batch: ya bütün artımlar yazılır ya hiçbiri — imleç yalnız sonra ilerler.
		if err := r.store.UpsertExceptionGroups(ctx, groups); err != nil {
			return res, err
		}
	}
	work.CursorNs = to.UnixNano()
	work.UpdatedAt = r.now().UnixMilli()
	r.mu.Lock()
	r.written[src.ID] = cloneExGroupState(work)
	r.mu.Unlock()
	if r.state != nil {
		if raw, merr := json.Marshal(work); merr == nil {
			if perr := r.state.PutSetting(ctx, ExGroupKey(src.ID), raw); perr != nil {
				log.Printf("[oracle/exgroups] %s: imleç kaydedilemedi (bu süreç bellekten devam eder; liderlik el değiştirirse bu aralık bir kez daha sayılabilir): %v", src.Name, perr)
			}
		}
	}
	res.Groups = len(groups)
	return res, nil
}

func cloneExGroupState(st *ExGroupState) *ExGroupState {
	out := &ExGroupState{V: 1, CursorNs: st.CursorNs, UpdatedAt: st.UpdatedAt, Groups: make(map[string]*ExGroupBreakdown, len(st.Groups))}
	for fp, b := range st.Groups {
		c := *b
		c.Channels = cloneU64(b.Channels)
		c.Services = cloneU64(b.Services)
		if b.Slots != nil {
			c.Slots = make(map[int64]uint64, len(b.Slots))
			for k, v := range b.Slots {
				c.Slots[k] = v
			}
		}
		out.Groups[fp] = &c
	}
	return out
}

func cloneU64(m map[string]uint64) map[string]uint64 {
	if m == nil {
		return nil
	}
	out := make(map[string]uint64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// GroupSource — SAF: Oracle grubunun kaynağı (parmak izi kaynak listesinde
// yeniden hesaplanır). ok=false: kaynak silinmiş / Oracle grubu değil.
func GroupSource(cfg Settings, g chstore.ExceptionGroup) (SourceConfig, bool) {
	if !chstore.IsOracleGroup(g.Fingerprint) {
		return SourceConfig{}, false
	}
	for _, src := range cfg.Sources {
		if src.ID != "" && chstore.OracleGroupFingerprint(src.ID, g.Type, g.Message) == g.Fingerprint {
			return src, true
		}
	}
	return SourceConfig{}, false
}

// GroupLag — SAF: Oracle grubunun last_seen'inin duvar saatine göre yapısal
// gecikmesi (yalnız kapanmış dakikalar sayılır): now − ExGroupClosedEnd.
// Tazelik ölçen tüketiciler (bildirim kapıları, öncelik gerekçesi) bu kadar
// geriden ölçer; aksi hâlde akan bir grup "16 dk önce durdu" okunurdu.
func GroupLag(src SourceConfig, now time.Time) time.Duration {
	return now.Sub(ExGroupClosedEnd(src, now))
}

// GroupNotifyGate — ExceptionNotifier kapısı: (GroupNotifies, GroupLag).
func GroupNotifyGate(cfg Settings, g chstore.ExceptionGroup, now time.Time) (bool, time.Duration) {
	if !chstore.IsOracleGroup(g.Fingerprint) {
		return true, 0
	}
	src, ok := GroupSource(cfg, g)
	if !ok || !src.Enabled || ProblemModeOf(src) != ProblemModeLive {
		return false, 0
	}
	return true, GroupLag(src, now)
}

// GroupNotifies — SAF (ExceptionNotifier süzgeci): span grubu → true; Oracle
// grubu yalnız kaynağı ETKİN ve problemMode = live ise. Kaynak bulunamazsa
// (silinmiş) bildirim yok — sahipsiz grup sessiz kalır.
func GroupNotifies(cfg Settings, g chstore.ExceptionGroup) bool {
	ok, _ := GroupNotifyGate(cfg, g, time.Now())
	return ok
}

// ── API / bildirim tarafı: grup istatistiği önbelleği ────────────────────

// GroupStats — bir Oracle grubunun öncelik + satır bilgisi.
type GroupStats struct {
	Source    SourceConfig
	Lag       time.Duration // GroupLag
	LastHour  uint64        // son 1 sa ağırlık (kapanış ucundan geriye)
	PrevHour  uint64        // önceki 1 sa
	Breakdown *ExGroupBreakdown
	BlobOK    bool // blob okundu (false: okunamadı — saatlik sayılar 0)
}

// GroupStatsCache — kaynak başına blob, TTL'li (varsayılan 30 sn). Öncelik
// satır BAŞINA hesaplanır (liste 300 satır, bildirim tiki): CH okuması kaynak
// başına TTL'de bir. Okuma hatasında son iyi blob korunur.
type GroupStatsCache struct {
	get      func(ctx context.Context, key string) ([]byte, error)
	settings func() Settings
	now      func() time.Time
	ttl      time.Duration

	mu    sync.Mutex
	blobs map[string]cachedExBlob
}

type cachedExBlob struct {
	st ExGroupState
	at time.Time
	ok bool
}

func NewGroupStatsCache(get func(ctx context.Context, key string) ([]byte, error), settings func() Settings) *GroupStatsCache {
	return &GroupStatsCache{get: get, settings: settings, now: time.Now, ttl: 30 * time.Second, blobs: map[string]cachedExBlob{}}
}

// Stats — ok=false: Oracle grubu değil ya da kaynağı yok (silinmiş).
func (c *GroupStatsCache) Stats(g chstore.ExceptionGroup) (GroupStats, bool) {
	if c == nil || c.settings == nil || !chstore.IsOracleGroup(g.Fingerprint) {
		return GroupStats{}, false
	}
	src, ok := GroupSource(c.settings(), g)
	if !ok {
		return GroupStats{}, false
	}
	now := c.now()
	out := GroupStats{Source: src, Lag: GroupLag(src, now)}
	blob := c.blob(src.ID, now)
	if blob.ok {
		out.BlobOK = true
		out.Breakdown = blob.st.Groups[g.Fingerprint]
		out.LastHour, out.PrevHour = HourStats(out.Breakdown, blob.st.CursorNs)
	}
	return out, true
}

func (c *GroupStatsCache) blob(sourceID string, now time.Time) cachedExBlob {
	c.mu.Lock()
	b, have := c.blobs[sourceID]
	c.mu.Unlock()
	if have && now.Sub(b.at) < c.ttl {
		return b
	}
	if c.get == nil {
		return b
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	raw, err := c.get(ctx, ExGroupKey(sourceID))
	cancel()
	if err != nil {
		b.at = now // tekrar denemeyi TTL'e yay; son iyi değer (varsa) korunur
		c.mu.Lock()
		c.blobs[sourceID] = b
		c.mu.Unlock()
		return b
	}
	nb := cachedExBlob{st: DecodeExGroupState(raw), at: now, ok: true}
	c.mu.Lock()
	c.blobs[sourceID] = nb
	c.mu.Unlock()
	return nb
}
