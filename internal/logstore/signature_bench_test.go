package logstore

// signature_bench_test.go — v0.10.1829: imza maliyeti, mevcut bütçe
// fikstürüyle (TestNormalizeSignature_PerfBudget) aynı gövdeler.

import (
	"fmt"
	"testing"
)

func perfFixtureBodies() []string {
	bodies := make([]string, 2000)
	for i := range bodies {
		bodies[i] = fmt.Sprintf(`{"request_id":"SPE%013dZTod%024d","msg":"inquiry %d completed for K7QX%07d","f1":"a","f2":"b","f3":{"x":1}}`,
			i, int64(i)*31, i, i)
	}
	return bodies
}

func BenchmarkNormalizeSignature_PerfFixture(b *testing.B) {
	bodies := perfFixtureBodies()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_ = NormalizeSignature(bodies[n%len(bodies)])
	}
}

func BenchmarkNormalizeSignature_PlainText(b *testing.B) {
	lines := []string{
		"order 3f2b1c4e-9d8a-4b7c-8e6f-1a2b3c4d5e6f failed after 1500ms for customer 12345 from 10.0.12.34:8080",
		"Payment authorization failed for customer account, retrying later",
		"pod orders-api-7d9f6-x2k4p restarted, attempt 3 of 5, took 18.45MB",
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_ = NormalizeSignature(lines[n%len(lines)])
	}
}
