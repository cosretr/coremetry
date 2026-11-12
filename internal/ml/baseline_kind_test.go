package ml

// v0.10.2065 — Davis S5 (docs/anomaly/davis-ml-gap.md satır 10): olay kanıtında
// evidence.baselineKind — taban ne: seasonal (aynı takvim sınıfı günler ×
// ±1 sa) | recent (sınıf eşleşmesi < 3 → son günlerin tümü; ya da kırılma
// noktasında önceki seviye) | none (zaman tabanı yok: pod'lar arası /
// ilk kez görülen). Yalnız katlı teknik ayrıntıda gösterilir.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestBaselineKindOf(t *testing.T) {
	loc := time.UTC
	cal := chstore.AnomalyCalendarConfig{}
	wed := time.Date(2026, 11, 11, 10, 0, 0, 0, loc) // çarşamba: 21 günde ≥3 iş günü
	ev := func(det string, at time.Time) chstore.MLEvent {
		return chstore.MLEvent{Detector: det, LastSeen: at.Add(5 * time.Minute).UnixNano(), WindowSec: 300}
	}
	for _, c := range []struct {
		name string
		ev   chstore.MLEvent
		days int
		want string
	}{
		{"iş günü, 21 gün → mevsimsel", ev("op_latency", wed), 21, "seasonal"},
		{"cumartesi, 7 gün → tek cumartesi, tüm günler", ev("log_volume", time.Date(2026, 11, 14, 10, 0, 0, 0, loc)), 7, "recent"},
		{"pod'lar arası", ev("trace_pod_outlier", wed), 21, "none"},
		{"log pod'lar arası", ev("pod_outlier", wed), 21, "none"},
		{"ilk kez görülen şablon", ev("rare_template", wed), 21, "none"},
		{"ilk kez görülen kod", ev("rare_error_code", wed), 21, "none"},
		{"kırılma: önceki seviye", ev("trace_change_point", wed), 21, "recent"},
	} {
		if got := baselineKindOf(c.ev, c.days, cal, loc); got != c.want {
			t.Errorf("%s: %s, beklenen %s", c.name, got, c.want)
		}
	}
	// Dedektör kendi türünü yazdıysa korunur.
	pre := chstore.MLEvent{Detector: "op_latency", Evidence: `{"baselineKind":"none"}`}
	if got := withBaselineKind(pre, 21, cal, loc).Evidence; !strings.Contains(got, `"baselineKind":"none"`) {
		t.Fatalf("var olan tür ezilmemeli: %s", got)
	}
	if got := withBaselineKind(ev("op_latency", wed), 21, cal, loc).Evidence; !strings.Contains(got, `"baselineKind":"seasonal"`) {
		t.Fatalf("kanıta yazılmalı: %s", got)
	}
}

// Teknik ayrıntının "Beklenen" satırı türü söyler; düz cümle değişmez.
func TestBaselineKindTechnical(t *testing.T) {
	base := CardInput{Detector: "log_volume", Signal: chstore.MLSignalLog, BaselineDays: 21}
	for kind, want := range map[string]string{
		"seasonal": "taban: mevsimsel",
		"recent":   "taban: son günlerin tümü",
		"none":     "taban: zaman tabanı yok",
	} {
		in := base
		in.BaselineKind = kind
		if got := technicalOf(in, specFor(in)).Expected; !strings.Contains(got, want) {
			t.Errorf("%s: %q", kind, got)
		}
	}
	if got := technicalOf(base, specFor(base)).Expected; strings.Contains(got, "taban:") {
		t.Errorf("eski olay (tür yok) metni değişmemeli: %q", got)
	}
	ev := chstore.MLEvent{Detector: "log_volume", Signal: chstore.MLSignalLog, Evidence: `{"baselineKind":"recent"}`}
	if CardInputFromEvent(ev, time.Now(), time.UTC).BaselineKind != "recent" {
		t.Fatal("kart girdisi kanıttan okur")
	}
}
