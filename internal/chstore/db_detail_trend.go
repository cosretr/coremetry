package chstore

import (
	"context"
	"fmt"
	"time"
)

// db_detail_trend.go — v0.10.1095 — /database detayının üç grafiği
// (Calls/s · Error % · P99) için TEK veritabanı trend okuması.
//
// Operatör kararı ("Önerin A yapalım", docs/DECISIONS.md 2026-10-04):
// grafikler db_summary_5m'in 5 dk kovalarını çiziyordu, 10 dakikalık bir
// DB olayı tek nokta oluyordu. Kısa pencerede (≤ 3 sa) kaynak artık
// db_summary_1m (1 dk kova, TTL 7 gün); uzun pencerede bugünkü gibi
// db_summary_5m.
//
// NEDEN AYRI OKUMA, /api/databases/trends DEĞİL: o uç filo geneli (her
// veritabanının sparkline'ı + sağlık rozeti) ve /databases tablosu onu
// 5 dk ızgarasıyla tüketiyor (dbTrendBucketSeconds, Cur* son TAM kova).
// Uca grenlik parametresi eklemek tablonun rozet kuralını da değiştirirdi;
// burada kimlik üçlüsü PK önekinde süzülür, okuma tek DB'nin kovalarıdır.
//
// Karolar (CALLS / ERRORS / …) DEĞİŞMEDİ: GetDatabaseDetail →
// dbDetailAggregate (db_caller_summary_5m). db-health dedektörü de 5m'de
// kalır (db_health.go) — bu dosya yalnız grafiklerin kaynağıdır.
// Statement detayı 5m'de kalır: MV'si ifade başına (db_statement_summary_5m),
// 1 dk ikizi yok.

// dbTrendFineMaxWindow — bu süreye kadar (dahil) pencereler 1 dk kovadan
// okunur. 3 sa = 180 nokta: 150 px'lik üç panelde okunur kalır, operatör
// kararıyla sabit (servis grafiklerinin metricAutoStep merdiveni 6 sa'e
// kadar 60 sn verir; DB tarafı daha temkinli).
const dbTrendFineMaxWindow = 3 * time.Hour

// DB detay trend kaynakları — SQL'e YALNIZ bu sabitlerden girer.
const (
	dbTrendTableFine   = "db_summary_1m"
	dbTrendTableCoarse = "db_summary_5m"
)

// DBTrendGrain — detay trendinin kaynağı: tablo + kova genişliği (sn).
// Önbellek anahtarı BucketSec'i taşır (api/db_detail_trend.go): kapsama
// değişince (1m tablo yeni doldu) eski 5m yükü 1m anahtarında servis
// edilmesin.
type DBTrendGrain struct {
	Table     string
	BucketSec int
}

// dbTrendGrainFor — SAF seçici (tablo testi db_detail_trend_test.go).
//
//	pencere ≤ 3 sa VE 1m tablo pencere başını kapsıyor → db_summary_1m, 60 sn
//	aksi hâlde                                        → db_summary_5m, 300 sn
//
// fineCovers=false üç durumu birden taşır: tablo yok (küme kipinde DDL
// ertelendi), boş, ya da ilk kovası pencere başından sonra (geriye dolmaz /
// TTL 7 günü aştı). Hepsinde 5m'e SESSİZCE düşülür — eksik bir 1m serisi
// çizmektense doğru bir 5m serisi.
func dbTrendGrainFor(from, to time.Time, fineCovers bool) DBTrendGrain {
	if w := to.Sub(from); w > 0 && w <= dbTrendFineMaxWindow && fineCovers {
		return DBTrendGrain{Table: dbTrendTableFine, BucketSec: 60}
	}
	return DBTrendGrain{Table: dbTrendTableCoarse, BucketSec: 300}
}

// DBDetailTrendGrain — pencere için kaynağı seçer. Kapsama probu yalnız
// kısa pencerede koşar (uzun pencerede cevap zaten 5m); prob 60 sn
// önbellekli (sourceCovers), hata → kapsamıyor. nil Store → 5m.
func (s *Store) DBDetailTrendGrain(ctx context.Context, from, to time.Time) DBTrendGrain {
	fine := false
	if w := to.Sub(from); w > 0 && w <= dbTrendFineMaxWindow {
		fine = s.sourceCovers(ctx, priorSrcDBSummary1m, from.Truncate(time.Minute))
	}
	return dbTrendGrainFor(from, to, fine)
}

// dbDetailTrendSQL — SAF: tek veritabanının kova serisi. SQL golden testi
// db_detail_trend_test.go. Üç sınır (sert kısıt): zaman-sınırlı WHERE
// (`>= from` kovaya hizalı, `< to` — v0.9.823 sözleşmesi), LIMIT, süre
// tavanı. Kimlik üçlüsü ORDER BY önekinde (db_system, instance, db_name):
// okuma birkaç granül. nameSQL dbDetailNameFilter'dan (boş dbName =
// "bu instance'taki tüm veritabanları", karolarla AYNI kapsam).
//
// LIMIT 30000: 5m'de 90 gün = 25 920 kova; 1m yolu ≤ 181 kova.
func dbDetailTrendSQL(table, nameSQL string) string {
	if table != dbTrendTableFine {
		table = dbTrendTableCoarse
	}
	return `
		SELECT toUnixTimestamp64Nano(toDateTime64(time_bucket, 9))                     AS bucket_ns,
		       countMerge(span_count_state)                                            AS span_count,
		       countIfMerge(error_count_state)                                         AS error_count,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p99_ms
		FROM ` + table + `
		WHERE db_system = ? AND instance = ?` + nameSQL + `
		  AND time_bucket >= ? AND time_bucket < ?
		GROUP BY time_bucket
		ORDER BY time_bucket
		LIMIT 30000
		SETTINGS max_execution_time = 10`
}

// DBDetailTrend — /database detay grafiklerinin yükü. Points artan zamanlı,
// boş kova yok (DBTrendPoint sözleşmesi); Rps = kova çağrısı / BucketSec.
type DBDetailTrend struct {
	BucketSec int            `json:"bucketSec"`
	Source    string         `json:"source"` // db_summary_1m | db_summary_5m
	Points    []DBTrendPoint `json:"points"`
}

// GetDBDetailTrend — g'nin tablosundan (system, instance, dbName) kimliğinin
// kova serisi. instance "" → 'unknown' (GetDatabaseDetail ile aynı eşleme).
func (s *Store) GetDBDetailTrend(
	ctx context.Context, g DBTrendGrain, system, instance, dbName string, from, to time.Time,
) (*DBDetailTrend, error) {
	if g.Table != dbTrendTableFine {
		g = DBTrendGrain{Table: dbTrendTableCoarse, BucketSec: 300}
	}
	out := &DBDetailTrend{BucketSec: g.BucketSec, Source: g.Table, Points: []DBTrendPoint{}}
	if system == "" {
		return out, nil
	}
	mvInstance := instance
	if mvInstance == "" {
		mvInstance = "unknown"
	}
	nameSQL, nameArgs := dbDetailNameFilter("db_name", dbName)
	bucketStart := from.Truncate(time.Duration(g.BucketSec) * time.Second)
	args := append([]any{system, mvInstance}, nameArgs...)
	args = append(args, bucketStart, to)
	rows, err := s.telemetryReadConn().Query(ctx, dbDetailTrendSQL(g.Table, nameSQL), args...)
	if err != nil {
		return nil, fmt.Errorf("db detay trendi (%s): %w", g.Table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			bucketNs              int64
			spanCount, errorCount uint64
			p99Ms                 *float64
		)
		if err := rows.Scan(&bucketNs, &spanCount, &errorCount, &p99Ms); err != nil {
			return nil, err
		}
		out.Points = append(out.Points, dbTrendPointOf(bucketNs, spanCount, errorCount, p99Ms, g.BucketSec))
	}
	return out, rows.Err()
}

// dbTrendPointOf — SAF: bir kovanın noktası. Hız kovanın KENDİ genişliğine
// bölünür (1m'de 60, 5m'de 300) — sabit 300'e bölmek 1m serisini 5× düşük
// çizerdi.
func dbTrendPointOf(bucketNs int64, spanCount, errorCount uint64, p99Ms *float64, bucketSec int) DBTrendPoint {
	if bucketSec <= 0 {
		bucketSec = 300
	}
	pt := DBTrendPoint{
		T:     bucketNs,
		Rps:   float64(spanCount) / float64(bucketSec),
		P99Ms: safeF(p99Ms),
	}
	if spanCount > 0 {
		pt.ErrorRate = float64(errorCount) / float64(spanCount) * 100
	}
	return pt
}
