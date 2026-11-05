package anomaly

// subjects_ch.go — v0.10.1791 (Davis F1.3): db / queue öznelerinin ardışık
// + mevsimsel serileri, mevcut MV'lerden (db_summary_5m, messaging_summary_5m;
// ham spans YOK — invariant 3). Servis yolunun (buildAllBucketsQuery /
// buildAllSeasonalQueryTZ) özne ikizi: aynı ifade (metricValueExpr), aynı
// pencereler, aynı dilim; anahtar (sistem, kimlik).
//
// Seçim: son 1 sa'te sistem başına hacme göre üst-N (LIMIT N BY sistem —
// sunucu tarafında, tam katalog uygulamaya gelmez). Seri okumaları seçilen
// demetlere `(sistem, kimlik) IN ((?, ?), …)` ile sınırlıdır: bağlı DEĞER
// listesi, alt sorgu değil (Distributed'da GLOBAL IN gerekmez — v0.5.427).
// GROUP BY itilmez (distributed_group_by_no_merge yok); boş kova satır
// üretmez = "veri yok", 0 değil (fillDwellGrid / padTrailingSilence aynen).

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// subjectCHSource — bir özne türünün MV tanımı.
type subjectCHSource struct {
	Kind   string // chstore.AnomalySubjectDB | AnomalySubjectQueue
	Table  string
	SysCol string
	IDCol  string
	// SubjectID — (sistem, kimlik) → özne kimliği (DBSubjectID / QueueSubjectID).
	SubjectID func(system, id string) string
}

var subjectCHSources = []subjectCHSource{
	{Kind: chstore.AnomalySubjectDB, Table: "db_summary_5m", SysCol: "db_system", IDCol: "instance", SubjectID: chstore.DBSubjectID},
	{Kind: chstore.AnomalySubjectQueue, Table: "messaging_summary_5m", SysCol: "msg_system", IDCol: "destination", SubjectID: chstore.QueueSubjectID},
}

// subjectSelectLookback — seçim hacminin penceresi (endpoint seçimiyle aynı: 1 sa).
const subjectSelectLookback = time.Hour

// buildSubjectSelectQuery — SAF: sistem başına üst-N özne (hacim azalan,
// eşitlikte kimlik artan — deterministik). Binds: from, to, topN, maxSeries+1.
// İkinci LIMIT tavanı bir aşar: çağıran "tavan aşıldı"yı görebilsin.
func buildSubjectSelectQuery(src subjectCHSource) string {
	return fmt.Sprintf(`
		SELECT %[2]s, %[3]s, countMerge(span_count_state) AS c
		FROM %[1]s
		WHERE time_bucket >= ? AND time_bucket < ?
		  AND %[2]s != '' AND %[3]s != ''
		GROUP BY %[2]s, %[3]s
		ORDER BY %[2]s, c DESC, %[3]s
		LIMIT ? BY %[2]s
		LIMIT ?
		SETTINGS max_execution_time = 10`, src.Table, src.SysCol, src.IDCol)
}

// subjectTupleIn — SAF: n demet için `(sys, id) IN ((?, ?), …)` yüklemi.
func subjectTupleIn(src subjectCHSource, n int) string {
	if n < 1 {
		n = 1
	}
	return "(" + src.SysCol + ", " + src.IDCol + ") IN (" +
		strings.TrimSuffix(strings.Repeat("(?, ?), ", n), ", ") + ")"
}

// buildSubjectBucketsQuery — SAF: buildAllBucketsQuery'nin özne ikizi.
// Binds: cutoff, upper, demetler (2n).
func buildSubjectBucketsQuery(src subjectCHSource, vexpr string, n int) string {
	return fmt.Sprintf(`
		SELECT %[2]s, %[3]s, toUnixTimestamp(time_bucket) AS t, %[4]s AS v,
		       countMerge(span_count_state) / 300.0 AS rate
		FROM %[1]s
		WHERE time_bucket >= ? AND time_bucket < ?
		  AND %[5]s
		GROUP BY %[2]s, %[3]s, t
		ORDER BY %[2]s, %[3]s, t
		LIMIT 1000 BY %[2]s, %[3]s
		LIMIT 20000000
		SETTINGS max_execution_time = 25`, src.Table, src.SysCol, src.IDCol, vexpr, subjectTupleIn(src, n))
}

// buildSubjectSeasonalQuery — SAF: buildAllSeasonalQueryTZ'nin özne ikizi
// (aynı slot / sınıf / üst sınır yüklemleri, aynı dilim). Binds: cutoff,
// upper, sınıf, slot, slot, yarıçap, demetler (2n).
func buildSubjectSeasonalQuery(src subjectCHSource, vexpr, tz string, n int) string {
	sodExpr := "(toHour(time_bucket, '" + tz + "') * 3600 + toMinute(time_bucket, '" + tz + "') * 60)"
	return fmt.Sprintf(`
		SELECT %[2]s, %[3]s, toUnixTimestamp(time_bucket) AS t, %[4]s AS v,
		       countMerge(span_count_state) / 300.0 AS rate
		FROM %[1]s
		WHERE time_bucket >= ?
		  AND time_bucket < ?
		  AND %[6]s = ?
		  AND least(abs(%[5]s - ?), 86400 - abs(%[5]s - ?)) <= ?
		  AND %[7]s
		GROUP BY %[2]s, %[3]s, t
		ORDER BY %[2]s, %[3]s, t
		LIMIT 700 BY %[2]s, %[3]s
		LIMIT 14000000
		SETTINGS max_execution_time = 25`, src.Table, src.SysCol, src.IDCol, vexpr, sodExpr, seasonalClassExpr(tz), subjectTupleIn(src, n))
}

// subjectPair — seçilmiş (sistem, kimlik) demeti.
type subjectPair struct{ System, ID string }

// subjectPairArgs — SAF: demetlerin bind sırası.
func subjectPairArgs(ps []subjectPair) []any {
	out := make([]any, 0, 2*len(ps))
	for _, p := range ps {
		out = append(out, p.System, p.ID)
	}
	return out
}

// subjectSeries — bir öznenin bir metrikteki serileri (servis yolunun
// bucketsByMetric / seasonalByMetric / ratesByMetric ikizi).
type subjectSeries struct {
	Values, Rates           []float64
	Seasonal, SeasonalRates []float64
}

// subjectTick — bir tikin özne serileri: metrik → subjectKey.String() → seri.
type subjectTick map[string]map[string]subjectSeries

func (t subjectTick) put(metric, key string, f func(*subjectSeries)) {
	m := t[metric]
	if m == nil {
		m = map[string]subjectSeries{}
		t[metric] = m
	}
	s := m[key]
	f(&s)
	m[key] = s
}

// selectCHSubjects — sistem başına üst-N; dönen > maxSeries ise tavan aşıldı.
func (d *Detector) selectCHSubjects(ctx context.Context, src subjectCHSource, now time.Time, topN, maxSeries int) ([]subjectPair, error) {
	upper := lastCompleteBucketStart(now)
	rows, err := d.store.TelemetryReadConn().Query(ctx, buildSubjectSelectQuery(src),
		upper.Add(-subjectSelectLookback), upper, topN, maxSeries+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []subjectPair
	for rows.Next() {
		var sys, id string
		var c uint64
		if err := rows.Scan(&sys, &id, &c); err != nil {
			return nil, err
		}
		out = append(out, subjectPair{System: sys, ID: id})
	}
	return out, rows.Err()
}

// fetchCHSubjects — seçilen öznelerin üç metrikteki ardışık + mevsimsel
// serileri tika yazılır. Metrik başına iki okuma (servis yoluyla aynı şekil);
// bir okuma hatası yalnız o metrik/türü düşürür (batchSeries sözleşmesi).
func (d *Detector) fetchCHSubjects(ctx context.Context, src subjectCHSource, pairs []subjectPair, tracked []string, now time.Time,
	dwell, days, neighbor int, loc *time.Location, out subjectTick) {
	if len(pairs) == 0 {
		return
	}
	keyOf := func(sys, id string) string {
		sid := src.SubjectID(sys, id)
		return subjectKey{Kind: src.Kind, Owner: sid, ID: sid}.String()
	}
	args := subjectPairArgs(pairs)
	cutoff := now.Add(-time.Duration(historyHours) * time.Hour)
	upper := lastCompleteBucketStart(now)
	loc = seasonalLocOrUTC(loc)
	scut, supper, _, target, radius := seasonalBinds(now, days, neighbor, loc)
	class := dayClass(now.In(loc))
	dayKey := seasonalDayKey(loc)
	for _, m := range tracked {
		vexpr, err := metricValueExpr(m)
		if err != nil {
			continue
		}
		vals, rates, times := map[string][]float64{}, map[string][]float64{}, map[string][]int64{}
		if err := d.scanSubjectRows(ctx, buildSubjectBucketsQuery(src, vexpr, len(pairs)), append([]any{cutoff, upper}, args...),
			func(k string, t int64, v, r float64) {
				vals[k] = append(vals[k], v)
				rates[k] = append(rates[k], r)
				times[k] = append(times[k], t)
			}, keyOf); err != nil {
			logSubjectErr(src.Kind, m, "ardışık", err)
			continue
		}
		for k, series := range vals {
			v, r := fillDwellGrid(series, rates[k], times[k], upper.Unix(), dwell)
			out.put(m, k, func(s *subjectSeries) { s.Values, s.Rates = v, r })
		}
		seas, seasRates, days := map[string][]float64{}, map[string][]float64{}, map[string]map[int64]struct{}{}
		sargs := append([]any{scut, supper, class, target, target, radius}, args...)
		if err := d.scanSubjectRows(ctx, buildSubjectSeasonalQuery(src, vexpr, seasonalTZLiteral(loc), len(pairs)), sargs,
			func(k string, t int64, v, r float64) {
				seas[k] = append(seas[k], v)
				seasRates[k] = append(seasRates[k], r)
				if days[k] == nil {
					days[k] = map[int64]struct{}{}
				}
				days[k][seasonalDay(t, dayKey)] = struct{}{}
			}, keyOf); err != nil {
			logSubjectErr(src.Kind, m, "mevsimsel", err)
			continue
		}
		pruneSeasonalByDayDiversity(seas, days, seasonalMinDaysFor(class))
		for k, v := range seas {
			sv, sr := v, seasRates[k]
			out.put(m, k, func(s *subjectSeries) { s.Seasonal, s.SeasonalRates = sv, sr })
		}
	}
}

// scanSubjectRows — (sys, id, t, v, rate) satır döngüsü.
func (d *Detector) scanSubjectRows(ctx context.Context, q string, args []any, f func(key string, t int64, v, rate float64), keyOf func(sys, id string) string) error {
	rows, err := d.store.TelemetryReadConn().Query(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sys, id string
		var t uint32
		var v, rate float64
		if err := rows.Scan(&sys, &id, &t, &v, &rate); err != nil {
			return err
		}
		f(keyOf(sys, id), int64(t), v, rate)
	}
	return rows.Err()
}
