package ml

// svc_error_rate.go — v0.10.2077 (spec docs/plans/spec-ml-service-level.md
// dilim 1, operatör onayı 2026-11-13): SERVİS düzeyi hata oranı. Eski
// `anomaly:<svc>:error_rate` (metric_anomaly) işinin ML karşılığı; bu dilimde
// YALNIZ ml_events yazar (Problem yok; gölge kapısı dilim 3).
//
// KAYNAK: yalnız VictoriaMetrics RED (OTel SDK sunucu histogramları:
// http.server.request.duration, rpc.server.duration /
// rpc.server.call.duration — ÖRNEKLENMEMİŞ). Span / CH MV'leri ASLA:
// başarılı trace'ler %5, hatalılar %100 örneklenir → span oranı ~20 kat
// şişik. VM yoksa / ısınıyorsa dedektör susar (TraceRED hatası), CH'ye
// düşülmez.
//
// TABAN SERİSİ (5 dk): ek sorgu yok. TraceRED'in zaten attığı `sum by (svc, route)`
// count ve err satırları servis başına toplanır (route etiketi boş satırlar
// dahil = `sum by (svc)` ile aynı toplam). Aileler TOPLANMAZ (anomaly
// red_vm_query.go kuralı): kova başına servisin ilk ailesi (HTTP → gRPC
// legacy → gRPC stable) kullanılır. Seri ml_series'e servis başına 2 satır
// (`*|svc_count`, `*|svc_err_ratio`); kardinalite ≤ 2 × servis.
//
// GİRİŞ-SPAN İLKESİ: aileler yalnız SUNUCU (server) istek metrikleridir;
// consumer (Kafka/kuyruk tüketicisi) girişi bu kaynakta YOK — yalnız tüketici
// olan servis bu dedektörde kördür (tik durumunda ayrıca "ölçülemedi" denmez;
// servisin RED serisi hiç yoktur).
//
// TETİK (v0.10.2373, etki olayı programı dilim I3; operatör kararı 2026-11-19
// "tek etki olayı Problem açsın", emsal Dynatrace Davis): karar 5 dakikalık
// kovalarla DEĞİL, 1 DAKİKALIK örneklerle ve etki kuralıyla (impact_rule.go
// impactDecide, impactFailureRate) verilir: son 5 tam dakikanın en az 3'ünde
// oran ≥ max(%1, taban medyanı × 1,5, taban medyanı + 1 puan) ve o dakikada
// en az 10 istek; en son ölçülen dakika da ihlal. Eski kural (3 ardışık 5 dk
// kova, ≥ 2 × taban, taban trafiği ≥ 0,2 istek/sn) SİLİNDİ: veri gecikmesi ve
// tik hizasıyla Problem ~20 dk sonra açılıyordu. Taban DEĞİŞMEDİ: 5 dakikalık
// `*|svc_err_ratio` serisinin mevsimsel medyanı (bu yüzden 5 dk seri
// ml_series'e yazılmaya devam eder); taban örneği MinBaselineSamples'ın
// altındaysa dedektör o serviste sessiz.
//
// 1 DK ÖRNEK: aile başına tik başına 2 EK aralık sorgusu
// (`sum by (svc) (increase(..._count[60s]))`, count ve err; 5 nokta), KENDİ
// backoff ailesi (impact:<aile>) ve tikin VM sayacıyla (vmRange). Pencere = [bitiş − 5 dk, bitiş),
// bitiş = (now − IngestLagSec) dakikaya aşağı yuvarlanmış. Dakikada istek 0
// ya da nokta yok → o dakika ÖLÇÜLMEDİ (0/0 oran değildir; ihlal de temiz de
// sayılmaz). İstek var, hata satırı yok → oran 0, ölçüldü. Sorgu atlanır
// (backoff) ya da hata verirse o aile bu tik sessiz; CH span'ine DÜŞÜLMEZ.
//
// UZATMA: olay açıkken (deps.extends) pencerede en az bir ihlal kaldıkça olay
// yazılmaya devam eder (kural "temiz" diyene dek), günlük tavanı harcamaz.
// Başlangıç (StartedAt) ilk ihlal dakikasının başıdır ve koşu boyunca sabit
// kalır (deps.startedAt). `-batch` servisler MUAF DEĞİL (batchKeep yük
// sinyali saymaz; hata oranı gerçek sinyaldir).

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	detSvcErrorRate = "svc_error_rate"
	// svcRoute — servis düzeyi serinin route yer tutucusu (ml_series part
	// "*|<ölçü>"). Ölçü adları route serilerininkinden FARKLI (svc_count /
	// svc_err_ratio): TraceRED'in route döngüsü ("count") bunları görmez.
	svcRoute        = "*"
	svcMeasureCount = "svc_count"
	svcMeasureRatio = "svc_err_ratio"
	// svcImpactStepSec — etki kuralının örnek adımı (1 dk; kod sabiti).
	svcImpactStepSec = impactStepSec
)

// svcAgg — SAF toplayıcı: aile × servis × kova → count / err toplamı.
type svcAgg struct {
	count, err map[svcFamKey]map[int64]float64 // aile × servis → kova → değer
	errOK      map[int]bool                    // ailenin err sorgusu koştu mu
}

type svcFamKey struct {
	fam int
	svc string
}

func newSvcAgg() *svcAgg {
	return &svcAgg{count: map[svcFamKey]map[int64]float64{}, err: map[svcFamKey]map[int64]float64{}, errOK: map[int]bool{}}
}

// add — measure "count" | "err"; diğerleri yok sayılır. famIdx = pin sırası.
func (a *svcAgg) add(famIdx int, svc, measure string, t int64, v float64) {
	if svc == "" || math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	var m map[svcFamKey]map[int64]float64
	switch measure {
	case "count":
		m = a.count
	case "err":
		m = a.err
	default:
		return
	}
	k := svcFamKey{fam: famIdx, svc: svc}
	if m[k] == nil {
		m[k] = map[int64]float64{}
	}
	m[k][t] += v
}

// errRan — ailenin err sorgusu bu turda koştu (bütçe/geri çekilme atlatmadı).
func (a *svcAgg) errRan(famIdx int) { a.errOK[famIdx] = true }

// emit — SAF: servis × kova başına İLK ailenin (en küçük sıra, count > 0)
// değerleri → series haritasına `*|svc_count` ve (err koştuysa)
// `*|svc_err_ratio`. Seri tavanı UYGULANMAZ (servis sayısıyla sınırlı).
// Dönüş (v0.10.2221 H5): servis → en yeni kovada seçilen ailenin sırası
// (olay kaydının seri ifadesi için).
func (a *svcAgg) emit(series map[string]map[int64]float64) map[string]int {
	type pick struct {
		fam        int
		count, err float64
		errKnown   bool
	}
	best := map[string]map[int64]pick{} // svc → kova → seçilen aile
	for k, byB := range a.count {
		fam, svc := k.fam, k.svc
		for t, c := range byB {
			if c <= 0 {
				continue
			}
			if best[svc] == nil {
				best[svc] = map[int64]pick{}
			}
			if cur, ok := best[svc][t]; ok && cur.fam <= fam {
				continue
			}
			p := pick{fam: fam, count: c, errKnown: a.errOK[fam]}
			if e := a.err[k]; e != nil {
				p.err = e[t]
			}
			best[svc][t] = p
		}
	}
	put := func(svc, part string, t int64, v float64) {
		k := chstore.MLSeriesKey(svc, part)
		if series[k] == nil {
			series[k] = map[int64]float64{}
		}
		series[k][t] = v
	}
	famOf := map[string]int{}
	for svc, byT := range best {
		var latest int64
		for t, p := range byT {
			put(svc, tracePart(svcRoute, svcMeasureCount), t, p.count)
			if p.errKnown {
				put(svc, tracePart(svcRoute, svcMeasureRatio), t, p.err/p.count)
			}
			if _, ok := famOf[svc]; !ok || t > latest {
				famOf[svc], latest = p.fam, t
			}
		}
	}
	return famOf
}

// svcErrScore — olayın skoru; tetik etki kuralıdır (z kapı değil), skor en az
// CriticalZ. Gerçek z kanıtta ("z").
func svcErrScore(z, criticalZ float64) float64 { return math.Max(math.Abs(z), criticalZ) }

// ── v0.10.2373 (I3): 1 dk örnek + etki kuralı ─────────────────────────────

// svcImpactQueries — SAF: ailenin 1 dakikalık servis düzeyi istek ve
// sunucu-hatası sayısı (traceQueries'in pinleri; route kırılımı ve bucket yok).
func svcImpactQueries(pin anomaly.REDFamilyPin, svc, status string) (count, errq string) {
	count = fmt.Sprintf("sum by (%s) (increase(%s_count[%ds]))", svc, pin.Names, svcImpactStepSec)
	errq = fmt.Sprintf("sum by (%s) (increase(%s_count{%s=~\"%s\"}[%ds]))", svc, pin.Names, status, pin.ServerErr, svcImpactStepSec)
	return
}

// svcImpactEnd — SAF: karar penceresinin bitişi (hariç) = ingest gecikme
// payını geçmiş son TAM dakikanın sonu. Saat dilimi bağımsız (unix sn).
func svcImpactEnd(now time.Time, lagSec int) time.Time {
	return time.Unix((now.Unix()-int64(lagSec))/svcImpactStepSec*svcImpactStepSec, 0)
}

// addMinutes — aralık sorgusu satırlarını DAKİKA BAŞI anahtarıyla ekler. VM
// noktası t, (t − 60 sn, t] artışıdır → dakika başı t − 60. Pencere
// [end − 5 dk, end) dışındaki nokta yok sayılır.
func (a *svcAgg) addMinutes(famIdx int, svcLabel, measure string, rows []chstore.SpanMetricSeries, end int64) {
	for _, r := range rows {
		svc := groupKeyLabels(r.GroupKey)[svcLabel]
		for _, p := range r.Points {
			if m, ok := impactMinuteOf(p.Time, end); ok {
				a.add(famIdx, svc, measure, m, p.Value)
			}
		}
	}
}

// svcImpactBackoff — 1 dk'lık sorguların KENDİ backoff ailesi: ağır hata
// (422 / süre aşımı) işlem düzeyi `trace:<aile>` sorgularını bekletmez.
const svcImpactBackoff = "impact:"

// svcImpactFetch — ailenin 1 dk'lık count + err aralık sorguları (tik başına
// aile başına 2 sorgu, 5 nokta) → toplayıcı. Atlanan (backoff) aile eklenmez.
// count hatasında aile eklenmez; err hatasında count YİNE eklenir ama errRan
// işaretlenmez: aile dakikayı sahiplenir, oran bilinmez → o ailenin
// servisleri bu tik sessiz (alt sıradaki aileye kaymaz). Hata çağırana döner;
// çağıran tiki düşürmez ve tik hatası saymaz (trace_red.go: yalıtım).
func svcImpactFetch(ctx context.Context, deps Deps, pin anomaly.REDFamilyPin, fl traceFamilyLabels, famIdx int, agg *svcAgg, end, now time.Time) error {
	cq, eq := svcImpactQueries(pin, fl.svc, fl.status)
	from := end.Add(-time.Duration(impactWindow-1) * svcImpactStepSec * time.Second)
	crows, skip, err := vmRange(ctx, deps, svcImpactBackoff+pin.ID, cq, from, end, svcImpactStepSec, now)
	if skip {
		return nil
	}
	if err != nil {
		return fmt.Errorf("vm %s/svc_count_1m: %w", pin.ID, err)
	}
	agg.addMinutes(famIdx, fl.svc, "count", crows, end.Unix())
	erows, skip, err := vmRange(ctx, deps, svcImpactBackoff+pin.ID, eq, from, end, svcImpactStepSec, now)
	if skip {
		return nil
	}
	if err != nil {
		return fmt.Errorf("vm %s/svc_err_1m: %w", pin.ID, err)
	}
	agg.addMinutes(famIdx, fl.svc, "err", erows, end.Unix())
	agg.errRan(famIdx)
	return nil
}

// svcImpactSamples — SAF: servisin [end − 5 dk, end) dakikalarının örnekleri
// (en eski → en yeni). minute = svcAgg.emit çıktısı (dakika başı anahtarlı;
// dakika başına ilk aile). Ölçülmüş = o dakikada istek > 0 VE oran biliniyor
// (ailenin err sorgusu koştu). İstek 0 / nokta yok → ölçülmedi.
func svcImpactSamples(minute map[string]map[int64]float64, svc string, end int64) []impactSample {
	cnt := minute[chstore.MLSeriesKey(svc, tracePart(svcRoute, svcMeasureCount))]
	rat := minute[chstore.MLSeriesKey(svc, tracePart(svcRoute, svcMeasureRatio))]
	out := make([]impactSample, impactWindow)
	for i := range out {
		t := end - int64(impactWindow-i)*svcImpactStepSec
		c, cok := cnt[t]
		r, rok := rat[t]
		if cok && rok && c > 0 {
			out[i] = impactSample{Value: r, Load: c, OK: true}
		} else if cok {
			out[i].Load = c
		}
	}
	return out
}

// svcImpactVerdict — servis hata oranının etki kararı.
type svcImpactVerdict struct {
	impactVerdict
	Emit        bool    // olay bu tik yazılır: yeni ateşleme ya da açık olayın uzatması
	Median, MAD float64 // taban oran medyanı / MAD (5 dk seri, mevsimsel)
	Z           float64 // robust z (bilgi; kapı değil)
	BaseRate    float64 // taban trafiği, istek/sn (count medyanı; bilgi, kapı değil)
}

// svcBase — servisin mevsimsel taban özeti: karar için gereken her şey
// (örnek sayısı, oran medyanı / MAD, taban trafiği). v0.10.2377 (I2): tik bunu
// servis başına saklar, dakikalık yoklama CH okumadan aynı kararı verir.
type svcBase struct {
	N           int     // taban oran örneği sayısı
	Median, MAD float64 // taban oran medyanı / MAD
	Rate        float64 // taban trafiği (count medyanı)
}

// svcBaseOf — SAF: taban örnekleri → özet.
func svcBaseOf(baseRatio, baseCount []float64) svcBase {
	b := svcBase{N: len(baseRatio)}
	b.Median, b.MAD = medianMAD(baseRatio)
	b.Rate, _ = medianMAD(baseCount)
	return b
}

// svcImpactDecide — SAF: samples = son impactWindow dakikanın örnekleri,
// baseRatio / baseCount = mevsimsel taban örnekleri, open = olayın açık koşusu
// var. Taban örneği azsa sessiz (açık olay da uzamaz; kapanış koşu sayacında).
// Uzatma: açıkken kural "temiz" demiyor ve pencerede en az bir ihlal var.
func svcImpactDecide(samples []impactSample, baseRatio, baseCount []float64, minSamples int, open bool) svcImpactVerdict {
	return svcImpactDecideBase(samples, svcBaseOf(baseRatio, baseCount), minSamples, open)
}

// svcImpactDecideBase — SAF: svcImpactDecide'ın taban özetiyle çalışan gövdesi
// (tik ve yoklama AYNI kararı verir).
func svcImpactDecideBase(samples []impactSample, b svcBase, minSamples int, open bool) svcImpactVerdict {
	v := svcImpactVerdict{impactVerdict: impactVerdict{Onset: -1}}
	if b.N < minSamples {
		v.Reason = "eksik veri: taban örneği az"
		return v
	}
	v.Median, v.MAD, v.BaseRate = b.Median, b.MAD, b.Rate
	v.impactVerdict = impactDecide(impactFailureRate, samples, v.Median)
	v.Z = (v.Observed - v.Median) / effMAD(v.Median, v.MAD)
	v.Emit = impactEmit(v.impactVerdict, open)
	return v
}

// svcImpactEvent — SAF: kararın olayı. Gözlenen = en yeni ölçülen dakikanın
// oranı, taban = medyan. StartedAt = ilk ihlal dakikasının başı; açık koşunun
// yazılmış başlangıcı (openStart, 0 = yok) daha erkense o kalır (pencere
// kayarken başlangıç oynamaz). LastSeen = pencere bitişi. WindowSec taban
// kovasıdır (kartın "açık" kuralı ve oynatma serisi 5 dk ml_series'e bakar);
// örnek adımı kanıtta (sampleSec). Kanıt kararı açıklar: kural, eşik, ihlal
// sayısı ve dakika dakika oran (null = ölçülmedi) + yük. "dwell" YAZILMAZ.
func svcImpactEvent(svc string, samples []impactSample, v svcImpactVerdict, end time.Time, openStart int64, cfg chstore.MLConfig, class string) chstore.MLEvent {
	first := end.Unix() - int64(len(samples))*svcImpactStepSec
	started := impactRunStart(end, len(samples), v.Onset, openStart)
	ratios, loads := make([]any, len(samples)), make([]float64, len(samples))
	for i, s := range samples {
		loads[i] = s.Load
		if s.OK {
			ratios[i] = s.Value
		}
	}
	ev := map[string]any{"source": "vm", "route": "", "dayClass": class, "mad": v.MAD, "baselineDays": cfg.BaselineDays,
		"measure": svcMeasureRatio, "z": v.Z, "baseRate": v.BaseRate,
		"rule": "impact", "fired": v.Fire, "threshold": v.Threshold, "violations": v.Violations, "window": impactWindow,
		"minViolations": impactViolations, "minLoad": impactRules[impactFailureRate].MinLoad,
		"sampleSec": svcImpactStepSec, "minuteFrom": first, "ratios": ratios, "loads": loads}
	// v0.10.2380 — başarısız / başarılı istek (en yeni ölçülen dakika + olağan düzey).
	if rs, ok := svcRequestSplit(samples, v, first); ok {
		ev[requestsKey] = rs
	}
	raw, _ := json.Marshal(ev)
	return chstore.MLEvent{
		ID: chstore.MLEventID(detSvcErrorRate, svc, ""), Detector: detSvcErrorRate, Signal: chstore.MLSignalTrace, Stage: chstore.MLStageShadow,
		Service: svc, StartedAt: started, LastSeen: end.UnixNano(), WindowSec: uint32(cfg.BucketSec),
		Baseline: v.Median, Observed: v.Observed, Score: svcErrScore(v.Z, cfg.CriticalZ), Direction: "up", Evidence: string(raw),
	}
}

// requestsKey — olay kanıtındaki başarısız / başarılı istek bölümü
// (chstore.MLRequestSplitOf aynı anahtarı okur).
const requestsKey = "requests"

// svcRequestSplit — SAF (v0.10.2380): kararın örneklerinden dakikada
// başarısız / başarılı istek. Kaynak dedektörün ZATEN attığı 1 dakikalık
// istek + hata sayısı çifti (oran × istek = hata sayısı; ek sorgu yok). En
// yeni ÖLÇÜLEN dakika okunur (v.Observed ile aynı örnek). Olağan düzey: taban
// trafiği (5 dk serinin istek/sn medyanı × 60) × taban oran medyanı; taban
// trafiği yoksa yazılmaz. first = pencerenin ilk dakikasının başı (unix sn).
// ok=false: pencerede ölçülmüş dakika yok.
func svcRequestSplit(samples []impactSample, v svcImpactVerdict, first int64) (chstore.StdRequestSplit, bool) {
	for i := len(samples) - 1; i >= 0; i-- {
		if s := samples[i]; s.OK {
			return chstore.NewRequestSplit(first+int64(i)*svcImpactStepSec, s.Value, s.Load, v.Median, v.BaseRate*svcImpactStepSec)
		}
	}
	return chstore.StdRequestSplit{}, false
}

// svcImpactEvents — TraceRED tikinin servis düzeyi adımı: 1 dk'lık örnekler
// (minute) + mevsimsel taban → olaylar. Uzatma günlük tavanı harcamaz.
// Servisler ad sırasıyla gezilir (tavanı kimin harcadığı deterministik).
func (d *TraceRED) svcImpactEvents(deps Deps, minute map[string]map[int64]float64, base map[string][]float64, end time.Time, cfg chstore.MLConfig, class string, now time.Time) []chstore.MLEvent {
	countPart, ratioPart := tracePart(svcRoute, svcMeasureCount), tracePart(svcRoute, svcMeasureRatio)
	var svcs []string
	for k := range minute {
		if svc, part, _ := strings.Cut(k, "\x00"); part == countPart {
			svcs = append(svcs, svc)
		}
	}
	sort.Strings(svcs)
	var out []chstore.MLEvent
	for _, svc := range svcs {
		open := deps.extends(detSvcErrorRate, svc, "")
		samples := svcImpactSamples(minute, svc, end.Unix())
		v := svcImpactDecide(samples, base[chstore.MLSeriesKey(svc, ratioPart)], base[chstore.MLSeriesKey(svc, countPart)], cfg.MinBaselineSamples, open)
		if !v.Emit || !(open || d.dayCap(detSvcErrorRate, cfg, now)) {
			continue
		}
		out = append(out, svcImpactEvent(svc, samples, v, end, deps.startedAt(detSvcErrorRate, svc, ""), cfg, class))
	}
	return out
}

// ── v0.10.2377 (I2): dakikalık yoklama ───────────────────────────────────
// Zamanlı tikler arasında recorder dakikada bir sorar (impact_probe.go):
// "kural ateşleyen servis var mı?". Yoklama tikin 1 dk'lık sorgusunun AYNISIDIR
// (aile başına 2 aralık sorgusu, aynı backoff ailesi ve VM sayacı); taban son
// tikin sakladığı özetten gelir (CH okuması yok), etiket keşfi yapılmaz (son
// tikin keşfettiği etiketler). Satırlar pencere bitişiyle saklanır: hemen
// ardından koşan erken tik aynı pencere bitişi için yeniden sormaz.

// impFam — etiketleri keşfedilmiş RED ailesi (sıra = pin sırası).
type impFam struct {
	idx int
	pin anomaly.REDFamilyPin
	fl  traceFamilyLabels
}

// cachedFamilies — son tikin keşfettiği etiketlerle aileler; keşif sorgusu YOK.
func (d *TraceRED) cachedFamilies() []impFam {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []impFam
	for fi, pin := range anomaly.REDFamilyPins() {
		if fl, ok := d.labels[pin.ID]; ok && fl.ok {
			out = append(out, impFam{idx: fi, pin: pin, fl: fl})
		}
	}
	return out
}

// impactMinutes — ailelerin 1 dk'lık servis count + err satırları → toplayıcı.
// Yoklama aynı pencere bitişi için satırları zaten aldıysa VM'e gidilmez.
// Hata ilk ailenin hatasıdır; toplayıcı yine döner (svcImpactFetch kuralı:
// err sorgusu düşen aile dakikayı sahiplenir, oranı bilinmez).
func (d *TraceRED) impactMinutes(ctx context.Context, deps Deps, fams []impFam, end, now time.Time) (*svcAgg, error) {
	if agg := d.probeRows(end); agg != nil {
		return agg, nil
	}
	agg := newSvcAgg()
	var first error
	for _, f := range fams {
		if err := svcImpactFetch(ctx, deps, f.pin, f.fl, f.idx, agg, end, now); err != nil && first == nil {
			first = err
		}
	}
	return agg, first
}

// probeRows — yoklamanın bu pencere bitişi için sakladığı satırlar (yoksa nil).
// Toplayıcı saklandıktan sonra yalnız OKUNUR (emit).
func (d *TraceRED) probeRows(end time.Time) *svcAgg {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.probeAgg != nil && d.probeEnd == end.Unix() {
		return d.probeAgg
	}
	return nil
}

// cacheImpactBase — tik sonunda: servis başına taban özeti (yoklamanın girdisi).
func (d *TraceRED) cacheImpactBase(base map[string][]float64, at time.Time) {
	countPart, ratioPart := tracePart(svcRoute, svcMeasureCount), tracePart(svcRoute, svcMeasureRatio)
	out := map[string]svcBase{}
	for k, xs := range base {
		if svc, part, _ := strings.Cut(k, "\x00"); part == ratioPart {
			out[svc] = svcBaseOf(xs, base[chstore.MLSeriesKey(svc, countPart)])
		}
	}
	d.mu.Lock()
	d.impBase, d.impBaseAt = out, at
	d.mu.Unlock()
}

// impactBase — saklı taban özeti; hiç yoksa ya da impactBaseMaxTicks tikten
// eskiyse ok=false (bayat tabanla erken tik açılmaz; zamanlı tik yeniler).
func (d *TraceRED) impactBase(now time.Time, tick time.Duration) (map[string]svcBase, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.impBase == nil || now.Sub(d.impBaseAt) > impactBaseMaxTicks*tick {
		return nil, false
	}
	return d.impBase, true
}

// probeOn — impactProber: yoklama yalnız svc_error_rate açıkken.
func (d *TraceRED) probeOn(cfg chstore.MLConfig) bool { return cfg.DetectorOn(detSvcErrorRate) }

// probeImpact — impactProber: etki kuralı ŞU AN ateşleyen servisler (ad
// sırasıyla). Açık koşu / bekleme süzgeci çağırandadır (probeTriggers). Taban
// özeti ya da keşfedilmiş aile yoksa sessiz ve SORGUSUZ. Sorgu hatası döner
// (çağıran yalıtır: log, erken tik yok); hatalı yoklamanın satırları saklanmaz.
func (d *TraceRED) probeImpact(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]impactCandidate, error) {
	if deps.VM == nil || !deps.VM.Configured() {
		return nil, nil
	}
	base, ok := d.impactBase(now, time.Duration(cfg.TickSec)*time.Second)
	fams := d.cachedFamilies()
	if !ok || len(fams) == 0 {
		return nil, nil
	}
	end := svcImpactEnd(now, cfg.IngestLagSec)
	agg, err := d.impactMinutes(ctx, deps, fams, end, now)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.probeAgg, d.probeEnd = agg, end.Unix()
	d.mu.Unlock()
	minute := map[string]map[int64]float64{}
	agg.emit(minute)
	var svcs []string
	for svc, b := range base {
		if b.N >= cfg.MinBaselineSamples {
			svcs = append(svcs, svc)
		}
	}
	sort.Strings(svcs)
	var out []impactCandidate
	for _, svc := range svcs {
		if v := svcImpactDecideBase(svcImpactSamples(minute, svc, end.Unix()), base[svc], cfg.MinBaselineSamples, false); v.Fire {
			out = append(out, impactCandidate{BaseID: chstore.MLEventID(detSvcErrorRate, svc, ""), Service: svc})
		}
	}
	return out, nil
}

// svcErrHead — SAF: "/anomalies" cümlesi, değer + normal:
// "orders-api: servis hata oranı normalde %0,4, şimdi %3,2".
func svcErrHead(in CardInput, sp detSpec) string {
	f := newValueFmt(sp, in, in.Observed)
	normal := "normalde neredeyse hiç yok"
	if in.Baseline > 0 {
		normal = "normalde " + f.one(in.Baseline, false)
	}
	return in.Service + ": servis hata oranı " + normal + ", şimdi " + f.one(in.Observed, false)
}
