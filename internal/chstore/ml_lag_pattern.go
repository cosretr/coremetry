package chstore

// ml_lag_pattern.go — v0.10.2476 (operatör kararı): her gün benzer saatte
// tekrarlayan Kafka consumer lag olayı "beklenen günlük desen"dir. Mevsimsel
// taban (medyan) kısa günlük sıçramaları yok saydığı için etki kuralı her
// gün aynı saatte ateşliyor ve v0.10.2396 kuralıyla P1 açıyordu.
//
// KURAL (SAF, LagDailyPatternDays): aynı seri (servis + bölüm kodu; topic
// koddadır) önceki 7 günün EN AZ 3'ünde, bugünkü olayın başlangıç saatinin
// ± 1 saatinde ([saat başı − 1 sa, saat başı + 2 sa)) bir lag olayı başlattıysa
// desen. Saat, saklanan zaman damgasının kendi dilimi (UTC). Bastırma YOK:
// Problem açılır, P3 ve gerekçe cümlesiyle (computePriority dalı, Metric
// MLGroupLagPatternMetric; Value = gün sayısı, Threshold = 7).
//
// Geçmiş okuması (MLLagEpisodeStarts): ml_events (ORDER BY id; epizot
// başına satır, MLEpisodeID) — detector + zaman alt/üst sınırı + servis /
// kod IN listesi, LIMIT, max_execution_time. Kaydedici olay başına bir kez
// okur (bellek), tik başına en çok bir okuma.

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// MLGroupLagPatternMetric — desen satırının ölçüsü (Value = gün, Threshold = 7).
const MLGroupLagPatternMetric = "ml_group_lag_pattern"

// Kural sabitleri (operatör kararı; ayar yalnız aç/kapa: ml.lagDailyPattern).
const (
	LagPatternWindowDays = 7
	LagPatternMinDays    = 3
	LagPatternHourSlack  = time.Hour

	mlLagHistoryCodes = 200
)

// LagDailyPatternDays — SAF: önceki 7 günden kaçında (1..7 gün önce) olay
// başlangıcının saat dilimi ± 1 saatte (yarı açık pencere) bir önceki bölüm
// başlamış. Aynı günün birden çok bölümü bir gün sayılır; bugünkü ve
// geleceğe ait / sıfır başlangıç sayılmaz.
func LagDailyPatternDays(cur int64, prior []int64) int {
	if cur <= 0 {
		return 0
	}
	hour := time.Unix(0, cur).UTC().Truncate(time.Hour)
	days := 0
	for k := 1; k <= LagPatternWindowDays; k++ {
		base := hour.Add(-time.Duration(k) * 24 * time.Hour)
		from, to := base.Add(-LagPatternHourSlack).UnixNano(), base.Add(time.Hour+LagPatternHourSlack).UnixNano()
		for _, s := range prior {
			if s > 0 && s < cur && s >= from && s < to {
				days++
				break
			}
		}
	}
	return days
}

// IsLagDailyPattern — SAF: eşik (≥ 3 / 7).
func IsLagDailyPattern(days int) bool { return days >= LagPatternMinDays }

// trLocative — SAF: sayının bulunma eki ("4'ünde", "6'sında").
func trLocative(n int) string {
	suf := map[int]string{0: "'ında", 1: "'inde", 2: "'sinde", 3: "'ünde", 4: "'ünde", 5: "'inde", 6: "'sında", 7: "'sinde", 8: "'inde", 9: "'unda"}
	return strconv.Itoa(n) + suf[n%10]
}

// LagDailyPatternText — SAF: TEK TANIM (Problem gerekçesi, Tekrar bloğu,
// anomali satırı, karar gerekçesi).
func LagDailyPatternText(days int) string {
	return "beklenen günlük desen: son " + strconv.Itoa(LagPatternWindowDays) + " günün " + trLocative(days) + " benzer saatte görüldü"
}

// lagPatternOf — SAF: desen satırıysa StdLag'e gün + cümle.
func lagPatternOf(p Problem, l StdLag) StdLag {
	if p.Metric != MLGroupLagPatternMetric || p.Value < 1 {
		return l
	}
	l.PatternDays = int(p.Value)
	l.Pattern = LagDailyPatternText(l.PatternDays)
	return l
}

const mlLagHistorySQL = `
		SELECT service, code, started_at FROM ml_events FINAL
		WHERE detector = 'metric_kafka' AND started_at >= ? AND started_at < ? AND service IN ? AND code IN ?
		LIMIT 5000
		SETTINGS max_execution_time = 5`

// MLLagEpisodeStarts — servis × kod serilerinin [since, until) arasında
// başlayan lag bölümleri: MLSeriesKey(servis, kod) → başlangıçlar (unix ns).
// Boş girdi = okuma yok. Servis ya da kod listesi 200 ile sınırlı.
func (s *Store) MLLagEpisodeStarts(ctx context.Context, services, codes []string, since, until time.Time) (map[string][]int64, error) {
	if len(services) == 0 || len(codes) == 0 || !until.After(since) {
		return nil, nil
	}
	if len(codes) > mlLagHistoryCodes {
		codes = codes[:mlLagHistoryCodes]
	}
	if len(services) > mlLagHistoryCodes {
		services = services[:mlLagHistoryCodes]
	}
	rows, err := s.conn.Query(WithQueryTag(ctx, "ml_lag_pattern_history"), mlLagHistorySQL, chDateTime64Arg(since), chDateTime64Arg(until), services, codes)
	if err != nil {
		return nil, fmt.Errorf("ml lag history: %w", err)
	}
	defer rows.Close()
	out := map[string][]int64{}
	for rows.Next() {
		var svc, code string
		var st time.Time
		if err := rows.Scan(&svc, &code, &st); err != nil {
			return nil, err
		}
		k := MLSeriesKey(svc, code)
		out[k] = append(out[k], st.UnixNano())
	}
	return out, rows.Err()
}
