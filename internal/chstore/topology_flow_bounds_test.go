package chstore

import (
	"regexp"
	"testing"
)

// v0.10.863 (scale-audit 09-23) — GetFlowTopology'nin root_traces CTE'leri (iki
// geçiş) LIMIT taşır: GLOBAL IN'e giden trace id kümesi sınırlı. Kaynak pini:
// her `WITH root_traces AS (` gövdesinde LIMIT olmalı.
func TestFlowTopologyRootTracesCTEIsBounded(t *testing.T) {
	// Yalnız OKUYUCU gövdesi: WriteRootFlowsBucket'ın aynı adlı CTE'si MV kova
	// yazıcısıdır, TAM olmak zorunda — orada LIMIT toplamı bozar.
	body := funcBody(t, "topology.go", "func (s *Store) GetFlowTopology(")
	re := regexp.MustCompile(`(?s)WITH root_traces AS \((.*?)\),`)
	ms := re.FindAllStringSubmatch(body, -1)
	if len(ms) != 2 {
		t.Fatalf("GetFlowTopology'de 2 root_traces CTE beklenir, %d bulundu", len(ms))
	}
	lim := regexp.MustCompile("LIMIT ` *\\+ *flowRootTraceSampleSQL") // gofmt birleştirme boşluklarını kaldırabilir
	for i, m := range ms {
		if !lim.MatchString(m[1]) {
			t.Fatalf("root_traces CTE #%d LIMIT taşımıyor:\n%s", i+1, m[1])
		}
	}
	if flowRootTraceSample < 5000 || flowRootTraceSample > 100000 {
		t.Fatalf("örneklem tavanı makul aralık dışında: %d", flowRootTraceSample)
	}
}
