package anomaly

// v0.10.1588 — Davis paritesi FAZ 0.5 / D (operatör kararı 7 + soru 6): dış
// kaynak Problem'leri (seri, küme, tavan, kaynak erişilemiyor) CANLI
// tarayıcıda incident'a bağlanır; gölge tarayıcı (notifier nil — Oracle gölge
// kipi) BAĞLAMAZ (incident açılışı bildirim gönderirdi). Belirti (FP sınıfı
// h_duplicate_wave): aynı olaya N ayrı satır, incident katlaması yok.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestExternalOpenAttachesOnlyLiveScanner(t *testing.T) {
	cfg := chstore.DefaultAnomalySensitivity()
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	vals := append(baselineVals(30), repeat(60, cfg.DwellBuckets)...)
	run := func(live bool, sens chstore.AnomalySensitivityConfig) *fakeExtStore {
		f := &fakeExtStore{cfg: sens, series: []chstore.SpanMetricSeries{extSeries(vals, now, "OP1", "E1")}}
		s := newExtScanner(f, now)
		s.live = live
		if _, err := s.Scan(context.Background(), extTarget); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < externalDownAfter; i++ {
			s.ReportSourceHealth(context.Background(), "o-1", "extsrc", "timeout", now.Add(time.Duration(i)*time.Minute))
		}
		return f
	}
	f := run(true, cfg)
	if len(f.attached) != 2 {
		t.Fatalf("canlı tarayıcı: seri açılışı + kaynak erişilemiyor bağlanmalı, %d bağ: %+v", len(f.attached), f.attached)
	}
	if f.attached[0].RuleID != "anomaly:ext:extsrc/OP1/E1:ext:fail_count" {
		t.Fatalf("ilk bağ seri Problem'i olmalı: %s", f.attached[0].RuleID)
	}
	if f := run(false, cfg); len(f.attached) != 0 {
		t.Fatalf("gölge tarayıcı bağlamamalı: %+v", f.attached)
	}
	off := false
	noAttach := cfg
	noAttach.AttachToIncident = &off
	if f := run(true, noAttach); len(f.attached) != 0 {
		t.Fatalf("attachToIncident kapalıyken bağlamamalı: %+v", f.attached)
	}
	if NewExternalScanner(&fakeExtStore{}, nil).live {
		t.Fatal("notifier nil → gölge")
	}
}

// Fırtına açılışı vidayla kapılı bağlanır (kablolama pini; çekirdek chstore'da).
func TestExceptionStormAttachWiring(t *testing.T) {
	b, err := os.ReadFile("exception_storm.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	open := strings.Index(s, "[exception-storm] AÇILDI")
	gate := strings.Index(s, "if cfg.StormAttachesToIncident() {")
	call := strings.Index(s, "d.store.AttachProblemToIncident(ctx, p)")
	if open < 0 || gate < 0 || call < 0 || !(open < gate && gate < call) {
		t.Fatalf("fırtına: açılıştan sonra vidayla kapılı bağlama bekleniyordu (%d %d %d)", open, gate, call)
	}
}
