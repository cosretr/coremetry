package chstore

// impact_replay_read.go — v0.10.2392 (etki olayı programı: "etki kuralı geri
// oynatma"): operatörün "yeni etki kuralı son N günde kaç Problem açardı"
// sorusunun iki ClickHouse okuması. YALNIZ ml_series (dedektörün kendi 5
// dakikalık taban serisi); spans / metric_points / VM'e gidilmez.
//
// 1) Aday okuması — oynatma penceresinde kuralın MUTLAK alt sınırını (eşik
//    tabandan bağımsız en az bu: rule.threshold(0)) ve yük kapısını geçen en
//    az bir kovası olan servisler (lag: servis × topic part'ı). Bu süzgeç
//    KESİNDİR, yaklaşık değil: alt sınırın altındaki kova hiçbir tabanla
//    ihlal olamaz. Trafiğe göre sıralı, LIMIT (çağıran tavan + 1 ister →
//    "kırpıldı" bilgisi).
// 2) Seri okuması — adayların taban geriye bakışı dahil tüm kovaları,
//    (service, part) başına üç paralel dizi (zaman, normalize değer, kova
//    yerel mi). Normalize MLBaseline'ınkiyle AYNI (count · hedef / bucket_sec).
//
// İkisi de zaman sınırlı WHERE + LIMIT + max_execution_time taşır. FINAL
// yok (MLBaseline / MLSeriesRange emsali): RMT çift satırı çağıran t ile
// tekilleştirir.

import (
	"context"
	"time"
)

// ImpactReplayCandidateReq — aday okuması.
type ImpactReplayCandidateReq struct {
	Signal string
	// Servis düzeyi kip (PartPrefix boş): ValueParts (1–2 tam part) ve
	// her birinin mutlak alt sınırı MinValues; LoadPart (istek/sn serisi)
	// ve dakikalık yük kapısı MinLoadPerMin (0 = kapı yok).
	ValueParts    []string
	MinValues     []float64
	LoadPart      string
	MinLoadPerMin float64
	// Topic kipi (consumer lag): part = PartPrefix ya da PartPrefix + "#…",
	// '@' taşıyan (Thanos) part'lar hariç; MinValues[0] alt sınır.
	PartPrefix string
	From, To   time.Time
	BucketSec  int
	Limit      int
}

// ImpactReplayCandidate — aday: servis (topic kipinde part'ıyla).
type ImpactReplayCandidate struct {
	Service string
	Part    string // servis düzeyi kipte boş
	Traffic float64
	Hits    uint64 // alt sınırı geçen kova sayısı
}

// impactReplaySvcCandSQL — SAF: servis düzeyi aday sorgusu. Kova başına
// değer + yük part'ları yan yana (maxIf); ölçülmemiş part 0 okunur (alt
// sınır > 0 olduğundan isabet sayılmaz).
func impactReplaySvcCandSQL() string {
	return `
		SELECT service, sum(c) AS traffic, countIf(hit) AS hits FROM (
			SELECT service, bucket,
			       maxIf(count, part = ?) AS c,
			       (maxIf(count, part = ?) >= ? OR maxIf(count, part = ?) >= ?) AND (? = '' OR c * 60 >= ?) AS hit
			FROM ml_series
			WHERE signal = ? AND part IN (?) AND bucket >= ? AND bucket < ? AND bucket_sec = ?
			GROUP BY service, bucket
		)
		GROUP BY service
		HAVING hits > 0
		ORDER BY traffic DESC, service
		LIMIT ?
		SETTINGS max_execution_time = 5`
}

// impactReplayTopicCandSQL — SAF: topic kipi (consumer lag) aday sorgusu.
func impactReplayTopicCandSQL() string {
	return `
		SELECT service, part, max(count) AS traffic, countIf(count >= ?) AS hits
		FROM ml_series
		WHERE signal = ? AND (part = ? OR startsWith(part, ?)) AND position(part, '@') = 0
		  AND bucket >= ? AND bucket < ? AND bucket_sec = ?
		GROUP BY service, part
		HAVING hits > 0
		ORDER BY traffic DESC, service, part
		LIMIT ?
		SETTINGS max_execution_time = 5`
}

// ImpactReplayCandidates — aday okuması (bkz. dosya başı).
func (s *Store) ImpactReplayCandidates(ctx context.Context, req ImpactReplayCandidateReq) ([]ImpactReplayCandidate, error) {
	if len(req.MinValues) == 0 || req.Limit <= 0 {
		return nil, nil
	}
	from, to := req.From.UTC(), req.To.UTC() // bucket DateTime: DateTime64 bind YOK
	var out []ImpactReplayCandidate
	if req.PartPrefix != "" {
		rows, err := s.conn.Query(ctx, impactReplayTopicCandSQL(), req.MinValues[0], req.Signal, req.PartPrefix, req.PartPrefix+"#",
			from, to, uint32(req.BucketSec), req.Limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var c ImpactReplayCandidate
			if err := rows.Scan(&c.Service, &c.Part, &c.Traffic, &c.Hits); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, rows.Err()
	}
	if len(req.ValueParts) == 0 {
		return nil, nil
	}
	a, ma := req.ValueParts[0], req.MinValues[0]
	b, mb := a, ma
	if len(req.ValueParts) > 1 && len(req.MinValues) > 1 {
		b, mb = req.ValueParts[1], req.MinValues[1]
	}
	parts := append([]string{}, req.ValueParts...)
	if req.LoadPart != "" {
		parts = append(parts, req.LoadPart)
	}
	rows, err := s.conn.Query(ctx, impactReplaySvcCandSQL(), req.LoadPart, a, ma, b, mb, req.LoadPart, req.MinLoadPerMin,
		req.Signal, parts, from, to, uint32(req.BucketSec), req.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ImpactReplayCandidate
		if err := rows.Scan(&c.Service, &c.Traffic, &c.Hits); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ImpactReplaySeriesReq — seri okuması: Services × (Parts ya da PartPrefix).
type ImpactReplaySeriesReq struct {
	Signal     string
	Services   []string
	Parts      []string // tam part'lar; boşsa PartPrefix kipi
	PartPrefix string
	From, To   time.Time
	TargetSec  int
}

// ImpactReplaySeries — bir (service, part) serisi; T/V/Native paralel.
// Native = kova hedef boyda (oynatma yalnız bunları yargılar; taban
// MLBaseline gibi hepsini normalize okur).
type ImpactReplaySeries struct {
	Service, Part string
	T             []uint32
	V             []float64
	Native        []uint8
}

// ImpactReplaySeriesGroupCap — (service, part) başına en çok nokta (28 g × 288
// = 8064; geriye bakış ml_series TTL'iyle zaten sınırlı).
const ImpactReplaySeriesGroupCap = 12000

// impactReplaySeriesSQL — SAF: part koşulu kipe göre.
func impactReplaySeriesSQL(prefix bool) string {
	cond := "part IN (?)"
	if prefix {
		cond = "(part = ? OR startsWith(part, ?)) AND position(part, '@') = 0"
	}
	return `
		SELECT service, part,
		       groupArray(12000)(toUInt32(toUnixTimestamp(bucket))) AS ts,
		       groupArray(12000)(count * ? / bucket_sec) AS vs,
		       groupArray(12000)(toUInt8(bucket_sec = ?)) AS nat
		FROM ml_series
		WHERE signal = ? AND service IN (?) AND ` + cond + `
		  AND bucket >= ? AND bucket < ?
		GROUP BY service, part
		LIMIT 5000
		SETTINGS max_execution_time = 8`
}

// ImpactReplaySeries — seri okuması (bkz. dosya başı).
func (s *Store) ImpactReplaySeries(ctx context.Context, req ImpactReplaySeriesReq) ([]ImpactReplaySeries, error) {
	if len(req.Services) == 0 || req.TargetSec <= 0 {
		return nil, nil
	}
	args := []any{float64(req.TargetSec), uint32(req.TargetSec), req.Signal, req.Services}
	prefix := len(req.Parts) == 0
	if prefix {
		args = append(args, req.PartPrefix, req.PartPrefix+"#")
	} else {
		args = append(args, req.Parts)
	}
	args = append(args, req.From.UTC(), req.To.UTC()) // bucket DateTime: DateTime64 bind YOK
	rows, err := s.conn.Query(ctx, impactReplaySeriesSQL(prefix), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImpactReplaySeries
	for rows.Next() {
		var r ImpactReplaySeries
		if err := rows.Scan(&r.Service, &r.Part, &r.T, &r.V, &r.Native); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
