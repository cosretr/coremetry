package chstore

// service_slowdown.go — v0.10.1091: yaygın yavaşlama hızlı yolunun
// (`svc-slowdown:<servis>`) OKUMA yarısı. Karar ve Problem yaşam döngüsü
// evaluator/service_slowdown.go'da (lider kilidi, notify, incident).
//
// İki MV okuması, TAMAMLANMIŞ kova başına bir kez (tik başına değil —
// evaluator sonucu kova değişene dek bellekten sunar):
//
//  1. Operasyon pivotu — operation_summary_5m, chstore.OpP99PivotQuery
//     (trace_op_latency'nin AYNI tek geçişi; kopya yok): cari kova p99'u vs aynı
//     operasyonun önceki 24 sa'inin (cari kova hariç) HAVUZLANMIŞ p95'i
//     (BaseP95 — dünkü kısa bir olay taban p99'unu şişirip bugünkünü
//     susturmasın), HAVING'de kuralın tabanları (çağrı, mutlak p99, kat) + YAVAŞ
//     PAY tabanı cur_p95 ≥ MinP99Ms/2 (CurP95MinMs — tek yavaş trace'in iç içe
//     span'leri üç operasyonun p99'unu birden şişirebilir, p95'ini değil).
//     Batch servisler iç WHERE'de düşer — LIMIT onlara harcanmasın. Satır az.
//  2. Servis özeti — service_summary_5m, servis başına cari kova çağrısı +
//     p99, önceki SAATİN çağrısı (trafik çöküşü kolunun tabanı) ve 24 sa p99
//     tabanı; HAVING yalnız (a) operasyon kolunun aday servisleri + açık
//     problemlerin servisleri (toplam çağrı tabanı / açıklama için) ya da
//     (b) trafik çöküşü koşulunu tutan servisler. Batch servisler iç WHERE'de
//     düşer (iş bitince trafiği zaten "çöker").
//
// Neden ikinci okuma: servisin TOPLAM çağrısı ve servis p99'u operasyon
// satırlarından türetilemez — HAVING'e takılmayan operasyonlar pivotun
// çıktısında yok, tDigest'ler operasyonlar üstünden birleştirilemez. İkisi de
// MV, ikisi de sınırlı (LIMIT + max_execution_time + zaman aralıklı WHERE).

import (
	"context"
	"time"
)

// SvcSlowdownBucket — kural kovası (MV kovası).
const SvcSlowdownBucket = 5 * time.Minute

// svcSlowBaseLookback — operasyon ve servis p99 tabanı: cari kovadan önceki
// 24 sa (op_latency'nin taban penceresiyle aynı).
const svcSlowBaseLookback = 24 * time.Hour

// SvcSlowHourBuckets — trafik çöküşü tabanı: cari kovadan önceki 12 kova (1 sa)
// kova başı ortalaması. Neden 24 sa ortalaması değil: gündüz/gece eğrisi olan
// serviste gece trafiği 24 sa ortalamasının çok altındadır, kol her gece
// "çöküş" görürdü; bir saat önce aynı servisin olağan hacmi yerel taban.
const SvcSlowHourBuckets = 12

// SvcSlowCollapseP99Factor — trafik çöküşü kolunda servis p99'unun 24 sa
// (p95) tabanına göre en az katı (vida değil; operatör kuralı "p99 ≥ 3× taban").
//
// ÖNCELİK UYARISI (inceleme F): çöküş kolunun oranı ≥ 3, operasyon kolununki ≥
// RiseFactor. problem_priority.bigBreachRatio 3'ün (operasyon kolunda
// riseFactor'ün) ÜSTÜNE çıkarılırsa bu kolların problemi critical P2 olur —
// "tetiklenince daima P1" yalnız bigBreachRatio ≤ 3 (vars. 2) iken geçerli
// (pin: TestServiceSlowdownPriority).
const SvcSlowCollapseP99Factor = 3.0

// svcSlowServiceLimit — servis özeti satır tavanı (HAVING dar; tavan kemer).
const svcSlowServiceLimit = 1000

// SvcSlowReadTimeout — iki okumanın TOPLAM süre bütçesi (evaluator bağlamı) ve
// SQL max_execution_time'ı: evaluateAll tikinde 25 s'lik pivot beklenmez.
const SvcSlowReadTimeout = 10 * time.Second

// SvcSlowOpRow — operasyon pivotunun satırı (tabanları geçmiş operasyon).
// BaseP95Ms = önceki 24 sa'in havuzlanmış p95'i (taban); CurP95Ms = cari kova
// p95'i (yavaş pay tabanı).
type SvcSlowOpRow struct {
	Service, Operation            string
	CurP99Ms, BaseP95Ms, CurP95Ms float64
	CurCalls, BaseCalls           uint64
}

// SvcSlowServiceRow — servis özetinin satırı.
type SvcSlowServiceRow struct {
	Service   string
	CurCalls  uint64  // cari kova
	HourCalls uint64  // önceki SvcSlowHourBuckets kova toplamı
	CurP99Ms  float64 // cari kova servis p99'u (0 = ölçü yok)
	BaseP95Ms float64 // önceki 24 sa servis p95'i — taban (0 = ölçü yok)
}

// ServiceSlowdownOpsQuery — SAF: operasyon pivotu (golden test). cur = cari
// (tamamlanmış) kovanın başı; taban [cur − 24 sa, cur), cari [cur, cur + 5 dk).
func ServiceSlowdownOpsQuery(cur time.Time, cfg ServiceSlowdownConfig, sens AnomalySensitivityConfig) (string, []any) {
	cond, bargs := sens.BatchServiceSQL("service_name")
	return OpP99PivotQuery(OpP99PivotSpec{
		SlotStarts:  []time.Time{cur},
		BaseStart:   cur.Add(-svcSlowBaseLookback),
		AlignedNow:  cur.Add(SvcSlowdownBucket),
		Floors:      OpP99Floors{MinCalls: cfg.MinCallsPerOp, Ratio: cfg.RiseFactor, MinP99Ms: cfg.MinP99Ms},
		ExcludeCond: cond,
		ExcludeArgs: bargs,
		CurP95MinMs: SvcSlowP95Floor(cfg),
		BaseP95:     true,
		TimeoutSec:  int(SvcSlowReadTimeout / time.Second),
	})
}

// SvcSlowP95Floor — yavaş pay tabanı: cari kova p95'i ≥ MinP99Ms / 2 (vars.
// 2500 ms). SQL HAVING ile Go ikizi (evaluator svcSlowOpQualifies) aynı sayıyı
// buradan alır.
func SvcSlowP95Floor(cfg ServiceSlowdownConfig) float64 {
	return cfg.MinP99Ms / 2
}

// ServiceSlowdownOps — operasyon pivotu; truncated = satır tavanına dayandı
// (eksik küme: görünmeyen servis "temiz" sayılmaz).
func (s *Store) ServiceSlowdownOps(ctx context.Context, cur time.Time, cfg ServiceSlowdownConfig, sens AnomalySensitivityConfig) ([]SvcSlowOpRow, bool, error) {
	q, args := ServiceSlowdownOpsQuery(cur, cfg, sens)
	rows, err := s.telemetryReadConn().Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []SvcSlowOpRow{}
	for rows.Next() {
		var r SvcSlowOpRow
		if err := rows.Scan(&r.Service, &r.Operation, &r.CurP99Ms, &r.BaseP95Ms, &r.CurCalls, &r.BaseCalls, &r.CurP95Ms); err != nil {
			return nil, false, err
		}
		r.CurP99Ms, r.BaseP95Ms, r.CurP95Ms = finiteOrZero(r.CurP99Ms), finiteOrZero(r.BaseP95Ms), finiteOrZero(r.CurP95Ms)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return out, len(out) >= OpP99PivotLimit, nil
}

// serviceSlowdownServicesSQL — SAF metin. Bind sırası: cur (cari çağrı), saat
// başı, cur (saat sonu), cur (cari p99), cur (taban p95 sonu), taban başı,
// cari kova sonu, [batch kalıpları], servisler (dizi), saat çağrı tabanı,
// 100 × saat kovası, (100 − dropPct), cari çağrı tabanı, mutlak p99 tabanı,
// p99 katı.
//
// Çöküş kolu (inceleme B) ayrıca cari kovada ≥ MinCallsTotal çağrı VE servis
// p99'u ≥ MinP99Ms ister: 150 → 60 çağrılık sessiz bir serviste tek 300 ms'lik
// istek "−%60 + p99 3×" yapıyordu. Taban servis p95'i (operasyon kolu gibi).
//
// Çöküş koşulu tamsayı/ondalık çapraz çarpımla (bölme yok, NaN yok):
//
//	cur_calls ≤ (1 − drop/100) × hour_calls / 12
//	⇔ cur_calls × 1200 ≤ (100 − drop) × hour_calls
func serviceSlowdownServicesSQL(batchCond string) string {
	excl := ""
	if batchCond != "" {
		excl = `
		  AND NOT ` + batchCond
	}
	return `
		SELECT service_name,
		       countMergeIf(span_count_state, time_bucket >= ?)                     AS cur_calls,
		       countMergeIf(span_count_state, time_bucket >= ? AND time_bucket < ?) AS hour_calls,
		       arrayElement(quantilesTDigestMergeIf(0.5, 0.95, 0.99)(duration_q_state, time_bucket >= ?), 3) / 1e6 AS cur_p99,
		       arrayElement(quantilesTDigestMergeIf(0.5, 0.95, 0.99)(duration_q_state, time_bucket < ?), 2) / 1e6  AS base_p95
		FROM service_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ?` + excl + `
		GROUP BY service_name
		HAVING service_name IN ?
		    OR (hour_calls >= ? AND cur_calls * ? <= ? * hour_calls
		        AND cur_calls >= ? AND cur_p99 >= ?
		        AND base_p95 > 0 AND cur_p99 >= ? * base_p95)
		ORDER BY cur_p99 / base_p95 DESC
		LIMIT ` + itoa(svcSlowServiceLimit) + `
		SETTINGS max_execution_time = 10`
}

// ServiceSlowdownServicesQuery — SAF: servis özeti SQL'i + argümanlar (golden).
// services: operasyon kolunun aday servisleri + açık problemlerin servisleri
// (nil → boş dizi).
func ServiceSlowdownServicesQuery(cur time.Time, cfg ServiceSlowdownConfig, sens AnomalySensitivityConfig, services []string) (string, []any) {
	if services == nil {
		services = []string{}
	}
	cond, bargs := sens.BatchServiceSQL("service_name")
	hourStart := cur.Add(-SvcSlowHourBuckets * SvcSlowdownBucket)
	args := []any{cur, hourStart, cur, cur, cur, cur.Add(-svcSlowBaseLookback), cur.Add(SvcSlowdownBucket)}
	args = append(args, bargs...)
	args = append(args, services,
		uint64(cfg.MinCallsTotal)*SvcSlowHourBuckets, uint64(100*SvcSlowHourBuckets), 100-cfg.DropPct,
		uint64(cfg.MinCallsTotal), cfg.MinP99Ms, SvcSlowCollapseP99Factor)
	return serviceSlowdownServicesSQL(cond), args
}

// ServiceSlowdownServices — servis özeti; truncated = satır tavanına dayandı.
func (s *Store) ServiceSlowdownServices(ctx context.Context, cur time.Time, cfg ServiceSlowdownConfig, sens AnomalySensitivityConfig, services []string) ([]SvcSlowServiceRow, bool, error) {
	q, args := ServiceSlowdownServicesQuery(cur, cfg, sens, services)
	rows, err := s.telemetryReadConn().Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []SvcSlowServiceRow{}
	for rows.Next() {
		var r SvcSlowServiceRow
		if err := rows.Scan(&r.Service, &r.CurCalls, &r.HourCalls, &r.CurP99Ms, &r.BaseP95Ms); err != nil {
			return nil, false, err
		}
		r.CurP99Ms, r.BaseP95Ms = finiteOrZero(r.CurP99Ms), finiteOrZero(r.BaseP95Ms)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return out, len(out) >= svcSlowServiceLimit, nil
}

// SvcSlowdownRuleID — kural id = problem id: "svc-slowdown:<servis>".
func SvcSlowdownRuleID(service string) string { return RuleSvcSlowdownPrefix + service }
