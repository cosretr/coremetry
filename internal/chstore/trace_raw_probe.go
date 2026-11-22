package chstore

import (
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// trace_raw_probe.go — HAM YOLDA YENİLİK DİLİMİ (v0.10.126, perf bütçesi P2).
//
// ── ÖLÇÜM (docs/perf/perf-budget-2026-08-28.md §2.2/§3, lokal) ───────────
//
// Terfisiz attribute filtresi MV'yi diskalifiye eder; liste ham `spans`
// üzerinde GROUP BY trace_id koşar. 24h pencerede 1.54M satır / 273 MiB
// okunup 50 satır döner (30.800:1), perfcheck p50 4.19 s. `LIMIT` ÇIKTIYI
// sınırlar, İŞİ değil: spans ORDER BY (service_name, time) olduğu için
// GROUP BY trace_id pencerenin her trace'ini hash tablosuna koyar, sonra
// sıralar — maliyet pencere genişliğiyle lineer.
//
// ── FİKİR ────────────────────────────────────────────────────────────────
//
// Varsayılan sıralama zaman-DESC: operatör "en yeni 50"yi ister ve en yeni
// 50 trace hemen her zaman pencerenin son saatinde başlamıştır. Önce dar
// bir kuyruk penceresi taranır; K = offset+limit+1 kesin satır çıkarsa
// sayfa tam taramayla BİRE BİR aynıdır, çıkmazsa basamak genişler
// (1h → 6h → 24h → tam pencere). trace_slice.go'nun MV yolunda yaptığının
// ham karşılığı. Kötü durumda (seyrek eşleşme, 24h) fazladan basamaklar
// ≈ tam taramanın %40'ı; iyi durumda 24h yerine 2h taranır.
//
// ── DOĞRULUK — floor'a binen trace ───────────────────────────────────────
//
// floor = to-W. floor'dan ÖNCE başlamış ama sonrasında span'i olan bir
// trace dar pencerede KISMİ toplanır (trace_start geç, span_count eksik,
// dur_ms kısa) — sessiz ve makul görünen yanlış satır. Çare: tarama
// floor'un L (1h) altından başlar ama yalnız trace_start ≥ floor olan
// satırlar KESİN sayılır: böyle bir trace'in bütün span'leri floor'un
// üstünde, yani taranan pencerenin içindedir; toplamı tam. trace_start <
// floor olan satırlar şüphelidir (daha erken span'leri olabilir) ve DESC
// sırada kesinlerin ALTINA düşer — K satırın hepsi kesinse şüpheli hiçbir
// satır sayfaya giremez. Kabul edilen kusur (runTraceStage2'nin floor
// tespitiyle aynı sınıf): floor'un altında L'den uzun span boşluğu olan
// bir trace floor'un üstünde "kesin" görünür.
//
// Sayım sorguları (exact/approx) DEĞİŞMEZ — ayrı sorgudur, tam pencere.

// traceRawProbeRungs — kuyruk pencereleri, dar→geniş.
// v0.10.2466 — 15 dk basamağı eklendi (operatör: 15 dk → 1 sa → 6 sa → tam)
// ve uygunluk (W+L)*3/2 ≤ pencere'ye gevşedi: 12 saatlik pencere artık
// 15m, 1h, 6h basamaklarını dener. Tam tarama bütçeyi aşarsa en geniş
// başarılı basamağın KESİN satırları kısmi sonuç olarak döner
// (traceRawProbeExact) — basamak ne kadar genişse kısmi cevap o kadar dolu.
var traceRawProbeRungs = []time.Duration{15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}

// traceRawProbeLookback — floor'un altına bakış (L) üst sınırı. MV yolunun
// 12 kova (1h) lookback'iyle aynı.
const traceRawProbeLookback = time.Hour

// traceRawProbeLookbackFor — v0.10.2466: L = min(1h, W). 15 dk basamağı
// 75 dk değil 30 dk tarar (prod ≈ 27k span/s: 49M yerine 24M span). Kabul
// edilen kusur aynı sınıf: floor'un altında L'den uzun span boşluğu olan
// trace "kesin" görünür; 15 dk'lık boşluklu trace pratikte yok.
func traceRawProbeLookbackFor(w time.Duration) time.Duration {
	if w < traceRawProbeLookback {
		return w
	}
	return traceRawProbeLookback
}

// traceRawProbeMaxLimit — liste sayfaları için; CSV dışa aktarımı (50k)
// K=50001 kesin satır bulamaz, boşuna basamak tarardı.
const traceRawProbeMaxLimit = 500

// traceRawProbeWindows — pencereye sığan basamaklar. Saf; tablo-testli.
func traceRawProbeWindows(from, to time.Time) []time.Duration {
	if from.IsZero() || to.IsZero() || !to.After(from) {
		return nil
	}
	win := to.Sub(from)
	var out []time.Duration
	for _, w := range traceRawProbeRungs {
		if (w+traceRawProbeLookbackFor(w))*3/2 <= win {
			out = append(out, w)
		}
	}
	return out
}

// traceRawProbeEligible — yalnız zaman-DESC liste sayfaları. Süre/servis
// sıralaması tüm pencereyi görmek zorunda; id listesi zaten sınırlı.
func traceRawProbeEligible(f TraceFilter) bool {
	if f.Sort != "" && f.Sort != "time" {
		return false
	}
	if f.Order == "asc" {
		return false
	}
	if f.TraceID != "" || len(f.TraceIDs) > 0 {
		return false
	}
	if f.Limit <= 0 || f.Limit > traceRawProbeMaxLimit || f.Offset < 0 {
		return false
	}
	return len(traceRawProbeWindows(f.From, f.To)) > 0
}

// traceRawProbePage — dar pencere sonucundan sayfayı keser. rows
// trace_start DESC sıralı (LIMIT K OFFSET 0). ok=false: K satır yok ya da
// K'nın içinde şüpheli (trace_start < floor) var → basamak genişler.
// Saf; tablo-testli.
func traceRawProbePage(rows []TraceRow, floorNs int64, offset, limit int) (page []TraceRow, hasMore, ok bool) {
	if limit <= 0 || offset < 0 {
		return nil, false, false
	}
	k := offset + limit + 1
	if len(rows) < k {
		return nil, false, false
	}
	for _, r := range rows[:k] {
		if r.StartTime < floorNs {
			return nil, false, false
		}
	}
	page = rows[offset:k]
	hasMore = len(page) > limit
	if hasMore {
		page = page[:limit]
	}
	return page, hasMore, true
}

// traceRawProbeExact — v0.10.2466 (kısmi sonuç): bir sonraki basamak ya da
// tam tarama bütçeyi aştığında, son başarılı basamağın yalnız KESİN satırları
// (trace_start ≥ floor; DESC sıralı) üzerinden sayfa. Bu satırlar tam
// taramanın en yeni satırlarıyla bire bir aynıdır — eksik olan yalnız
// floor'dan ESKİ trace'ler (yanıt "kısmi: floor sonrası" der). SAF; testli.
func traceRawProbeExact(rows []TraceRow, floorNs int64, offset, limit int) (page []TraceRow, hasMore bool) {
	page = []TraceRow{}
	if limit <= 0 || offset < 0 {
		return page, false
	}
	n := 0
	for n < len(rows) && rows[n].StartTime >= floorNs {
		n++
	}
	if offset >= n {
		return page, false
	}
	end := offset + limit
	if end > n {
		end = n
	}
	return rows[offset:end], n > offset+limit
}

// scanTraceListRows — buildGetTracesListSQL satırlarını çözer (probe ve
// tam tarama aynı şekli okur).
func scanTraceListRows(rows driver.Rows) ([]TraceRow, error) {
	out := []TraceRow{}
	for rows.Next() {
		var t TraceRow
		var hasErr uint8
		var ts time.Time
		if err := rows.Scan(&t.TraceID, &t.RootName, &t.ServiceName, &t.RootRoute, &ts, &t.DurationMs, &t.SpanCount, &hasErr, &t.ErrorSpans); err != nil {
			return nil, err
		}
		t.StartTime = ts.UnixNano()
		t.HasError = hasErr == 1
		out = append(out, t)
	}
	return out, rows.Err()
}
