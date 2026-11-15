package api

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

func TestRenderThanosPodsTR(t *testing.T) {
	if renderThanosPodsTR(thanosPodsResult{}, "s") != "" {
		t.Fatal("Thanos yok/boş → kanıt yok")
	}
	res := thanosPodsResult{Rows: []thanos.PodRow{
		{Cluster: "ocpa", Pod: "s-1", Phase: "Running", Restarts: 0, CPUCores: 0.5, MemBytes: 2 << 30},
		{Cluster: "ocpa", Pod: "s-2", Phase: "Pending", Restarts: 3, CPUCores: 0.1, MemBytes: 512 << 20},
		{Cluster: "ocpb", Pod: "s-3", Restarts: 1, MemBytes: 1 << 30},
	}, Errors: []string{"ocpz"}}
	out := renderThanosPodsTR(res, "s")
	for _, want := range []string{"3 pod, 2 cluster", "- ocpa: 2 pod (1 çalışıyor), restart 3", "- ocpa/s-2: Pending, restart 3", "Okunamayan cluster (Thanos zaman aşımı/hata): ocpz"} {
		if !strings.Contains(out, want) {
			t.Errorf("çıktıda %q yok:\n%s", want, out)
		}
	}
	if !strings.Contains(renderThanosPodsTR(thanosPodsResult{Errors: []string{"x"}}, "s"), "pod bulunamadı") {
		t.Error("yalnız hata → bulunamadı + hata satırı")
	}
}

func TestPodHTTPChartFences(t *testing.T) {
	f := podHTTPChartFences("s", 3600)
	if strings.Count(f, "```chart") != 2 || !strings.Contains(f, `"source":"metric"`) || !strings.Contains(f, `"agg":"rate"`) || !strings.Contains(f, `"agg":"p99"`) {
		t.Fatalf("grafik kartları: %s", f)
	}
}

// v0.10.1250 — deploy sorusu en az 24 saat geriye bakar.
func TestGuidedDeployMinLookback(t *testing.T) {
	if guidedDeployMinLookback.Hours() != 24 {
		t.Fatalf("taban %v", guidedDeployMinLookback)
	}
}

// v0.10.1251 — "bff" önekle tek servise (bff-asset-management) çözülüyordu;
// kelime başka servislerde tam ad parçasıysa aile yoluna düşer.
func TestSharedSegmentTokenIsFamily(t *testing.T) {
	svcs := []string{"bff-asset-management-prod", "mobile-overview-bff-prod", "mobile-crm-bff-prod", "checkout"}
	for _, q := range []string{"bff sorununu incelemek için nereye bakmalıyım?", "bff'ler nasıl?"} {
		r := routeGuidedIntent(q, svcs, nil, nil, "")
		if r.Service == "bff-asset-management-prod" || len(r.Family) < 3 {
			t.Errorf("%q → svc=%q fam=%v, aile bekleniyordu", q, r.Service, r.Family)
		}
	}
	if r := routeGuidedIntent("checkout nasıl", svcs, nil, nil, ""); r.Service != "checkout" {
		t.Errorf("tekil servis bozuldu: %+v", r)
	}
	if serviceSegmentHits("bff", svcs) != 2 || serviceSegmentHits("crm", svcs) != 1 {
		t.Error("segment sayımı")
	}
}

// v0.10.1252 — servisli exception / teknik hata sorusu exception gruplarına gider.
func TestServiceExceptionsRoute(t *testing.T) {
	svcs := []string{"bsa-mobile-login-prod"}
	for _, q := range []string{"bsa-mobile-login-prod teknik hataları?", "bsa-mobile-login-prod exception'ları?", "bsa-mobile-login-prod istisnaları"} {
		if r := routeGuidedIntent(q, svcs, nil, nil, ""); r.Intent != guidedServiceExceptions || r.Service != "bsa-mobile-login-prod" {
			t.Errorf("%q → %+v", q, r)
		}
	}
	if r := routeGuidedIntent("bsa-mobile-login-prod hata logları?", svcs, nil, nil, ""); r.Intent == guidedServiceExceptions {
		t.Error("log hatası sorusu exception'a kaymamalı")
	}
	out := renderServiceExceptionsTR("s", []chstore.ExceptionRow{{Type: "java.net.SocketTimeoutException", Count: 7, Message: "Read timed out"}}, 3600)
	if !strings.Contains(out, "1 grup, toplam 7 kez") || !strings.Contains(out, "SocketTimeoutException · 7 kez · Read timed out") {
		t.Fatal(out)
	}
}

// v0.10.1256 — "en yavaş trace'ler" yerine hata sıralaması akışı.
func TestTopErrorRoutes(t *testing.T) {
	svcs := []string{"checkout", "payment"}
	cases := map[string]guidedIntent{
		"En hatalı servisler?":              guidedTopErrorServices,
		"Takımımın en hatalı servisleri?":   guidedMyTopErrors,
		"checkout en hatalı operasyonları?": guidedTopErrorOps,
	}
	for q, want := range cases {
		if r := routeGuidedIntent(q, svcs, nil, nil, ""); r.Intent != want {
			t.Errorf("%q → %s, want %s", q, r.Intent, want)
		}
	}
	if r := routeGuidedIntent("checkout hataları", svcs, nil, nil, ""); r.Intent == guidedTopErrorOps || r.Intent == guidedTopErrorServices {
		t.Error("sıralama sinyali olmadan hata sorusu sıralamaya kaymamalı")
	}
	out := renderTopErrorsTR("Filoda en hatalı servisler", []chstore.ErrorRank{{Name: "a", Spans: 200, Errors: 5}}, 3600, "servis")
	if !strings.Contains(out, "- a: 5 hata / 200 istek (%2.50)") {
		t.Fatal(out)
	}
	if got := topErrorFollowups([]string{"a", "b", "c", "d"}); len(got) != 3 || got[0] != "a en hatalı operasyonları?" {
		t.Fatalf("takip: %v", got)
	}
}
