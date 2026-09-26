package argocd

// discover_test.go — v0.10.957 — keşif adaylarının SAF türetimi (P1.4
// salt-okunur probe; audit §5.1–5.2, §11 H1.1/H1.2). Probe (api katmanı)
// hub'ın label-values API'sinden iş başına namespace / exported_namespace
// değerlerini toplar; aday kuralları burada: honorLabels durumu (A/B/C),
// paylaşılan iş adı, kayıtlı instance eşlemesi, kimlik önerisi.

import (
	"strings"
	"testing"
)

func TestAppInfoSelector(t *testing.T) {
	cases := map[string]string{
		"":                    `argocd_app_info`,
		"team-a-prod-metrics": `argocd_app_info{job="team-a-prod-metrics"}`,
		`we"ird\job`:          `argocd_app_info{job="we\"ird\\job"}`,
		"a\nb":                `argocd_app_info{job="a\nb"}`,
	}
	for job, want := range cases {
		if got := AppInfoSelector(job, ""); got != want {
			t.Errorf("AppInfoSelector(%q) = %s, beklenen %s", job, got, want)
		}
	}
	if got := AppInfoSelector("j", "team-a"); got != `argocd_app_info{job="j",namespace="team-a"}` {
		t.Errorf("namespace'li seçici: %s", got)
	}
	if got := AppInfoSelector("", "team-a"); got != `argocd_app_info{namespace="team-a"}` {
		t.Errorf("yalnız namespace: %s", got)
	}
}

func byJobNS(cs []Candidate) map[string]Candidate {
	m := map[string]Candidate{}
	for _, c := range cs {
		m[c.MetricsJob+"|"+c.HubNamespace] = c
	}
	return m
}

func TestBuildCandidatesNamespaceCases(t *testing.T) {
	probes := []JobProbe{
		// (A) ServiceMonitor, varsayılan honorLabels: exported_namespace var ve == namespace.
		{Job: "team-a-prod-metrics", Namespaces: []string{"team-a-prod"}, Exported: map[string][]string{"team-a-prod": {"team-a-prod"}}},
		// (C) apps-in-any-namespace: exported_namespace var ve != namespace.
		{Job: "team-b-prod-metrics", Namespaces: []string{"team-b-prod"}, Exported: map[string][]string{"team-b-prod": {"team-b-apps", "team-b-prod"}}},
		// (B) honorLabels: true — exported_namespace YOK; namespace = Application ns.
		{Job: "team-c-uat-metrics", Namespaces: []string{"team-c-uat"}},
		// (B) + birden çok app namespace'i → instance ns bilinemez.
		{Job: "team-d-uat-metrics", Namespaces: []string{"team-d-apps-1", "team-d-apps-2"}},
	}
	got := byJobNS(BuildCandidates("c-hub-a", probes, nil))
	if len(got) != 4 {
		t.Fatalf("4 aday beklenirdi: %+v", got)
	}
	a := got["team-a-prod-metrics|team-a-prod"]
	if a.NamespaceCase != "A" || a.AppsAnyNamespace || a.ID != "team-a-prod" || !a.Discovered || a.Name != "team-a-prod" {
		t.Errorf("A: %+v", a)
	}
	c := got["team-b-prod-metrics|team-b-prod"]
	if c.NamespaceCase != "C" || !c.AppsAnyNamespace || strings.Join(c.AppNamespaces, ",") != "team-b-apps,team-b-prod" {
		t.Errorf("C: %+v", c)
	}
	b := got["team-c-uat-metrics|team-c-uat"]
	if b.NamespaceCase != "B" || b.AppsAnyNamespace || b.Note == "" {
		t.Errorf("B (tek ns, tahmini hub ns + not): %+v", b)
	}
	b2 := got["team-d-uat-metrics|"]
	if b2.NamespaceCase != "B" || !b2.AppsAnyNamespace || b2.HubNamespace != "" || b2.ID != "team-d-uat-metrics" || b2.Note == "" {
		t.Errorf("B (çok ns → hub ns boş, id işten): %+v", b2)
	}
}

// Aynı iş adı birden çok instance namespace'inde (ArgoCD CR'leri hep
// "argocd" adlı → iş "argocd-metrics"): (iş, ns) başına AYRI aday.
func TestBuildCandidatesSharedJobName(t *testing.T) {
	probes := []JobProbe{{
		Job:        "argocd-metrics",
		Namespaces: []string{"team-b", "team-a"},
		Exported:   map[string][]string{"team-a": {"team-a"}, "team-b": {"team-b"}},
	}}
	cs := BuildCandidates("c-hub-a", probes, nil)
	if len(cs) != 2 || cs[0].HubNamespace != "team-a" || cs[1].HubNamespace != "team-b" {
		t.Fatalf("(iş, ns) başına sıralı aday: %+v", cs)
	}
	if cs[0].ID == cs[1].ID {
		t.Fatalf("kimlikler tekil olmalı: %+v", cs)
	}
}

func TestBuildCandidatesMatchesConfigured(t *testing.T) {
	existing := []Instance{
		{ID: "prod-a", HubClusterID: "c-hub-a", HubNamespace: "team-a-prod", MetricsJob: "team-a-prod-metrics"},
		// ns eşleşir, iş kayıtta boş → yine aynı instance
		{ID: "legacy", HubClusterID: "c-hub-a", HubNamespace: "team-b-prod"},
		// yalnız kimlik çakışması (başka ns) → aday yeni kimlik alır
		{ID: "team-c-uat", HubClusterID: "c-hub-a", HubNamespace: "somewhere-else"},
	}
	probes := []JobProbe{
		{Job: "team-a-prod-metrics", Namespaces: []string{"team-a-prod"}, Exported: map[string][]string{"team-a-prod": {"team-a-prod"}}},
		{Job: "team-b-prod-metrics", Namespaces: []string{"team-b-prod"}, Exported: map[string][]string{"team-b-prod": {"team-b-prod"}}},
		{Job: "team-c-uat-metrics", Namespaces: []string{"team-c-uat"}, Exported: map[string][]string{"team-c-uat": {"team-c-uat"}}},
	}
	got := byJobNS(BuildCandidates("c-hub-a", probes, existing))
	if a := got["team-a-prod-metrics|team-a-prod"]; a.ConfiguredID != "prod-a" || a.ID != "prod-a" {
		t.Errorf("kayıtlı instance eşlenmeli: %+v", a)
	}
	if b := got["team-b-prod-metrics|team-b-prod"]; b.ConfiguredID != "legacy" {
		t.Errorf("iş alanı boş kayıt ns ile eşlenir: %+v", b)
	}
	if c := got["team-c-uat-metrics|team-c-uat"]; c.ConfiguredID != "" || c.ID == "team-c-uat" || !strings.HasPrefix(c.ID, "team-c-uat") {
		t.Errorf("kimlik çakışması yeni önek-kimlik almalı: %+v", c)
	}
}

func TestBuildCandidatesErrorsAndTruncation(t *testing.T) {
	probes := []JobProbe{
		{Job: "Team_X Metrics", Err: "upstream unavailable"},
		{Job: "team-y-metrics", Namespaces: []string{"team-y"}, Exported: map[string][]string{"team-y": {"team-y"}}, Truncated: true},
		{Job: ""}, // boş iş adı atlanır
	}
	cs := BuildCandidates("c-hub-a", probes, nil)
	if len(cs) != 2 {
		t.Fatalf("hatalı iş aday olarak kalır, boş iş atlanır: %+v", cs)
	}
	m := byJobNS(cs)
	x := m["Team_X Metrics|"]
	if x.Error != "upstream unavailable" || x.ID != "team-x-metrics" {
		t.Errorf("hata adayı + slug kimlik: %+v", x)
	}
	if y := m["team-y-metrics|team-y"]; !strings.Contains(y.Note, "truncated") {
		t.Errorf("kesilme notu: %+v", y)
	}
}

func TestSuggestID(t *testing.T) {
	cases := map[string]string{
		"team-a-prod":           "team-a-prod",
		"Team_A.Prod":           "team-a-prod",
		"--x--":                 "x",
		"":                      "instance",
		"!!!":                   "instance",
		strings.Repeat("a", 80): strings.Repeat("a", 63),
	}
	for in, want := range cases {
		if got := suggestID(in); got != want {
			t.Errorf("suggestID(%q) = %q, beklenen %q", in, got, want)
		}
		if got := suggestID(in); !instanceIDRe.MatchString(got) {
			t.Errorf("suggestID(%q) = %q doğrulamadan geçmiyor", in, got)
		}
	}
}

// v0.10.957 — inceleme (§5.6 iki hub): keşif hub başına; aday probe edilen
// hub'ı taşır ve YALNIZ o hub'daki kayıtla eşlenir — öteki hub'daki aynı
// namespace ayrı bir instance'tır. Kimlik önerisi yine TÜM hub'lardaki
// kimliklerden kaçınır (id tüm hub'larda tekil).
func TestBuildCandidatesPerHub(t *testing.T) {
	existing := []Instance{{ID: "openshift-gitops", HubClusterID: "c-hub-a", HubNamespace: "openshift-gitops", MetricsJob: "openshift-gitops-metrics"}}
	probes := []JobProbe{{Job: "openshift-gitops-metrics", Namespaces: []string{"openshift-gitops"},
		Exported: map[string][]string{"openshift-gitops": {"openshift-gitops"}}}}

	onA := BuildCandidates("c-hub-a", probes, existing)
	if len(onA) != 1 || onA[0].ConfiguredID != "openshift-gitops" || onA[0].HubClusterID != "c-hub-a" {
		t.Fatalf("aynı hub'da kayıtlı instance eşlenmeli: %+v", onA)
	}
	onB := BuildCandidates("c-hub-b", probes, existing)
	if len(onB) != 1 || onB[0].ConfiguredID != "" || onB[0].HubClusterID != "c-hub-b" {
		t.Fatalf("öteki hub'daki aynı ns eşlenmemeli: %+v", onB)
	}
	if onB[0].ID == "openshift-gitops" || !instanceIDRe.MatchString(onB[0].ID) {
		t.Fatalf("kimlik tüm hub'larda tekil olmalı: %q", onB[0].ID)
	}
}
