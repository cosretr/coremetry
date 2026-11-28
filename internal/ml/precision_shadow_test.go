package ml

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var precNow = time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)

// precBad — düşme şartlarının HEPSİNİ sınırda karşılayan karne satırı.
func precBad() chstore.MLDetectorScore {
	return chstore.MLDetectorScore{Detector: "op_latency", Samples: 24, Hits: 6, Wrong: 18, WilsonLo: 0.12, WilsonHi: 0.449,
		Labellers: 2, LabelDays: 5, WorstSvcShare: 0.6}
}

func precCfg(mode string) chstore.MLPrecisionShadow {
	c := chstore.DefaultMLPrecisionShadow()
	c.Mode = mode
	return c
}

func precIn(mode string, mut func(*PrecisionInput)) PrecisionInput {
	in := PrecisionInput{Score: precBad(), Cfg: precCfg(mode), EnabledML: 8, Now: precNow}
	if mut != nil {
		mut(&in)
	}
	return in
}

func days(n float64) int64 { return precNow.Add(-time.Duration(n * 24 * float64(time.Hour))).Unix() }

func TestDecidePrecisionShadowTable(t *testing.T) {
	on, sh := chstore.MLPrecisionModeOn, chstore.MLPrecisionModeShadow
	cases := []struct {
		name string
		in   PrecisionInput
		want string
	}{
		// temel: bütün şartlar sınırda tutuyor
		{"shadow kipinde düşerdi", precIn(sh, nil), PrecWouldDemote},
		{"on kipinde düşer", precIn(on, nil), PrecDemote},
		// üst sınır eşiği (< 0,50)
		{"üst 0,4999 düşer", precIn(on, func(i *PrecisionInput) { i.Score.WilsonHi = 0.4999 }), PrecDemote},
		{"üst 0,50 kalır", precIn(on, func(i *PrecisionInput) { i.Score.WilsonHi = 0.50 }), PrecKeep},
		{"etiketsiz kalır", precIn(on, func(i *PrecisionInput) { i.Score = chstore.MLDetectorScore{Detector: "op_latency"} }), PrecKeep},
		// küçük n
		{"n 19 frenlenir", precIn(on, func(i *PrecisionInput) { i.Score.Samples, i.Score.Hits = 19, 3 }), precBlocked + PrecBlockFewLabels},
		{"n 20 düşer", precIn(on, func(i *PrecisionInput) { i.Score.Samples, i.Score.Hits = 20, 5 }), PrecDemote},
		// etiketleyen / gün
		{"tek kişi", precIn(on, func(i *PrecisionInput) { i.Score.Labellers = 1 }), precBlocked + PrecBlockFewLabellers},
		{"4 gün", precIn(on, func(i *PrecisionInput) { i.Score.LabelDays = 4 }), precBlocked + PrecBlockFewDays},
		// tek servis baskın (> 0,6)
		{"pay 0,61", precIn(on, func(i *PrecisionInput) { i.Score.WorstSvcShare = 0.61 }), precBlocked + PrecBlockSingleService},
		// kuyruk yanlılığı (alt ≥ 0,60)
		{"kuyruk alt 0,60", precIn(on, func(i *PrecisionInput) { i.Score.Queue = chstore.MLQueueCounts{N: 8, Correct: 8, WilsonLo: 0.60} }), precBlocked + PrecBlockQueueBias},
		{"kuyruk alt 0,59", precIn(on, func(i *PrecisionInput) { i.Score.Queue = chstore.MLQueueCounts{N: 8, Correct: 7, WilsonLo: 0.59} }), PrecDemote},
		// 14 gün yeniden düşme freni
		{"geri döneli 13,9 gün", precIn(on, func(i *PrecisionInput) { i.State.RestoredAt = days(13.9) }), precBlocked + PrecBlockCooldown},
		{"geri döneli 14 gün", precIn(on, func(i *PrecisionInput) { i.State.RestoredAt = days(14) }), PrecDemote},
		// açık P1
		{"açık P1", precIn(on, func(i *PrecisionInput) { i.OpenP1 = true }), precBlocked + PrecBlockOpenP1},
		// günlük sınır
		{"bugün 1 düştü", precIn(on, func(i *PrecisionInput) { i.DemotedToday = 1 }), precBlocked + PrecBlockDailyCap},
		{"maxPerDay 2", precIn(on, func(i *PrecisionInput) { i.DemotedToday = 1; i.Cfg.MaxPerDay = 2 }), PrecDemote},
		// %25 tavanı: 8 açık → en çok 2
		{"tavan: 1 gölgede, 8 açık", precIn(on, func(i *PrecisionInput) { i.DemotedTotal = 1 }), PrecDemote},
		{"tavan: 2 gölgede, 8 açık", precIn(on, func(i *PrecisionInput) { i.DemotedTotal = 2 }), precBlocked + PrecBlockShareCap},
		{"tavan: 3 açık → hiç", precIn(on, func(i *PrecisionInput) { i.EnabledML = 3 }), precBlocked + PrecBlockShareCap},
		// muafiyetler
		{"svc_error_rate muaf", precIn(on, func(i *PrecisionInput) { i.Score.Detector = "svc_error_rate" }), precBlocked + PrecBlockExempt},
		{"svc_response_time muaf", precIn(on, func(i *PrecisionInput) { i.Score.Detector = "svc_response_time" }), precBlocked + PrecBlockExempt},
		{"endpoint_impact muaf", precIn(on, func(i *PrecisionInput) { i.Score.Detector = "endpoint_impact" }), precBlocked + PrecBlockExempt},
		{"pod_oom muaf", precIn(on, func(i *PrecisionInput) { i.Score.Detector = "pod_oom" }), precBlocked + PrecBlockExempt},
		{"metric_kafka muaf", precIn(on, func(i *PrecisionInput) { i.Score.Detector = "metric_kafka" }), precBlocked + PrecBlockExempt},
		{"Problem açmayan", precIn(on, func(i *PrecisionInput) { i.Score.Detector = "throughput_drop" }), precBlocked + PrecBlockExempt},
		{"ML dışı aile", precIn(on, func(i *PrecisionInput) { i.Score.Detector = "svc-slowdown" }), precBlocked + PrecBlockExempt},
		// pin
		{"on pini", precIn(on, func(i *PrecisionInput) { i.Cfg.Pins = map[string]string{"op_latency": "on"} }), PrecKeep},
		{"shadow pini kuralı sürmez", precIn(on, func(i *PrecisionInput) { i.Cfg.Pins = map[string]string{"op_latency": "shadow"} }), PrecKeep},
	}
	for _, c := range cases {
		got := DecidePrecisionShadow(c.in)
		if got.Outcome != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, got.Outcome, got.Reason, c.want)
		}
		if got.Reason == "" {
			t.Errorf("%s: gerekçe boş", c.name)
		}
	}
}

func TestDecidePrecisionShadowRestore(t *testing.T) {
	on, sh := chstore.MLPrecisionModeOn, chstore.MLPrecisionModeShadow
	good := chstore.MLDetectorScore{Detector: "op_latency", Samples: 12, Hits: 12, WilsonLo: 0.757, WilsonHi: 1}
	dem := func(mode string, since *chstore.MLDetectorScore, at float64, mut func(*PrecisionInput)) PrecisionInput {
		return precIn(mode, func(i *PrecisionInput) {
			i.State = chstore.MLPrecisionDetState{Demoted: true, DemotedAt: days(at)}
			i.Since = since
			if mut != nil {
				mut(i)
			}
		})
	}
	lo := func(v float64, n int64) *chstore.MLDetectorScore {
		s := good
		s.WilsonLo, s.Samples = v, n
		return &s
	}
	cases := []struct {
		name string
		in   PrecisionInput
		want string
	}{
		{"7 gün dolmadı", dem(on, &good, 6.9, nil), precBlocked + PrecBlockMinShadow},
		{"7 gün doldu, alt 0,60", dem(on, lo(0.60, 10), 7, nil), PrecRestore},
		{"shadow kipinde dönerdi", dem(sh, lo(0.60, 10), 7, nil), PrecWouldRestore},
		{"alt 0,59 kalır", dem(on, lo(0.59, 10), 7, nil), PrecKeep},
		{"9 yeni etiket kalır", dem(on, lo(0.9, 9), 30, nil), PrecKeep},
		{"yeni etiket okunamadı kalır", dem(on, nil, 30, nil), PrecKeep},
		{"on pini hemen döndürür", dem(on, nil, 1, func(i *PrecisionInput) { i.Cfg.Pins = map[string]string{"op_latency": "on"} }), PrecRestore},
		{"muaf olduysa döner", dem(on, nil, 1, func(i *PrecisionInput) { i.Score.Detector = "metric_kafka" }), PrecRestore},
	}
	for _, c := range cases {
		if got := DecidePrecisionShadow(c.in); got.Outcome != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, got.Outcome, got.Reason, c.want)
		}
	}
}

func TestPrecisionReasonSentence(t *testing.T) {
	d := DecidePrecisionShadow(precIn(chstore.MLPrecisionModeShadow, func(i *PrecisionInput) { i.Score.WilsonHi = 0.45 }))
	if d.Reason != "30 günde 24 etiket, 6'sı doğru; üst sınır %45" {
		t.Errorf("gerekçe: %q", d.Reason)
	}
	for n, want := range map[int64]string{0: "0'ı", 1: "1'i", 3: "3'ü", 6: "6'sı", 9: "9'u", 10: "10'u", 20: "20'si", 24: "24'ü", 40: "40'ı", 100: "100'ü", 2000: "2000'i"} {
		if got := trPossessive(n); got != want {
			t.Errorf("trPossessive(%d) = %s, want %s", n, got, want)
		}
	}
	b := DecidePrecisionShadow(precIn(chstore.MLPrecisionModeOn, func(i *PrecisionInput) { i.OpenP1 = true }))
	if !strings.Contains(b.Reason, "P1") || b.Blocked() != PrecBlockOpenP1 || !b.Signal {
		t.Errorf("fren gerekçesi: %+v", b)
	}
}

// Günlük plan: en kötü önce; günde 1; kip geçişi; kayıt + audit alanları.
func TestPlanPrecisionDay(t *testing.T) {
	bad1 := precBad()
	bad2 := precBad()
	bad2.Detector, bad2.WilsonHi = "error_rate", 0.30 // daha kötü
	ok := chstore.MLDetectorScore{Detector: "op_error_rate", Samples: 40, Hits: 35, WilsonLo: 0.74, WilsonHi: 0.95, Labellers: 3, LabelDays: 9}
	enabled := map[string]bool{}
	for _, n := range chstore.MLDetectorNames {
		enabled[n] = true
	}
	in := PrecisionDayInput{Rows: []chstore.MLDetectorScore{bad1, bad2, ok}, Cfg: precCfg(chstore.MLPrecisionModeShadow), Enabled: enabled,
		Fed: map[string]chstore.MLDetectorProblemsFed{"error_rate": {Fed: 5, Real: 1}}, Day: "2026-11-28", Now: precNow}
	st, decs, recs := PlanPrecisionDay(in)
	if len(decs) != 3 {
		t.Fatalf("3 karar: %+v", decs)
	}
	if decs[0].Detector != "error_rate" || decs[0].Outcome != PrecWouldDemote {
		t.Errorf("en kötü önce düşerdi: %+v", decs[0])
	}
	if decs[1].Detector != "op_latency" || decs[1].Blocked() != PrecBlockDailyCap {
		t.Errorf("ikinci günlük sınıra takılır: %+v", decs[1])
	}
	if !st.Detectors["error_rate"].Demoted || !st.Detectors["error_rate"].Virtual || st.LastEvalDay != "2026-11-28" {
		t.Errorf("sanal durum: %+v", st)
	}
	if len(recs) != 2 || recs[0].ProblemsFed != 5 || recs[0].ProblemsReal != 1 || recs[0].Window != 30 || recs[0].N != 24 || recs[0].Wrong != 18 {
		t.Errorf("kayıtlar: %+v", recs)
	}
	if !precisionAudited(recs[0].Outcome) || precisionAudited(recs[1].Outcome) {
		t.Error("audit yalnız durum değişikliği")
	}
	a := precisionAuditEntry(recs[0])
	if a.ActorID != PrecisionAuditActor || a.Action != "ml.precision.would_demote" || a.TargetID != "error_rate" ||
		!strings.Contains(a.Details, `"upper":0.3`) || !strings.Contains(a.Details, `"window":30`) || !strings.Contains(a.Details, `"reason":`) {
		t.Errorf("audit: %+v", a)
	}
	// Sanal kayıt davranışı değiştirmez; on kipine geçişte silinir.
	if len(chstore.MLPrecisionDemotedSet(precCfg(chstore.MLPrecisionModeShadow), st)) != 0 {
		t.Error("shadow kipinde gölge kümesi boş olmalı")
	}
	if len(chstore.MLPrecisionDemotedSet(precCfg(chstore.MLPrecisionModeOn), st)) != 0 {
		t.Error("sanal kayıt on kipinde de gölge sayılmaz")
	}
	in2 := in
	in2.State, in2.Cfg, in2.Day, in2.Now = st, precCfg(chstore.MLPrecisionModeOn), "2026-11-29", precNow.Add(24*time.Hour)
	st2, decs2, _ := PlanPrecisionDay(in2)
	if decs2[0].Detector != "error_rate" || decs2[0].Outcome != PrecDemote || st2.Detectors["error_rate"].Virtual {
		t.Errorf("on kipinde gerçek düşüş (sanal silindi, frenler baştan): %+v %+v", decs2, st2.Detectors)
	}
	if set := chstore.MLPrecisionDemotedSet(precCfg(chstore.MLPrecisionModeOn), st2); !set["error_rate"] || len(set) != 1 {
		t.Errorf("gölge kümesi: %v", set)
	}
	// Aynı gün ikinci koşu: çağıran LastEvalDay ile atlar (idempotent); plan
	// yine de çağrılırsa bugün düşen sayılır, ikinci düşüş olmaz.
	in3 := in2
	in3.State = st2
	_, decs3, _ := PlanPrecisionDay(in3)
	for _, d := range decs3 {
		if d.Outcome == PrecDemote {
			t.Errorf("aynı gün ikinci düşüş: %+v", d)
		}
	}
}

func TestMLPrecisionDemotedSetPins(t *testing.T) {
	st := chstore.MLPrecisionState{Detectors: map[string]chstore.MLPrecisionDetState{
		"op_latency": {Demoted: true}, "error_rate": {Demoted: true}, "metric_kafka": {Demoted: true}}}
	c := precCfg(chstore.MLPrecisionModeOn)
	c.Pins = map[string]string{"error_rate": "on", "template_spike": "shadow", "log_volume": "shadow"}
	set := chstore.MLPrecisionDemotedSet(c, st)
	if !set["op_latency"] || set["error_rate"] || set["metric_kafka"] || !set["template_spike"] || set["log_volume"] || len(set) != 2 {
		t.Errorf("pin / muaf: %v", set)
	}
	c.Mode = chstore.MLPrecisionModeShadow
	if chstore.MLPrecisionDemotedSet(c, st) != nil {
		t.Error("shadow kipinde pin de davranış değiştirmez")
	}
}

func TestDetShadowStamp(t *testing.T) {
	evs := []chstore.MLEvent{{ID: "a", Detector: "op_latency"}, {ID: "b", Detector: "error_rate", Evidence: `{"x":1}`}}
	stampDetShadow(evs, nil, precNow)
	if evs[0].Evidence != "" || evs[1].Evidence != `{"x":1}` {
		t.Fatal("boş küme damgalamaz")
	}
	stampDetShadow(evs, map[string]bool{"error_rate": true}, precNow)
	if _, ok := DetShadowOf(evs[0].Evidence); ok {
		t.Error("gölgede olmayan damgalanmaz")
	}
	d, ok := DetShadowOf(evs[1].Evidence)
	if !ok || d.Reason != DetShadowReason || !strings.Contains(evs[1].Evidence, `"x":1`) {
		t.Errorf("damga: %v %+v %s", ok, d, evs[1].Evidence)
	}
	if got := demotedOut(evs, map[string]bool{"error_rate": true}); len(got) != 1 || got[0].ID != "a" {
		t.Errorf("servis düzeyi girdi: %+v", got)
	}
	if got := demotedOut(evs, nil); len(got) != 2 {
		t.Error("boş küme girdiyi aynen döndürür")
	}
}
