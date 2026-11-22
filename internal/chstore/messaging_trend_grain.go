package chstore

import (
	"context"
	"time"
)

// messaging_trend_grain.go — v0.10.2450 (operatör 2026-11-22: "messaging
// özeti 1 dakikalık çözünürlükte olsun"). /messaging satır trendlerinin
// (GetMessagingTrends → /api/messaging/trends) kaynak seçimi.
//
// db_summary_1m / dbTrendGrainFor (v0.10.1095) emsali: kısa pencerede
// (≤ 3 sa) messaging_summary_1m, uzun pencerede ya da 1m tablo pencere
// başını kapsamıyorsa (geriye dolmaz, TTL 7 gün, küme kipinde DDL
// ertelendi, boş) messaging_summary_5m. Hiçbir durumda boşluk çizilmez:
// eksik bir 1m serisi yerine doğru bir 5m serisi.
//
// Pencere TOPLAMLARI (liste, çekmece, operasyon tablosu, prior kıyas)
// 5m'de kalır: çekmece sayıları messaging_caller_summary_5m'den gelir ve
// aynı çekmecede iki farklı kova hizalaması iki farklı toplam gösterirdi.

// msgTrendFineMaxWindow — bu süreye kadar (dahil) pencereler 1 dk kovadan
// okunur (3 sa = 180 nokta; db detay trendiyle aynı eşik).
const msgTrendFineMaxWindow = 3 * time.Hour

// msgTrendSettle — 1 dk kovada "tam kova" için kova bitiminden sonra
// beklenen süre. Collector batch + async insert gecikmesi 5 dk kovada %3,
// 1 dk kovada ~%17 eksik sayım demek; rozet (Cur*) yarım dakikayı sahte
// "trafik düştü" diye okumasın.
const msgTrendSettle = 30 * time.Second

// Messaging trend kaynakları — SQL'e YALNIZ bu sabitlerden girer.
const (
	msgTrendTableFine   = "messaging_summary_1m"
	msgTrendTableCoarse = "messaging_summary_5m"
)

// msgTrendGrainFor — SAF seçici (messaging_summary_1m_test.go).
//
//	pencere ≤ 3 sa VE 1m tablo pencere başını kapsıyor → messaging_summary_1m, 60 sn
//	aksi hâlde                                        → messaging_summary_5m, 300 sn
func msgTrendGrainFor(from, to time.Time, fineCovers bool) DBTrendGrain {
	if w := to.Sub(from); w > 0 && w <= msgTrendFineMaxWindow && fineCovers {
		return DBTrendGrain{Table: msgTrendTableFine, BucketSec: 60}
	}
	return DBTrendGrain{Table: msgTrendTableCoarse, BucketSec: 300}
}

// MessagingTrendGrain — pencere için kaynağı seçer. Kapsama probu yalnız
// kısa pencerede koşar; 60 sn önbellekli (sourceCovers), hata → kapsamıyor.
func (s *Store) MessagingTrendGrain(ctx context.Context, from, to time.Time) DBTrendGrain {
	fine := false
	if w := to.Sub(from); w > 0 && w <= msgTrendFineMaxWindow {
		fine = s.sourceCovers(ctx, priorSrcMsgSummary1m, from.Truncate(time.Minute))
	}
	return msgTrendGrainFor(from, to, fine)
}

// msgTrendCurrentNow — SAF: Cur* "tam kova" kararının saati. 1m kovada
// msgTrendSettle geri çekilir; 5m bugünkü gibi (davranış değişmez).
func msgTrendCurrentNow(g DBTrendGrain, now time.Time) time.Time {
	if g.Table == msgTrendTableFine {
		return now.Add(-msgTrendSettle)
	}
	return now
}

// msgTrendsSQL — SAF: filo geneli messaging trend sorgusu. Üç sınır (sert
// kısıt): zaman-sınırlı WHERE (`>= from` kovaya hizalı, `< to`), LIMIT,
// süre tavanı. Bilinmeyen tablo → 5m.
//
// LIMIT 200000: 1m yolu ≤ 181 kova × satır; 1 100 topic'e kadar tam seri.
func msgTrendsSQL(table string) string {
	if table != msgTrendTableFine {
		table = msgTrendTableCoarse
	}
	return `
		SELECT msg_system,
		       cluster,
		       destination,
		       toUnixTimestamp64Nano(toDateTime64(time_bucket, 9))                     AS bucket_ns,
		       countMerge(span_count_state)                                            AS span_count,
		       countMerge(error_count_state)                                           AS error_count,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p99_ms
		FROM ` + table + `
		WHERE time_bucket >= ? AND time_bucket < ?
		GROUP BY msg_system, cluster, destination, time_bucket
		ORDER BY msg_system, cluster, destination, time_bucket
		LIMIT 200000
		SETTINGS max_execution_time = 15`
}
