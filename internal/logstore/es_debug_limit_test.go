package logstore

import (
	"testing"
	"time"
)

// v0.10.918 — aynı trace'in span aramaları tek teşhis satırı üretir.
func TestESDebugLimiter(t *testing.T) {
	l := &esDebugLimiter{}
	t0 := time.Date(2026, 9, 25, 17, 25, 43, 0, time.UTC)
	k1 := esDebugKey(Filter{TraceID: "abc", SpanID: "s1"})
	k2 := esDebugKey(Filter{TraceID: "abc", SpanID: "s2"})
	if k1 != k2 {
		t.Fatalf("span'lar aynı trace anahtarına düşmeli: %q %q", k1, k2)
	}
	if !l.allow(k1, t0) || l.allow(k2, t0.Add(time.Second)) {
		t.Fatal("ilk satır yazılmalı, aynı trace'in ikincisi bastırılmalı")
	}
	if !l.allow(esDebugKey(Filter{TraceID: "def"}), t0) {
		t.Fatal("farklı trace kendi satırını almalı")
	}
	if !l.allow(k1, t0.Add(esDebugEvery)) {
		t.Fatal("pencere dolunca tekrar yazılmalı")
	}
	if esDebugKey(Filter{Service: "svc"}) != "s:svc" {
		t.Fatal("servis anahtarı")
	}
}
