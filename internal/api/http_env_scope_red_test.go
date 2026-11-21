package api

// v0.10.1377 — /metric-red'in VM env süzgeci (adlar SENTETİK). SÖZLEŞME:
// /metric-red VM'de env'i resolveHTTPEnvScope ile süzer (EnvAmbiguous yalnız
// çözülemeyince); MCP query_metrics aynı kapsamı çözer.
// v0.10.1437 — GET /api/metrics/http-env-scope kaldırıldı (v0.10.1429'dan beri
// sayfalar eşleyiciyi /red-family'den alıyor); uç testleri onunla gitti,
// dosya http_env_scope_route_test.go'dan taşındı.

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// TestHTTPEnvScopeRouteRemoved_v0101437 — ölü uç geri gelmez; paylaşılan SAF
// terim yardımcısı /red-family için yaşar.
func TestHTTPEnvScopeRouteRemoved_v0101437(t *testing.T) {
	if _, ok := extraRouteRegistrars["http-env-scope"]; ok {
		t.Fatal("GET /api/metrics/http-env-scope v0.10.1437'de kaldırıldı; istemcisi yok (v0.10.1429)")
	}
	if !reflect.DeepEqual(httpEnvScopeTerms("eu-a", ""), map[string]string{"env": "eu-a"}) ||
		!reflect.DeepEqual(httpEnvScopeTerms("eu-a", "prod-eu-a"), map[string]string{"env": "eu-a", "cluster": "prod-eu-a"}) {
		t.Fatal("terimler")
	}
}

func TestHTTPEnvScopeFilters_v0101377(t *testing.T) {
	sc := httpEnvScope{Applied: true, Terms: []httpEnvTerm{
		{Role: "env", Want: "eu-a", Label: "deployment_environment_name", Values: []string{"dr-eu-a", "prod-eu-a"}, Tier: "prefix"},
		{Role: "cluster", Want: "prod-eu-a", Label: "deployment_environment_name", Values: []string{"prod-eu-a"}, Tier: "exact"},
	}}
	want := []chstore.FilterExpr{{Key: "deployment_environment_name", Op: "=", Values: []string{"prod-eu-a"}}}
	if got := httpEnvScopeFilters(sc); !reflect.DeepEqual(got, want) {
		t.Fatalf("aynı etiket → kesişim: %+v", got)
	}
	sc.Terms[1].Label = "k8s_cluster_name"
	sc.Terms[1].Values = []string{"eu-a-k8s"}
	want = []chstore.FilterExpr{
		{Key: "deployment_environment_name", Op: "=", Values: []string{"dr-eu-a", "prod-eu-a"}},
		{Key: "k8s_cluster_name", Op: "=", Values: []string{"eu-a-k8s"}},
	}
	if got := httpEnvScopeFilters(sc); !reflect.DeepEqual(got, want) {
		t.Fatalf("ayrı etiket → iki ifade: %+v", got)
	}
	sc.Applied = false
	if got := httpEnvScopeFilters(sc); got != nil {
		t.Fatalf("uygulanmamış kapsam süzgeç üretmez: %+v", got)
	}
}

// fakeVMRedSource — metricSource (fakeEPSource) + VM etiket okuyucusu.
type fakeVMRedSource struct {
	*fakeEPSource
	*fakeVMReader
}

func TestServiceMetricREDEnvScopeOnVM_v0101377(t *testing.T) {
	from, to := epFixtureWindow()
	id := httpMetricIdentity{Metric: "http_server_request_duration_seconds_count", RTMetric: "http_server_request_duration_seconds", Instrument: "histogram", Service: "orders-api-prod", MatchedBy: "service_name"}
	live := map[string][]string{"deployment_environment_name": {"prod-eu-a", "prod-tr-c"}}
	s := &Server{}

	// CH kaynağı (etiket okuyucusu yok) → nil kapsam, eski yol.
	if sc := s.serviceMetricREDEnvScope(context.Background(), redFixtureSource("s", nil), "eu-a", to); sc != nil {
		t.Fatalf("CH'de kapsam çözülmez: %+v", sc)
	}
	src := fakeVMRedSource{redFixtureSource("s", []string{"http.response.status_code"}), &fakeVMReader{values: live}}
	if sc := s.serviceMetricREDEnvScope(context.Background(), src, "", to); sc != nil {
		t.Fatal("env yokken kapsam çözülmez")
	}

	// Çözüldü → süzgeç her sorguda, EnvAmbiguous=false, not etiketi söyler.
	p := serviceMetricREDPlan{Service: "orders-api-prod", From: from, To: to, Mdp: 300, RateWin: 180, Env: "eu-a", ShowInfra: true} // v0.10.2434 — tesisat süzgeci ayrı test
	p.EnvScope = s.serviceMetricREDEnvScope(context.Background(), src, p.Env, p.To)
	resp, err := buildServiceMetricRED(context.Background(), src, id, p)
	if err != nil {
		t.Fatal(err)
	}
	wantF := chstore.FilterExpr{Key: "deployment_environment_name", Op: "=", Values: []string{"prod-eu-a"}}
	for i, fs := range src.filters {
		if len(fs) != 1 || !reflect.DeepEqual(fs[0], wantF) {
			t.Fatalf("rate sorgusu %d env süzgeci taşımalı: %+v", i, fs)
		}
	}
	for _, q := range src.fakeEPSource.queries {
		if len(q.Filters) != 1 || !reflect.DeepEqual(q.Filters[0], wantF) {
			t.Fatalf("yüzdelik sorgusu env süzgeci taşımalı: %+v", q.Filters)
		}
	}
	if resp.EnvAmbiguous || resp.EnvScope == nil || !resp.EnvScope.Applied ||
		!strings.Contains(resp.Note, `env süzgeci metrikte deployment_environment_name="prod-eu-a" ile uygulandı.`) ||
		strings.Contains(resp.Note, "TÜM ortamları") {
		t.Fatalf("uygulandı: amb=%v not=%s", resp.EnvAmbiguous, resp.Note)
	}

	// Çözülmedi → süzgeç YOK, EnvAmbiguous=true, not gerekçeyi söyler.
	src2 := fakeVMRedSource{redFixtureSource("s", []string{"http.response.status_code"}), &fakeVMReader{values: live}}
	p.Env = "us-x"
	p.EnvScope = s.serviceMetricREDEnvScope(context.Background(), src2, p.Env, p.To)
	resp, err = buildServiceMetricRED(context.Background(), src2, id, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, fs := range src2.filters {
		if len(fs) != 0 {
			t.Fatalf("çözülmeyen env süzgeç eklememeli: %+v", fs)
		}
	}
	if !resp.EnvAmbiguous || !strings.Contains(resp.Note,
		`env süzgeci uygulanamadı (metrikte bu ortam yok (deployment_environment_name değerleri arasında "us-x" eşleşmedi)) — seriler TÜM ortamları kapsıyor.`) {
		t.Fatalf("çözülmedi: amb=%v not=%s", resp.EnvAmbiguous, resp.Note)
	}

	// Okuma hatası → Failed kapsamı, "çözülemedi" notu.
	src3 := fakeVMRedSource{redFixtureSource("s", []string{"http.response.status_code"}), &fakeVMReader{valuesErr: context.DeadlineExceeded}}
	p.Env = "eu-a"
	p.EnvScope = s.serviceMetricREDEnvScope(context.Background(), src3, p.Env, p.To)
	resp, _ = buildServiceMetricRED(context.Background(), src3, id, p)
	if !resp.EnvAmbiguous || resp.EnvScope == nil || !resp.EnvScope.Failed ||
		!strings.Contains(resp.Note, "env süzgeci çözülemedi (VM etiket sorgusu başarısız: ") {
		t.Fatalf("hata: %+v not=%s", resp.EnvScope, resp.Note)
	}
}

// v0.10.1377 — /metric-red ve MCP query_metrics AYNI çözücüyü çağırır.
func TestServiceMetricREDEnvScopeWiring_v0101377(t *testing.T) {
	red := readRepoFile(t, "service_metric_red.go")
	if !strings.Contains(red, "pp.EnvScope = s.serviceMetricREDEnvScope(ctx, src, p.Env, p.To)") ||
		!strings.Contains(red, "serviceMetricREDKey(p, src.Name(), mx, serviceMetricREDLabelDigest(src))") {
		t.Fatal("/metric-red env kapsamını çözmeli ve anahtara LabelMap özetini koymalı")
	}
	if !strings.Contains(readRepoFile(t, "mcp_deps.go"), "p.EnvScope = s.serviceMetricREDEnvScope(ctx, src, env, to)") {
		t.Fatal("MCP query_metrics aynı env kapsamını çözmeli")
	}
}
