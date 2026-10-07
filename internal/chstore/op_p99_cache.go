package chstore

// op_p99_cache.go — v0.10.1118: operasyon pivotunun 24 sa TABAN ÖNBELLEĞİ ve
// yol seçimi (sorgular + saf birleşim: op_p99_cached.go).
//
// Kimin belleği: dedektörleri koşturan süreç (lider işçi — recorder ve
// evaluator lider kilidiyle koşar; lider olmayan pod pivot çağırmaz). Lider
// değişince yeni lider ilk turunda eski tek geçişe düşer ve tabanı arka planda
// kurar. BİLİNEN BOŞLUK: liderliği kaybeden pod önbelleği belleğinde tutar
// (LeaderHolder'da kayıp kancası yok); yeniden lider olursa opBaselineEvictAfter
// (2 sa) kullanılmamış giriş SIFIRLANIR, daha kısa aradaki taban OpBaselineMaxLag
// sınırında zaten geçerli.
//
// PAYLAŞIM — iki kapsam (op_latency, svc_slowdown) TEK girişi paylaşır; anahtar
// yalnız tabanın değerini değiştiren girdiler: pencere uzunluğu + çağrı tabanı
// (HAVING). Dışlama (svc-slowdown'un batch servisleri) tabanda UYGULANMAZ:
// dışlama servis bazlı satır süzgeci, bir çiftin taban değerini değiştirmez;
// dışlanan servislerin cari satırı tur sorgusunun iç WHERE'inde zaten yok →
// birleşim onların tabanına hiç bakmaz (eşdeğerlik testi: canlı motor
// svc-slowdown vakası batch çiftleri tabanda tutarken eskisiyle aynı). Nicelik
// kümesi sabit (p95 + p99 aynı tDigest'ten), base_buckets her zaman.
//
// Pencere sonu E — kapsamların ilk cari kovaları farklı (op_latency: sürdürme
// penceresinin başı, svc-slowdown: tek cari kova; ~1 kova fark). Tazeleme
// E = son opBaselineScopeFresh (15 dk) içinde görülmüş kapsamların ilk cari
// kovalarının EN ERKENİ: her kapsamın ilk cari kovası zamanla yalnız ilerler →
// E hiçbirinin cari kovasını tabana sokmaz. Yeni / büyümüş (dwell) bir kapsam
// E'den önce başlarsa "ahead" → o tur eski yol + tazeleme (yeni en erkenle).
//
// Tazeleme (opBaselineDecide, SAF):
//   - giriş yok / iyi taban yok         → eski yol + arka planda tazele;
//   - taban sonu ilk cari kovadan SONRA → eski yol + tazele ("ahead");
//   - gecikme > OpBaselineMaxLag (6 sa) → eski yol + tazele ("stale");
//   - gecikme ≥ OpBaselineRefreshEvery (1 sa) → önbellek + arka planda tazele;
//   - son tazeleme LIMIT'e dayandı      → eski yol, OpBaselineCappedBackoff
//     (6 sa) yeniden deneme yok (her saat ~1 GB boşa okunmasın);
//   - son tazeleme HATA verdi           → OpBaselineRetryBackoff (15 dk)
//     yeniden deneme yok (iyi taban yoksa her tur eski yol + 24 sa tazeleme
//     = eskinin ~2 katı okuma olurdu, CH zorlanırken daha da kötü).
//
// Tazeleme ARKA PLANDA (kendi 70 s bağlamı, SQL max_execution_time 60): yaygın
// yavaşlamanın 10 s okuma bütçesine ve recorder turuna binmez; aynı anahtarda
// aynı anda tek tazeleme. Hata → son iyi taban KORUNUR. İyi taban yoksa her tur
// eski tek geçiş — tespit asla durmaz.

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"

	"github.com/cilcenk/coremetry/internal/selfobs"
)

// Pivot kapsamları — çağıran kimliği (metrik etiketi + pencere sonu kaydı;
// kardinalite sabit).
const (
	OpPivotScopeOpLatency   = "op_latency"
	OpPivotScopeSvcSlowdown = "svc_slowdown"
)

const (
	// OpBaselineRefreshEvery — taban penceresi bu kadar kayınca (ilk cari kova
	// − önbellek sonu) arka planda tazelenir; gecikme [0, 1 sa + tazeleme süresi).
	OpBaselineRefreshEvery = time.Hour
	// OpBaselineMaxLag — bundan bayat taban kullanılmaz (tazeleme saatlerdir
	// düşüyor): sınırsız bayatlık sessiz yanlış hüküm demek → eski yol.
	OpBaselineMaxLag = 6 * time.Hour
	// OpBaselineRetryBackoff — tazeleme hatasından sonra yeniden deneme beklemesi.
	OpBaselineRetryBackoff = 15 * time.Minute
	// OpBaselineCappedBackoff — LIMIT'e dayanan tazelemeden sonra bekleme.
	OpBaselineCappedBackoff = 6 * time.Hour
	// opBaselineEvictAfter — bu süre kullanılmayan giriş silinir / sıfırlanır.
	opBaselineEvictAfter = 2 * time.Hour
	// opBaselineScopeFresh — pencere sonu seçiminde hesaba katılan kapsam yaşı.
	opBaselineScopeFresh = 15 * time.Minute
	// opBaselineRefreshDeadline — tazelemenin Go bağlamı (SQL sınırı + pay).
	opBaselineRefreshDeadline = (OpBaselineTimeoutSec + 10) * time.Second
)

// opBaselineKey — tabanın değerini değiştiren girdiler (dosya başı).
type opBaselineKey struct {
	lookback time.Duration
	minCalls int
}

func opBaselineKeyOf(sp OpP99PivotSpec) opBaselineKey {
	return opBaselineKey{
		lookback: sp.SlotStarts[len(sp.SlotStarts)-1].Sub(sp.BaseStart),
		minCalls: sp.Floors.MinCalls,
	}
}

// opScopeSeen — bir kapsamın son ilk cari kovası ve görülme anı.
type opScopeSeen struct {
	firstSlot, at time.Time
}

// opBaselineEntry — bir anahtarın durumu (opPivotCache.mu altında). base
// DEĞİŞMEZ: tazeleme yeni bir harita atar, okuyan birleşim kilitsiz okur.
type opBaselineEntry struct {
	base        map[OpPair]OpBaseline // nil = iyi taban yok
	end         time.Time             // pencere sonu (hariç)
	refreshedAt time.Time
	rows        int
	capped      bool      // son tazeleme OpBaselineLimit'e dayandı (base nil)
	nextTryAt   time.Time // hata / tavan sonrası bu andan önce tazeleme yok
	refreshing  bool
	lastUsed    time.Time
	seen        map[string]opScopeSeen
}

// opBaselineVerdict — opBaselineDecide'ın hükmü.
type opBaselineVerdict struct {
	use     bool          // önbellekli yol
	refresh bool          // arka planda tazele
	reason  string        // use=false iken eski yolun nedeni (metrik etiketi)
	lag     time.Duration // ilk cari kova − pencere sonu
}

// opBaselineDecide — SAF: girişin bu tur için hükmü (dosya başındaki tablo).
func opBaselineDecide(e opBaselineEntry, firstSlot, now time.Time) opBaselineVerdict {
	lag := firstSlot.Sub(e.end)
	v := opBaselineVerdict{lag: lag}
	due := !e.refreshing && !now.Before(e.nextTryAt)
	switch {
	case e.capped:
		v.reason = "capped"
		v.refresh = due
	case e.base == nil:
		v.reason = "no_baseline"
		v.refresh = due
	case lag < 0:
		v.reason = "ahead"
		v.refresh = due
	case lag > OpBaselineMaxLag:
		v.reason = "stale"
		v.refresh = due
	default:
		v.use = true
		v.refresh = due && lag >= OpBaselineRefreshEvery
	}
	return v
}

// opBaselineEnd — SAF: tazelemenin pencere sonu = son opBaselineScopeFresh
// içinde görülmüş kapsamların ilk cari kovalarının en erkeni (çağıranınki dahil).
func opBaselineEnd(seen map[string]opScopeSeen, firstSlot, now time.Time) time.Time {
	end := firstSlot
	for _, s := range seen {
		if now.Sub(s.at) <= opBaselineScopeFresh && s.firstSlot.Before(end) {
			end = s.firstSlot
		}
	}
	return end
}

// opPivotCacheable — SAF: önbellekli yol eski yolla eşdeğer mi? Tur sorgusu
// yalnız cari penceresinde satırı olan çiftleri döndürür; eşdeğerlik "cari
// verisi olmayan çift (cur_calls = cur_p99 = cur_p95 = 0) eski HAVING'den
// geçemez" şartına dayanır — tabanların en az biri pozitifse doğru (iki
// çağıranda da öyle). Hepsi ≤ 0 ya da kova yok → eski yol.
func opPivotCacheable(sp OpP99PivotSpec) bool {
	if len(sp.SlotStarts) == 0 {
		return false
	}
	f := sp.Floors
	return f.MinCalls > 0 || f.MinP99Ms > 0 || f.Ratio > 0 || sp.CurP95MinMs > 0
}

// opPivotCache — süreç-içi taban önbelleği (Store alanı; sıfır değeri hazır).
type opPivotCache struct {
	mu      sync.Mutex
	entries map[opBaselineKey]*opBaselineEntry
}

// opPivotIO — pivotun G/Ç dikişi (testte sahte; üretimde Store metotları).
type opPivotIO struct {
	legacy   func(ctx context.Context, sp OpP99PivotSpec) ([]OpP99Row, error)
	current  func(ctx context.Context, sp OpP99PivotSpec) ([]OpP99CurRow, error)
	baseline func(ctx context.Context, q string, args []any) (map[OpPair]OpBaseline, int, error)
	spawn    func(func())
	now      func() time.Time
}

// acquire — kilit altında hüküm; tazeleme gerekiyorsa giriş "refreshing"
// işaretlenir (anahtar başına tek uçuş) ve pencere sonu seçilir. Uzun süre
// kullanılmayan girişler süpürülür (bu anahtarınki sıfırlanır — liderliği
// kaybedip yeniden kazanan pod eski tabanı kullanmasın).
func (c *opPivotCache) acquire(key opBaselineKey, scope string, firstSlot, now time.Time) (map[OpPair]OpBaseline, opBaselineVerdict, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[opBaselineKey]*opBaselineEntry{}
	}
	for k, e := range c.entries {
		if !e.refreshing && now.Sub(e.lastUsed) > opBaselineEvictAfter {
			delete(c.entries, k)
		}
	}
	e := c.entries[key]
	if e == nil {
		e = &opBaselineEntry{}
		c.entries[key] = e
	}
	if e.seen == nil {
		e.seen = map[string]opScopeSeen{}
	}
	e.seen[scope] = opScopeSeen{firstSlot: firstSlot, at: now}
	e.lastUsed = now
	v := opBaselineDecide(*e, firstSlot, now)
	var end time.Time
	if v.refresh {
		e.refreshing = true
		end = opBaselineEnd(e.seen, firstSlot, now)
	}
	return e.base, v, end
}

// pivot — yol seçimi: önbellekli (tur sorgusu + JoinOpP99Pivot) ya da eski tek
// geçiş (OpP99PivotQuery). Tur sorgusu hatası çağırana döner (eski yola
// düşmez: zorlanan CH'ye ikinci ağır sorgu bindirilmesin); tavana dayanırsa
// eski yol (eksik küme birebirliği bozardı).
func (c *opPivotCache) pivot(ctx context.Context, scope string, sp OpP99PivotSpec, io opPivotIO) ([]OpP99Row, error) {
	if !opPivotCacheable(sp) {
		opPivotRecordRead(ctx, scope, "legacy", "uncacheable")
		return io.legacy(ctx, sp)
	}
	key := opBaselineKeyOf(sp)
	firstSlot := sp.SlotStarts[len(sp.SlotStarts)-1]
	base, v, end := c.acquire(key, scope, firstSlot, io.now())
	if v.refresh {
		// defer: bu turun okuması hata da verse panik de etse işaret bırakılmaz.
		defer io.spawn(func() { c.refresh(scope, key, end, io) })
	}
	reason := v.reason
	if v.use {
		opPivotRecordLag(ctx, scope, v.lag)
		cur, err := io.current(ctx, sp)
		if err != nil {
			return nil, err
		}
		if len(cur) < OpP99CurrentLimit {
			opPivotRecordRead(ctx, scope, "cached", "")
			return JoinOpP99Pivot(sp, cur, base), nil
		}
		reason = "current_capped"
		log.Printf("[op-pivot] %s: tur sorgusu satır tavanına (%d) dayandı — bu tur eski tek geçiş", scope, OpP99CurrentLimit)
	}
	if reason == "stale" {
		log.Printf("[op-pivot] %s: taban %s bayat (tavan %s) — eski tek geçiş", scope, v.lag, OpBaselineMaxLag)
	}
	opPivotRecordRead(ctx, scope, "legacy", reason)
	return io.legacy(ctx, sp)
}

// refresh — tabanı [end − lookback, end) için yeniden kurar (arka plan). Hata →
// son iyi taban korunur, OpBaselineRetryBackoff bekleme; LIMIT'e dayanma →
// taban kullanılmaz (capped), OpBaselineCappedBackoff bekleme. Panik
// tazelemeyi sonlandırır, süreci değil.
func (c *opPivotCache) refresh(scope string, key opBaselineKey, end time.Time, io opPivotIO) {
	start := io.now()
	var (
		base map[OpPair]OpBaseline
		n    int
		err  error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), opBaselineRefreshDeadline)
		defer cancel()
		q, args := OpBaselineQuery(end.Add(-key.lookback), end, key.minCalls)
		base, n, err = io.baseline(ctx, q, args)
	}()
	now := io.now()
	capped := err == nil && n >= OpBaselineLimit
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[opBaselineKey]*opBaselineEntry{}
	}
	e := c.entries[key]
	if e == nil {
		e = &opBaselineEntry{lastUsed: now}
		c.entries[key] = e
	}
	e.refreshing = false
	switch {
	case err != nil:
		// son iyi taban (varsa) korunur
		e.nextTryAt = now.Add(OpBaselineRetryBackoff)
	case capped:
		e.base, e.end, e.rows, e.capped, e.refreshedAt = nil, end, n, true, now
		e.nextTryAt = now.Add(OpBaselineCappedBackoff)
	default:
		e.base, e.end, e.rows, e.capped, e.refreshedAt = base, end, n, false, now
		e.nextTryAt = time.Time{}
	}
	c.mu.Unlock()

	dur := now.Sub(start)
	result := "ok"
	switch {
	case err != nil:
		result = "error"
		log.Printf("[op-pivot] %s: taban tazelemesi düştü (%v, %s) — son iyi taban korunuyor, %s sonra yeniden", scope, err, dur, OpBaselineRetryBackoff)
	case capped:
		result = "capped"
		log.Printf("[op-pivot] %s: taban tazelemesi satır tavanına (%d) dayandı — taban kullanılmıyor, eski tek geçiş; %s sonra yeniden", scope, OpBaselineLimit, OpBaselineCappedBackoff)
	default:
		log.Printf("[op-pivot] %s: taban tazelendi — %d çift, pencere sonu %s, %s", scope, n, end.UTC().Format(time.RFC3339), dur)
	}
	opPivotRecordRefresh(scope, result, dur, n, err == nil)
}

// ── Store bağlantısı ─────────────────────────────────────────────────────────

// OpP99Pivot — operasyon p99 pivotu (trace_op_latency ve yaygın yavaşlama):
// tabanı önbellekli yol, gerekirse eski tek geçiş (opPivotCache.pivot). Çıktı
// eski sorgunun satırlarıyla birebir (taban gecikmesi hariç — op_p99_cached.go).
func (s *Store) OpP99Pivot(ctx context.Context, scope string, sp OpP99PivotSpec) ([]OpP99Row, error) {
	return s.opPivot.pivot(ctx, scope, sp, opPivotIO{
		legacy:   s.opP99PivotLegacy,
		current:  s.opP99PivotCurrent,
		baseline: s.opP99Baseline,
		spawn:    func(f func()) { go f() },
		now:      time.Now,
	})
}

// opP99PivotLegacy — eski tek geçiş (OpP99PivotQuery), satırları OpP99Row.
func (s *Store) opP99PivotLegacy(ctx context.Context, sp OpP99PivotSpec) ([]OpP99Row, error) {
	q, args := OpP99PivotQuery(sp)
	rows, err := s.telemetryReadConn().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OpP99Row{}
	for rows.Next() {
		var r OpP99Row
		if err := rows.Scan(OpP99PivotScanDest(&r, sp)...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// opP99PivotCurrent — tur sorgusu (OpP99CurrentQuery).
func (s *Store) opP99PivotCurrent(ctx context.Context, sp OpP99PivotSpec) ([]OpP99CurRow, error) {
	q, args := OpP99CurrentQuery(sp)
	rows, err := s.telemetryReadConn().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OpP99CurRow{}
	for rows.Next() {
		var r OpP99CurRow
		var isBatch uint8
		if err := rows.Scan(OpP99CurrentScanDest(&r, sp, &isBatch)...); err != nil {
			return nil, err
		}
		r.IsBatch = isBatch != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// opP99Baseline — taban tazelemesinin okuması (OpBaselineQuery); n = dönen satır.
func (s *Store) opP99Baseline(ctx context.Context, q string, args []any) (map[OpPair]OpBaseline, int, error) {
	rows, err := s.telemetryReadConn().Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := map[OpPair]OpBaseline{}
	n := 0
	for rows.Next() {
		var p OpPair
		var b OpBaseline
		if err := rows.Scan(&p.Service, &p.Operation, &b.P99Ms, &b.P95Ms, &b.Calls, &b.Buckets); err != nil {
			return nil, 0, err
		}
		out[p] = b
		n++
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, n, nil
}

// ── Öz-gözlem (selfobs) ──────────────────────────────────────────────────────
//
// Enstrümanlar ilk kullanımda kurulur (selfobs.Init'ten SONRA — paket init'inde
// kurulsa noop meter'a bağlanırdı). Etiketler sabit kümeler: scope (2),
// path (cached / legacy), reason (≤ 7), result (ok / error / capped).

var (
	opPivotMetricsOnce sync.Once
	opPivotReads       otelmetric.Int64Counter
	opBaselineDuration otelmetric.Float64Histogram
	opBaselineRows     otelmetric.Int64Gauge
	opBaselineAge      otelmetric.Float64Gauge
)

func opPivotMetrics() {
	opPivotMetricsOnce.Do(func() {
		m := selfobs.Meter()
		var err error
		if opPivotReads, err = m.Int64Counter("op_pivot_reads_total",
			otelmetric.WithDescription("Operasyon p99 pivotu okumaları: path=cached (taban önbelleği + cari kovalar) / legacy (24 sa tek geçiş), reason = eski yolun nedeni")); err != nil {
			log.Printf("[op-pivot] metric op_pivot_reads_total: %v", err)
		}
		if opBaselineDuration, err = m.Float64Histogram("op_baseline_refresh_duration_seconds",
			otelmetric.WithDescription("Operasyon 24 sa taban önbelleği tazeleme süresi"), otelmetric.WithUnit("s")); err != nil {
			log.Printf("[op-pivot] metric op_baseline_refresh_duration_seconds: %v", err)
		}
		if opBaselineRows, err = m.Int64Gauge("op_baseline_rows",
			otelmetric.WithDescription("Son başarılı taban tazelemesinin satır (çift) sayısı")); err != nil {
			log.Printf("[op-pivot] metric op_baseline_rows: %v", err)
		}
		if opBaselineAge, err = m.Float64Gauge("op_baseline_age_seconds",
			otelmetric.WithDescription("Kullanılan taban önbelleğinin gecikmesi: ilk cari kova − önbellek pencere sonu"), otelmetric.WithUnit("s")); err != nil {
			log.Printf("[op-pivot] metric op_baseline_age_seconds: %v", err)
		}
	})
}

func opPivotRecordRead(ctx context.Context, scope, path, reason string) {
	opPivotMetrics()
	if opPivotReads != nil {
		opPivotReads.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("scope", scope),
			attribute.String("path", path), attribute.String("reason", reason)))
	}
}

func opPivotRecordLag(ctx context.Context, scope string, lag time.Duration) {
	opPivotMetrics()
	if opBaselineAge != nil {
		opBaselineAge.Record(ctx, lag.Seconds(), otelmetric.WithAttributes(attribute.String("scope", scope)))
	}
}

func opPivotRecordRefresh(scope, result string, dur time.Duration, rows int, ok bool) {
	opPivotMetrics()
	ctx := context.Background()
	attrs := otelmetric.WithAttributes(attribute.String("scope", scope))
	if opBaselineDuration != nil {
		opBaselineDuration.Record(ctx, dur.Seconds(), otelmetric.WithAttributes(attribute.String("scope", scope), attribute.String("result", result)))
	}
	if ok && opBaselineRows != nil {
		opBaselineRows.Record(ctx, int64(rows), attrs)
	}
}
