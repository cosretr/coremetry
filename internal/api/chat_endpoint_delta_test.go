package api

// chat_endpoint_delta_test.go — v0.10.1783 (operator-reported: "Endpoint
// sayfasında ✨ Açıkla deyince gelen açıklama yeterli değil … response time
// artışı varsa onu söyleyebilir"). Ön-getirme p99 değişimini modele veriyordu
// ama model çoğu kez söylemiyordu; endpointDeltaTR deterministik cümleyi
// kurar, operasyon kartı cevabın üstünde çizer.

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestEndpointDeltaTR(t *testing.T) {
	red := func(calls, errs uint64, p99, p50 float64) chstore.OpRED {
		return chstore.OpRED{Calls: calls, Errors: errs, P99Ms: p99, P50Ms: p50, Measured: true}
	}
	cases := []struct {
		name      string
		cur, prev chstore.OpRED
		rangeS    int64
		want      string
		ok        bool
	}{
		{"artış (brief örneği)", red(1180, 57, 1900, 0), red(1200, 2, 420, 0), 3600,
			"Son 1 saatte yanıt süresi p99 420 ms → 1,9 sn (+%352), hata oranı %0,2 → %4,8, trafik 1.200 → 1.180 istek (değişmedi); önceki eşit pencereye göre.", true},
		{"düşüş", red(600, 0, 200, 0), red(1200, 24, 800, 0), 6 * 3600,
			"Son 6 saatte yanıt süresi p99 800 ms → 200 ms (-%75), hata oranı %2,0 → %0,0, trafik 1.200 → 600 istek (-%50); önceki eşit pencereye göre.", true},
		{"değişmedi", red(1050, 10, 430, 0), red(1000, 10, 420, 0), 3600,
			"Son 1 saatte yanıt süresi p99 420 ms → 430 ms (değişmedi), hata oranı %1,0 → %1,0 (değişmedi), trafik 1.000 → 1.050 istek (değişmedi); önceki eşit pencereye göre.", true},
		{"önceki pencerede trafik yok", red(300, 3, 120, 0), red(0, 0, 0, 0), 3600,
			"Son 1 saatte yanıt süresi p99 120 ms, hata oranı %1,0, trafik 300 istek; önceki pencerede trafik yok.", true},
		{"önceki ölçülmedi", red(300, 0, 120, 40), chstore.OpRED{}, 86400,
			"Son 1 günde yanıt süresi p50 40 ms / p99 120 ms, hata oranı %0,0, trafik 300 istek; önceki pencerede trafik yok.", true},
		{"ölçülmedi", chstore.OpRED{Calls: 5}, red(5, 0, 10, 0), 3600, "", false},
		{"p50 var", red(2000, 0, 2400, 150), red(2000, 0, 600, 50), 24 * 3600,
			"Son 1 günde yanıt süresi p50 50 ms → 150 ms (+%200) / p99 600 ms → 2,4 sn (+%300), hata oranı %0,0 → %0,0 (değişmedi), trafik 2.000 → 2.000 istek (değişmedi); önceki eşit pencereye göre.", true},
		{"küçük oran sıçraması değişmedi sayılır", red(10000, 2, 50, 0), red(10000, 1, 50, 0), 1800,
			"Son 30 dakikada yanıt süresi p99 50 ms → 50 ms (değişmedi), hata oranı %<0,1 → %<0,1 (değişmedi), trafik 10.000 → 10.000 istek (değişmedi); önceki eşit pencereye göre.", true},
		{"hata sıfırdan doğdu", red(1000, 50, 300, 0), red(1000, 0, 300, 0), 3600,
			"Son 1 saatte yanıt süresi p99 300 ms → 300 ms (değişmedi), hata oranı %0,0 → %5,0, trafik 1.000 → 1.000 istek (değişmedi); önceki eşit pencereye göre.", true},
	}
	for _, c := range cases {
		got, ok := endpointDeltaTR(c.cur, c.prev, c.rangeS)
		if ok != c.ok || got != c.want {
			t.Errorf("%s:\n got (%v) %q\nwant (%v) %q", c.name, ok, got, c.ok, c.want)
		}
	}
}

func TestEndpointChangeTag(t *testing.T) {
	cases := []struct {
		cur, prev float64
		want      string
	}{
		{110, 100, "+%10"}, {109, 100, "değişmedi"}, {91, 100, "değişmedi"}, {90, 100, "-%10"},
		{0, 0, "değişmedi"}, {5, 0, ""},
	}
	for _, c := range cases {
		if got := endpointChangeTag(c.cur, c.prev); got != c.want {
			t.Errorf("endpointChangeTag(%v, %v) = %q, want %q", c.cur, c.prev, got, c.want)
		}
	}
}

// Kablolama: satır yalnız v2 endpoint kartına, kart yayınından ÖNCE yazılır.
func TestEndpointDeltaWiring(t *testing.T) {
	b, err := os.ReadFile("chat_operation_prefetch.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	set := strings.Index(src, "if p.endpoint && cosreV2Marked(pctx) {")
	call := strings.Index(src, "p.DeltaText, _ = endpointDeltaTR(p.Current, p.Previous, rangeS)")
	em := strings.Index(src, `emit("block", map[string]any{"id": "op-prefetch"`)
	if set < 0 || call < set || em < call {
		t.Fatalf("deltaText kart yayınından önce v2+endpoint kapısıyla yazılmıyor (%d %d %d)", set, call, em)
	}
}
