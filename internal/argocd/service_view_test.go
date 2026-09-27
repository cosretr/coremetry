package argocd

// v0.10.981 — GitOps sekmesi eşleme çekirdeği (service_view.go).

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestServiceAppQuery(t *testing.T) {
	if q := ServiceAppQuery(nil, []string{" ", ""}); q != "" {
		t.Fatalf("boş girdi sorgu üretmemeli: %q", q)
	}
	q := ServiceAppQuery([]string{"pay", "pay", "a.b"}, []string{`x"y`})
	for _, want := range []string{
		`max by (job, namespace, exported_namespace, name,`,
		// pin kolu topk'suz ve önde; namespace kolu topk'lu
		`autosync_enabled) (argocd_app_info{name=~"x\"y"}) or topk(50, max by (`,
		`(argocd_app_info{dest_namespace=~"a\\.b|pay"}))`, // QuoteMeta + dize kaçışı, tekil, sıralı
	} {
		if !strings.Contains(q, want) {
			t.Errorf("sorgu %q parçasını içermiyor:\n%s", want, q)
		}
	}
	if strings.Count(q, "topk(") != 1 {
		t.Errorf("tek topk (namespace kolu) beklenir: %s", q)
	}
	if strings.Contains(q, "pod") || strings.Contains(q, "instance,") {
		t.Errorf("pod/instance by'a girmemeli (kopyalar katlanır): %s", q)
	}
}

func TestPromRegexAltCap(t *testing.T) {
	var vals []string
	for i := 0; i < ServiceSelectorCap+10; i++ {
		vals = append(vals, strings.Repeat("a", i+1))
	}
	if n := strings.Count(promRegexAlt(vals), "|") + 1; n != ServiceSelectorCap {
		t.Fatalf("alternasyon %d, tavan %d", n, ServiceSelectorCap)
	}
}

func TestServiceSyncQuery(t *testing.T) {
	if ServiceSyncQuery(nil) != "" {
		t.Fatal("boş ad listesi sorgu üretmemeli")
	}
	want := `sum by (namespace, exported_namespace, name, phase) (increase(argocd_app_sync_total{name=~"a|b"}[24h]))`
	if got := ServiceSyncQuery([]string{"b", "a"}); got != want {
		t.Fatalf("got %s", got)
	}
}

func TestParseAppInfoVector(t *testing.T) {
	raw := json.RawMessage(`[
	 {"metric":{"job":"t-prod-metrics","namespace":"t-prod","exported_namespace":"apps","name":"a1","sync_status":"Synced","health_status":"Healthy","autosync_enabled":"true"},"value":[1,"1"]},
	 {"metric":{"job":"t-prod-metrics","namespace":"t-prod","exported_namespace":"apps","name":"a1","sync_status":"OutOfSync"},"value":[1,"1"]},
	 {"metric":{"namespace":"t-dev","name":"a2","autosync_enabled":"false"},"value":[1,"1"]},
	 {"metric":{"namespace":"t-dev","name":""},"value":[1,"1"]}
	]`)
	apps, err := ParseAppInfoVector("hub-1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 {
		t.Fatalf("iki tekil uygulama beklenir, %d", len(apps))
	}
	by := map[string]AppStatus{apps[0].Name: apps[0], apps[1].Name: apps[1]}
	a1, a2 := by["a1"], by["a2"] // sıra etiket dizgisine göre (belirlenimli), ada göre bak
	if a1.AppNamespace != "apps" || a1.InstanceNamespace != "t-prod" || a1.SyncStatus != "Synced" || a1.AutoSync == nil || !*a1.AutoSync {
		t.Errorf("a1: %+v", a1)
	}
	if a2.AppNamespace != "t-dev" || a2.InstanceNamespace != "" || a2.AutoSync == nil || *a2.AutoSync || a2.HubClusterID != "hub-1" {
		t.Errorf("a2 (honorLabels: namespace = uygulama ns, instance yok): %+v", a2)
	}
	if _, err := ParseAppInfoVector("h", json.RawMessage(`{`)); err == nil {
		t.Error("bozuk vektör hata vermeli")
	}
}

func TestParseSyncVector(t *testing.T) {
	raw := json.RawMessage(`[
	 {"metric":{"namespace":"t","exported_namespace":"apps","name":"a1","phase":"Succeeded"},"value":[1,"2.9"]},
	 {"metric":{"namespace":"apps","name":"a1","phase":"Failed"},"value":[1,"1.2"]},
	 {"metric":{"namespace":"apps","name":"a1","phase":"Error"},"value":[1,"0.2"]},
	 {"metric":{"namespace":"apps","name":"a1","phase":"Running"},"value":[1,"NaN"]}
	]`)
	got, err := ParseSyncVector(raw)
	if err != nil {
		t.Fatal(err)
	}
	m := got[[2]string{"apps", "a1"}]
	if m["Succeeded"] != 3 || m["Failed"] != 1 || len(m) != 2 {
		t.Fatalf("fazlar %v (0.2 ve NaN atılır, yuvarlanır)", m)
	}
}

func TestNameHasPart(t *testing.T) {
	cases := []struct {
		name, part string
		want       bool
	}{
		{"pay-team-checkout-api-prod-a", "checkout-api", true},
		{"pay-team-checkout-api-prod-a", "checkout", true},
		{"pay-team-checkout-api-prod-a", "check", false},
		{"checkout", "checkout", true},
		{"Pay-Checkout-prod", "checkout", true},
		{"x", "", false},
	}
	for _, c := range cases {
		if got := nameHasPart(c.name, c.part); got != c.want {
			t.Errorf("nameHasPart(%q,%q)=%v", c.name, c.part, got)
		}
	}
}

func TestMatchServiceApps(t *testing.T) {
	norm := func(s string) string { return strings.TrimSuffix(strings.ToLower(s), "/") }
	settings := Settings{
		Instances: []Instance{
			{ID: "i1", HubClusterID: "hub", HubNamespace: "t-prod", Name: "Prod Argo"},
			{ID: "i2", HubClusterID: "hub", HubNamespace: "t-dev", MetricsJob: "t-dev-metrics"},
		},
		Pins: []Pin{
			{ClusterID: "c-a", Namespace: "pay", Workload: "ledger", InstanceID: "i1", AppNamespace: "apps", AppName: "custom-ledger-app"},
			// servise ait olmayan iş yükünün pini yok sayılır
			{ClusterID: "c-z", Namespace: "x", Workload: "y", InstanceID: "i1", AppNamespace: "apps", AppName: "other"},
		},
		Mapping: Mapping{NameConfidence: 70},
	}
	wl := []ServiceWorkload{
		{ClusterID: "c-a", Namespace: "pay", Workload: "checkout"},
		{ClusterID: "c-b", Namespace: "pay", Workload: "checkout"},
		{ClusterID: "c-a", Namespace: "pay", Workload: "ledger"},
	}
	apps := []AppStatus{
		// ad eşleşmesi, dest_server c-a'ya çözülür → yalnız c-a iş yükü
		{HubClusterID: "hub", InstanceNamespace: "t-prod", AppNamespace: "apps", Name: "p-pay-checkout-prod-a", DestServer: "https://API.A:6443/", DestNamespace: "pay"},
		// dest_server çözülemez → iki kümedeki checkout'a da (tahmini)
		{HubClusterID: "hub", InstanceNamespace: "t-prod", AppNamespace: "apps", Name: "p-pay-checkout-prod", DestServer: "https://unknown", DestNamespace: "pay"},
		// pinli iş yükü: ad tahmini YOK; pin uygulaması manual
		{HubClusterID: "hub", InstanceNamespace: "t-prod", AppNamespace: "apps", Name: "p-pay-ledger-prod-a", DestServer: "https://api.a:6443", DestNamespace: "pay"},
		{HubClusterID: "hub", InstanceNamespace: "t-prod", AppNamespace: "apps", Name: "custom-ledger-app", DestServer: "https://api.a:6443", DestNamespace: "elsewhere"},
		// aynı namespace, eşleşmeyen → sayılır
		{HubClusterID: "hub", InstanceNamespace: "t-prod", AppNamespace: "apps", Name: "p-pay-billing-prod-a", DestNamespace: "pay"},
		// başka namespace → sayılmaz
		{HubClusterID: "hub", AppNamespace: "t-dev", Job: "t-dev-metrics", Name: "p-pay-checkout-dev", DestNamespace: "pay-dev"},
		// in-cluster = hub kümesi; iş yükü c-a'da → eşleşmez, sayılır
		{HubClusterID: "hub", InstanceNamespace: "t-prod", AppNamespace: "apps", Name: "x-checkout-y", DestServer: InClusterServer, DestNamespace: "pay"},
	}
	res := MatchServiceApps(ServiceMatchInput{
		Apps: apps, Workloads: wl, Settings: settings,
		ClusterByServer: map[string]string{"https://api.a:6443": "c-a"}, NormalizeServer: norm,
	})
	if len(res.Apps) != 3 {
		t.Fatalf("3 eşleşme beklenir, %d: %+v", len(res.Apps), res.Apps)
	}
	m := res.Apps[0]
	if m.Name != "custom-ledger-app" || m.Match != MatchManual || m.Confidence != 100 || m.InstanceID != "i1" || m.InstanceName != "Prod Argo" ||
		len(m.Workloads) != 1 || m.Workloads[0].Workload != "ledger" {
		t.Errorf("manual önce: %+v", m)
	}
	byName := map[string]ServiceApp{}
	for _, a := range res.Apps {
		byName[a.Name] = a
	}
	if a := byName["p-pay-checkout-prod-a"]; a.Match != MatchName || a.Confidence != 70 || a.DestCluster != "c-a" || len(a.Workloads) != 1 || a.Workloads[0].ClusterID != "c-a" {
		t.Errorf("çözülen dest_server tek kümeyle eşleşmeli: %+v", a)
	}
	if a := byName["p-pay-checkout-prod"]; a.DestCluster != "" || len(a.Workloads) != 2 {
		t.Errorf("çözülemeyen dest_server iki kümeye de: %+v", a)
	}
	if _, ok := byName["p-pay-ledger-prod-a"]; ok {
		t.Error("pinli iş yükü için ad tahmini yapılmamalı")
	}
	if res.OtherInNamespace != 3 {
		t.Errorf("aynı namespace'te eşleşmeyen: ledger-ad, billing, in-cluster = 3; got %d", res.OtherInNamespace)
	}
}

func TestPinnedAppNames(t *testing.T) {
	pins := []Pin{
		{ClusterID: "c", Namespace: "n", Workload: "w", AppName: "a"},
		{ClusterID: "c", Namespace: "n", Workload: "other", AppName: "b"},
	}
	got := PinnedAppNames(pins, []ServiceWorkload{{ClusterID: "c", Namespace: "n", Workload: "w"}})
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("got %v", got)
	}
}
