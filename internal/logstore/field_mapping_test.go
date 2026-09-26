package logstore

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// v0.10.944 — CoSRE çapraz-kaynak eşleme (field_mapping.go). Karar kuralı
// configured > discovered > none; prob yoksa/düştüyse "unverified" (yokluk
// iddia edilmez). Testler saf çözücüyü, CH ayna listelerini ve kayıttan
// rol okumayı pinler — ağ yok.

func TestResolveFieldRole(t *testing.T) {
	kw := traceFieldCap{Types: []string{"keyword"}, Searchable: true, Aggregatable: true}
	text := traceFieldCap{Types: []string{"text"}, Searchable: true}
	caps := map[string]traceFieldCap{
		"openshift.labels.cluster":            kw,
		"resource_attributes.cluster":         text,
		"resource_attributes.cluster.keyword": kw,
		"k8s.namespace.name":                  text, // text-only: keyword şartlı rolde SAYILMAZ
		"trace_id":                            text, // kimlik rolü: varlık yeter
	}
	cands := []string{"resource_attributes.k8s.cluster.name", "resource_attributes.cluster", "openshift.labels.cluster"}
	cases := []struct {
		name        string
		configured  string
		candidates  []string
		probed      bool
		needKeyword bool
		want        FieldResolution
	}{
		{"configured kazanır (keşif sonucuna bakılmaz)", "labels.cluster", cands, true, true,
			FieldResolution{Field: "labels.cluster", Source: FieldConfigured}},
		{"configured prob olmadan da kazanır", " labels.cluster ", cands, false, true,
			FieldResolution{Field: "labels.cluster", Source: FieldConfigured}},
		{"discovered: aday SIRASIYLA, .keyword alt-alanı sayılır", "", cands, true, true,
			FieldResolution{Field: "resource_attributes.cluster",
				Fields: []string{"resource_attributes.cluster", "openshift.labels.cluster"}, Source: FieldDiscovered}},
		{"discovered tek aday → Fields boş", "", []string{"missing.a", "openshift.labels.cluster"}, true, true,
			FieldResolution{Field: "openshift.labels.cluster", Source: FieldDiscovered}},
		{"text-only aday keyword rolünde none", "", []string{"k8s.namespace.name"}, true, true,
			FieldResolution{Source: FieldNone}},
		{"kimlik rolünde text yeter", "", []string{"trace.id", "trace_id"}, true, false,
			FieldResolution{Field: "trace_id", Source: FieldDiscovered}},
		{"prob başarılı, hiçbiri yok → none", "", []string{"a", "b"}, true, true,
			FieldResolution{Source: FieldNone}},
		{"prob yok → unverified (yokluk iddia edilmez)", "", cands, false, true,
			FieldResolution{Source: FieldUnverified}},
	}
	for _, c := range cases {
		got := resolveFieldRole(c.configured, c.candidates, caps, c.probed, c.needKeyword)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

// Varsayılan (defaults()) alan "configured" SAYILMAZ: ham harita boşsa
// trace.id / service.name keşifle doğrulanır. Operatör yazdıysa configured.
func TestBuildESFieldMappingConfiguredVsDefault(t *testing.T) {
	kw := traceFieldCap{Types: []string{"keyword"}, Aggregatable: true}
	caps := map[string]traceFieldCap{
		"service.name":                kw,
		"trace.id":                    kw,
		"kubernetes.pod_name":         kw,
		"@timestamp":                  {Types: []string{"date"}, Aggregatable: true},
		"deployment.environment.name": kw,
	}
	eff := ESConfig{}
	eff.defaults()
	raw := ESFieldMap{SpanID: "custom.span", Namespace: "labels.ns"}
	m := buildESFieldMapping(raw, eff.Fields, caps, true)
	want := map[string]string{
		RoleService:   "service.name (discovered)",
		RoleEnv:       "deployment.environment.name (discovered)",
		RoleCluster:   "none",
		RoleNamespace: "labels.ns (configured)",
		RolePod:       "kubernetes.pod_name (discovered)",
		RoleVersion:   "none",
		RoleTraceID:   "trace.id (discovered)",
		RoleSpanID:    "custom.span (configured)",
		RoleTimestamp: "@timestamp (discovered)",
	}
	if got := m.Labels(); !reflect.DeepEqual(got, want) {
		t.Fatalf("labels:\n got %v\nwant %v", got, want)
	}
	if m.Backend != "elasticsearch" {
		t.Fatalf("backend = %q", m.Backend)
	}
	// Prob yoksa yapılandırılmamış roller unverified, yapılandırılmışlar aynen.
	m2 := buildESFieldMapping(raw, eff.Fields, nil, false)
	if m2.Role(RoleService).Source != FieldUnverified || m2.Role(RoleNamespace).Source != FieldConfigured {
		t.Fatalf("probsuz eşleme: %v", m2.Labels())
	}
	if m2.Role(RoleTraceID).Real() {
		t.Fatal("unverified trace alanı 'gerçek' sayılmamalı (match=trace_id iddiası yanlış olurdu)")
	}
}

func TestUnresolvedFilterRoles(t *testing.T) {
	m := FieldMapping{Roles: map[string]FieldResolution{
		RoleService:   {Field: "service.name", Source: FieldDiscovered},
		RoleCluster:   {Source: FieldNone},
		RoleNamespace: {Source: FieldUnverified},
		RolePod:       {Source: FieldNone},
		RoleEnv:       {Source: FieldNone},
	}}
	got := UnresolvedFilterRoles(Filter{Service: "checkout", Cluster: "cluster-a", Namespace: "payments", Pod: "p-1", Env: "prod"}, m)
	want := []string{RoleCluster, RolePod} // env Page.EnvUnapplied'da; unverified yokluk iddiası değil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := UnresolvedFilterRoles(Filter{Service: "checkout"}, m); got != nil {
		t.Fatalf("istenmeyen filtre raporlanmamalı: %v", got)
	}
}

func TestFieldResolutionLabelAndHas(t *testing.T) {
	r := FieldResolution{Field: "a", Fields: []string{"a", "b"}, Source: FieldDiscovered}
	if r.Label() != "a|b (discovered)" {
		t.Fatalf("label = %q", r.Label())
	}
	if !r.Has("b") || !r.Has("b.keyword") || r.Has("c") {
		t.Fatal("Has çok-alanlı rolü ve .keyword alt-alanını tanımalı")
	}
	if (FieldResolution{Source: FieldNone}).Label() != "none" || (FieldResolution{}).Label() != "none" {
		t.Fatal("alansız çözüm etiketi kaynağın kendisi olmalı")
	}
	if (FieldResolution{Field: "x", Source: FieldSchema}).Real() != true {
		t.Fatal("schema gerçek alan")
	}
}

// CH ayna listeleri clickhouse.go ifadeleriyle aynı anahtarları taşımalı —
// rapor edilen alan, süzgecin gerçekten baktığı alan olsun (v0.8.265 sınıfı).
func TestCHRoleKeysMirrorExprs(t *testing.T) {
	for _, c := range []struct {
		name string
		keys []string
		expr string
	}{
		{"env", chEnvKeys, chLogsEnvExpr},
		{"cluster", chClusterKeys, chLogsClusterExpr},
		{"namespace", chNamespaceKeys, chLogsNamespaceExpr},
		{"pod", chPodKeys, chLogsPodExpr},
	} {
		if strings.Count(c.expr, "indexOf(res_keys") != len(c.keys) {
			t.Errorf("%s: ifade %d anahtar taşıyor, ayna %d", c.name, strings.Count(c.expr, "indexOf(res_keys"), len(c.keys))
		}
		for _, k := range c.keys {
			if !strings.Contains(c.expr, "'"+k+"'") {
				t.Errorf("%s: %q ifadede yok", c.name, k)
			}
		}
	}
	m := chFieldMapping()
	for _, role := range FieldRoles {
		if r := m.Role(role); r.Source != FieldSchema || !r.Real() {
			t.Errorf("CH %s: %+v (schema beklenir)", role, r)
		}
	}
}

// esServiceFallbackFields, buildQuery'deki svcFields literalinin kuyruğu
// ile aynı olmalı (rapor = süzgeç).
func TestESServiceFallbackFieldsMirrorSvcFields(t *testing.T) {
	b, err := os.ReadFile("elasticsearch.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "svcFields := []string{")
	if i < 0 {
		t.Fatal("svcFields bulunamadı")
	}
	block := src[i : i+strings.Index(src[i:], "}")]
	for _, f := range esServiceFallbackFields {
		if !strings.Contains(block, `"`+f+`"`) {
			t.Errorf("svcFields %q taşımıyor", f)
		}
	}
}

func TestRoleValue(t *testing.T) {
	esRec := &LogRecord{
		ServiceName: "checkout",
		TraceID:     "0123456789abcdef0123456789abcdef",
		Attributes: map[string]string{
			"resource_attributes.k8s.cluster.name": "cluster-a",
			"labels.ns":                            "payments",
		},
		ResourceAttributes: map[string]string{
			"kubernetes.pod_name":         "checkout-7d6f9b54c5-xkv2m",
			"deployment.environment.name": "prod",
		},
	}
	m := FieldMapping{Roles: map[string]FieldResolution{
		RoleNamespace: {Field: "labels.ns", Source: FieldConfigured},
	}}
	for role, want := range map[string]string{
		RoleService:   "checkout",
		RoleTraceID:   "0123456789abcdef0123456789abcdef",
		RoleCluster:   "cluster-a",
		RoleNamespace: "payments",
		RolePod:       "checkout-7d6f9b54c5-xkv2m",
		RoleEnv:       "prod",
		RoleVersion:   "",
	} {
		if got := RoleValue(esRec, role, m); got != want {
			t.Errorf("ES %s = %q want %q", role, got, want)
		}
	}
	chRec := &LogRecord{ResourceAttributes: map[string]string{
		"k8s.cluster.name": "cluster-a", "k8s.namespace.name": "inventory", "service.version": "1.4.2",
	}}
	for role, want := range map[string]string{RoleCluster: "cluster-a", RoleNamespace: "inventory", RoleVersion: "1.4.2"} {
		if got := RoleValue(chRec, role, chFieldMapping()); got != want {
			t.Errorf("CH %s = %q want %q", role, got, want)
		}
	}
	if RoleValue(nil, RoleCluster, m) != "" {
		t.Fatal("nil kayıt boş döner")
	}
}

// Env tohumu yalnız BOŞ üyeyi doldurur; UI/blob değeri kazanır.
func TestWithESFieldEnv(t *testing.T) {
	t.Setenv("COREMETRY_ES_FIELD_CLUSTER", "labels.cluster")
	t.Setenv("COREMETRY_ES_FIELD_NAMESPACE", " labels.ns ")
	t.Setenv("COREMETRY_ES_FIELD_POD", "")
	t.Setenv("COREMETRY_ES_FIELD_VERSION", "labels.version")
	got := withESFieldEnv(ESFieldMap{Version: "ui.version"})
	if got.Cluster != "labels.cluster" || got.Namespace != "labels.ns" || got.Pod != "" || got.Version != "ui.version" {
		t.Fatalf("env tohumu: %+v", got)
	}
}

// CH Search cluster/namespace'i taşımıyor — bayrak bunu söylemeli.
func TestCHSearchUnapplied(t *testing.T) {
	if got := chSearchUnapplied(Filter{Pod: "p", Env: "prod", Service: "checkout"}); got != nil {
		t.Fatalf("uygulanan filtreler raporlanmamalı: %v", got)
	}
	got := chSearchUnapplied(Filter{Cluster: "cluster-a", Namespace: "payments"})
	if !reflect.DeepEqual(got, []string{RoleCluster, RoleNamespace}) {
		t.Fatalf("got %v", got)
	}
}
