package argocd

// discover_test.go — v0.10.957 — keşif adaylarının SAF türetimi (P1.4
// salt-okunur probe; audit §5.1–5.2, §11 H1.1/H1.2). Probe (api katmanı)
// hub'ın label-values API'sinden iş başına namespace / exported_namespace
// değerlerini toplar; aday kuralları burada: honorLabels durumu (A/B/C),
// paylaşılan iş adı, kayıtlı instance eşlemesi, kimlik önerisi.

import (
	"encoding/json"
	"strconv"
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

// ── v0.10.974 — keşifte uygulama / shard sayısı (BE2) ──────────────────────
//
// Onaylı mockup dipnotu + audit §5.4 kural 1–3: uygulama sayısı iş başına
// TEK anlık `count [by (namespace)] (group by (namespace, exported_namespace,
// name) (argocd_app_info{job="J"}))` (name label browser'ı YASAK, kural 4);
// shard sayısı sınırlı `pod` label-values (≤100). Saf parçalar: sorgu metni,
// JobWide işareti, iş başına plan (shard kapsamı), sonuç atama ve notlar.

func TestAppCountQuery(t *testing.T) {
	const grp = `group by (namespace, exported_namespace, name) `
	cases := []struct {
		job  string
		wide bool
		want string
	}{
		{"team-a-prod-metrics", false, `count by (namespace) (` + grp + `(argocd_app_info{job="team-a-prod-metrics"}))`},
		{"team-a-prod-metrics", true, `count(` + grp + `(argocd_app_info{job="team-a-prod-metrics"}))`},
		// AppInfoSelector ile AYNI kaçış (PromQL dize sabiti).
		{`we"ird\job`, true, `count(` + grp + `(argocd_app_info{job="we\"ird\\job"}))`},
		{"a\nb", false, `count by (namespace) (` + grp + `(argocd_app_info{job="a\nb"}))`},
	}
	for _, c := range cases {
		if got := AppCountQuery(c.job, c.wide); got != c.want {
			t.Errorf("AppCountQuery(%q, %v)\n got %s\nwant %s", c.job, c.wide, got, c.want)
		}
	}
}

// JobWide — iş genelinde exported_namespace YOK (durum B, iş düzeyi aday):
// namespace etiketi Application ns'idir, sayım iş genelinde count() ile.
func TestBuildCandidatesJobWide(t *testing.T) {
	probes := []JobProbe{
		{Job: "a-metrics", Namespaces: []string{"a"}, Exported: map[string][]string{"a": {"a"}}},        // A
		{Job: "b-metrics", Namespaces: []string{"b"}},                                                   // B tek ns
		{Job: "c-metrics", Namespaces: []string{"c-1", "c-2"}},                                          // B çok ns
		{Job: "d-metrics", Namespaces: []string{"d", "d-x"}, Exported: map[string][]string{"d": {"d"}}}, // d-x: ns düzeyi B
		{Job: "e-metrics", Err: "unavailable: down"},                                                    // hata
	}
	got := byJobNS(BuildCandidates("c-hub-a", probes, nil))
	want := map[string]bool{"a-metrics|a": false, "b-metrics|b": true, "c-metrics|": true, "d-metrics|d": false, "d-metrics|d-x": false, "e-metrics|": false}
	if len(got) != len(want) {
		t.Fatalf("adaylar: %+v", got)
	}
	for k, w := range want {
		if c, ok := got[k]; !ok || c.JobWide != w {
			t.Errorf("%s JobWide=%v, beklenen %v (%+v)", k, c.JobWide, w, c)
		}
	}
	raw, _ := json.Marshal(got["b-metrics|b"])
	if strings.Contains(strings.ToLower(string(raw)), "jobwide") {
		t.Fatalf("JobWide yalnız sunucu içi (json:\"-\"): %s", raw)
	}
}

// PlanCounts — hatasız adaylar iş başına ADAY SIRASIYLA gruplanır; shard
// kapsamı tek adaylı ya da JobWide işte (iş, ""), paylaşılan işte (iş, ns).
// Hatalı aday hiç sayım çağrısı almaz.
func TestPlanCounts(t *testing.T) {
	cands := BuildCandidates("c-hub-a", []JobProbe{
		{Job: "argocd-metrics", Namespaces: []string{"team-b", "team-a"}, Exported: map[string][]string{"team-a": {"team-a"}, "team-b": {"team-b"}}},
		{Job: "team-c-metrics", Namespaces: []string{"team-c"}, Exported: map[string][]string{"team-c": {"team-c-apps"}}},
		{Job: "team-d-metrics", Namespaces: []string{"d-1", "d-2"}},
		{Job: "team-x-metrics", Err: "internal: boom"},
	}, nil)
	// sıra: argocd-metrics|team-a, argocd-metrics|team-b, team-c, team-d, team-x(hata)
	plan := PlanCounts(cands)
	type sc struct{ job, ns, idx string }
	type row struct {
		job  string
		wide bool
		idx  string
		sh   []sc
	}
	want := []row{
		{"argocd-metrics", false, "0,1", []sc{{"argocd-metrics", "team-a", "0"}, {"argocd-metrics", "team-b", "1"}}},
		{"team-c-metrics", false, "2", []sc{{"team-c-metrics", "", "2"}}},
		{"team-d-metrics", true, "3", []sc{{"team-d-metrics", "", "3"}}},
	}
	ints := func(xs []int) string {
		var s []string
		for _, x := range xs {
			s = append(s, strconv.Itoa(x))
		}
		return strings.Join(s, ",")
	}
	if len(plan) != len(want) {
		t.Fatalf("plan %+v", plan)
	}
	for i, w := range want {
		p := plan[i]
		if p.Job != w.job || p.JobWide != w.wide || ints(p.Idx) != w.idx || len(p.Shards) != len(w.sh) {
			t.Fatalf("plan[%d] = %+v, beklenen %+v", i, p, w)
		}
		for k, s := range w.sh {
			if g := p.Shards[k]; g.Job != s.job || g.Namespace != s.ns || ints(g.Idx) != s.idx {
				t.Errorf("plan[%d].Shards[%d] = %+v, beklenen %+v", i, k, g, s)
			}
		}
	}
	if len(PlanCounts(nil)) != 0 {
		t.Fatal("boş aday → boş plan")
	}
}

func TestParseCountVector(t *testing.T) {
	m, err := ParseCountVector(json.RawMessage(`[{"metric":{"namespace":"team-a"},"value":[1700000000.5,"1184"]},` +
		`{"metric":{},"value":[1700000000.5,"57"]},{"metric":{"namespace":"team-b"},"value":[1700000000.5,3]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if m["team-a"] != "1184" || m[""] != "57" {
		t.Fatalf("namespace → değer: %v", m)
	}
	if v, ok := m["team-b"]; !ok || v != "" {
		t.Fatalf("dize olmayan değer boş dizeye iner (tamsayı değil notu): %v", m)
	}
	if m, err := ParseCountVector(json.RawMessage(`[]`)); err != nil || len(m) != 0 {
		t.Fatalf("boş vektör: %v %v", m, err)
	}
	for _, bad := range []string{`{`, `{"a":1}`, `[1,2]`} {
		if _, err := ParseCountVector(json.RawMessage(bad)); err == nil {
			t.Errorf("%s hata vermeli", bad)
		}
	}
}

func TestAppCountFor(t *testing.T) {
	byNS := map[string]string{"team-a": "1184", "team-z": "0", "team-f": "1.5", "team-n": "NaN", "team-m": "-1",
		"team-i": "+Inf", "team-e": "1e3", "team-s": "", "": "57"}
	cases := []struct {
		name  string
		ns    string
		wide  bool
		trunc bool
		want  int // -1 = nil
		note  string
	}{
		{"tamsayı", "team-a", false, false, 1184, ""},
		{"sıfır geçerli", "team-z", false, false, 0, ""},
		{"üslü tamsayı", "team-e", false, false, 1000, ""},
		{"iş geneli count() → boş anahtar", "", true, false, 57, ""},
		{"iş geneli: aday ns'i yok sayılır", "b-ns", true, false, 57, ""},
		{"sonuçta yok", "team-q", false, false, -1, "anlık sorguda seri yok"},
		{"kesik sonuçta yok", "team-q", false, true, -1, "uygulama sayısı okunamadı: sonuç seri tavanında kesildi"},
		{"kesirli", "team-f", false, false, -1, `uygulama sayısı okunamadı: tamsayı değil ("1.5")`},
		{"NaN", "team-n", false, false, -1, `uygulama sayısı okunamadı: tamsayı değil ("NaN")`},
		{"negatif", "team-m", false, false, -1, `uygulama sayısı okunamadı: tamsayı değil ("-1")`},
		{"sonsuz", "team-i", false, false, -1, `uygulama sayısı okunamadı: tamsayı değil ("+Inf")`},
		{"dize değil", "team-s", false, false, -1, `uygulama sayısı okunamadı: tamsayı değil ("")`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, note := AppCountFor(Candidate{HubNamespace: c.ns}, c.wide, byNS, c.trunc)
			switch {
			case c.want < 0 && n != nil:
				t.Fatalf("nil beklenirdi, %d", *n)
			case c.want >= 0 && (n == nil || *n != c.want):
				t.Fatalf("%v, beklenen %d", n, c.want)
			}
			if note != c.note {
				t.Fatalf("not %q, beklenen %q", note, c.note)
			}
		})
	}
}

func TestApplyCounts(t *testing.T) {
	cands := []Candidate{{MetricsJob: "j", HubNamespace: "team-a"}, {MetricsJob: "j", HubNamespace: "team-b"}}
	j := CountJob{Job: "j", Idx: []int{0, 1}, Shards: []ShardScope{{Job: "j", Namespace: "team-a", Idx: []int{0}}, {Job: "j", Namespace: "team-b", Idx: []int{1}}}}
	ApplyAppCounts(cands, j, map[string]string{"team-a": "12"}, false)
	ApplyShardCount(cands, j.Shards[0], 3, false)
	ApplyShardCount(cands, j.Shards[1], 0, false)
	a, b := cands[0], cands[1]
	if a.AppCount == nil || *a.AppCount != 12 || a.ShardCount == nil || *a.ShardCount != 3 || a.ShardCountTruncated || a.CountNote != "" {
		t.Errorf("a: %+v", a)
	}
	if b.AppCount != nil || b.ShardCount != nil || b.CountNote != "anlık sorguda seri yok; pod etiketi yok" {
		t.Errorf("b (iki not birleşir): %+v", b)
	}
	// ≥100 pod: ShardCount=100 alt sınır + işaret, not yok.
	c := []Candidate{{MetricsJob: "j"}}
	ApplyShardCount(c, ShardScope{Job: "j", Idx: []int{0}}, 100, true)
	if c[0].ShardCount == nil || *c[0].ShardCount != 100 || !c[0].ShardCountTruncated || c[0].CountNote != "" {
		t.Errorf("kesik pod listesi: %+v", c[0])
	}
	// Not tekilleşir (bütçe notu iki kez yazılsa da bir kez görünür).
	NoteCounts(c, []int{0}, CountNoteBudget)
	NoteCounts(c, []int{0}, CountNoteBudget)
	if c[0].CountNote != CountNoteBudget || CountNoteBudget != "sayım atlandı: keşif bütçesi doldu" {
		t.Errorf("tekil not: %q", c[0].CountNote)
	}
	raw, _ := json.Marshal(Candidate{ID: "x"})
	for _, k := range []string{"appCount", "shardCount", "shardCountTruncated", "countNote"} {
		if strings.Contains(string(raw), k) {
			t.Errorf("sayısız aday %q taşımamalı (omitempty): %s", k, raw)
		}
	}
	if got := AppCountFailNote("timeout"); got != "uygulama sayısı okunamadı: timeout" {
		t.Errorf("AppCountFailNote: %q", got)
	}
	if got := ShardCountFailNote("unavailable"); got != "shard sayısı okunamadı: unavailable" {
		t.Errorf("ShardCountFailNote: %q", got)
	}
}
