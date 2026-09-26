package logstore

import (
	"encoding/json"
	"strings"
	"testing"
)

// v0.10.944 — rol süzgeçleri (CoSRE search_logs): yapılandırılmış alan
// KAZANIR (yalnız o alan sorulur), yapılandırma yoksa bugünkü aday
// listesi; namespace yeni yapısal clause (esNamespaceFields — `namespace:`
// kısaltmasıyla tek sözleşme).

func newTestESStore(fields ESFieldMap) *ESStore {
	s := &ESStore{cfg: ESConfig{Fields: fields}}
	s.rawFields = fields
	s.cfg.defaults()
	s.fields = s.cfg.Fields
	return s
}

func TestNamespaceFilterUsesCandidateFields(t *testing.T) {
	s := newTestESStore(ESFieldMap{})
	raw, _ := json.Marshal(s.buildQuery(Filter{Namespace: "payments-prod"}))
	q := string(raw)
	for _, fld := range esNamespaceFields {
		if !strings.Contains(q, `"`+fld+`.keyword":"payments-prod"`) {
			t.Errorf("namespace clause %s adayını taşımıyor: %s", fld, q)
		}
	}
	if !strings.Contains(q, `"must_not":[{"exists":{"field":"kubernetes.namespace_name.keyword"}}]`) {
		t.Errorf("çıplak dal exists-guard'lı olmalı: %s", q)
	}
	raw, _ = json.Marshal(s.buildQuery(Filter{Service: "checkout"}))
	if strings.Contains(string(raw), "namespace") {
		t.Errorf("boş Namespace clause üretmemeli: %s", raw)
	}
}

func TestConfiguredRoleFieldWins(t *testing.T) {
	s := newTestESStore(ESFieldMap{Cluster: "labels.cluster", Pod: "labels.pod", Namespace: "labels.ns"})
	raw, _ := json.Marshal(s.buildQuery(Filter{Cluster: "cluster-a", Pod: "checkout-1", Namespace: "payments"}))
	q := string(raw)
	for _, want := range []string{
		`"labels.cluster.keyword":"cluster-a"`,
		`"labels.pod.keyword":"checkout-1"`,
		`"labels.ns.keyword":"payments"`,
	} {
		if !strings.Contains(q, want) {
			t.Errorf("yapılandırılmış alan eksik %s: %s", want, q)
		}
	}
	for _, notWant := range []string{"openshift.labels.cluster", "kubernetes.pod_name", "kubernetes.namespace_name"} {
		if strings.Contains(q, notWant) {
			t.Errorf("yapılandırılmış alan varken aday %s sorulmamalı: %s", notWant, q)
		}
	}
	// Yapılandırma yokken bugünkü davranış: aday liste.
	s2 := newTestESStore(ESFieldMap{})
	raw, _ = json.Marshal(s2.buildQuery(Filter{Cluster: "cluster-a"}))
	if !strings.Contains(string(raw), `"openshift.labels.cluster.keyword":"cluster-a"`) {
		t.Errorf("aday cluster listesi korunmalı: %s", raw)
	}
}

// v0.10.944 — ES'te sayısal seviye alanı yapılandırılmamışsa SeverityMin
// sorguya girmez; Page bunu itiraf eder (davranış aynı, sessizlik değil).
func TestESSearchUnappliedSeverity(t *testing.T) {
	if got := esSearchUnapplied(Filter{SeverityMin: 17}, ESFieldMap{}); len(got) != 1 || got[0] != FilterSeverity {
		t.Fatalf("severityNo yok → severity uygulanamadı: %v", got)
	}
	if got := esSearchUnapplied(Filter{SeverityMin: 17}, ESFieldMap{SeverityNo: "severity_number"}); got != nil {
		t.Fatalf("severityNo varsa uygulanır: %v", got)
	}
	if got := esSearchUnapplied(Filter{}, ESFieldMap{}); got != nil {
		t.Fatalf("istenmeyen filtre raporlanmaz: %v", got)
	}
}
