package api

// trace_bundle_hint_test.go — v0.10.1889 (Log Search 3.6, audit §4): bundle
// `hint` unix saniye, dakikaya yuvarlanır; çöp / ≤0 / bir günden ileri =
// ipucu yok (anahtar kardinalitesi sınırlı, yanlış birim sessizce kabul
// edilmez).

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseBundleHint(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		raw  string
		want time.Time
	}{
		{"", time.Time{}},
		{"abc", time.Time{}},
		{"0", time.Time{}},
		{"-5", time.Time{}},
		{"1762430459", time.Unix(1762430459, 0).Truncate(time.Minute)},
		{" 1762430400 ", time.Unix(1762430400, 0)},
		{"1762430459000", time.Time{}}, // ms yanlışlıkla → gelecekte, yok sayılır
	}
	for _, c := range cases {
		if got := parseBundleHint(c.raw, now); !got.Equal(c.want) {
			t.Errorf("parseBundleHint(%q) = %v, beklenen %v", c.raw, got, c.want)
		}
	}
}

// İpucu CH yoluna gerçekten ulaşır (test edilmiş ama ulaşılamaz sınıfı).
func TestBundleHintReachesResolver(t *testing.T) {
	b, _ := os.ReadFile("trace_bundle.go")
	if !strings.Contains(string(b), "s.traceDetailPayload(ctx, id, hint)") {
		t.Error("bundle ipucunu traceDetailPayload'a geçirmeli")
	}
	r, _ := os.ReadFile("trace_routes.go")
	if !strings.Contains(string(r), "s.resolveTraceSpansNear(ctx, id, hint)") {
		t.Error("traceDetailPayload ipucunu resolveTraceSpansNear'a geçirmeli")
	}
	z, _ := os.ReadFile("trace_resolve.go")
	if !strings.Contains(string(z), "s.store.GetTraceNear(ctx, id, hint)") {
		t.Error("resolveTraceSpansNear CH yolunda GetTraceNear çağırmalı")
	}
}
