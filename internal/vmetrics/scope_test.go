package vmetrics

// v0.10.1839 — extra_filters[] carrier (Metrics Explorer Faz 1 dilim 1.5).

import (
	"context"
	"net/url"
	"testing"
)

func TestRequestCtxAddsScopeWithoutMutating(t *testing.T) {
	s := New()
	s.Configure(Settings{BaseURL: "http://vm:8428"})
	cfg := s.CurrentSettings()
	params := url.Values{"query": {"up"}}
	ctx := WithExtraFilters(context.Background(), []string{ClusterSelector("k8s_cluster_name", `acme-prod-1`)})
	req := s.requestCtx(ctx, "/api/v1/query", params, cfg)
	if got := req.Params.Get("extra_filters[]"); got != `{k8s_cluster_name="acme-prod-1"}` {
		t.Fatalf("extra_filters[] = %q", got)
	}
	if req.Params.Get("query") != "up" {
		t.Fatal("query text must ride unchanged")
	}
	if params.Has("extra_filters[]") {
		t.Fatal("caller params mutated")
	}
	if plain := s.requestCtx(context.Background(), "/api/v1/query", params, cfg); plain.Params.Has("extra_filters[]") {
		t.Fatal("no scope on ctx must add nothing")
	}
	if WithExtraFilters(context.Background(), nil) != context.Background() {
		t.Fatal("empty scope must return ctx unchanged")
	}
	if got := ClusterSelector("cluster", `a"b`); got != `{cluster="a\"b"}` {
		t.Fatalf("value must be Prometheus-quoted: %s", got)
	}
}

func TestClusterLabelDefaultsToConvention(t *testing.T) {
	s := New()
	s.Configure(Settings{BaseURL: "http://vm:8428"})
	if got := s.ClusterLabel(); got != "k8s_cluster_name" {
		t.Fatalf("default cluster label %q", got)
	}
	s.Configure(Settings{BaseURL: "http://vm:8428", LabelMap: LabelMap{Cluster: " cluster "}})
	if got := s.ClusterLabel(); got != "cluster" {
		t.Fatalf("configured cluster label %q", got)
	}
}
