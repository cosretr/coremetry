package argocd

// settings_test.go — v0.10.957 — system_settings["argocd"] sözleşmesi
// (Rollouts v2 P1.4; docs/rollouts/v2-audit.md §10.7, §7.2–7.3, §7.5, §5.6;
// karar 2, 5). Saf seam'ler tablolu: varsayılanlar, okuma-yolu kelepçesi
// (sıfır/geçersiz → korunan varsayılan, tavan üstü → tavan), PUT doğrulaması
// (alan yolu taşıyan 400), düz token reddi, apiUrl normalleştirme ve
// kalıcılık (eski pod'un bayat blobu yeni PUT'u ezmez). Canlı CH / ağ YOK.

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"testing"
)

func bptr(b bool) *bool { return &b }

func TestDefaultSettings(t *testing.T) {
	d := DefaultSettings()
	if d.Enabled || len(d.Hubs) != 0 || len(d.EnvList) != 0 || len(d.Instances) != 0 || len(d.Pins) != 0 {
		t.Fatalf("varsayılan KAPALI ve boş olmalı: %+v", d)
	}
	if !(Hub{ClusterID: "c-1"}).Inject() {
		t.Fatal("hub injectClusterLabel varsayılanı true (her Thanos sorgusunun bugünkü davranışı)")
	}
	want := struct {
		api  APIWorker
		cls  Classification
		rd   Reader
		iv   Intervals
		mapp Mapping
	}{
		APIWorker{RPS: 1, Burst: 5, MaxConcurrent: 2},                                                // §7.3
		Classification{WindowMin: 5, OutOfBandLookbackMin: 30, MetricsOnlyMode: MetricsOnlyEstimate}, // §7.5
		Reader{MaxSeries: 50000, MaxBodyMiB: 64, TimeoutS: 30},                                       // karar 2
		Intervals{MetricsS: 60, InventoryMin: 15, MapperMin: 10, ClassifierReevalH: 24},              // §10.7
		Mapping{NameConfidence: 70, NamespaceConfidence: 30},                                         // annex §7.4
	}
	if d.APIWorker != want.api || d.Classification != want.cls || d.Reader != want.rd || d.Intervals != want.iv || d.Mapping != want.mapp {
		t.Fatalf("varsayılanlar audit'ten saptı:\n got %+v %+v %+v %+v %+v", d.APIWorker, d.Classification, d.Reader, d.Intervals, d.Mapping)
	}
	// Varsayılanlar kendi kelepçesinden değişmeden geçer (tablo tutarlılığı).
	n := d.Normalized()
	if n.APIWorker != d.APIWorker || n.Classification != d.Classification || n.Reader != d.Reader || n.Intervals != d.Intervals || n.Mapping != d.Mapping {
		t.Fatalf("varsayılanlar normalize'da değişti: %+v", n)
	}
	// Varsayılanlar kendi PUT doğrulamasından geçer.
	if _, err := Validate(d, nil); err != nil {
		t.Fatalf("varsayılan blob geçerli olmalı: %v", err)
	}
}

// JSON alan adları audit §10.7 ile birebir (FE + P3 işçileri sözleşmesi).
func TestSettingsJSONFieldNames(t *testing.T) {
	s := DefaultSettings()
	s.Hubs = []Hub{{ClusterID: "c-1", InjectClusterLabel: bptr(false)}}
	s.EnvList = []string{"prod"}
	s.Instances = []Instance{{ID: "team-a-prod", HubClusterID: "c-1", Name: "n", HubNamespace: "team-a-prod", MetricsJob: "j", APIURL: "https://argocd.example.invalid",
		TokenRef: "env:X", InsecureSkipVerify: true, Enabled: true, Discovered: true, AppsAnyNamespace: true}}
	s.Pins = []Pin{{ClusterID: "c-1", Namespace: "ns", WorkloadKind: "Deployment", Workload: "w", InstanceID: "team-a-prod", AppNamespace: "team-a-prod", AppName: "a"}}
	s.UpdatedAt = 1
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]json.RawMessage) string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	// v0.10.957 — inceleme: iki hub (§5.6) — üst düzey hubClusterId /
	// injectClusterLabel YOK; hubs[] + instance başına hubClusterId.
	if got, want := keys(m), "apiWorker,classification,enabled,envList,hubs,instances,intervals,mapping,pins,reader,updatedAt"; got != want {
		t.Fatalf("üst alanlar:\n got %s\nwant %s", got, want)
	}
	var hubs []map[string]json.RawMessage
	_ = json.Unmarshal(m["hubs"], &hubs)
	if got, want := keys(hubs[0]), "clusterId,injectClusterLabel"; got != want {
		t.Fatalf("hub alanları:\n got %s\nwant %s", got, want)
	}
	var inst []map[string]json.RawMessage
	_ = json.Unmarshal(m["instances"], &inst)
	if got, want := keys(inst[0]), "apiUrl,appsAnyNamespace,discovered,enabled,hubClusterId,hubNamespace,id,insecureSkipVerify,metricsJob,name,tokenRef"; got != want {
		t.Fatalf("instance alanları:\n got %s\nwant %s", got, want)
	}
	for _, bad := range []string{`"token"`, `"password"`} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("blob düz gizli alan taşımamalı: %s", raw)
		}
	}
	for name, want := range map[string]string{
		"apiWorker":      "burst,maxConcurrent,rps",
		"classification": "metricsOnlyMode,outOfBandLookbackMin,windowMin",
		"reader":         "maxBodyMiB,maxSeries,timeoutS",
		"intervals":      "classifierReevalH,inventoryMin,mapperMin,metricsS",
		"mapping":        "nameConfidence,namespaceConfidence",
	} {
		var sub map[string]json.RawMessage
		_ = json.Unmarshal(m[name], &sub)
		if got := keys(sub); got != want {
			t.Errorf("%s alanları: got %s want %s", name, got, want)
		}
	}
	var pins []map[string]json.RawMessage
	_ = json.Unmarshal(m["pins"], &pins)
	if got, want := keys(pins[0]), "appName,appNamespace,clusterId,instanceId,namespace,workload,workloadKind"; got != want {
		t.Fatalf("pin alanları:\n got %s\nwant %s", got, want)
	}
}

func TestNormalizedClamps(t *testing.T) {
	d := DefaultSettings()
	cases := []struct {
		name  string
		in    func(*Settings)
		check func(t *testing.T, n Settings)
	}{
		{"sıfır blob → tüm varsayılanlar", func(s *Settings) { *s = Settings{} }, func(t *testing.T, n Settings) {
			if n.Reader != d.Reader || n.APIWorker != d.APIWorker || n.Intervals != d.Intervals || n.Mapping != d.Mapping || n.Classification != d.Classification {
				t.Fatalf("%+v", n)
			}
		}},
		{"karar 2 tavanları: reader tavana kelepçelenir", func(s *Settings) { s.Reader = Reader{MaxSeries: 50001, MaxBodyMiB: 65, TimeoutS: 46} },
			func(t *testing.T, n Settings) {
				if n.Reader != (Reader{MaxSeries: 50000, MaxBodyMiB: 64, TimeoutS: 45}) {
					t.Fatalf("%+v", n.Reader)
				}
			}},
		{"negatif → varsayılan", func(s *Settings) { s.Reader = Reader{MaxSeries: -1, MaxBodyMiB: -1, TimeoutS: -1} },
			func(t *testing.T, n Settings) {
				if n.Reader != d.Reader {
					t.Fatalf("%+v", n.Reader)
				}
			}},
		{"alt sınır altı → alt sınır", func(s *Settings) { s.Reader = Reader{MaxSeries: 10, MaxBodyMiB: 1, TimeoutS: 1} },
			func(t *testing.T, n Settings) {
				if n.Reader != (Reader{MaxSeries: 1000, MaxBodyMiB: 1, TimeoutS: 5}) {
					t.Fatalf("%+v", n.Reader)
				}
			}},
		{"rps: 0 → 1, NaN → 1, çok küçük → 0.1, çok büyük → 20", func(s *Settings) { s.APIWorker.RPS = math.NaN() },
			func(t *testing.T, n Settings) {
				if n.APIWorker.RPS != 1 {
					t.Fatalf("NaN → %v", n.APIWorker.RPS)
				}
				for in, want := range map[float64]float64{0: 1, -3: 1, 0.01: 0.1, 99: 20, 2.5: 2.5, math.Inf(1): 1} {
					x := Settings{APIWorker: APIWorker{RPS: in}}.Normalized().APIWorker.RPS
					if x != want {
						t.Fatalf("rps %v → %v, beklenen %v", in, x, want)
					}
				}
			}},
		{"out_of_band penceresi ≥ eşleşme penceresi", func(s *Settings) { s.Classification = Classification{WindowMin: 45, OutOfBandLookbackMin: 10} },
			func(t *testing.T, n Settings) {
				if n.Classification.WindowMin != 45 || n.Classification.OutOfBandLookbackMin != 45 {
					t.Fatalf("%+v", n.Classification)
				}
			}},
		{"bilinmeyen metricsOnlyMode → estimate; büyük harf kabul", func(s *Settings) { s.Classification.MetricsOnlyMode = "bogus" },
			func(t *testing.T, n Settings) {
				if n.Classification.MetricsOnlyMode != MetricsOnlyEstimate {
					t.Fatalf("%q", n.Classification.MetricsOnlyMode)
				}
				if got := (Settings{Classification: Classification{MetricsOnlyMode: " UNKNOWN "}}).Normalized().Classification.MetricsOnlyMode; got != MetricsOnlyUnknown {
					t.Fatalf("%q", got)
				}
			}},
		// v0.10.957 — enjeksiyon HUB BAŞINA (§5.6): nil → true, false korunur.
		{"hub başına injectClusterLabel: nil → true, false korunur",
			func(s *Settings) {
				s.Hubs = []Hub{{ClusterID: "c-a"}, {ClusterID: "c-b", InjectClusterLabel: bptr(false)}}
			},
			func(t *testing.T, n Settings) {
				if len(n.Hubs) != 2 || n.Hubs[0].InjectClusterLabel == nil || !*n.Hubs[0].InjectClusterLabel ||
					n.Hubs[1].InjectClusterLabel == nil || *n.Hubs[1].InjectClusterLabel {
					t.Fatalf("%+v", n.Hubs)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := DefaultSettings()
			tc.in(&s)
			tc.check(t, s.Normalized())
		})
	}
}

// Normalized aynı dilimleri paylaşmaz: okuyucu kopyayı değiştirirse canlı
// ayar bozulmasın.
func TestNormalizedDoesNotAlias(t *testing.T) {
	s := Settings{EnvList: []string{"prod"}, Instances: []Instance{{ID: "a"}}, Pins: []Pin{{Workload: "w"}},
		Hubs: []Hub{{ClusterID: "c-a", InjectClusterLabel: bptr(true)}}}
	n := s.Normalized()
	n.EnvList[0], n.Instances[0].ID, n.Pins[0].Workload, n.Hubs[0].ClusterID = "x", "x", "x", "x"
	*n.Hubs[0].InjectClusterLabel = false
	if s.EnvList[0] != "prod" || s.Instances[0].ID != "a" || s.Pins[0].Workload != "w" || s.Hubs[0].ClusterID != "c-a" || !*s.Hubs[0].InjectClusterLabel {
		t.Fatalf("Normalized girdiyi paylaşıyor: %+v", s)
	}
}

var testClusters = []ClusterRef{
	{ID: "c-aaaa0001", Name: "cluster-a", Enabled: true},
	{ID: "c-bbbb0002", Name: "cluster-b", Enabled: true},
	{ID: "c-off00003", Name: "cluster-off", Enabled: false},
}

func validBlob() Settings {
	return Settings{
		Enabled: true,
		Hubs:    []Hub{{ClusterID: "c-aaaa0001"}},
		EnvList: []string{"prod", "uat"},
		Instances: []Instance{
			{ID: "team-a-prod", HubClusterID: "c-aaaa0001", Name: "team-a-prod", HubNamespace: "team-a-prod", MetricsJob: "team-a-prod-metrics",
				APIURL: "https://argocd-team-a.apps.example.invalid", TokenRef: "env:COREMETRY_ARGOCD_TEAM_A", Enabled: true},
			// hubClusterId boş: tek hub varken o hub'a tamamlanır (§5.6)
			{ID: "team-b-uat", HubNamespace: "team-b-uat", MetricsJob: "team-b-uat-metrics", Discovered: true},
		},
		Pins: []Pin{{ClusterID: "c-bbbb0002", Namespace: "checkout", WorkloadKind: "Deployment", Workload: "checkout-api",
			InstanceID: "team-a-prod", AppNamespace: "team-a-prod", AppName: "p-team-a-checkout-prod-b"}},
	}
}

func TestValidateAcceptsAndCanonicalizes(t *testing.T) {
	in := validBlob()
	in.Hubs[0].ClusterID = "  c-aaaa0001 "
	in.Instances[0].HubClusterID = " c-aaaa0001 "
	in.EnvList = []string{" PROD ", "uat", "prod", ""}
	in.Instances[0].APIURL = "HTTPS://ArgoCD-Team-A.Apps.Example.Invalid/"
	in.Instances[0].TokenRef = " env:COREMETRY_ARGOCD_TEAM_A "
	in.Instances[0].ID = " team-a-prod "
	in.Pins[0].WorkloadKind = "deployment"
	in.Classification.MetricsOnlyMode = " Unknown "
	in.UpdatedAt = 999
	out, err := Validate(in, testClusters)
	if err != nil {
		t.Fatalf("geçerli blob reddedildi: %v", err)
	}
	if len(out.Hubs) != 1 || out.Hubs[0].ClusterID != "c-aaaa0001" {
		t.Errorf("hub kırpılmalı: %+v", out.Hubs)
	}
	if out.Instances[0].HubClusterID != "c-aaaa0001" || out.Instances[1].HubClusterID != "c-aaaa0001" {
		t.Errorf("instance hub'ı kırpılmalı / tek hub'a tamamlanmalı: %+v", out.Instances)
	}
	if strings.Join(out.EnvList, ",") != "prod,uat" {
		t.Errorf("envList küçük harf + tekil + boşsuz: %v", out.EnvList)
	}
	if out.Instances[0].APIURL != "https://argocd-team-a.apps.example.invalid" {
		t.Errorf("apiUrl normalleşmeli: %q", out.Instances[0].APIURL)
	}
	if out.Instances[0].TokenRef != "env:COREMETRY_ARGOCD_TEAM_A" || out.Instances[0].ID != "team-a-prod" {
		t.Errorf("kırpma: %+v", out.Instances[0])
	}
	if out.Pins[0].WorkloadKind != "Deployment" {
		t.Errorf("tür kanonik yazım: %q", out.Pins[0].WorkloadKind)
	}
	if out.Classification.MetricsOnlyMode != MetricsOnlyUnknown {
		t.Errorf("mod: %q", out.Classification.MetricsOnlyMode)
	}
	if out.UpdatedAt != 0 {
		t.Errorf("updatedAt sunucu sahipli, girdi atılır: %d", out.UpdatedAt)
	}
	// Sayısal sıfırlar sıfır kalır (= "varsayılanı kullan"; rollouts emsali:
	// saklanan girdidir, uygulanan Normalized).
	if out.Reader != (Reader{}) {
		t.Errorf("sıfır reader saklanmalı: %+v", out.Reader)
	}
	// Girdi değişmedi.
	if in.EnvList[0] != " PROD " {
		t.Error("Validate girdiyi yerinde değiştirmemeli")
	}
}

func TestValidateRejectsWithFieldPath(t *testing.T) {
	mod := func(f func(*Settings)) Settings {
		s := validBlob()
		f(&s)
		return s
	}
	cases := []struct {
		name string
		in   Settings
		path string
	}{
		{"bilinmeyen hub", mod(func(s *Settings) { s.Hubs[0].ClusterID = "c-nope" }), "hubs[0].clusterId"},
		{"hub adı id değil", mod(func(s *Settings) { s.Hubs[0].ClusterID = "cluster-a" }), "hubs[0].clusterId"},
		{"hub id boş", mod(func(s *Settings) { s.Hubs[0].ClusterID = " " }), "hubs[0].clusterId"},
		{"enabled + hub yok", mod(func(s *Settings) { s.Hubs = nil }), "hubs"},
		{"enabled + devre dışı hub", mod(func(s *Settings) { s.Hubs = append(s.Hubs, Hub{ClusterID: "c-off00003"}) }), "hubs[1].clusterId"},
		{"hub tekrar", mod(func(s *Settings) { s.Hubs = append(s.Hubs, Hub{ClusterID: " c-aaaa0001"}) }), "hubs[1].clusterId"},
		{"instance hub'ı listede değil", mod(func(s *Settings) { s.Instances[0].HubClusterID = "c-bbbb0002" }), "instances[0].hubClusterId"},
		{"iki hub + instance hub'ı boş", mod(func(s *Settings) { s.Hubs = append(s.Hubs, Hub{ClusterID: "c-bbbb0002"}) }), "instances[1].hubClusterId"},
		{"env boşluklu", mod(func(s *Settings) { s.EnvList = []string{"pr od"} }), "envList[0]"},
		{"env tireli (ad ayrıştırmayı bozar)", mod(func(s *Settings) { s.EnvList = []string{"prod", "pre-prod"} }), "envList[1]"},
		{"instance id boş", mod(func(s *Settings) { s.Instances[1].ID = "" }), "instances[1].id"},
		{"instance id büyük harf", mod(func(s *Settings) { s.Instances[1].ID = "Team-B" }), "instances[1].id"},
		{"instance id tekrar", mod(func(s *Settings) { s.Instances[1].ID = "team-a-prod" }), "instances[1].id"},
		{"hubNamespace boş", mod(func(s *Settings) { s.Instances[0].HubNamespace = "" }), "instances[0].hubNamespace"},
		{"hubNamespace geçersiz", mod(func(s *Settings) { s.Instances[0].HubNamespace = "Team_A" }), "instances[0].hubNamespace"},
		{"hubNamespace tekrar", mod(func(s *Settings) { s.Instances[1].HubNamespace = "team-a-prod" }), "instances[1].hubNamespace"},
		{"metricsJob kontrol karakteri", mod(func(s *Settings) { s.Instances[0].MetricsJob = "a\nb" }), "instances[0].metricsJob"},
		{"apiUrl şemasız", mod(func(s *Settings) { s.Instances[0].APIURL = "argocd.example.invalid" }), "instances[0].apiUrl"},
		{"apiUrl userinfo", mod(func(s *Settings) { s.Instances[0].APIURL = "https://u:p@argocd.example.invalid" }), "instances[0].apiUrl"},
		{"tokenRef düz token", mod(func(s *Settings) { s.Instances[0].TokenRef = "eyJhbGciOi" }), "instances[0].tokenRef"},
		{"pin bilinmeyen instance", mod(func(s *Settings) { s.Pins[0].InstanceID = "nope" }), "pins[0].instanceId"},
		{"pin bilinmeyen cluster", mod(func(s *Settings) { s.Pins[0].ClusterID = "c-nope" }), "pins[0].clusterId"},
		{"pin bilinmeyen tür", mod(func(s *Settings) { s.Pins[0].WorkloadKind = "CronJob" }), "pins[0].workloadKind"},
		{"pin workload boş", mod(func(s *Settings) { s.Pins[0].Workload = " " }), "pins[0].workload"},
		{"pin app boş", mod(func(s *Settings) { s.Pins[0].AppName = "" }), "pins[0].appName"},
		{"pin tekrar", mod(func(s *Settings) { s.Pins = append(s.Pins, s.Pins[0]) }), "pins[1]"},
		{"reader tavan üstü (karar 2)", mod(func(s *Settings) { s.Reader.MaxSeries = 50001 }), "reader.maxSeries"},
		{"reader gövde tavan üstü", mod(func(s *Settings) { s.Reader.MaxBodyMiB = 65 }), "reader.maxBodyMiB"},
		{"reader timeout tavan üstü", mod(func(s *Settings) { s.Reader.TimeoutS = 46 }), "reader.timeoutS"},
		{"negatif burst", mod(func(s *Settings) { s.APIWorker.Burst = -1 }), "apiWorker.burst"},
		{"rps NaN", mod(func(s *Settings) { s.APIWorker.RPS = math.NaN() }), "apiWorker.rps"},
		{"rps aralık dışı", mod(func(s *Settings) { s.APIWorker.RPS = 50 }), "apiWorker.rps"},
		{"interval aralık dışı", mod(func(s *Settings) { s.Intervals.MetricsS = 5 }), "intervals.metricsS"},
		{"bilinmeyen metricsOnlyMode", mod(func(s *Settings) { s.Classification.MetricsOnlyMode = "maybe" }), "classification.metricsOnlyMode"},
		{"out_of_band < pencere", mod(func(s *Settings) { s.Classification = Classification{WindowMin: 30, OutOfBandLookbackMin: 10} }), "classification.outOfBandLookbackMin"},
		{"zayıf güven ≥ tahmini", mod(func(s *Settings) { s.Mapping = Mapping{NameConfidence: 40, NamespaceConfidence: 60} }), "mapping.namespaceConfidence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Validate(tc.in, testClusters)
			var fe *FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("FieldError beklenirdi, %v", err)
			}
			if fe.Path != tc.path {
				t.Fatalf("yol %q, beklenen %q (%v)", fe.Path, tc.path, err)
			}
			if !strings.HasPrefix(err.Error(), tc.path+": ") {
				t.Fatalf("mesaj yolu taşımalı: %q", err.Error())
			}
		})
	}
}

// Kapalı blobda hub isteğe bağlı ama verilmişse VAR olmalı; devre dışı hub
// kapalı blobda kabul (operatör önce hazırlar, sonra açar).
func TestValidateHubWhenDisabled(t *testing.T) {
	s := validBlob()
	s.Enabled = false
	s.Hubs, s.Instances, s.Pins = nil, nil, nil
	if _, err := Validate(s, testClusters); err != nil {
		t.Fatalf("kapalı + hub yok geçerli: %v", err)
	}
	s.Hubs = []Hub{{ClusterID: "c-off00003"}}
	if _, err := Validate(s, testClusters); err != nil {
		t.Fatalf("kapalı + devre dışı hub geçerli: %v", err)
	}
	s.Hubs = []Hub{{ClusterID: "c-nope"}}
	if _, err := Validate(s, testClusters); err == nil {
		t.Fatal("kapalı blobda da bilinmeyen hub 400")
	}
	s.Hubs = []Hub{{ClusterID: "c-aaaa0001"}}
	if _, err := Validate(s, nil); err == nil {
		t.Fatal("cluster listesi yokken hub doğrulanamaz → 400")
	}
	// hub'sız blobda instance bağlanamaz (her instance kendi hub'ını taşır, §5.6)
	s.Hubs = nil
	s.Instances = []Instance{{ID: "team-a-prod", HubNamespace: "team-a-prod"}}
	var fe *FieldError
	if _, err := Validate(s, testClusters); !errors.As(err, &fe) || fe.Path != "instances[0].hubClusterId" {
		t.Fatalf("hub'sız instance → instances[0].hubClusterId: %v", err)
	}
}

// v0.10.957 — inceleme: Argo CD İKİ hub'da koşar (operatör, audit §5.6).
// Her instance kendi hub'ını taşır; id tüm hub'larda tekil; hubNamespace
// HUB BAŞINA tekil (iki hub'daki "openshift-gitops" iki ayrı instance'tır);
// enjeksiyon hub başına saklanır.
func TestValidateTwoHubs(t *testing.T) {
	s := validBlob()
	s.Hubs = []Hub{{ClusterID: "c-aaaa0001"}, {ClusterID: "c-bbbb0002", InjectClusterLabel: bptr(false)}}
	s.Instances = []Instance{
		{ID: "gitops-a", HubClusterID: "c-aaaa0001", HubNamespace: "openshift-gitops"},
		{ID: "gitops-b", HubClusterID: "c-bbbb0002", HubNamespace: "openshift-gitops"},
		{ID: "team-a-prod", HubClusterID: "c-bbbb0002", HubNamespace: "team-a-prod"},
	}
	out, err := Validate(s, testClusters)
	if err != nil {
		t.Fatalf("iki hub'da aynı namespace geçerli: %v", err)
	}
	if len(out.Hubs) != 2 || out.Hubs[1].Inject() || !out.Hubs[0].Inject() {
		t.Fatalf("hub başına enjeksiyon korunmalı: %+v", out.Hubs)
	}
	if h, ok := out.HubByID("c-bbbb0002"); !ok || h.Inject() {
		t.Fatalf("HubByID: %+v %v", h, ok)
	}
	for _, tc := range []struct {
		name string
		mut  func(*Settings)
		path string
	}{
		{"aynı hub'da aynı ns", func(s *Settings) { s.Instances[2].HubNamespace = "openshift-gitops" }, "instances[2].hubNamespace"},
		{"id hub'lar arası tekrar", func(s *Settings) { s.Instances[1].ID = "gitops-a" }, "instances[1].id"},
		{"iki hub + boş instance hub'ı", func(s *Settings) { s.Instances[0].HubClusterID = "" }, "instances[0].hubClusterId"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := s
			bad.Instances = append([]Instance(nil), s.Instances...)
			tc.mut(&bad)
			var fe *FieldError
			if _, err := Validate(bad, testClusters); !errors.As(err, &fe) || fe.Path != tc.path {
				t.Fatalf("FieldError{%q} beklenirdi: %v", tc.path, err)
			}
		})
	}
}

// https://kubernetes.default.svc yalnız hub'ın kendi kaydında (audit §3.2,
// §5.3); lane R2 thanos_clusters'ta işaretsiz kabul eder, kapı burada.
func TestValidateInClusterServerOnlyOnHub(t *testing.T) {
	clusters := []ClusterRef{
		{ID: "c-aaaa0001", Name: "cluster-a", Enabled: true, APIServerURLs: []string{"https://kubernetes.default.svc"}},
		{ID: "c-bbbb0002", Name: "cluster-b", Enabled: true, APIServerURLs: []string{"https://api.cluster-b.example.invalid:6443"}},
	}
	s := validBlob()
	if _, err := Validate(s, clusters); err != nil {
		t.Fatalf("hub'da in-cluster URL geçerli: %v", err)
	}
	s.Hubs = []Hub{{ClusterID: "c-bbbb0002"}}
	s.Instances[0].HubClusterID = "c-bbbb0002"
	_, err := Validate(s, clusters)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Path != "hubs" || !strings.Contains(err.Error(), "cluster-a") {
		t.Fatalf("hub olmayan kayıtta in-cluster URL reddedilmeli, bağlı kaydı söylemeli: %v", err)
	}
	// v0.10.957 — iki hub: her hub kendi in-cluster adresini taşıyabilir (§5.6).
	clusters[1].APIServerURLs = []string{"https://kubernetes.default.svc:443"}
	s.Hubs = []Hub{{ClusterID: "c-aaaa0001"}, {ClusterID: "c-bbbb0002"}}
	s.Instances[1].HubClusterID = "c-aaaa0001"
	if _, err := Validate(s, clusters); err != nil {
		t.Fatalf("iki hub'ın ikisi de in-cluster adresi taşıyabilir: %v", err)
	}
	for _, spelling := range []string{"HTTPS://Kubernetes.Default.Svc/", "https://kubernetes.default.svc:443", "https://kubernetes.default.svc.cluster.local:6443"} {
		if !IsInClusterServer(spelling) {
			t.Errorf("%q in-cluster sayılmalı", spelling)
		}
	}
	for _, other := range []string{"", "https://api.cluster-a.example.invalid:6443", "http://kubernetes.default.svc.example.invalid", "not a url"} {
		if IsInClusterServer(other) {
			t.Errorf("%q in-cluster SAYILMAMALI", other)
		}
	}
}

func TestParseInputRejectsPlaintextToken(t *testing.T) {
	cases := []struct {
		name, body, path string
	}{
		{"instance token", `{"instances":[{"id":"a","hubNamespace":"a"},{"id":"b","hubNamespace":"b","token":"eyJ"}]}`, "instances[1].token"},
		{"büyük harfli anahtar", `{"instances":[{"id":"a","hubNamespace":"a","Password":"x"}]}`, "instances[0].Password"},
		{"üst düzey token", `{"token":"x"}`, "token"},
		{"apiToken", `{"instances":[{"id":"a","apiToken":"x"}]}`, "instances[0].apiToken"},
		// v0.10.957 — tek-hub şekli sessizce atılmaz (§5.6 iki hub)
		{"eski üst düzey hubClusterId", `{"enabled":true,"hubClusterId":"c-1"}`, "hubClusterId"},
		{"eski üst düzey injectClusterLabel", `{"injectClusterLabel":false}`, "injectClusterLabel"},
		{"bozuk JSON", `{"instances":`, ""},
		{"yanlış tip", `{"enabled":"yes"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseInput([]byte(tc.body))
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Path != tc.path {
				t.Fatalf("FieldError{%q} beklenirdi, %#v", tc.path, err)
			}
		})
	}
	s, err := ParseInput([]byte(`{"enabled":true,"hubs":[{"clusterId":"c-1"}],"instances":[{"id":"a","hubClusterId":"c-1","hubNamespace":"a","tokenRef":"env:A"}],"unknownFutureField":1}`))
	if err != nil || !s.Enabled || s.Instances[0].TokenRef != "env:A" || s.Hubs[0].ClusterID != "c-1" || s.Instances[0].HubClusterID != "c-1" {
		t.Fatalf("geçerli gövde: %v %+v", err, s)
	}
}

func TestNormalizeAPIURL(t *testing.T) {
	ok := []struct{ in, want string }{
		{"https://argocd.example.invalid", "https://argocd.example.invalid"},
		{" HTTPS://ArgoCD.Example.Invalid/ ", "https://argocd.example.invalid"},
		{"https://argocd.example.invalid:8443/", "https://argocd.example.invalid:8443"},
		// Argo CD --rootpath korunur; yol büyük/küçük harfi korunur (§7.1).
		{"https://apps.example.invalid/ArgoCD/", "https://apps.example.invalid/ArgoCD"},
		{"http://argocd-server.team-a.svc", "http://argocd-server.team-a.svc"},
		// Varsayılan port EKLENMEZ: Argo route'u 443'tür, K8s API'nin :6443
		// kuralı (lane R2) burada yanlış olurdu.
		{"https://argocd.example.invalid:443", "https://argocd.example.invalid:443"},
	}
	for _, c := range ok {
		got, err := NormalizeAPIURL(c.in)
		if err != nil || got != c.want {
			t.Errorf("NormalizeAPIURL(%q) = %q, %v; beklenen %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "argocd.example.invalid", "ftp://argocd.example.invalid", "https://", "https://u:p@argocd.example.invalid",
		"https://argocd.example.invalid/?refresh=hard", "https://argocd.example.invalid/#x", "https://argo cd.example.invalid"} {
		if got, err := NormalizeAPIURL(bad); err == nil {
			t.Errorf("NormalizeAPIURL(%q) = %q, hata beklenirdi", bad, got)
		}
	}
}

func TestBoundsCoverEveryIntField(t *testing.T) {
	b := Bounds()
	for _, p := range []string{"apiWorker.rps", "apiWorker.burst", "apiWorker.maxConcurrent", "classification.windowMin",
		"classification.outOfBandLookbackMin", "reader.maxSeries", "reader.maxBodyMiB", "reader.timeoutS", "intervals.metricsS",
		"intervals.inventoryMin", "intervals.mapperMin", "intervals.classifierReevalH", "mapping.nameConfidence", "mapping.namespaceConfidence"} {
		bd, ok := b[p]
		if !ok {
			t.Errorf("bounds %q yok", p)
			continue
		}
		if !(bd.Min <= bd.Default && bd.Default <= bd.Max) {
			t.Errorf("%s: min ≤ varsayılan ≤ max bozuk: %+v", p, bd)
		}
	}
	if r := b["reader.maxSeries"]; r.Max != 50000 || r.Default != 50000 {
		t.Errorf("karar 2 seri tavanı: %+v", r)
	}
	if r := b["reader.maxBodyMiB"]; r.Max != 64 || r.Default != 64 {
		t.Errorf("karar 2 gövde tavanı: %+v", r)
	}
	if r := b["reader.timeoutS"]; r.Max != 45 || r.Default != 30 {
		t.Errorf("karar 2 timeout: %+v", r)
	}
}

// ── Kalıcılık + token çözümü ───────────────────────────────────────────────

type fakeStore struct {
	raw    []byte
	getErr error
	putErr error
	puts   int
	key    string
}

func (f *fakeStore) GetSetting(_ context.Context, key string) ([]byte, error) {
	f.key = key
	return f.raw, f.getErr
}
func (f *fakeStore) PutSetting(_ context.Context, key string, raw []byte) error {
	f.key = key
	f.puts++
	if f.putErr != nil {
		return f.putErr
	}
	f.raw = raw
	return nil
}

func TestPersistence(t *testing.T) {
	ctx := context.Background()
	st := &fakeStore{}
	s := NewSettingsService()
	if err := s.LoadPersisted(ctx, st); err != nil || s.Current().Enabled || st.key != SettingsKey {
		t.Fatalf("boş blob varsayılanı korur, anahtar %q: %v %+v", st.key, err, s.Current())
	}
	st.raw = []byte("{bozuk")
	if err := s.LoadPersisted(ctx, st); err == nil {
		t.Fatal("bozuk JSON hata döner")
	}
	if s.Resolved().Reader.MaxSeries != 50000 {
		t.Fatal("bozuk JSON canlı ayarı ezmez")
	}
	st.getErr = errors.New("ch down")
	if err := s.LoadPersisted(ctx, st); err == nil {
		t.Fatal("store hatası döner")
	}
	st.getErr = nil
	cfg := validBlob()
	cfg.Reader.TimeoutS = 40
	if err := s.SavePersisted(ctx, st, cfg); err != nil || st.puts != 1 || s.Current().UpdatedAt == 0 || !s.Current().Enabled {
		t.Fatalf("kaydet: %v %+v", err, s.Current())
	}
	other := NewSettingsService()
	if err := other.LoadPersisted(ctx, st); err != nil || other.Resolved().Reader.TimeoutS != 40 || len(other.Current().Instances) != 2 {
		t.Fatalf("öteki pod blobu almalı: %v %+v", err, other.Current())
	}
	// bayat blob canlı ayarı ezmez (eski pod / yarış)
	s.Configure(Settings{Enabled: false, UpdatedAt: s.Current().UpdatedAt + 1})
	if err := s.LoadPersisted(ctx, st); err != nil || s.Current().Enabled {
		t.Fatalf("bayat blob ezmemeli: %v %+v", err, s.Current())
	}
	st.putErr = errors.New("ro")
	if err := s.SavePersisted(ctx, st, cfg); err == nil || s.Current().Enabled {
		t.Fatal("put hatasında canlı ayar değişmez")
	}
	if (*SettingsService)(nil).LoadPersisted(ctx, st) != nil || s.LoadPersisted(ctx, nil) != nil {
		t.Fatal("nil güvenli")
	}
	if err := s.SavePersisted(ctx, nil, cfg); err == nil {
		t.Fatal("store yokken kaydetme hata döner (panik değil)")
	}
}

// Eski/elle yazılmış blob (yalnız birkaç alan) varsayılanlarla yüklenir.
func TestOldBlobLoadsWithDefaults(t *testing.T) {
	st := &fakeStore{raw: []byte(`{"enabled":false,"hubs":[{"clusterId":"c-aaaa0001"}]}`)}
	s := NewSettingsService()
	if err := s.LoadPersisted(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	r := s.Resolved()
	d := DefaultSettings()
	if len(r.Hubs) != 1 || r.Hubs[0].ClusterID != "c-aaaa0001" || r.Reader != d.Reader || r.Intervals != d.Intervals || r.APIWorker != d.APIWorker ||
		r.Hubs[0].InjectClusterLabel == nil || !*r.Hubs[0].InjectClusterLabel {
		t.Fatalf("%+v", r)
	}
}

// tokenRef Configure'da çözülür (thanos deseni); çözülemeyen ref FAIL-CLOSED
// (§7.2): Token hata döner, durum rozete yansır, çözülen değer hiçbir JSON'a
// girmez.
func TestTokenRefResolution(t *testing.T) {
	env := map[string]string{"COREMETRY_ARGOCD_TEAM_A": "  s3cr3t-value \n"}
	files := map[string]string{"/var/run/secrets/argocd/team-b": "file-s3cr3t\n"}
	s := newSettingsServiceWith(func(k string) string { return env[k] }, func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("open " + p + ": no such file or directory")
	})
	cfg := validBlob()
	cfg.Instances = append(cfg.Instances,
		Instance{ID: "team-b-file", HubNamespace: "team-b-file", TokenRef: "file:/var/run/secrets/argocd/team-b"},
		Instance{ID: "team-c-missing", HubNamespace: "team-c", TokenRef: "env:COREMETRY_ARGOCD_MISSING"},
	)
	s.Configure(cfg)
	if tok, err := s.Token("team-a-prod"); err != nil || tok != "s3cr3t-value" {
		t.Fatalf("env ref: %q %v", tok, err)
	}
	if tok, err := s.Token("team-b-file"); err != nil || tok != "file-s3cr3t" {
		t.Fatalf("file ref: %q %v", tok, err)
	}
	if tok, err := s.Token("team-c-missing"); err == nil || tok != "" {
		t.Fatalf("çözülemeyen ref fail-closed: %q %v", tok, err)
	}
	if _, err := s.Token("team-b-uat"); !errors.Is(err, ErrNoTokenRef) {
		t.Fatalf("ref'siz instance: %v", err)
	}
	if _, err := s.Token("nope"); err == nil {
		t.Fatal("bilinmeyen instance hata")
	}
	if ok, msg := s.TokenStatus("team-a-prod"); !ok || msg != "" {
		t.Fatalf("durum: %v %q", ok, msg)
	}
	if ok, msg := s.TokenStatus("team-c-missing"); ok || msg == "" {
		t.Fatalf("durum hata taşımalı: %v %q", ok, msg)
	}
	raw, _ := json.Marshal(s.Current())
	raw2, _ := json.Marshal(s.Resolved())
	for _, b := range [][]byte{raw, raw2} {
		if strings.Contains(string(b), "s3cr3t") {
			t.Fatalf("çözülen token JSON'a sızdı: %s", b)
		}
	}
}

// v0.10.957 — inceleme: LoadPersisted eskiden Current()'ı okuyup kilidi
// bırakıyor, bayat blobun tokenRef'lerini (dosya IO) çözüyor, sonra
// KOŞULSUZ swap ediyordu. Arada bu pod'da biten bir admin PUT'u
// (SavePersisted) bayat blobla ezilirdi (≤30 s, sonraki yenilemeye dek).
// Deterministik: bayat blobun dosya ref'i çözülürken PUT yapılır.
func TestLoadPersistedDoesNotClobberConcurrentSave(t *testing.T) {
	ctx := context.Background()
	var s *SettingsService
	putDone := false
	s = newSettingsServiceWith(func(string) string { return "" }, func(p string) ([]byte, error) {
		if p == "/run/secrets/argocd/stale" && !putDone {
			putDone = true
			newer := Settings{EnvList: []string{"newer"}}
			if err := s.SavePersisted(ctx, &fakeStore{}, newer); err != nil {
				t.Errorf("PUT: %v", err)
			}
		}
		return []byte("tok"), nil
	})
	stale, _ := json.Marshal(Settings{EnvList: []string{"stale"}, UpdatedAt: 1,
		Instances: []Instance{{ID: "a", HubNamespace: "a", TokenRef: "file:/run/secrets/argocd/stale"}}})
	if err := s.LoadPersisted(ctx, &fakeStore{raw: stale}); err != nil {
		t.Fatal(err)
	}
	if !putDone {
		t.Fatal("test kurgusu: PUT çözüm sırasında koşmadı")
	}
	if got := s.Current().EnvList; len(got) != 1 || got[0] != "newer" {
		t.Fatalf("eşzamanlı PUT bayat yüklemeyle ezildi: envList=%v", got)
	}
	// Eşit/yeni blob hâlâ alınır ve canlı ayarın token'ları tazelenir.
	cur := s.Current()
	cur.Instances = []Instance{{ID: "b", HubNamespace: "b", TokenRef: "file:/run/secrets/argocd/b"}}
	same, _ := json.Marshal(cur)
	if err := s.LoadPersisted(ctx, &fakeStore{raw: same}); err != nil {
		t.Fatal(err)
	}
	if tok, err := s.Token("b"); err != nil || tok != "tok" {
		t.Fatalf("eşit damgalı blob alınmalı: %q %v", tok, err)
	}
}
