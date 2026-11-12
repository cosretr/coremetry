package ml

// v0.10.2018 (T-err) — trace_error_count saf çekirdeği: mutlak hata
// sayısı kendi tabanına göre (oran YOK). Taban, dedektörün ölçtüğü
// kovalarda hata yoksa sıfırla doldurulur (ölçülmemiş ≠ sıfır).
// Servis adları SENTETİK.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func errCfg() chstore.MLConfig {
	c := chstore.DefaultMLConfig()
	c.DwellBuckets, c.MinBaselineSamples, c.CriticalZ, c.MinAffectedRequests = 2, 20, 4, 10
	return c
}

func zeros(n int) []float64 { return make([]float64, n) }

func TestPadErrBaseline(t *testing.T) {
	pts := []chstore.MLPoint{{T: 1, V: 3}, {T: 2, V: 1}}
	if got := padErrBaseline(pts, 5); len(got) != 5 || got[0] != 3 || got[4] != 0 {
		t.Errorf("dolgu: %v", got)
	}
	if got := padErrBaseline(pts, 1); len(got) != 2 {
		t.Errorf("ölçülen kova örnekten azsa dolgu yok: %v", got)
	}
	if got := padErrBaseline(nil, 0); len(got) != 0 {
		t.Errorf("hiç ölçüm yok → boş taban (eksik veri): %v", got)
	}
}

func TestMeasuredErrBuckets(t *testing.T) {
	base := map[string][]chstore.MLPoint{"a": {{T: 10}, {T: 20}}, "b": {{T: 20}, {T: 30}}}
	if n := measuredErrBuckets(base); n != 3 {
		t.Errorf("farklı kova = %d, istenen 3", n)
	}
}

func TestJudgeErrCount(t *testing.T) {
	cfg := errCfg()
	noisy := append(zeros(15), 2, 3, 1, 2, 4, 3, 2, 1, 2, 3) // med 0..1 civarı, ara sıra birkaç hata
	cases := []struct {
		name   string
		cur    []float64
		base   []float64
		fire   bool
		reason string
	}{
		{"hiç hata görmemiş operasyonda 40'a sıçrama → olay", []float64{30, 40}, zeros(30), true, ""},
		{"tek kova sıçrayıp düşen → olay değil (dwell)", []float64{0, 40}, zeros(30), false, "dwell"},
		{"mutlak taban altında (8 hata, eşik 10) → olay değil", []float64{8, 8}, zeros(30), false, "mutlak"},
		{"normal gürültü içinde (4 hata) → olay değil", []float64{4, 4}, noisy, false, ""},
		{"gürültülü tabanda 60 → olay", []float64{50, 60}, noisy, true, ""},
		{"taban yetersiz (ölçülen kova az) → eksik veri", []float64{50, 60}, zeros(5), false, "eksik veri"},
		{"dwell kovası eksik → değerlendirme yok", []float64{60}, zeros(30), false, "dwell"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := judgeErrCount(c.cur, c.base, cfg)
			if j.Fire != c.fire {
				t.Fatalf("fire=%v istenen %v (%+v)", j.Fire, c.fire, j)
			}
			if c.reason != "" && !strings.Contains(j.Reason, c.reason) {
				t.Errorf("gerekçe %q, %q içermeli", j.Reason, c.reason)
			}
			if j.Fire && (j.NormalMax <= 0 || j.Observed != c.cur[len(c.cur)-1] || j.Z <= 0) {
				t.Errorf("olay alanları: %+v", j)
			}
		})
	}
}

func TestTraceErrEvent(t *testing.T) {
	last := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	j := errJudgement{Fire: true, Observed: 40, NormalMax: 4, Median: 0, Z: 40}
	ev := traceErrEvent("checkout", "POST /pay", j, last, errCfg(), "weekday", []string{"t1", "t2", "t3"})
	if ev.Detector != detTraceErrorCount || ev.Signal != chstore.MLSignalTrace || ev.Code != "POST /pay" || ev.Direction != "up" {
		t.Fatalf("olay: %+v", ev)
	}
	if ev.Baseline != 4 || ev.Observed != 40 {
		t.Errorf("taban = normalin üst sınırı, gözlenen = son kova: %+v", ev)
	}
	for _, want := range []string{`"samples":["t1","t2","t3"]`, `"source":"spanmetrics_1m"`, `"dayClass":"weekday"`, `"measure":"error_count"`} {
		if !strings.Contains(ev.Evidence, want) {
			t.Errorf("kanıt %q içermeli: %s", want, ev.Evidence)
		}
	}
	if eventPart(ev) != "POST /pay|errcnt" {
		t.Errorf("seri parçası %q", eventPart(ev))
	}
}

// Batch kuralı: hata SAYISI sınıfı — pay kanıtı yoksa (CH'de oran yok) KALIR.
func TestTraceErrBatchClass(t *testing.T) {
	ev := chstore.MLEvent{Detector: detTraceErrorCount, Signal: chstore.MLSignalTrace, Service: "billing-batch"}
	if batchMeasureOf(ev, nil) != batchMeasureErrCount {
		t.Fatal("trace_error_count hata sayısı sınıfında olmalı")
	}
	if !batchKeep(ev, func(s string) bool { return strings.HasSuffix(s, "-batch") }, nil) {
		t.Error("pay kanıtı yokken batch serviste de kalmalı (susturma yalnız olumlu kanıtla)")
	}
}

// D2/D3/D4: aynı servis + teyit penceresi → trace_red ile tek grup; v0.10.2052
// kapısı farklı SİNYAL sayar → iki trace dedektörü Problem açmaz.
func TestTraceErrGroupsWithTraceRED(t *testing.T) {
	evs := []chstore.MLEvent{gateEv("e", detTraceErrorCount, "checkout", 0, 20, 4, 40), gateEv("r", detOpErrorRate, "checkout", 5, 25, 0.01, 0.2)}
	gs := GroupMLEvents(evs, 10*time.Minute)
	if len(gs) != 1 || len(gs[0].Detectors) != 2 {
		t.Fatalf("tek grup, iki dedektör bekleniyordu: %+v", gs)
	}
	d := DecideGroupPromotion(gs[0], evs, GroupGateInput{}, gBase)
	if d.WouldOpen || !strings.Contains(d.Reason, "tek sinyal") {
		t.Fatalf("iki trace dedektörü açmamalı: %+v", d)
	}
	// log hata oranı teyit ederse (ikinci sinyal) açar
	evs = append(evs, gateEv("l", detErrorRate, "checkout", 8, 22, 5, 30))
	if d := DecideGroupPromotion(GroupMLEvents(evs, 10*time.Minute)[0], evs, GroupGateInput{}, gBase); !d.WouldOpen {
		t.Errorf("trace + log açmalı: %+v", d)
	}
}

// Anlatım: düz cümle, oran yok, Türkçe yönelme eki.
func TestTraceErrSummary(t *testing.T) {
	st := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	in := CardInput{Detector: detTraceErrorCount, Signal: chstore.MLSignalTrace, Service: "checkout", Code: "POST /pay",
		StartedAt: st, LastSeen: st.Add(10 * time.Minute), BucketSec: 300, Observed: 40, Baseline: 3.2, Direction: "up", Now: st.Add(10 * time.Minute)}
	s := ExplainSummary(in)
	if !strings.HasPrefix(s, "checkout servisinin POST /pay operasyonunda hata sayısı son 5 dakikada 40'a çıktı; bu saatte normali en çok 4") {
		t.Errorf("özet: %q", s)
	}
	if strings.Contains(s, "%") || strings.Contains(s, "oran") {
		t.Errorf("oran söylenmemeli: %q", s)
	}
	for n, want := range map[int64]string{40: "40'a", 7: "7'ye", 2: "2'ye", 10: "10'a", 6: "6'ya", 100: "100'e"} {
		if got := numSfx(n, sfxDat); got != want {
			t.Errorf("numSfx(%d) = %q, istenen %q", n, got, want)
		}
	}
}
