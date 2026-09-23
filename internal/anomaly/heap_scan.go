package anomaly

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// heap_scan.go — v0.10.891 (paritesi #4 dilim 2, spec Onay 2026-09-23):
// JVM heap bandı → Problem fazı, GÖLGE kipi. Dedektör tikinin sonunda
// (kümeleme sonrası, davranış motoru öncesi), soft-fail, RED hattına gecikme
// bindirmez. Bu sürümde Problem YAZILMAZ: kip "shadow" hüküm + sayaç + log +
// verdict önbelleği; kip "on" da shadow gibi davranır (tek log satırı) —
// canlı kip v0.10.892'de, bir hafta gölge gözleminden sonra.
//
// Veri: ARTIMLI HALKA. (servis,pod) başına 288 kovalık bellek halkası; tik
// başına yalnız son iki TAM 5-dk kova okunur (filo-geneli tek çift sorgu,
// GroupBy servis+pod). Tavana çarpılırsa (CH 50k satır / VM 1000 seri)
// servis-parçalı okuma (jvm.memory.limit basan servis listesi, tik başına ≤60
// servis × 2 sorgu, döngüsel; filo sonucu korunur).
// Lider olur olmaz TEK SEFERLİK parçalı 24 s doldurma (tik başına en çok
// heapBackfillPerTick servis) → 75 dk kör pencere yok. Kip off'a çekilince
// halka boşaltılır (bellek geri).
//
// Hüküm servis başına: her pod'un bandı (ComputeHeapBandWith, kartla AYNI
// okuyucu + AYNI politika), en kötü pod (z), canlı pod = son heapSilent
// kova içinde yazmış olan. Karar tablosu heapDecide (SAF, tablo testli):
// en kötü canlı pod critical → open; açık satır varken canlı pod yok →
// resolve ("pod silent"); canlı maxZ ≤ resolveZ → resolve; aksi none (touch).

const (
	heapRingLen         = 288 // 24 s / 5 dk
	heapReadBuckets     = 2   // artımlı okuma: son iki tam kova (geç export'u yakalar)
	heapBackfillPerTick = 60  // doldurma: tik başına servis (2 sorgu/servis)
	heapServiceSince    = 30 * time.Minute
	heapServiceLimit    = 1000
	heapVerdictTTL      = 15 * time.Minute
	heapVerdictPrefix   = "heap-verdict:"
	// heapCapPerTick — tavan durumunda servis-parçalı okuma: tik başına en çok
	// bu kadar servis (2 sorgu/servis), döngüsel; okuma penceresi heapCapBuckets
	// kova ki rotasyon aralığında kaçan kova kalmasın (put üstüne yazar).
	heapCapPerTick = 60
	heapCapBuckets = 6 // 30 dk
	// heapServiceRefreshTicks — servis listesi (label-values) kaç tikte bir tazelenir.
	heapServiceRefreshTicks = 5
	// heapTickBudget — doldurma/parçalı döngülerin yumuşak zaman bütçesi:
	// aşılınca kalan servisler sonraki tike devreder (davranış motoru ve tik
	// bloke olmasın; lider TTL 6 dk).
	heapTickBudget = 45 * time.Second
)

// heapRing — artan zaman sıralı kova serisi, ≤ heapRingLen.
type heapRing struct {
	times []int64
	vals  []float64
}

// put — aynı kova varsa üstüne yazar (geç gelen export), yoksa sıralı ekler;
// tavanı aşınca en eski düşer.
func (r *heapRing) put(t int64, v float64) {
	i := sort.Search(len(r.times), func(i int) bool { return r.times[i] >= t })
	if i < len(r.times) && r.times[i] == t {
		r.vals[i] = v
		return
	}
	r.times = append(r.times, 0)
	r.vals = append(r.vals, 0)
	copy(r.times[i+1:], r.times[i:])
	copy(r.vals[i+1:], r.vals[i:])
	r.times[i], r.vals[i] = t, v
	if n := len(r.times) - heapRingLen; n > 0 {
		r.times = r.times[n:]
		r.vals = r.vals[n:]
	}
}

func (r *heapRing) last() int64 {
	if len(r.times) == 0 {
		return 0
	}
	return r.times[len(r.times)-1]
}

// heapState — lider sürecin belleği; kip off'ta nil.
type heapState struct {
	rings           map[string]map[string]*heapRing // svc → pod → halka
	backfillPending []string                        // doldurulacak servisler; nil + backfilled=true → bitti
	backfilled      bool
	wouldOpen       map[string]bool // svc → gölgede "açık" durumu (geçiş sayımı)
	onWarned        bool
	lastSource      string
	// services — jvm.memory.limit basan servis listesi (heapServiceRefreshTicks'te
	// bir tazelenir); capPending — tavan durumunda parçalı okuma rotasyonu.
	services    []string
	servicesAge int
	capPending  []string
}

// HeapVerdict — kart rozeti için önbelleğe yazılan hüküm (Redis, 15 dk).
type HeapVerdict struct {
	Service   string  `json:"service"`
	Status    string  `json:"status"` // ok | deviating | critical | no_baseline
	Pod       string  `json:"pod,omitempty"`
	Z         float64 `json:"z"`
	Current   float64 `json:"current"`
	Median    float64 `json:"median"`
	Mode      string  `json:"mode"`   // shadow | on
	Source    string  `json:"source"` // vm | ch
	At        int64   `json:"at"`     // unix s
	WouldOpen bool    `json:"wouldOpen"`
	WouldP1   bool    `json:"wouldP1"`
	LivePods  int     `json:"livePods"`
}

// heapObs — süreç-içi sayaçlar (behaviorObs deseni); /api/stats üstünden çıkar.
var heapObs struct {
	ticks, wouldOpenTotal, wouldResolveTotal                          atomic.Int64
	ringPods, ringServices, wouldOpenNow, wouldP1Now, backfillPending atomic.Int64
	capped, ringCapped, backfilled                                    atomic.Int64
	lastMs, lastUnix                                                  atomic.Int64
	mode, source, lastErr                                             atomic.Value
}

// HeapObservability — anlık görüntü; lider dışı pod'da sıfır ve bu DOĞRU cevap.
func HeapObservability() chstore.HeapDetectorStats {
	s := chstore.HeapDetectorStats{
		Ticks: heapObs.ticks.Load(), WouldOpenTotal: heapObs.wouldOpenTotal.Load(), WouldResolveTotal: heapObs.wouldResolveTotal.Load(),
		RingPods: heapObs.ringPods.Load(), RingServices: heapObs.ringServices.Load(), WouldOpenNow: heapObs.wouldOpenNow.Load(), WouldP1Now: heapObs.wouldP1Now.Load(),
		BackfillPending: heapObs.backfillPending.Load(), Capped: heapObs.capped.Load() == 1, RingCapped: heapObs.ringCapped.Load() == 1, Backfilled: heapObs.backfilled.Load() == 1,
		LastDurationMs: heapObs.lastMs.Load(), LastUnix: heapObs.lastUnix.Load(),
	}
	if v, ok := heapObs.mode.Load().(string); ok {
		s.Mode = v
	}
	if v, ok := heapObs.source.Load().(string); ok {
		s.Source = v
	}
	if v, ok := heapObs.lastErr.Load().(string); ok {
		s.LastError = v
	}
	return s
}

// heapDecision — servis başına karar (SAF).
type heapDecision struct {
	Action   string // open | resolve | none
	Pod      string // en kötü canlı pod
	Band     HeapBand
	LivePods int
	Reason   string
}

// heapDecide — SAF karar tablosu. bands: pod → bant; live: pod → canlı mı.
func heapDecide(bands map[string]HeapBand, live map[string]bool, hasOpen bool) heapDecision {
	liveBands := map[string]HeapBand{}
	for pod, b := range bands {
		if live[pod] {
			liveBands[pod] = b
		}
	}
	d := heapDecision{Action: "none", LivePods: len(liveBands)}
	worst := heapWorstByStatus(liveBands)
	if worst != "" {
		d.Pod, d.Band = worst, liveBands[worst]
		if d.Band.Status == HeapBandCritical {
			d.Action = "open"
			return d
		}
	}
	if !hasOpen {
		return d
	}
	if len(liveBands) == 0 {
		d.Action, d.Reason = "resolve", "no live JVM pod (silent beyond heapSilentBuckets)"
		return d
	}
	maxZ := 0.0
	for _, b := range liveBands {
		if b.Status != HeapBandNoBaseline && b.Z > maxZ {
			maxZ = b.Z
		}
	}
	if maxZ <= resolveZ {
		d.Action, d.Reason = "resolve", "recovered"
	}
	return d
}

// heapWorstByStatus — SAF: önce durum sırası (critical > deviating > ok), sonra z,
// sonra ad. Kartın HeapWorstPod'u yalnız z'ye bakar; kararda tek kovalık yüksek
// anlık z'li bir pod'un critical (3 kova) pod'u maskelememesi için sıra şart.
func heapWorstByStatus(bands map[string]HeapBand) string {
	rank := map[string]int{HeapBandCritical: 3, HeapBandDeviating: 2, HeapBandOK: 1}
	worst, bestR, bestZ := "", 0, 0.0
	for pod, b := range bands {
		r := rank[b.Status]
		if r == 0 {
			continue
		}
		if worst == "" || r > bestR || (r == bestR && (b.Z > bestZ || (b.Z == bestZ && pod < worst))) {
			worst, bestR, bestZ = pod, r, b.Z
		}
	}
	return worst
}

// heapWouldP1 — SAF: gölgede açılsaydı öncelik P1 olur muydu (computePriority
// aynı sentetik satırla; StartedAt şimdi → yaş kolu yok, yalnız oran kolu).
func heapWouldP1(service string, b HeapBand, now time.Time) bool {
	p := chstore.Problem{RuleID: "anomaly:" + service + ":" + HeapBandMetric, RuleName: "Anomaly · JVM heap after GC",
		Severity: "critical", Service: service, Metric: HeapBandMetric, Value: b.Current, Threshold: b.Median, Comparator: ">",
		Status: "open", StartedAt: now.UnixNano()}
	return chstore.EnrichProblemsWithPriority([]chstore.Problem{p})[0].Priority == "P1"
}

// scanHeap — tik fazı. Soft-fail: hiçbir hata RED hattına sızmaz.
func (d *Detector) scanHeap(ctx context.Context, now time.Time, snap *chstore.OpenProblems, sens chstore.AnomalySensitivityConfig) {
	rt := chstore.NormalizeAnomalyRuntime(sens.Runtime)
	heapObs.mode.Store(rt.HeapMode)
	if rt.HeapMode == chstore.HeapModeOff {
		if d.heap != nil {
			// Halka belleği geri + sayaçlar sıfır (bayat "AÇILIRDI" kalmasın);
			// verdict önbelleği bilinen servisler için silinir (kart rozeti).
			for svc := range d.heap.rings {
				d.heapDropVerdict(ctx, svc)
			}
			d.heap = nil
			for _, a := range []*atomic.Int64{&heapObs.ringPods, &heapObs.ringServices, &heapObs.wouldOpenNow, &heapObs.wouldP1Now, &heapObs.backfillPending, &heapObs.capped, &heapObs.ringCapped, &heapObs.backfilled} {
				a.Store(0)
			}
			log.Printf("[anomaly/heap] kip off — halka boşaltıldı, sayaçlar sıfır")
		}
		return
	}
	start := time.Now()
	srcOr := d.heapWire.source.Load()
	src := srcOr.pick(rt.HeapSource)
	if src.HeapBackend == nil {
		heapObs.lastErr.Store("heap kaynağı bağlı değil (SetHeapSource)")
		return
	}
	heapObs.source.Store(src.name)
	if d.heap == nil || d.heap.lastSource != src.name {
		if d.heap != nil {
			log.Printf("[anomaly/heap] kaynak değişti %s → %s — halka yeniden dolduruluyor", d.heap.lastSource, src.name)
		}
		d.heap = &heapState{rings: map[string]map[string]*heapRing{}, wouldOpen: map[string]bool{}, lastSource: src.name}
	}
	st := d.heap
	if rt.HeapMode == chstore.HeapModeOn && !st.onWarned {
		st.onWarned = true
		log.Printf("[anomaly/heap] kip 'on' bu sürümde gölge gibi davranır — canlı kip v0.10.892")
	}
	pol := HeapPolicyFrom(sens)
	lastComplete := HeapLastComplete(now)

	// 1) Doldurma (tek seferlik, parçalı).
	if !st.backfilled {
		d.heapBackfill(ctx, src, st, now, rt)
		heapObs.backfillPending.Store(int64(len(st.backfillPending)))
		if !st.backfilled {
			d.heapFinish(start, st)
			return
		}
	}
	// 2) Artımlı okuma: son iki tam kova, filo-geneli. Pencere üst sınırı
	// lastComplete+300: VM query_range değeri t anında BİTEN kovayı verir
	// (bucketStart = t−adım), yani L kovası ancak t = L+300 değerlendirmesiyle
	// gelir; CH'de L+300 kovasına düşen tek örnek HeapBuckets'ta atılır.
	from := time.Unix(lastComplete-(heapReadBuckets-1)*HeapBucketSec, 0)
	to := time.Unix(lastComplete+HeapBucketSec, 0)
	fr, err := ReadHeapFleet(ctx, src, from, to)
	if err != nil {
		heapObs.lastErr.Store("artımlı okuma: " + err.Error())
		d.heapFinish(start, st)
		return
	}
	pct := fr.Pct // filo sonucu HER ZAMAN kullanılır; tavanda üstüne parçalı okuma eklenir
	if fr.Capped {
		heapObs.capped.Store(1)
		d.heapReadCapped(ctx, src, st, lastComplete, pct, start)
	} else {
		heapObs.capped.Store(0)
		st.capPending = nil
	}
	ringCapped := d.heapIngest(st, pct, lastComplete, rt.HeapMaxPods, now)
	if ringCapped {
		heapObs.ringCapped.Store(1)
	} else {
		heapObs.ringCapped.Store(0)
	}
	// 3) Servis başına hüküm (gölge): sayaç + geçiş logu + verdict önbelleği.
	openNow, p1Now := 0, 0
	silentBefore := lastComplete - int64(rt.HeapSilentBuckets)*HeapBucketSec
	for svc, pods := range st.rings {
		bands, live := map[string]HeapBand{}, map[string]bool{}
		for pod, r := range pods {
			bands[pod] = ComputeHeapBandWith(r.vals, pol)
			live[pod] = r.last() >= silentBefore
		}
		open := snap.ByKey("anomaly:"+svc+":"+HeapBandMetric, svc)
		hasOpen := open != nil && open.ID != ""
		dec := heapDecide(bands, live, hasOpen || st.wouldOpen[svc])
		v := HeapVerdict{Service: svc, Status: HeapBandNoBaseline, Pod: dec.Pod, Mode: rt.HeapMode, Source: src.name, At: now.Unix(), LivePods: dec.LivePods}
		if dec.Pod != "" {
			v.Status, v.Z, v.Current, v.Median = dec.Band.Status, dec.Band.Z, dec.Band.Current, dec.Band.Median
		}
		switch dec.Action {
		case "open":
			v.WouldOpen = true
			v.WouldP1 = heapWouldP1(svc, dec.Band, now)
			openNow++
			if v.WouldP1 {
				p1Now++
			}
			if !st.wouldOpen[svc] {
				st.wouldOpen[svc] = true
				heapObs.wouldOpenTotal.Add(1)
				log.Printf("[anomaly/heap] WOULD-OPEN %s pod=%s cur=%.1f%% med=%.1f%% mad=%.1f z=%.1f p1=%v (kip %s, kaynak %s)",
					svc, dec.Pod, dec.Band.Current, dec.Band.Median, dec.Band.MAD, dec.Band.Z, v.WouldP1, rt.HeapMode, src.name)
			}
		case "resolve":
			if st.wouldOpen[svc] {
				delete(st.wouldOpen, svc)
				heapObs.wouldResolveTotal.Add(1)
				log.Printf("[anomaly/heap] WOULD-RESOLVE %s (%s)", svc, dec.Reason)
			}
		}
		d.heapCacheVerdict(ctx, v)
	}
	heapObs.wouldOpenNow.Store(int64(openNow))
	heapObs.wouldP1Now.Store(int64(p1Now))
	heapObs.lastErr.Store("")
	d.heapFinish(start, st)
}

func (d *Detector) heapFinish(start time.Time, st *heapState) {
	pods := 0
	for _, m := range st.rings {
		pods += len(m)
	}
	heapObs.ringPods.Store(int64(pods))
	heapObs.ringServices.Store(int64(len(st.rings)))
	if st.backfilled {
		heapObs.backfilled.Store(1)
	} else {
		heapObs.backfilled.Store(0)
	}
	heapObs.ticks.Add(1)
	heapObs.lastMs.Store(time.Since(start).Milliseconds())
	heapObs.lastUnix.Store(time.Now().Unix())
}

// heapServices — jvm.memory.limit basan servisler (son 30 dk); >1000 kör (log).
func heapServices(ctx context.Context, src HeapSource) ([]string, error) {
	lv, ok := src.(interface {
		MetricLabelValues(ctx context.Context, metric, key string, since time.Duration, q string, limit int) ([]string, error)
	})
	if !ok {
		return nil, nil
	}
	svcs, err := lv.MetricLabelValues(ctx, HeapMetricLimit, "resource.service.name", heapServiceSince, "", heapServiceLimit)
	if err != nil {
		return nil, err
	}
	if len(svcs) >= heapServiceLimit {
		log.Printf("[anomaly/heap] servis listesi %d tavanında — fazlası bu tikte kör", heapServiceLimit)
	}
	sort.Strings(svcs)
	return svcs, nil
}

// heapBackfill — lider başlangıcı: 24 s tarihçe, tik başına ≤ heapBackfillPerTick servis.
func (d *Detector) heapBackfill(ctx context.Context, src heapPicked, st *heapState, now time.Time, rt chstore.AnomalyRuntimeConfig) {
	if st.backfillPending == nil {
		svcs := d.heapServiceList(ctx, src, st)
		if svcs == nil {
			return // liste okunamadı (lastErr dolu) — sonraki tik
		}
		if len(svcs) == 0 {
			st.backfilled = true // JVM yok — doldurulacak bir şey yok, faz artımlı çalışır
			log.Printf("[anomaly/heap] doldurma: jvm.memory.limit basan servis yok (kaynak %s)", src.name)
			return
		}
		st.backfillPending = svcs
		log.Printf("[anomaly/heap] doldurma başladı: %d servis, tik başına %d (kaynak %s)", len(svcs), heapBackfillPerTick, src.name)
	}
	lastComplete := HeapLastComplete(now)
	n := 0
	start := time.Now()
	for len(st.backfillPending) > 0 && n < heapBackfillPerTick && time.Since(start) < heapTickBudget {
		svc := st.backfillPending[0]
		st.backfillPending = st.backfillPending[1:]
		n++
		rd, err := ReadHeapPodsNoProbe(ctx, src, svc, now.Add(-HeapHistory), now)
		if err != nil {
			heapObs.lastErr.Store("doldurma " + svc + ": " + err.Error())
			continue
		}
		if rd.Capped {
			heapObs.capped.Store(1)
		}
		d.heapIngest(st, map[string]map[string]map[int64]float64{svc: rd.Pct}, lastComplete, rt.HeapMaxPods, now)
	}
	if len(st.backfillPending) == 0 {
		st.backfilled = true
		pods := 0
		for _, m := range st.rings {
			pods += len(m)
		}
		log.Printf("[anomaly/heap] doldurma tamam: %d servis, %d pod", len(st.rings), pods)
	}
}

// heapServiceList — servis listesi önbelleği (heapServiceRefreshTicks).
func (d *Detector) heapServiceList(ctx context.Context, src heapPicked, st *heapState) []string {
	if st.services != nil && st.servicesAge < heapServiceRefreshTicks {
		st.servicesAge++
		return st.services
	}
	svcs, err := heapServices(ctx, src)
	if err != nil {
		heapObs.lastErr.Store("servis listesi: " + err.Error())
		return st.services
	}
	st.services, st.servicesAge = svcs, 0
	return svcs
}

// heapReadCapped — tavan durumunda parçalı artımlı okuma: filo sonucu KORUNUR,
// üstüne tik başına ≤ heapCapPerTick servis (2 sorgu/servis, probe yok),
// döngüsel; pencere heapCapBuckets kova ki rotasyonda kaçan kova kalmasın.
// Yumuşak bütçe aşılınca kalan sonraki tike devreder.
func (d *Detector) heapReadCapped(ctx context.Context, src heapPicked, st *heapState, lastComplete int64, into map[string]map[string]map[int64]float64, start time.Time) {
	if len(st.capPending) == 0 {
		st.capPending = append([]string(nil), d.heapServiceList(ctx, src, st)...)
	}
	from := time.Unix(lastComplete-(heapCapBuckets-1)*HeapBucketSec, 0)
	to := time.Unix(lastComplete+HeapBucketSec, 0)
	n := 0
	for len(st.capPending) > 0 && n < heapCapPerTick && time.Since(start) < heapTickBudget {
		svc := st.capPending[0]
		st.capPending = st.capPending[1:]
		n++
		rd, err := ReadHeapPodsNoProbe(ctx, src, svc, from, to)
		if err != nil {
			heapObs.lastErr.Store("parçalı okuma " + svc + ": " + err.Error())
			continue
		}
		if len(rd.Pct) > 0 {
			into[svc] = rd.Pct
		}
	}
}

// heapDropVerdict — kip off'ta kart rozeti kalmasın.
func (d *Detector) heapDropVerdict(ctx context.Context, service string) {
	if box := d.heapWire.cache.Load(); box != nil && box.c != nil {
		_ = box.c.Del(ctx, HeapVerdictKey(service))
	}
}

// heapIngest — pct'yi halkalara yazar; 24 s'den eski pod'lar düşer; pod tavanı
// (yeni pod kabul edilmez → ringCapped). Dönüş: tavana çarpıldı mı.
func (d *Detector) heapIngest(st *heapState, pct map[string]map[string]map[int64]float64, lastComplete int64, maxPods int, now time.Time) bool {
	total := 0
	for _, m := range st.rings {
		total += len(m)
	}
	capped := false
	for svc, pods := range pct {
		if st.rings[svc] == nil {
			st.rings[svc] = map[string]*heapRing{}
		}
		for pod, samples := range pods {
			r := st.rings[svc][pod]
			if r == nil {
				if maxPods > 0 && total >= maxPods {
					capped = true
					continue
				}
				r = &heapRing{}
				st.rings[svc][pod] = r
				total++
			}
			times, vals := HeapBuckets(samples, lastComplete)
			for i, t := range times {
				r.put(t, vals[i])
			}
		}
	}
	// 24 s'dir yazmayan pod düşer; boş servis düşer.
	cutoff := now.Add(-HeapHistory).Unix()
	for svc, pods := range st.rings {
		for pod, r := range pods {
			if r.last() < cutoff {
				delete(pods, pod)
			}
		}
		if len(pods) == 0 {
			delete(st.rings, svc)
		}
	}
	return capped
}

// heapCacheVerdict — kart rozeti için (Noop cache'te sessizce yok).
func (d *Detector) heapCacheVerdict(ctx context.Context, v HeapVerdict) {
	box := d.heapWire.cache.Load()
	if box == nil || box.c == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	if err := box.c.Set(ctx, HeapVerdictKey(v.Service), b, heapVerdictTTL); err != nil {
		heapObs.lastErr.Store("verdict cache: " + err.Error())
	}
}

// HeapVerdictKey — api heap-baseline rotası aynı anahtardan okur.
func HeapVerdictKey(service string) string { return heapVerdictPrefix + strings.TrimSpace(service) }
