package chstore

// problem_breakdown.go — v0.10.2388 (triage mockup dilim 3: Problem detayında
// "Hata nerede" bölümü; uç internal/api/problem_breakdown.go). Hata oranı
// Problem'inin kırılımları için dört SINIRLI okuma + olağan oranın saf hesabı.
// Hepsi tek servise kapsanır; hiçbiri liste yolunda koşmaz (yalnız detay ucu).
//
//  1. MLRouteErrorBaseline — rotaların mevsimsel tabanı. Kaynak dedektörün
//     KENDİ serisi (ml_series, signal=trace; parça `<rota>|count` ve
//     `<rota>|err_ratio`) ve dedektörün KENDİ dilimi (aynı takvim sınıfı günler
//     × ±1 saat; günleri çağıran verir: ml.SeasonalBaselineSlice). MLBaseline
//     aynı dilimi FİLONUN tamamı için okur (≤ 20.000 seri, 10 sn bütçe) — tik
//     için doğru, bir sayfa isteği için değil. Burada ORDER BY öneki
//     (signal, service) ile tek servisin satırları taranır.
//  2. ServiceErrorExceptionTypes — pencerede HATA durumlu span'lerdeki
//     exception kayıtlarının türe göre sayımı. Ham `spans`, çünkü exception
//     sayımının rollup'ı yok (exception_groups ömür toplamı taşır; pencereli
//     sayı uydurulmaz — v0.9.524 dersi). Sınırlı drill-down sınıfı
//     (occurrencesQuery emsali): tek servis + zaman aralığı + ex_match,
//     LIMIT, max_execution_time; birincil anahtar (service_name, time) aralığı.
//     status_code = 'error' süzgeci BİLİNÇLİ: collector kuyruk örneklemesi
//     başarısız izleri tam tutar, başarılı izleri örnekler; hata durumlu
//     span'lerin sayımı TAMDIR, başarılı iz içindeki (yakalanmış) exception
//     kayıtlarınınki değildir — ikisi aynı paya karıştırılmaz.
//  3. NewestExceptionGroupsByType — o türlerin exception_groups'taki en yeni
//     grubu (en yeni örnek mesaj + grup sayfası bağlantısı). Grup satırı
//     5 dakikalık işçiyle gelir: yeni tür için henüz satır olmayabilir.
//  4. ServiceDependencyEvents — servisin bağımlılık dedektörü olayları
//     (dependency_error_rate / dependency_latency), hafif kolonlar (kanıt /
//     kayıt gövdesi okunmaz).

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
)

// ── 1. Rota tabanı ──────────────────────────────────────────────────────

// MLRouteBaselineRequest — tek servisin rota tabanı dilimi.
type MLRouteBaselineRequest struct {
	Service   string
	Dates     []string // takvim sınıfı eşleşen günler ("2026-11-12" …)
	HourFrom  int      // yerel saat penceresi [HourFrom, HourTo]
	HourTo    int
	TargetSec int // normalize hedefi (dedektörün kovası)
	TZ        string
}

// MLRouteBaseline — bir rotanın taban dilimi.
type MLRouteBaseline struct {
	// CountPos — trafiği olan (count > 0) taban kovası sayısı.
	CountPos int
	// CountMed — taban trafiğinin medyanı (istek/sn; ağırlık olarak kullanılır).
	CountMed float64
	// ErrRatios — hata oranı örnekleri (0–1). Dedektör oranı yalnız VM hata
	// serisi döndürdüğünde yazar: hiç 5xx üretmemiş rotada örnek YOKTUR.
	ErrRatios []float64
}

// MLRouteBaselineCap — okunan rota tavanı (servis başına).
const MLRouteBaselineCap = 2000

// mlRouteBaselineSQL — SAF: okuma sorgusu (test). MLBaseline ile aynı
// normalizasyon (count · hedef / bucket_sec) ve aynı dilim süzgeci; fark
// servis kapsamı + yalnız iki ölçü. Bağımlılık serileri (`dep|…`) aynı
// sinyalde yaşar ve `|err_ratio` ile biter — rota değildir, dışarıda.
// FINAL yok (MLBaseline gerekçesi: çift satır iki örnek olur, medyan dayanıklı).
func mlRouteBaselineSQL() string {
	return `
		SELECT route,
		       toUInt32(countIf(m = 'count' AND v > 0))    AS count_pos,
		       quantileExactIf(0.5)(v, m = 'count')        AS count_med,
		       groupArrayIf(v, m = 'err_ratio')            AS err_ratios
		FROM (
		  SELECT if(endsWith(part, '|count'), 'count', 'err_ratio')                              AS m,
		         substring(part, 1, length(part) - if(endsWith(part, '|count'), 6, 10))          AS route,
		         count * ? / bucket_sec                                                          AS v
		  FROM ml_series
		  WHERE signal = ? AND service = ?
		    AND (endsWith(part, '|count') OR endsWith(part, '|err_ratio'))
		    AND NOT startsWith(part, 'dep|')
		    AND toDate(bucket, ?) IN (?)
		    AND toHour(bucket, ?) BETWEEN ? AND ?
		    AND bucket >= ?
		)
		GROUP BY route
		ORDER BY route
		LIMIT ?
		SETTINGS max_execution_time = 5`
}

// MLRouteErrorBaseline — servisin rotalarının taban dilimi (rota → taban).
// Gün listesi boşsa okuma yok. truncated: tavan doldu (rota sayısı > tavan).
func (s *Store) MLRouteErrorBaseline(ctx context.Context, req MLRouteBaselineRequest) (map[string]MLRouteBaseline, bool, error) {
	out := map[string]MLRouteBaseline{}
	if req.Service == "" || len(req.Dates) == 0 || req.TargetSec <= 0 {
		return out, false, nil
	}
	tz := req.TZ
	if tz == "" {
		tz = "UTC"
	}
	rows, err := s.conn.Query(ctx, mlRouteBaselineSQL(),
		float64(req.TargetSec), MLSignalTrace, req.Service, tz, req.Dates, tz, req.HourFrom, req.HourTo,
		time.Now().Add(-35*24*time.Hour).UTC(), // bucket DateTime: DateTime64 bind YOK (AST pini)
		MLRouteBaselineCap+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	truncated := false
	for rows.Next() {
		var route string
		var b MLRouteBaseline
		var pos uint32
		if err := rows.Scan(&route, &pos, &b.CountMed, &b.ErrRatios); err != nil {
			return nil, false, err
		}
		if len(out) >= MLRouteBaselineCap {
			truncated = true
			continue
		}
		b.CountPos = int(pos)
		if math.IsNaN(b.CountMed) || math.IsInf(b.CountMed, 0) || b.CountMed < 0 {
			b.CountMed = 0 // count örneği yok → quantileExactIf nan
		}
		out[route] = b
	}
	return out, truncated, rows.Err()
}

// RouteNormalErrRate — SAF: rotanın olağan hata oranı (0–1) = taban diliminin
// medyanı. Trafiği olup hata örneği olmayan kova 0 sayılır (dedektör hata
// oranını yalnız VM bir hata serisi döndürdüğünde yazar; seri yoksa o kovada
// hata YOKTU — eksik ölçüm değil). Örnek sayısı = max(trafikli kova, hata
// örneği); minSamples'ın altındaysa ok=false: olağan düzey bilinmiyor.
func RouteNormalErrRate(b MLRouteBaseline, minSamples int) (float64, bool) {
	if minSamples < 1 {
		minSamples = 1
	}
	xs := make([]float64, 0, len(b.ErrRatios))
	for _, v := range b.ErrRatios {
		if finiteNonNeg(v) {
			xs = append(xs, math.Min(v, 1))
		}
	}
	zeros := 0
	if b.CountPos > len(xs) {
		zeros = b.CountPos - len(xs)
	}
	n := zeros + len(xs)
	if n < minSamples {
		return 0, false
	}
	sort.Float64s(xs)
	at := func(i int) float64 { // sıfır dolgulu sıralı dizinin i. elemanı
		if i < zeros {
			return 0
		}
		return xs[i-zeros]
	}
	if n%2 == 1 {
		return at(n / 2), true
	}
	return (at(n/2-1) + at(n/2)) / 2, true
}

// ── 2. Exception türleri ────────────────────────────────────────────────

// ExceptionTypeCount — pencerede bir exception türünün kayıt sayısı.
type ExceptionTypeCount struct {
	Type  string
	Count uint64
}

// ExceptionTypeCap — okunan tür tavanı; dolarsa toplam eksiktir (truncated).
const ExceptionTypeCap = 100

// serviceErrorExceptionTypesSQL — SAF: sorgu (test). exFragments tek tanım:
// tür çözümü öteki beş exception sorgusuyla aynı.
func serviceErrorExceptionTypesSQL(hasCols bool, shardSkip string) string {
	f := exFragments(hasCols)
	return `
		SELECT ` + f.Type + ` AS ex_type,
		       count() AS cnt
		FROM spans
		WHERE service_name = ? AND time >= ? AND time <= ?
		  AND status_code = 'error'
		  AND ` + f.Match + `
		GROUP BY ex_type
		ORDER BY cnt DESC, ex_type
		LIMIT ?
		SETTINGS max_execution_time = 5,
		         ` + shardSkip
}

// ServiceErrorExceptionTypes — [from, to] penceresinde servisin hata durumlu
// span'lerindeki exception kayıtları, türe göre (çoktan aza). truncated: tür
// tavanı doldu → dönen satırların toplamı pencerenin toplamı DEĞİLDİR.
func (s *Store) ServiceErrorExceptionTypes(ctx context.Context, service string, from, to time.Time) ([]ExceptionTypeCount, bool, error) {
	if service == "" || !to.After(from) {
		return nil, false, nil
	}
	rows, err := s.conn.Query(ctx, serviceErrorExceptionTypesSQL(s.hasExCols, s.shardSkipSetting()),
		service, from.UTC(), to.UTC(), ExceptionTypeCap+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []ExceptionTypeCount
	truncated := false
	for rows.Next() {
		var r ExceptionTypeCount
		if err := rows.Scan(&r.Type, &r.Count); err != nil {
			return nil, false, err
		}
		if len(out) >= ExceptionTypeCap {
			truncated = true
			continue
		}
		out = append(out, r)
	}
	return out, truncated, rows.Err()
}

// ── 3. Türün en yeni grubu ──────────────────────────────────────────────

// ExceptionTypeGroup — bir türün (servis içinde) en yeni exception grubu.
type ExceptionTypeGroup struct {
	Type        string
	Fingerprint string // en son görülen grubun parmak izi (grup sayfası)
	Message     string // o grubun mesajı (grup en yeni örneğin mesajını taşır)
	Groups      uint64 // pencereden beri görülen grup sayısı (bu türde)
	LastSeen    int64  // unix ns
}

// exceptionGroupsByTypeSQL — SAF: sorgu (test). Oracle grupları (`ora:`) span
// exception'ı değildir, dışarıda.
func exceptionGroupsByTypeSQL() string {
	return `
		SELECT ex_type,
		       argMax(fingerprint, last_seen)             AS fp,
		       argMax(ex_message, last_seen)              AS msg,
		       toUInt64(count())                          AS groups,
		       toUnixTimestamp64Nano(max(last_seen))      AS seen
		FROM exception_groups FINAL
		WHERE service = ? AND ex_type IN (?)
		  AND last_seen >= toDateTime64(?, 9, 'UTC')
		  AND ` + NotOracleGroupSQL + `
		GROUP BY ex_type
		LIMIT ?
		SETTINGS max_execution_time = 5`
}

// NewestExceptionGroupsByType — tür → en yeni grup (since'ten beri görülen).
// Boş tür listesi = okuma yok.
func (s *Store) NewestExceptionGroupsByType(ctx context.Context, service string, types []string, since time.Time) (map[string]ExceptionTypeGroup, error) {
	out := map[string]ExceptionTypeGroup{}
	if service == "" || len(types) == 0 {
		return out, nil
	}
	if len(types) > ExceptionTypeCap {
		types = types[:ExceptionTypeCap]
	}
	rows, err := s.conn.Query(ctx, exceptionGroupsByTypeSQL(), service, types, chDateTime64Arg(since), ExceptionTypeCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g ExceptionTypeGroup
		if err := rows.Scan(&g.Type, &g.Fingerprint, &g.Message, &g.Groups, &g.LastSeen); err != nil {
			return nil, err
		}
		out[g.Type] = g
	}
	return out, rows.Err()
}

// ── 4. Bağımlılık olayları ──────────────────────────────────────────────

// Bağımlılık dedektörleri (ml/dependency.go; ad sözleşmesi MLTraceDetectorNames).
const (
	MLDetDependencyErrorRate = "dependency_error_rate"
	MLDetDependencyLatency   = "dependency_latency"
)

// DependencyEvent — bağımlılık olayının hafif satırı.
type DependencyEvent struct {
	ID        string
	Detector  string
	Code      string // "<aile>|<hedef>" (ml depPart; aile http | db | messaging)
	Stage     string
	WindowSec uint32
	StartedAt int64 // unix ns
	LastSeen  int64 // unix ns
	Baseline  float64
	Observed  float64
}

// DependencyEventCap — okunan olay tavanı (tek servis).
const DependencyEventCap = 50

// serviceDependencyEventsSQL — SAF: sorgu (test). Hafif kolonlar; pencereyle
// ÖRTÜŞEN olaylar (son görülme ≥ baş, başlangıç ≤ son), yalnız yukarı yön.
func serviceDependencyEventsSQL() string {
	return `
		SELECT id, detector, code, stage, window_sec, started_at, last_seen, baseline, observed
		FROM ml_events FINAL
		WHERE service = ?
		  AND detector IN ('` + MLDetDependencyErrorRate + `', '` + MLDetDependencyLatency + `')
		  AND direction = 'up'
		  AND last_seen >= ? AND started_at <= ?
		ORDER BY last_seen DESC, id
		LIMIT ?
		SETTINGS max_execution_time = 5`
}

// ServiceDependencyEvents — servisin [from, to] ile örtüşen bağımlılık
// olayları (en yeni önce, ≤ DependencyEventCap).
func (s *Store) ServiceDependencyEvents(ctx context.Context, service string, from, to time.Time) ([]DependencyEvent, error) {
	if service == "" || to.Before(from) {
		return nil, nil
	}
	rows, err := s.conn.Query(ctx, serviceDependencyEventsSQL(), service, chDateTime64Arg(from), chDateTime64Arg(to), DependencyEventCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DependencyEvent
	for rows.Next() {
		var e DependencyEvent
		var st, ls time.Time
		if err := rows.Scan(&e.ID, &e.Detector, &e.Code, &e.Stage, &e.WindowSec, &st, &ls, &e.Baseline, &e.Observed); err != nil {
			return nil, err
		}
		e.StartedAt, e.LastSeen = st.UnixNano(), ls.UnixNano()
		out = append(out, e)
	}
	return out, rows.Err()
}

// DependencyTargetOf — SAF: olay kodu "<aile>|<hedef>" → (aile, hedef). Hedef
// kendi içinde `|` taşıyabilir. Biçim tutmuyorsa ikisi de boş.
func DependencyTargetOf(code string) (family, target string) {
	family, target, ok := strings.Cut(code, "|")
	if !ok || family == "" || target == "" {
		return "", ""
	}
	return family, target
}
