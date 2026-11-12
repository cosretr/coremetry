package ml

// v0.10.2019 — ML metrik aileleri için Thanos kaynağı: saf çekirdek.
// Kaynak seçimi, pod → servis eşlemesi (ServicePodRegex anlamı), cluster'lı
// kimlik / parça, servis toplaması, bütçe. Servis / cluster adları SENTETİK.

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

func TestPickFamilySource(t *testing.T) {
	prev := chstore.MLMetricSource{Source: "thanos", Clusters: []string{"prod-east"}}
	yes, no, unk := familyCheck{Present: true, Checked: true}, familyCheck{Checked: true}, familyCheck{}
	cases := []struct {
		name    string
		vm      familyCheck
		th      map[string]familyCheck
		prev    chstore.MLMetricSource
		want    string
		wantCls []string
	}{
		{"VM'de var → VM (Thanos'a bakılsa da)", yes, map[string]familyCheck{"prod-east": yes}, chstore.MLMetricSource{}, "vm", nil},
		{"VM'de yok, iki cluster'da var → Thanos, ad sıralı", no, map[string]familyCheck{"prod-west": yes, "prod-east": yes, "dr": no}, chstore.MLMetricSource{}, "thanos", []string{"prod-east", "prod-west"}},
		{"hiçbir yerde yok, hepsi ölçüldü → none", no, map[string]familyCheck{"prod-east": no}, chstore.MLMetricSource{}, "none", nil},
		{"Thanos yok, VM ölçüldü boş → none", no, nil, chstore.MLMetricSource{}, "none", nil},
		{"VM ölçülemedi, Thanos'ta yok → önceki karar korunur", unk, map[string]familyCheck{"prod-east": no}, prev, "thanos", []string{"prod-east"}},
		{"VM boş, bir cluster ölçülemedi, önceki yok → ölçülemedi", no, map[string]familyCheck{"prod-east": unk}, chstore.MLMetricSource{}, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pickFamilySource(c.vm, c.th, c.prev, time.Unix(100, 0))
			if got.Source != c.want || !reflect.DeepEqual(got.Clusters, c.wantCls) {
				t.Fatalf("kaynak = %+v; istenen %q %v", got, c.want, c.wantCls)
			}
		})
	}
}

// VM yolu yalnız kaynağı vm ya da ölçülemeyen aileyi sorar (ölçüm hatası
// bugünkü davranışı susturmaz); Thanos yolu yalnız thanos ailesini.
func TestFamilyRoutes(t *testing.T) {
	src := map[string]chstore.MLMetricSource{"metric_jvm": {Source: "thanos"}, "metric_pool": {Source: "vm"}, "metric_kafka": {Source: "none"}}
	for fam, want := range map[string][2]bool{"metric_jvm": {false, true}, "metric_pool": {true, false}, "metric_kafka": {false, false}, "metric_k8s": {true, false}} {
		if vm, th := familyOnVM(src, fam), familyOnThanos(src, fam); vm != want[0] || th != want[1] {
			t.Errorf("%s: vm=%v thanos=%v istenen %v", fam, vm, th, want)
		}
	}
}

// Pod → servis: ServicePodRegex ile aynı anlam (deployment | servis | ortam
// eksiz ad + "-"), en uzun önek kazanır, çakışan anahtar atlanır.
func TestPodIndex(t *testing.T) {
	svcs := []string{"checkout", "checkout-api", "mobile-loans-bff-prod", "ledger-int", "ledger-uat"}
	meta := map[string]chstore.ServiceMetadata{"checkout-api": {Deployment: "checkout-api-v2"}}
	ix := buildPodIndex(svcs, meta)
	cases := map[string]string{
		"checkout-7d9f8b6c5d-abcde":         "checkout",
		"checkout-api-v2-6b8f49b9d5-8hrtj":  "checkout-api",
		"checkout-api-5f6d7c8b9a-xk2lp":     "checkout-api",
		"mobile-loans-bff-6b8f49b9d5-8hrtj": "mobile-loans-bff-prod",
		"ledger-0":                          "", // ledger-int ve ledger-uat aynı ortam eksiz ada iner → belirsiz
		"payments-6b8f49b9d5-8hrtj":         "",
	}
	for pod, want := range cases {
		got, ok := ix.serviceOf(pod)
		if got != want || ok != (want != "") {
			t.Errorf("serviceOf(%q) = %q,%v istenen %q", pod, got, ok, want)
		}
		// eşleşen her pod, servisin ServicePodRegex'iyle de eşleşir
		if want != "" {
			re := regexp.MustCompile("^(?:" + thanos.ServicePodRegex(want, meta[want].Deployment) + ")$")
			if !re.MatchString(pod) {
				t.Errorf("%q, %s regex'iyle eşleşmiyor (anlam ayrıştı)", pod, want)
			}
		}
	}
}

func TestThanosPartAndRender(t *testing.T) {
	if p := thanosMetricPart("metric_jvm", "heap_used", "prod-east"); p != "metric_jvm|heap_used@prod-east" {
		t.Fatalf("parça %q", p)
	}
	if fam, id, cl := splitThanosPart("metric_jvm|heap_used@prod-east"); fam != "metric_jvm" || id != "heap_used" || cl != "prod-east" {
		t.Fatalf("ayrıştırma %q %q %q", fam, id, cl)
	}
	if _, _, cl := splitThanosPart("metric_jvm|heap_used"); cl != "" {
		t.Fatal("VM parçasında cluster olmamalı")
	}
	got := renderThanosExpr("avg by ({svc}) (jvm_memory_used_bytes{area=\"heap\"})", 300)
	if got != "avg by (namespace, pod) (jvm_memory_used_bytes{area=\"heap\"})" {
		t.Fatalf("pod düzeyi ifade: %q", got)
	}
	// aynı servis farklı cluster → farklı olay kimliği; label cluster'ı taşır
	a := chstore.MLEventID("metric_jvm", "checkout", thanosMetricPart("metric_jvm", "heap_used", "prod-east"))
	b := chstore.MLEventID("metric_jvm", "checkout", thanosMetricPart("metric_jvm", "heap_used", "prod-west"))
	if a == b {
		t.Fatal("cluster'lar ayrı kimlik almalı")
	}
	cfg := chstore.DefaultMLConfig()
	if l, _ := MetricLabelFor(cfg, "metric_jvm", "metric_jvm|heap_used@prod-east"); l != "Heap kullanımı" {
		t.Errorf("etiket %q", l)
	}
}

func TestExprAggAndCombine(t *testing.T) {
	for _, fam := range chstore.MLMetricDetectorNames {
		for _, e := range chstore.DefaultMLMetricFamilies()[fam] {
			if a := exprAgg(e.Expr); a == "" {
				t.Errorf("%s/%s: toplayıcı boş", fam, e.ID)
			}
		}
	}
	vals := []float64{2, 4, 9}
	for agg, want := range map[string]float64{"avg": 5, "sum": 15, "max": 9} {
		if got := combine(agg, vals); got != want {
			t.Errorf("%s = %v istenen %v", agg, got, want)
		}
	}
	if exprAgg("histogram_quantile(0.95, sum by ({svc}, le) (rate(x[5m])))") != "max" {
		t.Error("kuantil/oran ifadesi max ile birleşmeli")
	}
}

// Bütçe: cluster başına tik tavanı; fazlası sorulmaz ve sayılır.
func TestThanosPlanBudget(t *testing.T) {
	var exprs []thanosPlanned
	for i := 0; i < 15; i++ {
		exprs = append(exprs, thanosPlanned{family: "metric_k8s", id: string(rune('a' + i))})
	}
	run, cut := budgetPlan(exprs, thanosQueriesPerClusterTick)
	if len(run) != thanosQueriesPerClusterTick || cut != 15-thanosQueriesPerClusterTick || run[0].id != "a" {
		t.Fatalf("plan %d / kesik %d", len(run), cut)
	}
}

// D2: cluster'lı metrik olayı aynı servisin grubuna girer.
func TestThanosEventGroupsWithService(t *testing.T) {
	jvm := gEv("j", "metric_jvm", "checkout", 0, 20)
	jvm.Code = thanosMetricPart("metric_jvm", "heap_used", "prod-east")
	evs := []chstore.MLEvent{jvm, gEv("l", detOpLatency, "checkout", 5, 25)}
	gs := GroupMLEvents(evs, 10*time.Minute)
	if len(gs) != 1 || len(gs[0].Detectors) != 2 {
		t.Fatalf("tek grup bekleniyordu: %+v", gs)
	}
}

func TestThanosSummaryCarriesCluster(t *testing.T) {
	st := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	in := CardInput{Detector: "metric_jvm", Signal: chstore.MLSignalMetric, Service: "checkout", Code: "metric_jvm|heap_used@prod-east",
		Part: "metric_jvm|heap_used@prod-east", MetricLabel: "Heap kullanımı", MetricUnit: "bytes", StartedAt: st, LastSeen: st.Add(10 * time.Minute), BucketSec: 300,
		Observed: 2e9, Baseline: 1e9, Direction: "up", Now: st.Add(10 * time.Minute)}
	// v0.10.2032 — metrik özeti değer + normal + cluster.
	if s := ExplainSummary(in); !strings.HasPrefix(s, "checkout: Heap kullanımı prod-east cluster'ında") || !strings.Contains(s, "seviyesine çıktı (5 dakikalık ölçüm); normalde yaklaşık") {
		t.Errorf("özet: %q", s)
	}
}

// Batch kuralı: Load işaretli seri Thanos'tan gelse de (cluster ekli parça) yük sayılır.
func TestThanosBatchLoadPart(t *testing.T) {
	ev := chstore.MLEvent{Detector: "metric_k8s", Signal: chstore.MLSignalMetric, Service: "billing-batch", Code: thanosMetricPart("metric_k8s", "cpu", "prod-east")}
	if batchMeasureOf(ev, map[string]bool{"metric_k8s|cpu": true}) != batchMeasureLoad {
		t.Fatal("cluster ekli parça Load sınıfını kaybetmemeli")
	}
}
