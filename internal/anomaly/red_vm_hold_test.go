package anomaly

// red_vm_hold_test.go — operatör 2026-10-24: "VM serisi varken örneklenmiş
// span karar kaynağı olmasın". error_rate'te VM servisi biliyorsa span'e
// düşülmez (Hold); mevsimsel ısınma VM'den çıkarmaz; VM-yalnız servis
// değerlendirilir; explain replay'i boş route'lu gateway rampasını VM'den bulur.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const gwSvc = "apigateway-mobile-prod"

// gwVal — sahte VM: gateway senaryosu (servis düzeyi toplam — route boyutu
// sorguda yok, boş http_route serisi de aynı toplama girer).
func gwVal(sc gwScenario) func(svc, probe string, k vmREDKind, b int64) (float64, bool) {
	return func(svc, probe string, k vmREDKind, b int64) (float64, bool) {
		if svc != gwSvc || probe != vmREDFamilies[0].Probe {
			return 0, false
		}
		r := sc.at(b)
		c := r.rate * bucketSeconds
		switch k {
		case vmKindCount:
			return c, true
		case vmKindErr:
			return r.v / 100 * c, true
		}
		return 80, true
	}
}

func gwFake(sc gwScenario) *fakeREDVM {
	return &fakeREDVM{labels: map[string][]string{vmREDFamilies[0].Probe: {"service_name", "http_response_status_code", "http_route", "le"}},
		statusSvcs: []string{gwSvc}, val: gwVal(sc), svcs: []string{gwSvc}}
}

func TestStrictErrorRateNeverFallsBackToSpan(t *testing.T) {
	now := incidentDay.Add(7*time.Hour + explainTickOffset)
	sens := chstore.DefaultAnomalySensitivity()
	base := warm(t, newFakeSource(gwFake(gwScenario{baseRatio: 0.05})), now, sens)
	clone := func() *vmREDView { c := *base; return &c }

	if _, u := base.seriesFor(gwSvc, "error_rate", 3); !u.VM {
		t.Fatalf("ısınmış görünümde VM bekleniyordu: %+v", u)
	}
	cases := []struct {
		name     string
		edit     func(v *vmREDView)
		wantVM   bool
		wantHold bool
		wantSeas bool
	}{
		{"hata türü okunamadı → hold (span DEĞİL)", func(v *vmREDView) {
			v.readyConsec[0][vmKindErr], v.readyDet[0][vmKindErr], v.failed[0][vmKindErr] = false, false, true
		}, false, true, false},
		{"sayım ısınıyor → hold", func(v *vmREDView) {
			v.readyConsec[0][vmKindCount], v.readyDet[0][vmKindCount] = false, false
		}, false, true, false},
		{"yalnız mevsimsel eksik → VM, mevsimsel taban yok", func(v *vmREDView) {
			v.readyDet[0][vmKindErr] = false
		}, true, false, false},
		{"durum kümesi okunamadı → hold", func(v *vmREDView) {
			v.meta = append([]vmFamMeta(nil), v.meta...)
			v.meta[0].statusSvc = nil
		}, false, true, false},
		{"servisin durum etiketi yok → span (VM ölçemez)", func(v *vmREDView) {
			v.meta = append([]vmFamMeta(nil), v.meta...)
			v.meta[0].statusSvc = map[string]bool{}
		}, false, false, false},
		{"tam hazır → VM + mevsimsel", func(*vmREDView) {}, true, false, true},
	}
	for _, c := range cases {
		v := clone()
		c.edit(v)
		s, u := v.seriesFor(gwSvc, "error_rate", 3)
		if u.VM != c.wantVM || u.Hold != c.wantHold || (s.Seasonal != nil) != c.wantSeas {
			t.Errorf("%s: %+v (mevsimsel %d)", c.name, u, len(s.Seasonal))
		}
		if c.wantHold && !strings.HasPrefix(redSourceEvidenceTR(u), "kaynak: VictoriaMetrics — bu tik beklemede") {
			t.Errorf("%s: kanıt %q", c.name, redSourceEvidenceTR(u))
		}
	}
	// v0.10.1534 (Problem standardı R6, D12) — p99_ms ve request_rate de bu
	// kurala tabi: VM servisi biliyorsa ısınmada span'e DÜŞÜLMEZ (hold).
	v := clone()
	v.readyConsec[0][vmKindP99], v.readyDet[0][vmKindP99] = false, false
	if _, u := v.seriesFor(gwSvc, "p99_ms", 3); u.VM || !u.Hold {
		t.Errorf("p99 ısınmada bekletilmeli (D12): %+v", u)
	}
	v = clone()
	v.readyDet[0][vmKindP99] = false // yalnız mevsimsel eksik → VM'de kalır
	if _, u := v.seriesFor(gwSvc, "p99_ms", 3); !u.VM {
		t.Errorf("p99 mevsimsel ısınmada VM'de kalmalı: %+v", u)
	}
	v = clone()
	v.readyConsec[0][vmKindCount], v.readyDet[0][vmKindCount] = false, false
	if _, u := v.seriesFor(gwSvc, "request_rate", 3); u.VM || !u.Hold {
		t.Errorf("request_rate ısınmada bekletilmeli (D12): %+v", u)
	}
	if _, u := base.seriesFor(gwSvc, "request_rate", 3); !u.VM {
		t.Errorf("hazır görünümde request_rate VM'den: %+v", u)
	}
	// VM'in hiç bilmediği servis: span (karar span'de).
	if _, u := base.seriesFor("legacy-svc", "error_rate", 3); u.VM || u.Hold {
		t.Errorf("VM'in bilmediği servis span olmalı: %+v", u)
	}
}

// TestOverlayHoldDropsSpanSeries — bekletilen çiftin span serileri silinir.
func TestOverlayHoldDropsSpanSeries(t *testing.T) {
	buckets := map[string]map[string][]float64{"error_rate": {"a": {1, 2}}}
	rates := map[string]map[string][]float64{"error_rate": {"a": {1, 1}}}
	seas := map[string]map[string][]float64{"error_rate": {"a": {1}}}
	used := overlayREDSeries(holdProvider{}, []string{"a"}, []string{"error_rate"}, 3, buckets, seas, rates, map[string]map[string][]float64{})
	if !used["a|error_rate"].Hold || buckets["error_rate"]["a"] != nil || rates["error_rate"]["a"] != nil || seas["error_rate"]["a"] != nil {
		t.Fatalf("hold span serilerini silmeli: %+v %v", used, buckets)
	}
	counts, fb := redSourceCounts(used)
	if counts["error_rate"]["hold"] != 1 || fb["error_rate"]["hold: x"].Count != 1 || fb["error_rate"]["hold: x"].Services[0] != "a" {
		t.Errorf("durum sayımı: %v %v", counts, fb)
	}
}

type holdProvider struct{}

func (holdProvider) seriesFor(string, string, int) (vmSeries, redSourceUsed) {
	return vmSeries{}, redSourceUsed{Hold: true, Reason: "x"}
}

// TestVMOnlyServiceIsEvaluated — span MV'sinde olmayan servis listeye girer.
func TestVMOnlyServiceIsEvaluated(t *testing.T) {
	now := incidentDay.Add(7*time.Hour + explainTickOffset)
	v := warm(t, newFakeSource(gwFake(gwScenario{baseRatio: 0.05})), now, chstore.DefaultAnomalySensitivity())
	got := v.vmServices()
	if len(got) != 1 || got[0] != gwSvc {
		t.Fatalf("VM servisleri: %v", got)
	}
	merged, added := mergeServices([]string{"orders-api"}, got)
	if added != 1 || len(merged) != 2 || merged[0] != "orders-api" || merged[1] != gwSvc {
		t.Errorf("birleşim: %v (+%d)", merged, added)
	}
	if m, a := mergeServices([]string{gwSvc}, got); a != 0 || len(m) != 1 {
		t.Errorf("zaten olan servis eklenmemeli: %v", m)
	}
	var nilView *vmREDView
	if nilView.vmServices() != nil {
		t.Error("nil görünüm boş liste")
	}
}

// TestExplainReplayBFFFromVM — operatörün penceresi replay: servis span
// MV'sinde YOK, seriler yalnız VM'de (boş route'lu gateway); kural rampayı
// VM kaynağından tek Problem olarak açar ve gerekçeleri adım adım yazar.
func TestExplainReplayBFFFromVM(t *testing.T) {
	sens := chstore.DefaultAnomalySensitivity()
	days, minS, neighbor := seasonalDays, seasonalMinSamples, seasonalNeighborBuckets
	from, to := incidentDay.Add(3*time.Hour), incidentDay.Add(9*time.Hour)
	noSpan := func(context.Context, string, string, time.Time, time.Time) (map[int64]spanRow, error) {
		return nil, nil
	}
	rep := explainRun(context.Background(), gwFake(gwScenario{baseRatio: 0.05, incident: true}), noSpan, gwSvc, "error_rate",
		from, to, sens, days, minS, neighbor, true)
	if rep.SpanPresent || !rep.VMPresent || !rep.VMConfigured {
		t.Fatalf("span yok / VM var bekleniyordu: %+v", rep)
	}
	if rep.Problems != 1 || rep.FirstOpenAt == 0 {
		t.Fatalf("tek açılış bekleniyordu: %d açılış, verdict %q, blockers %v", rep.Problems, rep.Verdict, rep.TopBlockers)
	}
	var first ExplainStep
	for _, s := range rep.Steps {
		if s.At == rep.FirstOpenAt {
			first = s
		}
		if s.Source != redSourceVM {
			t.Fatalf("her adım VM olmalı: %+v", s)
		}
	}
	if first.OpenedBy != "error_ramp" || first.ZBlocker != zBlockAbsFloor {
		t.Errorf("ilk açılış: %+v", first)
	}
	if !strings.Contains(rep.Verdict, "error_ramp") || !strings.Contains(rep.Verdict, "vm") {
		t.Errorf("verdict: %q", rep.Verdict)
	}
	if rep.VMQueries == 0 || rep.VMQueries > explainMaxVMQueries+4 {
		t.Errorf("VM sorgu sayısı sınırlı olmalı: %d", rep.VMQueries)
	}
	// Kural kapalıyken aynı pencere: açılmaz ve kapı absFloor olarak raporlanır.
	off := sens
	off.ErrorRamp.Enabled = boolPtrT(false)
	rep2 := explainRun(context.Background(), gwFake(gwScenario{baseRatio: 0.05, incident: true}), noSpan, gwSvc, "error_rate",
		from, to, off, days, minS, neighbor, true)
	if rep2.Problems != 0 || len(rep2.TopBlockers) == 0 || !strings.HasPrefix(rep2.TopBlockers[0], zBlockAbsFloor) {
		t.Errorf("kural kapalı: %d açılış, kapılar %v", rep2.Problems, rep2.TopBlockers)
	}
}

func TestNormalizeExplainWindow(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 3, 0, 0, time.UTC)
	f, to := NormalizeExplainWindow(time.Time{}, time.Time{}, now)
	if to != now.Truncate(5*time.Minute) || to.Sub(f) != explainDefaultWindow {
		t.Errorf("varsayılan: %v %v", f, to)
	}
	f, to = NormalizeExplainWindow(now.Add(-48*time.Hour), now.Add(time.Hour), now)
	if to.Sub(f) != explainMaxWindow {
		t.Errorf("tavan: %v", to.Sub(f))
	}
}

func TestBuildExplainSpanQueryShape(t *testing.T) {
	q := buildExplainSpanQuery("x")
	for _, want := range []string{"FROM service_summary_5m", "service_name = ?", "time_bucket >= ?", "time_bucket < ?", "LIMIT", "max_execution_time"} {
		if !strings.Contains(q, want) {
			t.Errorf("%q yok: %s", want, q)
		}
	}
	if strings.Contains(q, "FROM spans") {
		t.Error("ham spans okunmamalı")
	}
}
