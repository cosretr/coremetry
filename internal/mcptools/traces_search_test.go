package mcptools

// traces_search_test.go — v0.10.2465 (operator-reported, prod 2026-11-22):
// çıplak değer ("ocp5") önce cluster, sonra env (tam / parça), sonra
// namespace; hiçbiri değilse url yorumu (endpoint_lookup'a). Araç sözleşmesi:
// her yorum sayısıyla, ilk okunan yorum servis / operasyon / örnek / link.
// Link kodeki Go↔TS: testdata/cosre/traces-deeplink-v1.json (TS tarafı
// frontend/src/pages/traces/tracesDeepLink.contract.test.ts aynı dosyayı
// Traces sayfasının çözücüleriyle — URLSearchParams + decodeFilters +
// decodeRange — okur).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestResolveTraceValueOrder(t *testing.T) {
	dims := TraceValueDims{
		Clusters:   []string{"c1", "c2"},
		Envs:       []string{"prod-c1", "c1", "uat-c3"},
		Namespaces: []string{"c1", "payments"},
	}
	got := ResolveTraceValue("C1", dims, nil)
	want := []string{"cluster:c1:exact", "env:c1:exact", "env:prod-c1:segment"}
	if len(got) != len(want) {
		t.Fatalf("yorumlar = %+v, want %v", got, want)
	}
	for i, m := range got {
		if k := m.Kind + ":" + m.Value + ":" + m.Match; k != want[i] {
			t.Errorf("[%d] = %s, want %s (sıra cluster → env tam → env parça → namespace)", i, k, want[i])
		}
	}
	// Remote Cluster adı → span değeri.
	if got := ResolveTraceValue("east", TraceValueDims{}, []ClusterRef{{ID: "c-east", Name: "east", SpanValues: []string{"c3"}}}); len(got) != 1 || got[0].Kind != TraceValueCluster || got[0].Value != "c3" {
		t.Errorf("Remote Cluster çözümü = %+v", got)
	}
	// Yalnız namespace.
	if got := ResolveTraceValue("payments", dims, nil); len(got) != 1 || got[0].Kind != TraceValueNamespace {
		t.Errorf("namespace = %+v", got)
	}
	// Hiçbir boyut → url yorumu (okuma yok, endpoint_lookup önerisi).
	got = ResolveTraceValue("orders-v2", dims, nil)
	if len(got) != 1 || got[0].Kind != TraceValueURL || !strings.Contains(got[0].Note, "endpoint_lookup(url=orders-v2)") {
		t.Errorf("url yorumu = %+v", got)
	}
	if ResolveTraceValue("  ", dims, nil) != nil {
		t.Error("boş değer yorum üretmemeli")
	}
}

func TestTracesSearchContract(t *testing.T) {
	var aggs []chstore.AggregateFilter
	var tfs []chstore.TraceFilter
	r := tsrReader{
		dims: func(_ context.Context, v string) (TraceValueDims, error) {
			return TraceValueDims{Clusters: []string{"c1"}, Envs: []string{"prod-c1"}}, nil
		},
		agg: func(_ context.Context, f chstore.AggregateFilter) ([]chstore.AggregateRow, error) {
			aggs = append(aggs, f)
			if f.GroupBy == "operation" {
				return []chstore.AggregateRow{{GroupKey: "GET /orders", GroupExtra: "orders-api", TraceCount: 900}}, nil
			}
			if f.Env != "" { // env yorumu: daha az
				return []chstore.AggregateRow{{GroupKey: "orders-api", TraceCount: 10}}, nil
			}
			return []chstore.AggregateRow{
				{GroupKey: "billing-api", TraceCount: 300, ErrorCount: 3, P99Ms: 120.5},
				{GroupKey: "orders-api", TraceCount: 900, ErrorCount: 9, P99Ms: 80},
			}, nil
		},
		traces: func(_ context.Context, f chstore.TraceFilter) ([]chstore.TraceRow, uint64, bool, error) {
			tfs = append(tfs, f)
			return []chstore.TraceRow{{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736"}}, 0, false, nil
		},
	}
	out, err := runTracesSearch(context.Background(), Deps{}, r, tracesSearchArgs{Value: "c1", RangeS: 3600})
	if err != nil {
		t.Fatal(err)
	}
	ms := out["interpretations"].([]TraceValueMatch)
	if len(ms) != 2 || ms[0].Kind != TraceValueCluster || ms[0].Requests != 1200 || ms[0].Errors != 12 || !ms[0].Read ||
		ms[1].Kind != TraceValueEnv || ms[1].Value != "prod-c1" || ms[1].Requests != 10 {
		t.Fatalf("yorumlar = %+v", ms)
	}
	top := out["top_services"].([]tsrService)
	if top[0].Service != "orders-api" || top[0].Traces != 900 || top[1].P99Ms != 120.5 {
		t.Errorf("top_services = %+v (trace sayısına göre)", top)
	}
	if out["matched"] != TraceValueCluster {
		t.Errorf("matched = %v", out["matched"])
	}
	if ids := out["sample_trace_ids"].([]string); len(ids) != 1 {
		t.Errorf("örnekler = %v", ids)
	}
	link, _ := out["deep_link"].(string)
	if !strings.HasPrefix(link, "/traces?") || !strings.Contains(link, "cluster=c1") || !strings.Contains(link, "range=custom%3A") {
		t.Errorf("deep_link = %q", link)
	}
	// Kapsam: cluster yorumu Cluster alanı, env yorumu Env alanı; örnek okuması yalnız ilk yorumda.
	if aggs[0].Cluster != "c1" || aggs[0].GroupBy != "service" || len(tfs) != 1 || tfs[0].Cluster != "c1" || tfs[0].Limit != tsrSamples {
		t.Errorf("okumalar: aggs=%+v tfs=%+v", aggs, tfs)
	}
	// url yorumu okuma koşmaz.
	aggs = nil
	r.dims = func(context.Context, string) (TraceValueDims, error) { return TraceValueDims{}, nil }
	out, err = runTracesSearch(context.Background(), Deps{}, r, tracesSearchArgs{Value: "zz9"})
	if err != nil || len(aggs) != 0 || out["deep_link"] != nil {
		t.Errorf("url yorumu: err=%v aggs=%d out=%v", err, len(aggs), out)
	}
	// Pencere tavanı.
	out, _ = runTracesSearch(context.Background(), Deps{}, r, tracesSearchArgs{Cluster: "c1", RangeS: 86400})
	if out["window_s"] != tsrMaxRangeS {
		t.Errorf("window_s = %v, want %d", out["window_s"], tsrMaxRangeS)
	}
	// Argümansız çağrı reddedilir.
	if _, err := runTracesSearch(context.Background(), Deps{}, r, tracesSearchArgs{}); err == nil {
		t.Error("boş argüman kabul edildi")
	}
}

// deepLinkFixture — Go↔TS link sözleşmesi (testdata/cosre/traces-deeplink-v1.json).
type deepLinkFixture struct {
	Name   string         `json:"name"`
	Href   string         `json:"href"`
	Expect map[string]any `json:"expect"`
}

func TestTracesDeepLinkFixture(t *testing.T) {
	from := time.UnixMilli(1763820000000).UTC()
	to := time.UnixMilli(1763823600000).UTC()
	cases := []struct {
		name string
		f    chstore.TraceFilter
	}{
		{"cluster", chstore.TraceFilter{Cluster: "c1", Sort: "time"}},
		{"env+service+errors", chstore.TraceFilter{Env: "prod-c1", Service: "orders-api", HasError: true, Sort: "time"}},
		{"namespace filter+duration", chstore.TraceFilter{Filters: []chstore.FilterExpr{{Key: "k8s.namespace.name", Op: "=", Values: []string{"payments"}}}, MinMs: 250, Search: "GET /orders", Sort: "time"}},
	}
	var got []deepLinkFixture
	for _, c := range cases {
		fx := deepLinkFixture{Name: c.name, Href: TracesSearchLink(c.f, from, to), Expect: map[string]any{
			"service": c.f.Service, "search": c.f.Search, "cluster": c.f.Cluster, "env": c.f.Env, "hasError": c.f.HasError,
			"minMs": c.f.MinMs, "fromMs": from.UnixMilli(), "toMs": to.UnixMilli(), "filters": c.f.Filters,
		}}
		if fx.Expect["filters"] == nil || len(c.f.Filters) == 0 {
			fx.Expect["filters"] = []chstore.FilterExpr{}
		}
		got = append(got, fx)
	}
	body, err := json.MarshalIndent(map[string]any{"version": 1, "cases": got}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')
	path := filepath.Join("..", "..", "testdata", "cosre", "traces-deeplink-v1.json")
	if *updateToolsetGolden {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture okunamadı (-update ile üret): %v", err)
	}
	if string(want) != string(body) {
		t.Errorf("TracesDeepLink çıktısı fixture'dan ayrıştı — TS sözleşmesini de güncelle (go test -run TestTracesDeepLinkFixture -update)")
	}
}
