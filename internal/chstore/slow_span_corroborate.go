package chstore

// slow_span_corroborate.go — v0.10.1299 (operatör 2026-10-17: "Bu sorgu metric
// grafiği 5 dakika aralıklı olduğu için sağlıklı değil"; MV kuantilleri kaç
// isteğin ne kadar yavaş olduğunu söyleyemez — yalnız ham span'ler istek
// başına doğrudur).
//
// CoSRE taramasında bir gecikme ADAYI (MV p99 farkı LatencyLevel kapılarını
// geçti) bulgu olmadan önce ham span'le doğrulanır: eşiği (≥ max(baseline p99
// × 1.25, 100 ms)) aşan istekler cari ve önceki pencerede sayılır. Tek tarama,
// dakika kovalı (grafiğin "yavaş istek / dk" serisi de buradan), en yavaş
// süre + exemplar trace.
//
// Sınırlar (CLAUDE.md ham spans kuralı): (service_name, time) ORDER BY öneki +
// idx_name set(0) + duration yüklemi; time-bounded WHERE; LIMIT;
// max_execution_time = 3 (emsal CodeFunctionSample). Toplam aralık 6 saati
// aşarsa trace örneklemesi (cityHash64(trace_id) % N = 0, sayımlar × N;
// emsal heatmapSampleN).

import (
	"context"
	"fmt"
	"math"
	"time"
)

const (
	slowCorrobTimeoutSec = 3
	slowCorrobRowCap     = 1500 // ≤ 12 sa × 60 dk + pay
	slowCorrobSampleSpan = 6 * time.Hour
)

// SlowSpanMinute — bir dakikadaki eşik-üstü istek sayısı (örneklemde × N).
type SlowSpanMinute struct {
	TS    int64 // dakika başı, unix saniye
	Count uint64
}

// SlowSpanCorroboration — doğrulama okumasının sonucu.
type SlowSpanCorroboration struct {
	ThresholdMs float64
	CurSlow     uint64 // cari pencerede eşik-üstü istek (× SampleN)
	PriorSlow   uint64 // önceki pencerede (× SampleN; uzunluk normalize EDİLMEZ)
	MaxMs       float64
	ExemplarID  string
	SampleN     int
	Minutes     []SlowSpanMinute // yalnız cari pencere, artan zaman
}

// SlowCorrobSampleN — SAF: toplam aralık 6 saati aşarsa örnekleme payı.
func SlowCorrobSampleN(from, to time.Time) int {
	d := to.Sub(from)
	if d <= slowCorrobSampleSpan {
		return 1
	}
	return int(math.Ceil(float64(d) / float64(slowCorrobSampleSpan)))
}

// SlowSpanCorroborateSQL — SAF: SQL + bind'lar (kanıt çipi sorguyu aynen gösterir). Bind sırası SQL sırasıyla
// birebir: service, name, priorFrom, curTo, thresholdNs.
func SlowSpanCorroborateSQL(service, op string, thresholdNs int64, priorFrom, curTo time.Time, sampleN int) (string, []any) {
	sample := ""
	if sampleN > 1 {
		sample = fmt.Sprintf("\n		  AND cityHash64(trace_id) %% %d = 0", sampleN)
	}
	sql := fmt.Sprintf(`
		SELECT toInt64(toUnixTimestamp(toStartOfMinute(time))) AS m,
		       toUInt64(count())                           AS n,
		       max(duration)                               AS max_ns,
		       argMax(trace_id, duration)                  AS exemplar
		FROM spans
		WHERE service_name = ?
		  AND name = ?
		  AND time >= ?
		  AND time < ?
		  AND duration >= ?%s
		GROUP BY m
		ORDER BY m
		LIMIT %d
		SETTINGS max_execution_time = %d`, sample, slowCorrobRowCap, slowCorrobTimeoutSec)
	return sql, []any{service, op, priorFrom, curTo, thresholdNs}
}

// foldSlowMinutes — SAF: dakika satırları → sonuç (curFrom'dan önceki dakikalar
// önceki pencereye). Dakika curFrom'u kesiyorsa (hizasız pencere) cariye
// sayılır — muhafazakâr değil ama tek dakikalık kayma; LatCorrobMinSlow
// tabanı bunu yutar.
func foldSlowMinutes(rows []slowMinuteRow, curFrom time.Time, sampleN int, thrMs float64) SlowSpanCorroboration {
	out := SlowSpanCorroboration{ThresholdMs: thrMs, SampleN: sampleN}
	var maxNs int64
	cf := curFrom.Truncate(time.Minute).Unix()
	for _, r := range rows {
		n := r.n * uint64(sampleN)
		if r.m >= cf {
			out.CurSlow += n
			out.Minutes = append(out.Minutes, SlowSpanMinute{TS: r.m, Count: n})
			if r.maxNs > maxNs {
				maxNs, out.ExemplarID = r.maxNs, r.exemplar
			}
		} else {
			out.PriorSlow += n
		}
	}
	out.MaxMs = float64(maxNs) / 1e6
	return out
}

type slowMinuteRow struct {
	m        int64
	n        uint64
	maxNs    int64
	exemplar string
}

// SlowSpanCorroborate — eşiği aşan ham span'leri cari [curFrom, curTo) ve önceki
// [priorFrom, curFrom) pencerede sayar.
func (s *Store) SlowSpanCorroborate(ctx context.Context, service, op string, thresholdMs float64, curFrom, curTo, priorFrom time.Time) (SlowSpanCorroboration, error) {
	if service == "" || op == "" || !curTo.After(curFrom) || !curFrom.After(priorFrom) || thresholdMs <= 0 {
		return SlowSpanCorroboration{}, fmt.Errorf("slow-span corroborate: geçersiz girdi")
	}
	n := SlowCorrobSampleN(priorFrom, curTo)
	sql, args := SlowSpanCorroborateSQL(service, op, int64(thresholdMs*1e6), priorFrom, curTo, n)
	rows, err := s.telemetryReadConn().Query(ctx, s.weighRawSQL(sql), args...) // v0.10.2452 — slow-span counts represented
	if err != nil {
		return SlowSpanCorroboration{}, fmt.Errorf("slow-span corroborate: %w", err)
	}
	defer rows.Close()
	var rs []slowMinuteRow
	for rows.Next() {
		var r slowMinuteRow
		if err := rows.Scan(&r.m, &r.n, &r.maxNs, &r.exemplar); err != nil {
			return SlowSpanCorroboration{}, err
		}
		rs = append(rs, r)
	}
	if err := rows.Err(); err != nil {
		return SlowSpanCorroboration{}, err
	}
	return foldSlowMinutes(rs, curFrom, n, thresholdMs), nil
}
