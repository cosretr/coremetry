package api

// logs_fieldstats_sample_test.go — v0.10.1852 (Log Search Faz 2 dilim 2.1).
// `est` → örnekleme basamağı; lift ve yeteneksiz backend (CH log yolu) için
// kapalı; anahtar eki yalnız örneklemede (örneklemesiz anahtar bayt-bayt aynı —
// eski önbellek girdileri ve CH yanıtı değişmez), basamaklar birbirini zehirlemez.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestLogsFieldStatsSampleProbability(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		lift    bool
		capable bool
		want    float64
	}{
		{"est yok", "", false, true, 0},
		{"bozuk", "abc", false, true, 0},
		{"negatif", "-5", false, true, 0},
		{"hedefin altı", "150000", false, true, 0},
		{"1M → 0.2", "1000000", false, true, 0.2},
		{"boşluklu", " 2000000 ", false, true, 0.1},
		{"lift açık", "1000000", true, true, 0},
		{"CH (yetenek yok)", "1000000", false, false, 0},
	}
	for _, c := range cases {
		if got := logsFieldStatsSampleProbability(c.raw, c.lift, c.capable); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLogsFieldStatsSampleKey(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	f := logstore.Filter{Service: "orders-api", From: now.Add(-time.Hour), To: now}
	base := logsFieldStatsKeyLift("CHANNEL_CODE", f, "a", "b", 5, false)
	if got := base + logsFieldStatsSampleKey(0); got != base {
		t.Fatalf("örneklemesiz anahtar değişmemeli: %q", got)
	}
	a := base + logsFieldStatsSampleKey(0.2)
	b := base + logsFieldStatsSampleKey(0.1)
	if a == base || b == base || a == b {
		t.Fatalf("basamaklar ayrı anahtar olmalı: %q %q %q", base, a, b)
	}
}
