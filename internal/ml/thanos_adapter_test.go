package ml

// v0.10.2019 — Thanos adaptörü SAHTE Thanos'a karşı (canlı çağrı YOK):
// etkin cluster listesi, anlık vektör çözümü, kısmi yanıt = hata; cluster
// izolasyonu (yanıt vermeyen cluster ötekileri bekletmez).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/thanos"
)

func fakeThanos(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		if !strings.Contains(r.Form.Get("query"), "jvm") {
			http.Error(w, "beklenmeyen sorgu", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestThanosAdapterInstant(t *testing.T) {
	ok := fakeThanos(t, `{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"namespace":"shop","pod":"checkout-7d9f8b6c5d-abcde"},"value":[1700000000,"123.5"]},
		{"metric":{"namespace":"shop","pod":"checkout-7d9f8b6c5d-fghij"},"value":[1700000000,"NaN"]}]}}`)
	partial := fakeThanos(t, `{"status":"success","warnings":["store unavailable"],"data":{"resultType":"vector","result":[]}}`)
	svc := thanos.New()
	svc.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{
		{ID: "c-00000001", Name: "prod-east", URL: ok.URL, Enabled: true, NamespaceFilter: "shop"},
		{ID: "c-00000002", Name: "prod-west", URL: partial.URL, Enabled: true},
		{ID: "c-00000003", Name: "kapali", URL: ok.URL, Enabled: false},
	}})
	q := NewThanosQuerier(svc)
	cls := q.Clusters()
	if len(cls) != 2 || cls[0].Name != "prod-east" || cls[0].NamespaceFilter != "shop" {
		t.Fatalf("etkin cluster'lar: %+v", cls)
	}
	rows, err := q.Instant(context.Background(), "c-00000001", "avg by (namespace, pod) (jvm_threads_current)", time.Unix(1700000000, 0))
	if err != nil || len(rows) != 1 || rows[0].Value != 123.5 || rows[0].Labels["pod"] != "checkout-7d9f8b6c5d-abcde" {
		t.Fatalf("vektör: %+v err=%v (NaN atlanmalı)", rows, err)
	}
	if _, err := q.Instant(context.Background(), "c-00000002", "count(jvm_threads_current)", time.Now()); err == nil {
		t.Fatal("kısmi yanıt hata sayılmalı (kısmi okumadan yokluk çıkarılmaz)")
	}
	if NewThanosQuerier(nil) != nil {
		t.Fatal("Thanos yoksa querier nil")
	}
}

// Bir cluster yanıt vermezse yalnız o cluster son tarihine takılır; öteki
// sonucunu verir ve toplam süre son tarihle sınırlı kalır.
func TestForClustersIsolation(t *testing.T) {
	old := thanosClusterDeadline
	thanosClusterDeadline = 200 * time.Millisecond
	defer func() { thanosClusterDeadline = old }()
	var mu sync.Mutex
	done := map[string]bool{}
	t0 := time.Now()
	forClusters(context.Background(), []ThanosCluster{{ID: "a", Name: "slow"}, {ID: "b", Name: "fast"}}, func(ctx context.Context, c ThanosCluster) {
		if c.Name == "slow" {
			<-ctx.Done()
			return
		}
		mu.Lock()
		done[c.Name] = true
		mu.Unlock()
	})
	if !done["fast"] {
		t.Fatal("hızlı cluster sonucu kayboldu")
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("yavaş cluster tüm tiki bekletti: %v", d)
	}
}

// Pod örnekleri → servis değerleri: namespace kalkanı, eşlenemeyen pod sayımı.
func TestPodValuesByService(t *testing.T) {
	ix := buildPodIndex([]string{"checkout"}, nil)
	rows := []ThanosSample{
		{Labels: map[string]string{"namespace": "shop", "pod": "checkout-7d9f8b6c5d-abcde"}, Value: 1},
		{Labels: map[string]string{"namespace": "shop", "pod": "checkout-7d9f8b6c5d-fghij"}, Value: 3},
		{Labels: map[string]string{"namespace": "shop", "pod": "orphan-7d9f8b6c5d-fghij"}, Value: 9},
		{Labels: map[string]string{"namespace": "kube-system", "pod": "checkout-7d9f8b6c5d-zzzzz"}, Value: 100},
	}
	vals, un := podValuesByService(rows, ix, namespaceRegex("shop|payments"))
	if len(vals["checkout"]) != 2 || un != 1 {
		t.Fatalf("değerler %v eşlenemeyen %d", vals, un)
	}
	if namespaceRegex("") != nil || namespaceRegex("(bozuk") != nil {
		t.Error("boş/bozuk filtre süzgeçsiz")
	}
}
