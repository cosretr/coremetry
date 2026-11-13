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
// MALİYET: EK SORGU YOK. TraceRED'in zaten attığı `sum by (svc, route)`
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
// TETİK (spec, operatör onayı): oran ≥ 2 × mevsimsel taban medyanı, oran
// ≥ %1, taban trafiği (count medyanı) ≥ 0,2 istek/sn, 3 kova (15 dk) sürmüş.
// `-batch` servisler MUAF DEĞİL (batchKeep yük sinyali saymaz; hata oranı
// gerçek sinyaldir).

import (
	"math"
	"sort"
	"strings"
	"time"

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

	svcErrMinRatio = 2.0  // taban medyanının katı
	svcErrFloor    = 0.01 // mutlak oran tabanı (%1)
	svcErrMinRate  = 0.2  // taban trafiği, istek/sn (count medyanı)
	svcErrDwell    = 3    // ardışık kova (5 dk kova → 15 dk)
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
func (a *svcAgg) emit(series map[string]map[int64]float64) {
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
	for svc, byT := range best {
		for t, p := range byT {
			put(svc, tracePart(svcRoute, svcMeasureCount), t, p.count)
			if p.errKnown {
				put(svc, tracePart(svcRoute, svcMeasureRatio), t, p.err/p.count)
			}
		}
	}
}

// svcErrVerdict — servis hata oranı kararı.
type svcErrVerdict struct {
	Fire     bool
	Reason   string
	Median   float64 // taban oran medyanı
	MAD      float64
	Observed float64 // son kova oranı
	Z        float64 // robust z (bilgi; kapı değil)
	BaseRate float64 // taban trafiği (istek/sn, count medyanı)
}

// svcErrorRateVerdict — SAF: cur = son svcErrDwell kovanın oranları (eksik
// kova = çağıran nil verir), baseRatio / baseCount = mevsimsel taban
// örnekleri. Spec eşikleri sabit (vida yok).
func svcErrorRateVerdict(cur, baseRatio, baseCount []float64, minSamples int) svcErrVerdict {
	v := svcErrVerdict{}
	if len(cur) < svcErrDwell {
		v.Reason = "dwell: 3 kova yok"
		return v
	}
	if len(baseRatio) < minSamples {
		v.Reason = "eksik veri: taban örneği az"
		return v
	}
	v.Median, v.MAD = medianMAD(baseRatio)
	v.BaseRate, _ = medianMAD(baseCount)
	v.Observed = cur[len(cur)-1]
	if v.BaseRate < svcErrMinRate {
		v.Reason = "taban trafiği 0,2 istek/sn altında"
		return v
	}
	for _, x := range cur {
		if x < svcErrFloor || x < svcErrMinRatio*v.Median {
			v.Reason = "dwell: kova eşiği geçmedi"
			return v
		}
	}
	v.Z = (v.Observed - v.Median) / effMAD(v.Median, v.MAD)
	v.Fire = true
	return v
}

// svcErrScore — kalite süzgecinin z kapısı (feedbackZ) için skor. Tetik
// spec'in oran kuralıdır; z kapısı onu sessizce sıkılaştırmasın diye skor
// en az CriticalZ — operatörün "değil" etiketleri eşiği yine yükseltir
// (feedbackZ > CriticalZ → bastırılır). Gerçek z kanıtta ("z").
func svcErrScore(z, criticalZ float64) float64 { return math.Max(math.Abs(z), criticalZ) }

// svcErrorEvents — TraceRED tikinin servis düzeyi adımı: series'teki
// `*|svc_err_ratio` serileri + taban → olaylar (günlük tavan dahil).
func (d *TraceRED) svcErrorEvents(series map[string]map[int64]float64, base map[string][]float64, last time.Time, cfg chstore.MLConfig, class string, now time.Time) []chstore.MLEvent {
	step := int64(cfg.BucketSec)
	from := last.Unix() - int64(svcErrDwell-1)*step
	var out []chstore.MLEvent
	for k, byB := range series {
		svc, part, _ := strings.Cut(k, "\x00")
		if part != tracePart(svcRoute, svcMeasureRatio) {
			continue
		}
		cur, ok := dwellWindow(byB, from, last.Unix(), step, false)
		if !ok {
			continue // eksik kova (scrape boşluğu / err sorgusu atlandı) ≠ 0
		}
		v := svcErrorRateVerdict(cur, base[k], base[chstore.MLSeriesKey(svc, tracePart(svcRoute, svcMeasureCount))], cfg.MinBaselineSamples)
		if !v.Fire || !d.dayCap(detSvcErrorRate, cfg, now) {
			continue
		}
		j := Judgement{Fire: true, Direction: "up", Z: svcErrScore(v.Z, cfg.CriticalZ), Median: v.Median, MAD: v.MAD, Observed: v.Observed}
		ev := d.event(detSvcErrorRate, svc, "", j, last, cfg, class)
		ev.Evidence = mergeEvidence(ev.Evidence, map[string]any{"measure": svcMeasureRatio, "z": v.Z, "baseRate": v.BaseRate,
			"dwell": svcErrDwell, "minRatio": svcErrMinRatio, "floor": svcErrFloor, "minRate": svcErrMinRate})
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
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
