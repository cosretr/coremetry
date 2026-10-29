package chstore

import (
	"encoding/json"
	"reflect"
	"testing"
)

// v0.10.1495 regresyon tablosu — v0.10.1577'de api'den taşındı (karar artık
// burada; sentezleyici de okuyor). Operatör (prod): `runtime:jvm-gc:<svc>:<pod>`
// problemi "Where errors concentrate" gösterdi; problem hatayla ilgili değildi.
// Kıyas bölümü problem TÜRÜNDEN seçilir. Servis adları sentetik.
func TestProblemBubbleModeByKind(t *testing.T) {
	cases := []struct {
		name string
		p    *Problem
		want BubbleMode
	}{
		{"hata oranı kuralı", &Problem{ID: "r1", RuleID: "r1", Service: "orders-api", Metric: "error_rate"}, BubbleModeErrors},
		{"http hata oranı", &Problem{ID: "r2", Service: "orders-api", Metric: "http_error_rate"}, BubbleModeErrors},
		{"p99 gecikme — hata dili YOK", &Problem{ID: "r3", Service: "orders-api", Metric: "p99_ms"}, BubbleModeWindow},
		{"trafik", &Problem{ID: "r4", Service: "orders-api", Metric: "request_rate"}, BubbleModeWindow},
		{"yaygın yavaşlama", &Problem{ID: "svc-slowdown:orders-api", Service: "orders-api", Metric: "p99_ms"}, BubbleModeWindow},
		{"servis türü açıkça", &Problem{ID: "r5", Service: "orders-api", Kind: ProblemKindService, Metric: "error_rate"}, BubbleModeErrors},
		{"prod: JVM GC tek pod", &Problem{ID: "runtime:jvm-gc:core-api:core-api-7d9f-abcde", RuleID: "runtime:jvm-gc:core-api:core-api-7d9f-abcde",
			Service: "core-api", Pod: "core-api-7d9f-abcde", Metric: "jvm_gc_pause_ratio"}, BubbleModeNone},
		{"runtime ailesi rule id ile", &Problem{ID: "x", RuleID: "runtime:heap:core-api", Service: "core-api", Metric: "heap_used_pct"}, BubbleModeNone},
		{"runtime ama metrik adı error içerse de none", &Problem{ID: "runtime:thread-errors:core-api", Service: "core-api", Metric: "thread_error_count"}, BubbleModeNone},
		{"db kapasite", &Problem{ID: "db-capacity:db1", Service: "db.example.test", Kind: "database", Metric: "conn_pct"}, BubbleModeNone},
		{"db sağlık", &Problem{ID: "db-health:x", Service: "orders-api", Metric: "error_count"}, BubbleModeNone},
		{"monitor", &Problem{ID: "monitor:abc", Service: "orders-api", Metric: "up"}, BubbleModeNone},
		{"self-health", &Problem{ID: "self-ingest", Service: "coremetry", Metric: "lag"}, BubbleModeNone},
		{"servissiz özne", &Problem{ID: "exception-storm", Metric: "error_count"}, BubbleModeNone},
		{"dış kaynak türü", &Problem{ID: "ext", Service: "orders-api", Kind: ProblemKindExternal, Metric: "error_rate"}, BubbleModeNone},
		{"nil", nil, BubbleModeNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ProblemBubbleMode(c.p); got != c.want {
				t.Errorf("ProblemBubbleMode = %q, want %q", got, c.want)
			}
		})
	}
}

// v0.7.51 tablosu (api'den taşındı, v0.10.1577) — metrik → örnek trace türü;
// aynı hata-lık kapısı BubbleUp kıyasının hata alt kümesini seçer.
func TestExemplarKindForMetric(t *testing.T) {
	tests := []struct {
		metric string
		want   ExemplarKind
	}{
		{"error_rate", ExemplarError},
		{"errors", ExemplarError},
		{"5xx_error_pct", ExemplarError},
		{"ERROR_RATE", ExemplarError},
		{"p99_ms", ExemplarSlow},
		{"p95_latency", ExemplarSlow},
		{"duration_ms", ExemplarSlow},
		{"latency", ExemplarSlow},
		{"request_rate", ExemplarAny},
		{"throughput", ExemplarAny},
		{"", ExemplarAny},
	}
	for _, tt := range tests {
		if got := ExemplarKindForMetric(tt.metric); got != tt.want {
			t.Errorf("ExemplarKindForMetric(%q) = %q, want %q", tt.metric, got, tt.want)
		}
	}
}

// v0.10.1577 — DeepEvidence.BubbleUp JSON gidiş-dönüş; eski satırın JSON'u
// (alan yok) nil verir; Result yalnız ok durumunda.
func TestDeepEvidenceBubbleUpJSON(t *testing.T) {
	in := DeepEvidence{BubbleUp: &BubbleUpEvidence{Mode: "errors", AnchorStartNs: 1, FromNs: 1, ToNs: 2, ComputedNs: 3, Attempts: 1,
		Status: BubbleUpStatusOK, SelectionTotal: 40, BaselineTotal: 900,
		Attributes: []BubbleUpAttribute{{Key: "http.route", Values: []BubbleUpValue{{Value: "/v1/pay-now", SelectionPct: 0.8, Score: 0.69}}}}}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out DeepEvidence
	if err := json.Unmarshal(b, &out); err != nil || !reflect.DeepEqual(in, out) {
		t.Fatalf("gidiş-dönüş: %v\n%s", err, b)
	}
	var legacy DeepEvidence
	if err := json.Unmarshal([]byte(`{"checked":[{"family":"logs","found":false,"detail":"x","records":0}]}`), &legacy); err != nil || legacy.BubbleUp != nil {
		t.Fatalf("eski JSON → nil: %v %+v", err, legacy.BubbleUp)
	}
	if b, _ := json.Marshal(DeepEvidence{}); string(b) != "{}" {
		t.Errorf("boş kanıt alan eklememeli (omitempty): %s", b)
	}
	if r := in.BubbleUp.Result(); r == nil || r.SelectionTotal != 40 || len(r.Attributes) != 1 {
		t.Errorf("ok → sonuç: %+v", r)
	}
	if (&BubbleUpEvidence{Status: BubbleUpStatusUnreadable, SelectionTotal: 1}).Result() != nil || (*BubbleUpEvidence)(nil).Result() != nil {
		t.Error("okunamadı / nil → sonuç yok")
	}
}
