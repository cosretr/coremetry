package ml

// publish_gate.go — v0.10.2241, anomali kartı A1 (docs/ml/anomaly-card-audit.md
// §9 gates[], §10 A1, §12 K2/K9/K10): dedektörden BAĞIMSIZ tek yayın kapısı
// katmanı. Doğrulayıcıdan geçen her sözleşme kaydı yazımdan önce burada
// değerlendirilir; sonuç YALNIZ kaydın gates[]'ine ve gölge sayacına yazılır.
// GÖLGE: hiçbir şey gizlenmez, sınıf/publish_state değişmez, bildirim, Problem
// terfisi ve kart bugünkü gibi (A2 bunları yürürlüğe koyar). v0.10.2343: A2
// uygulama yolu publish_gate_enforce.go'da, KAPALI (mlPublishGatesEnforce).
//
// Kapılar yalnız KAYDI okur (ek VM/CH/ES sorgusu yok); kayıtta gereken veri
// yoksa satır NotEvaluable + neden yazılır (Passed=true: değerlendirilemeyen
// kapı olayı tutmaz).

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Kapı adları — gates[].name, gölge raporu ve testler bu adlarla konuşur.
const (
	GateSeriesConsistency = "series_consistency"   // çizilen seri = test edilen seri
	GateVisibleDeviation  = "visible_deviation"    // olay penceresinde bant dışı seri kovası
	GateMinVolume         = "min_volume"           // gecikme: pencerede asgari çağrı
	GateMinAbsDiff        = "min_abs_diff"         // gecikme: asgari mutlak fark (ms)
	GateEvidence          = "independent_evidence" // bağımsız kanıt ≥ 2 (K10)
	GateImpact            = "service_impact"       // hata oranı / gecikme bant dışında
)

// Eşikler — K2: kod sabiti, ayar değil (operatör kararı 2026-11-15).
const (
	gateLatencyMinRunMin    = 3   // art arda ≥ 3 seri kovası sapma (gecikme; 1 dk seride 3 dk)
	gateLatencyMinCalls     = 100 // olay penceresinde ≥ 100 çağrı
	gateLatencyMinAbsDiffMs = 25  // |tepe − beklenen| ≥ 25 ms
	gateMinIndependent      = 2   // bağımsız kanıt (K10)
)

// dropGates — kalırsa olay A2'de yayımlanmaz; evidence/impact kalırsa sınıf düşer.
var dropGates = map[string]bool{GateSeriesConsistency: true, GateVisibleDeviation: true, GateMinVolume: true, GateMinAbsDiff: true}

// K9: kaynağı seriyle aynı olsa da bağımsız sayılan kanıt türleri.
var independentKinds = map[string]bool{"rollout": true, "confirm": true, "exception": true, "endpoint": true}

// seriesMeasure — SAF: serinin ölçü ailesi (yalnız kayıt alanlarından).
// latency: ms ya da yavaş istek oranı ya da op_latency'den türetilmiş seri;
// error: hata oranı; volume: telemetri hacmi (v0.10.2242, Dilim B);
// "" diğer (lag, metrik, sayım).
func seriesMeasure(s RecordSeries) string {
	switch {
	case s.Aggregation == volAggRatio || s.Aggregation == volAggSpans:
		return "volume"
	case s.Unit == "ms" || s.Aggregation == "slow_ratio" || s.Source == "ml:op_latency":
		return "latency"
	case s.Aggregation == "err_ratio":
		return "error"
	}
	return ""
}

// EvaluateGates — SAF: kaydın yayın kapıları (gates[] satırları, sabit sıra).
// Gecikme serisinde görünür sapmaya hacim + mutlak fark satırları eklenir.
func EvaluateGates(r EventRecord) []RecordGate {
	occ := r.Series.Kind == SeriesKindOccurrence
	m := seriesMeasure(r.Series)
	out := []RecordGate{seriesConsistencyGate(r, occ), visibleDeviationGate(r, occ, m == "latency")}
	if m == "latency" {
		out = append(out, minVolumeGate(r), minAbsDiffGate(r))
	}
	return append(out, evidenceGate(r), impactGate(r, occ, m))
}

func notEvaluable(name, reason string) RecordGate {
	return RecordGate{Name: name, Passed: true, NotEvaluable: true, Reason: "değerlendirilemedi: " + reason}
}

// seriesConsistencyGate — çizilen seri test edilen seri mi: tanım tam,
// snapshot dolu, snapshot toplaması = seri toplaması, bant sütunları tam.
// Value = tutarsızlık sayısı, eşik 0.
func seriesConsistencyGate(r EventRecord, occ bool) RecordGate {
	if occ {
		return notEvaluable(GateSeriesConsistency, "oluşum kaydı; sayısal seri yok")
	}
	var bad []string
	s, sn := r.Series, r.Snapshot
	if s.Source == "" || s.Query == "" || s.Aggregation == "" || s.Unit == "" || s.StepSec == 0 {
		bad = append(bad, "seri tanımı eksik")
	}
	if len(sn.V) == 0 || sn.StepSec == 0 {
		bad = append(bad, "snapshot yok")
	}
	if sn.Aggregation != s.Aggregation {
		bad = append(bad, fmt.Sprintf("snapshot toplaması %q ≠ seri %q", sn.Aggregation, s.Aggregation))
	}
	if len(sn.Lo) != len(sn.V) || len(sn.Hi) != len(sn.V) {
		bad = append(bad, "snapshot bandı eksik")
	}
	g := RecordGate{Name: GateSeriesConsistency, Passed: len(bad) == 0, Value: float64(len(bad)), Threshold: 0}
	if len(bad) == 0 {
		g.Reason = fmt.Sprintf("snapshot = %s %s (%s)", s.Source, s.Aggregation, s.Unit)
	} else {
		g.Reason = strings.Join(bad, "; ")
	}
	return g
}

// eventWindow — SAF: olay penceresinin snapshot indeksleri [onset, bitiş|son].
func eventWindow(r EventRecord) (from, to int, ok bool) {
	sn := r.Snapshot
	n := len(sn.V)
	if n == 0 || sn.StepSec == 0 || r.OnsetAt <= 0 {
		return 0, 0, false
	}
	idx := func(ns int64) int { return int(math.Floor(float64(ns/1e9-sn.T0) / float64(sn.StepSec))) }
	from, to = max(idx(r.OnsetAt), 0), n-1
	if r.EndedAt > 0 {
		to = min(idx(r.EndedAt), n-1)
	}
	return from, to, from <= to
}

// outsideRunMin — SAF: pencerede yönünde bant dışında kalan en uzun art arda
// koşu, dakika (nokta × adım / 60). Bant yoksa -1.
func outsideRunMin(r EventRecord, from, to int, dir string) float64 {
	sn := r.Snapshot
	if len(sn.Lo) != len(sn.V) || len(sn.Hi) != len(sn.V) {
		return -1
	}
	run, best := 0, 0
	for i := from; i <= to; i++ {
		if (dir == "down" && sn.V[i] < sn.Lo[i]) || (dir != "down" && sn.V[i] > sn.Hi[i]) {
			run++
			best = max(best, run)
		} else {
			run = 0
		}
	}
	return float64(best) * float64(sn.StepSec) / 60
}

// outsideRunBuckets — SAF (v0.10.2247, Dilim G): pencerede yönünde bant
// dışında kalan en uzun art arda koşu, SERİ KOVASI cinsinden. Snapshot 1 dk
// ızgarada, seri adımı daha büyükse (5 dk kova 1 dk'ya basamaklanmış ya da
// kova pencereli kayan değer) noktalar hizalı seri kovasına (unix sn / kova)
// toplanır; kovadaki herhangi bir nokta bant dışındaysa kova bant dışı. Böylece
// tek değerlendirme kovası "art arda 5 dakika" sayılmaz. Bant yoksa -1.
// bucketSec = max(seri adımı, snapshot adımı).
func outsideRunBuckets(r EventRecord, from, to int, dir string) (run int, bucketSec int64) {
	sn := r.Snapshot
	if len(sn.Lo) != len(sn.V) || len(sn.Hi) != len(sn.V) {
		return -1, 0
	}
	step := int64(sn.StepSec)
	bucketSec = max(int64(r.Series.StepSec), step)
	var keys []int64
	outside := map[int64]bool{}
	for i := from; i <= to; i++ {
		k := (sn.T0 + int64(i)*step) / bucketSec
		if len(keys) == 0 || keys[len(keys)-1] != k {
			keys = append(keys, k)
		}
		if (dir == "down" && sn.V[i] < sn.Lo[i]) || (dir != "down" && sn.V[i] > sn.Hi[i]) {
			outside[k] = true
		}
	}
	cur := 0
	for j, k := range keys {
		switch {
		case !outside[k]:
			cur = 0
		case j > 0 && outside[keys[j-1]] && keys[j-1] == k-1:
			cur++
		default:
			cur = 1
		}
		run = max(run, cur)
	}
	return run, bucketSec
}

// visibleDeviationGate — snapshot penceresinde değer beklenen bandın dışına
// çıkıyor mu. v0.10.2247 (Dilim G): sayım SERİ KOVASI cinsinden (dedektörün
// yargıladığı nokta); gecikme serisinde art arda ≥ 3 kova (1 dk seride 3 dk),
// diğerlerinde ≥ 1 kova. Önceden 5 dk kovanın 1 dk ızgaradaki basamağı art
// arda 5 dk sayılıyor, tek kovalık sapma gecikme kuralını geçiyordu.
func visibleDeviationGate(r EventRecord, occ, latency bool) RecordGate {
	if occ {
		return notEvaluable(GateVisibleDeviation, "oluşum kaydı; bant yok")
	}
	from, to, ok := eventWindow(r)
	if !ok {
		return notEvaluable(GateVisibleDeviation, "olay penceresi snapshot dışında")
	}
	run, bucketSec := outsideRunBuckets(r, from, to, r.Direction)
	if run < 0 {
		return notEvaluable(GateVisibleDeviation, "snapshot bandı yok")
	}
	th := 1
	if latency {
		th = gateLatencyMinRunMin
	}
	g := RecordGate{Name: GateVisibleDeviation, Passed: run >= th, Value: float64(run), Threshold: float64(th)}
	g.Reason = fmt.Sprintf("bant dışında art arda %d kova (%d sn kova, %s dk)", run, bucketSec, fmtNum(float64(int64(run)*bucketSec)/60))
	if !g.Passed {
		g.Reason += fmt.Sprintf(" < %d kova", th)
	}
	return g
}

// minVolumeGate — gecikme serisinde olay penceresinde ≥ 100 çağrı (record.volume).
func minVolumeGate(r EventRecord) RecordGate {
	if r.Volume == nil {
		why := r.VolumeReason
		if why == "" {
			why = "hacim yok"
		}
		return notEvaluable(GateMinVolume, why)
	}
	v := *r.Volume
	g := RecordGate{Name: GateMinVolume, Passed: v >= gateLatencyMinCalls, Value: v, Threshold: gateLatencyMinCalls}
	g.Reason = fmt.Sprintf("olay penceresinde %s çağrı", fmtNum(v))
	if !g.Passed {
		g.Reason += fmt.Sprintf(" < %d", gateLatencyMinCalls)
	}
	return g
}

// minAbsDiffGate — gecikme serisinde |tepe − beklenen| ≥ 25 ms.
func minAbsDiffGate(r EventRecord) RecordGate {
	if r.Series.Unit != "ms" {
		return notEvaluable(GateMinAbsDiff, fmt.Sprintf("seri birimi %s; ms farkı tanımsız", r.Series.Unit))
	}
	d := math.Abs(r.PeakValue - r.ExpectedValue)
	g := RecordGate{Name: GateMinAbsDiff, Passed: d >= gateLatencyMinAbsDiffMs, Value: d, Threshold: gateLatencyMinAbsDiffMs}
	g.Reason = fmt.Sprintf("tepe %s ms, beklenen %s ms, fark %s ms", fmtNum(r.PeakValue), fmtNum(r.ExpectedValue), fmtNum(d))
	if !g.Passed {
		g.Reason += fmt.Sprintf(" < %d", gateLatencyMinAbsDiffMs)
	}
	return g
}

// independentEvidence — SAF: bağımsız kanıt sayısı (K9). Sayılmaz: bağımsız
// işaretli değil; başlığın tekrarı; kaynağı seriyle aynı ve türü K9
// listesinde değil (aynı kaynak + ölçü); türetilmiş serinin (`ml:<dedektör>`)
// onay satırı (kendi girdisinden bağımsız değil, satır dedektörü taşımaz).
func independentEvidence(r EventRecord) int {
	t := normText(r.Title)
	derived := strings.HasPrefix(r.Series.Source, "ml:")
	n := 0
	for _, e := range r.Evidence {
		switch {
		case !e.Independent:
		case t != "" && normText(e.Summary) == t:
		case e.Source == r.Series.Source && !independentKinds[e.Kind]:
		case derived && e.Kind == "confirm":
		default:
			n++
		}
	}
	return n
}

// evidenceGate — bağımsız kanıt ≥ 2; kalırsa sınıf info, Problem adayı değil (K10).
func evidenceGate(r EventRecord) RecordGate {
	n := independentEvidence(r)
	g := RecordGate{Name: GateEvidence, Passed: n >= gateMinIndependent, Value: float64(n), Threshold: gateMinIndependent}
	g.Reason = fmt.Sprintf("bağımsız kanıt %d / %d satır", n, len(r.Evidence))
	if !g.Passed {
		g.Reason += " — sınıf info, Problem adayı değil"
	}
	return g
}

// impactGate — servis etkisi: kayıt yalnız kendi serisini taşır. Seri hata
// oranı ya da gecikmeyse etki = olay penceresinde yukarı yönde bant dışı
// (dakika); bant içinde ya da yalnız aşağı yönde → servis etkisi gösterilmiyor,
// service_anomaly olamaz. Diğer ölçülerde hata oranı / gecikme kayıtta yok →
// değerlendirilemez (B/C hacim kaydı ikisini taşıyınca dolar).
func impactGate(r EventRecord, occ bool, measure string) RecordGate {
	if measure == "volume" && !occ {
		return volumeImpactGate(r)
	}
	if occ || measure == "" {
		return notEvaluable(GateImpact, "kayıtta hata oranı / gecikme serisi yok")
	}
	from, to, ok := eventWindow(r)
	if !ok {
		return notEvaluable(GateImpact, "olay penceresi snapshot dışında")
	}
	run := outsideRunMin(r, from, to, "up")
	if run < 0 {
		return notEvaluable(GateImpact, "snapshot bandı yok")
	}
	word := map[string]string{"latency": "gecikme", "error": "hata oranı"}[measure]
	g := RecordGate{Name: GateImpact, Passed: run > 0, Value: run, Threshold: float64(r.Snapshot.StepSec) / 60}
	if g.Passed {
		g.Reason = fmt.Sprintf("%s üst bandın üstünde %s dk", word, fmtNum(run))
	} else {
		g.Reason = word + " üst bandı aşmıyor — servis etkisi yok, service_anomaly olamaz"
	}
	return g
}

// contextImpact — SAF (v0.10.2242, Dilim B): yan serilerde (hata oranı,
// gecikme) olay penceresinde yukarı yönde üst bandın üstündeki en uzun art
// arda koşu (dk) ve değerlendirilebilen yan seri ölçüleri.
func contextImpact(r EventRecord) (run float64, evaluated []string) {
	from, to, ok := eventWindow(r)
	if !ok {
		return 0, nil
	}
	for _, c := range r.Context {
		if c.Measure != ctxErrorRate && c.Measure != ctxLatency {
			continue
		}
		cr := EventRecord{Snapshot: c.Snapshot}
		if len(c.Snapshot.V) != len(r.Snapshot.V) {
			continue
		}
		x := outsideRunMin(cr, from, to, "up")
		if x < 0 {
			continue
		}
		evaluated = append(evaluated, c.Measure)
		run = math.Max(run, x)
	}
	return run, evaluated
}

// volumeImpactGate — hacim kaydında servis etkisi YAN serilerden: hata oranı
// ya da gecikme üst bandı aşıyorsa etki var (Passed). İkisi de değerlendirilip
// bant içindeyse etki yok → sınıf telemetry_volume. Biri eksikse ve etki
// görünmüyorsa değerlendirilemez.
func volumeImpactGate(r EventRecord) RecordGate {
	run, ev := contextImpact(r)
	g := RecordGate{Name: GateImpact, Passed: run > 0, Value: run, Threshold: float64(r.Snapshot.StepSec) / 60}
	switch {
	case run > 0:
		g.Reason = fmt.Sprintf("hacimle birlikte hata oranı / gecikme üst bandın üstünde %s dk", fmtNum(run))
	case len(ev) < 2:
		return notEvaluable(GateImpact, "kayıtta hata oranı / gecikme serisi yok (hacim kaydının yan serisi eksik)")
	default:
		g.Reason = "hacim değişti; hata oranı ve gecikme bant içinde — servis etkisi yok, telemetry_volume"
	}
	return g
}

// ShadowHeld — SAF (v0.10.2243, Dilim C "Elenenler"): olayın kaydındaki
// kapı satırlarına göre A2'nin yayımlamayacağı (gölge kararı drop) olay mı.
// Kayıt yok / çözülemiyor / invalid / kapı satırı yok → false. Yalnız okur:
// liste, bildirim ve Problem bu karara bakmaz (A1 gölge).
func ShadowHeld(ev chstore.MLEvent) bool {
	if ev.Record == "" || ev.PublishState == PublishInvalid {
		return false
	}
	if ev.PublishState == PublishHeld { // v0.10.2343 (A2 açık): yazılmış karar
		return true
	}
	var rec EventRecord
	if json.Unmarshal([]byte(ev.Record), &rec) != nil || len(rec.Gates) == 0 {
		return false
	}
	return gateVerdict(rec, rec.Gates).Drop
}

// gateOutcome — bir olayın gölge sonucu (gölge raporunun birimi).
type gateOutcome struct {
	Evaluated    bool
	Invalid      bool
	Drop         bool
	WouldClass   string   // A2'de düşeceği sınıf; "" = değişmez
	Failed       []string // kalan kapılar
	NotEvaluable []string
}

// gateVerdict — SAF: kapı satırlarından A2'nin yapacağı: drop kapısı kalırsa
// yayımlanmaz; kanıt ya da etki kalırsa service_anomaly → info.
func gateVerdict(r EventRecord, gates []RecordGate) gateOutcome {
	o := gateOutcome{Evaluated: true}
	demote := false
	// v0.10.2242 (Dilim B): hacim kaydı etki kapısı "etki yok" derse (ya da
	// değerlendirilemeyip kayıt zaten telemetry_volume ise — giriş span'i
	// olmayan servis) sınıf telemetry_volume; kanıt kapısı onu info'ya indirmez.
	volume := seriesMeasure(r.Series) == "volume" && r.Series.Kind != SeriesKindOccurrence
	telemetry := false
	for _, g := range gates {
		if volume && g.Name == GateImpact {
			telemetry = (!g.NotEvaluable && !g.Passed) || (g.NotEvaluable && r.Class == ClassTelemetryVolume)
		}
	}
	for _, g := range gates {
		switch {
		case g.NotEvaluable:
			o.NotEvaluable = append(o.NotEvaluable, g.Name)
		case !g.Passed:
			o.Failed = append(o.Failed, g.Name)
			if dropGates[g.Name] {
				o.Drop = true
			} else {
				demote = true
			}
		}
	}
	switch {
	case o.Drop:
	case telemetry:
		o.WouldClass = ClassTelemetryVolume
	case demote && r.Class != ClassInfo:
		o.WouldClass = ClassInfo
	}
	return o
}

// Gölge raporu — olay id'si başına SON sonuç (açık olay her tik yeniden
// değerlendirilir; kapanış kaydı sonuncudur), 24 sa pencere, tavanlı bellek.
const (
	gateShadowWindow = 24 * time.Hour
	gateShadowCap    = 20000
)

type gateEntry struct {
	detector string
	o        gateOutcome
	at       int64 // unix sn
	// v0.10.2343 (K16 incelemesi) — kalıcı (olay pencerede kaldıkça) işaretler:
	// service olayın servisi; problem: olay gözlenirken servisinde açık bir
	// Problem vardı; escalated: olay bir ML Problem kararına (grup / servis
	// düzeyi "açardı" ya da canlı) girdi.
	service   string
	problem   bool
	escalated bool
}

// observeGate — bir olayın kapı sonucunu kaydeder (id yoksa yok sayar).
// Önceki tiklerin problem / escalated işaretleri korunur.
func (s *stats) observeGate(id, detector, service string, o gateOutcome) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gateShadow == nil {
		s.gateShadow = map[string]gateEntry{}
	}
	prev, ok := s.gateShadow[id]
	if !ok && len(s.gateShadow) >= gateShadowCap {
		return
	}
	s.gateShadow[id] = gateEntry{detector: detector, o: o, at: time.Now().Unix(), service: service, problem: prev.problem, escalated: prev.escalated}
}

// gateShadowHasDrop — gölgede tutulacak olay var mı (Problem okumasını yalnız
// gerekince yapmak için).
func (s *stats) gateShadowHasDrop() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.gateShadow {
		if e.o.Drop {
			return true
		}
	}
	return false
}

// markGateLinks — v0.10.2343: tutulacak olaylara ucuz, dürüst birleşim:
// escalated = bu tikte bir ML Problem kararına girdi; problemSvcs = açık
// Problem'i olan servisler. Oy / karar etiketi KULLANILMAZ (kalıcı kural).
func (s *stats) markGateLinks(escalated, problemSvcs map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.gateShadow {
		if !e.o.Drop {
			continue
		}
		ch := false
		if escalated[id] && !e.escalated {
			e.escalated, ch = true, true
		}
		if e.service != "" && problemSvcs[e.service] && !e.problem {
			e.problem, ch = true, true
		}
		if ch {
			s.gateShadow[id] = e
		}
	}
}

// escalatedIDs — SAF: bu tikte bir ML Problem kararına giren olay kimlikleri
// (bitmemiş "açardı"/canlı grup üyeleri + servis düzeyi "açardı" kararları).
func escalatedIDs(groups []decidedGroup, svc map[string]SvcShadow) map[string]bool {
	out := map[string]bool{}
	for _, g := range groups {
		if (g.D.WouldOpen || g.D.Live) && !g.D.Finished {
			for _, id := range g.G.Members {
				out[id] = true
			}
		}
	}
	for id, d := range svc {
		if d.WouldOpen {
			out[id] = true
		}
	}
	return out
}

// gateDropExamples — tutulacak olay örneği sayısı (dedektör başına).
const gateDropExamples = 3

// gateShadowReport — pencere dışını budar, dedektör başına raporu döner.
func (s *stats) gateShadowReport(now time.Time) map[string]chstore.MLGateShadow {
	cut := now.Add(-gateShadowWindow).Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.gateShadow {
		if e.at < cut {
			delete(s.gateShadow, id)
		}
	}
	return foldGateShadow(s.gateShadow)
}

// foldGateShadow — SAF: olay sonuçlarını dedektör başına sayar.
func foldGateShadow(es map[string]gateEntry) map[string]chstore.MLGateShadow {
	if len(es) == 0 {
		return nil
	}
	out := map[string]chstore.MLGateShadow{}
	type ex struct {
		id   string
		rank int
		at   int64
	}
	drops := map[string][]ex{}
	inc := func(m *map[string]int64, k string) {
		if *m == nil {
			*m = map[string]int64{}
		}
		(*m)[k]++
	}
	for id, e := range es {
		r := out[e.detector]
		switch o := e.o; {
		case !o.Evaluated:
			r.NotEvaluated++
			if o.Invalid {
				r.Invalid++
			}
		default:
			r.Evaluated++
			switch {
			case o.Drop:
				r.WouldDrop++
				rank := 0
				if e.problem {
					r.WouldDropProblem++
					rank++
				}
				if e.escalated {
					r.WouldDropEscalated++
					rank++
				}
				drops[e.detector] = append(drops[e.detector], ex{id, rank, e.at})
			case o.WouldClass == ClassInfo:
				r.WouldInfo++
			case o.WouldClass == ClassTelemetryVolume:
				r.WouldTelemetryVolume++
			default:
				r.WouldPublish++
			}
			for _, g := range o.Failed {
				inc(&r.FailedByGate, g)
			}
			for _, g := range o.NotEvaluable {
				inc(&r.NotEvaluableByGate, g)
			}
		}
		out[e.detector] = r
	}
	// örnekler: Problem'le çakışan / dönüşen önce, sonra en yeni, sonra id
	for det, xs := range drops {
		sort.Slice(xs, func(i, j int) bool {
			if xs[i].rank != xs[j].rank {
				return xs[i].rank > xs[j].rank
			}
			if xs[i].at != xs[j].at {
				return xs[i].at > xs[j].at
			}
			return xs[i].id < xs[j].id
		})
		r := out[det]
		for _, x := range xs[:min(len(xs), gateDropExamples)] {
			r.DropExamples = append(r.DropExamples, x.id)
		}
		out[det] = r
	}
	return out
}

// gateShadowLine — SAF: tik log satırı, toplamlar + kapı başına kalan.
func gateShadowLine(m map[string]chstore.MLGateShadow) string {
	var t chstore.MLGateShadow
	failed, notEval := map[string]int64{}, map[string]int64{}
	for _, r := range m {
		t.Evaluated += r.Evaluated
		t.WouldPublish += r.WouldPublish
		t.WouldDrop += r.WouldDrop
		t.WouldInfo += r.WouldInfo
		t.WouldTelemetryVolume += r.WouldTelemetryVolume
		t.NotEvaluated += r.NotEvaluated
		t.Invalid += r.Invalid
		t.WouldDropProblem += r.WouldDropProblem
		t.WouldDropEscalated += r.WouldDropEscalated
		for k, v := range r.FailedByGate {
			failed[k] += v
		}
		for k, v := range r.NotEvaluableByGate {
			notEval[k] += v
		}
	}
	return fmt.Sprintf("[ml] tick publish_gates_shadow window=24h evaluated=%d would_publish=%d would_drop=%d would_info=%d would_telemetry_volume=%d not_evaluated=%d invalid=%d failed_by_gate=%s not_evaluable_by_gate=%s would_drop_problem=%d would_drop_escalated=%d enforce=%v",
		t.Evaluated, t.WouldPublish, t.WouldDrop, t.WouldInfo, t.WouldTelemetryVolume, t.NotEvaluated, t.Invalid, kvLine(failed), kvLine(notEval),
		t.WouldDropProblem, t.WouldDropEscalated, publishGatesEnforce)
}

func kvLine(m map[string]int64) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for i, k := range ks {
		ks[i] = fmt.Sprintf("%s:%d", k, m[k])
	}
	return strings.Join(ks, ",")
}
