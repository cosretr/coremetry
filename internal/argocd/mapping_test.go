package argocd

// mapping_test.go — v0.10.985 — Rollouts v2 P3.2 eşleyici çekirdeği
// (mapping.go; docs/rollouts/v2-audit.md §6, §10.3.5, §10.6).
// Sözleşme:
//   - MappingRow migrations/0015'teki argocd_app_mapping kolonlarını SIRASIYLA
//     adıyla (`ch`) ve tipiyle aynalar; yazıcı sözleşmesi (TTL çapası, yöntem ↔
//     sınıf, zayıf kenar iş yükü taşımaz) INSERT'ten önce reddeder.
//   - Ad ayrıştırma: envList + argoSuffix, sona çapalı, tireli component;
//     liste boşsa ayrıştırıcı yok.
//   - BuildMapping: pin ad tahminini yener; ad kenarı dest_namespace ister;
//     dest_server çözülürse yalnız o küme, çözülmezse her küme; küme-içi adres
//     (geniş kural) instance'ın KENDİ hub'ına; iki hub = iki kenar; zayıf kenar
//     + candidates; hazır olmayan instance üretmez.
//   - ReconcileMapping: yeni / değişmeyen / dokunulan (first_matched_at
//     taşınır) / değişen / kaldırılan (yalnız hazır instance).
//   - ServiceAppsFromEdges: canlı matcher'la (MatchServiceApps) AYNI sonuç.

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func mpDDLColumns(t *testing.T, table string) [][2]string {
	t.Helper()
	b, err := os.ReadFile("../../migrations/0015_rollouts_v2.sql")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "CREATE TABLE IF NOT EXISTS "+table+" ")
	if i < 0 {
		t.Fatalf("0015'te %s yok", table)
	}
	body := src[i:]
	body = body[strings.Index(body, "(\n")+2:]
	body = body[:strings.Index(body, ") ENGINE")]
	var cols [][2]string
	for _, ln := range strings.Split(body, "\n") {
		if c := strings.Index(ln, "--"); c >= 0 {
			ln = ln[:c]
		}
		ln = strings.TrimSuffix(strings.TrimSpace(ln), ",")
		if ln == "" {
			continue
		}
		f := strings.Fields(ln)
		cols = append(cols, [2]string{f[0], f[1]})
	}
	return cols
}

func TestMappingRowMirrorsDDL(t *testing.T) {
	ddl := mpDDLColumns(t, "argocd_app_mapping")
	rt := reflect.TypeOf(MappingRow{})
	if rt.NumField() != len(ddl) {
		t.Fatalf("DDL %d kolon, Go %d alan: %v", len(ddl), rt.NumField(), ddl)
	}
	goType := map[reflect.Type]string{
		reflect.TypeOf(""): "string", reflect.TypeOf(uint8(0)): "UInt8", reflect.TypeOf(uint16(0)): "UInt16",
		reflect.TypeOf(uint64(0)): "UInt64", reflect.TypeOf(time.Time{}): "DateTime64(3)",
	}
	for i, c := range ddl {
		f := rt.Field(i)
		want := c[1]
		if want == "String" || want == "LowCardinality(String)" {
			want = "string"
		}
		if f.Tag.Get("ch") != c[0] || goType[f.Type] != want {
			t.Fatalf("kolon %d: DDL %v, Go %s %s", i, c, f.Tag.Get("ch"), f.Type)
		}
	}
	if got := strings.Join(MappingColumns(), ", "); !strings.HasPrefix(got, "cluster_id, namespace, workload_kind, workload, instance_id") || !strings.HasSuffix(got, "removed_at, version") {
		t.Fatalf("MappingColumns sırası: %s", got)
	}
}

var mpT0 = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func mpValidRow() MappingRow {
	return MappingRow{ClusterID: "c-a", Namespace: "pay", WorkloadKind: "Deployment", Workload: "checkout", InstanceID: "i1",
		AppNamespace: "apps", AppName: "p-pay-checkout-prod-a", MatchMethod: MatchName, MatchClass: ClassEstimated,
		Confidence: 70, Candidates: 1, FirstMatchedAt: mpT0, LastVerifiedAt: mpT0, Version: 1}
}

func TestValidateMappingRow(t *testing.T) {
	if err := ValidateMappingRow(mpValidRow()); err != nil {
		t.Fatal(err)
	}
	weak := mpValidRow()
	weak.WorkloadKind, weak.Workload, weak.MatchMethod, weak.MatchClass, weak.Confidence, weak.Candidates = "", "", MatchNamespace, ClassWeak, 30, 4
	if err := ValidateMappingRow(weak); err != nil {
		t.Fatalf("zayıf kenar geçerli: %v", err)
	}
	for name, mut := range map[string]func(*MappingRow){
		"last_verified_at sıfır": func(r *MappingRow) { r.LastVerifiedAt = time.Time{} },
		"first_matched_at epoch": func(r *MappingRow) { r.FirstMatchedAt = time.Unix(0, 0) },
		"removed_at epoch":       func(r *MappingRow) { r.RemovedAt = time.Unix(0, 0) },
		"cluster boş":            func(r *MappingRow) { r.ClusterID = " " },
		"instance boş":           func(r *MappingRow) { r.InstanceID = "" },
		"yöntem sözlük dışı":     func(r *MappingRow) { r.MatchMethod, r.MatchClass = "guess", ClassEstimated },
		"sınıf yönteme uymuyor":  func(r *MappingRow) { r.MatchClass = ClassExact },
		"zayıf kenarda iş yükü":  func(r *MappingRow) { r.MatchMethod, r.MatchClass = MatchNamespace, ClassWeak },
		"iş yükü kenarı türsüz":  func(r *MappingRow) { r.WorkloadKind = "" },
		"güven 0":                func(r *MappingRow) { r.Confidence = 0 },
		"güven 101":              func(r *MappingRow) { r.Confidence = 101 },
		"candidates 0":           func(r *MappingRow) { r.Candidates = 0 },
		"version sıfır":          func(r *MappingRow) { r.Version = 0 },
		"manual iş yükü adı boş": func(r *MappingRow) { r.MatchMethod, r.MatchClass, r.Workload = MatchManual, ClassExact, "" },
	} {
		r := mpValidRow()
		mut(&r)
		if ValidateMappingRow(r) == nil {
			t.Errorf("%s: reddedilmeliydi", name)
		}
	}
	rm := mpValidRow()
	rm.RemovedAt = mpT0.Add(time.Hour)
	if err := ValidateMappingRow(rm); err != nil {
		t.Fatalf("kaldırılmış kenar geçerli: %v", err)
	}
}

func TestMatchClassOf(t *testing.T) {
	for m, c := range map[string]string{MatchManual: ClassExact, MatchResource: ClassExact, MatchName: ClassEstimated,
		MatchPodLabel: ClassEstimated, MatchNamespace: ClassWeak, "x": ""} {
		if got := MatchClassOf(m); got != c {
			t.Errorf("%s → %q, beklenen %q", m, got, c)
		}
	}
}

func TestNameParser(t *testing.T) {
	if NewNameParser(nil, []string{"a"}) != nil || NewNameParser([]string{"prod"}, nil) != nil || NewNameParser([]string{" "}, []string{"a"}) != nil {
		t.Fatal("env ya da suffix listesi boşsa ayrıştırıcı yok")
	}
	var np *NameParser
	if _, ok := np.Parse("a-b-c-prod-x"); ok {
		t.Fatal("nil ayrıştırıcı ayrıştırmaz")
	}
	np = NewNameParser([]string{"prod", "INT", "prod", "uat"}, []string{"ca", "cb", "e1.x", "CA"})
	cases := []struct {
		name string
		want ParsedName
		ok   bool
	}{
		{"p-pay-checkout-prod-ca", ParsedName{"p", "pay", "checkout", "prod", "ca"}, true},
		{"p-pay-checkout-api-prod-ca", ParsedName{"p", "pay", "checkout-api", "prod", "ca"}, true}, // tireli component
		{"p-pay-x-prod-prod-cb", ParsedName{"p", "pay", "x-prod", "prod", "cb"}, true},             // env adı component'te de geçer
		{"P-Pay-Ledger-INT-CA", ParsedName{"p", "pay", "ledger", "int", "ca"}, true},               // küçük harfe indirilir
		{"p-pay-ledger-uat-e1.x", ParsedName{"p", "pay", "ledger", "uat", "e1.x"}, true},           // QuoteMeta: '.' sabit
		{"p-pay-ledger-uat-e1yx", ParsedName{}, false},                                             // '.' joker değil
		{"p-pay-ledger-dev-ca", ParsedName{}, false},                                               // bilinmeyen env
		{"p-pay-ledger-prod-cz", ParsedName{}, false},                                              // bilinmeyen suffix
		{"pay-ledger-prod-ca", ParsedName{}, false},                                                // dört parça
		{"p-pay-ledger-prod-ca-extra", ParsedName{}, false},                                        // sona çapalı
	}
	for _, c := range cases {
		got, ok := np.Parse(c.name)
		if ok != c.ok || got != c.want {
			t.Errorf("Parse(%q) = %+v %v, beklenen %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
	if p, _ := np.Parse("p-pay-checkout-api-prod-ca"); p.BaseKey() != "p-pay-checkout-api-prod" {
		t.Errorf("base key %q", p.BaseKey())
	}
}

// ── BuildMapping ──────────────────────────────────────────────────────────

func mpSettings() Settings {
	s := DefaultSettings()
	s.Enabled, s.MetricsWorker.Enabled = true, true
	s.Hubs = []Hub{{ClusterID: "hub-a"}, {ClusterID: "hub-b"}}
	s.EnvList = []string{"prod"}
	s.Instances = []Instance{
		{ID: "i1", HubClusterID: "hub-a", HubNamespace: "t-prod", Name: "Prod Argo A", Enabled: true},
		{ID: "i2", HubClusterID: "hub-b", HubNamespace: "t-prod", Name: "Prod Argo B", Enabled: true},
	}
	return s.Normalized()
}

func mpEdges(res MapperResult) []string {
	var out []string
	for _, e := range res.Edges {
		k := e.Key
		out = append(out, k.ClusterID+"/"+k.Namespace+"/"+k.WorkloadKind+"/"+k.Workload+" ← "+k.InstanceID+"/"+k.AppName+" "+e.Method)
	}
	return out
}

func mpHas(t *testing.T, res MapperResult, want ...string) {
	t.Helper()
	got := map[string]bool{}
	for _, e := range mpEdges(res) {
		got[e] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("kenar yok: %s\nvar olanlar:\n  %s", w, strings.Join(mpEdges(res), "\n  "))
		}
	}
}

func mpHasNot(t *testing.T, res MapperResult, bad ...string) {
	t.Helper()
	for _, e := range mpEdges(res) {
		for _, b := range bad {
			if e == b {
				t.Errorf("olmaması gereken kenar: %s", b)
			}
		}
	}
}

var mpByServer = map[string]string{"https://api.a:6443": "c-a", "https://api.b:6443": "c-b"}

func mpNorm(s string) string { return strings.TrimSuffix(strings.ToLower(s), "/") }

func TestBuildMappingPinBeatsName(t *testing.T) {
	set := mpSettings()
	set.Pins = []Pin{{ClusterID: "c-a", Namespace: "pay", WorkloadKind: "Deployment", Workload: "ledger", InstanceID: "i1", AppNamespace: "apps", AppName: "custom-ledger"}}
	res := BuildMapping(MapperInput{
		Apps: []MapperApp{
			{InstanceID: "i1", HubClusterID: "hub-a", AppNamespace: "apps", Name: "custom-ledger", DestServer: "https://api.a:6443", DestNamespace: "elsewhere"},
			{InstanceID: "i1", HubClusterID: "hub-a", AppNamespace: "apps", Name: "p-pay-ledger-prod-ca", DestServer: "https://api.a:6443", DestNamespace: "pay"},
		},
		Workloads: []MapperWorkload{{ClusterID: "c-a", Namespace: "pay", Kind: "StatefulSet", Workload: "ledger"}},
		Settings:  set, Ready: map[string]bool{"i1": true, "i2": true}, ByServer: mpByServer, Normalize: mpNorm,
	})
	// Pin kenarı pinin türünü taşır (insan beyanı), iş yükü gözlenmese de yazılır.
	mpHas(t, res, "c-a/pay/Deployment/ledger ← i1/custom-ledger manual")
	// Pinli iş yükü ad tahmini ALMAZ.
	mpHasNot(t, res, "c-a/pay/StatefulSet/ledger ← i1/p-pay-ledger-prod-ca name")
	for _, e := range res.Edges {
		if e.Method == MatchManual && e.Confidence != 100 {
			t.Errorf("manual güven 100: %+v", e)
		}
	}
	if res.UnmappedApps != 1 {
		t.Errorf("iş yükü kenarı olmayan: p-pay-ledger = 1; got %d", res.UnmappedApps)
	}
}

func TestBuildMappingNameRules(t *testing.T) {
	set := mpSettings()
	wl := []MapperWorkload{
		{ClusterID: "c-a", Namespace: "pay", Kind: "Deployment", Workload: "checkout"},
		{ClusterID: "c-b", Namespace: "pay", Kind: "Deployment", Workload: "checkout"},
		{ClusterID: "hub-a", Namespace: "pay", Kind: "Deployment", Workload: "checkout"},
		{ClusterID: "hub-b", Namespace: "pay", Kind: "Deployment", Workload: "checkout"},
		{ClusterID: "c-a", Namespace: "pay", Kind: "", Workload: "billing"}, // türsüz: atlanır
	}
	apps := []MapperApp{
		// dest_server c-a'ya çözülür → yalnız c-a
		{InstanceID: "i1", HubClusterID: "hub-a", AppNamespace: "apps", Name: "p-pay-checkout-prod-ca", DestServer: "https://API.A:6443/", DestNamespace: "pay"},
		// çözülemez → her kümedeki checkout (tahmini)
		{InstanceID: "i1", HubClusterID: "hub-a", AppNamespace: "apps", Name: "p-pay-checkout-prod", DestServer: "https://unknown", DestNamespace: "pay"},
		// ad eşleşir ama dest_namespace başka → kenar yok
		{InstanceID: "i1", HubClusterID: "hub-a", AppNamespace: "apps", Name: "p-pay-checkout-prod-cb", DestServer: "https://api.b:6443", DestNamespace: "pay-canary"},
		// küme-içi GENİŞ kural (:443): instance'ın kendi hub'ı
		{InstanceID: "i1", HubClusterID: "hub-a", AppNamespace: "apps", Name: "x-checkout-a", DestServer: "https://kubernetes.default.svc:443", DestNamespace: "pay"},
		{InstanceID: "i2", HubClusterID: "hub-b", AppNamespace: "apps", Name: "x-checkout-b", DestServer: "https://kubernetes.default.svc", DestNamespace: "pay"},
		// hazır olmayan instance'ın uygulaması görülmez
		{InstanceID: "i9", HubClusterID: "hub-a", AppNamespace: "apps", Name: "p-pay-checkout-prod-zz", DestNamespace: "pay"},
	}
	res := BuildMapping(MapperInput{Apps: apps, Workloads: wl, Settings: set, Ready: map[string]bool{"i1": true, "i2": true},
		ByServer: mpByServer, Normalize: mpNorm, SuffixByCluster: map[string]string{"c-a": "ca", "c-b": "cb"}})
	mpHas(t, res,
		"c-a/pay/Deployment/checkout ← i1/p-pay-checkout-prod-ca name",
		"c-a/pay/Deployment/checkout ← i1/p-pay-checkout-prod name",
		"c-b/pay/Deployment/checkout ← i1/p-pay-checkout-prod name",
		"hub-a/pay/Deployment/checkout ← i1/p-pay-checkout-prod name",
		"hub-a/pay/Deployment/checkout ← i1/x-checkout-a name",
		"hub-b/pay/Deployment/checkout ← i2/x-checkout-b name",
		// zayıf kenar yalnız dest_server çözülene
		"c-a/pay// ← i1/p-pay-checkout-prod-ca namespace",
		"c-b/pay-canary// ← i1/p-pay-checkout-prod-cb namespace",
		"hub-a/pay// ← i1/x-checkout-a namespace",
	)
	mpHasNot(t, res,
		"c-b/pay/Deployment/checkout ← i1/p-pay-checkout-prod-ca name",
		"c-b/pay/Deployment/checkout ← i1/p-pay-checkout-prod-cb name",
		"c-a/pay/Deployment/checkout ← i1/x-checkout-a name",
		"hub-b/pay/Deployment/checkout ← i1/x-checkout-a name",
		"c-a/pay/Deployment/checkout ← i9/p-pay-checkout-prod-zz name",
	)
	for _, e := range res.Edges {
		if e.Key.Workload == "billing" {
			t.Errorf("türsüz iş yükü kenar almamalı: %+v", e)
		}
		if e.Method == MatchName && e.Confidence != set.Mapping.NameConfidence {
			t.Errorf("ad güveni ayardan: %+v", e)
		}
		if e.Method == MatchNamespace && e.Confidence != set.Mapping.NamespaceConfidence {
			t.Errorf("zayıf güven ayardan: %+v", e)
		}
	}
	if res.Diag["workload_kind_empty"] != 1 || res.Diag["dest_unresolved"] != 1 || res.UnmappedApps != 1 {
		t.Errorf("teşhis: %v unmapped=%d", res.Diag, res.UnmappedApps)
	}
	// §6 adım 4: p-pay-checkout-prod-cb c-b'ye çözülür ve suffix'i cb — uyumlu;
	// ad parçalı kenar sayacı: ayrışan ad component'e TAM eşleşir.
	if res.Diag["suffix_mismatch"] != 0 || res.Diag["name_component_exact"] != 1 || res.Diag["name_parsed"] != 2 {
		t.Errorf("ayrıştırma teşhisi: %v", res.Diag)
	}
}

func TestBuildMappingSuffixMismatchAndCandidates(t *testing.T) {
	set := mpSettings()
	res := BuildMapping(MapperInput{
		Apps: []MapperApp{
			// ad "cb" diyor ama dest_server c-a — işaretlenir, kenar DEĞİŞMEZ
			{InstanceID: "i1", HubClusterID: "hub-a", AppNamespace: "apps", Name: "p-pay-checkout-prod-cb", DestServer: "https://api.a:6443", DestNamespace: "pay"},
			{InstanceID: "i1", HubClusterID: "hub-a", AppNamespace: "apps", Name: "p-pay-billing-prod-ca", DestServer: "https://api.a:6443", DestNamespace: "pay"},
			{InstanceID: "i2", HubClusterID: "hub-b", AppNamespace: "apps", Name: "p-pay-ledger-prod-ca", DestServer: "https://api.a:6443", DestNamespace: "pay"},
		},
		Workloads: []MapperWorkload{{ClusterID: "c-a", Namespace: "pay", Kind: "Deployment", Workload: "checkout"}},
		Settings:  set, Ready: map[string]bool{"i1": true, "i2": true}, ByServer: mpByServer, Normalize: mpNorm,
		SuffixByCluster: map[string]string{"c-a": "ca", "c-b": "cb"},
	})
	mpHas(t, res, "c-a/pay/Deployment/checkout ← i1/p-pay-checkout-prod-cb name")
	if res.Diag["suffix_mismatch"] != 1 {
		t.Errorf("suffix tutarsızlığı sayılmalı: %v", res.Diag)
	}
	n := 0
	for _, e := range res.Edges {
		if e.Method == MatchNamespace {
			n++
			if e.Candidates != 3 {
				t.Errorf("c-a/pay altında 3 uygulama (iki hub dahil): %+v", e)
			}
		}
	}
	if n != 3 || res.UnmappedApps != 2 {
		t.Errorf("3 zayıf kenar, 2 eşlenmemiş: %d %d", n, res.UnmappedApps)
	}
}

// Two hubs (§5.6): aynı ad iki instance'ta iki ayrı uygulamadır → iki kenar;
// pin instance'ıyla bağlı — diğer hub'daki aynı adlı uygulamaya uymaz.
func TestBuildMappingTwoHubs(t *testing.T) {
	set := mpSettings()
	set.Pins = []Pin{{ClusterID: "c-a", Namespace: "pay", WorkloadKind: "Deployment", Workload: "api", InstanceID: "i2", AppNamespace: "apps", AppName: "pay-api"}}
	apps := []MapperApp{
		{InstanceID: "i1", HubClusterID: "hub-a", AppNamespace: "apps", Name: "pay-api", DestServer: "https://api.a:6443", DestNamespace: "pay"},
		{InstanceID: "i2", HubClusterID: "hub-b", AppNamespace: "apps", Name: "pay-api", DestServer: "https://api.a:6443", DestNamespace: "pay"},
		{InstanceID: "i1", HubClusterID: "hub-a", AppNamespace: "apps", Name: "pay-web", DestServer: "https://api.a:6443", DestNamespace: "pay"},
		{InstanceID: "i2", HubClusterID: "hub-b", AppNamespace: "apps", Name: "pay-web", DestServer: "https://api.a:6443", DestNamespace: "pay"},
	}
	wl := []MapperWorkload{
		{ClusterID: "c-a", Namespace: "pay", Kind: "Deployment", Workload: "api"},
		{ClusterID: "c-a", Namespace: "pay", Kind: "Deployment", Workload: "web"},
	}
	res := BuildMapping(MapperInput{Apps: apps, Workloads: wl, Settings: set, Ready: map[string]bool{"i1": true, "i2": true}, ByServer: mpByServer, Normalize: mpNorm})
	mpHas(t, res,
		"c-a/pay/Deployment/api ← i2/pay-api manual",
		"c-a/pay/Deployment/web ← i1/pay-web name",
		"c-a/pay/Deployment/web ← i2/pay-web name",
	)
	// i1'in pay-api'si: pin i2'nin; iş yükü pinli → ad tahmini de yok.
	mpHasNot(t, res, "c-a/pay/Deployment/api ← i1/pay-api manual", "c-a/pay/Deployment/api ← i1/pay-api name")
	// Hazır olmayan hub: pini sayılır, üretilmez.
	res = BuildMapping(MapperInput{Apps: apps, Workloads: wl, Settings: set, Ready: map[string]bool{"i1": true}, ByServer: mpByServer, Normalize: mpNorm})
	mpHasNot(t, res, "c-a/pay/Deployment/api ← i2/pay-api manual")
	if res.Diag["pin_instance_not_ready"] != 1 {
		t.Errorf("hazır olmayan instance'ın pini sayılmalı: %v", res.Diag)
	}
	// Pinin uygulaması hazır instance'ta yok → pin_app_missing.
	set.Pins[0].AppName = "gone"
	res = BuildMapping(MapperInput{Apps: apps, Workloads: wl, Settings: set, Ready: map[string]bool{"i1": true, "i2": true}, ByServer: mpByServer, Normalize: mpNorm})
	if res.Diag["pin_app_missing"] != 1 {
		t.Errorf("uygulaması olmayan pin sayılmalı: %v", res.Diag)
	}
}

// ── ReconcileMapping ──────────────────────────────────────────────────────

func TestReconcileMapping(t *testing.T) {
	now := mpT0.Add(48 * time.Hour)
	key := func(w, inst, app string) EdgeKey {
		return EdgeKey{"c-a", "pay", "Deployment", w, inst, "apps", app}
	}
	row := func(k EdgeKey, conf uint8, verified time.Time) MappingRow {
		return MappingRow{ClusterID: k.ClusterID, Namespace: k.Namespace, WorkloadKind: k.WorkloadKind, Workload: k.Workload,
			InstanceID: k.InstanceID, AppNamespace: k.AppNamespace, AppName: k.AppName, MatchMethod: MatchName, MatchClass: ClassEstimated,
			Confidence: conf, Candidates: 1, FirstMatchedAt: mpT0, LastVerifiedAt: verified, Version: 5}
	}
	kUnch, kTouch, kChg, kNew, kGone, kOther := key("a", "i1", "a1"), key("b", "i1", "b1"), key("c", "i1", "c1"), key("d", "i1", "d1"), key("e", "i1", "e1"), key("f", "i2", "f1")
	existing := []MappingRow{
		row(kUnch, 70, now.Add(-time.Hour)),
		row(kTouch, 70, now.Add(-MappingTouchEvery)),
		row(kChg, 60, now.Add(-time.Hour)),
		row(kGone, 70, now.Add(-time.Hour)),
		row(kOther, 70, now.Add(-time.Hour)), // hazır olmayan instance: dokunulmaz
	}
	desired := []MapperEdge{
		{Key: kUnch, Method: MatchName, Confidence: 70, Candidates: 1},
		{Key: kTouch, Method: MatchName, Confidence: 70, Candidates: 1},
		{Key: kChg, Method: MatchName, Confidence: 70, Candidates: 1},
		{Key: kNew, Method: MatchName, Confidence: 70, Candidates: 1},
	}
	var ver uint64 = 100
	rows, diag := ReconcileMapping(ReconcileInput{Desired: desired, Existing: existing, Ready: map[string]bool{"i1": true}, Now: now,
		NextVersion: func() uint64 { ver++; return ver }})
	by := map[EdgeKey]MappingRow{}
	for _, r := range rows {
		if err := ValidateMappingRow(r); err != nil {
			t.Fatalf("yazılan satır sözleşmeyi ihlal ediyor: %v (%+v)", err, r)
		}
		by[r.Key()] = r
	}
	if len(rows) != 4 || diag["edges_unchanged"] != 1 || diag["edges_touched"] != 1 || diag["edges_changed"] != 1 || diag["edges_new"] != 1 || diag["edges_removed"] != 1 {
		t.Fatalf("satırlar %d, teşhis %v", len(rows), diag)
	}
	if _, ok := by[kUnch]; ok {
		t.Error("değişmeyen taze kenar yazılmamalı")
	}
	if r := by[kTouch]; !r.FirstMatchedAt.Equal(mpT0) || !r.LastVerifiedAt.Equal(now) || !r.RemovedAt.IsZero() {
		t.Errorf("dokunulan kenar first_matched_at'i taşımalı: %+v", r)
	}
	if r := by[kChg]; r.Confidence != 70 || !r.FirstMatchedAt.Equal(mpT0) {
		t.Errorf("değişen kenar: %+v", r)
	}
	if r := by[kNew]; !r.FirstMatchedAt.Equal(now) || r.MatchClass != ClassEstimated {
		t.Errorf("yeni kenar: %+v", r)
	}
	if r := by[kGone]; !r.RemovedAt.Equal(now) || r.Confidence != 70 || !r.FirstMatchedAt.Equal(mpT0) || r.MatchMethod != MatchName {
		t.Errorf("kaldırılan kenar bütün alanları taşımalı: %+v", r)
	}
	if _, ok := by[kOther]; ok {
		t.Error("hazır olmayan instance'ın kenarı kaldırılmamalı")
	}
	if !sort.SliceIsSorted(rows, func(i, j int) bool { return edgeKeyLess(rows[i].Key(), rows[j].Key()) }) {
		t.Error("satırlar anahtar sırasında")
	}
}

// ── ServiceAppsFromEdges ──────────────────────────────────────────────────

func mpStatus(inst, app, ds, dns string) StatusRow {
	return StatusRow{InstanceID: inst, AppNamespace: "apps", AppName: app, ChangedAt: mpT0, ChangeKind: ChangeState,
		SyncStatus: "Synced", HealthStatus: "Healthy", AutoSync: "true", Project: "pay", DestServer: ds, DestNamespace: dns, Version: 1}
}

func TestServiceAppsFromEdges(t *testing.T) {
	set := mpSettings()
	wl := []ServiceWorkload{{ClusterID: "c-a", Namespace: "pay", Workload: "checkout"}, {ClusterID: "c-a", Namespace: "pay", Workload: "ledger"}}
	e := func(w, inst, app, method string, conf uint8) MappingRow {
		r := mpValidRow()
		r.Workload, r.InstanceID, r.AppName, r.MatchMethod, r.MatchClass, r.Confidence = w, inst, app, method, MatchClassOf(method), conf
		if method == MatchNamespace {
			r.Workload, r.WorkloadKind = "", ""
		}
		return r
	}
	removed := e("checkout", "i1", "old-app", MatchName, 70)
	removed.RemovedAt = mpT0
	edges := []MappingRow{
		e("checkout", "i1", "both-app", MatchName, 70),
		e("ledger", "i1", "both-app", MatchManual, 100), // manual varken servisteki ad kenarı düşer
		e("checkout", "i2", "p-pay-checkout-prod-ca", MatchName, 70),
		e("checkout", "i1", "no-status", MatchName, 70),
		e("checkout", "i7", "removed-instance", MatchName, 70), // ayarda yok
		e("other-wl", "i1", "not-this-service", MatchName, 70),
		removed,
		e("", "i1", "both-app", MatchNamespace, 30),
		e("", "i1", "weak-only", MatchNamespace, 30),
		e("", "i2", "weak-only-b", MatchNamespace, 30),
	}
	st := map[AppKey]StatusRow{
		{"i1", "apps", "both-app"}:               mpStatus("i1", "both-app", "https://api.a:6443", "pay"),
		{"i2", "apps", "p-pay-checkout-prod-ca"}: mpStatus("i2", "p-pay-checkout-prod-ca", "https://kubernetes.default.svc:443", "pay"),
	}
	res := ServiceAppsFromEdges(ServiceEdgesInput{Workloads: wl, Edges: edges, Statuses: st, Settings: set,
		Syncs: map[AppKey]map[string]int{{"i1", "apps", "both-app"}: {"Succeeded": 2}}, SyncsOK: true, ByServer: mpByServer, Normalize: mpNorm})
	if len(res.Apps) != 2 {
		t.Fatalf("2 uygulama beklenir: %+v", res.Apps)
	}
	a := res.Apps[0]
	if a.Name != "both-app" || a.Match != MatchManual || a.Confidence != 100 || len(a.Workloads) != 1 || a.Workloads[0].Workload != "ledger" ||
		a.InstanceName != "Prod Argo A" || a.HubClusterID != "hub-a" || a.InstanceNamespace != "t-prod" || a.DestCluster != "c-a" ||
		a.AutoSync == nil || !*a.AutoSync || a.Syncs24h["Succeeded"] != 2 {
		t.Errorf("manual önce, ad kenarı düşmüş: %+v", a)
	}
	b := res.Apps[1]
	if b.Match != MatchName || b.Confidence != 70 || b.DestCluster != "hub-b" || b.Syncs24h == nil || len(b.Syncs24h) != 0 {
		t.Errorf("ad kenarı, küme-içi → kendi hub'ı, boş senkron haritası: %+v", b)
	}
	if res.StatusMissing != 1 || res.OtherInNamespace != 2 {
		t.Errorf("durumu olmayan 1, eşleşmeyen zayıf 2: %d %d", res.StatusMissing, res.OtherInNamespace)
	}
	res = ServiceAppsFromEdges(ServiceEdgesInput{Workloads: wl, Edges: edges, Statuses: st, Settings: set})
	for _, a := range res.Apps {
		if a.Syncs24h != nil {
			t.Errorf("senkron okunmadıysa null: %+v", a)
		}
	}
	// silinmiş son durum gösterilmez
	del := mpStatus("i1", "both-app", "", "pay")
	del.ChangeKind = ChangeDeleted
	st[AppKey{"i1", "apps", "both-app"}] = del
	if res := ServiceAppsFromEdges(ServiceEdgesInput{Workloads: wl, Edges: edges, Statuses: st, Settings: set}); len(res.Apps) != 1 || res.StatusMissing != 2 {
		t.Errorf("silinmiş uygulama gösterilmez: %+v", res)
	}
	// v0.10.985 inceleme: devre dışı instance'ı işçi okumuyor — son durumu
	// donmuş; kenarı (taze olsa da) ne listeye ne sayıma girer.
	st[AppKey{"i1", "apps", "both-app"}] = mpStatus("i1", "both-app", "https://api.a:6443", "pay")
	off := mpSettings()
	for i := range off.Instances {
		if off.Instances[i].ID == "i2" {
			off.Instances[i].Enabled = false
		}
	}
	if res := ServiceAppsFromEdges(ServiceEdgesInput{Workloads: wl, Edges: edges, Statuses: st, Settings: off}); len(res.Apps) != 1 ||
		res.Apps[0].InstanceID != "i1" || res.OtherInNamespace != 1 {
		t.Errorf("devre dışı instance'ın kenarı atlanmalı: %+v", res)
	}
}

// v0.10.985 — sekmenin anlamı değişmesin: aynı uygulama/iş yükü/pin kümesinde
// canlı matcher ile eşleyici + kenar okuması aynı listeyi (eşleme, güven, iş
// yükleri, sıra) ve aynı "aynı namespace'te eşleşmeyen" sayısını verir. Fikstür
// bilinçli farkların dışında: küme-içi adres TAM yazım, eşleşmeyen uygulamaların
// dest_server'ı çözülür.
func TestMapperMatchesLiveMatcher(t *testing.T) {
	set := mpSettings()
	set.Pins = []Pin{
		{ClusterID: "c-a", Namespace: "pay", WorkloadKind: "Deployment", Workload: "ledger", InstanceID: "i1", AppNamespace: "apps", AppName: "custom-ledger-app"},
		{ClusterID: "c-z", Namespace: "x", WorkloadKind: "Deployment", Workload: "y", InstanceID: "i1", AppNamespace: "apps", AppName: "other"},
	}
	type app struct {
		inst, hub, name, ds, dns string
	}
	fixture := []app{
		{"i1", "hub-a", "p-pay-checkout-prod-a", "https://API.A:6443/", "pay"},
		{"i1", "hub-a", "p-pay-checkout-prod", "https://unknown", "pay"},
		{"i1", "hub-a", "p-pay-ledger-prod-a", "https://api.a:6443", "pay"},
		{"i1", "hub-a", "custom-ledger-app", "https://api.a:6443", "elsewhere"},
		{"i1", "hub-a", "p-pay-billing-prod-a", "https://api.a:6443", "pay"},
		{"i2", "hub-b", "p-pay-checkout-prod-b", "https://api.b:6443", "pay"},
		{"i2", "hub-b", "x-checkout-y", InClusterServer, "pay"},
		{"i2", "hub-b", "p-pay-web-prod-b", "https://api.b:6443", "pay"},
	}
	svc := []ServiceWorkload{
		{ClusterID: "c-a", Namespace: "pay", Workload: "checkout"},
		{ClusterID: "c-b", Namespace: "pay", Workload: "checkout"},
		{ClusterID: "c-a", Namespace: "pay", Workload: "ledger"},
		{ClusterID: "hub-b", Namespace: "pay", Workload: "checkout"},
	}
	var live []AppStatus
	var mapped []MapperApp
	st := map[AppKey]StatusRow{}
	for _, a := range fixture {
		live = append(live, AppStatus{HubClusterID: a.hub, InstanceNamespace: "t-prod", AppNamespace: "apps", Name: a.name,
			DestServer: a.ds, DestNamespace: a.dns, SyncStatus: "Synced", HealthStatus: "Healthy", Project: "pay"})
		mapped = append(mapped, MapperApp{InstanceID: a.inst, HubClusterID: a.hub, AppNamespace: "apps", Name: a.name, DestServer: a.ds, DestNamespace: a.dns})
		st[AppKey{a.inst, "apps", a.name}] = StatusRow{InstanceID: a.inst, AppNamespace: "apps", AppName: a.name, ChangedAt: mpT0,
			ChangeKind: ChangeState, SyncStatus: "Synced", HealthStatus: "Healthy", Project: "pay", DestServer: a.ds, DestNamespace: a.dns, Version: 1}
	}
	want := MatchServiceApps(ServiceMatchInput{Apps: live, Workloads: svc, Settings: set, ClusterByServer: mpByServer, NormalizeServer: mpNorm})

	// Filo iş yükleri: servisinkiler + başka servislerin (aynı namespace'te web).
	fleet := []MapperWorkload{{ClusterID: "c-b", Namespace: "pay", Kind: "Deployment", Workload: "web"}}
	for _, w := range svc {
		fleet = append(fleet, MapperWorkload{ClusterID: w.ClusterID, Namespace: w.Namespace, Kind: "Deployment", Workload: w.Workload})
	}
	ready := map[string]bool{"i1": true, "i2": true}
	built := BuildMapping(MapperInput{Apps: mapped, Workloads: fleet, Settings: set, Ready: ready, ByServer: mpByServer, Normalize: mpNorm})
	var ver uint64
	rows, _ := ReconcileMapping(ReconcileInput{Desired: built.Edges, Ready: ready, Now: mpT0, NextVersion: func() uint64 { ver++; return ver }})
	got := ServiceAppsFromEdges(ServiceEdgesInput{Workloads: svc, Edges: rows, Statuses: st, Settings: set, SyncsOK: true, ByServer: mpByServer, Normalize: mpNorm})

	type view struct {
		Hub, Name, Match string
		Conf             int
		Workloads        []ServiceWorkload
	}
	flat := func(apps []ServiceApp) []view {
		out := []view{}
		for _, a := range apps {
			out = append(out, view{a.HubClusterID, a.Name, a.Match, a.Confidence, a.Workloads})
		}
		return out
	}
	if !reflect.DeepEqual(flat(got.Apps), flat(want.Apps)) {
		t.Fatalf("eşleyici ≠ canlı:\n got %+v\nwant %+v", flat(got.Apps), flat(want.Apps))
	}
	if got.OtherInNamespace != want.OtherInNamespace {
		t.Fatalf("aynı namespace'te eşleşmeyen: eşleyici %d, canlı %d", got.OtherInNamespace, want.OtherInNamespace)
	}
	if len(want.Apps) < 4 || want.Apps[0].Match != MatchManual {
		t.Fatalf("fikstür anlamlı olmalı (manual + birkaç ad eşleşmesi): %+v", flat(want.Apps))
	}
}
