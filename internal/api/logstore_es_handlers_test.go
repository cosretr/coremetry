package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cilcenk/coremetry/internal/logstore"
)

// v0.10.944 — CoSRE çapraz-kaynak eşleme (Faz A, yalnız backend): ES alan
// haritasının yeni rol alanları (cluster / namespace / pod / version)
// GET /api/settings/logstore'da görünür, PUT'ta yazılır; PUT gövdesinde
// anahtar HİÇ yoksa saklı değer korunur (Faz A formu bu anahtarları
// bilmiyor — düz string olsaydı her UI kaydı eşlemeyi sessizce silerdi),
// boş dize ise temizler (keşfe döner).

func TestMergeESSettingsRoleFields(t *testing.T) {
	cur := logstore.ESSettings{
		Backend: "elasticsearch", Addresses: []string{"http://es-a:9200"},
		Fields: logstore.ESFieldMap{TraceID: "trace.id", Cluster: "labels.cluster", Namespace: "labels.ns", Pod: "labels.pod", Version: "labels.version"},
	}
	cases := []struct {
		name string
		body string
		want logstore.ESFieldMap
	}{
		{"eski form: rol anahtarları yok → saklı korunur",
			`{"backend":"elasticsearch","addresses":["http://es-a:9200"],"fields":{"traceId":"trace_id","env":"deployment.environment"}}`,
			logstore.ESFieldMap{TraceID: "trace_id", Env: "deployment.environment", Cluster: "labels.cluster", Namespace: "labels.ns", Pod: "labels.pod", Version: "labels.version"}},
		{"yeni değer kırpılır, boş dize temizler",
			`{"backend":"elasticsearch","addresses":["http://es-a:9200"],"fields":{"cluster":" openshift.labels.cluster ","pod":"","version":"service.version"}}`,
			logstore.ESFieldMap{Cluster: "openshift.labels.cluster", Namespace: "labels.ns", Pod: "", Version: "service.version"}},
		{"fields hiç yok → rol alanları korunur",
			`{"backend":"elasticsearch","addresses":["http://es-a:9200"]}`,
			logstore.ESFieldMap{Cluster: "labels.cluster", Namespace: "labels.ns", Pod: "labels.pod", Version: "labels.version"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var in esSettingsInput
			if err := json.Unmarshal([]byte(c.body), &in); err != nil {
				t.Fatal(err)
			}
			got, bad := mergeESSettings(in, cur)
			if bad != "" {
				t.Fatalf("400: %s", bad)
			}
			if got.Fields != c.want {
				t.Fatalf("fields:\n got %+v\nwant %+v", got.Fields, c.want)
			}
		})
	}
}

// GET snapshot yeni anahtarları taşır; env tohumu (COREMETRY_ES_FIELD_*)
// ilk UI kaydından önce de görünür (source "env").
func TestGetLogstoreESSettingsExposesRoleFields(t *testing.T) {
	t.Setenv("COREMETRY_ES_FIELD_POD", "kubernetes.pod_name")
	mgr := logstore.NewESManager(logstore.NewSwitchable(nil), nil, nil, logstore.ESSettings{
		Backend: "elasticsearch", Addresses: []string{"http://es-a:9200"},
		Fields: logstore.ESFieldMap{Cluster: "openshift.labels.cluster", Namespace: "kubernetes.namespace_name", Version: "service.version"},
	})
	s := &Server{logsMgr: mgr}
	rec := httptest.NewRecorder()
	s.getLogstoreESSettings(rec, httptest.NewRequest(http.MethodGet, "/api/settings/logstore", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var snap struct {
		Source string            `json:"source"`
		Fields map[string]string `json:"fields"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"cluster": "openshift.labels.cluster", "namespace": "kubernetes.namespace_name",
		"pod": "kubernetes.pod_name", "version": "service.version",
	}
	for k, v := range want {
		if snap.Fields[k] != v {
			t.Errorf("fields.%s = %q want %q (payload %s)", k, snap.Fields[k], v, rec.Body.String())
		}
	}
	if snap.Source != "env" {
		t.Errorf("source = %q", snap.Source)
	}
}
