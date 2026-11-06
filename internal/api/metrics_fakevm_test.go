package api

// metrics_fakevm_test.go — v0.10.1835 (Metrics Explorer Faz 1): an httptest
// VictoriaMetrics that answers the Prometheus-API shapes VM documents
// (status/data/isPartial envelope; matrix/vector results; string-array
// label endpoints). No real VM is available to the test suite, so every
// VM behaviour these tests lean on is ASSUMED from the docs and listed in
// the Faz 1 report under "not verified".
//
// The fake records every request (path + decoded query/form values) so a
// test can assert what Coremetry SENT — the half a response-only fake
// cannot see (extra_filters, topk wrapping, limits).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

type fakeVMReq struct {
	Path   string
	Values url.Values
}

type fakeVM struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []fakeVMReq
	// handle answers one request; nil → fakeVMDefault.
	handle func(path string, v url.Values) (int, string)
}

func newFakeVM(t *testing.T, handle func(path string, v url.Values) (int, string)) *fakeVM {
	t.Helper()
	f := &fakeVM{handle: handle}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		v := url.Values{}
		for k, vs := range r.Form {
			v[k] = append([]string(nil), vs...)
		}
		f.mu.Lock()
		f.got = append(f.got, fakeVMReq{Path: r.URL.Path, Values: v})
		f.mu.Unlock()
		h := f.handle
		if h == nil {
			h = fakeVMDefault
		}
		code, body := h(r.URL.Path, v)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeVM) requests() []fakeVMReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeVMReq(nil), f.got...)
}

// requestsTo — only the requests for one path.
func (f *fakeVM) requestsTo(path string) []fakeVMReq {
	var out []fakeVMReq
	for _, r := range f.requests() {
		if r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// server — a Server whose DEFAULT metric source is this fake VM (Enabled +
// BaseURL), with an always-miss cache so every request computes.
func (f *fakeVM) server() *Server {
	vm := vmetrics.New()
	vm.Configure(vmetrics.Settings{Enabled: true, BaseURL: f.srv.URL})
	return &Server{
		store:    &chstore.Store{},
		vmetrics: vm,
		cache:    &fakeCache{},
		l1:       newL1Cache(64),
		stats:    newCacheStats(),
	}
}

func fakeVMDefault(path string, _ url.Values) (int, string) {
	switch {
	case path == "/api/v1/query_range":
		return 200, vmMatrix(nil, false)
	case path == "/api/v1/query":
		return 200, vmVector(nil, false)
	case path == "/api/v1/labels", strings.HasPrefix(path, "/api/v1/label/"):
		return 200, `{"status":"success","data":[]}`
	}
	return 404, `{"status":"error","errorType":"not_found","error":"unknown path"}`
}

// vmSeries — one fake series: labels + one value per point.
type vmSeries struct {
	labels map[string]string
	values []float64
}

func vmLabelsJSON(l map[string]string) string {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	// deterministic
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	var b strings.Builder
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%q:%q", k, l[k])
	}
	b.WriteString("}")
	return b.String()
}

// vmMatrix — query_range body. Points are stamped 1700000060, +60, … (the
// first lies after the request start used by these tests, so rangeSeries
// keeps it).
func vmMatrix(series []vmSeries, partial bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"status":"success","isPartial":%v,"data":{"resultType":"matrix","result":[`, partial)
	for i, s := range series {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"metric":%s,"values":[`, vmLabelsJSON(s.labels))
		for j, v := range s.values {
			if j > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `[%d,"%g"]`, 1700000060+60*j, v)
		}
		b.WriteString("]}")
	}
	b.WriteString("]}}")
	return b.String()
}

func vmVector(series []vmSeries, partial bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"status":"success","isPartial":%v,"data":{"resultType":"vector","result":[`, partial)
	for i, s := range series {
		if i > 0 {
			b.WriteString(",")
		}
		v := 0.0
		if len(s.values) > 0 {
			v = s.values[len(s.values)-1]
		}
		fmt.Fprintf(&b, `{"metric":%s,"value":[1700003600,"%g"]}`, vmLabelsJSON(s.labels), v)
	}
	b.WriteString("]}}")
	return b.String()
}

// nSeries — n series labelled job=j000…, constant value i+1 (so the
// ranking order is fully determined by i).
func nSeries(n int) []vmSeries {
	out := make([]vmSeries, n)
	for i := range out {
		out[i] = vmSeries{labels: map[string]string{"job": fmt.Sprintf("j%03d", i)}, values: []float64{float64(i + 1), float64(i + 1)}}
	}
	return out
}
