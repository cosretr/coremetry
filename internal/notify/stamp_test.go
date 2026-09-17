package notify

// stamp_test.go — v0.10.758: damga sunucu diliminde ve etiketli; hiçbir
// şablonda ham UTC RFC3339 / "15:04 MST" kalmadı (pin).

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestStampIn(t *testing.T) {
	ns := time.Date(2026, 9, 16, 22, 30, 0, 0, time.UTC).UnixNano()
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	if got := stampIn(ns, ist); got != "17.09.2026 01:30:00 Europe/Istanbul" {
		t.Errorf("İstanbul damgası: %q", got)
	}
	if got := stampIn(ns, nil); got != "16.09.2026 22:30:00 UTC" {
		t.Errorf("nil → UTC etiketli: %q", got)
	}
	if got := clockIn(ns, ist); got != "01:30 Europe/Istanbul" {
		t.Errorf("kısa şekil: %q", got)
	}
}

func TestNoRawUTCStampsLeftInTemplates(t *testing.T) {
	src, err := os.ReadFile("notify.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if strings.Contains(s, "p.StartedAt).UTC().Format(") {
		t.Fatal("notify.go'da ham UTC StartedAt damgası kaldı — notifyStamp/notifyClock kullanılmalı")
	}
	if n := strings.Count(s, "notifyStamp(p.StartedAt)"); n < 5 {
		t.Fatalf("notifyStamp çağrısı %d (< 5 şablon)", n)
	}
	if !strings.Contains(s, "notifyClock(p.StartedAt)") {
		t.Fatal("WhatsApp kısa damgası notifyClock olmalı")
	}
}
