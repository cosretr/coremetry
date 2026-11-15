package api

// v0.10.1376 — ortam adı → VM ortam etiketi + canlı değer (adlar SENTETİK).
// SÖZLEŞME: LabelMap.Env kazanır; yoksa deployment_environment_name; diğer
// yazımlar YALNIZ varsayılanın hiç değeri yoksa. Değer okuması 24 sa, ≤1000,
// önbellekli (anahtar TÜM girdileri taşır). Çözülmeyen terim → Applied=false +
// gerekçe (süzgeç sessizce düşmez); env + cluster aynı etikette → kesişim.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// fakeVMReader — v0.10.1376: etiket değerleri + anlık sorgu sahte okuyucusu.
type fakeVMReader struct {
	lm        vmetrics.LabelMap
	values    map[string][]string
	partial   bool
	valuesErr error
	total     []chstore.SpanMetricSeries
	p50, p95  []chstore.SpanMetricSeries
	queryErr  error

	mu         sync.Mutex
	labelCalls []string
	windows    [][2]time.Time
	queries    []string
	ats        []time.Time
}

func (f *fakeVMReader) MetricLabelValuesIn(_ context.Context, metric, label string, from, to time.Time, limit int) ([]string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if metric != httpEnvMetric || limit != httpEnvValuesLimit {
		return nil, false, errors.New("beklenmeyen metrik/limit: " + metric)
	}
	f.labelCalls = append(f.labelCalls, label)
	f.windows = append(f.windows, [2]time.Time{from, to})
	if f.valuesErr != nil {
		return nil, false, f.valuesErr
	}
	return f.values[label], f.partial, nil
}

func (f *fakeVMReader) MetricLabelMap() vmetrics.LabelMap { return f.lm }

func (f *fakeVMReader) QueryPromQLInstant(_ context.Context, q string, at time.Time) ([]chstore.SpanMetricSeries, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	f.ats = append(f.ats, at)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	switch {
	case strings.HasPrefix(q, "sum by"):
		return f.total, nil
	case strings.HasPrefix(q, "histogram_quantile(0.5,"):
		return f.p50, nil
	case strings.HasPrefix(q, "histogram_quantile(0.95,"):
		return f.p95, nil
	}
	return nil, errors.New("beklenmeyen sorgu: " + q)
}

func TestHTTPEnvLabelCandidates_v0101376(t *testing.T) {
	got := httpEnvLabelCandidates(vmetrics.LabelMap{})
	want := []string{"deployment_environment_name", "deployment_environment", "deploy_environment_name", "environment"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("varsayılan + kalan adaylar: %v", got)
	}
	if got := httpEnvLabelCandidates(vmetrics.LabelMap{Env: " env_x "}); !reflect.DeepEqual(got, []string{"env_x"}) {
		t.Fatalf("LabelMap.Env kazanır: %v", got)
	}
}

func TestHTTPEnvLabelFor_v0101376(t *testing.T) {
	to := ccTo
	cases := []struct {
		name    string
		r       *fakeVMReader
		label   string
		source  string
		vals    []string
		calls   []string
		wantErr bool
	}{
		{"varsayılan değerli", &fakeVMReader{values: map[string][]string{
			"deployment_environment_name": {"prod-eu-a"}, "deployment_environment": {"eu-a"}}},
			"deployment_environment_name", httpEnvSourceDefault, []string{"prod-eu-a"}, []string{"deployment_environment_name"}, false},
		{"varsayılan değersiz → keşif", &fakeVMReader{values: map[string][]string{"deployment_environment": {"eu-a"}}},
			"deployment_environment", httpEnvSourceDiscovered, []string{"eu-a"}, []string{"deployment_environment_name", "deployment_environment"}, false},
		{"hiçbiri değerli değil", &fakeVMReader{},
			"deployment_environment_name", httpEnvSourceDefault, nil, []string{"deployment_environment_name", "deployment_environment", "deploy_environment_name", "environment"}, false},
		{"configured değersiz bile kazanır", &fakeVMReader{lm: vmetrics.LabelMap{Env: "env_x"}, values: map[string][]string{"deployment_environment_name": {"prod-eu-a"}}},
			"env_x", httpEnvSourceConfigured, nil, []string{"env_x"}, false},
		{"okuma hatası", &fakeVMReader{valuesErr: errors.New("boom")}, "", "", nil, []string{"deployment_environment_name"}, true},
	}
	for _, c := range cases {
		el, err := (&Server{}).httpEnvLabelFor(context.Background(), c.r, to)
		if (err != nil) != c.wantErr || el.Label != c.label || el.Source != c.source || !reflect.DeepEqual(el.Values, c.vals) || !reflect.DeepEqual(c.r.labelCalls, c.calls) {
			t.Errorf("%s: %+v err=%v calls=%v", c.name, el, err, c.r.labelCalls)
		}
	}
}

func TestHTTPEnvValuesKeyAndWindow_v0101376(t *testing.T) {
	to := time.Date(2026, 10, 19, 12, 7, 30, 0, time.UTC)
	k := httpEnvValuesKey(httpEnvMetric, "deployment_environment_name", to)
	if k != "http-env-values:v1:m=http_server_request_duration_seconds_count:l=deployment_environment_name:n=1000:to=1792411200" {
		t.Fatalf("anahtar: %s", k)
	}
	if httpEnvValuesKey(httpEnvMetric, "deployment_environment_name", to.Add(2*time.Minute)) != k {
		t.Fatal("aynı 10 dk dilimi aynı anahtar")
	}
	for _, other := range []string{
		httpEnvValuesKey(httpEnvMetric, "environment", to),
		httpEnvValuesKey("http_client_request_duration_seconds_count", "deployment_environment_name", to),
		httpEnvValuesKey(httpEnvMetric, "deployment_environment_name", to.Add(10*time.Minute)),
	} {
		if other == k {
			t.Fatalf("girdi değişti, anahtar aynı: %s", other)
		}
	}
	from, end := httpEnvValuesWindow(to)
	if end.Sub(from) != httpEnvValuesLookback || to.Before(from) || to.After(end) {
		t.Fatalf("pencere 24 sa ve to'yu kapsar: %v – %v", from, end)
	}
	r := &fakeVMReader{values: map[string][]string{"deployment_environment_name": {"prod-eu-a"}}}
	if _, _, err := (&Server{}).httpEnvLabelValues(context.Background(), r, "deployment_environment_name", to); err != nil || len(r.windows) != 1 ||
		!r.windows[0][0].Equal(from) || !r.windows[0][1].Equal(end) {
		t.Fatalf("okuma aynı pencereyi görür: %v %v", r.windows, err)
	}
}

func TestHTTPEnvLabelValuesCached_v0101376(t *testing.T) {
	s := &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	r := &fakeVMReader{values: map[string][]string{"deployment_environment_name": {"prod-eu-a", "dr-eu-a"}}, partial: true}
	for i := 0; i < 2; i++ {
		vals, partial, err := s.httpEnvLabelValues(context.Background(), r, "deployment_environment_name", ccTo)
		if err != nil || !partial || !reflect.DeepEqual(vals, []string{"prod-eu-a", "dr-eu-a"}) {
			t.Fatalf("tur %d: %v %v %v", i, vals, partial, err)
		}
	}
	if len(r.labelCalls) != 1 {
		t.Fatalf("ikinci okuma önbellekten: %d çağrı", len(r.labelCalls))
	}
	// Hata önbelleğe yazılmaz.
	e := &fakeVMReader{valuesErr: errors.New("boom")}
	for i := 0; i < 2; i++ {
		if _, _, err := s.httpEnvLabelValues(context.Background(), e, "environment", ccTo); err == nil {
			t.Fatal("hata dönmeli")
		}
	}
	if len(e.labelCalls) != 2 {
		t.Fatalf("hata önbelleğe yazılmamalı: %d", len(e.labelCalls))
	}
}

func TestResolveHTTPEnvScope_v0101376(t *testing.T) {
	live := map[string][]string{"deployment_environment_name": {"prod-eu-a", "dr-eu-a", "prod-tr-c", "eu-b"}}
	cases := []struct {
		name    string
		lm      vmetrics.LabelMap
		values  map[string][]string
		terms   map[string]string
		applied bool
		matcher string
		reason  string
	}{
		{"env önek", vmetrics.LabelMap{}, live, map[string]string{"env": "tr-c"}, true, `deployment_environment_name="prod-tr-c"`, ""},
		{"env belirsiz → ikisi birden", vmetrics.LabelMap{}, live, map[string]string{"env": "eu-a"}, true, `deployment_environment_name=~"dr-eu-a|prod-eu-a"`, ""},
		{"env yok", vmetrics.LabelMap{}, live, map[string]string{"env": "us-x"}, false, "",
			`metrikte bu ortam yok (deployment_environment_name değerleri arasında "us-x" eşleşmedi)`},
		{"env + cluster aynı etiket → kesişim", vmetrics.LabelMap{}, live, map[string]string{"env": "eu-a", "cluster": "prod-eu-a"}, true, `deployment_environment_name="prod-eu-a"`, ""},
		{"kesişim boş", vmetrics.LabelMap{}, live, map[string]string{"env": "eu-a", "cluster": "eu-b"}, false, "", "env ve cluster aynı etikette kesişmiyor"},
		{"cluster kendi etiketinde", vmetrics.LabelMap{Cluster: "k8s_cluster_name"},
			map[string][]string{"deployment_environment_name": {"prod-eu-a"}, "k8s_cluster_name": {"eu-a-k8s"}},
			map[string]string{"env": "eu-a", "cluster": "eu-a-k8s"}, true, `deployment_environment_name="prod-eu-a", k8s_cluster_name="eu-a-k8s"`, ""},
		{"terim yok", vmetrics.LabelMap{}, live, map[string]string{"env": " "}, false, "", "süzgeç terimi yok"},
	}
	for _, c := range cases {
		r := &fakeVMReader{lm: c.lm, values: c.values}
		sc, err := (&Server{}).resolveHTTPEnvScope(context.Background(), r, c.terms, ccTo)
		if err != nil || sc.Applied != c.applied || sc.Matcher != c.matcher || sc.Reason != c.reason || sc.Label != "deployment_environment_name" {
			t.Errorf("%s: %+v err=%v", c.name, sc, err)
		}
	}
	// Etiket okuma hatası → err (çağıran "çözülemedi" der, "yok" DEĞİL).
	if _, err := (&Server{}).resolveHTTPEnvScope(context.Background(), &fakeVMReader{valuesErr: errors.New("boom")}, map[string]string{"env": "eu-a"}, ccTo); err == nil {
		t.Fatal("okuma hatası err dönmeli")
	}
}

// v0.10.2211 — operatör (prod): cluster=cl-a ortam etiketinde "cl-a" değeri
// başka servislerden BİREBİR bulunuyordu → deployment_environment_name="cl-a",
// bu servisin serileri prod-cl-a taşıdığı için karolar boş. Cluster varlığının
// span ortamları (service_env_summary_5m) önce denenir.
func TestResolveMetricEnvScopeClusterEntity(t *testing.T) {
	live := map[string][]string{"deployment_environment_name": {"cl-a", "prod-cl-c", "prod-cl-a", "prod-cl-b"}}
	cl := map[string]string{"cluster": "cl-a"}
	run := func(envs []string) httpEnvScope {
		sc, err := (&Server{}).resolveMetricEnvScopeMapped(context.Background(), &fakeVMReader{values: live}, httpEnvMetric, cl, envs, ccTo)
		if err != nil {
			t.Fatal(err)
		}
		return sc
	}
	if sc := run([]string{"prod-cl-a"}); !sc.Applied || sc.Matcher != `deployment_environment_name="prod-cl-a"` || sc.Terms[0].Tier != httpEnvTierEntity {
		t.Fatalf("eşlemeli: %+v", sc)
	}
	if sc := run([]string{"prod-cl-a", "dr-cl-a"}); sc.Matcher != `deployment_environment_name="prod-cl-a"` {
		t.Fatalf("metrikte olmayan ortam düşer: %+v", sc)
	}
	if sc := run(nil); sc.Matcher != `deployment_environment_name="cl-a"` || sc.Terms[0].Tier != vmetrics.EnvTierExact {
		t.Fatalf("eşleme yoksa bugünkü davranış: %+v", sc)
	}
	if sc := run([]string{"qa-x"}); sc.Matcher != `deployment_environment_name="cl-a"` {
		t.Fatalf("eşleşmeyen eşleme ham ada düşer: %+v", sc)
	}
	// Cluster kendi etiketindeyse (LabelMap.Cluster) eşleme kullanılmaz.
	r := &fakeVMReader{lm: vmetrics.LabelMap{Cluster: "k8s_cluster_name"}, values: map[string][]string{
		"deployment_environment_name": {"prod-cl-a"}, "k8s_cluster_name": {"cl-a"}}}
	sc, _ := (&Server{}).resolveMetricEnvScopeMapped(context.Background(), r, httpEnvMetric, cl, []string{"prod-cl-a"}, ccTo)
	if sc.Matcher != `k8s_cluster_name="cl-a"` {
		t.Fatalf("cluster etiketi: %+v", sc)
	}
}

func TestServiceClusterEnvsNilSafe(t *testing.T) {
	if got := (&Server{}).serviceClusterEnvs(context.Background(), "svc", "cl-a", ccTo); got != nil {
		t.Fatalf("store yokken nil: %v", got)
	}
	if !strings.Contains(redFamilyKey(vmetrics.LabelMap{}, "s", "", "cl-a\x00prod-cl-a", ccTo, "vm"), "red-family:v1:") ||
		redFamilyKey(vmetrics.LabelMap{}, "s", "", "cl-a\x00prod-cl-a", ccTo, "vm") == redFamilyKey(vmetrics.LabelMap{}, "s", "", "cl-a\x00", ccTo, "vm") {
		t.Fatal("anahtar çözülen ortam kümesini taşımalı")
	}
}
