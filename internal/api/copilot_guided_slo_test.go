package api

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// v0.10.808 (dış skill denetimi V4) — guided paketler SLO ile açılır.

func TestRenderSLOStatusTR(t *testing.T) {
	t.Run("tanımsız", func(t *testing.T) {
		out := renderSLOStatusTR(guidedSLOSet{}, "shop")
		if !strings.Contains(out, "tanımlı SLO yok") || !strings.Contains(out, "UYDURMA") {
			t.Errorf("%q", out)
		}
	})
	set := guidedSLOSet{Total: 7, Rows: []guidedSLORow{
		{SLO: chstore.SLO{Name: "availability", Service: "shop", SLIType: "availability", Target: 0.999, WindowDays: 30},
			Status:   &chstore.SLOStatus{Total: 5000, Good: 4990, SLI: 0.998, BudgetRemaining: 0.0, Healthy: false},
			Forecast: &chstore.SLOForecast{BurnRate: 14.4, HoursToExhaust: 3, WillBreachWithin24h: true}},
		{SLO: chstore.SLO{Name: "latency", Service: "shop", SLIType: "latency", Target: 0.95, WindowDays: 7, ThresholdMs: 300, Operation: "GET /cart"},
			Status:   &chstore.SLOStatus{Total: 2000, Good: 1990, SLI: 0.995, BudgetRemaining: 0.62, Healthy: true},
			Forecast: &chstore.SLOForecast{BurnRate: 0.4, SafeBurn: true}},
		{SLO: chstore.SLO{Name: "empty", Service: "shop", Target: 0.99, WindowDays: 30},
			Status: &chstore.SLOStatus{NoData: true, Hint: "Olay yok"}},
		{SLO: chstore.SLO{Name: "broken", Service: "shop", Target: 0.99, WindowDays: 30}, Err: "boom"},
	}}
	out := renderSLOStatusTR(set, "shop")
	for _, want := range []string{
		"SLO DURUMU (shop, 7 tanım)",
		"availability (hedef %99.9 / 30 g): SLI %99.8 · bütçe %0 kaldı · İHLAL · yanma 14.4× (1 sa) · tükenme ~3 sa — 24 SA İÇİNDE",
		"latency (hedef %95 / 7 g, p≤300 ms, op GET /cart): SLI %99.5 · bütçe %62 kaldı · sağlıklı · yanma 0.4× (1 sa) · yanma güvenli",
		"empty (hedef %99 / 30 g): OLAY YOK",
		"broken (hedef %99 / 30 g): durum OKUNAMADI",
		"(+3 SLO daha; ilk 4 gösterildi)",
		"tükenme süresini kendin hesaplama",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("eksik %q\n---\n%s", want, out)
		}
	}
}

func TestPctTR(t *testing.T) {
	for v, want := range map[float64]string{0.999: "99.9", 0.9: "90", 0.99951: "99.951", 1: "100", 0: "0"} {
		if got := pctTR(v); got != want {
			t.Errorf("pctTR(%v)=%q, want %q", v, got, want)
		}
	}
}

// Kablolama: iki paket de SLO adımını service_context'ten ÖNCE çağırır; ajan
// döngüsü prompt'u list_slo_status'u anar.
func TestGuidedBundlesStartWithSLO(t *testing.T) {
	src, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	for _, fn := range []string{"guidedRootCauseBundle(", "guidedServiceHealthBundle("} {
		i := strings.Index(body, "func (s *Server) "+fn)
		if i < 0 {
			t.Fatalf("%s yok", fn)
		}
		j := strings.Index(body[i:], "\nfunc ")
		f := body[i : i+j]
		a, c := strings.Index(f, "s.guidedSLOStep(ctx, emit, &b, service)"), strings.Index(f, `emitGuidedStep(emit, "service_context"`)
		if a < 0 || c < 0 || a > c {
			t.Errorf("%s: SLO adımı service_context'ten önce olmalı (slo=%d ctx=%d)", fn, a, c)
		}
		if !strings.Contains(f, `"SLO durumu + `) {
			t.Errorf("%s: kaynak dizesi SLO durumunu anmalı", fn)
		}
	}
	if !strings.Contains(copilot.SystemPromptChatAgentLoop(), "list_slo_status") {
		t.Error("ajan döngüsü prompt'u list_slo_status'u anmalı")
	}
}
