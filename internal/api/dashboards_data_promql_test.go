package api

// dashboards_data_promql_test.go — v0.10.2003 (Dashboards Faz 3 dilim 3.6):
// bundle'a promql slotu + slot penceresi + piksel bütçesi. Anahtar her yeni
// girdiyi taşır (v0.5.187), boşken eski anahtar bayt-aynı kalır.

import (
	"testing"
	"time"
)

func TestDashPanelKey_PromqlInputsDistinct(t *testing.T) {
	from := time.Unix(1_700_000_000, 0)
	to := from.Add(time.Hour)
	base := bundleReq{ID: "p", Type: "promql", Query: "sum(rate(x[5m]))", Step: 60}
	k0 := dashPanelKey("vm", base, from, to)
	variants := map[string]bundleReq{
		"query": {ID: "p", Type: "promql", Query: "sum(rate(y[5m]))", Step: 60},
		"mdp":   {ID: "p", Type: "promql", Query: base.Query, Step: 60, MaxDataPoints: 300},
		"win":   {ID: "p", Type: "promql", Query: base.Query, Step: 60, From: from.Add(-time.Hour).UnixNano(), To: to.UnixNano()},
	}
	for name, v := range variants {
		if dashPanelKey("vm", v, from, to) == k0 {
			t.Errorf("%s anahtara girmiyor", name)
		}
	}
	if dashPanelKey("ch", base, from, to) == k0 {
		t.Error("kaynak damgası anahtara girmiyor")
	}
	// mdp kelepçesi: 9999 ile 4000 aynı istek → aynı anahtar.
	a := base
	a.MaxDataPoints = 9999
	b := base
	b.MaxDataPoints = 4000
	if dashPanelKey("vm", a, from, to) != dashPanelKey("vm", b, from, to) {
		t.Error("kelepçelenen mdp ayrı anahtar açıyor")
	}
}

func TestDashPanelKey_OldSlotsUnchanged(t *testing.T) {
	from := time.Unix(1_700_000_000, 0)
	to := from.Add(time.Hour)
	q := bundleReq{ID: "p", Type: "metric", Name: "m", Agg: "avg", Step: 60}
	// Pencere override'ı geçersizse (To ≤ From) yok sayılır → anahtar aynı.
	bad := q
	bad.From, bad.To = 5, 5
	if dashPanelKey("x", q, from, to) != dashPanelKey("x", bad, from, to) {
		t.Error("geçersiz pencere anahtarı değiştirdi")
	}
}

func TestBundleReqWindow(t *testing.T) {
	from := time.Unix(100, 0)
	to := time.Unix(200, 0)
	f, tt := bundleReq{}.window(from, to)
	if !f.Equal(from) || !tt.Equal(to) {
		t.Fatal("override yokken gövde penceresi")
	}
	f, tt = bundleReq{From: time.Unix(0, 0).Add(50 * time.Second).UnixNano(), To: to.UnixNano()}.window(from, to)
	if f.Unix() != 50 || tt.Unix() != 200 {
		t.Fatalf("override: %v %v", f, tt)
	}
	if (bundleReq{MaxDataPoints: -3}).maxDP() != 0 || (bundleReq{MaxDataPoints: 5000}).maxDP() != 4000 {
		t.Fatal("mdp kelepçesi")
	}
}
