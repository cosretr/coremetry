package v2probe

// tokenize_test.go — v0.10.979 — jetonlayıcı değişmezleri (spec §8): kararlılık,
// sıralı ilk görülme numaralaması, port/şema harfi harfine,
// kubernetes.default.svc harfi harfine, tohum takma adları kazanır, ScrubText
// serbest metindeki küme adını siler, Count(); varsayılan red; sızıntı testi
// (nöbetçi değerler hiçbir çıktıda görünmez).

import (
	"strings"
	"testing"
)

func testSeeds() Seeds {
	return Seeds{
		Clusters: []Alias{
			{Token: "cluster-a", Raw: []string{"c-1a2b3c4d", "realcluster-prod-01", "ocp-prod-01"}},
			{Token: "cluster-b", Raw: []string{"c-2b3c4d5e", "realcluster-prod-02"}},
			{Token: "hub-1", Raw: []string{"c-9f8e7d6c", "hubraw"}},
		},
		APIHosts:   map[string]string{"api.real.example.invalid:6443": "cluster-a", "api.real.example.invalid": "cluster-a"},
		EnvList:    []string{"dev", "test", "prod"},
		SuffixList: []string{"ocpa", "ocpb"},
	}
}

func TestTokenizerClasses(t *testing.T) {
	tok := NewTokenizer(testSeeds())
	cases := []struct{ label, raw, want string }{
		{"cluster", "realcluster-prod-01", "cluster-a"},
		{"cluster", "c-9f8e7d6c", "hub-1"},
		{"cluster", "hubraw", "hub-1"},
		{"cluster_id", "unknown-x", "cluster-c"},
		{"tenant", "unknown-y", "cluster-d"},
		{"tenant", "unknown-x", "cluster-c"}, // kararlılık: sınıf paylaşımlı
		{"prometheus", "k8s", "<prometheus-1>"},
		{"prometheus_replica", "prometheus-k8s-0", "<replica-1>"},
		{"prometheus_replica", "prometheus-k8s-1", "<replica-2>"},
		{"prometheus_replica", "", ""},
		{"dest_server", "https://kubernetes.default.svc", "https://kubernetes.default.svc"},
		{"dest_server", "", ""},
		{"dest_server", "https://api.real.example.invalid:6443", "https://api.cluster-a.<domain>:6443"},
		{"dest_server", "https://api.other.example.invalid:6443", "https://api.server-1.<domain>:6443"},
		{"dest_server", "http://api.other.example.invalid", "http://api.server-1.<domain>"},
		{"dest_server", "not a url", "<dest-server-1>"},
		{"namespace", "team-payments-prod", "<team-1>-prod"},
		{"exported_namespace", "team-payments-dev", "<team-1>-dev"},
		{"dest_namespace", "team-billing-PROD", "<team-2>-prod"},
		{"namespace", "openshift-monitoring", "<ns-1>"},
		{"job", "team-payments-prod-metrics", "<team-1>-prod-metrics"},
		{"job", "kube-state-metrics", "<job-1>"},
		{"name", "checkout-api-prod-ocpa", "app-1"},
		{"project", "payments", "<project-1>"},
		{"repo", "https://git.example.invalid/x.git", "<repo-1>"},
		{"sfx", "ocpb", "<suffix-b>"},
		{"sfx", "OCPA", "<suffix-a>"},
		{"sfx", "zzz", "<sfx-1>"},
		{"base", "checkout-api-prod", "<base-1>"},
		{"pod", "argocd-application-controller-0", "<pod-1>"},
		{"deploy_env", "prod-realcluster-prod-01", "prod-cluster-a"},
		{"deploy_env", "prod", "<deploy-env-1>"},
		{"deploy_env", "", ""},
		{"__name__", "kube_deployment_created", "kube_deployment_created"},
		{"version", "v2.13.0", "v2.13.0"},
		{"port", "6443", "6443"},
		{"env", "prod", "prod"},
		{"apps_per_target_ns", "3", "3"},
		{"secret_host", "db.internal.example.invalid", "<secret_host-1>"},
		{"secret_host", "", ""},
		// v0.10.979 — hub H0.3/H0.5 küme kimliği etiketleri: küresel allowlist
		// (eski T1 sayaç adı k8s_cluster) sınıfı gölgeleyemez.
		{"k8s_cluster", "realcluster-prod-01", "cluster-a"},
		{"openshift_cluster", "realcluster-prod-01", "cluster-a"},
		{"cluster_name", "hubraw", "hub-1"},
		{"k8s_cluster", "unseeded-hub-01", "cluster-e"},
	}
	for _, c := range cases {
		if got := tok.Value(c.label, c.raw); got != c.want {
			t.Errorf("Value(%s, %q) = %q, beklenen %q", c.label, c.raw, got, c.want)
		}
	}
	if d := tok.Denied(); len(d) != 1 || d[0] != "secret_host" {
		t.Errorf("Denied: %v", d)
	}
	if tok.Value("job", "kube-state-metrics", "job") != "kube-state-metrics" {
		t.Error("literalFor job harfi harfine olmalı")
	}
	// T1 satırı: sayaç literalFor ile harfi harfine kalır (rapor T1 özeti okur).
	if tok.Value("k8s_cluster", "120000", "k8s_cluster") != "120000" {
		t.Error("literalFor k8s_cluster sayacı harfi harfine olmalı")
	}
	if tok.Count() == 0 {
		t.Error("Count 0")
	}
}

// TestLiteralLabelsDisjointFromClasses — v0.10.979 — küresel allowlist hiçbir
// jetonlanan sınıfla kesişmez (kesişseydi Value sırası ne olursa olsun bir
// taraf yanlış olurdu; olay: k8s_cluster).
func TestLiteralLabelsDisjointFromClasses(t *testing.T) {
	explicit := map[string]bool{"prometheus": true, "prometheus_replica": true, "dest_server": true, "job": true, "name": true,
		"project": true, "repo": true, "sfx": true, "base": true, "pod": true, "deploy_env": true}
	for l := range LiteralLabels {
		if clusterLabelSet[l] || nsLabelSet[l] || explicit[l] {
			t.Errorf("LiteralLabels %q bir jeton sınıfıyla çakışıyor", l)
		}
	}
	for _, l := range []string{"sampled", "svc_version", "img_tag", "container_id", "depl", "rs", "sts", "ds", "env_name", "k8s_cluster", "ocp_cluster", "n", "spans"} {
		if LiteralLabels[l] {
			t.Errorf("T sayacı %q küresel allowlist'te olmamalı (T1 LiteralFor)", l)
		}
	}
	// Sınıf allowlist'ten önce: aynı ad iki tarafta olsa bile jeton kazanır.
	tok := NewTokenizer(testSeeds())
	LiteralLabels["k8s_cluster"] = true
	defer delete(LiteralLabels, "k8s_cluster")
	if got := tok.Value("k8s_cluster", "realcluster-prod-01"); got != "cluster-a" {
		t.Errorf("sınıf allowlist'ten önce bakılmalı: %q", got)
	}
}

func TestTokenizerStability(t *testing.T) {
	a, b := NewTokenizer(testSeeds()), NewTokenizer(testSeeds())
	seq := [][2]string{{"namespace", "team-x-prod"}, {"namespace", "team-y-prod"}, {"cluster", "zeta"}, {"name", "app-x"}, {"dest_server", "https://api.q.example.invalid:6443"}}
	for _, s := range seq {
		if x, y := a.Value(s[0], s[1]), b.Value(s[0], s[1]); x != y {
			t.Errorf("iki koşu farklı jeton: %s vs %s", x, y)
		}
	}
	// v0.10.988 — ikinci çağrı ilkine eşit olmalı (SA4000: koşul iki kez aynıydı).
	v1, v2 := a.Value("namespace", "team-x-prod"), a.Value("namespace", "team-x-prod")
	if v1 != "<team-1>-prod" || v2 != v1 {
		t.Error("aynı ham → aynı jeton")
	}
	if a.Count() != b.Count() {
		t.Error("Count farklı")
	}
}

func TestLetters(t *testing.T) {
	for i, want := range []string{"a", "b", "c"} {
		if letters(i) != want {
			t.Errorf("letters(%d) = %s", i, letters(i))
		}
	}
	if letters(25) != "z" || letters(26) != "aa" || letters(27) != "ab" {
		t.Errorf("letters: %s %s %s", letters(25), letters(26), letters(27))
	}
}

func TestScrubText(t *testing.T) {
	tok := NewTokenizer(testSeeds())
	tok.Value("namespace", "team-payments-prod")
	in := `store realcluster-prod-01 unreachable at https://thanos.realcluster-prod-01.example.invalid:9090/api/v1/query; hub hubraw ok; ns team-payments-prod; see http://x.example.invalid`
	out := tok.ScrubText(in)
	for _, bad := range []string{"realcluster-prod-01", "hubraw", "team-payments-prod", "thanos.", "x.example.invalid"} {
		if strings.Contains(out, bad) {
			t.Errorf("ScrubText sızdırdı %q: %s", bad, out)
		}
	}
	if !strings.Contains(out, "store cluster-a unreachable") || !strings.Contains(out, "hub hub-1 ok") || !strings.Contains(out, "https://<host>") {
		t.Errorf("ScrubText biçimi: %s", out)
	}
	if scrubURLs("no url here") != "no url here" || scrubURLs("ftp://keep.this") != "ftp://keep.this" {
		t.Error("scrubURLs")
	}
	if got := scrubURLs("a https://h:1/p b"); got != "a https://<host>/p b" {
		t.Errorf("scrubURLs yol: %q", got)
	}
}

func TestTokenizerRowSorted(t *testing.T) {
	tok := NewTokenizer(testSeeds())
	row := tok.Row(map[string]string{"namespace": "team-b-prod", "job": "team-b-prod-metrics", "cluster": "realcluster-prod-02", "unknown": "v"}, nil)
	if row["namespace"] != "<team-1>-prod" || row["job"] != "<team-1>-prod-metrics" || row["cluster"] != "cluster-b" || row["unknown"] != "<unknown-1>" {
		t.Errorf("Row: %v", row)
	}
}
