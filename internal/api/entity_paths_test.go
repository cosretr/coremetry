package api

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// v0.10.2233 (Entity S3) — span → entity yolu, /api/logs entity kapsamı ve
// ad hoc log/trace çözümü ratchet'i.

func TestEntityPathFor(t *testing.T) {
	mapped := spanClusterRef{Cfg: thanos.ClusterConfig{ID: "c-1", Name: "east", Enabled: true}, ThanosOn: true}
	cases := []struct {
		name string
		h    entityPathHint
		ref  spanClusterRef
		want entityPath
	}{
		{"tam yol", entityPathHint{Cluster: "east-v", Namespace: "shop", Pod: "web-5c7dbf9b8d-x2b9z", Node: "w1", Service: "web"}, mapped,
			entityPath{Reason: "ok", ClusterValue: "east-v", ClusterID: "c-1", ClusterName: "east", Cluster: "cluster:c-1", Namespace: "ns:c-1/shop",
				Node: "node:c-1/w1", Pod: "pod:c-1/shop/web-5c7dbf9b8d-x2b9z", Service: "svc:web"}},
		{"k8s yok", entityPathHint{Service: "web"}, mapped, entityPath{Reason: "no-k8s", Service: "svc:web"}},
		{"cluster değeri yok", entityPathHint{Pod: "p"}, mapped, entityPath{Reason: "no-cluster"}},
		{"eşlenmemiş", entityPathHint{Cluster: "x", Pod: "p"}, spanClusterRef{ThanosOn: true, Reason: "no_remote_cluster"}, entityPath{Reason: "unmapped-cluster", ClusterValue: "x"}},
		{"namespace'siz", entityPathHint{Cluster: "east-v", Pod: "p", Node: "w1"}, mapped,
			entityPath{Reason: "no-namespace", ClusterValue: "east-v", ClusterID: "c-1", ClusterName: "east", Cluster: "cluster:c-1", Node: "node:c-1/w1"}},
	}
	for _, c := range cases {
		if got := entityPathFor(c.h, c.ref); got != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

func TestLogsEntityScopeFor(t *testing.T) {
	pod, _ := entity.ParseID("pod:c-1/shop/web-5c7dbf9b8d-x2b9z")
	sc, err := logsEntityScopeFor(pod, []string{"east-v"})
	if err != nil || sc != (logsEntityScope{Pod: "web-5c7dbf9b8d-x2b9z", Namespace: "shop", Cluster: "east-v"}) {
		t.Fatalf("pod: %+v %v", sc, err)
	}
	if sc, _ := logsEntityScopeFor(pod, []string{"a", "b"}); sc.Cluster != "" {
		t.Fatal("çok değerli kayıtta cluster süzgeci konmaz")
	}
	wl, _ := entity.ParseID("wl:c-1/shop/Deployment/web")
	if sc, _ := logsEntityScopeFor(wl, []string{"east-v"}); !sc.Workload || sc.Namespace != "shop" {
		t.Fatalf("wl: %+v", sc)
	}
	cl, _ := entity.ParseID("cluster:c-1")
	if _, err := logsEntityScopeFor(cl, []string{"a", "b"}); err == nil {
		t.Fatal("tek değeri olmayan cluster kapsamı reddedilir")
	}
	ctr, _ := entity.ParseID("ctr:c-1/shop/p/app")
	if _, err := logsEntityScopeFor(ctr, nil); err == nil {
		t.Fatal("container kapsamı desteklenmez")
	}
}

func TestApplyLogsEntityParam(t *testing.T) {
	s := &Server{}
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	run := func(q url.Values, f logstore.Filter) (logstore.Filter, int) {
		w := httptest.NewRecorder()
		ok := s.applyLogsEntityParam(w, httptest.NewRequest("GET", "/api/logs", nil), q, &f)
		if ok {
			return f, 200
		}
		return f, w.Code
	}
	win := logstore.Filter{From: t0.Add(-time.Hour), To: t0}
	f, code := run(url.Values{"entity": {"pod:edge-b/shop/web-5c7dbf9b8d-x2b9z"}}, win)
	if code != 200 || f.Pod != "web-5c7dbf9b8d-x2b9z" || f.Namespace != "shop" || f.Cluster != "edge-b" || f.Search != "" {
		t.Fatalf("pod kapsamı alan süzgeci olmalı: %d %+v", code, f)
	}
	if _, code := run(url.Values{"entity": {"pod:c/ns/p"}}, logstore.Filter{}); code != 400 {
		t.Fatalf("pencere zorunlu: %d", code)
	}
	if _, code := run(url.Values{"entity": {"pod:c/ns/p"}}, logstore.Filter{From: t0.Add(-8 * 24 * time.Hour), To: t0}); code != 400 {
		t.Fatalf("pencere ≤ 7 gün: %d", code)
	}
	if _, code := run(url.Values{"entity": {"svc:a"}}, logstore.Filter{Service: "b", From: win.From, To: win.To}); code != 400 {
		t.Fatalf("çelişen servis: %d", code)
	}
	if f, code := run(url.Values{}, win); code != 200 || f.Pod != "" {
		t.Fatal("entity yokken filtre değişmez")
	}
	if logsEntityKey(logstore.Filter{}) != "" || logsEntityKey(logstore.Filter{Pod: "a"}) == logsEntityKey(logstore.Filter{Pod: "b"}) {
		t.Fatal("anahtar: kapsamsızda boş, kapsamda pod'u hash'ler")
	}
}

// TestNoAdHocLogTraceJoin — pod logları alan süzgecinden, span → cluster
// çözümü tek yerden (resolveSpanCluster / POST /api/entity/paths).
func TestNoAdHocLogTraceJoin(t *testing.T) {
	banned := []string{"tracePodClusterFor(", "podLogSearch(", "podLogFilter(", "spanClusterValue === "}
	for _, root := range []string{"..", "../../frontend/src"} {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(p)
			if (ext != ".go" && ext != ".ts" && ext != ".tsx") || strings.HasSuffix(p, "entity_paths_test.go") {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			for _, s := range banned {
				if strings.Contains(string(b), s) {
					t.Errorf("%s: %q — log/trace ↔ entity çözümü entity modelinden geçmeli", p, s)
				}
			}
			return nil
		})
	}
}
