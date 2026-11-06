package templater

// masker_bench_test.go — v0.10.1829: Drain maskesinin satır maliyeti.

import "testing"

func BenchmarkMask(b *testing.B) {
	lines := []string{
		"order 3f2b1c4e-9d8a-4b7c-8e6f-1a2b3c4d5e6f failed after 1500ms for customer 12345 from 10.0.12.34:8080",
		"Payment authorization failed for customer account, retrying later",
		"pod orders-api-7d9f6-x2k4p restarted, attempt 3 of 5, took 18.45MB",
		`{"request_id":"SPE1015060201ZTod000000000000202610240340413867","msg":"inquiry completed in 15ms"}`,
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_ = Mask(lines[n%len(lines)])
	}
}
