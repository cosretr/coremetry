package chstore

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// dbstmt_dist — v0.10.1972 (operatör: Statement süre gösterimi "1 2 4", 4 = süre dağılımı; mockup
// stmt-duration onaylı). Seçili penceredeki çağrıların süre histogramı, log ölçekli kovalar.
//
// Ek MV / kolon YOK: kaynak özetle aynı TDigest durumu (duration_q_state). Durum 200 eşit kütle
// noktasında okunur (q = (i+0.5)/200, her nokta çağrıların %0,5'i); her nokta düştüğü kovaya
// cnt/200 çağrı ekler. Yani dağılım YAKLAŞIKTIR (TDigest çözünürlüğü + %0,5 adım) — ekranda öyle
// yazılır. quantilesTDigestMerge durumdaki düzeylerden farklı düzeyleri kabul eder (dbstmt_detail
// emsalleri). Izgara özetle aynı (DBStmtTrendGrain: kısa pencere 1 dk tablo, aksi 5 dk).

// DBStmtDistBucket — [LoMs, HiMs) aralığındaki yaklaşık çağrı sayısı. HiMs 0 = üst sınır yok.
type DBStmtDistBucket struct {
	LoMs  float64 `json:"loMs"`
	HiMs  float64 `json:"hiMs"`
	Count uint64  `json:"count"`
}

// dbStmtDistEdgesMs — 1-2-5 merdiveni, 0,1 ms → 60 sn. İlk kovanın altı 0, son kovanın üstü açık.
var dbStmtDistEdgesMs = []float64{0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 30000, 60000}

const dbStmtDistLevels = 200

// dbStmtDistLevelList — "0.0025, 0.0075, …, 0.9975" (SAF; sabit, argüman değil — tip uyumu).
func dbStmtDistLevelList() string {
	parts := make([]string, dbStmtDistLevels)
	for i := range parts {
		parts[i] = fmt.Sprintf("%g", (float64(i)+0.5)/dbStmtDistLevels)
	}
	return strings.Join(parts, ", ")
}

// dbStmtDistSQL — saf kurucu; tablo izin listesinden (özet ızgarası).
func dbStmtDistSQL(table, where string) string {
	return `
		SELECT
			countMerge(span_count_state) AS cnt,
			quantilesTDigestMerge(` + dbStmtDistLevelList() + `)(duration_q_state) AS qs
		FROM ` + table + ` ` + where + `
		SETTINGS max_execution_time = 10`
}

// dbStmtDistBuckets — SAF: kütle noktaları (ns) → kovalar. Baştaki ve sondaki boş kovalar atılır,
// aradakiler kalır (eksen kesintisiz). Kalan sayılar son dolu kovaya eklenir (toplam = cnt).
func dbStmtDistBuckets(cnt uint64, qsNs []float64) []DBStmtDistBucket {
	if cnt == 0 || len(qsNs) == 0 {
		return nil
	}
	edges := dbStmtDistEdgesMs
	n := len(edges) + 1 // [0,e0) … [e_last, ∞)
	mass := make([]float64, n)
	per := float64(cnt) / float64(len(qsNs))
	for _, ns := range qsNs {
		if math.IsNaN(ns) || math.IsInf(ns, 0) {
			continue
		}
		ms := ns / 1e6
		i := 0
		for i < len(edges) && ms >= edges[i] {
			i++
		}
		mass[i] += per
	}
	first, last := -1, -1
	for i, m := range mass {
		if m > 0 {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return nil
	}
	out := make([]DBStmtDistBucket, 0, last-first+1)
	var sum uint64
	for i := first; i <= last; i++ {
		b := DBStmtDistBucket{Count: uint64(math.Round(mass[i]))}
		if i > 0 {
			b.LoMs = edges[i-1]
		}
		if i < len(edges) {
			b.HiMs = edges[i]
		}
		sum += b.Count
		out = append(out, b)
	}
	// Yuvarlama artığı: toplam çağrı sayısıyla birebir.
	if sum != cnt {
		d := int64(cnt) - int64(sum)
		for i := len(out) - 1; i >= 0; i-- {
			if out[i].Count > 0 || d > 0 {
				nv := int64(out[i].Count) + d
				if nv < 0 {
					nv = 0
				}
				out[i].Count = uint64(nv)
				break
			}
		}
	}
	return out
}

// GetDBStmtDistribution — pencerenin yaklaşık süre dağılımı (özetle aynı ızgara). Çağrı yoksa nil.
func (s *Store) GetDBStmtDistribution(ctx context.Context, q DBStmtDetailQuery, g DBStmtTrendGrain) ([]DBStmtDistBucket, error) {
	table, wc := "db_statement_summary_5m", dbStmtDetailWhere(q)
	if g.Table == dbStmtTrendTableFine {
		table, wc = dbStmtTrendTableFine, dbStmtDetailWhereGrain(q, time.Minute)
	}
	var cnt uint64
	var qs []float64
	if err := s.telemetryReadConn().QueryRow(ctx, dbStmtDistSQL(table, wc.sql()), wc.args...).Scan(&cnt, &qs); err != nil {
		return nil, err
	}
	return dbStmtDistBuckets(cnt, qs), nil
}
