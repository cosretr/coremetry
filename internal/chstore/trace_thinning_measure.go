package chstore

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
)

// trace_thinning_measure.go — v0.10.2440: gölge ölçümün sorguları + lider
// işçi. Bir tik = EN ÇOK BİR gün partition'ı. Sorgu bütçesi (prod ölçeği
// ~2.3 milyar span/gün, docs/plans/trace-thinning-2026-11-21.md §Maliyet):
//
//  1. system.parts — spans partition'ının satır + byte'ı (kesin, metadata).
//  2. Referans kaynakları — root_cause_hypotheses.exemplar_trace_id,
//     trace_snapshots.trace_id, cosre_interactions.trace_id; her biri
//     LIMIT'li, toplam ≤ thinningMaxRefs.
//  3. trace_id_ts (ORDER BY trace_id) — referansların HANGİSİ bu günde,
//     hangi saatlerde: birincil anahtar araması.
//  4. trace_summary_5m — günde bulunan referanslar, yalnız o saatlerin 5 dk
//     kovaları (time_bucket IN küme → (time_bucket, trace_id) PK budaması).
//  5. trace_summary_5m — örneklem: günün her saatinden BİR 5 dk kova
//     (~1/12 partition okunur) × sipHash64(trace_id) % den (GROUP BY
//     belleği ≤ ~2M trace). Kök (servis, operasyon) p99'u aynı örneklemden
//     pencere fonksiyonuyla.

const (
	thinningMaxRefs     = 2000
	thinningSampleLimit = 5_000_000
	thinningTickEvery   = 10 * time.Minute
)

// thinningInnerCols — örneklem / referans iç sorgusunun çıktısı:
// trace_id, is_ref, spans, errs, dur, p99_op, n_op. Dış sorgu sınıfa göre
// sayar (3 satır'a kadar ≤ 5 satır).
func thinningOuterSQL(inner string, keepPercent int) string {
	return `SELECT toUInt8(cls) AS c, toUInt64(count()) AS traces, toUInt64(sum(spans)) AS spans FROM (
	  SELECT spans, ` + thinningClassSQL(keepPercent) + ` AS cls FROM (` + inner + `)
	) GROUP BY c ORDER BY c LIMIT 10`
}

// thinningBucketList — DateTime literal listesi (sayılardan üretilir;
// kullanıcı girdisi yok). IN kümesi PK'nın ilk kolonunu noktalara böler.
func thinningBucketList(ts []time.Time) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = fmt.Sprintf("toDateTime(%d)", t.UTC().Unix())
	}
	return strings.Join(parts, ", ")
}

// thinningSampleSQL — örneklem iç sorgusu. excludeRefs = günde bulunan
// referanslar ayrı (kesin) sayıldığı için örneklemden düşülür.
func thinningSampleSQL(buckets []time.Time, den uint64, excludeRefs bool) string {
	ex := ""
	if excludeRefs {
		ex = ` AND trace_id NOT IN (?)`
	}
	return fmt.Sprintf(`SELECT trace_id, toUInt8(0) AS is_ref, spans, errs, dur,
	    quantile(0.99)(dur) OVER w AS p99_op, count() OVER w AS n_op
	  FROM (
	    SELECT trace_id,
	      countMerge(span_count_state) AS spans,
	      countIfMerge(error_count_state) AS errs,
	      argMaxIfMerge(root_service_state) AS rs,
	      argMaxIfMerge(root_name_state) AS rn,
	      toInt64(maxMerge(trace_end_state)) - toInt64(toUnixTimestamp64Nano(minMerge(trace_start_state))) AS dur
	    FROM trace_summary_5m
	    WHERE time_bucket IN (%s) AND sipHash64(trace_id) %% %d = 0%s
	    GROUP BY trace_id
	    LIMIT %d
	  )
	  WINDOW w AS (PARTITION BY rs, rn)`, thinningBucketList(buckets), den, ex, thinningSampleLimit)
}

// thinningRefSQL — günde bulunan referansların kesin satırları (yavaş
// kuyruk hükmü yok: referans zaten önemli; sınıf hash → hata → ref).
func thinningRefSQL(buckets []time.Time) string {
	return fmt.Sprintf(`SELECT trace_id, toUInt8(1) AS is_ref, spans, errs, toInt64(0) AS dur, toFloat64(0) AS p99_op, toUInt64(0) AS n_op
	  FROM (
	    SELECT trace_id, countMerge(span_count_state) AS spans, countIfMerge(error_count_state) AS errs
	    FROM trace_summary_5m
	    WHERE time_bucket IN (%s) AND trace_id IN (?)
	    GROUP BY trace_id
	    LIMIT %d
	  )`, thinningBucketList(buckets), thinningMaxRefs)
}

const thinningQS = " SETTINGS max_execution_time = %d, skip_unavailable_shards = 1"

func (s *Store) thinningAgg(ctx context.Context, q string, args ...any) (thinSampleAgg, error) {
	var agg thinSampleAgg
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return agg, err
	}
	defer rows.Close()
	for rows.Next() {
		var c uint8
		var t, sp uint64
		if err := rows.Scan(&c, &t, &sp); err != nil {
			return agg, err
		}
		agg.Traces += t
		agg.Spans += sp
		if int(c) < len(agg.Classes) {
			agg.Classes[c].Traces += t
			agg.Classes[c].Spans += sp
		}
	}
	return agg, rows.Err()
}

// thinningPartsSrc — spans partition metadata kaynağı. Küme kipinde
// cluster() shard başına TEK replika okur (clusterAllReplicas byte'ı
// replika sayısıyla katlardı).
func (s *Store) thinningPartsSrc(ctx context.Context) (src, table string) {
	table, cluster := s.skipMatTarget(ctx, "spans")
	if cluster == "" {
		return "system.parts", table
	}
	return fmt.Sprintf("cluster('%s', system.parts)", cluster), table
}

// thinningRefs — referans trace_id'leri (en fazla thinningMaxRefs;
// truncated = sınıra dayandı). since: gün başı — referans trace'ten sonra
// oluşur. Tek kaynak hatası diğerlerini durdurmaz (note'a yazılır).
func (s *Store) thinningRefs(ctx context.Context, since time.Time) (ids []string, truncated bool, note string) {
	srcs := []struct{ name, q string }{
		{"root_cause_hypotheses", `SELECT DISTINCT exemplar_trace_id FROM root_cause_hypotheses WHERE exemplar_trace_id != '' AND computed_at >= ? LIMIT ?`},
		{"trace_snapshots", `SELECT DISTINCT trace_id FROM trace_snapshots WHERE trace_id != '' AND created_at >= ? LIMIT ?`},
		{"cosre_interactions", `SELECT DISTINCT trace_id FROM cosre_interactions WHERE trace_id != '' AND created_at >= ? LIMIT ?`},
	}
	seen := map[string]bool{}
	var notes []string
	for _, src := range srcs {
		rows, err := s.conn.Query(ctx, src.q+fmt.Sprintf(thinningQS, 10), since, thinningMaxRefs)
		if err != nil {
			notes = append(notes, src.name+": "+err.Error())
			continue
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err == nil && !seen[id] {
				if len(ids) >= thinningMaxRefs {
					truncated = true
					break
				}
				seen[id] = true
				ids = append(ids, id)
			}
		}
		rows.Close()
	}
	return ids, truncated, strings.Join(notes, "; ")
}

// thinningRefHours — referanslardan bu günde span'i olanlar + saatleri
// (trace_id_ts PK araması). Saatlerin 5 dk kovaları döner.
func (s *Store) thinningRefHours(ctx context.Context, refs []string, day time.Time) (inDay []string, buckets []time.Time, err error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}
	rows, err := s.conn.Query(ctx, `SELECT trace_id, groupUniqArray(toDateTime(time_bucket)) FROM trace_id_ts
		WHERE trace_id IN (?) AND time_bucket >= ? AND time_bucket < ?
		GROUP BY trace_id LIMIT ?`+fmt.Sprintf(thinningQS, 15), refs, day, day.Add(24*time.Hour), thinningMaxRefs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	hours := map[int64]bool{}
	for rows.Next() {
		var id string
		var hs []time.Time
		if err := rows.Scan(&id, &hs); err != nil {
			return nil, nil, err
		}
		inDay = append(inDay, id)
		for _, h := range hs {
			hours[h.UTC().Truncate(time.Hour).Unix()] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return inDay, thinningHourBuckets(hours), nil
}

// thinningHourBuckets — saatlerin 12'şer 5 dk kovası, sıralı.
func thinningHourBuckets(hours map[int64]bool) []time.Time {
	var out []time.Time
	keys := make([]int64, 0, len(hours))
	for h := range hours {
		keys = append(keys, h)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, h := range keys {
		for m := 0; m < 12; m++ {
			out = append(out, time.Unix(h, 0).UTC().Add(time.Duration(m*5)*time.Minute))
		}
	}
	return out
}

// MeasureThinningDay — tek gün partition'ının gölge tahmini.
func (s *Store) MeasureThinningDay(ctx context.Context, day time.Time, keepPercent int) (ThinningDay, error) {
	start := time.Now()
	day = day.UTC().Truncate(24 * time.Hour)
	dayKey := day.Format("2006-01-02")
	ctx = WithQueryTag(ctx, "trace-thinning-shadow")

	// 1) kesin span + byte.
	src, table := s.thinningPartsSrc(ctx)
	var partsRows, partsBytes uint64
	if err := s.conn.QueryRow(ctx, `SELECT toUInt64(sum(rows)), toUInt64(sum(bytes_on_disk)) FROM `+src+`
		WHERE database = currentDatabase() AND table = ? AND active AND partition_id = ?`+fmt.Sprintf(thinningQS, 10),
		table, day.Format("20060102")).Scan(&partsRows, &partsBytes); err != nil {
		return ThinningDay{}, fmt.Errorf("system.parts: %w", err)
	}

	var notes []string
	// 2–4) referanslar (kesin).
	refs, truncated, refNote := s.thinningRefs(ctx, day)
	if refNote != "" {
		notes = append(notes, refNote)
	}
	inDay, refBuckets, err := s.thinningRefHours(ctx, refs, day)
	if err != nil {
		notes = append(notes, "trace_id_ts: "+err.Error())
		inDay = nil
	}
	var refAgg thinSampleAgg
	if len(inDay) > 0 && len(refBuckets) > 0 {
		refAgg, err = s.thinningAgg(ctx, thinningOuterSQL(thinningRefSQL(refBuckets), keepPercent)+fmt.Sprintf(thinningQS, 30), inDay)
		if err != nil {
			notes = append(notes, "referans: "+err.Error())
			refAgg = thinSampleAgg{}
			inDay = nil
		}
	}

	// 5) örneklem.
	buckets := thinningSampleBuckets(day)
	bucketFraction := float64(len(buckets)) / 288
	den := thinningSampleDen(partsRows, bucketFraction)
	args := []any{}
	if len(inDay) > 0 {
		args = append(args, inDay)
	}
	sample, err := s.thinningAgg(ctx, thinningOuterSQL(thinningSampleSQL(buckets, den, len(inDay) > 0), keepPercent)+fmt.Sprintf(thinningQS, 120), args...)
	if err != nil {
		return ThinningDay{}, fmt.Errorf("örneklem: %w", err)
	}

	d := thinningEstimate(sample, refAgg, den, bucketFraction, partsRows, partsBytes)
	d.Day = dayKey
	d.KeepPercent = keepPercent
	d.MeasuredAt = time.Now().UnixMilli()
	d.RefsInDay = uint64(len(inDay))
	d.RefsTruncated = truncated
	d.ElapsedMs = time.Since(start).Milliseconds()
	if partsRows == 0 {
		notes = append(notes, "spans partition'ı yok (retention düşürmüş ya da veri yok)")
	}
	d.Note = strings.Join(notes, "; ")
	return d, nil
}

// StartTraceThinningShadow — worker kipinde, lider kilidiyle. Mod shadow
// değilse tik boş geçer (sorgu yok).
func (s *Store) StartTraceThinningShadow(ctx context.Context, lock cache.Lock) {
	leader := cache.NewLeaderHolder(lock, thinningLockKey, cache.LeaderTTL(thinningTickEvery))
	leader.Start(ctx)
	t := time.NewTicker(thinningTickEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !leader.IsLeader() {
				continue
			}
			tctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			if err := s.thinningTick(tctx, time.Now()); err != nil {
				log.Printf("[trace-thinning] %v", err)
			}
			cancel()
		}
	}
}

func (s *Store) thinningTick(ctx context.Context, now time.Time) error {
	cfg, err := s.LoadThinningSettings(ctx)
	if err != nil {
		return err
	}
	if cfg.Mode != ThinningModeShadow {
		return nil
	}
	st, err := s.loadThinningShadow(ctx)
	if err != nil {
		return err
	}
	band := thinningBand(now, cfg.FullDays, s.spansRetentionDays(ctx))
	have := map[string]ThinningDay{}
	for _, d := range st.Days {
		have[d.Day] = d
	}
	next := thinningNextDay(band, have, cfg.KeepPercent, now)
	if next == "" {
		return nil
	}
	day, _ := time.Parse("2006-01-02", next)
	d, merr := s.MeasureThinningDay(ctx, day, cfg.KeepPercent)
	runErr := ""
	if merr != nil {
		runErr = next + ": " + merr.Error()
		d = ThinningDay{Day: next, KeepPercent: cfg.KeepPercent, MeasuredAt: now.UnixMilli(), Failed: true, Note: merr.Error()}
	}
	if err := s.saveThinningShadow(ctx, band, &d, runErr, now); err != nil {
		return err
	}
	return merr
}
