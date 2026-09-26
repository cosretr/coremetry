package api

// v0.10.956 — Rollouts v2 P1.3 (docs/rollouts/v2-audit.md §3.2): Remote
// Cluster kaydının apiServerUrls / argoSuffix / pairGroup alanları.
//
// Sözleşme:
//   - GET /api/clusters/sources (viewer+) mevcut `clusters: [ad]` şeklini
//     KORUR ve yanına EK `entries: [{id, name, pairGroup?, argoSuffix?}]`
//     koyar — yalnız etkin kayıtlar; URL, apiServerUrls, token ya da
//     tokenRef viewer'a GİTMEZ.
//   - PUT /api/settings/thanos doğrulaması (normalise + tekillik,
//     thanos.ReconcileClusterSettings) blob'a yazmadan ÖNCE 400 döner; hata
//     kayıt adını söyler, ham URL'i (userinfo parolası) yankılamaz.
//   - Audit ayrıntısı yeni alanları taşır; secret taşımaz.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/thanos"
)

func thanosFieldsTestServer(t *testing.T) *Server {
	t.Helper()
	svc := thanos.New()
	svc.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{
		{ID: "c-aaaaaaaa", Name: "cluster-a", URL: "http://thanos-a.example.invalid", Enabled: true,
			Token: "tok-secret-a", TokenRef: "env:COREMETRY_THANOS_TOKEN_A",
			APIServerURLs: []string{"https://api.cluster-a.example.invalid:6443"}, ArgoSuffix: "ca", PairGroup: "pair-1"},
		{ID: "c-bbbbbbbb", Name: "cluster-b", URL: "http://thanos-b.example.invalid", Enabled: true},
		{ID: "c-cccccccc", Name: "cluster-off", URL: "http://thanos-off.example.invalid", Enabled: false,
			ArgoSuffix: "coff", PairGroup: "pair-9"},
	}})
	return &Server{thanos: svc}
}

func TestClusterSourcesEntriesAreViewerSafe(t *testing.T) {
	s := thanosFieldsTestServer(t)
	rec := httptest.NewRecorder()
	s.getClusterSources(rec, httptest.NewRequest(http.MethodGet, "/api/clusters/sources", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("200 beklenir, alınan %d", rec.Code)
	}
	body := rec.Body.String()
	var got struct {
		Clusters []string `json:"clusters"`
		Entries  []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			PairGroup  string `json:"pairGroup"`
			ArgoSuffix string `json:"argoSuffix"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	// Eski şekil aynen: yalnız etkin adlar.
	if strings.Join(got.Clusters, ",") != "cluster-a,cluster-b" {
		t.Fatalf("clusters [ad] şekli korunmalı (yalnız etkin): %v", got.Clusters)
	}
	if len(got.Entries) != 2 || got.Entries[0].ID != "c-aaaaaaaa" || got.Entries[0].Name != "cluster-a" ||
		got.Entries[0].PairGroup != "pair-1" || got.Entries[0].ArgoSuffix != "ca" ||
		got.Entries[1].Name != "cluster-b" || got.Entries[1].PairGroup != "" {
		t.Fatalf("entries: etkin kayıtlar id/ad/pairGroup/argoSuffix ile: %+v", got.Entries)
	}
	for _, leak := range []string{"apiServerUrls", "api.cluster-a", "thanos-a.example", `"url"`, "tok-secret", "tokenRef", "COREMETRY_THANOS", "coff"} {
		if strings.Contains(body, leak) {
			t.Fatalf("viewer yanıtı %q içermemeli: %s", leak, body)
		}
	}

	// Thanos servisi yoksa da boş diziler (null değil — FE .map()'liyor).
	rec2 := httptest.NewRecorder()
	(&Server{}).getClusterSources(rec2, httptest.NewRequest(http.MethodGet, "/api/clusters/sources", nil))
	if !strings.Contains(rec2.Body.String(), `"clusters":[]`) || !strings.Contains(rec2.Body.String(), `"entries":[]`) {
		t.Fatalf("servis yokken boş diziler: %s", rec2.Body.String())
	}
}

func TestPutThanosSettingsRejectsBadRolloutFields(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"geçersiz API server URL (userinfo)",
			`{"clusters":[{"name":"cluster-a","url":"http://thanos-a.example.invalid","enabled":true,
			  "apiServerUrls":["https://admin:hunter2@api.cluster-a.example.invalid"]}]}`,
			[]string{"cluster-a", "apiServerUrls"}},
		{"yol taşıyan URL",
			`{"clusters":[{"name":"cluster-a","url":"http://thanos-a.example.invalid","enabled":true,
			  "apiServerUrls":["https://api.cluster-a.example.invalid:6443/k8s"]}]}`,
			[]string{"cluster-a", "apiServerUrls"}},
		{"aynı API server iki kayıtta",
			`{"clusters":[
			  {"name":"cluster-a","url":"http://thanos-a.example.invalid","enabled":true,"apiServerUrls":["https://api.cluster-a.example.invalid"]},
			  {"name":"cluster-b","url":"http://thanos-b.example.invalid","enabled":true,"apiServerUrls":["HTTPS://API.cluster-a.example.invalid:6443/"]}]}`,
			[]string{"https://api.cluster-a.example.invalid:6443", "cluster-a"}},
		{"argoSuffix büyük/küçük harf duyarsız tekil",
			`{"clusters":[
			  {"name":"cluster-a","url":"http://thanos-a.example.invalid","enabled":true,"apiServerUrls":[],"argoSuffix":"ca"},
			  {"name":"cluster-b","url":"http://thanos-b.example.invalid","enabled":true,"apiServerUrls":[],"argoSuffix":"CA"}]}`,
			[]string{"argoSuffix", "cluster-a"}},
		{"pairGroup kontrol karakteri",
			`{"clusters":[{"name":"cluster-a","url":"http://thanos-a.example.invalid","enabled":true,"apiServerUrls":[],"pairGroup":"a\nb"}]}`,
			[]string{"pairGroup"}},
	}
	for _, tc := range cases {
		s := thanosFieldsTestServer(t)
		before := s.thanos.CurrentSettings()
		rec := httptest.NewRecorder()
		s.putThanosSettings(rec, httptest.NewRequest(http.MethodPut, "/api/settings/thanos", strings.NewReader(tc.body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: 400 beklenir, alınan %d (%s)", tc.name, rec.Code, rec.Body.String())
		}
		for _, w := range tc.want {
			if !strings.Contains(rec.Body.String(), w) {
				t.Fatalf("%s: hata %q içermeli: %s", tc.name, w, rec.Body.String())
			}
		}
		if strings.Contains(rec.Body.String(), "hunter2") {
			t.Fatalf("%s: hata parolayı yankılamamalı: %s", tc.name, rec.Body.String())
		}
		// Reddedilen PUT canlı yapılandırmaya dokunmaz.
		after := s.thanos.CurrentSettings()
		if len(after.Clusters) != len(before.Clusters) || after.Clusters[0].ArgoSuffix != "ca" {
			t.Fatalf("%s: reddedilen PUT yapılandırmayı değiştirmemeli: %+v", tc.name, after.Clusters)
		}
	}
}

func TestThanosSettingsAuditDetails(t *testing.T) {
	s := thanosFieldsTestServer(t)
	details := thanosSettingsAuditDetails(s.thanos.Snapshot())
	var got struct {
		Clusters []string `json:"clusters"`
		Count    int      `json:"count"`
		Rollouts []struct {
			Name          string   `json:"name"`
			APIServerURLs []string `json:"apiServerUrls"`
			ArgoSuffix    string   `json:"argoSuffix"`
			PairGroup     string   `json:"pairGroup"`
		} `json:"rollouts"`
	}
	if err := json.Unmarshal([]byte(details), &got); err != nil {
		t.Fatalf("audit ayrıntısı JSON olmalı: %v (%s)", err, details)
	}
	if got.Count != 3 || strings.Join(got.Clusters, ",") != "cluster-a(on),cluster-b(on),cluster-off(off)" {
		t.Fatalf("mevcut ad(on/off) + count biçimi korunmalı: %s", details)
	}
	// Yalnız alan taşıyan kayıtlar (cluster-b hiçbirini taşımıyor).
	if len(got.Rollouts) != 2 || got.Rollouts[0].Name != "cluster-a" ||
		strings.Join(got.Rollouts[0].APIServerURLs, ",") != "https://api.cluster-a.example.invalid:6443" ||
		got.Rollouts[0].ArgoSuffix != "ca" || got.Rollouts[0].PairGroup != "pair-1" ||
		got.Rollouts[1].Name != "cluster-off" || got.Rollouts[1].ArgoSuffix != "coff" {
		t.Fatalf("rollouts ayrıntısı alan taşıyan kayıtları listelemeli: %s", details)
	}
	for _, leak := range []string{"tok-secret", "tokenRef", "COREMETRY_THANOS", "hasToken"} {
		if strings.Contains(details, leak) {
			t.Fatalf("audit ayrıntısı %q içermemeli: %s", leak, details)
		}
	}
}
