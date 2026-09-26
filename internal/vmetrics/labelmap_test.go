package vmetrics

// v0.10.944 — LabelMap çözümleyicisi (configured > convention > discovered >
// none), PUT doğrulaması ve kalıcılık tam turu. Sentetik etiket değerleri.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

func TestResolveLabelRole(t *testing.T) {
	otlp := []string{"service_name", "k8s_namespace_name", "k8s_pod_name", "deployment_environment_name", "http_route"}
	ksm := []string{"job", "namespace", "pod", "cluster"}
	tests := []struct {
		name       string
		role       string
		m          LabelMap
		present    []string
		discovered bool
		want       string // LabelResolution.String()
		wantPres   bool
	}{
		{"servis konvansiyonu (keşifte görüldü)", RoleService, LabelMap{}, otlp, true, "service_name (convention)", true},
		{"servis konvansiyonu (keşif yok = bugünkü davranış)", RoleService, LabelMap{}, nil, false, "service_name (convention)", false},
		{"servis: service_name yok → kimlik listesinden keşif", RoleService, LabelMap{}, ksm, true, "job (discovered)", true},
		{"servis: hiçbir aday yok → none", RoleService, LabelMap{}, []string{"http_route"}, true, "none", false},
		{"yapılandırılmış servis kazanır", RoleService, LabelMap{Service: "app"}, otlp, true, "app (configured)", false},
		{"env keşif: deployment_environment_name", RoleEnv, LabelMap{}, otlp, true, "deployment_environment_name (discovered)", true},
		{"env yok → none (konvansiyon YOK)", RoleEnv, LabelMap{}, ksm, true, "none", false},
		{"env keşif yapılmadıysa none", RoleEnv, LabelMap{}, nil, false, "none", false},
		{"env yapılandırılmış", RoleEnv, LabelMap{Env: " env "}, ksm, true, "env (configured)", false},
		{"namespace OTel", RoleNamespace, LabelMap{}, otlp, true, "k8s_namespace_name (discovered)", true},
		{"namespace ksm", RoleNamespace, LabelMap{}, ksm, true, "namespace (discovered)", true},
		{"pod ksm", RolePod, LabelMap{}, ksm, true, "pod (discovered)", true},
		{"cluster düz", RoleCluster, LabelMap{}, ksm, true, "cluster (discovered)", true},
		{"version yok", RoleVersion, LabelMap{}, otlp, true, "none", false},
		{"version keşif", RoleVersion, LabelMap{}, []string{"service_version"}, true, "service_version (discovered)", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := map[string]bool{}
			for _, p := range tt.present {
				set[p] = true
			}
			got := ResolveLabelRole(tt.role, tt.m, set, tt.discovered)
			if got.String() != tt.want || got.Present != tt.wantPres {
				t.Fatalf("got %q present=%v, want %q present=%v", got.String(), got.Present, tt.want, tt.wantPres)
			}
		})
	}
}

// v0.10.944 — birden çok aday yazım görüldüğünde ilk görülen Label olur,
// kalanı Alternatives'e düşer ve String() hepsini söyler: MetricsQL iki etiket
// adını tek eşleştiricide VEYA'layamaz, sessizce ilkini seçmek diğer yazımı
// taşıyan serileri gizlerdi. Yapılandırılmış etiket ve konvansiyon koşulsuz
// kazanır (Alternatives yalnız discovered dalında).
func TestResolveLabelRoleAlternatives(t *testing.T) {
	both := map[string]bool{
		"deployment_environment_name": true, "deployment_environment": true,
		"k8s_namespace_name": true, "namespace": true,
		"k8s_pod_name": true, "pod": true,
		"service_name": true, "job": true,
	}
	tests := []struct {
		name     string
		role     string
		m        LabelMap
		wantLbl  string
		wantAlts string
		wantStr  string
	}{
		{"env iki yazım", RoleEnv, LabelMap{}, "deployment_environment_name", "deployment_environment",
			"deployment_environment_name (discovered; ayrıca: deployment_environment)"},
		{"namespace OTel + ksm", RoleNamespace, LabelMap{}, "k8s_namespace_name", "namespace",
			"k8s_namespace_name (discovered; ayrıca: namespace)"},
		{"pod OTel + ksm", RolePod, LabelMap{}, "k8s_pod_name", "pod", "k8s_pod_name (discovered; ayrıca: pod)"},
		{"configured kazanır, alternatif yok", RoleEnv, LabelMap{Env: "deployment_environment"}, "deployment_environment", "",
			"deployment_environment (configured)"},
		{"servis konvansiyonu kazanır (job da var)", RoleService, LabelMap{}, "service_name", "", "service_name (convention)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveLabelRole(tt.role, tt.m, both, true)
			if got.Label != tt.wantLbl || strings.Join(got.Alternatives, ",") != tt.wantAlts || got.String() != tt.wantStr {
				t.Fatalf("got label=%q alts=%v str=%q", got.Label, got.Alternatives, got.String())
			}
		})
	}
	// Tek yazım: Alternatives boş, String() eski biçim (eşleme raporu değişmez).
	one := ResolveLabelRole(RoleEnv, LabelMap{}, map[string]bool{"deployment_environment": true}, true)
	if one.Label != "deployment_environment" || len(one.Alternatives) != 0 || one.String() != "deployment_environment (discovered)" {
		t.Fatalf("tek yazım: %+v %q", one, one.String())
	}
}

// Rapor sırası sabit ve her rol bir kez.
func TestResolveLabelRolesOrder(t *testing.T) {
	got := ResolveLabelRoles(LabelMap{}, nil, false)
	if len(got) != len(LabelRoles) {
		t.Fatalf("rol sayısı %d", len(got))
	}
	for i, r := range got {
		if r.Role != LabelRoles[i] {
			t.Fatalf("sıra: %d=%s, beklenen %s", i, r.Role, LabelRoles[i])
		}
	}
}

// Adaylar UYDURULMAZ: kaynak listelerden türetilir ve konvansiyonu tekrar etmez.
func TestRoleCandidatesDerived(t *testing.T) {
	svc := RoleCandidates(RoleService)
	for _, c := range svc {
		if c == "service_name" {
			t.Fatal("konvansiyon etiketi aday listesinde tekrar ediyor")
		}
	}
	if len(svc) == 0 || svc[0] != "k8s_deployment_name" {
		t.Fatalf("servis adayları chstore.ServiceIdentityLabels sırasını izlemeli: %v", svc)
	}
	env := RoleCandidates(RoleEnv)
	if len(env) != len(chstore.EnvAttrKeys) || env[0] != "deployment_environment_name" {
		t.Fatalf("env adayları chstore.EnvAttrKeys'ten türemeli: %v", env)
	}
	for _, role := range LabelRoles {
		for _, c := range RoleCandidates(role) {
			if !ValidLabelName(c) {
				t.Fatalf("%s adayı geçerli etiket adı değil: %q", role, c)
			}
		}
	}
}

func TestLabelMapProblem(t *testing.T) {
	if p := LabelMapProblem(LabelMap{Service: "service_name", Env: " deployment_environment ", Pod: ""}); p != "" {
		t.Fatalf("geçerli eşleme reddedildi: %s", p)
	}
	p := LabelMapProblem(LabelMap{Namespace: "k8s.namespace.name"})
	if p == "" || !strings.Contains(p, "labelMap.namespace") || !strings.Contains(p, "k8s_namespace_name") {
		t.Fatalf("noktalı ad reddedilmeli ve doğru yazımı önermeli: %q", p)
	}
	if LabelMapProblem(LabelMap{Pod: "1pod"}) == "" {
		t.Fatal("rakamla başlayan etiket adı kabul edildi")
	}
}

// Eşlemesiz blob eskisiyle bayt-aynı (omitzero); eşleme kalıcılıkta kaybolmaz
// ve Snapshot'ta görünür (form Faz B'de kendi değerini okuyabilmeli).
func TestLabelMapPersistence(t *testing.T) {
	raw, err := json.Marshal(Settings{Enabled: true, BaseURL: "http://vm:8428"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "labelMap") {
		t.Fatalf("eşlemesiz blob labelMap taşıyor: %s", raw)
	}
	store := &fakeVMSettingsStore{}
	s := New()
	cfg := Settings{Enabled: true, BaseURL: "http://vm:8428",
		LabelMap: LabelMap{Service: "app", Env: "env", Namespace: "namespace"}}
	if err := s.SavePersisted(context.Background(), store, cfg); err != nil {
		t.Fatal(err)
	}
	fresh := New()
	if err := fresh.LoadPersisted(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if got := fresh.CurrentSettings(); got != cfg {
		t.Fatalf("tam tur alan kaybetti:\n got  %+v\n want %+v", got, cfg)
	}
	if snap := fresh.Snapshot(); snap.LabelMap != cfg.LabelMap {
		t.Fatalf("Snapshot eşlemeyi göstermiyor: %+v", snap.LabelMap)
	}
	if fresh.MetricLabelMap() != cfg.LabelMap {
		t.Fatalf("MetricLabelMap canlı eşlemeyi döndürmüyor: %+v", fresh.MetricLabelMap())
	}
	var snapJSON map[string]any
	b, _ := json.Marshal(fresh.Snapshot())
	_ = json.Unmarshal(b, &snapJSON)
	if _, ok := snapJSON["labelMap"]; !ok {
		t.Fatalf("GET gövdesinde labelMap anahtarı yok: %s", b)
	}
}

// ── QueryMetricDetailed / keşif — VM stub'ıyla tel seviyesi ────────────────

func TestQueryMetricDetailedFlags(t *testing.T) {
	var gotQuery, gotStep string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		gotStep = r.URL.Query().Get("step")
		_, _ = w.Write([]byte(`{"status":"success","isPartial":true,"data":{"resultType":"matrix","result":[` +
			`{"metric":{"k8s_pod_name":"checkout-1"},"values":[[1700000060,"1.5"],[1700000120,"NaN"]]}]}}`))
	}))
	defer srv.Close()
	s := New()
	s.Configure(Settings{BaseURL: srv.URL})
	from := time.Unix(1700000000, 0)
	d, err := s.QueryMetricDetailed(context.Background(), chstore.MetricQueryFilter{
		Name: "jvm_memory_used_bytes", Aggregation: "avg", GroupBy: []string{"k8s_pod_name"},
		Filters: []chstore.FilterExpr{{Key: "k8s_namespace_name", Op: "=", Values: []string{"payments"}}},
		From:    from, To: from.Add(10 * time.Minute), StepSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Partial {
		t.Fatal("isPartial çözülmedi")
	}
	if d.Step != 60 || gotStep != "60s" {
		t.Fatalf("adım: %d / %q", d.Step, gotStep)
	}
	if !strings.Contains(gotQuery, `k8s_namespace_name="payments"`) {
		t.Fatalf("süzgeç ifadeye girmedi: %s", gotQuery)
	}
	if len(d.Series) != 1 || len(d.Series[0].Points) != 1 {
		t.Fatalf("seri/nokta: %+v", d.Series)
	}
	if d.AnsweredAt.IsZero() || d.AnsweredAt.Location() != time.UTC {
		t.Fatalf("cevap anı UTC damgalı değil: %v", d.AnsweredAt)
	}
}

func TestQueryMetricDetailedUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	s := New()
	s.Configure(Settings{BaseURL: srv.URL})
	_, err := s.QueryMetricDetailed(context.Background(), chstore.MetricQueryFilter{Name: "up", Service: "checkout"})
	if !errors.Is(err, sourcestate.ErrUnauthorized) {
		t.Fatalf("401 ErrUnauthorized'a açılmadı: %v", err)
	}
}

// Keşif MUTLAK pencereyi taşır (sohbet çıpası) ve __name__'i düşürür.
func TestMetricLabelNamesInWindow(t *testing.T) {
	var start, end, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, start, end = r.URL.Path, r.URL.Query().Get("start"), r.URL.Query().Get("end")
		_, _ = w.Write([]byte(`{"status":"success","data":["pod","__name__","namespace"]}`))
	}))
	defer srv.Close()
	s := New()
	s.Configure(Settings{BaseURL: srv.URL})
	from := time.Unix(1700000000, 0)
	got, partial, err := s.MetricLabelNamesIn(context.Background(), "up", from, from.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if partial {
		t.Fatal("isPartial alanı yokken kısmi okundu")
	}
	if path != "/api/v1/labels" || start != "1700000000.000" || end != "1700003600.000" {
		t.Fatalf("pencere/uç yanlış: %s %s %s", path, start, end)
	}
	if strings.Join(got, ",") != "namespace,pod" {
		t.Fatalf("etiketler: %v", got)
	}
}
