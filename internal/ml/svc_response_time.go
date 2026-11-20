package ml

// svc_response_time.go — v0.10.2385 (etki olayı programı, dilim I4; operatör
// kararı 2026-11-19 "tek etki olayı Problem açsın", emsal Dynatrace Davis
// "response time degradation"): SERVİS düzeyi yanıt süresi. Motorda servis
// düzeyi gecikme ölçüsü yoktu: op_latency endpoint başına yavaş istek payına,
// svc_breadth ≥ 3 operasyonun birlikte yavaşlamasına bakar; tek ağır endpoint
// ya da hepsi biraz yavaşlayan servis iki kuralın arasından geçiyordu.
//
// ÖLÇÜ: servisin MEDYAN (p50) ve EN YAVAŞ %10 (p90) yanıt süresi, ms. Kaynak
// TraceRED'in RED aile pinleri (anomaly.REDFamilyPins; yalnız SUNUCU istek
// histogramları, örneklenmemiş). Aileler TOPLANMAZ: dakika / kova başına
// servisin ilk ailesi (pin sırası) kullanılır. Üst sıradaki ailenin sorgusu
// düştüyse alt aileler o dakika GÜVENİLMEZ (servisin üst ailede verisi olup
// olmadığı bilinmez; HTTP tabanıyla gRPC süresi kıyaslanmasın) → o ailelerin
// servisleri o dakika ölçülmedi.
//
// TETİK: 1 dakikalık örnek + etki kuralı (impact_rule.go impactDecide). İki
// ölçü AYRI yargılanır, biri yeterse olay:
//
//	medyan       ≥ max(taban × 1,5, taban + 100 ms)   (impactResponseMedian)
//	en yavaş %10 ≥ max(taban × 2,   taban + 1000 ms)  (impactResponseSlow)
//
// ikisi de: son 5 tam dakikanın en az 3'ünde ihlal, en son ölçülen dakika da
// ihlal, ihlal dakikasında en az 10 istek. Olayın gözleneni / tabanı ateşleyen
// ölçününkidir (ikisi de ateşlerse oranı büyük olan): Problem önceliği
// (oran ≥ 2 → P1) anlamlı kalsın. Kanıt hangi ölçü(ler)in ateşlediğini söyler.
//
// SORGULAR (vm_budget.go başlığındaki 422 dersi: ham bucket'ın ARALIK sorgusu
// yasak) — hepsi ANLIK, bucket'lar VM'de servis × le'ye toplanır, yüzdelikler
// burada hesaplanır (tek tarama hem p50 hem p90 hem de dakikanın istek
// sayısını verir: +Inf bucket'ı; ayrı count sorgusu YOK):
//
//	1 dk örnek : sum by (svc, le) (increase(<aile>_bucket[60s]))   dakika sonunda
//	5 dk taban : sum by (svc, le) (increase(<aile>_bucket[300s]))  kova sonunda
//
// Tamamlanmış dakika değişmez → dakika başına aile başına BİR kez sorulur ve
// saklanır (rtMin). Kararlı durumda dakikada aile başına 1 sorgu (yoklama ya
// da tik; erken tik 0), soğuk başlangıçta en çok 5 × aile. Her sorgunun
// taradığı ham örnek, tikin zaten attığı 300 sn pencereli p95 sorgusunun
// (by svc, route, le) beşte biri; yanıt servis × bucket satırı, 1 nokta.
// Taban sorgusu tik başına aile başına 1 (p95 sorgusuyla aynı tarama).
//
// YALITIM: iki sorgu ailesi de kendi backoff anahtarında (impact-rt:<aile>,
// impact-rt5:<aile>); hata tik hatası DEĞİL, yalnız bu dedektör o tik sessiz
// ("ölçülemedi"). Sorgular tik ölçerine yazılmaz: atlama trace_red ailesini
// "kör" işaretlemez (işlem düzeyi dedektörlerin kapanışı etkilenmez). Bu
// dedektörün AÇIK koşusu ise ölçülemeyen ailedeyse kaçırma saymaz (runBlind;
// veri-yok tutması) — VM beklemesi "düzeldi" diye kapanış yazmasın.
//
// TABAN: 5 dakikalık `*|svc_p50` / `*|svc_p90` serilerinin mevsimsel medyanı
// (takvim sınıfı günler × saat ± 1; TraceRED'in taban okumasından, ek CH
// sorgusu yok). Ölçünün taban örneği MinBaselineSamples'ın altındaysa o ölçü
// yargılanmaz; hiçbir servisin tabanı yoksa 1 dk'lık sorgular da atılmaz.
// Kovada 10 istekten azı varsa taban örneği yazılmaz (yüzdelik gürültü).
//
// SINIR: yüzdelik bucket sınırlarıyla ölçülür; en büyük sonlu bucket'ın üstü
// görülmez (taban oraya yakınsa eşik ölçülemez → o serviste kör).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	detSvcResponseTime = "svc_response_time"
	svcMeasureP50      = "svc_p50"
	svcMeasureP90      = "svc_p90"
	// rtBackoff / rtBaseBackoff — 1 dk'lık ve 5 dk'lık bucket sorgularının
	// KENDİ backoff aileleri (birinin ağır hatası diğerini ve işlem düzeyi
	// `trace:<aile>` sorgularını bekletmez).
	rtBackoff     = "impact-rt:"
	rtBaseBackoff = "impact-rt5:"
)

// rtMeasure — yargılanan ölçü: etki kuralı, ml_series ölçü adı, yüzdelik.
type rtMeasure struct {
	kind  impactKind
	part  string  // ml_series ölçü adı (part "*|<part>")
	key   string  // kanıt / cümle anahtarı (chstore.MLRespMedian | MLRespSlow)
	label string  // "medyan" | "en yavaş %10"
	agg   string  // olay kaydı toplaması
	q     float64 // yüzdelik
}

// rtMeasures — sıra sabit: 0 = medyan, 1 = en yavaş %10.
var rtMeasures = [2]rtMeasure{
	{impactResponseMedian, svcMeasureP50, chstore.MLRespMedian, "medyan", "p50", 0.5},
	{impactResponseSlow, svcMeasureP90, chstore.MLRespSlow, "en yavaş %10", "p90", 0.9},
}

// rtMeasureOfPart — SAF: ml_series ölçü adı → ölçü sırası (bilinmeyen → medyan).
func rtMeasureOfPart(part string) int {
	if strings.HasSuffix(part, svcMeasureP90) {
		return 1
	}
	return 0
}

// rtPoint — servisin bir penceredeki (1 dk ya da 5 dk) özeti: Q[i] =
// rtMeasures[i] yüzdeliği (ms), Load = penceredeki istek sayısı.
type rtPoint struct {
	Q    [2]float64
	Load float64
}

// ── histogram → yüzdelik (SAF) ───────────────────────────────────────────

// histBucket — kümülatif bucket: üst sınır (le) ve o sınıra kadarki istek.
type histBucket struct{ le, n float64 }

// histQuantile — SAF: Prometheus histogram_quantile'ın aynısı. bs le sırasında,
// son bucket +Inf olmalı. Yüzdelik +Inf bucket'ına düşerse en büyük SONLU
// sınır döner (ölçüm tavanı). ok=false: bucket eksik / istek yok.
func histQuantile(q float64, bs []histBucket) (float64, bool) {
	n := len(bs)
	if n < 2 || !math.IsInf(bs[n-1].le, 1) {
		return 0, false
	}
	total := bs[n-1].n
	if !(total > 0) || math.IsInf(total, 0) {
		return 0, false
	}
	rank := q * total
	b := sort.Search(n-1, func(i int) bool { return bs[i].n >= rank })
	if b == n-1 {
		return bs[n-2].le, true
	}
	lo, prev := 0.0, 0.0
	if b > 0 {
		lo, prev = bs[b-1].le, bs[b-1].n
	}
	hi, cnt := bs[b].le, bs[b].n-prev
	if b == 0 && hi <= 0 {
		return hi, true
	}
	if cnt <= 0 {
		return hi, true
	}
	return lo + (hi-lo)*(rank-prev)/cnt, true
}

// rtFromBuckets — SAF: `sum by (svc, le) (increase(..._bucket[w]))` satırları
// → servis başına özet. scale = aile birimi → ms. Bozuk le / sonlu olmayan
// değer atılır; kümülatif sayım tekdüze yapılır (sayaç sıfırlanması artığı).
// Yüzdeliği hesaplanamayan (isteksiz, +Inf'siz) servis YOKTUR (ölçülmedi).
func rtFromBuckets(rows []chstore.SpanMetricSeries, svcLabel string, scale float64) map[string]rtPoint {
	by := map[string][]histBucket{}
	for _, r := range rows {
		lb := groupKeyLabels(r.GroupKey)
		svc := lb[svcLabel]
		le, err := strconv.ParseFloat(lb["le"], 64)
		v, ok := instantValue(r)
		if svc == "" || err != nil || math.IsNaN(le) || !ok || v < 0 {
			continue
		}
		by[svc] = append(by[svc], histBucket{le: le, n: v})
	}
	out := make(map[string]rtPoint, len(by))
	for svc, bs := range by {
		sort.Slice(bs, func(i, j int) bool { return bs[i].le < bs[j].le })
		for i := 1; i < len(bs); i++ {
			bs[i].n = math.Max(bs[i].n, bs[i-1].n)
		}
		var p rtPoint
		ok := true
		for i, m := range rtMeasures {
			v, qok := histQuantile(m.q, bs)
			p.Q[i], ok = v*scale, ok && qok
		}
		if ok {
			p.Load = bs[len(bs)-1].n
			out[svc] = p
		}
	}
	return out
}

// rtBucketQuery — SAF: ailenin servis × le'ye toplanmış bucket artışı
// (route / pod / durum kırılımı yok; anlık sorgu, winSec pencere).
func rtBucketQuery(pin anomaly.REDFamilyPin, svc string, winSec int) string {
	return fmt.Sprintf("sum by (%s, le) (increase(%s_bucket[%ds]))", svc, pin.Names, winSec)
}

// pinMsScale — SAF: aile birimi → ms çarpanı.
func pinMsScale(pin anomaly.REDFamilyPin) float64 {
	if pin.Unit == "s" {
		return 1000
	}
	return 1
}

// rtPick — bir servisin o pencere için seçilen noktası ve ailesi (pin sırası).
type rtPick struct {
	rtPoint
	fam int
}

// rtPickFirst — SAF: "ilk aile kazanır". order = keşfedilmiş ailelerin pin
// sırası; got[aile] = o ailenin bu pencere için alınmış servis haritası (yoksa
// alınamadı). Alınamayan ilk ailede DURULUR: alt aileler güvenilmez. Servis,
// isteği olan (Load > 0) ilk aileden okunur.
func rtPickFirst(order []int, got map[int]map[string]rtPoint) map[string]rtPick {
	out := map[string]rtPick{}
	for _, fi := range order {
		byS, ok := got[fi]
		if !ok {
			break
		}
		for svc, p := range byS {
			if _, taken := out[svc]; !taken && p.Load > 0 {
				out[svc] = rtPick{rtPoint: p, fam: fi}
			}
		}
	}
	return out
}

// ── 5 dk taban serisi ────────────────────────────────────────────────────

// rtBaseSeries — tikin taban adımı: aile başına 1 anlık bucket sorgusu (kova
// penceresi) → `*|svc_p50` / `*|svc_p90` (series haritasına, kova = bucket;
// çağıran AYRI harita verir, route serilerinin tavanına girmez: ≤ 2 × servis). Kovada rtBaseMinLoad'dan az istek
// varsa örnek yazılmaz. Hata / atlama: o ailede durulur, hata döner (çağıran
// yalıtır); o kovanın taban örneği eksik kalır (0 yazılmaz).
func rtBaseSeries(ctx context.Context, deps Deps, fams []impFam, at time.Time, bucket int64, bucketSec int, series map[string]map[int64]float64, now time.Time) error {
	deps.meter = nil // yalıtım: atlama trace_red ailesini kör işaretlemez
	got := map[int]map[string]rtPoint{}
	order := make([]int, 0, len(fams))
	var ferr error
	for _, f := range fams {
		order = append(order, f.idx)
		if ferr != nil {
			continue
		}
		rows, skip, err := vmInstant(ctx, deps, rtBaseBackoff+f.pin.ID, rtBucketQuery(f.pin, f.fl.svc, bucketSec), at, now)
		switch {
		case err != nil:
			ferr = fmt.Errorf("vm %s/svc_rt_5m: %w", f.pin.ID, err)
		case skip:
			ferr = fmt.Errorf("vm %s/svc_rt_5m: aile beklemede", f.pin.ID)
		default:
			got[f.idx] = rtFromBuckets(rows, f.fl.svc, pinMsScale(f.pin))
		}
	}
	minLoad := impactRules[impactResponseMedian].MinLoad
	for svc, p := range rtPickFirst(order, got) {
		if p.Load < minLoad {
			continue
		}
		for i, m := range rtMeasures {
			k := chstore.MLSeriesKey(svc, tracePart(svcRoute, m.part))
			if series[k] == nil {
				series[k] = map[int64]float64{}
			}
			series[k][bucket] = p.Q[i]
		}
	}
	return ferr
}

// ── 1 dk örnekler ────────────────────────────────────────────────────────

// rtWindow — karar penceresinin dakikaları: dakika başı → servis → nokta.
// blindFrom = bu geçişte en az bir dakikası alınamayan ilk ailenin pin sırası
// (−1 = hepsi alındı).
type rtWindow struct {
	end       int64
	min       map[int64]map[string]rtPick
	blindFrom int
}

// rtMinutes — pencerenin [end − 5 dk, end) dakikaları. Saklı dakika yeniden
// sorulmaz (tamamlanmış dakika değişmez; yoklama, erken tik ve sonraki tik
// aynı satırları kullanır). Eksik (dakika, aile) çifti için 1 anlık sorgu;
// geçiş başına aile başına en çok BİR başarısız sorgu (düşen aile o geçişte
// yeniden sorulmaz). Hata ilk hatadır; pencere yine döner (alınabilen kadar).
func (d *TraceRED) rtMinutes(ctx context.Context, deps Deps, fams []impFam, end, now time.Time) (rtWindow, error) {
	deps.meter = nil // yalıtım: atlama trace_red ailesini kör işaretlemez
	first := end.Unix() - impactWindow*impactStepSec
	w := rtWindow{end: end.Unix(), min: map[int64]map[string]rtPick{}, blindFrom: -1}
	order := make([]int, 0, len(fams))
	for _, f := range fams {
		order = append(order, f.idx)
	}
	d.mu.Lock()
	if d.rtMin == nil {
		d.rtMin = map[int64]map[int]map[string]rtPoint{}
	}
	for m := range d.rtMin { // pencereden çıkan dakikalar
		if m < first {
			delete(d.rtMin, m)
		}
	}
	have := map[int64]map[int]map[string]rtPoint{}
	for m := first; m < end.Unix(); m += impactStepSec {
		have[m] = map[int]map[string]rtPoint{}
		for fi, byS := range d.rtMin[m] {
			have[m][fi] = byS
		}
	}
	d.mu.Unlock()
	failed := map[int]bool{}
	var ferr error
	for m := first; m < end.Unix(); m += impactStepSec {
		for _, f := range fams {
			if _, ok := have[m][f.idx]; ok {
				continue
			}
			if !failed[f.idx] {
				rows, skip, err := vmInstant(ctx, deps, rtBackoff+f.pin.ID, rtBucketQuery(f.pin, f.fl.svc, impactStepSec), time.Unix(m+impactStepSec, 0), now)
				if err == nil && !skip {
					have[m][f.idx] = rtFromBuckets(rows, f.fl.svc, pinMsScale(f.pin))
					continue
				}
				failed[f.idx] = true
				if err != nil && ferr == nil {
					ferr = fmt.Errorf("vm %s/svc_rt_1m: %w", f.pin.ID, err)
				}
			}
			if w.blindFrom < 0 || f.idx < w.blindFrom {
				w.blindFrom = f.idx
			}
			break // alt aileler bu dakika güvenilmez
		}
		w.min[m] = rtPickFirst(order, have[m])
	}
	d.mu.Lock()
	for m, byF := range have {
		if len(byF) > 0 {
			d.rtMin[m] = byF
		}
	}
	d.mu.Unlock()
	return w, ferr
}

// rtSamples — SAF: servisin pencere örnekleri, ölçü başına (en eski → en
// yeni). Ölçülmüş = o dakikada servisin noktası var (istek > 0). fam = en
// yeni ölçülen dakikanın ailesi (−1 = hiç ölçülmedi).
func rtSamples(w rtWindow, svc string) (s [2][]impactSample, fam int) {
	fam = -1
	for i := range s {
		s[i] = make([]impactSample, impactWindow)
	}
	for i := 0; i < impactWindow; i++ {
		p, ok := w.min[w.end-int64(impactWindow-i)*impactStepSec][svc]
		if !ok {
			continue
		}
		fam = p.fam
		for k := range s {
			s[k][i] = impactSample{Value: p.Q[k], Load: p.Load, OK: true}
		}
	}
	return s, fam
}

// ── karar ────────────────────────────────────────────────────────────────

// rtBase — servisin mevsimsel taban özeti, ölçü başına (yoklama CH okumadan
// aynı kararı verir).
type rtBase struct {
	N           [2]int
	Median, MAD [2]float64
}

// rtBaseOf — SAF: taban örnekleri (p50, p90) → özet.
func rtBaseOf(p50, p90 []float64) rtBase {
	var b rtBase
	for i, xs := range [2][]float64{p50, p90} {
		b.N[i] = len(xs)
		b.Median[i], b.MAD[i] = medianMAD(xs)
	}
	return b
}

// ready — SAF: en az bir ölçünün tabanı yeterli.
func (b rtBase) ready(minSamples int) bool { return b.N[0] >= minSamples || b.N[1] >= minSamples }

// rtVerdict — servis yanıt süresinin etki kararı.
type rtVerdict struct {
	M     [2]impactVerdict // ölçü başına karar
	Have  [2]bool          // ölçünün tabanı yeterli (yargılandı)
	Fired [2]bool          // ölçü bu tik ateşliyor
	Fire  bool             // en az bir ölçü ateşliyor
	Emit  bool             // olay bu tik yazılır (ateşleme ya da açık koşunun uzatması)
	Lead  int              // olayın ölçüsü (rtMeasures sırası); −1 = yok
	Base  rtBase
	// Reason — Emit=false iken neden (tanı; arayüz metni değil).
	Reason string
}

// rtRatio — SAF: değer / taban (taban 0 → +Inf).
func rtRatio(val, base float64) float64 {
	if base <= 0 {
		return math.Inf(1)
	}
	return val / base
}

// rtDecide — SAF: samples[i] = rtMeasures[i] ölçüsünün son impactWindow
// dakikası; open = olayın açık koşusu var. Tabanı yetersiz ölçü yargılanmaz;
// ikisi de yetersizse sessiz. Olayın ölçüsü (Lead): ateşleyenler önce, sonra
// oranı (en yeni dakika / taban) büyük olan; hiçbiri ateşlemiyorsa (uzatma)
// penceresinde ihlal kalan ölçülerden ihlali çok, sonra TEPE oranı büyük olan
// (en yeni dakika tabana dönmüş olabilir: ölçü uzatma boyunca oynamasın).
func rtDecide(samples [2][]impactSample, b rtBase, minSamples int, open bool) rtVerdict {
	v := rtVerdict{Lead: -1, Base: b}
	for i := range v.M {
		v.M[i].Onset = -1
	}
	if !b.ready(minSamples) {
		v.Reason = "eksik veri: taban örneği az"
		return v
	}
	better := func(i, j int) bool { // i, j'den önce mi
		if v.Fired[i] != v.Fired[j] {
			return v.Fired[i]
		}
		if v.Fire {
			return rtRatio(v.M[i].Observed, b.Median[i]) > rtRatio(v.M[j].Observed, b.Median[j])
		}
		if v.M[i].Violations != v.M[j].Violations {
			return v.M[i].Violations > v.M[j].Violations
		}
		return rtRatio(v.M[i].Peak, b.Median[i]) > rtRatio(v.M[j].Peak, b.Median[j])
	}
	var cand []int
	for i, m := range rtMeasures {
		if b.N[i] < minSamples {
			v.M[i].Reason = "eksik veri: taban örneği az"
			continue
		}
		v.Have[i] = true
		v.M[i] = impactDecide(m.kind, samples[i], b.Median[i])
		v.Fired[i] = v.M[i].Fire
		v.Fire = v.Fire || v.Fired[i]
		if impactEmit(v.M[i], open) {
			cand = append(cand, i)
		}
	}
	if len(cand) == 0 {
		for i := range rtMeasures {
			if v.Have[i] {
				v.Reason = rtMeasures[i].label + ": " + v.M[i].Reason
				break
			}
		}
		return v
	}
	v.Lead = cand[0]
	for _, i := range cand[1:] {
		if better(i, v.Lead) {
			v.Lead = i
		}
	}
	v.Emit = true
	return v
}

// rtEvent — SAF: kararın olayı. Gözlenen / taban = olayın ölçüsünün en yeni
// ölçülen dakikası / mevsimsel medyanı (ms). StartedAt = ateşleyen ölçülerin
// ilk ihlal dakikasının başı; açık koşunun yazılmış başlangıcı daha erkense
// o kalır. LastSeen = pencere bitişi. WindowSec taban kovası (kart ve
// oynatma 5 dk ml_series'e bakar); örnek adımı kanıtta. Kanıt kararı açıklar:
// kural, olayın ölçüsü, ateşleyen ölçüler, eşik, ihlal, dakika dakika değer
// (null = ölçülmedi) ve yük; ölçü başına ayrıntı kendi anahtarında.
func rtEvent(svc string, samples [2][]impactSample, v rtVerdict, end time.Time, openStart int64, cfg chstore.MLConfig, class string) chstore.MLEvent {
	n := impactWindow
	first := end.Unix() - int64(n)*impactStepSec
	lead := rtMeasures[v.Lead]
	lv := v.M[v.Lead]
	onset := -1
	fired := []string{}
	vals := [2][]any{make([]any, n), make([]any, n)}
	loads := make([]float64, n)
	ev := map[string]any{"source": "vm", "route": "", "dayClass": class, "baselineDays": cfg.BaselineDays, "unit": "ms",
		"measure": lead.part, "mad": v.Base.MAD[v.Lead], "rule": "impact", "fired": v.Fire,
		"threshold": lv.Threshold, "violations": lv.Violations, "window": impactWindow, "minViolations": impactViolations,
		"minLoad": impactRules[lead.kind].MinLoad, "sampleSec": impactStepSec, "minuteFrom": first}
	for i, m := range rtMeasures {
		for k, s := range samples[i] {
			if k < n && s.OK {
				vals[i][k], loads[k] = s.Value, s.Load
			}
		}
		if !v.Have[i] {
			continue
		}
		if v.Fired[i] {
			fired = append(fired, m.key)
		}
		// başlangıç: ateşleyen ölçülerin (uzatmada: ihlali kalanların) ilk ihlali
		if mv := v.M[i]; mv.Violations > 0 && (v.Fired[i] || !v.Fire) && (onset < 0 || mv.Onset < onset) {
			onset = mv.Onset
		}
		ev[m.key] = map[string]any{"baseline": v.Base.Median[i], "threshold": v.M[i].Threshold, "violations": v.M[i].Violations,
			"observed": v.M[i].Observed, "fired": v.Fired[i]}
	}
	z := (lv.Observed - v.Base.Median[v.Lead]) / effMAD(v.Base.Median[v.Lead], v.Base.MAD[v.Lead])
	ev["z"], ev["firedMeasures"], ev["p50s"], ev["p90s"], ev["loads"] = z, fired, vals[0], vals[1], loads
	raw, _ := json.Marshal(ev)
	return chstore.MLEvent{
		ID: chstore.MLEventID(detSvcResponseTime, svc, ""), Detector: detSvcResponseTime, Signal: chstore.MLSignalTrace, Stage: chstore.MLStageShadow,
		Service: svc, StartedAt: impactRunStart(end, n, onset, openStart), LastSeen: end.UnixNano(), WindowSec: uint32(cfg.BucketSec),
		Baseline: v.Base.Median[v.Lead], Observed: lv.Observed, Score: svcErrScore(z, cfg.CriticalZ), Direction: "up", Evidence: string(raw),
	}
}

// rtBases — SAF: TraceRED'in taban okumasından servis başına özet.
func rtBases(base map[string][]float64) map[string]rtBase {
	p50, p90 := tracePart(svcRoute, svcMeasureP50), tracePart(svcRoute, svcMeasureP90)
	out := map[string]rtBase{}
	for k := range base {
		svc, part, _ := strings.Cut(k, "\x00")
		if part != p50 && part != p90 {
			continue
		}
		if _, ok := out[svc]; !ok {
			out[svc] = rtBaseOf(base[chstore.MLSeriesKey(svc, p50)], base[chstore.MLSeriesKey(svc, p90)])
		}
	}
	return out
}

// rtEligible — SAF: tabanı yeterli servisler, ad sırasıyla.
func rtEligible(bases map[string]rtBase, minSamples int) []string {
	var out []string
	for svc, b := range bases {
		if b.ready(minSamples) {
			out = append(out, svc)
		}
	}
	sort.Strings(out)
	return out
}

// rtEvents — pencere + taban → olaylar (ad sırasıyla; tavanı kimin harcadığı
// deterministik). Uzatma günlük tavanı harcamaz. fams: olay servisi → ailesi.
func (d *TraceRED) rtEvents(deps Deps, w rtWindow, bases map[string]rtBase, end time.Time, cfg chstore.MLConfig, class string, now time.Time) (out []chstore.MLEvent, fams map[string]int) {
	fams = map[string]int{}
	for _, svc := range rtEligible(bases, cfg.MinBaselineSamples) {
		open := deps.extends(detSvcResponseTime, svc, "")
		samples, fam := rtSamples(w, svc)
		v := rtDecide(samples, bases[svc], cfg.MinBaselineSamples, open)
		if !v.Emit || !(open || d.dayCap(detSvcResponseTime, cfg, now)) {
			continue
		}
		out = append(out, rtEvent(svc, samples, v, end, deps.startedAt(detSvcResponseTime, svc, ""), cfg, class))
		fams[svc] = fam
	}
	return out, fams
}

// rtTick — TraceRED tikinin yanıt süresi adımı: taban özeti (yoklama için
// saklanır) → tabanı olan servis varsa 1 dk'lık pencere → olaylar. Hatalar
// YALITILIR: not + log, tik hatası değil. baseErr = taban serisi sorgusunun hatası.
func (d *TraceRED) rtTick(ctx context.Context, deps Deps, fams []impFam, base map[string][]float64, famOf map[int]map[string]any,
	end time.Time, cfg chstore.MLConfig, class string, now time.Time, baseErr error) []chstore.MLEvent {
	bases := rtBases(base)
	d.mu.Lock()
	d.rtBase, d.rtBaseAt = bases, now
	d.mu.Unlock()
	if baseErr != nil {
		log.Printf("[ml] svc_response_time taban sorgusu düştü (bu kovanın taban örneği yok): %v", baseErr)
	}
	if len(rtEligible(bases, cfg.MinBaselineSamples)) == 0 {
		d.setRTState(fmt.Sprintf("ölçülemedi: taban oluşuyor (hiçbir servisin yeterli taban örneği yok; en az %d gerekir)", cfg.MinBaselineSamples), -1)
		return nil
	}
	w, err := d.rtMinutes(ctx, deps, fams, end, now)
	evs, evFam := d.rtEvents(deps, w, bases, end, cfg, class, now)
	for i := range evs {
		if fi, ok := evFam[evs[i].Service]; ok && famOf[fi] != nil {
			evs[i].Evidence = mergeEvidence(evs[i].Evidence, famOf[fi])
		}
	}
	d.setRTState(rtNoteOf(w, fams, err, baseErr), w.blindFrom)
	if err != nil {
		log.Printf("[ml] svc_response_time 1 dk sorgusu düştü (ölçülemeyen ailelerde dedektör bu tik sessiz): %v", err)
	}
	return evs
}

// rtNoteOf — SAF: tik durumundaki "ölçülemedi" notu ("" = temiz). İlk aile
// ölçülemediyse hiçbir servis ölçülmedi; alt aile ölçülemediyse kısmen.
func rtNoteOf(w rtWindow, fams []impFam, err, baseErr error) string {
	why := "VM sorgusu beklemede (backoff)"
	if err != nil {
		why = truncRunes(err.Error(), 160)
	}
	switch {
	case w.blindFrom >= 0 && len(fams) > 0 && w.blindFrom > fams[0].idx:
		return "kısmen ölçülüyor: " + why
	case w.blindFrom >= 0:
		return "ölçülemedi: " + why
	case baseErr != nil:
		return "kısmen ölçülüyor: taban örneği yazılamadı: " + truncRunes(baseErr.Error(), 160)
	}
	return ""
}

// setRTState — son tikin notu ve ölçülemeyen ilk aile sırası (−1 = yok).
func (d *TraceRED) setRTState(note string, blindFrom int) {
	d.mu.Lock()
	d.rtNote, d.rtBlindFrom, d.rtRan = note, blindFrom, true
	d.mu.Unlock()
}

// clearRT — dedektör kapalı: saklı dakikalar, taban özeti ve not silinir
// (kapalıyken yoklama da koşmaz; yeniden açılınca bayat taban kullanılmasın).
func (d *TraceRED) clearRT() {
	d.mu.Lock()
	d.rtMin, d.rtBase, d.rtNote, d.rtBlindFrom, d.rtRan = nil, nil, "", -1, false
	d.mu.Unlock()
}

// runBlind — recorder kancası (no_data.go tickCoverage.runBlind): bu
// dedektörün açık koşusu son tikte ÖLÇÜLEMEDİ mi. Koşunun ailesi (kanıt
// redFamily) ölçülemeyen ilk aileden itibarense kaçırma sayılmaz, koşu
// tutulur. Ailesi bilinmeyen koşu, ölçülemeyen aile varsa tutulur.
func (d *TraceRED) runBlind(ev chstore.MLEvent) bool {
	if ev.Detector != detSvcResponseTime {
		return false
	}
	d.mu.Lock()
	from, ran := d.rtBlindFrom, d.rtRan
	d.mu.Unlock()
	if !ran || from < 0 {
		return false
	}
	fam, _ := evidenceMap(ev.Evidence)["redFamily"].(string)
	for fi, pin := range anomaly.REDFamilyPins() {
		if pin.ID == fam {
			return fi >= from
		}
	}
	return true
}

// ── dakikalık yoklama (impact_probe.go) ──────────────────────────────────

// svcRTProbe — TraceRED'in İKİNCİ etki yoklaması (yanıt süresi). Ayrı tip:
// recorder dedektör başına her yoklamayı kendi anahtarıyla (probeOn) sorar.
type svcRTProbe struct{ d *TraceRED }

// moreProbers — impactProbersOf kancası: dedektörün ek etki yoklamaları.
func (d *TraceRED) moreProbers() []impactProber { return []impactProber{svcRTProbe{d}} }

func (p svcRTProbe) probeOn(cfg chstore.MLConfig) bool { return cfg.DetectorOn(detSvcResponseTime) }

// probeImpact — etki kuralı ŞU AN ateşleyen servisler (ad sırasıyla). Taban
// son zamanlı tikin özetinden (CH okuması yok; bayatsa / yoksa sessiz ve
// SORGUSUZ), aileler son tikin keşfinden. Dakikalar rtMinutes'ten: yoklama
// yalnız yeni dakikayı sorar, hemen ardından koşan erken tik hiç sormaz.
// Sorgu hatası döner (çağıran yalıtır: log, erken tik yok).
func (p svcRTProbe) probeImpact(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]impactCandidate, error) {
	if deps.VM == nil || !deps.VM.Configured() {
		return nil, nil
	}
	d := p.d
	d.mu.Lock()
	bases, at := d.rtBase, d.rtBaseAt
	d.mu.Unlock()
	if bases == nil || now.Sub(at) > impactBaseMaxTicks*time.Duration(cfg.TickSec)*time.Second {
		return nil, nil
	}
	svcs, fams := rtEligible(bases, cfg.MinBaselineSamples), d.cachedFamilies()
	if len(svcs) == 0 || len(fams) == 0 {
		return nil, nil
	}
	end := svcImpactEnd(now, cfg.IngestLagSec)
	w, err := d.rtMinutes(ctx, deps, fams, end, now)
	if err != nil {
		return nil, err
	}
	var out []impactCandidate
	for _, svc := range svcs {
		samples, _ := rtSamples(w, svc)
		if rtDecide(samples, bases[svc], cfg.MinBaselineSamples, false).Fire {
			out = append(out, impactCandidate{BaseID: chstore.MLEventID(detSvcResponseTime, svc, ""), Service: svc})
		}
	}
	return out, nil
}

// ── kart / Problem cümlesi ───────────────────────────────────────────────

// rtMeasureOfEvent — SAF: olayın ölçüsü (kanıt measure; yoksa medyan).
func rtMeasureOfEvent(e chstore.MLEvent) rtMeasure {
	part, _ := evidenceMap(e.Evidence)["measure"].(string)
	return rtMeasures[rtMeasureOfPart(part)]
}

// svcRTHead — SAF: "/anomalies" cümlesi, değer + normal:
// "orders: yanıt süresi (medyan) normalde 180 ms, şimdi 920 ms".
func svcRTHead(in CardInput) string {
	m := rtMeasures[rtMeasureOfPart(in.Part)]
	return fmt.Sprintf("%s: yanıt süresi (%s) normalde %s, şimdi %s", in.Service, m.label, msTR(in.Baseline), msTR(in.Observed))
}
