package chstore

// op_p99_cached.go — v0.10.1118: operasyon p99 pivotunun (OpP99PivotQuery)
// TABANI ÖNBELLEKLİ yolu — tur başına yalnız CARİ kovalar okunur.
//
// Ölçüm (operatör prod system.query_log, son 1 sa): en pahalı sorgu bu pivot —
// n=13/sa, ort. 1692 ms, en çok 2524 ms, ort. okuma ≈ 974 MiB; kardeş biçim
// (yaygın yavaşlama, p95'li) n=3/sa, 774 ms, 977 MiB; toplam ≈ 16 GB/sa. Her tur
// ~24 sa'lik tDigest durumlarını YALNIZ taban (slot 0) için yeniden
// birleştiriyordu; taban turlar arasında neredeyse kıpırdamaz.
//
// Yol (SQL'de iki sınırlı sorgu + SAF Go birleşimi):
//
//  1. TABAN — OpBaselineQuery: (servis, operasyon) başına base_p99, base_p95,
//     base_calls, base_buckets; TEK geçiş, [E − lookback, E), E = tazeleme
//     anında iki kapsamın İLK cari kovalarının en erkeni (eski sorgunun taban
//     sonu). Saatte bir arka planda tazelenir, iki kapsamca paylaşılır,
//     liderin belleğinde durur (op_p99_cache.go).
//  2. TUR — OpP99CurrentQuery: yalnız sürdürme penceresinin kovaları
//     [SlotStarts[dwell−1], AlignedNow); tabandan BAĞIMSIZ tabanlar HAVING'de
//     (cur_calls, cur_p99, cur_p95, önceki kovaların çağrı + mutlak p99'u; aktif
//     çift önceki kova koşullarından muaf — eski HAVING'in tabana bağlı
//     olmayan yarısı, gerekli koşul).
//  3. BİRLEŞİM — JoinOpP99Pivot: eski HAVING'in tabana bağlı koşulları
//     (base_calls, base > 0, her kovada cur ≥ kat × taban, batch yük kapısı +
//     muaf çiftler), ORDER BY oran DESC, LIMIT OpP99PivotLimit — birebir.
//     Eşdeğerlik testi: op_p99_cached_test.go (eski SQL'in Go referansı +
//     clickhouse local'de iki yolun aynı satırları döndürdüğü canlı motor testi).
//
// Tek bilinçli fark — TABAN GECİKMESİ: önbellekteki pencerenin sonu tazeleme
// anındaki ilk cari kova; bir sonraki tazelemeye dek (≤ 1 sa + tazeleme süresi)
// taban penceresi gerçeğin ≤ 1 sa gerisinde kalır. Pencere sonu hiçbir zaman
// ilk cari kovadan SONRA değil (opBaselineDecide "ahead" → eski yol): bir olay
// kendi tabanını eskisinden fazla seyreltemez — aradaki ≤ 1 sa'lik dilim
// tabana da cari kovalara da girmez.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// OpP99Slot — bir kovanın ölçüsü (p99 ms + çağrı).
type OpP99Slot struct {
	P99Ms float64
	Calls uint64
}

// OpP99Row — pivotun çıktı satırı, OpP99PivotQuery'nin kolon sırasıyla
// (OpP99PivotScanDest). BaseMs = base_p99 (BaseP95'te base_p95). Earlier =
// sürdürme penceresinin en yeniden önceki kovaları, yeniden eskiye (dwell − 1
// eleman). CurP95Ms yalnız CurP95MinMs > 0'da, BaseBuckets yalnız batch kapısı
// doluyken dolar (eski sorgu o kolonları yalnız o zaman taşır).
type OpP99Row struct {
	Service, Operation  string
	CurP99Ms, BaseMs    float64
	CurCalls, BaseCalls uint64
	Earlier             []OpP99Slot
	CurP95Ms            float64
	BaseBuckets         uint64
}

// OpP99CurRow — tur sorgusunun satırı (OpP99CurrentQuery). Slots[0] = en yeni
// kova (slot 1), len = dwell; verisiz kova sıfır (maxIf/sumIf varsayılanı).
// CurP95Ms = en yeni kovanın p95'i. IsBatch = batch kalıp koşulu (Batch.Cond)
// CH'de değerlendirilmiş — eski HAVING'deki AYNI SQL yüklemi (Go ikizi değil).
type OpP99CurRow struct {
	Service, Operation string
	Slots              []OpP99Slot
	CurP95Ms           float64
	IsBatch            bool
}

// OpBaseline — çiftin önbellekteki tabanı (OpBaselineQuery).
type OpBaseline struct {
	P99Ms, P95Ms   float64
	Calls, Buckets uint64
}

// OpP99CurrentLimit — tur sorgusunun satır tavanı. Sorgu oran sırası
// bilemez (taban Go'da); tavana dayanan sonuç EKSİK olabilir → çağıran eski
// tek geçişe düşer (opPivotCache.pivot), birebirlik korunur.
const OpP99CurrentLimit = 50_000

// OpBaselineLimit / OpBaselineTimeoutSec — taban tazelemesinin satır tavanı ve
// süre sınırı. Tazeleme sıcak yolda değil (arka plan); tavana dayanırsa taban
// kullanılmaz (eksik taban = sessiz kaçırma) → eski yol, log + metrik.
const (
	OpBaselineLimit      = 500_000
	OpBaselineTimeoutSec = 60
)

// OpP99PivotScanDest — eski tek geçişin (OpP99PivotQuery) satır hedefleri, kolon
// sırasıyla: 6 temel kolon, önceki kovalar p99_<i> / calls_<i>, [cur_p95],
// [base_buckets].
func OpP99PivotScanDest(r *OpP99Row, sp OpP99PivotSpec) []any {
	dwell := len(sp.SlotStarts)
	if dwell < 1 {
		dwell = 1
	}
	dest := []any{&r.Service, &r.Operation, &r.CurP99Ms, &r.BaseMs, &r.CurCalls, &r.BaseCalls}
	r.Earlier = make([]OpP99Slot, dwell-1)
	for i := range r.Earlier {
		dest = append(dest, &r.Earlier[i].P99Ms, &r.Earlier[i].Calls)
	}
	if sp.CurP95MinMs > 0 {
		dest = append(dest, &r.CurP95Ms)
	}
	if sp.Batch.Cond != "" {
		dest = append(dest, &r.BaseBuckets)
	}
	return dest
}

// OpP99CurrentQuery — SAF: tur sorgusu. Yalnız sürdürme penceresinin kovaları
// (iç WHERE [SlotStarts[dwell−1], AlignedNow) — taban okunmaz); iç alt sorgu
// eski sorgunun AYNI kova numarası (multiIf, 1 = en yeni) ve AYNI tDigest
// birleşimi; dış sorgu kova başına p99/çağrı + en yeni kovanın p95'i, batch
// kapısı doluyken toUInt8(<Batch.Cond>) AS is_batch.
//
// HAVING — eski HAVING'in tabandan BAĞIMSIZ koşulları (gerekli koşul; nihai
// hüküm JoinOpP99Pivot'ta): cur_calls ≥ MinCalls, cur_p99 ≥ MinP99Ms,
// [cur_p95 ≥ CurP95MinMs], önceki her kovada çağrı + mutlak p99 (aktif çift
// muaf — eski metnin aynı OR tuple IN'i). Oran sırası yok (taban yok): LIMIT
// OpP99CurrentLimit tavandır, dayanırsa çağıran eski yola düşer.
//
// Bind sırası metin sırası: [batch kalıpları], kova başları, pencere başı,
// AlignedNow, [dışlama], MinCalls, MinP99Ms, [CurP95MinMs], önceki kova başına
// (MinCalls, MinP99Ms), [aktif çiftler].
func OpP99CurrentQuery(sp OpP99PivotSpec) (string, []any) {
	dwell := len(sp.SlotStarts)
	f := sp.Floors
	timeout := sp.TimeoutSec
	if timeout <= 0 {
		timeout = OpP99PivotTimeoutSec
	}
	args := make([]any, 0, 16)
	sel := `
		SELECT service_name, name,
		       maxIf(p99, slot = 1)   AS cur_p99,
		       sumIf(calls, slot = 1) AS cur_calls,
		       maxIf(p95, slot = 1)   AS cur_p95`
	for i := 2; i <= dwell; i++ {
		sel += fmt.Sprintf(`,
		       maxIf(p99, slot = %d)   AS p99_%d,
		       sumIf(calls, slot = %d) AS calls_%d`, i, i, i, i)
	}
	if sp.Batch.Cond != "" {
		sel += `,
		       toUInt8(` + sp.Batch.Cond + `) AS is_batch`
		args = append(args, sp.Batch.Args...)
	}
	conds := make([]string, dwell)
	for i := range conds {
		conds[i] = fmt.Sprintf("time_bucket >= ?, %d", i+1)
	}
	for _, s := range sp.SlotStarts {
		args = append(args, s)
	}
	args = append(args, sp.SlotStarts[dwell-1], sp.AlignedNow)
	where := ""
	if sp.ExcludeCond != "" {
		where = `
		    AND NOT ` + sp.ExcludeCond
		args = append(args, sp.ExcludeArgs...)
	}
	having := `
		HAVING cur_calls >= ? AND cur_p99 >= ?`
	args = append(args, f.MinCalls, f.MinP99Ms)
	if sp.CurP95MinMs > 0 {
		having += `
		   AND cur_p95 >= ?`
		args = append(args, sp.CurP95MinMs)
	}
	if dwell > 1 {
		earlier := make([]string, 0, dwell-1)
		for i := 2; i <= dwell; i++ {
			earlier = append(earlier, fmt.Sprintf(`calls_%d >= ? AND p99_%d >= ?`, i, i))
			args = append(args, f.MinCalls, f.MinP99Ms)
		}
		if len(sp.Active) == 0 {
			for _, c := range earlier {
				having += `
		   AND ` + c
			}
		} else {
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
	}
	return sel + `
		FROM (
		  SELECT service_name, name,
		         multiIf(` + strings.Join(conds, ", ") + `, 0) AS slot,
		         quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state) AS q,
		         arrayElement(q, 3) / 1e6 AS p99,
		         arrayElement(q, 2) / 1e6 AS p95,
		         countMerge(span_count_state) AS calls
		  FROM operation_summary_5m
		  WHERE time_bucket >= ? AND time_bucket < ?` + where + `
		  GROUP BY service_name, name, slot
		)
		GROUP BY service_name, name` + having + `
		LIMIT ` + itoa(OpP99CurrentLimit) + `
		SETTINGS max_execution_time = ` + itoa(int64(timeout)), args
}

// OpP99CurrentScanDest — tur sorgusunun satır hedefleri (OpP99CurrentQuery'nin
// kolon sırası). isBatch: batch kapısı doluysa is_batch kolonunun hedefi
// (UInt8); çağıran r.IsBatch = *isBatch != 0 yapar.
func OpP99CurrentScanDest(r *OpP99CurRow, sp OpP99PivotSpec, isBatch *uint8) []any {
	dwell := len(sp.SlotStarts)
	if dwell < 1 {
		dwell = 1
	}
	r.Slots = make([]OpP99Slot, dwell)
	dest := []any{&r.Service, &r.Operation, &r.Slots[0].P99Ms, &r.Slots[0].Calls, &r.CurP95Ms}
	for i := 1; i < dwell; i++ {
		dest = append(dest, &r.Slots[i].P99Ms, &r.Slots[i].Calls)
	}
	if sp.Batch.Cond != "" {
		dest = append(dest, isBatch)
	}
	return dest
}

// OpBaselineQuery — SAF: taban tazelemesi. [start, end) penceresinde çift
// başına havuzlanmış p99 + p95 (AYNI tDigest birleşimi — iki çağıranın
// ölçüsü tek okumada), çağrı ve AKTİF kova sayısı (uniqExact — birleşmemiş
// parçalarda aynı kova birden çok satırdır; eski sorgunun base_buckets'ı).
// Dışlama YOK: taban iki kapsamca paylaşılır (op_p99_cache.go); svc-slowdown'un
// batch dışlaması servis bazlı satır süzgecidir, bir çiftin tabanını
// değiştirmez ve tur sorgusunun iç WHERE'inde uygulanır. HAVING base_calls ≥
// MinCalls: altındaki çift eski HAVING'de de elenir (önbellekte olmaması =
// base_calls 0 = aynı hüküm) — bellek yalnız hüküm verebilecek çiftlere.
func OpBaselineQuery(start, end time.Time, minCalls int) (string, []any) {
	args := []any{start, end, minCalls}
	return `
		SELECT service_name, name,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state) AS q, 3) / 1e6 AS base_p99,
		       arrayElement(q, 2) / 1e6     AS base_p95,
		       countMerge(span_count_state) AS base_calls,
		       uniqExact(time_bucket)       AS base_buckets
		FROM operation_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ?
		GROUP BY service_name, name
		HAVING base_calls >= ?
		LIMIT ` + itoa(OpBaselineLimit) + `
		SETTINGS max_execution_time = ` + itoa(OpBaselineTimeoutSec), args
}

// opCallsAtLeast — SQL `calls >= ?` (UInt64 ile tamsayı bind): negatif / sıfır
// taban her değeri geçirir.
func opCallsAtLeast(calls uint64, min int) bool {
	return min <= 0 || calls >= uint64(min)
}

// JoinOpP99Pivot — SAF: tur satırları + önbellekteki taban → eski tek geçişin
// (OpP99PivotQuery) döndüreceği satırlar. Eski HAVING / ORDER BY / LIMIT'in
// birebir Go karşılığı (kolon kolon):
//
//	cur_calls ≥ MinCalls AND base_calls ≥ MinCalls
//	AND base > 0 AND cur_p99 ≥ Ratio × base AND cur_p99 ≥ MinP99Ms
//	AND [her önceki kova: calls_i ≥ MinCalls AND p99_i ≥ Ratio × base AND
//	     p99_i ≥ MinP99Ms] — ya da çift Active'te
//	AND [cur_p95 ≥ CurP95MinMs]
//	AND NOT [is_batch AND çift Batch.Exempt'te değil AND her kova
//	         calls × base_buckets ≥ SurgeFactor × base_calls × CurBuckets]
//	ORDER BY cur_p99 / base DESC LIMIT OpP99PivotLimit
//
// Tabanı olmayan çift = base_calls 0, taban 0 (maxIf/sumIf varsayılanı) → eski
// HAVING'de de elenir. Karşılaştırmalar SQL'deki biçimle (çarpım, bölme değil)
// ve aynı float64 değerlerle — sınırda da aynı hüküm.
//
// Eşitlikte sıra: SQL'in ORDER BY'ı eşit oranlı satırların sırasını
// TANIMLAMAZ (LIMIT kesiminde hangisinin kalacağı belirsizdi); burada servis,
// sonra operasyon artan — SQL'in izin verdiği sıralardan biri, artık kararlı.
func JoinOpP99Pivot(sp OpP99PivotSpec, cur []OpP99CurRow, base map[OpPair]OpBaseline) []OpP99Row {
	dwell := len(sp.SlotStarts)
	if dwell < 1 {
		dwell = 1
	}
	f := sp.Floors
	active := make(map[OpPair]bool, len(sp.Active))
	for _, p := range sp.Active {
		active[p] = true
	}
	g := sp.Batch
	exempt := make(map[OpPair]bool, len(g.Exempt))
	for _, p := range g.Exempt {
		exempt[p] = true
	}
	surge := uint64(0)
	if g.SurgeFactor > 0 {
		surge = uint64(g.SurgeFactor)
	}
	out := []OpP99Row{}
	for _, r := range cur {
		p := OpPair{Service: r.Service, Operation: r.Operation}
		b := base[p]
		bv := b.P99Ms
		if sp.BaseP95 {
			bv = b.P95Ms
		}
		slot := func(i int) OpP99Slot {
			if i < len(r.Slots) {
				return r.Slots[i]
			}
			return OpP99Slot{}
		}
		c := slot(0)
		if !opCallsAtLeast(c.Calls, f.MinCalls) || !opCallsAtLeast(b.Calls, f.MinCalls) ||
			!(bv > 0) || !(c.P99Ms >= f.Ratio*bv) || !(c.P99Ms >= f.MinP99Ms) {
			continue
		}
		if dwell > 1 && !active[p] {
			ok := true
			for i := 1; i < dwell; i++ {
				s := slot(i)
				if !opCallsAtLeast(s.Calls, f.MinCalls) || !(s.P99Ms >= f.Ratio*bv) || !(s.P99Ms >= f.MinP99Ms) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
		}
		if sp.CurP95MinMs > 0 && !(r.CurP95Ms >= sp.CurP95MinMs) {
			continue
		}
		if g.Cond != "" && r.IsBatch && !exempt[p] {
			underLoad := true
			for i := 0; i < dwell; i++ {
				if !(slot(i).Calls*b.Buckets >= surge*b.Calls*g.CurBuckets) {
					underLoad = false
					break
				}
			}
			if underLoad {
				continue
			}
		}
		row := OpP99Row{
			Service: r.Service, Operation: r.Operation,
			CurP99Ms: c.P99Ms, BaseMs: bv, CurCalls: c.Calls, BaseCalls: b.Calls,
			Earlier: make([]OpP99Slot, dwell-1),
		}
		for i := range row.Earlier {
			row.Earlier[i] = slot(i + 1)
		}
		if sp.CurP95MinMs > 0 {
			row.CurP95Ms = r.CurP95Ms
		}
		if g.Cond != "" {
			row.BaseBuckets = b.Buckets
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := out[i].CurP99Ms/out[i].BaseMs, out[j].CurP99Ms/out[j].BaseMs
		if ri != rj {
			return ri > rj
		}
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Operation < out[j].Operation
	})
	if len(out) > OpP99PivotLimit {
		out = out[:OpP99PivotLimit]
	}
	return out
}
