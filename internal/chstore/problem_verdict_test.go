package chstore

import (
	"strings"
	"testing"
	"time"
)

// v0.10.1015 — Problems sekmesinde öğretme: karar İMZAYA bağlıdır ve ortak
// durum tablosunda (saved_views, page="problem-verdict") saklanır.

func TestValidProblemSignature(t *testing.T) {
	ok := []string{
		"p:rule-high-error-rate|payments-api",
		"e:9f8a7c6d5e4b3a21",
		"a:latency|orders-api|p99 up",
		"p:anomaly:ext:oracle-prod/OP_A/E1/MOB/-:ext:error_count|eft-svc",
	}
	bad := []string{"", "p:", "p:   ", "x:abc", "rule|svc", "e:" + strings.Repeat("a", 400), "p:a\nb", "e:\x00"}
	for _, s := range ok {
		if !ValidProblemSignature(s) {
			t.Errorf("geçerli olmalı: %q", s)
		}
	}
	for _, s := range bad {
		if ValidProblemSignature(s) {
			t.Errorf("geçersiz olmalı: %q", s)
		}
	}
	if !ValidProblemVerdict("real") || !ValidProblemVerdict("noise") || ValidProblemVerdict("") || ValidProblemVerdict("anomaly") {
		t.Error("karar yalnız real | noise")
	}
}

func TestProblemVerdictRowRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)
	if got := problemVerdictID("e:abc"); got != "pv:e:abc" {
		t.Fatalf("satır kimliği: %q", got)
	}
	q := `{"signature":"p:rule-1|payments-api","label":"Error rate > 5%","kind":"problem","service":"payments-api","by":"op@example.test"}`
	v, ok := problemVerdictFromRow("noise", q, at)
	if !ok || v.Signature != "p:rule-1|payments-api" || v.Verdict != "noise" || v.Label != "Error rate > 5%" || v.Kind != "problem" || v.Service != "payments-api" || v.By != "op@example.test" || v.At != at.UnixNano() {
		t.Fatalf("satır → karar: %+v ok=%v", v, ok)
	}
	// Silinmiş (ad boş), tanınmayan karar, bozuk gövde, geçersiz imza → atlanır.
	for name, row := range map[string][2]string{
		"mezar taşı":       {"", q},
		"bilinmeyen karar": {"maybe", q},
		"bozuk JSON":       {"real", `{"signature":`},
		"geçersiz imza":    {"real", `{"signature":"x:1"}`},
	} {
		if _, ok := problemVerdictFromRow(row[0], row[1], at); ok {
			t.Errorf("%s: karar sayılmamalı", name)
		}
	}
}
