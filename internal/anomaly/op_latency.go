package anomaly

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// op_latency.go — operasyon düzeyi GECİKME anomalisi (v0.9.1064,
// Faz 2.3 / G5). trace_ops hattı yalnız HATA sayıyordu: trafiğin %2'si
// olan bir endpoint 10× yavaşlarsa servis p99'u kıpırdamaz ve "hangi
// endpoint" — SRE'nin ikinci sorusu — cevapsız kalırdı.
// operation_summary_5m zaten duration_q_state taşıyor; trace_ops'un
// cur/base şekli p99'a uygulanır (aynı MV-pivot, aynı LIMIT/timeout
// disiplini, aynı dar örnek-trace ikinci sorgusu).

// OpLatencyAnomaly — kalifiye bir (servis, operasyon) gecikme sıçraması.
type OpLatencyAnomaly struct {
	Service       string  `json:"service"`
	Operation     string  `json:"operation"`
	CurP99Ms      float64 `json:"curP99Ms"`
	BaseP99Ms     float64 `json:"baseP99Ms"`
	Ratio         float64 `json:"ratio"` // cur/base
	CurCalls      uint64  `json:"curCalls"`
	SampleTraceID string  `json:"sampleTraceId"` // en yavaş cari span'in trace'i
	LastSeenNs    int64   `json:"lastSeenNs"`
}

// Kalifikasyon eşikleri — trace_ops'un v0.9.327 felsefesiyle aynı:
// küçük sayılar üstünde aritmetik olay değildir.
const (
	// opLatencyMinCalls — İKİ pencerede de hacim tabanı. 30 çağrının
	// p99'u tek kuyruk isteğidir; baseline tarafında da aynı taban
	// (oturmamış baseline'a karşı oran anlamsız).
	opLatencyMinCalls = 30
	// opLatencyMinRatio — günlük dalgalanmanın dışı. Hata hattının
	// traceOpMinRatio'suyla aynı sayı; iki dedektör "sıçrama" için
	// aynı şeyi söylesin.
	opLatencyMinRatio = 3.0
	// opLatencyMinP99Ms — mutlak taban. 20ms'lik bir operasyonun 3×'i
	// filo ölçeğinde operatör dikkati değildir; 200ms üstü kuyruk
	// gerçek kullanıcı acısıdır.
	opLatencyMinP99Ms = 200.0
)

// opLatencyKind — anomaly_events türü (recorder yazar; batch kapısının
// aktif-olay okuması aynı adla okur).
const opLatencyKind = "trace_op_latency"

// opLatencyBucket — MV pivotunun ham satırı (saf sınıflayıcı girdisi).
type opLatencyBucket struct {
	Service   string
	Operation string
	CurP99Ms  float64
	BaseP99Ms float64
	CurCalls  uint64
	BaseCalls uint64
	// BaseBuckets — v0.10.1046: taban penceresinde çiftin span taşıdığı
	// (AKTİF) 5 dk kova sayısı, uniqExact(time_bucket). YALNIZ batch kalıp
	// listesi doluyken okunur (aynı iç alt sorgudan, ek tarama yok); aksi
	// hâlde 0 ve hiçbir dal ona bakmaz.
	BaseBuckets uint64
}

// opLatPair — (servis, operasyon) çifti: trace_op_latency olayının kimliği
// (FingerprintAnomaly(kind, operasyon, servis)).
type opLatPair struct{ Service, Operation string }

// opLatBatchGate — classifyOpLatency'nin batch kapısı (v0.10.1046). Sıfır
// değer = kural kapalı (bugünkü davranış).
type opLatBatchGate struct {
	isBatch    func(service string) bool // nil = kapı yok
	curBuckets uint64                    // cari penceredeki 5 dk kova sayısı (recorder: 1)
	exempt     map[opLatPair]bool        // olayı AKTİF çiftler — kapı uygulanmaz
}

// opLatBatchPlan — bir tikin batch kapısı planı: SQL koşulu + argümanları,
// muaf çiftler (SQL bind sırasıyla) ve aynı planın Go kemeri. Sıfır değer =
// kapı yok → opLatencyQuery bugünkü SQL'i birebir üretir.
type opLatBatchPlan struct {
	cond   string
	args   []any
	exempt []opLatPair
	gate   opLatBatchGate
}

// planOpLatBatch — SAF: DetectOpLatencyAnomalies'in I/O'suz yarısı.
//
//	kalıp listesi boş (kural kapalı / ayar doğrulanmamış) → kapı yok
//	aktif-olay okuması HATA verdi                         → kapı yok (süzgeç
//	                                                        okunamıyorsa süzme)
//	aksi                                                  → kapı + aktif çiftler muaf
//
// active zaten tavana kesilmiş gelir (batchLatCapKeys: 200 çift;
// opLatCapExemptBytes: 64 KiB metin): tavan ötesindeki aktif çiftler muaf
// DEĞİL (bind listesi sınırlı olmak zorunda).
func planOpLatBatch(sens chstore.AnomalySensitivityConfig, curBuckets uint64, active []chstore.ActiveAnomalyKey, readErr error) opLatBatchPlan {
	cond, args := sens.BatchServiceSQL("service_name")
	if cond == "" || readErr != nil {
		return opLatBatchPlan{}
	}
	exempt := make([]opLatPair, 0, len(active))
	set := make(map[opLatPair]bool, len(active))
	for _, k := range active {
		p := opLatPair{Service: k.Service, Operation: k.Pattern}
		if set[p] {
			continue
		}
		set[p] = true
		exempt = append(exempt, p)
	}
	return opLatBatchPlan{
		cond: cond, args: args, exempt: exempt,
		gate: opLatBatchGate{isBatch: sens.IsBatchService, curBuckets: curBuckets, exempt: set},
	}
}

// classifyOpLatency — kesin eşikler + sıralama (saf, tablo-testli;
// classifyTraceOps'un ikizi). SQL'in kaba HAVING'i LIMIT'i anlamlı
// tutar; nihai hüküm burada.
//
// v0.10.1046 — gate (sıfır değer = kural kapalı): batch serviste gecikme
// sıçraması, çift YÜK ALTINDAYKEN (opLatencyUnderLoad) ve olayı zaten AKTİF
// değilken olay DEĞİL — operatör: "Batch servislerde yük altındaki gecikme
// artışı da anomali sayılmasın". SAF SUSTURMA: kalan olaylar eski kümenin alt
// kümesi, alanları birebir; batch olmayan çiftler ilk 50'de yalnız yer
// kazanır. SQL ikizi opLatencyQuery'nin HAVING'inde; burası kemer.
func classifyOpLatency(rows []opLatencyBucket, gate opLatBatchGate) []OpLatencyAnomaly {
	out := []OpLatencyAnomaly{}
	for _, r := range rows {
		if r.CurCalls < opLatencyMinCalls || r.BaseCalls < opLatencyMinCalls {
			continue
		}
		if r.BaseP99Ms <= 0 || r.CurP99Ms < opLatencyMinP99Ms {
			continue
		}
		ratio := r.CurP99Ms / r.BaseP99Ms
		if ratio < opLatencyMinRatio {
			continue
		}
		if gate.isBatch != nil && gate.isBatch(r.Service) &&
			!gate.exempt[opLatPair{Service: r.Service, Operation: r.Operation}] &&
			opLatencyUnderLoad(r, gate.curBuckets) {
			continue
		}
		out = append(out, OpLatencyAnomaly{
			Service:   r.Service,
			Operation: r.Operation,
			CurP99Ms:  r.CurP99Ms,
			BaseP99Ms: r.BaseP99Ms,
			Ratio:     ratio,
			CurCalls:  r.CurCalls,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Ratio != out[j].Ratio {
			return out[i].Ratio > out[j].Ratio
		}
		return out[i].CurCalls > out[j].CurCalls
	})
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

// opLatencyUnderLoad — v0.10.1046, batch kapısının trace_op_latency kolu:
// çiftin cari KOVA BAŞINA çağrısı, taban penceresinin AKTİF KOVA BAŞINA
// çağrısının ≥ 2 katı mı (batchLoadSurgeCounts)?
//
//	cur_calls × base_buckets ≥ 2 × base_calls × cur_buckets
//
// Neden aktif kova: taban 24 sa'lik TOPLAM; 24 saate bölmek (ilk sürüm) boş
// kovaları da sayardı ve günün ≤ %50'sinde koşan bir işin HER koşusu
// "sıçrama" olurdu — dünkü koşuyla aynı yükte 10× yavaşlayan bir koşu da
// susardı. Aktif kova başına çağrı işin OLAĞAN ÇALIŞMA hacmidir; 7/24
// serviste (288 aktif kova) eski tanımla aynı karar.
//
// BEDEL — bu bir ORTALAMA (DECISIONS v0.10.1046 "Bedeller"):
//   - damla profili: kovaların çoğunda birkaç çağrı + gerçek bir koşu (ör. 276
//     kovada 1 çağrı + 12 kova × 1.000) → ortalama ~43, koşu yükünün çok
//     altında; AYNI yükteki bir koşu da "sıçrama" sayılır ve bu çiftte
//     operasyon düzeyi gecikme olayı hiç açılmaz (servis düzeyi p99 görür);
//   - rampa profili (50, 50, 50, 2.000) → olağan büyük kova sıçrama sayılır;
//     küçük kovalar olayı yine açabilir, açıldıktan sonra çift muaftır;
//   - 7/24 batch servis: 24 sa ortalamasının ≥ 2 katı günlük tepe, tepe
//     saatlerinde YENİ olayı susturur.
//
// Ortalama bilinçli seçildi, max / p90 DEĞİL: onlarla kayan taban gerçek bir
// sıçramanın ilk kovasını birkaç dakika içinde kendine katar ve susturma
// sıçramanın ortasında kalkıp olay açılırdı.
func opLatencyUnderLoad(r opLatencyBucket, curBuckets uint64) bool {
	return batchLoadSurgeCounts(r.CurCalls, curBuckets, r.BaseCalls, r.BaseBuckets)
}

// opLatencyQuery — tespitin MV sorgusu + argümanları (v0.10.1046'te saf
// kurucuya çıkarıldı ki batch kolu SQL düzeyinde pinlenebilsin; traceOpQuery
// deseni).
//
// plan sıfır değer (kural kapalı / aktif küme okunamadı) → metin ve
// argümanlar v0.10.1046 ÖNCESİYLE BİREBİR aynı (TestOpLatencyQueryLegacyIdentity).
//
// plan dolu → üç ek, hepsi aynı geçişte (ek tarama yok):
//   - iç alt sorguya uniqExact(time_bucket) AS buckets (uniqExact ŞART:
//     birleşmemiş parçalarda aynı kova birden çok satırdır), dışa
//     maxIf(buckets, is_cur = 0) AS base_buckets;
//   - HAVING'e TEK eleme: batch servis VE çift muaf değil VE
//     cur_calls * base_buckets >= 2 * base_calls * cur_buckets (tamsayı —
//     batchLoadSurgeCounts ile birebir);
//   - muaf çiftler (aktif olaylar) skaler yer tutucularla tuple NOT IN.
//
// Koşul HAVING'de, Go'da DEĞİL: LIMIT 200 p99 oranına göre sıralıyor ve yük
// altındaki batch çiftleri tam da en büyük oranı taşıyanlar — Go'da elenseler
// LIMIT'i doldurup gerçek sıçramaları dışarıda bırakırlardı (v0.9.327'nin
// dersi: LIMIT yalnız hayatta kalabilecek satırlarda ısırmalı).
func opLatencyQuery(curStart, baseStart, alignedNow time.Time, plan opLatBatchPlan) (string, []any) {
	sel := `
		SELECT service_name, name,
		       maxIf(p99, is_cur = 1)   AS cur_p99,
		       maxIf(p99, is_cur = 0)   AS base_p99,
		       sumIf(calls, is_cur = 1) AS cur_calls,
		       sumIf(calls, is_cur = 0) AS base_calls`
	inner := `
		         countMerge(span_count_state) AS calls`
	having := `
		HAVING cur_calls >= ? AND base_calls >= ?
		   AND base_p99 > 0 AND cur_p99 >= ? * base_p99 AND cur_p99 >= ?`
	args := []any{curStart, baseStart, alignedNow,
		opLatencyMinCalls, opLatencyMinCalls, opLatencyMinRatio, opLatencyMinP99Ms}
	if plan.cond != "" {
		sel += `,
		       maxIf(buckets, is_cur = 0) AS base_buckets`
		inner += `,
		         uniqExact(time_bucket) AS buckets`
		exempt := ""
		if len(plan.exempt) > 0 {
			tuples := make([]string, len(plan.exempt))
			for i := range tuples {
				tuples[i] = "(?, ?)"
			}
			exempt = ` AND (service_name, name) NOT IN (` + strings.Join(tuples, ", ") + `)`
		}
		having += `
		   AND NOT (` + plan.cond + exempt + `
		            AND cur_calls * base_buckets >= ? * base_calls * ?)`
		args = append(args, plan.args...)
		for _, p := range plan.exempt {
			args = append(args, p.Service, p.Operation)
		}
		args = append(args, batchLoadSurgeFactor, plan.gate.curBuckets)
	}
	return sel + `
		FROM (
		  SELECT service_name, name,
		         time_bucket >= ? AS is_cur,
		         arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p99,` + inner + `
		  FROM operation_summary_5m
		  WHERE time_bucket >= ? AND time_bucket < ?
		  GROUP BY service_name, name, is_cur
		)
		GROUP BY service_name, name` + having + `
		ORDER BY cur_p99 / base_p99 DESC
		LIMIT 200
		SETTINGS max_execution_time = 25`, args
}

// DetectOpLatencyAnomalies — cari pencere p99 vs 24h (ya da 12×pencere)
// kuyruk baseline p99, operation_summary_5m üzerinden tek pivot geçişi.
// Örnek trace yalnız kalifiye ≤50 çift için dar, PK-prefix'li raw
// sorgudan (DetectTraceOpAnomalies ile aynı bedel modeli; pencereler 5m
// bucket'a hizalı, tespit ≤~5dk gecikir — kabul edilmiş desen).
func DetectOpLatencyAnomalies(ctx context.Context, store *chstore.Store, window time.Duration) ([]OpLatencyAnomaly, error) {
	conn := store.TelemetryReadConn()
	now := time.Now()

	alignedNow := now.Truncate(traceOpBucketLen)
	curBuckets := int(window / traceOpBucketLen)
	if curBuckets < 1 {
		curBuckets = 1
	}
	curWindow := time.Duration(curBuckets) * traceOpBucketLen
	curStart := alignedNow.Add(-curWindow)
	baseLookback := 24 * time.Hour
	if 12*curWindow > baseLookback {
		baseLookback = 12 * curWindow
	}
	baseStart := curStart.Add(-baseLookback)

	// v0.10.1046 — batch kapısı. Kalıplar trace_op'un ve metrik dedektörünün
	// okuduğu AYNI atomic ayardan (ForDetectors: doğrulanmamış ayarda liste
	// boş = kapı yok). Liste doluysa sorgudan ÖNCE TEK sınırlı okuma: bu türün
	// batch servislerde son opLatActiveAge (15 dk) içinde yazılmış olayları —
	// o çiftler kapıdan muaf (yalnız açılışı keser). Okuma hatası → kapı bu tik
	// HİÇ yok, geçişte bir kez log; sayı ya da bayt tavanı aşımı → en tazeler
	// muaf, ötesi muaf değil, geçişte bir kez log.
	sens := store.AnomalySensitivityForDetectors()
	var active []chstore.ActiveAnomalyKey
	var readErr error
	if svcCond, svcArgs := sens.BatchServiceSQL("service"); svcCond != "" {
		keys, err := store.ListActiveAnomalyKeys(ctx, opLatencyKind, "", opLatActiveAge, svcCond, svcArgs, batchLatActiveCap+1)
		readErr = err
		opLatActiveReadLatch.report(err != nil,
			"[anomaly-recorder] op latency: aktif olay okunamadı (%v) — batch gecikme kapısı bu okuma düzelene dek UYGULANMIYOR", err)
		var overflow bool
		var droppedBytes int
		active, overflow = batchLatCapKeys(keys)
		active, droppedBytes = opLatCapExemptBytes(active)
		opLatActiveCapLatch.report(err == nil && overflow,
			"[anomaly-recorder] op latency: batch servislerde aktif gecikme olayı tavanı (%d) aştı — tavan ötesindekiler kapıdan muaf DEĞİL", batchLatActiveCap)
		opLatActiveBytesLatch.report(err == nil && droppedBytes > 0,
			"[anomaly-recorder] op latency: muaf listesi metin tavanını (%d bayt) aştı — %d çift muaf, %d çift muaf DEĞİL", opLatExemptMaxBytes, len(active), droppedBytes)
	}
	plan := planOpLatBatch(sens, uint64(curBuckets), active, readErr)
	q, args := opLatencyQuery(curStart, baseStart, alignedNow, plan)
	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	buckets := []opLatencyBucket{}
	for rows.Next() {
		var b opLatencyBucket
		dest := []any{&b.Service, &b.Operation, &b.CurP99Ms, &b.BaseP99Ms, &b.CurCalls, &b.BaseCalls}
		if plan.cond != "" {
			dest = append(dest, &b.BaseBuckets)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := classifyOpLatency(buckets, plan.gate)
	if len(out) == 0 {
		return out, nil
	}

	// Örnek: cari penceredeki EN YAVAŞ span'in trace'i (hata hattının
	// argMax(trace_id, time)'ından farkla duration-argMax — gecikme
	// olayının temsilcisi en yavaş istek). Aynı üst-küme IN deseni.
	svcs := make([]string, 0, len(out))
	ops := make([]string, 0, len(out))
	for _, a := range out {
		svcs = append(svcs, a.Service)
		ops = append(ops, a.Operation)
	}
	srows, err := conn.Query(ctx, `
		SELECT service_name, name,
		       argMax(trace_id, duration)       AS sample,
		       toUnixTimestamp64Nano(max(time)) AS last_ns
		FROM spans
		WHERE time >= ? AND time < ?
		  AND service_name IN ?
		  AND name IN ?
		GROUP BY service_name, name
		LIMIT 2500
		SETTINGS max_execution_time = 10`,
		curStart, alignedNow, svcs, ops)
	if err != nil {
		// Örnek yoksa anomaliler yine döner — sample best-effort.
		return out, nil
	}
	defer srows.Close()
	type sk struct{ s, o string }
	samples := map[sk]struct {
		trace string
		last  int64
	}{}
	for srows.Next() {
		var s, o, tr string
		var last int64
		if err := srows.Scan(&s, &o, &tr, &last); err != nil {
			continue
		}
		samples[sk{s, o}] = struct {
			trace string
			last  int64
		}{tr, last}
	}
	for i := range out {
		if sm, ok := samples[sk{out[i].Service, out[i].Operation}]; ok {
			out[i].SampleTraceID = sm.trace
			out[i].LastSeenNs = sm.last
		}
	}
	return out, nil
}
