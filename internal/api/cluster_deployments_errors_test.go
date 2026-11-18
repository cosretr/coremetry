package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
)

// v0.10.2367 — /api/clusters/deployments: Thanos hatası 500 değil; zaman aşımı
// 504, diğer upstream hatası 502, kısa JSON neden; 15 sn olumsuz önbellek;
// anahtar başına dakikada bir log.

func TestClassifyClusterDepErr(t *testing.T) {
	cases := []struct {
		err    error
		code   int
		reason string
	}{
		{context.DeadlineExceeded, http.StatusGatewayTimeout, "timeout"},
		{fmt.Errorf("thanos x: %w", context.DeadlineExceeded), http.StatusGatewayTimeout, "timeout"},
		{errors.New("thanos x: HTTP 503: unavailable"), http.StatusBadGateway, "upstream"},
		{errors.New("dial tcp: connection refused"), http.StatusBadGateway, "upstream"},
		{context.Canceled, 0, ""},
	}
	for _, c := range cases {
		code, reason := classifyClusterDepErr(c.err)
		if code != c.code || reason != c.reason {
			t.Errorf("%v → %d %q, want %d %q", c.err, code, reason, c.code, c.reason)
		}
	}
}

func newClusterDepTestServer() *Server {
	c, _ := cache.NewNoop()
	return &Server{cache: c, l1: newL1Cache(16), stats: newCacheStats()}
}

func callClusterDep(s *Server, key, q string, fn func(ctx context.Context) (any, error)) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/clusters/deployments?cluster=c1&namespace=ns"+q, nil)
	s.serveClusterDeployments(w, r, key, "c1", "ns", fn)
	return w
}

func TestServeClusterDeployments_TimeoutIs504AndNegativeCached(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clusterDepNow = func() time.Time { return now }
	t.Cleanup(func() { clusterDepNow = time.Now })
	s := newClusterDepTestServer()
	var calls atomic.Int32
	fail := func(ctx context.Context) (any, error) {
		calls.Add(1)
		return nil, fmt.Errorf("thanos c1: %w", context.DeadlineExceeded)
	}
	key := "cluster-deployments:test-timeout"
	w := callClusterDep(s, key, "", fail)
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("code %d, want 504", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["reason"] != "timeout" || body["error"] == "" {
		t.Fatalf("body %q", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "deadline") {
		t.Fatalf("ham hata gövdeye sızdı: %s", w.Body.String())
	}
	// 15 sn içinde aynı anahtar Thanos'a gitmez.
	now = now.Add(10 * time.Second)
	if w := callClusterDep(s, key, "", fail); w.Code != http.StatusGatewayTimeout || calls.Load() != 1 {
		t.Fatalf("olumsuz önbellek: code %d calls %d", w.Code, calls.Load())
	}
	// ?refresh=1 atlar.
	callClusterDep(s, key, "&refresh=1", fail)
	if calls.Load() != 2 {
		t.Fatalf("refresh olumsuz önbelleği atlamalı: calls %d", calls.Load())
	}
	// 15 sn sonra yeniden sorulur; başarı 200 + cache.
	now = now.Add(16 * time.Second)
	ok := func(ctx context.Context) (any, error) { calls.Add(1); return map[string]any{"count": 0}, nil }
	if w := callClusterDep(s, key, "", ok); w.Code != http.StatusOK || calls.Load() != 3 {
		t.Fatalf("başarı: code %d calls %d", w.Code, calls.Load())
	}
	if w := callClusterDep(s, key, "", ok); w.Code != http.StatusOK || calls.Load() != 3 {
		t.Fatalf("başarı önbellekte kalmalı: code %d calls %d", w.Code, calls.Load())
	}
}

func TestServeClusterDeployments_UpstreamIs502(t *testing.T) {
	s := newClusterDepTestServer()
	w := callClusterDep(s, "cluster-deployments:test-upstream", "", func(ctx context.Context) (any, error) {
		return nil, errors.New("thanos c1: HTTP 503: down")
	})
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), `"reason":"upstream"`) {
		t.Fatalf("code %d body %s", w.Code, w.Body.String())
	}
}

func TestClusterDepFail_LogsOncePerMinutePerKey(t *testing.T) {
	now := time.Unix(1_900_000_000, 0)
	clusterDepNow = func() time.Time { return now }
	t.Cleanup(func() { clusterDepNow = time.Now })
	e := errors.New("boom")
	if !clusterDepFail("k-log", "cl-log", "ns", 502, "upstream", e) {
		t.Fatal("ilk hata loglanmalı")
	}
	now = now.Add(30 * time.Second)
	if clusterDepFail("k-log", "cl-log", "ns", 502, "upstream", e) {
		t.Fatal("dakika dolmadan ikinci log yok")
	}
	if !clusterDepFail("k-log2", "cl-log", "ns2", 502, "upstream", e) {
		t.Fatal("farklı namespace ayrı anahtar")
	}
	now = now.Add(31 * time.Second)
	if !clusterDepFail("k-log", "cl-log", "ns", 502, "upstream", e) {
		t.Fatal("dakika sonra yeniden loglanmalı")
	}
}

func TestGetClusterDeploymentsUsesClassifiedServe(t *testing.T) {
	src, err := os.ReadFile("thanos_handlers.go")
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(src), "func (s *Server) getClusterDeployments(")
	j := strings.Index(string(src)[i:], "\n}\n")
	body := string(src)[i : i+j]
	if !strings.Contains(body, "s.serveClusterDeployments(") || strings.Contains(body, "s.serveCached(") {
		t.Fatal("getClusterDeployments sınıflı yoldan (serveClusterDeployments) servis etmeli")
	}
}
