package api

// ml_evidence_test.go — v0.10.1873 (öğrenme döngüsü Faz 2 parti B, audit
// §7.1 "Kanıt"): /api/ml/{id}/evidence/{kind} önbellek anahtarı TÜM
// girdileri taşır (v0.5.187 sınıfı); tür ↔ sinyal eşlemesi sabit; uç saf
// kuruculara GERÇEKTEN bağlı (tested-but-unreachable dersi); log kalıp
// tablosu logstore üzerinden (ES'e doğrudan değil), metrik komşuları
// ml_series'ten (metric_points okuması yok).

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestMLEvidenceKeyHashesAllInputs(t *testing.T) {
	base := mlEvidenceKey("e1", "patterns", "Europe/Istanbul")
	for i, v := range []string{
		mlEvidenceKey("e2", "patterns", "Europe/Istanbul"),
		mlEvidenceKey("e1", "metrics", "Europe/Istanbul"),
		mlEvidenceKey("e1", "patterns", "UTC"),
		mlEvidenceKey("e1:kind=metrics", "patterns", "Europe/Istanbul"), // ayırıcı çakışması (%q)
	} {
		if v == base {
			t.Errorf("girdi %d anahtarı değiştirmedi: %s", i, v)
		}
	}
}

func TestMLEvidenceKinds(t *testing.T) {
	want := map[string]string{"endpoints": chstore.MLSignalTrace, "patterns": chstore.MLSignalLog, "metrics": chstore.MLSignalMetric}
	if len(mlEvidenceKinds) != len(want) {
		t.Fatalf("tür kümesi: %v", mlEvidenceKinds)
	}
	for k, v := range want {
		if mlEvidenceKinds[k] != v {
			t.Errorf("%s → %s, beklenen %s", k, mlEvidenceKinds[k], v)
		}
	}
}

func TestMLEvidenceWiring(t *testing.T) {
	raw, err := os.ReadFile("ml_evidence.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, must := range []string{
		`"GET /api/ml/{id}/evidence/{kind}"`, "s.serveCached(w, r, mlEvidenceKey(",
		"ml.EndpointEvidence(", "ml.PatternEvidence(", "ml.MetricNeighbors(",
		"logstore.GroupBySignatureN(", "SkipStatus: true", "s.store.MLSeriesRange(",
	} {
		if !strings.Contains(src, must) {
			t.Errorf("ml_evidence.go %q içermiyor", must)
		}
	}
	for _, banned := range []string{"metric_points", "QueryMetric(", "elastic", "s.store.GetEndpoints("} {
		if strings.Contains(src, banned) {
			t.Errorf("ml_evidence.go yasak okuma: %q", banned)
		}
	}
}
