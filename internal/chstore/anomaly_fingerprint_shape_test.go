package chstore

import (
	"strings"
	"testing"
)

// anomaly_fingerprint_shape_test.go — v0.10.1042. Susturma yazımı istemcinin
// gönderdiği olay kimliğini yalnız FingerprintAnomaly'nin ürettiği ŞEKİLDEYSE
// saklar (api silenceFingerprint). Şekil tahmin edilmez, üreticiden çivilenir:
// her üretim IsAnomalyFingerprint'ten geçmeli, biçimi bozuk her değer kalmalı.

func TestFingerprintAnomalyOutputsAreWellFormed(t *testing.T) {
	inputs := [][3]string{
		{"log_pattern", "ORA-00001", "svc-a"},
		{"trace_op", "POST /v1/charges", "payments-orchestrator"},
		{"trace_op_latency", "GET /x", "svc-b"},
		{"log_template_new", "tpl-7f3a", "svc-c"},
		{"behavior_change", "request_rate", "orders-batch"},
		{"elastic_ml", "job:detector", "svc-d"},
		{"", "", ""},
		{"k", "ünicode · ğüşıöç", "svc|with|pipes"},
	}
	for _, in := range inputs {
		fp := FingerprintAnomaly(in[0], in[1], in[2])
		if !IsAnomalyFingerprint(fp) {
			t.Errorf("FingerprintAnomaly%v = %q şekil yüklemini geçmiyor", in, fp)
		}
		if len(fp) != anomalyFingerprintLen {
			t.Errorf("uzunluk %d, sabit %d", len(fp), anomalyFingerprintLen)
		}
	}
}

func TestIsAnomalyFingerprintRejectsMalformed(t *testing.T) {
	good := FingerprintAnomaly("trace_op", "GET /x", "svc")
	for name, v := range map[string]string{
		"boş":            "",
		"düz anahtar":    "trace_op|GET /x|svc",
		"büyük harf":     strings.ToUpper(good),
		"bir kısa":       good[:len(good)-1],
		"bir uzun":       good + "0",
		"tam sha1 (40)":  strings.Repeat("a", 40),
		"hex olmayan":    "zzzzzzzzzzzzzzzz",
		"baş boşluk":     " " + good[1:],
		"son satır sonu": good[:len(good)-1] + "\n",
		"kısa hex (6)":   "abc123",
	} {
		if IsAnomalyFingerprint(v) {
			t.Errorf("%s: %q kimlik sayıldı", name, v)
		}
	}
	if !IsAnomalyFingerprint(good) {
		t.Fatalf("üretilmiş kimlik %q reddedildi", good)
	}
}
