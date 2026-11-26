package ml

// endpoint_impact.go — v0.10.2474 (operatör 2026-11-26: "Problem'ler Davis'ten
// kötü"): UÇ NOKTA (servis × route) hata oranı ve yanıt süresi, servis
// düzeyiyle AYNI etki kuralıyla (impact_rule.go impactDecide; 1 dakikalık
// örnek, son 5'in en az 3'ü, en yeni de ihlal, dakikada en az 10 istek).
//
// NEDEN: büyük bir serviste tek bozuk uç nokta servis toplamını eşiğin
// üstüne taşımaz (svc_error_rate / svc_response_time susar); uç nokta
// dedektörleri (op_error_rate, throughput_drop) varsayılan kapalı, Davis
// özne terfisi gölgede. Sonuç: hiç Problem yok.
//
// ÖLÇÜLER (olay başına TEK ölçü; bir uç nokta tek olay = tek üye):
//
//	hata oranı   ≥ max(%1, taban × 1,5, taban + 1 puan)   (impactFailureRate)
//	medyan       ≥ max(taban × 1,5, taban + 100 ms)        (impactResponseMedian)
//	en yavaş %10 ≥ max(taban × 2,   taban + 1000 ms)       (impactResponseSlow)
//
// Olayın ölçüsü (Lead): ateşleyen önce; ikisi de ateşlerse hata oranı, sonra
// en büyük oran (rtDecide ile aynı ilke).
//
// TABAN: hata oranı ve trafik TraceRED'in zaten yazdığı 5 dk `route|err_ratio`
// / `route|count` serilerinden (ek sorgu yok, bugün birikmiş taban); yanıt
// süresi yeni 5 dk `route|ep_p50` / `route|ep_p90` serileri (yalnız seçili uç
// noktalar; taban örneği MinBaselineSamples'a ulaşana dek o ölçü yargılanmaz).
//
// KARDİNALİTE (kod sabitleri):
//   - yük kapısı: taban trafiği (count medyanı) dakikada ≥ 10 istek (kuralın
//     MinLoad'ı) — altındaki uç nokta aday değil;
//   - servis başına en çok epTopK (10) uç nokta, taban trafiğine göre;
//   - tik başına en çok epGlobalCap (2.000) uç nokta (açık koşular önce);
//   - 1 dk satırları yalnız adaylar için saklanır.
//
// SORGULAR (hepsi ANLIK, LIMIT yerine sorgu VM'de servis × route'a
// toplanır; ham bucket'ın aralık sorgusu YOK — vm_budget.go 422 dersi):
//
//	1 dk count : sum by (svc, route) (increase(<aile>_count[60s]))
//	1 dk err   : sum by (svc, route) (increase(<aile>_count{durum=~hata}[60s]))
//	1 dk süre  : histogram_quantiles("ep_phi", 0.5, 0.9, sum by (svc, route, le) (increase(<aile>_bucket[60s])))
//	5 dk taban : aynı yüzdelik sorgusu [kova] (tik başına aile başına 1)
//
// Tamamlanmış dakika değişmez → (dakika, aile) başına BİR kez sorulur ve
// saklanır (yoklama, erken tik ve sonraki tik aynı satırları kullanır).
// Kararlı durumda dakikada aile başına 3 sorgu; her birinin taradığı ham
// örnek tikin 300 sn pencereli count / p95 sorgularının beşte biri.
// Yüzdelik VM'de hesaplanır (MetricsQL histogram_quantiles): yanıt uç nokta ×
// 2 satır, bucket satırı dönmez.
//
// YALITIM: üç backoff ailesi (impact-ep, impact-eprt, impact-ep5); hata tik
// hatası değil, "ölçülemedi" notu (Unmeasured). Sorgu düşerse o aile o
// dakika ölçülmedi sayılır (alt aileler güvenilmez, svc_response_time kuralı).
//
// PROBLEM: olay servis düzeyi kapısından (svc_shadow.go) servisin TEK
// Problem'ine (`anomaly-auto:svc:<svc>`) üye olur; açık Problem yoksa açar,
// manşet uç noktayı adlandırır. Servis düzeyi olay varken manşet ve değer
// onlardan (uç nokta yalnız üye).
//
// op_error_rate İLE TEKİLLEŞTİRME: aynı tikte bu dedektör bir uç nokta için
// olay yazdıysa op_error_rate o uç nokta için yazmaz (trace_red.go).

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

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	detEndpointImpact = "endpoint_impact"
	// epTopK — servis başına aday uç nokta (taban trafiğine göre). Büyük
	// serviste kritik yollar ilk 10'dadır; uzun kuyruk yük kapısına zaten takılır.
	epTopK = 10
	// epGlobalCap — tik başına aday uç nokta tavanı (açık koşular önce).
	epGlobalCap = 2000
	// ml_series ölçü adları (part "<route>|<ölçü>").
	epMeasureErr = "err_ratio" // TraceRED'in mevcut 5 dk oran serisi
	epMeasureP50 = "ep_p50"
	epMeasureP90 = "ep_p90"
	// epPhiLabel — histogram_quantiles'ın yüzdelik etiketi.
	epPhiLabel = "ep_phi"
	// backoff aileleri.
	epBackoff     = "impact-ep:"
	epRTBackoff   = "impact-eprt:"
	epBaseBackoff = "impact-ep5:"
)

// epMeasure — yargılanan ölçü.
type epMeasure struct {
	kind impactKind
	part string // ml_series ölçü adı + kanıt "measure"
	key  string // chstore ölçü anahtarı ("" = hata oranı)
}

// epMeasures — sıra sabit: 0 hata oranı, 1 medyan, 2 en yavaş %10.
var epMeasures = [3]epMeasure{
	{impactFailureRate, epMeasureErr, ""},
	{impactResponseMedian, epMeasureP50, chstore.MLRespMedian},
	{impactResponseSlow, epMeasureP90, chstore.MLRespSlow},
}

// epMeasureOfPart — SAF: kanıt / part ölçü adı → sıra (bilinmeyen → hata oranı).
func epMeasureOfPart(part string) int {
	switch {
	case strings.HasSuffix(part, epMeasureP50):
		return 1
	case strings.HasSuffix(part, epMeasureP90):
		return 2
	}
	return 0
}

// epMeasureOfEvent — SAF: olayın ölçüsü (kanıt measure).
func epMeasureOfEvent(e chstore.MLEvent) epMeasure {
	m, _ := evidenceMap(e.Evidence)["measure"].(string)
	return epMeasures[epMeasureOfPart(m)]
}

type epKey struct{ svc, route string }

// ── taban + aday seçimi ──────────────────────────────────────────────────

// epBase — uç noktanın mevsimsel taban özeti.
type epBase struct {
	RateN       int     // trafik taban örneği
	Rate        float64 // taban trafiği, istek/sn (count medyanı)
	N           [3]int
	Median, MAD [3]float64
}

// ready — SAF: trafik tabanı ve en az bir ölçünün tabanı yeterli.
func (b epBase) ready(minSamples int) bool {
	if b.RateN < minSamples {
		return false
	}
	for _, n := range b.N {
		if n >= minSamples {
			return true
		}
	}
	return false
}

// epBases — SAF: TraceRED'in taban okumasından uç nokta başına özet (servis
// düzeyi "*" satırları hariç).
func epBases(base map[string][]float64) map[epKey]epBase {
	out := map[epKey]epBase{}
	for k, xs := range base {
		svc, part, _ := strings.Cut(k, "\x00")
		route, m := splitTracePart(part)
		if m != "count" || route == svcRoute || svc == "" || route == "" {
			continue
		}
		b := epBase{RateN: len(xs)}
		b.Rate, _ = medianMAD(xs)
		for i, em := range epMeasures {
			ys := base[chstore.MLSeriesKey(svc, tracePart(route, em.part))]
			b.N[i] = len(ys)
			b.Median[i], b.MAD[i] = medianMAD(ys)
		}
		out[epKey{svc, route}] = b
	}
	return out
}

// epSelect — SAF: aday uç noktalar. rates = uç nokta → istek/sn; yük kapısı
// (dakikada minPerMin istek), servis başına k, toplam cap. pinned (açık koşu)
// yük kapısından ve k'dan muaf, tavanda önce. Dönüş (svc, route) sıralı.
func epSelect(rates map[epKey]float64, minPerMin float64, k, cap int, pinned func(epKey) bool) []epKey {
	bySvc := map[string][]epKey{}
	var pin []epKey
	for key, r := range rates {
		if pinned != nil && pinned(key) {
			pin = append(pin, key)
			continue
		}
		if r*60 >= minPerMin {
			bySvc[key.svc] = append(bySvc[key.svc], key)
		}
	}
	less := func(a, b epKey) bool {
		if rates[a] != rates[b] {
			return rates[a] > rates[b]
		}
		if a.svc != b.svc {
			return a.svc < b.svc
		}
		return a.route < b.route
	}
	var rest []epKey
	for _, ks := range bySvc {
		sort.Slice(ks, func(i, j int) bool { return less(ks[i], ks[j]) })
		if len(ks) > k {
			ks = ks[:k]
		}
		rest = append(rest, ks...)
	}
	sort.Slice(pin, func(i, j int) bool { return less(pin[i], pin[j]) })
	sort.Slice(rest, func(i, j int) bool { return less(rest[i], rest[j]) })
	out := append(pin, rest...)
	if len(out) > cap {
		out = out[:cap]
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].svc != out[j].svc {
			return out[i].svc < out[j].svc
		}
		return out[i].route < out[j].route
	})
	return out
}

// ── sorgular ─────────────────────────────────────────────────────────────

// epQueries — SAF: ailenin uç nokta başına count, sunucu hatası ve yüzdelik
// (medyan + p90) sorguları; win = pencere (sn).
func epQueries(f impFam, win int) (count, errq, quant string) {
	by := f.fl.svc + ", " + f.fl.route
	count = fmt.Sprintf("sum by (%s) (increase(%s[%ds]))", by, redSel(f.pin, partCount), win)
	errq = fmt.Sprintf("sum by (%s) (increase(%s[%ds]))", by, redSel(f.pin, partCount, mRe(f.fl.status, f.pin.ServerErr)), win)
	quant = fmt.Sprintf("histogram_quantiles(%q, 0.5, 0.9, sum by (%s, le) (increase(%s[%ds])))", epPhiLabel, by, redSel(f.pin, partBucket), win)
	return
}

// epSums — SAF: count / err satırları → uç nokta → değer (yalnız want).
func epSums(rows []chstore.SpanMetricSeries, f impFam, want map[epKey]bool) map[epKey]float64 {
	out := map[epKey]float64{}
	for _, r := range rows {
		lb := groupKeyLabels(r.GroupKey)
		k := epKey{lb[f.fl.svc], lb[f.fl.route]}
		v, ok := instantValue(r)
		if !ok || v < 0 || (want != nil && !want[k]) {
			continue
		}
		out[k] += v
	}
	return out
}

// epQuants — SAF: histogram_quantiles satırları → uç nokta → [p50, p90] ms.
// İki yüzdelikten biri eksik uç nokta dönmez.
func epQuants(rows []chstore.SpanMetricSeries, f impFam, want map[epKey]bool) map[epKey][2]float64 {
	type acc struct {
		q    [2]float64
		have [2]bool
	}
	by := map[epKey]*acc{}
	scale := pinMsScale(f.pin)
	for _, r := range rows {
		lb := groupKeyLabels(r.GroupKey)
		k := epKey{lb[f.fl.svc], lb[f.fl.route]}
		if want != nil && !want[k] {
			continue
		}
		phi, err := strconv.ParseFloat(lb[epPhiLabel], 64)
		v, ok := instantValue(r)
		if err != nil || !ok || v < 0 {
			continue
		}
		i := -1
		switch {
		case math.Abs(phi-0.5) < 1e-9:
			i = 0
		case math.Abs(phi-0.9) < 1e-9:
			i = 1
		}
		if i < 0 {
			continue
		}
		if by[k] == nil {
			by[k] = &acc{}
		}
		by[k].q[i], by[k].have[i] = v*scale, true
	}
	out := make(map[epKey][2]float64, len(by))
	for k, a := range by {
		if a.have[0] && a.have[1] {
			out[k] = a.q
		}
	}
	return out
}

// ── 5 dk taban serisi ────────────────────────────────────────────────────

// epBaseSeries — tikin taban adımı: seçili uç noktalar için (rates = bu
// kovanın istek/sn'si; yük kapısı + epTopK + epGlobalCap) aile başına 1 anlık
// yüzdelik sorgusu → `route|ep_p50` / `route|ep_p90` (out; kova = bucket).
// İlk aile kazanır; düşen ailede durulur (alt aileler güvenilmez), hata döner.
func epBaseSeries(ctx context.Context, deps Deps, fams []impFam, rates map[epKey]float64, at time.Time, bucket int64, bucketSec int, out map[string]map[int64]float64, now time.Time) error {
	deps.meter = nil // yalıtım: atlama trace_red ailesini kör işaretlemez
	want := map[epKey]bool{}
	for _, k := range epSelect(rates, impactRules[impactResponseMedian].MinLoad, epTopK, epGlobalCap, nil) {
		want[k] = true
	}
	if len(want) == 0 {
		return nil
	}
	taken := map[epKey]bool{}
	for _, f := range fams {
		_, _, qq := epQueries(f, bucketSec)
		rows, skip, err := vmInstant(ctx, deps, epBaseBackoff+f.pin.ID, qq, at, now)
		if err != nil {
			return fmt.Errorf("vm %s/ep_rt_5m: %w", f.pin.ID, err)
		}
		if skip {
			return fmt.Errorf("vm %s/ep_rt_5m: aile beklemede", f.pin.ID)
		}
		for k, q := range epQuants(rows, f, want) {
			if taken[k] {
				continue
			}
			taken[k] = true
			for i, part := range []string{epMeasureP50, epMeasureP90} {
				sk := chstore.MLSeriesKey(k.svc, tracePart(k.route, part))
				if out[sk] == nil {
					out[sk] = map[int64]float64{}
				}
				out[sk][bucket] = q[i]
			}
		}
	}
	return nil
}

// ── 1 dk örnekler ────────────────────────────────────────────────────────

// epFamMin — bir (dakika, aile) çiftinin alınmış satırları. Haritalar
// yazıldıktan sonra yalnız okunur (güncelleme yeni harita atar).
type epFamMin struct {
	cnt   map[epKey]float64
	err   map[epKey]float64
	q     map[epKey][2]float64
	errOK bool
	rtOK  bool
}

// epPick — uç noktanın o dakikadaki noktası (ilk aile).
type epPick struct {
	count, err  float64
	q           [2]float64
	errOK, rtOK bool
	fam         int
}

// epWindow — karar penceresi: dakika başı → uç nokta → nokta.
type epWindow struct {
	end   int64
	min   map[int64]map[epKey]epPick
	blind bool // en az bir (dakika, aile) alınamadı
}

// epMinutes — pencerenin [end − 5 dk, end) dakikaları, yalnız want uç
// noktaları için. Saklı dakika yeniden sorulmaz; eksik parça (count / err /
// yüzdelik) için 1 anlık sorgu, geçiş başına aile × sorgu türü başına en çok
// BİR başarısız sorgu. count alınamayan ailede o dakika durulur (alt aileler
// güvenilmez); err / yüzdelik düşerse yalnız o ölçü ölçülmedi.
func (d *TraceRED) epMinutes(ctx context.Context, deps Deps, fams []impFam, want map[epKey]bool, end, now time.Time) (epWindow, error) {
	deps.meter = nil
	first := end.Unix() - impactWindow*impactStepSec
	w := epWindow{end: end.Unix(), min: map[int64]map[epKey]epPick{}}
	d.mu.Lock()
	if d.epMin == nil {
		d.epMin = map[int64]map[int]epFamMin{}
	}
	for m := range d.epMin {
		if m < first {
			delete(d.epMin, m)
		}
	}
	have := map[int64]map[int]epFamMin{}
	for m := first; m < end.Unix(); m += impactStepSec {
		have[m] = map[int]epFamMin{}
		for fi, e := range d.epMin[m] {
			have[m][fi] = e
		}
	}
	d.mu.Unlock()
	failed := map[string]bool{}
	var ferr error
	fail := func(kind string, f impFam, err error) {
		failed[kind+f.pin.ID] = true
		if err != nil && ferr == nil {
			ferr = fmt.Errorf("vm %s/ep_%s_1m: %w", f.pin.ID, kind, err)
		}
	}
	for m := first; m < end.Unix(); m += impactStepSec {
		at := time.Unix(m+impactStepSec, 0)
		for _, f := range fams {
			cq, eq, qq := epQueries(f, impactStepSec)
			e, ok := have[m][f.idx]
			if !ok {
				if failed["count"+f.pin.ID] {
					w.blind = true
					break
				}
				rows, skip, err := vmInstant(ctx, deps, epBackoff+f.pin.ID, cq, at, now)
				if err != nil || skip {
					fail("count", f, err)
					w.blind = true
					break
				}
				e = epFamMin{cnt: epSums(rows, f, want)}
			}
			if !e.errOK && !failed["err"+f.pin.ID] {
				if rows, skip, err := vmInstant(ctx, deps, epBackoff+f.pin.ID, eq, at, now); err == nil && !skip {
					e.err, e.errOK = epSums(rows, f, want), true
				} else {
					fail("err", f, err)
				}
			}
			if !e.rtOK && !failed["rt"+f.pin.ID] {
				if rows, skip, err := vmInstant(ctx, deps, epRTBackoff+f.pin.ID, qq, at, now); err == nil && !skip {
					e.q, e.rtOK = epQuants(rows, f, want), true
				} else {
					fail("rt", f, err)
				}
			}
			if !e.errOK || !e.rtOK {
				w.blind = true
			}
			have[m][f.idx] = e
		}
		w.min[m] = epPickFirst(fams, have[m])
	}
	d.mu.Lock()
	for m, byF := range have {
		if len(byF) > 0 {
			d.epMin[m] = byF
		}
	}
	d.mu.Unlock()
	return w, ferr
}

// epPickFirst — SAF: "ilk aile kazanır" (pin sırası). Alınamayan ilk ailede
// durulur. Uç nokta, isteği olan ilk aileden okunur.
func epPickFirst(fams []impFam, got map[int]epFamMin) map[epKey]epPick {
	out := map[epKey]epPick{}
	for _, f := range fams {
		e, ok := got[f.idx]
		if !ok {
			break
		}
		for k, c := range e.cnt {
			if _, taken := out[k]; taken || c <= 0 {
				continue
			}
			p := epPick{count: c, fam: f.idx, errOK: e.errOK}
			p.err = e.err[k]
			if q, ok := e.q[k]; ok && e.rtOK {
				p.q, p.rtOK = q, true
			}
			out[k] = p
		}
	}
	return out
}

// epSamples — SAF: uç noktanın pencere örnekleri, ölçü başına (en eski → en
// yeni). Hata oranı ölçülmüş = istek > 0 ve err alındı; süre ölçülmüş =
// yüzdelik alındı. fam = en yeni ölçülen dakikanın ailesi (−1 = yok).
func epSamples(w epWindow, k epKey) (s [3][]impactSample, fam int) {
	fam = -1
	for i := range s {
		s[i] = make([]impactSample, impactWindow)
	}
	for i := 0; i < impactWindow; i++ {
		p, ok := w.min[w.end-int64(impactWindow-i)*impactStepSec][k]
		if !ok {
			continue
		}
		fam = p.fam
		for j := range s {
			s[j][i].Load = p.count
		}
		if p.errOK {
			s[0][i] = impactSample{Value: p.err / p.count, Load: p.count, OK: true}
		}
		if p.rtOK {
			s[1][i] = impactSample{Value: p.q[0], Load: p.count, OK: true}
			s[2][i] = impactSample{Value: p.q[1], Load: p.count, OK: true}
		}
	}
	return s, fam
}

// ── karar ────────────────────────────────────────────────────────────────

// epVerdict — uç noktanın etki kararı.
type epVerdict struct {
	M      [3]impactVerdict
	Have   [3]bool // ölçünün tabanı yeterli (yargılandı)
	Fired  [3]bool
	Fire   bool
	Emit   bool
	Lead   int // olayın ölçüsü (epMeasures sırası); −1 = yok
	Reason string
}

// epDecide — SAF: impactDecide'ı ölçü başına koşar (taban = mevsimsel
// medyan). open = açık koşu var (uzatma). Olayın ölçüsü: ateşleyenler önce;
// ateşleyenler arasında hata oranı önce, sonra oran (gözlenen / taban) büyük
// olan; uzatmada ihlali çok, sonra tepe oranı büyük olan.
func epDecide(s [3][]impactSample, b epBase, minSamples int, open bool) epVerdict {
	v := epVerdict{Lead: -1}
	for i := range v.M {
		v.M[i].Onset = -1
	}
	if !b.ready(minSamples) {
		v.Reason = "eksik veri: taban örneği az"
		return v
	}
	var cand []int
	for i, m := range epMeasures {
		if b.N[i] < minSamples {
			continue
		}
		v.Have[i] = true
		v.M[i] = impactDecide(m.kind, s[i], b.Median[i])
		v.Fired[i] = v.M[i].Fire
		v.Fire = v.Fire || v.Fired[i]
		if impactEmit(v.M[i], open) {
			cand = append(cand, i)
		}
	}
	if len(cand) == 0 {
		v.Reason = "etki kuralı ateşlemiyor"
		return v
	}
	better := func(i, j int) bool {
		if v.Fired[i] != v.Fired[j] {
			return v.Fired[i]
		}
		if v.Fire {
			if (i == 0) != (j == 0) {
				return i == 0
			}
			return rtRatio(v.M[i].Observed, b.Median[i]) > rtRatio(v.M[j].Observed, b.Median[j])
		}
		if v.M[i].Violations != v.M[j].Violations {
			return v.M[i].Violations > v.M[j].Violations
		}
		return rtRatio(v.M[i].Peak, b.Median[i]) > rtRatio(v.M[j].Peak, b.Median[j])
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

// epEvent — SAF: kararın olayı (svcImpactEvent / rtEvent deseni). Gözlenen /
// taban = olayın ölçüsünün en yeni ölçülen dakikası / mevsimsel medyanı.
// Kanıt: kural, ölçü, eşik, ihlal, dakika dakika oran / yüzdelik (null =
// ölçülmedi) ve yük; ImpactRuleFacts aynı alanları okur.
func epEvent(k epKey, s [3][]impactSample, v epVerdict, b epBase, end time.Time, openStart int64, cfg chstore.MLConfig, class string) chstore.MLEvent {
	n := impactWindow
	first := end.Unix() - int64(n)*impactStepSec
	lead := epMeasures[v.Lead]
	lv := v.M[v.Lead]
	vals := [3][]any{make([]any, n), make([]any, n), make([]any, n)}
	loads := make([]float64, n)
	onset := -1
	fired := []string{}
	for i, m := range epMeasures {
		for j, x := range s[i] {
			if j < n {
				loads[j] = math.Max(loads[j], x.Load)
				if x.OK {
					vals[i][j] = x.Value
				}
			}
		}
		if !v.Have[i] {
			continue
		}
		if v.Fired[i] {
			fired = append(fired, m.part)
		}
		if mv := v.M[i]; mv.Violations > 0 && (v.Fired[i] || !v.Fire) && (onset < 0 || mv.Onset < onset) {
			onset = mv.Onset
		}
	}
	unit := "ratio"
	if v.Lead > 0 {
		unit = "ms"
	}
	z := (lv.Observed - b.Median[v.Lead]) / effMAD(b.Median[v.Lead], b.MAD[v.Lead])
	ev := map[string]any{"source": "vm", "route": k.route, "dayClass": class, "baselineDays": cfg.BaselineDays, "unit": unit,
		"measure": lead.part, "mad": b.MAD[v.Lead], "z": z, "baseRate": b.Rate, "rule": "impact", "fired": v.Fire,
		"firedMeasures": fired, "threshold": lv.Threshold, "violations": lv.Violations, "window": impactWindow,
		"minViolations": impactViolations, "minLoad": impactRules[lead.kind].MinLoad, "sampleSec": impactStepSec,
		"minuteFrom": first, "ratios": vals[0], "p50s": vals[1], "p90s": vals[2], "loads": loads}
	raw, _ := json.Marshal(ev)
	return chstore.MLEvent{
		ID: chstore.MLEventID(detEndpointImpact, k.svc, k.route), Detector: detEndpointImpact, Signal: chstore.MLSignalTrace, Stage: chstore.MLStageShadow,
		Service: k.svc, Code: k.route, StartedAt: impactRunStart(end, n, onset, openStart), LastSeen: end.UnixNano(), WindowSec: uint32(cfg.BucketSec),
		Baseline: b.Median[v.Lead], Observed: lv.Observed, Score: svcErrScore(z, cfg.CriticalZ), Direction: "up", Evidence: string(raw),
	}
}

// ── tik ──────────────────────────────────────────────────────────────────

// epCandidates — SAF: taban özetinden adaylar (tabanı hazır; yük kapısı,
// epTopK, epGlobalCap; açık koşu önce).
func epCandidates(bases map[epKey]epBase, minSamples int, open func(epKey) bool) []epKey {
	rates := map[epKey]float64{}
	for k, b := range bases {
		if b.ready(minSamples) {
			rates[k] = b.Rate
		}
	}
	return epSelect(rates, impactRules[impactFailureRate].MinLoad, epTopK, epGlobalCap, open)
}

// epTick — TraceRED tikinin uç nokta adımı: adaylar (yoklama için saklanır)
// → 1 dk pencere → olaylar. Hatalar YALITILIR (not + log). famOf: aile
// sırası → kanıt alanları (redFamily, etiketler).
func (d *TraceRED) epTick(ctx context.Context, deps Deps, fams []impFam, base map[string][]float64, famOf map[int]map[string]any,
	end time.Time, cfg chstore.MLConfig, class string, now time.Time, baseErr error) []chstore.MLEvent {
	bases := epBases(base)
	open := func(k epKey) bool { return deps.extends(detEndpointImpact, k.svc, k.route) }
	cands := epCandidates(bases, cfg.MinBaselineSamples, open)
	keep := make(map[epKey]epBase, len(cands))
	for _, k := range cands {
		keep[k] = bases[k]
	}
	d.mu.Lock()
	d.epCands, d.epCandsAt = keep, now
	d.mu.Unlock()
	if baseErr != nil {
		log.Printf("[ml] endpoint_impact taban sorgusu düştü (bu kovanın yanıt süresi taban örneği yok): %v", baseErr)
	}
	if len(cands) == 0 {
		d.setEPNote(fmt.Sprintf("ölçülemedi: taban oluşuyor (yük kapısını geçen ve en az %d taban örneği olan uç nokta yok)", cfg.MinBaselineSamples))
		return nil
	}
	w, err := d.epMinutes(ctx, deps, fams, epWant(keep), end, now)
	var out []chstore.MLEvent
	for _, k := range cands {
		isOpen := open(k)
		s, fam := epSamples(w, k)
		v := epDecide(s, keep[k], cfg.MinBaselineSamples, isOpen)
		if !v.Emit || !(isOpen || d.dayCap(detEndpointImpact, cfg, now)) {
			continue
		}
		ev := epEvent(k, s, v, keep[k], end, deps.startedAt(detEndpointImpact, k.svc, k.route), cfg, class)
		if src := famOf[fam]; src != nil {
			ev.Evidence = mergeEvidence(ev.Evidence, src)
		}
		for _, f := range fams {
			if f.idx == fam {
				ev.Evidence = mergeEvidence(ev.Evidence, map[string]any{"routeLabel": f.fl.route})
			}
		}
		out = append(out, ev)
	}
	note := ""
	switch {
	case err != nil:
		note = "kısmen ölçülüyor: " + truncRunes(err.Error(), 160)
		log.Printf("[ml] endpoint_impact 1 dk sorgusu düştü (ölçülemeyen ailede / ölçüde dedektör bu tik sessiz): %v", err)
	case w.blind:
		note = "kısmen ölçülüyor: VM sorgusu beklemede (backoff)"
	}
	d.setEPNote(note)
	return out
}

// dedupeEndpointOps — SAF: aynı tikte endpoint_impact olayı yazılan uç nokta
// (servis × route) için op_error_rate olayı düşer: iki dedektör birlikte
// açıkken bir uç nokta servisin Problem'ine iki üye olarak girmesin. Sıra
// korunur; endpoint_impact olayı yoksa liste aynen döner.
func dedupeEndpointOps(evs []chstore.MLEvent) []chstore.MLEvent {
	hit := map[epKey]bool{}
	for _, e := range evs {
		if e.Detector == detEndpointImpact {
			hit[epKey{e.Service, e.Code}] = true
		}
	}
	if len(hit) == 0 {
		return evs
	}
	out := evs[:0]
	for _, e := range evs {
		if e.Detector == detOpErrorRate && hit[epKey{e.Service, e.Code}] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// epWant — SAF: aday kümesi.
func epWant(c map[epKey]epBase) map[epKey]bool {
	out := make(map[epKey]bool, len(c))
	for k := range c {
		out[k] = true
	}
	return out
}

func (d *TraceRED) setEPNote(note string) {
	d.mu.Lock()
	d.epNote = note
	d.mu.Unlock()
}

// clearEP — dedektör kapalı: saklı dakikalar, adaylar ve not silinir.
func (d *TraceRED) clearEP() {
	d.mu.Lock()
	d.epMin, d.epCands, d.epNote = nil, nil, ""
	d.mu.Unlock()
}

// epRates — SAF: bu kovanın route count serilerinden uç nokta → istek/sn.
func epRates(series map[string]map[int64]float64, bucket int64) map[epKey]float64 {
	out := map[epKey]float64{}
	for k, byB := range series {
		svc, part, _ := strings.Cut(k, "\x00")
		route, m := splitTracePart(part)
		if m != "count" || route == svcRoute {
			continue
		}
		if v, ok := byB[bucket]; ok {
			out[epKey{svc, route}] = v
		}
	}
	return out
}

// ── dakikalık yoklama (impact_probe.go) ──────────────────────────────────

// epProbe — TraceRED'in uç nokta etki yoklaması (kendi anahtarıyla sorulur).
type epProbe struct{ d *TraceRED }

func (p epProbe) probeOn(cfg chstore.MLConfig) bool { return cfg.DetectorOn(detEndpointImpact) }

// probeImpact — kuralı ŞU AN ateşleyen uç noktalar. Adaylar ve taban son
// zamanlı tikten (CH okuması yok; bayatsa / yoksa sessiz ve SORGUSUZ);
// dakikalar epMinutes'ten (yalnız yeni dakika sorulur).
func (p epProbe) probeImpact(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]impactCandidate, error) {
	if deps.VM == nil || !deps.VM.Configured() {
		return nil, nil
	}
	d := p.d
	d.mu.Lock()
	cands, at := d.epCands, d.epCandsAt
	d.mu.Unlock()
	if len(cands) == 0 || now.Sub(at) > impactBaseMaxTicks*time.Duration(cfg.TickSec)*time.Second {
		return nil, nil
	}
	fams := d.cachedFamilies()
	if len(fams) == 0 {
		return nil, nil
	}
	w, err := d.epMinutes(ctx, deps, fams, epWant(cands), svcImpactEnd(now, cfg.IngestLagSec), now)
	if err != nil {
		return nil, err
	}
	keys := make([]epKey, 0, len(cands))
	for k := range cands {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].svc != keys[j].svc {
			return keys[i].svc < keys[j].svc
		}
		return keys[i].route < keys[j].route
	})
	var out []impactCandidate
	for _, k := range keys {
		s, _ := epSamples(w, k)
		if epDecide(s, cands[k], cfg.MinBaselineSamples, false).Fire {
			out = append(out, impactCandidate{BaseID: chstore.MLEventID(detEndpointImpact, k.svc, k.route), Service: k.svc})
		}
	}
	return out, nil
}

// ── Problem cümlesi ──────────────────────────────────────────────────────

// epSentence — SAF: Problem detayındaki cümle (manşet; tek tanım chstore):
// "checkout · POST /pay: hata oranı normalde %0,2, şimdi %14 (70 kat)."
func epSentence(e chstore.MLEvent) string {
	m := epMeasureOfEvent(e)
	if m.key == "" {
		return chstore.MLEndpointErrRateSentence(e.Service, e.Code, e.Observed, e.Baseline)
	}
	return chstore.MLEndpointResponseTimeSentence(e.Service, e.Code, m.key, e.Observed, e.Baseline)
}

// epHead — SAF: "/anomalies" kart cümlesi (Problem cümlesinin noktasız hali).
func epHead(in CardInput) string {
	m := epMeasures[epMeasureOfPart(in.Part)]
	var s string
	if m.key == "" {
		s = chstore.MLEndpointErrRateSentence(in.Service, in.Code, in.Observed, in.Baseline)
	} else {
		s = chstore.MLEndpointResponseTimeSentence(in.Service, in.Code, m.key, in.Observed, in.Baseline)
	}
	return strings.TrimSuffix(s, ".")
}
