package anomaly

import (
	"context"
	"sort"
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
//
// v0.10.1085 — sürdürme: tek kova değil, ardışık N (vars. 2) tamamlanmış
// kovanın her biri ihlal etmeli; aynı tek MV geçişi kovaları ayrı döndürür.

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
// Cur* = sürdürme penceresinin EN YENİ tamamlanmış kovası (olayın raporladığı
// değerler); daha eskileri Earlier'da.
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
	// Earlier — v0.10.1085: sürdürme penceresinin en yeni kovadan ÖNCEKİ
	// kovaları, yeniden eskiye (Earlier[0] = bir önceki tamamlanmış kova).
	// dwell = 1'de boş. Verisiz kova sıfır değer taşır (çağrı 0 → hacim
	// tabanının altı → ihlal DEĞİL).
	Earlier []opLatencySlot
	// Active — v0.10.1085: çiftin trace_op_latency olayı zaten AKTİF (son
	// opLatActiveAge içinde yazılmış). Sürdürme yalnız AÇILIŞI yönetir: aktif
	// çift, en yeni kova tek başına ihlal ettiği sürece tazelenir. Tarayıcı
	// aktif-olay okumasından doldurur (SQL ikizi: HAVING'deki tuple IN).
	Active bool
}

// opLatencySlot — sürdürme penceresinin tek bir önceki kovası (v0.10.1085).
type opLatencySlot struct {
	P99Ms float64
	Calls uint64
}

// opLatPair — (servis, operasyon) çifti: trace_op_latency olayının kimliği
// (FingerprintAnomaly(kind, operasyon, servis)). v0.10.1091 — pivot chstore'a
// taşındığı için onun çift tipinin takma adı.
type opLatPair = chstore.OpPair

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
//
// v0.10.1085 — dwell (sürdürme): çift ancak ardışık `dwell` tamamlanmış
// kovanın HER BİRİNDE ihlal ederse olaydır — her kova ≥ opLatencyMinCalls
// çağrı, p99 ≥ opLatencyMinP99Ms ve p99 ≥ opLatencyMinRatio × taban.
// Operatör: tek yavaş istek (~30 çağrılık kovada tek 4 sn'lik publish) o
// kovanın p99'u olup "168×" açıyor, sonraki kovada 1×'e iniyordu. dwell ≤ 1 =
// v0.10.1085 öncesi tek-kova kararı birebir. Olay alanları (oran, p99, çağrı)
// EN YENİ kovadan — şekil değişmedi. Batch kapısı yalnız pencerenin HER
// kovası yük altındaysa susturur (metrik dedektörünün "her dwell kovası"
// emsali): yükün açıklamadığı tek bir ihlal kovası olayı açar.
//
// Sürdürme yalnız AÇILIŞA uygulanır (histerezis): olayı zaten aktif çift
// (r.Active) en yeni kova tek başına ihlal ettikçe tazelenir; en yeni kova
// temizse yazım durur ve olay olağan aktif yaşla düşer.
func classifyOpLatency(rows []opLatencyBucket, dwell int, gate opLatBatchGate) []OpLatencyAnomaly {
	if dwell < 1 {
		dwell = 1
	}
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
		// v0.10.1085 — sürdürme yalnız açılışa: aktif olay tek dip kovada
		// yazımı bırakıp "anomaly cleared" + yeni bildirimle dalgalanmasın.
		if !r.Active && !opLatencySustained(r, dwell) {
			continue
		}
		if gate.isBatch != nil && gate.isBatch(r.Service) &&
			!gate.exempt[opLatPair{Service: r.Service, Operation: r.Operation}] &&
			opLatencyUnderLoad(r, dwell, gate.curBuckets) {
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
//
// v0.10.1085 — sürdürme penceresinin HER kovası yük altında olmalı (en yeni +
// dwell−1 önceki); biri değilse yük gecikmeyi açıklamıyor → false. dwell = 1
// → yalnız en yeni kova (v0.10.1046 birebir). Eksik önceki kova → false
// (bilinmiyor = susturma yok).
func opLatencyUnderLoad(r opLatencyBucket, dwell int, curBuckets uint64) bool {
	if !batchLoadSurgeCounts(r.CurCalls, curBuckets, r.BaseCalls, r.BaseBuckets) {
		return false
	}
	if len(r.Earlier) < dwell-1 {
		return false
	}
	for i := 0; i < dwell-1; i++ {
		if !batchLoadSurgeCounts(r.Earlier[i].Calls, curBuckets, r.BaseCalls, r.BaseBuckets) {
			return false
		}
	}
	return true
}

// opLatencySustained — v0.10.1085: en yeni kovadan önceki dwell−1 kovanın HER
// BİRİ de ihlal ediyor mu? En yeni kova ve taban çağıranda (classifyOpLatency)
// zaten sınandı; burada aynı üç eşik, aynı biçimde (oran bölmeyle — en yeni
// kovanın sınamasıyla eşikte birebir). Eksik kova → false.
func opLatencySustained(r opLatencyBucket, dwell int) bool {
	if len(r.Earlier) < dwell-1 {
		return false
	}
	for i := 0; i < dwell-1; i++ {
		s := r.Earlier[i]
		if s.Calls < opLatencyMinCalls || s.P99Ms < opLatencyMinP99Ms || s.P99Ms/r.BaseP99Ms < opLatencyMinRatio {
			return false
		}
	}
	return true
}

// opLatencyQuery — tespitin MV sorgusu + argümanları (v0.10.1046'te saf
// kurucuya çıkarıldı ki batch kolu SQL düzeyinde pinlenebilsin; traceOpQuery
// deseni). v0.10.1091 — gövde chstore.OpP99PivotQuery'ye taşındı (yaygın
// yavaşlama kuralı aynı pivotu başka tabanlarla kullanıyor); burası yalnız
// trace_op_latency'nin tabanlarını ve batch planını bağlar. Metin ve
// argümanlar taşımadan önceyle BAYT BAYT aynı (golden testler).
//
// slotStarts — sürdürme penceresinin kova başları, yeniden eskiye
// (opLatencyWindows): [0] en yeni tamamlanmış kova, len = dwell ≥ 1.
//
// active — olayı zaten AKTİF çiftler (dwell ≥ 2'de anlamlı): önceki kovaların
// koşullarından muaf — sürdürme yalnız açılışı yönetir. Liste tavanlı (200
// çift + 64 KiB; batchLatCapKeys / opLatCapExemptBytes).
//
// plan dolu → batch yük kapısı (v0.10.1046) aynı geçişte, HAVING'de: Go'da
// elenseler LIMIT'i doldurup gerçek sıçramaları dışarıda bırakırlardı.
func opLatencyQuery(slotStarts []time.Time, baseStart, alignedNow time.Time, plan opLatBatchPlan, active []opLatPair) (string, []any) {
	return chstore.OpP99PivotQuery(chstore.OpP99PivotSpec{
		SlotStarts: slotStarts, BaseStart: baseStart, AlignedNow: alignedNow,
		Floors: chstore.OpP99Floors{MinCalls: opLatencyMinCalls, Ratio: opLatencyMinRatio, MinP99Ms: opLatencyMinP99Ms},
		Batch: chstore.OpP99BatchGate{
			Cond: plan.cond, Args: plan.args, Exempt: plan.exempt,
			SurgeFactor: batchLoadSurgeFactor, CurBuckets: plan.gate.curBuckets,
		},
		Active: active,
	})
}

// opLatSustainExempt — SAF (v0.10.1085): sürdürmeden muaf AKTİF çiftler,
// tekilleştirilmiş, okuma sırasıyla (en taze önce; tavan önceden uygulanmış).
// dwell ≤ 1 (sürdürme yok) ya da okuma hatası → boş.
func opLatSustainExempt(dwell int, active []chstore.ActiveAnomalyKey, readErr error) []opLatPair {
	if dwell <= 1 || readErr != nil {
		return nil
	}
	seen := make(map[opLatPair]bool, len(active))
	out := make([]opLatPair, 0, len(active))
	for _, k := range active {
		p := opLatPair{Service: k.Service, Operation: k.Pattern}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// opLatencyScanDest — v0.10.1085: satırın Scan hedefleri, opLatencyQuery'nin
// kolon sırasıyla (6 temel kolon, önceki kovalar p99_<i> / calls_<i>, batch
// kolunda base_buckets). Tek yerde ki sorgu ile tarayıcı ayrışmasın
// (clickhouse local testi aynı yardımcıyla ayrıştırır).
func opLatencyScanDest(b *opLatencyBucket, dwell int, batch bool) []any {
	dest := []any{&b.Service, &b.Operation, &b.CurP99Ms, &b.BaseP99Ms, &b.CurCalls, &b.BaseCalls}
	if dwell < 1 {
		dwell = 1
	}
	b.Earlier = make([]opLatencySlot, dwell-1)
	for i := range b.Earlier {
		dest = append(dest, &b.Earlier[i].P99Ms, &b.Earlier[i].Calls)
	}
	if batch {
		dest = append(dest, &b.BaseBuckets)
	}
	return dest
}

// opLatencyWindows — SAF (v0.10.1085): tespitin zaman sınırları. Kova =
// recorder penceresi (varsayılan 5 dk = MV kovası; daha geniş pencere kova
// başına curBuckets MV kovası). slotStarts[i] = en yeni (i+1). kovanın başı —
// hepsi TAMAMLANMIŞ (alignedNow'dan önce). Taban, sürdürme penceresinin
// TAMAMINDAN önce biter: sıçrayan kovalar kendi tabanlarını şişirmez. dwell =
// 1 → v0.10.1085 öncesi pencerelerle birebir (curStart = slotStarts[0]).
func opLatencyWindows(now time.Time, window time.Duration, dwell int) (slotStarts []time.Time, baseStart, alignedNow time.Time, curBuckets int) {
	if dwell < 1 {
		dwell = 1
	}
	alignedNow = now.Truncate(traceOpBucketLen)
	curBuckets = int(window / traceOpBucketLen)
	if curBuckets < 1 {
		curBuckets = 1
	}
	curWindow := time.Duration(curBuckets) * traceOpBucketLen
	slotStarts = make([]time.Time, dwell)
	for i := range slotStarts {
		slotStarts[i] = alignedNow.Add(-time.Duration(i+1) * curWindow)
	}
	baseLookback := 24 * time.Hour
	if 12*curWindow > baseLookback {
		baseLookback = 12 * curWindow
	}
	baseStart = slotStarts[dwell-1].Add(-baseLookback)
	return slotStarts, baseStart, alignedNow, curBuckets
}

// DetectOpLatencyAnomalies — cari pencere p99 vs 24h (ya da 12×pencere)
// kuyruk baseline p99, operation_summary_5m üzerinden tek pivot geçişi.
// Örnek trace yalnız kalifiye ≤50 çift için dar, PK-prefix'li raw
// sorgudan (DetectTraceOpAnomalies ile aynı bedel modeli; pencereler 5m
// bucket'a hizalı, tespit ≤~5dk gecikir — kabul edilmiş desen).
//
// v0.10.1056 — anahtar (anomaly_sensitivity.opLatency; operatör: "Trace op
// latency false pozitif geliyor, gerek yok gelmelerine bence.").
// Kapı HER G/Ç'den ÖNCE ve burada, çağıranda değil: dedektörün her çağıranı
// (bugün yalnız recorder) anahtara uyar, kapalıyken boş liste döner — hata
// DEĞİL. Kapalıyken ne MV sorgusu ne v0.10.1046 aktif-olay okuması ne örnek
// sorgusu. Ayar bu süreçte henüz doğrulanmadıysa KAPALI okunur
// (AnomalySensitivityForDetectors): birkaç tiklik boşluk açık olayı düşürmez
// (10 dk aktif yaş).
//
// v0.10.1085 — varsayılan AÇIK (nil = açık), sürdürme kuralıyla: çift ancak
// ardışık opLatencyDwellBuckets (vars. 2) tamamlanmış kovanın HER BİRİNDE
// ihlal ederse olay (operatör: "Önerini yapalım"). Pencere aynı atomic
// okumadan; tek MV sorgusu tüm kovaları birlikte döndürür.
func DetectOpLatencyAnomalies(ctx context.Context, store *chstore.Store, window time.Duration) ([]OpLatencyAnomaly, error) {
	sens := store.AnomalySensitivityForDetectors()
	if !sens.OpLatencyOn() {
		return []OpLatencyAnomaly{}, nil
	}
	conn := store.TelemetryReadConn()
	dwell := sens.OpLatencyDwell()
	slotStarts, baseStart, alignedNow, curBuckets := opLatencyWindows(time.Now(), window, dwell)
	// curStart — en yeni kovanın başı: örnek trace bu kovadan (olayın
	// raporladığı p99 da onun).
	curStart := slotStarts[0]

	// v0.10.1046 — batch kapısı. Kalıplar trace_op'un ve metrik dedektörünün
	// okuduğu AYNI atomic ayardan (ForDetectors: doğrulanmamış ayarda liste
	// boş = kapı yok). Liste doluysa sorgudan ÖNCE TEK sınırlı okuma: bu türün
	// batch servislerde son opLatActiveAge (15 dk) içinde yazılmış olayları —
	// o çiftler kapıdan muaf (yalnız açılışı keser). Okuma hatası → kapı bu tik
	// HİÇ yok, geçişte bir kez log; sayı ya da bayt tavanı aşımı → en tazeler
	// muaf, ötesi muaf değil, geçişte bir kez log. (sens yukarıda, anahtarla
	// aynı okuma — v0.10.1056.)
	//
	// v0.10.1085 — AYNI okuma sürdürme histerezisini de besler: dwell ≥ 2'de
	// servis daraltması YOK (her servisin aktif olayı açılış sürdürmesinden
	// muaf), yeni sorgu yok. Okuma hatası → o tik ne batch muafiyeti ne
	// sürdürme muafiyeti (aktif çift de iki kova ister — sessiz yön).
	var active []chstore.ActiveAnomalyKey
	var readErr error
	svcCond, svcArgs := sens.BatchServiceSQL("service")
	if dwell > 1 {
		svcCond, svcArgs = "", nil
	}
	if dwell > 1 || svcCond != "" {
		keys, err := store.ListActiveAnomalyKeys(ctx, opLatencyKind, "", opLatActiveAge, svcCond, svcArgs, batchLatActiveCap+1)
		readErr = err
		opLatActiveReadLatch.report(err != nil,
			"[anomaly-recorder] op latency: aktif olay okunamadı (%v) — batch gecikme kapısı ve sürdürme muafiyeti bu okuma düzelene dek UYGULANMIYOR", err)
		var overflow bool
		var droppedBytes int
		active, overflow = batchLatCapKeys(keys)
		active, droppedBytes = opLatCapExemptBytes(active)
		opLatActiveCapLatch.report(err == nil && overflow,
			"[anomaly-recorder] op latency: aktif gecikme olayı tavanı (%d) aştı — tavan ötesindekiler kapıdan ve sürdürmeden muaf DEĞİL", batchLatActiveCap)
		opLatActiveBytesLatch.report(err == nil && droppedBytes > 0,
			"[anomaly-recorder] op latency: muaf listesi metin tavanını (%d bayt) aştı — %d çift muaf, %d çift muaf DEĞİL", opLatExemptMaxBytes, len(active), droppedBytes)
	}
	plan := planOpLatBatch(sens, uint64(curBuckets), active, readErr)
	sustainExempt := opLatSustainExempt(dwell, active, readErr)
	q, args := opLatencyQuery(slotStarts, baseStart, alignedNow, plan, sustainExempt)
	activeSet := make(map[opLatPair]bool, len(sustainExempt))
	for _, p := range sustainExempt {
		activeSet[p] = true
	}
	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	buckets := []opLatencyBucket{}
	for rows.Next() {
		var b opLatencyBucket
		if err := rows.Scan(opLatencyScanDest(&b, dwell, plan.cond != "")...); err != nil {
			return nil, err
		}
		b.Active = activeSet[opLatPair{Service: b.Service, Operation: b.Operation}]
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := classifyOpLatency(buckets, dwell, plan.gate)
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
