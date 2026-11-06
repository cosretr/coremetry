package api

// v0.10.1839 — cluster scope (Metrics Explorer Faz 1 dilim 1.5). Kabul
// ölçütü (audit §7): "İki cluster'da ad listesi farklı; anahtar cluster
// taşır". extra_filters[] semantics are ASSUMED from the VM docs (single
// selector; the OR/AND rule for several is undocumented — we send one).

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// clusterVM — the name list depends on the extra_filters[] the request sent:
// acme-prod-1 carries orders-api's metrics, acme-prod-2 payments-mobile's.
func clusterVM(t *testing.T) *fakeVM {
	return newFakeVM(t, func(path string, v url.Values) (int, string) {
		ef := v.Get("extra_filters[]")
		switch path {
		case "/api/v1/label/__name__/values":
			switch ef {
			case `{k8s_cluster_name="acme-prod-1"}`:
				return 200, `{"status":"success","data":["orders_created_total"]}`
			case `{k8s_cluster_name="acme-prod-2"}`:
				return 200, `{"status":"success","data":["payments_settled_total"]}`
			case "":
				return 200, `{"status":"success","data":["orders_created_total","payments_settled_total"]}`
			}
			return 200, `{"status":"success","data":[]}`
		case "/api/v1/metadata":
			return 200, `{"status":"success","data":{}}`
		}
		return fakeVMDefault(path, v)
	})
}

func namesFor(t *testing.T, s *Server, cluster string) []string {
	t.Helper()
	u := "/api/metrics/names?limit=50"
	if cluster != "" {
		u += "&cluster=" + url.QueryEscape(cluster)
	}
	rec := httptest.NewRecorder()
	s.getMetricNames(rec, httptest.NewRequest("GET", u, nil))
	var p struct {
		Names []chstore.MetricInfo `json:"names"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("%v %s", err, rec.Body)
	}
	out := make([]string, len(p.Names))
	for i, n := range p.Names {
		out[i] = n.Name
	}
	return out
}

func TestClusterScopeNarrowsNames(t *testing.T) {
	s := clusterVM(t).server()
	a := strings.Join(namesFor(t, s, "acme-prod-1"), ",")
	b := strings.Join(namesFor(t, s, "acme-prod-2"), ",")
	all := strings.Join(namesFor(t, s, ""), ",")
	allExplicit := strings.Join(namesFor(t, s, "all"), ",")
	if a != "orders_created_total" || b != "payments_settled_total" || all != "orders_created_total,payments_settled_total" || allExplicit != all {
		t.Fatalf("a=%q b=%q all=%q all(explicit)=%q", a, b, all, allExplicit)
	}
}

func TestClusterScopeLeavesQueryTextUntouched(t *testing.T) {
	fv := clusterVM(t)
	s := fv.server()
	const q = `sum by (service_name) (rate(http_server_request_duration_seconds_count[5m]))`
	rec := httptest.NewRecorder()
	s.queryPromQL(rec, httptest.NewRequest("GET", "/api/metrics/promql?meta=1&cluster=acme-prod-1&step=60&query="+url.QueryEscape(q)+"&"+goldenWindow(), nil))
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	reqs := fv.requestsTo("/api/v1/query_range")
	if len(reqs) != 1 || reqs[0].Values.Get("query") != q || reqs[0].Values.Get("extra_filters[]") != `{k8s_cluster_name="acme-prod-1"}` {
		t.Fatalf("sent %+v", reqs)
	}
	var e struct {
		Meta metricMeta `json:"meta"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Meta.Cluster != "acme-prod-1" || e.Meta.Executed != q {
		t.Fatalf("meta %+v", e.Meta)
	}
	// builder query + label values + attr keys + histogram are scoped too
	for _, u := range []string{
		"/api/metrics/query?name=orders_created_total&agg=sum&step=60&cluster=acme-prod-1&" + goldenWindow(),
		"/api/metrics/labels?metric=orders_created_total&key=service.name&cluster=acme-prod-1",
		"/api/metrics/attr-keys?metric=orders_created_total&cluster=acme-prod-1",
		"/api/metrics/histogram?name=http_server_request_duration_seconds&service=orders-api&step=60&cluster=acme-prod-1&" + goldenWindow(),
	} {
		before := len(fv.requests())
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", u, nil)
		switch {
		case strings.HasPrefix(u, "/api/metrics/query"):
			s.queryMetric(rec, r)
		case strings.HasPrefix(u, "/api/metrics/labels"):
			s.getMetricLabelValues(rec, r)
		case strings.HasPrefix(u, "/api/metrics/attr-keys"):
			s.getMetricAttrKeys(rec, r)
		default:
			s.getMetricHistogram(rec, r)
		}
		after := fv.requests()[before:]
		if len(after) == 0 {
			t.Fatalf("%s: no VM request (%d %s)", u, rec.Code, rec.Body)
		}
		for _, req := range after {
			if req.Values.Get("extra_filters[]") != `{k8s_cluster_name="acme-prod-1"}` {
				t.Errorf("%s → %s without the scope: %v", u, req.Path, req.Values)
			}
		}
	}
}

func TestClusterLabelFollowsLabelMap(t *testing.T) {
	fv := clusterVM(t)
	vm := vmetrics.New()
	vm.Configure(vmetrics.Settings{Enabled: true, BaseURL: fv.srv.URL, LabelMap: vmetrics.LabelMap{Cluster: "cluster"}})
	s := fv.server()
	s.vmetrics = vm
	namesFor(t, s, "acme-prod-1")
	reqs := fv.requestsTo("/api/v1/label/__name__/values")
	if len(reqs) != 1 || reqs[0].Values.Get("extra_filters[]") != `{cluster="acme-prod-1"}` {
		t.Fatalf("configured cluster label not used: %+v", reqs)
	}
}

func TestClusterScopeKeys(t *testing.T) {
	// VM: every scoped surface forks its key by cluster
	s, rc := faz2Server()
	s.vmetrics.Configure(vmetrics.Settings{Enabled: true, BaseURL: "http://vm:8428"})
	win := fmt.Sprintf("from=%d&to=%d", faz2From.UnixNano(), faz2To.UnixNano())
	calls := []func(c string){
		func(c string) {
			s.getMetricNames(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/metrics/names?limit=5"+c, nil))
		},
		func(c string) {
			s.queryMetric(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/metrics/query?name=m&"+win+c, nil))
		},
		func(c string) {
			s.queryPromQL(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/metrics/promql?query=up&"+win+c, nil))
		},
		func(c string) {
			s.getMetricLabelValues(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/metrics/labels?metric=m&key=k"+c, nil))
		},
		func(c string) {
			s.getMetricAttrKeys(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/metrics/attr-keys?metric=m"+c, nil))
		},
		func(c string) {
			s.getMetricHistogram(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/metrics/histogram?name=m&"+win+c, nil))
		},
	}
	for i, call := range calls {
		call("")
		k0 := rc.last()
		call("&cluster=all")
		kAll := rc.last()
		call("&cluster=acme-prod-1")
		k1 := rc.last()
		call("&cluster=acme-prod-2")
		k2 := rc.last()
		if k0 != kAll || k0 == k1 || k1 == k2 || strings.Contains(k0, ":cl=") {
			t.Errorf("surface %d keys: none=%s all=%s p1=%s p2=%s", i, k0, kAll, k1, k2)
		}
	}
	// CH: builder query forks (filter applied), selectors do NOT (not applied)
	s3, rc3 := faz2Server()
	s3.queryMetric(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/metrics/query?name=m&"+win+"&cluster=acme-prod-1", nil))
	if !strings.Contains(rc3.last(), ":cl=acme-prod-1") {
		t.Errorf("CH query key lacks the cluster: %s", rc3.last())
	}
	s3.getMetricAttrKeys(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/metrics/attr-keys?metric=m&cluster=acme-prod-1", nil))
	if strings.Contains(rc3.last(), ":cl=") {
		t.Errorf("CH attr-keys cannot scope; key must not fork: %s", rc3.last())
	}
}

func TestClusterScopeCHFilter(t *testing.T) {
	cs := clusterScope{Value: "acme-prod-1"}
	got := cs.chFilters([]chstore.FilterExpr{{Key: "service.name", Op: "=", Values: []string{"orders-api"}}})
	if len(got) != 2 || got[1].Key != "cluster" || got[1].Values[0] != "acme-prod-1" {
		t.Fatalf("%+v", got)
	}
	if out := (clusterScope{}).chFilters(nil); out != nil {
		t.Fatalf("no scope must not add a filter: %+v", out)
	}
	if parseClusterValue(" ALL ") != "" || parseClusterValue("") != "" || parseClusterValue("acme-prod-1") != "acme-prod-1" {
		t.Fatal("parseClusterValue")
	}
}
