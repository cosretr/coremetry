package v2probe

// catalog_test.go — v0.10.979 — katalog sözleşmesi: kimlikler tekil, hiçbir
// şablon küme matcher'ı taşımaz, yer tutucu kümesi kapalı, tam parametrede
// `{{` kalmaz, boş env/suffix yalnız onları isteyen satırlarda
// ErrPlaceholderEmpty, paket sayımları çivili, Informs V-referansları
// V1..V14 içinde, label API satırlarının penceresi > 0 ve KindSeries yok,
// ns/pair/inst tekrarları beklendiği gibi açılır, {{RE}} ters tırnaklı ve
// QuoteMeta'lı, H2.5 regex'i sabit.

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func fullParams() Params {
	return Params{
		EnvList: []string{"dev", "test", "prod"}, SuffixList: []string{"ocpa", "ocp.b"},
		PairGroups: map[string][]string{"pair-1": {"ocpa", "ocp.b"}},
		NSMatcher:  `,namespace=~"team-.*"`, InstanceNS: []string{"team-a-prod", "team-b-dev"},
		K5Window: "1h", WithoutMatcher: true,
	}
}

func TestCatalogIDsUniqueAndNoMatcherLiteral(t *testing.T) {
	seen := map[string]bool{}
	for _, q := range Catalogue {
		if seen[q.ID] {
			t.Errorf("çift kimlik %s", q.ID)
		}
		seen[q.ID] = true
		for _, bad := range []string{`<L>=`, `="<V>"`, `{{L}}`, `cluster="`} {
			if strings.Contains(q.Expr, bad) {
				t.Errorf("%s şablonu matcher taşıyor: %s", q.ID, q.Expr)
			}
		}
		if q.Kind == KindSeries {
			t.Errorf("%s KindSeries — §11.0 ad/repo/dest_server gezilmez", q.ID)
		}
		if (q.Kind == KindLabels || q.Kind == KindLabelValues) && q.Window <= 0 {
			t.Errorf("%s label API satırı penceresiz", q.ID)
		}
		if q.Kind == KindLabelValues && q.Label != "__name__" {
			t.Errorf("%s label values etiketi __name__ olmalı", q.ID)
		}
		if q.Expect == "" {
			t.Errorf("%s Expect boş", q.ID)
		}
		if q.Shape == ShapeRows && q.MaxRows <= 0 {
			t.Errorf("%s satır biçimli ama MaxRows yok", q.ID)
		}
		if q.MaxRows > 100 {
			t.Errorf("%s MaxRows %d > 100", q.ID, q.MaxRows)
		}
	}
}

func TestCatalogPlaceholdersClosed(t *testing.T) {
	re := regexp.MustCompile(`\{\{[^}]*\}\}`)
	allowed := map[string]bool{}
	for _, p := range Placeholders {
		allowed[p] = true
	}
	for _, q := range Catalogue {
		for _, m := range re.FindAllString(q.Expr, -1) {
			if !allowed[m] {
				t.Errorf("%s bilinmeyen yer tutucu %s", q.ID, m)
			}
		}
		insts, err := Expand(q, fullParams())
		if err != nil {
			t.Fatalf("%s Expand: %v", q.ID, err)
		}
		for _, in := range insts {
			if strings.Contains(in.Expr, "{{") {
				t.Errorf("%s[%s] tam parametrede yer tutucu kaldı: %s", q.ID, in.Variant, in.Expr)
			}
		}
	}
}

func TestCatalogPackCounts(t *testing.T) {
	want := map[Pack]int{PackT: 3, PackK: 55, PackD: 8, PackR: 4, PackH: 57, PackN: 12}
	for p, n := range want {
		if got := len(ByPack(p)); got != n {
			t.Errorf("paket %s: %d satır, çivi %d", p, got, n)
		}
	}
	nomatch := 0
	for _, q := range Catalogue {
		if q.Repeat&RepeatNoMatcher != 0 {
			nomatch++
		}
	}
	if nomatch != 14 {
		t.Errorf("nomatch satırı %d, çivi 14 (H0.1–H0.5 + H1.1–H1.6c)", nomatch)
	}
}

func TestCatalogInformsReferences(t *testing.T) {
	vre := regexp.MustCompile(`^V(\d+)b?$`)
	for _, q := range Catalogue {
		for _, ref := range q.Informs {
			if m := vre.FindStringSubmatch(ref); m != nil {
				n := 0
				for _, c := range m[1] {
					n = n*10 + int(c-'0')
				}
				if n < 1 || n > 14 {
					t.Errorf("%s Informs %s: V1..V14 dışı", q.ID, ref)
				}
			}
		}
	}
}

func TestExpandEmptyListsOnlyForNamingRows(t *testing.T) {
	p := fullParams()
	p.EnvList, p.SuffixList = nil, nil
	needs := map[string]bool{}
	for _, q := range Catalogue {
		if strings.Contains(q.Expr, "{{env}}") || strings.Contains(q.Expr, "{{sfx}}") || strings.Contains(q.Expr, "{{RE}}") {
			needs[q.ID] = true
		}
	}
	if !needs["N1"] || !needs["H3.4"] || needs["H3.5"] || needs["K0.1"] {
		t.Fatalf("beklenen ihtiyaç kümesi değil: %v", needs)
	}
	for _, q := range Catalogue {
		_, err := Expand(q, p)
		if needs[q.ID] && !errors.Is(err, ErrPlaceholderEmpty) {
			t.Errorf("%s boş listede ErrPlaceholderEmpty vermeli, %v", q.ID, err)
		}
		if !needs[q.ID] && err != nil {
			t.Errorf("%s boş listeden etkilenmemeli: %v", q.ID, err)
		}
	}
}

func TestExpandVariants(t *testing.T) {
	p := fullParams()
	// ns: yalnız NSMatcher doluyken ikinci instance; K3.1 içeride, K3.2 seçici.
	k31, _ := Find("K3.1")
	insts, _ := Expand(k31, p)
	if len(insts) != 2 || insts[1].Variant != "ns" || !strings.Contains(insts[1].Expr, `owner_kind="Deployment",namespace=~"team-.*"}`) || strings.Contains(insts[0].Expr, "namespace") {
		t.Fatalf("K3.1 ns açılımı: %+v", insts)
	}
	k32, _ := Find("K3.2")
	insts, _ = Expand(k32, p)
	if len(insts) != 2 || insts[0].Expr != `count(kube_replicaset_spec_replicas)` || insts[1].Expr != `count(kube_replicaset_spec_replicas{namespace=~"team-.*"})` {
		t.Fatalf("K3.2 ns açılımı: %+v", insts)
	}
	p2 := p
	p2.NSMatcher = ""
	if insts, _ = Expand(k31, p2); len(insts) != 1 {
		t.Fatalf("filtre yokken tek instance: %+v", insts)
	}
	// dedup_off + nomatch (H0.2), nomatch yalnız WithoutMatcher'da.
	h02, _ := Find("H0.2")
	insts, _ = Expand(h02, p)
	vs := []string{}
	for _, in := range insts {
		vs = append(vs, in.Variant)
	}
	if strings.Join(vs, ",") != ",nomatch,dedup_off" {
		t.Fatalf("H0.2 varyantları: %v", vs)
	}
	p2.WithoutMatcher = false
	insts, _ = Expand(h02, p2)
	if len(insts) != 2 || insts[1].Variant != "dedup_off" {
		t.Fatalf("WithoutMatcher=false: %+v", insts)
	}
	// second_sample (K0.6b).
	k06b, _ := Find("K0.6b")
	if insts, _ = Expand(k06b, p); len(insts) != 2 || insts[1].Variant != "second_sample" {
		t.Fatalf("K0.6b: %+v", insts)
	}
	// pair: grup başına bir; suffix'ler QuoteMeta'lı.
	h35, _ := Find("H3.5")
	insts, _ = Expand(h35, p)
	if len(insts) != 1 || insts[0].Variant != "pair:pair-1" || !strings.Contains(insts[0].Expr, "`.+-(ocpa|ocp\\.b)`") {
		t.Fatalf("H3.5 pair: %+v", insts)
	}
	p3 := p
	p3.PairGroups = map[string][]string{"pair-1": {"only"}}
	if insts, _ = Expand(h35, p3); len(insts) != 0 {
		t.Fatalf("tek suffix'li grup instance üretmemeli: %+v", insts)
	}
	// inst: instance başına; ham ns VariantRaw'da; yoksa seçicisiz tek.
	h62b, _ := Find("H6.2b")
	insts, _ = Expand(h62b, p)
	if len(insts) != 2 || insts[0].Variant != "inst" || insts[0].VariantRaw != "team-a-prod" || !strings.Contains(insts[0].Expr, `argocd_app_sync_total{namespace="team-a-prod"}[24h]`) {
		t.Fatalf("H6.2b inst: %+v", insts)
	}
	p3.InstanceNS = nil
	if insts, _ = Expand(h62b, p3); len(insts) != 1 || insts[0].Expr != `sum by (phase) (increase(argocd_app_sync_total[24h]))` {
		t.Fatalf("H6.2b instance'sız: %+v", insts)
	}
	// k5w.
	k51, _ := Find("K5.1")
	insts, _ = Expand(k51, p)
	if insts[0].Expr != `sum(changes(kube_deployment_metadata_generation[1h]))` {
		t.Fatalf("k5w: %s", insts[0].Expr)
	}
	p3.K5Window = ""
	insts, _ = Expand(k51, p3)
	if insts[0].Expr != `sum(changes(kube_deployment_metadata_generation[6h]))` {
		t.Fatalf("k5w varsayılan: %s", insts[0].Expr)
	}
	// SQL/API satırları aynen.
	t1, _ := Find("T1")
	if insts, _ = Expand(t1, Params{}); len(insts) != 1 || insts[0].Expr != t1.Expr {
		t.Fatalf("T1: %+v", insts)
	}
}

func TestExpandREAndRegexLiterals(t *testing.T) {
	p := fullParams()
	n1, _ := Find("N1")
	insts, _ := Expand(n1, p)
	want := "name=~`[^-]+-[^-]+-.+-(dev|test|prod)-(ocpa|ocp\\.b)`"
	if !strings.Contains(insts[0].Expr, want) {
		t.Fatalf("RE: %s", insts[0].Expr)
	}
	if strings.Contains(insts[0].Expr, `"[^-]+`) {
		t.Fatal("RE çift tırnaklı olmamalı (QuoteMeta ters bölüsü)")
	}
	h25, _ := Find("H2.5")
	if !strings.Contains(h25.Expr, "`https?://[^/]+:([0-9]+)/?`") {
		t.Fatalf("H2.5 regex: %s", h25.Expr)
	}
	if _, err := regexp.Compile(`[^-]+-[^-]+-.+-(dev|test|prod)-(ocpa|ocp\.b)`); err != nil {
		t.Fatal(err)
	}
}

// TestCatalogTokeniseNotLiteral — v0.10.979 — jetonlanacak diye belgelenen
// hiçbir etiket ne küresel allowlist'te ne satırın LiteralFor'unda olabilir
// (olay: H0.3/H0.5 k8s_cluster küresel T sayacı olarak ham basıldı).
func TestCatalogTokeniseNotLiteral(t *testing.T) {
	for _, q := range Catalogue {
		lit := map[string]bool{}
		for _, l := range q.LiteralFor {
			lit[l] = true
		}
		for _, l := range q.Tokenise {
			if LiteralLabels[l] {
				t.Errorf("%s: Tokenise %q küresel LiteralLabels'ta", q.ID, l)
			}
			if lit[l] {
				t.Errorf("%s: Tokenise %q satırın LiteralFor'unda", q.ID, l)
			}
		}
	}
	t1, _ := Find("T1")
	for _, l := range []string{"sampled", "depl", "k8s_cluster", "ocp_cluster"} {
		found := false
		for _, x := range t1.LiteralFor {
			found = found || x == l
		}
		if !found {
			t.Errorf("T1 LiteralFor %q eksik", l)
		}
	}
}

// TestCatalogL2InnerGroupCarriesProject — v0.10.979 — iç group by `project`i
// taşımazsa dış count by (namespace, project) projesiz ns sayımına çöker.
func TestCatalogL2InnerGroupCarriesProject(t *testing.T) {
	q, ok := Find("L2")
	if !ok {
		t.Fatal("L2 yok")
	}
	if !strings.HasPrefix(q.Expr, "count by (namespace, project) (") {
		t.Errorf("L2 dış toplama: %s", q.Expr)
	}
	if !strings.Contains(q.Expr, "group by (namespace, exported_namespace, name, project)") {
		t.Errorf("L2 iç group by project taşımalı: %s", q.Expr)
	}
}

func TestCatalogScopeAndFallback(t *testing.T) {
	for _, q := range Catalogue {
		switch q.Pack {
		case PackK, PackD:
			if q.Scope != ScopeTarget {
				t.Errorf("%s hedef kapsamlı olmalı", q.ID)
			}
		case PackR:
			if q.Scope != ScopeTargetAndHub {
				t.Errorf("%s hedef+hub olmalı", q.ID)
			}
		case PackH, PackN:
			if q.Scope != ScopeHub {
				t.Errorf("%s hub kapsamlı olmalı", q.ID)
			}
		case PackT:
			if q.Scope != ScopeClickHouse {
				t.Errorf("%s ClickHouse kapsamlı olmalı", q.ID)
			}
		}
		if q.FallbackFor != "" {
			if _, ok := Find(q.FallbackFor); !ok {
				t.Errorf("%s FallbackFor %s yok", q.ID, q.FallbackFor)
			}
			if q.Shape != ShapeNames {
				t.Errorf("%s fallback yalnız etiket adı taşımalı", q.ID)
			}
		}
	}
	r0, _ := Find("R0")
	if !r0.Runs("target") || !r0.Runs("hub") || r0.Runs("clickhouse") {
		t.Fatal("R0 Runs")
	}
}
