package chstore

// v0.10.1792 — Davis paritesi FAZ 1.4: yönlü olayın okuyucu sözleşmesi.
// Belirti (1587 tuzağı): "taban yok" ratio=0 ile kodlanıyordu ve "tabana göre
// SIFIRA düştü" ile ayırt edilemiyordu. Yeni satır baseline_kind'i açık
// taşır; eski satır (baseline_kind '') eski yorumla okunur — geri uyum.

import (
	"math"
	"testing"
)

func TestAnomalyBaselineless(t *testing.T) {
	cases := []struct {
		name string
		ev   AnomalyEvent
		want bool
	}{
		{"eski satır, oran 0 → tabansız (eski yorum)", AnomalyEvent{PeakRatio: 0}, true},
		{"eski satır, oran 5 → tabanlı", AnomalyEvent{PeakRatio: 5}, false},
		{"yeni none → tabansız", AnomalyEvent{PeakRatio: 0, BaselineKind: AnomalyBaselineNone}, true},
		{"yeni seasonal, oran 0 → SIFIRA düştü, tabansız DEĞİL", AnomalyEvent{PeakRatio: 0, BaselineKind: AnomalyBaselineSeasonal}, false},
		{"yeni consecutive, oran 0 → tabansız değil", AnomalyEvent{PeakRatio: 0, BaselineKind: AnomalyBaselineConsecutive}, false},
	}
	for _, c := range cases {
		if got := AnomalyBaselineless(c.ev); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestIsCountOnlyAnomalyBaselineKind(t *testing.T) {
	cases := []struct {
		name string
		ev   AnomalyEvent
		want bool
	}{
		{"eski trace_op oran 0", AnomalyEvent{Kind: "trace_op", PeakRatio: 0}, true},
		{"eski log_pattern oran 0", AnomalyEvent{Kind: "log_pattern", PeakRatio: 0}, true},
		{"yeni trace_op none", AnomalyEvent{Kind: "trace_op", PeakRatio: 0, BaselineKind: AnomalyBaselineNone}, true},
		{"yeni trace_op consecutive oran 3", AnomalyEvent{Kind: "trace_op", PeakRatio: 3, BaselineKind: AnomalyBaselineConsecutive}, false},
		// Aynı bölümde önce oranlı sonra sayım-yalnız tik: tepe gerçek kalır.
		{"none ama tepe oran 5 (taşınmış)", AnomalyEvent{Kind: "trace_op", PeakRatio: 5, BaselineKind: AnomalyBaselineNone}, false},
		{"tabanlı ama oran 0", AnomalyEvent{Kind: "trace_op", PeakRatio: 0, BaselineKind: AnomalyBaselineSeasonal}, false},
		{"başka tür none", AnomalyEvent{Kind: "log_template_new", PeakRatio: 0, BaselineKind: AnomalyBaselineNone}, false},
	}
	for _, c := range cases {
		if got := IsCountOnlyAnomaly(c.ev); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAnomalyRatioKnownAndDirection(t *testing.T) {
	if AnomalyRatioKnown(AnomalyEvent{PeakRatio: 0}) || AnomalyRatioKnown(AnomalyEvent{PeakRatio: 4, BaselineKind: AnomalyBaselineNone}) ||
		AnomalyRatioKnown(AnomalyEvent{PeakRatio: math.NaN()}) {
		t.Error("tabansız / NaN oran bilinen sayıldı")
	}
	if !AnomalyRatioKnown(AnomalyEvent{PeakRatio: 4}) || !AnomalyRatioKnown(AnomalyEvent{PeakRatio: 0.3, BaselineKind: AnomalyBaselineSeasonal}) {
		t.Error("tabanlı oran bilinmiyor sayıldı")
	}
	for in, want := range map[float64]string{0: "", -1: "", 0.4: "down", 1: "up", 7: "up"} {
		if got := AnomalyDirectionOfRatio(in); got != want {
			t.Errorf("oran %v → %q, want %q", in, got, want)
		}
	}
	if !IsSubjectAnomaly(AnomalyEvent{Kind: SubjectAnomalyKind}) || IsSubjectAnomaly(AnomalyEvent{Kind: "trace_op"}) {
		t.Error("IsSubjectAnomaly")
	}
}

func TestStandardizeAnomalyEventBaseline(t *testing.T) {
	base := StandardizeAnomalyEvent(AnomalyEvent{Kind: "trace_op", Service: "svc", Pattern: "GET /x", PeakRatio: 0, BaselineKind: AnomalyBaselineNone}, StdFacts{})
	if base.Impact.Known || base.Impact.Primary.Text != "taban yok (ilk görülme)" {
		t.Errorf("tabansız etki: %+v", base.Impact)
	}
	legacy := StandardizeAnomalyEvent(AnomalyEvent{Kind: "trace_op", Service: "svc", PeakRatio: 0}, StdFacts{})
	if legacy.Impact.Known || legacy.Impact.Primary.Text != "taban yok (ilk görülme)" {
		t.Errorf("eski satır (ratio 0) geri uyum: %+v", legacy.Impact)
	}
	ratio := StandardizeAnomalyEvent(AnomalyEvent{Kind: "trace_op", Service: "svc", PeakRatio: 6, BaselineKind: AnomalyBaselineConsecutive}, StdFacts{})
	if !ratio.Impact.Known {
		t.Errorf("oranlı olay bilinmiyor: %+v", ratio.Impact)
	}
	subj := StandardizeAnomalyEvent(AnomalyEvent{Kind: SubjectAnomalyKind, Service: "checkout", Pattern: "p99_ms @ /cart", PeakRatio: 3}, StdFacts{})
	if subj.Family != StdFamilyLatency {
		t.Errorf("özne olayı ailesi %s", subj.Family)
	}
}
