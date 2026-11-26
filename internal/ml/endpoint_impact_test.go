package ml

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// epBaseOK — hazır taban: trafik rate istek/sn, hata oranı medyanı errMed,
// medyan / p90 ms (0 = o ölçünün tabanı yok).
func epBaseOK(rate, errMed, p50, p90 float64) epBase {
	b := epBase{RateN: 30, Rate: rate}
	for i, med := range []float64{errMed, p50, p90} {
		if i == 0 || med > 0 {
			b.N[i], b.Median[i] = 30, med
		}
	}
	return b
}

func epSamples3(err, p50, p90 impactSample) [3][]impactSample {
	return [3][]impactSample{repeatS(err), repeatS(p50), repeatS(p90)}
}

// Büyük, sağlıklı serviste tek bozuk uç nokta: servis kuralı ateşlemez, uç
// nokta kuralı (AYNI impactDecide) ateşler.
func TestEndpointImpactFiresWhereServiceRuleDoesNot(t *testing.T) {
	// 20 uç nokta × dakikada 1.000 istek (%0,2 hata) + POST /pay dakikada 100
	// istek, %0,2 → %14.
	healthy, payLoad := 20*1000.0, 100.0
	svcRatio := (healthy*0.002 + payLoad*0.14) / (healthy + payLoad)
	sv := svcImpactDecideBase(repeatS(impS(svcRatio, healthy+payLoad)), svcBase{N: 30, Median: 0.002}, 20, false)
	if sv.Fire {
		t.Fatalf("servis kuralı ateşlememeli (oran %.4f, eşik %.4f)", svcRatio, sv.Threshold)
	}
	v := epDecide(epSamples3(impS(0.14, payLoad), impS(120, payLoad), impS(300, payLoad)), epBaseOK(payLoad/60, 0.002, 110, 280), 20, false)
	if !v.Fire || !v.Emit || v.Lead != 0 || !v.Fired[0] || v.Fired[1] || v.Fired[2] {
		t.Fatalf("uç nokta hata oranıyla ateşlemeli: %+v", v)
	}
	if want := impactRules[impactFailureRate].threshold(0.002); v.M[0].Threshold != want {
		t.Fatalf("eşik servis kuralının aynısı olmalı: %v != %v", v.M[0].Threshold, want)
	}
	// yanıt süresi: medyan 100 → 400 ms (hata yok) → ölçü medyan
	r := epDecide(epSamples3(impS(0.002, payLoad), impS(400, payLoad), impS(600, payLoad)), epBaseOK(payLoad/60, 0.002, 100, 500), 20, false)
	if !r.Fire || r.Lead != 1 {
		t.Fatalf("medyanla ateşlemeli: %+v", r)
	}
	// ikisi de ateşlerse hata oranı olayın ölçüsü
	both := epDecide(epSamples3(impS(0.2, payLoad), impS(900, payLoad), impS(900, payLoad)), epBaseOK(payLoad/60, 0.002, 100, 200), 20, false)
	if both.Lead != 0 || !both.Fired[1] {
		t.Fatalf("hata oranı önce: %+v", both)
	}
}

// Yük kapısı: dakikada 10 isteğin altı ihlal değildir (aday da değildir).
func TestEndpointImpactLoadGate(t *testing.T) {
	v := epDecide(epSamples3(impS(0.5, 9), impS(10, 9), impS(10, 9)), epBaseOK(1, 0.002, 10, 20), 20, false)
	if v.Fire {
		t.Fatalf("dakikada 9 istekle ateşlememeli: %+v", v)
	}
	bases := map[epKey]epBase{
		{"checkout", "POST /pay"}:  epBaseOK(0.5, 0.002, 0, 0),    // 30 / dk
		{"checkout", "GET /cart"}:  epBaseOK(0.1, 0.002, 0, 0),    // 6 / dk → kapı dışı
		{"checkout", "GET /quiet"}: {RateN: 5, Rate: 10},          // taban örneği az
		{"payments", "POST /auth"}: epBaseOK(10.0/60, 0.01, 0, 0), // tam 10 / dk
	}
	got := epCandidates(bases, 20, nil)
	if fmt.Sprint(got) != "[{checkout POST /pay} {payments POST /auth}]" {
		t.Fatalf("adaylar: %v", got)
	}
}

// Servis başına en çok epTopK (trafiğe göre), toplam epGlobalCap; açık koşu
// kapıdan ve K'dan muaf, tavanda önce.
func TestEndpointImpactTopKAndCap(t *testing.T) {
	rates := map[epKey]float64{}
	for i := 0; i < epTopK+5; i++ {
		rates[epKey{"checkout", fmt.Sprintf("GET /r%02d", i)}] = float64(100 + i)
	}
	rates[epKey{"checkout", "GET /cold"}] = 0.01 // kapı dışı ama açık koşu
	pinned := func(k epKey) bool { return k.route == "GET /cold" }
	got := epSelect(rates, 10, epTopK, epGlobalCap, pinned)
	if len(got) != epTopK+1 {
		t.Fatalf("K + açık koşu: %d", len(got))
	}
	have := map[string]bool{}
	for _, k := range got {
		have[k.route] = true
	}
	if !have["GET /cold"] || !have[fmt.Sprintf("GET /r%02d", epTopK+4)] || have["GET /r00"] {
		t.Fatalf("en yoğun K + açık koşu seçilmeli: %v", got)
	}
	// genel tavan: 300 servis × K
	big := map[epKey]float64{}
	for s := 0; s < 300; s++ {
		for r := 0; r < epTopK; r++ {
			big[epKey{fmt.Sprintf("svc-%03d", s), fmt.Sprintf("GET /r%d", r)}] = float64(1 + s)
		}
	}
	big[epKey{"svc-000", "GET /open"}] = 0.5
	all := epSelect(big, 10, epTopK, epGlobalCap, func(k epKey) bool { return k.route == "GET /open" })
	if len(all) != epGlobalCap {
		t.Fatalf("genel tavan %d, alınan %d", epGlobalCap, len(all))
	}
	open := false
	for _, k := range all {
		open = open || k.route == "GET /open"
	}
	if !open {
		t.Fatal("açık koşu tavanda önce")
	}
}

func epEvFor(t *testing.T, svc, route string, base epBase, s [3][]impactSample) chstore.MLEvent {
	t.Helper()
	v := epDecide(s, base, 20, false)
	if !v.Emit {
		t.Fatalf("olay yazılmalı: %+v", v)
	}
	end := time.Date(2026, 11, 26, 9, 20, 0, 0, time.UTC)
	return epEvent(epKey{svc, route}, s, v, base, end, 0, chstore.DefaultMLConfig(), "weekday")
}

// Servisin açık Problem'i (servis düzeyi olay) varken uç nokta olayı üyedir:
// tek Problem girdisi, değer ve cümle servis olayından, uç nokta Events'te.
func TestEndpointImpactJoinsServiceProblem(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 30, 0, 0, time.UTC)
	svc := svcEv("svc1", detSvcErrorRate, "checkout", 0, 0.004, 0.03)
	ep := epEvFor(t, "checkout", "POST /pay", epBaseOK(2, 0.002, 0, 0), epSamples3(impS(0.14, 120), impGap, impGap))
	ep.ID = "ep1"
	kept := []chstore.MLEvent{svc, ep}
	ds := decideSvcShadow(kept, nil, nil, chstore.CurrentProblemPriority(), now)
	if !ds["ep1"].WouldOpen || ds["ep1"].RuleID != MLSvcRulePrefix+"checkout" || ds["ep1"].Owner {
		t.Fatalf("uç nokta aynı servis Problem'ine üye olmalı: %+v", ds["ep1"])
	}
	ins := svcProblemInputs(kept, ds)
	if len(ins) != 1 || fmt.Sprint(ins[0].Events) != "[svc1 ep1]" {
		t.Fatalf("tek girdi, iki üye: %+v", ins)
	}
	if !strings.HasPrefix(ins[0].Desc, chstore.MLErrRateSentence("checkout", 0.03, 0.004)) || strings.Contains(ins[0].Desc, "POST /pay") {
		t.Fatalf("manşet servis olayından: %q", ins[0].Desc)
	}
	if v, th := svcValueThreshold(svc); ins[0].Value != v || ins[0].Threshold != th {
		t.Fatalf("değer servis olayından: %v / %v", ins[0].Value, ins[0].Threshold)
	}
}

// Açık Problem yoksa uç nokta olayı servis Problem'ini açar; manşet uç
// noktayı adlandırır, ikinci uç nokta yalnız üye.
func TestEndpointImpactOpensServiceProblemWithEndpointHeadline(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 30, 0, 0, time.UTC)
	pay := epEvFor(t, "checkout", "POST /pay", epBaseOK(2, 0.002, 0, 0), epSamples3(impS(0.14, 120), impGap, impGap))
	cart := epEvFor(t, "checkout", "GET /cart", epBaseOK(2, 0.002, 0, 0), epSamples3(impS(0.03, 120), impGap, impGap))
	kept := []chstore.MLEvent{cart, pay}
	ds := decideSvcShadow(kept, nil, nil, chstore.CurrentProblemPriority(), now)
	ins := svcProblemInputs(kept, ds)
	if len(ins) != 1 || len(ins[0].Events) != 2 || ins[0].Metric != "error_rate" {
		t.Fatalf("tek Problem, iki üye: %+v", ins)
	}
	p := newSvcProblem(ins[0])
	p.RuleName = svcProblemRuleName
	h, ok := chstore.MLPromotedHeadline(p)
	if want := "checkout · POST /pay: hata oranı normalde %0,2, şimdi %14 (70 kat)."; !ok || h != want {
		t.Fatalf("manşet:\n got %q\nwant %q", h, want)
	}
	if ins[0].Value != 0.14 || ins[0].Threshold != svcErrThresholdFloor {
		t.Fatalf("değer / eşik (öncelik girdisi): %v / %v", ins[0].Value, ins[0].Threshold)
	}
	// yanıt süresi olayı: ölçü p50_ms, cümle ölçü adıyla
	rt := epEvFor(t, "checkout", "POST /pay", epBaseOK(2, 0.002, 180, 0), epSamples3(impS(0.001, 120), impS(920, 120), impGap))
	if svcMetric(rt) != "p50_ms" || epSentence(rt) != "checkout · POST /pay: yanıt süresi (medyan) normalde 180 ms, şimdi 920 ms (5,1 kat)." {
		t.Fatalf("yanıt süresi: %s %q", svcMetric(rt), epSentence(rt))
	}
	if f := ImpactRuleFacts(rt); f == nil || f.Measure != chstore.MLRespMedian || f.LoadPerMin != 120 {
		t.Fatalf("Neden gerçekleri: %+v", f)
	}
	if f := ImpactRuleFacts(pay); f == nil || f.Measure != "" || f.Violations != 5 || f.Rel != 1.5 {
		t.Fatalf("Neden gerçekleri (hata oranı): %+v", f)
	}
}

// op_error_rate ile çift üye yok: aynı uç noktanın op_error_rate olayı düşer,
// başka uç noktanınki kalır.
func TestEndpointImpactDedupesOpErrorRate(t *testing.T) {
	evs := []chstore.MLEvent{
		{ID: "op1", Detector: detOpErrorRate, Service: "checkout", Code: "POST /pay"},
		{ID: "op2", Detector: detOpErrorRate, Service: "checkout", Code: "GET /cart"},
		{ID: "op3", Detector: detOpErrorRate, Service: "payments", Code: "POST /pay"},
		{ID: "ep1", Detector: detEndpointImpact, Service: "checkout", Code: "POST /pay"},
		{ID: "lat", Detector: detOpLatency, Service: "checkout", Code: "POST /pay"},
	}
	var ids []string
	for _, e := range dedupeEndpointOps(evs) {
		ids = append(ids, e.ID)
	}
	if fmt.Sprint(ids) != "[op2 op3 ep1 lat]" {
		t.Fatalf("kalan: %v", ids)
	}
	only := []chstore.MLEvent{{ID: "op1", Detector: detOpErrorRate, Service: "checkout", Code: "POST /pay"}}
	if got := dedupeEndpointOps(only); len(got) != 1 {
		t.Fatal("endpoint_impact yokken op_error_rate aynen kalır")
	}
}

// Anahtar: varsayılan açık; kapalıyken TraceRED onu açmaz, yoklama sormaz,
// tik adımı koşmaz (clearEP).
func TestEndpointImpactSwitch(t *testing.T) {
	cfg := rtProbeCfg() // ana anahtar açık
	if !cfg.DetectorOn(detEndpointImpact) || !chstore.MLDetectorDefaultOn(detEndpointImpact) {
		t.Fatal("varsayılan açık olmalı")
	}
	off := chstore.DefaultMLConfig()
	off.Detectors[detEndpointImpact] = false
	if (epProbe{}).probeOn(off) {
		t.Fatal("kapalıyken yoklama yok")
	}
	only := rtProbeCfg()
	for k := range only.Detectors {
		only.Detectors[k] = false
	}
	d := NewTraceRED()
	if d.Enabled(only) {
		t.Fatal("hepsi kapalı")
	}
	only.Detectors[detEndpointImpact] = true
	if !d.Enabled(only) {
		t.Fatal("yalnız endpoint_impact açıkken TraceRED koşar")
	}
	d.epCands = map[epKey]epBase{{"checkout", "POST /pay"}: epBaseOK(2, 0.002, 0, 0)}
	d.epNote = "x"
	d.clearEP()
	if d.epCands != nil || d.epNote != "" {
		t.Fatal("kapalıyken durum silinir")
	}
	if !impactRuleRun(chstore.MLEvent{Detector: detEndpointImpact}) || !gateHoldExempt(chstore.MLEvent{Detector: detEndpointImpact}) {
		t.Fatal("etki kuralı ailesi: yayın kapılarından muaf")
	}
}

// epVM — 1 dk'lık uç nokta sorgularına yanıt veren sahte VM: checkout ·
// POST /pay dakikada 120 istek / 17 hata; GET /cart aday değil (süzülür).
type epVM struct {
	fakeVM
	queries []string
}

func (f *epVM) QueryPromQLInstant(_ context.Context, q string, _ time.Time) ([]chstore.SpanMetricSeries, error) {
	f.queries = append(f.queries, q)
	row := func(route string, v float64, extra ...string) chstore.SpanMetricSeries {
		gk := append([]string{`service_name="checkout"`, fmt.Sprintf("http_route=%q", route)}, extra...)
		return chstore.SpanMetricSeries{GroupKey: gk, Points: []chstore.SpanMetricPoint{{Value: v}}}
	}
	switch {
	case strings.HasPrefix(q, "histogram_quantiles("):
		return []chstore.SpanMetricSeries{row("POST /pay", 0.12, `ep_phi="0.5"`), row("POST /pay", 0.3, `ep_phi="0.9"`), row("GET /cart", 9, `ep_phi="0.5"`)}, nil
	case strings.Contains(q, "http_response_status_code"):
		return []chstore.SpanMetricSeries{row("POST /pay", 17), row("GET /cart", 50)}, nil
	}
	return []chstore.SpanMetricSeries{row("POST /pay", 120), row("GET /cart", 500)}, nil
}

// Dakika satırları: sorgu biçimi, aday süzgeci, saklama (yoklama yalnız yeni
// dakikayı sorar) ve yoklamanın ateşleyen uç noktası.
func TestEndpointImpactMinutesAndProbe(t *testing.T) {
	pin := anomaly.REDFamilyPins()[0]
	fams := []impFam{{idx: 0, pin: pin, fl: traceFamilyLabels{svc: "service_name", route: "http_route", status: "http_response_status_code", ok: true}}}
	vm := &epVM{}
	deps := Deps{VM: vm}
	d := NewTraceRED()
	now := time.Date(2026, 11, 26, 9, 30, 30, 0, time.UTC)
	cfg := rtProbeCfg()
	end := svcImpactEnd(now, cfg.IngestLagSec)
	pay := epKey{"checkout", "POST /pay"}
	w, err := d.epMinutes(context.Background(), deps, fams, map[epKey]bool{pay: true}, end, now)
	if err != nil || w.blind {
		t.Fatalf("pencere: %v blind=%v", err, w.blind)
	}
	if len(vm.queries) != 3*impactWindow {
		t.Fatalf("soğuk başlangıç: dakika × 3 sorgu, alınan %d", len(vm.queries))
	}
	for _, q := range vm.queries {
		if !strings.Contains(q, "sum by (service_name, http_route") || !strings.Contains(q, "[60s]") {
			t.Fatalf("sorgu biçimi: %s", q)
		}
	}
	s, fam := epSamples(w, pay)
	if fam != 0 || !s[0][4].OK || s[0][4].Load != 120 || s[1][4].Value != 120 || s[2][4].Value != 300 {
		t.Fatalf("örnekler: %+v fam=%d", s, fam)
	}
	if _, ok := w.min[end.Unix()-impactStepSec][epKey{"checkout", "GET /cart"}]; ok {
		t.Fatal("aday olmayan uç nokta saklanmaz")
	}
	// yoklama: taban %0,2 → %14 ateşler; bir dakika sonra yalnız yeni dakika sorulur
	d.mu.Lock()
	d.epCands, d.epCandsAt = map[epKey]epBase{pay: epBaseOK(2, 0.002, 0, 0)}, now
	d.labels[pin.ID] = fams[0].fl
	d.mu.Unlock()
	vm.queries = nil
	later := now.Add(time.Minute)
	got, err := epProbe{d}.probeImpact(context.Background(), deps, cfg, later)
	if err != nil || len(got) != 1 || got[0].BaseID != chstore.MLEventID(detEndpointImpact, "checkout", "POST /pay") {
		t.Fatalf("yoklama: %+v %v", got, err)
	}
	if len(vm.queries) != 3 {
		t.Fatalf("yoklama yalnız yeni dakikayı sorar: %d", len(vm.queries))
	}
}
