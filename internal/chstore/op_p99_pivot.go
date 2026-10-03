package chstore

// op_p99_pivot.go — operation_summary_5m üzerinde (servis, operasyon) başına
// cari kova(lar) p99'u ile 24 sa taban p99'unu TEK geçişte karşılaştıran MV
// pivotu. v0.10.1091'te internal/anomaly/op_latency.go'dan buraya taşındı
// (metin ve argümanlar BAYT BAYT aynı — anomaly paketinin golden testleri
// pinler): iki tüketici var artık —
//   - trace_op_latency (anomaly, recorder): tabanlar 30 çağrı / 200 ms / 3×,
//     sürdürme penceresi, batch yük kapısı, aktif-olay muafiyeti;
//   - yaygın yavaşlama (`svc-slowdown:`, evaluator): tabanlar 30 çağrı /
//     5000 ms / 20×, tek kova, batch servisler iç WHERE'de düşer.
// Kopyalamak yerine tabanlar ve iki ek koşul parametre oldu; yeni bir sorgu
// biçimi yok.

import (
	"fmt"
	"strings"
	"time"
)

// OpPair — (servis, operasyon) çifti (trace_op_latency olayının kimliği,
// FingerprintAnomaly(kind, operasyon, servis)). anomaly.opLatPair bunun takma
// adıdır.
type OpPair struct{ Service, Operation string }

// OpP99Floors — HAVING'in üç tabanı. Tipler bilinçli (int / float64): bind
// argümanlarının dinamik tipi taşınmadan önceki metinle aynı kalsın.
type OpP99Floors struct {
	MinCalls int     // cari VE taban penceresinde en az çağrı
	Ratio    float64 // cur_p99 ≥ Ratio × base_p99
	MinP99Ms float64 // cur_p99 ≥ MinP99Ms (mutlak)
}

// OpP99BatchGate — trace_op_latency'nin v0.10.1046 batch yük kapısı. Cond boş
// = kapı yok (metin kapısız biçimle birebir).
type OpP99BatchGate struct {
	Cond        string // BatchServiceSQL("service_name") koşulu
	Args        []any  // Cond'un argümanları
	Exempt      []OpPair
	SurgeFactor int    // batchLoadSurgeFactor (2)
	CurBuckets  uint64 // cari penceredeki 5 dk kova sayısı
}

// OpP99PivotSpec — pivotun girdileri.
//
// SlotStarts — sürdürme penceresinin kova başları, yeniden eskiye: [0] en yeni
// tamamlanmış kova, len = dwell ≥ 1. BaseStart / AlignedNow — taban
// penceresinin başı ve hizalı "şimdi" (iç WHERE sınırları).
//
// Active — olayı zaten AKTİF çiftler (dwell ≥ 2'de anlamlı): önceki kovaların
// koşullarından muaf.
//
// ExcludeCond / ExcludeArgs — v0.10.1091: iç WHERE'e `AND NOT <koşul>` (yaygın
// yavaşlama batch servisleri burada düşürür — LIMIT onlara harcanmasın). Boş =
// koşul yok, metin taşınmadan önceki biçimle birebir.
type OpP99PivotSpec struct {
	SlotStarts            []time.Time
	BaseStart, AlignedNow time.Time
	Floors                OpP99Floors
	Batch                 OpP99BatchGate
	Active                []OpPair
	ExcludeCond           string
	ExcludeArgs           []any
	// CurP95MinMs — v0.10.1091 (yaygın yavaşlama, inceleme A): cari kovanın
	// p95'i en az bu kadar olmalı (YAVAŞ PAY tabanı). İç içe span'leri olan TEK
	// yavaş trace (sunucu + iç + istemci operasyonu, ~40 çağrı) üç operasyonun
	// p99'unu birden "max"a çeker; p95 ise ancak çağrıların ~%5'inden fazlası
	// yavaşsa yükselir. Aynı tDigest'ten (arrayElement(…, 2)), ek tarama yok.
	// 0 = koşul yok (op_latency: metin birebir).
	CurP95MinMs float64
	// BaseP95 — v0.10.1091 (inceleme D): taban ölçüsü 24 sa p99 yerine HAVUZLANMIŞ
	// p95 (aynı tDigest): dünkü 10–60 dk'lık bir olay taban p99'unu olay
	// seviyesine çeker ve bugünkü aynı olayı "20× değil" diye susturur; p95 ancak
	// olay pencerenin %5'ini (~72 dk) aşarsa kıpırdar. Kolon adı base_p95.
	// Kova başı p99 medyanı (iç GROUP BY time_bucket) filo ölçeğinde (işlem ×
	// 288 kova tDigest durumu) bellek olarak ağır — bilinçli seçilmedi.
	BaseP95 bool
	// TimeoutSec — max_execution_time; 0 = OpP99PivotTimeoutSec (25).
	TimeoutSec int
}

// OpP99PivotLimit / OpP99PivotTimeoutSec — pivotun satır tavanı ve süre sınırı.
const (
	OpP99PivotLimit      = 200
	OpP99PivotTimeoutSec = 25
)

// OpP99PivotQuery — SAF: pivot SQL'i + argümanlar.
//
// dwell = 1 VE batch kapısı yok → metin v0.10.1046 ÖNCESİYLE BİREBİR
// (anomaly TestOpLatencyQueryLegacyIdentity); dwell = 1 + kapı → v0.10.1046
// metni birebir.
//
// v0.10.1085 — dwell ≥ 2 (sürdürme), AYNI tek geçiş (ek tarama yok, çift başına
// döngü yok): iç alt sorgunun is_cur'u kova numarasına genişler —
// multiIf(time_bucket >= ?, 1, time_bucket >= ?, 2, …, 0) AS slot (1 = en yeni,
// 0 = taban) ve GROUP BY slot; dış sorgu her önceki kova için p99_<i> /
// calls_<i> kolonu taşır, HAVING her kovaya aynı üç tabanı uygular (LIMIT
// yalnız sürdürmeyi geçebilecek satırlarda ısırır). Verisiz kova maxIf/sumIf'te
// 0 → çağrı tabanının altı → elenir.
//
// Batch kapısı dolu → üç ek, hepsi aynı geçişte (ek tarama yok):
//   - iç alt sorguya uniqExact(time_bucket) AS buckets (uniqExact ŞART:
//     birleşmemiş parçalarda aynı kova birden çok satırdır), dışa
//     maxIf(buckets, is_cur = 0) AS base_buckets;
//   - HAVING'e TEK eleme: batch servis VE çift muaf değil VE her kova
//     calls * base_buckets >= SurgeFactor * base_calls * CurBuckets;
//   - muaf çiftler skaler yer tutucularla tuple NOT IN.
//
// Koşullar HAVING'de, Go'da DEĞİL: LIMIT p99 oranına göre sıralı; Go'da
// elenecek satırlar LIMIT'i doldurup gerçek sıçramaları dışarıda bırakırdı
// (v0.9.327'nin dersi: LIMIT yalnız hayatta kalabilecek satırlarda ısırmalı).
func OpP99PivotQuery(sp OpP99PivotSpec) (string, []any) {
	dwell := len(sp.SlotStarts)
	f := sp.Floors
	timeout := sp.TimeoutSec
	if timeout <= 0 {
		timeout = OpP99PivotTimeoutSec
	}
	// dwell = 1 → v0.10.1046 metni (is_cur). Kova numarası 1 = cari, 0 = taban
	// iki biçimde de aynı, dış kolonlar yalnız adı değiştirir.
	slot, slotExpr := "is_cur", `time_bucket >= ? AS is_cur`
	if dwell > 1 {
		conds := make([]string, dwell)
		for i := range conds {
			conds[i] = fmt.Sprintf("time_bucket >= ?, %d", i+1)
		}
		slot, slotExpr = "slot", "multiIf("+strings.Join(conds, ", ")+", 0) AS slot"
	}
	// v0.10.1091 — p95 kolonu yalnız yaygın yavaşlamada (yavaş pay tabanı ya da
	// p95 tabanı); op_latency'nin metni birebir kalır.
	withP95 := sp.CurP95MinMs > 0 || sp.BaseP95
	base, baseSel := "base_p99", `maxIf(p99, `+slot+` = 0)   AS base_p99`
	if sp.BaseP95 {
		base, baseSel = "base_p95", `maxIf(p95, `+slot+` = 0)   AS base_p95`
	}
	quant := `arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p99,`
	if withP95 {
		quant = `quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state) AS q,
		         arrayElement(q, 3) / 1e6 AS p99,
		         arrayElement(q, 2) / 1e6 AS p95,`
	}
	sel := `
		SELECT service_name, name,
		       maxIf(p99, ` + slot + ` = 1)   AS cur_p99,
		       ` + baseSel + `,
		       sumIf(calls, ` + slot + ` = 1) AS cur_calls,
		       sumIf(calls, ` + slot + ` = 0) AS base_calls`
	inner := `
		         countMerge(span_count_state) AS calls`
	having := `
		HAVING cur_calls >= ? AND base_calls >= ?
		   AND ` + base + ` > 0 AND cur_p99 >= ? * ` + base + ` AND cur_p99 >= ?`
	args := make([]any, 0, 16)
	for _, s := range sp.SlotStarts {
		args = append(args, s)
	}
	args = append(args, sp.BaseStart, sp.AlignedNow)
	// v0.10.1091 — iç WHERE dışlaması (zaman sınırlarından hemen sonra bağlanır).
	where := ""
	if sp.ExcludeCond != "" {
		where = `
		    AND NOT ` + sp.ExcludeCond
		args = append(args, sp.ExcludeArgs...)
	}
	args = append(args, f.MinCalls, f.MinCalls, f.Ratio, f.MinP99Ms)
	// v0.10.1085 — önceki kovalar: kolon + aynı üç taban (en yeni kovanınkiyle
	// aynı biçim). Tamsayı kova numarası metne gömülür, bind değil. Aktif
	// çiftler önceki kova koşullarından muaf (OR tuple IN).
	var earlier []string
	for i := 2; i <= dwell; i++ {
		sel += fmt.Sprintf(`,
		       maxIf(p99, slot = %d)   AS p99_%d,
		       sumIf(calls, slot = %d) AS calls_%d`, i, i, i, i)
		earlier = append(earlier, fmt.Sprintf(`calls_%d >= ? AND p99_%d >= ? * %s AND p99_%d >= ?`, i, i, base, i))
		args = append(args, f.MinCalls, f.Ratio, f.MinP99Ms)
	}
	switch {
	case len(earlier) == 0:
	case len(sp.Active) == 0:
		for _, c := range earlier {
			having += `
		   AND ` + c
		}
	default:
		tuples := make([]string, len(sp.Active))
		for i := range tuples {
			tuples[i] = "(?, ?)"
		}
		having += `
		   AND ((` + strings.Join(earlier, `
		         AND `) + `)
		        OR (service_name, name) IN (` + strings.Join(tuples, ", ") + `))`
		for _, p := range sp.Active {
			args = append(args, p.Service, p.Operation)
		}
	}
	if sp.CurP95MinMs > 0 {
		sel += `,
		       maxIf(p95, ` + slot + ` = 1)   AS cur_p95`
		having += `
		   AND cur_p95 >= ?`
		args = append(args, sp.CurP95MinMs)
	}
	if b := sp.Batch; b.Cond != "" {
		sel += `,
		       maxIf(buckets, ` + slot + ` = 0) AS base_buckets`
		inner += `,
		         uniqExact(time_bucket) AS buckets`
		exempt := ""
		if len(b.Exempt) > 0 {
			tuples := make([]string, len(b.Exempt))
			for i := range tuples {
				tuples[i] = "(?, ?)"
			}
			exempt = ` AND (service_name, name) NOT IN (` + strings.Join(tuples, ", ") + `)`
		}
		having += `
		   AND NOT (` + b.Cond + exempt + `
		            AND cur_calls * base_buckets >= ? * base_calls * ?`
		for i := 2; i <= dwell; i++ {
			having += fmt.Sprintf(`
		            AND calls_%d * base_buckets >= ? * base_calls * ?`, i)
		}
		having += `)`
		args = append(args, b.Args...)
		for _, p := range b.Exempt {
			args = append(args, p.Service, p.Operation)
		}
		for i := 1; i <= dwell; i++ {
			args = append(args, b.SurgeFactor, b.CurBuckets)
		}
	}
	return sel + `
		FROM (
		  SELECT service_name, name,
		         ` + slotExpr + `,
		         ` + quant + inner + `
		  FROM operation_summary_5m
		  WHERE time_bucket >= ? AND time_bucket < ?` + where + `
		  GROUP BY service_name, name, ` + slot + `
		)
		GROUP BY service_name, name` + having + `
		ORDER BY cur_p99 / ` + base + ` DESC
		LIMIT ` + itoa(OpP99PivotLimit) + `
		SETTINGS max_execution_time = ` + itoa(int64(timeout)), args
}
