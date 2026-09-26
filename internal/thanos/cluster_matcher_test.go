package thanos

import (
	"strings"
	"testing"
)

// v0.10.128 — cluster matcher enjeksiyonu (design §1.1): TEK Thanos
// Querier'ın önünde N cluster varken her sorgu `<label>="<value>"`
// taşımalı; aksi hâlde pod/node/deployment tabloları cluster'ları
// karıştırır (keşif raporu engel #1). Şablon başına elle matcher
// eklemek yerine ifade düzeyinde enjeksiyon: her vektör seçicisine
// (süslü parantezli ya da çıplak metrik adı) matcher eklenir.
//
// Sözleşme: label boşsa ifade DEĞİŞMEZ (eski davranış). Fonksiyon adları,
// by/without/on/ignoring/group_* etiket listeleri, dize sabitleri,
// süreler ([5m]) ve sayılar metrik sanılmaz.

func TestWithClusterMatcherGolden(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"boş label → değişmez", `up`, `up`},
		{"çıplak metrik", `kube_node_info`, `kube_node_info{cluster="p1"}`},
		{"süslü parantez", `kube_pod_owner{owner_kind="ReplicaSet",pod!=""}`, `kube_pod_owner{cluster="p1",owner_kind="ReplicaSet",pod!=""}`},
		{"boş süslü parantez", `{__name__=~"(jvm|jboss)_.*"}`, `{cluster="p1",__name__=~"(jvm|jboss)_.*"}`},
		{"rate + süre", `sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{container!=""}[5m]))`,
			`sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{cluster="p1",container!=""}[5m]))`},
		{"çıplak metrik + süre", `rate(node_network_receive_bytes_total[5m])`, `rate(node_network_receive_bytes_total{cluster="p1"}[5m])`},
		{"by listesi metrik değil", `topk(500, sum by (instance) (node_memory_MemTotal_bytes))`,
			`topk(500, sum by (instance) (node_memory_MemTotal_bytes{cluster="p1"}))`},
		{"aritmetik iki seçici", `sum(node_memory_MemTotal_bytes) - sum(node_memory_MemAvailable_bytes)`,
			`sum(node_memory_MemTotal_bytes{cluster="p1"}) - sum(node_memory_MemAvailable_bytes{cluster="p1"})`},
		{"karşılaştırma + sayı", `max by (namespace, pod, reason) (kube_pod_container_status_last_terminated_reason{pod!=""} == 1)`,
			`max by (namespace, pod, reason) (kube_pod_container_status_last_terminated_reason{cluster="p1",pod!=""} == 1)`},
		{"time() fonksiyonu", `time() - ALERTS_FOR_STATE{alertstate="firing"}`, `time() - ALERTS_FOR_STATE{cluster="p1",alertstate="firing"}`},
		{"on/group_left etiket listeleri", `a * on (namespace, pod) group_left(node) kube_pod_info`,
			`a{cluster="p1"} * on (namespace, pod) group_left(node) kube_pod_info{cluster="p1"}`},
		{"dize içindeki süslü parantez", `x{foo="{bar}"}`, `x{cluster="p1",foo="{bar}"}`},
		{"kaçışlı tırnak", `x{re=~"a\"b"}`, `x{cluster="p1",re=~"a\"b"}`},
		{"offset", `x offset 5m`, `x{cluster="p1"} offset 5m`},
		{"etiket değeri kaçışlanır", `x`, `x{cluster="p\"1"}`},
		{"bool karşılaştırma", `x > bool 0`, `x{cluster="p1"} > bool 0`},
		{"and/or/unless", `a and b or c unless d`, `a{cluster="p1"} and b{cluster="p1"} or c{cluster="p1"} unless d{cluster="p1"}`},
		{"count by (__name__)", `count by (__name__) ({__name__=~"jvm_.*",namespace="n"})`,
			`count by (__name__) ({cluster="p1",__name__=~"jvm_.*",namespace="n"})`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			label, value := "cluster", "p1"
			if c.name == "boş label → değişmez" {
				label = ""
			}
			if c.name == "etiket değeri kaçışlanır" {
				value = `p"1`
			}
			got := withClusterMatcher(c.in, label, value)
			if got != c.want {
				t.Fatalf("\n girdi: %s\n alınan: %s\n beklenen: %s", c.in, got, c.want)
			}
		})
	}
}

// Her PromQL şablonu enjeksiyondan geçince çıplak metrik kalmamalı ve
// her seçici matcher taşımalı. Şablon listesi promql.go'nun tamamı —
// yeni şablon eklenince buraya da eklenir (bareSelectorCount kapısı
// eksik olanı yakalar).
func TestEveryPromQLTemplateGetsClusterMatcher(t *testing.T) {
	templates := map[string]string{
		"podCPUQuery":               podCPUQuery("ns.*", ""),
		"podMemQuery":               podMemQuery("", "api-.*"),
		"podLimitQuery":             podLimitQuery("cpu", "ns", ""),
		"podRequestQuery":           podRequestQuery("memory", "", ""),
		"singlePodCPUQuery":         singlePodCPUQuery("ns", "pod"),
		"singlePodMemQuery":         singlePodMemQuery("ns", "pod"),
		"singleNamespaceCPUQuery":   singleNamespaceCPUQuery("ns"),
		"singleNamespaceMemQuery":   singleNamespaceMemQuery("ns"),
		"nodeCPUQuery":              nodeCPUQuery(),
		"nodeMemTotalQuery":         nodeMemTotalQuery(),
		"nodeMemAvailQuery":         nodeMemAvailQuery(),
		"nodeCPUCountQuery":         nodeCPUCountQuery(),
		"nodeInfoQuery":             nodeInfoQuery,
		"summaryNodeCountQuery":     summaryNodeCountQuery,
		"summaryPodCountQuery":      summaryPodCountQuery("ns"),
		"summaryCPUUsedQuery":       summaryCPUUsedQuery,
		"summaryMemUsedQuery":       summaryMemUsedQuery,
		"podPhaseQuery":             podPhaseQuery("", ""),
		"podRestartsQuery":          podRestartsQuery("ns", ""),
		"podLastTermQuery":          podLastTermQuery("", ""),
		"nodeRoleQuery":             nodeRoleQuery,
		"nsRestartsQuery":           nsRestartsQuery(""),
		"nsFailingQuery":            nsFailingQuery(""),
		"summaryCPUCapacityQuery":   summaryCPUCapacityQuery,
		"summaryMemCapacityQuery":   summaryMemCapacityQuery,
		"summaryPodPhaseQuery":      summaryPodPhaseQuery("Running", ""),
		"summaryAlertCountQuery":    summaryAlertCountQuery("critical"),
		"nsCPUQuery":                nsCPUQuery(""),
		"nsMemQuery":                nsMemQuery(""),
		"nsPodCountQuery":           nsPodCountQuery(""),
		"resourceTrendQuery/node":   resourceTrendQuery("cpu", true),
		"resourceTrendQuery/all":    resourceTrendQuery("memory", false),
		"nsPodsCPUTrendQuery":       nsPodsCPUTrendQuery("ns"),
		"nsPodsMemTrendQuery":       nsPodsMemTrendQuery("ns"),
		"podNetQuery":               podNetQuery("receive", "", ""),
		"nodeNetQuery":              nodeNetQuery("transmit"),
		"summaryNetQuery":           summaryNetQuery("receive"),
		"nsPodOwnerQuery":           nsPodOwnerQuery("ns"),
		"nsReplicaSetOwnerQuery":    nsReplicaSetOwnerQuery("ns"),
		"nsDeployDesiredQuery":      nsDeployDesiredQuery("ns"),
		"nsDeployReadyQuery":        nsDeployReadyQuery("ns"),
		"nsDeployAvailFalseQuery":   nsDeployAvailFalseQuery("ns"),
		"deployTrendQuery/byPod":    deployTrendQuery("ns", "dep", "cpu", true),
		"deployTrendQuery/sum":      deployTrendQuery("ns", "dep", "memory", false),
		"haproxyTrendQuery/rps":     haproxyTrendQuery("ns", "5xx"),
		"haproxyTrendQuery/latency": haproxyTrendQuery("ns", "latency"),
		"jmxDiscoveryQuery":         jmxDiscoveryQuery("ns", "dep"),
		"jmxTrendQuery":             jmxTrendQuery("ns", "dep", "jvm_memory_used_bytes", true, "pod-1"),
	}
	for name, expr := range templates {
		t.Run(name, func(t *testing.T) {
			if expr == "" {
				t.Skip("şablon bu argümanlarla boş dönüyor")
			}
			got := withClusterMatcher(expr, "cluster", "p1")
			if n := bareSelectorCount(got); n != 0 {
				t.Fatalf("enjeksiyondan sonra %d çıplak metrik kaldı:\n%s\n→ %s", n, expr, got)
			}
			// Her süslü parantez grubu matcher taşır.
			if strings.Count(got, `cluster="p1"`) != strings.Count(got, "{") {
				t.Fatalf("her seçici matcher taşımalı (%d matcher / %d seçici):\n%s", strings.Count(got, `cluster="p1"`), strings.Count(got, "{"), got)
			}
			// Enjeksiyon idempotent değil ama tekrar edilmemeli — yeniden
			// koşum ikinci matcher eklerse çağıran çift geçirmiş demektir.
			if withClusterMatcher(got, "cluster", "p1") == got {
				t.Fatal("ikinci geçiş aynı çıktıyı verdi — enjeksiyon hiç olmamış gibi")
			}
		})
	}
}

// v0.10.950 — PromQL konsolu (spec karar 6): ham KULLANICI PromQL'i de bu
// tokenizer'dan geçer. Phase 0 audit §2 bypass'ı: `#` yorumu tanınmıyordu;
// yorumdaki kesme işareti (`# don't`) dize sanılıp sorgunun kalanı
// enjeksiyonsuz kopyalanıyordu. Golden kapsam: yorumlar (kesme işaretli
// dahil), subquery, @, offset, UTF-8 tırnaklı ad, absent(), ikili
// operatörler, agregatlar, ham dize + ters bölü, anahtar kelime adlı metrik
// (başarısız-kapalı). Her çıktıda çıplak seçici 0 (bareSelectorCount).
func TestWithClusterMatcherConsoleGolden(t *testing.T) {
	cases := []struct {
		name, in, want string
		label, value   string // boş → cluster / p1
	}{
		// ── yorumlar ──
		{name: "satır sonu yorumu", in: `up # don't`, want: `up{cluster="p1"}`},
		{name: "yorumdaki kesme işareti bypass değil", in: "up # don't\n+ secret_metric",
			want: "up{cluster=\"p1\"}\n+ secret_metric{cluster=\"p1\"}"},
		{name: "yorumdaki tırnak ve süslü", in: "up # \"} or secret\n+ other",
			want: "up{cluster=\"p1\"}\n+ other{cluster=\"p1\"}"},
		{name: "kendi satırındaki yorum satırıyla gider", in: "# başlık: it's CPU\nsum(x)\n# son söz",
			want: `sum(x{cluster="p1"})`},
		{name: "süslü parantez içinde yorum", in: "x{a=\"b\", # it's }\nc=\"d\"}",
			want: "x{cluster=\"p1\",a=\"b\",\nc=\"d\"}"},
		{name: "dize içindeki # yorum değil", in: `x{a="#b"} # c`, want: `x{cluster="p1",a="#b"}`},
		{name: `\r yorumu bitirir (Prometheus isEndOfLine)`, in: "up # c\r+ y",
			want: "up{cluster=\"p1\"}\r+ y{cluster=\"p1\"}"},
		{name: "yalnız yorum", in: "# sadece yorum", want: ""},
		{name: "çağrı adıyla parantez arasında yorum", in: "rate # it's\n(x[5m])",
			want: "rate\n(x{cluster=\"p1\"}[5m])"},
		// ── ham dize (backtick): `\` kaçış DEĞİL ──
		{name: "ters bölüyle biten ham dize", in: "label_replace(x, \"a\", `C:\\`, \"b\", \"c\") or secret",
			want: "label_replace(x{cluster=\"p1\"}, \"a\", `C:\\`, \"b\", \"c\") or secret{cluster=\"p1\"}"},
		// ── subquery ──
		{name: "subquery", in: `max_over_time(rate(x[5m])[1h:1m])`, want: `max_over_time(rate(x{cluster="p1"}[5m])[1h:1m])`},
		{name: "subquery + offset", in: `rate(x[5m])[30m:] offset 1h`, want: `rate(x{cluster="p1"}[5m])[30m:] offset 1h`},
		// ── @ ──
		{name: "@ zaman damgası", in: `x @ 1609746000`, want: `x{cluster="p1"} @ 1609746000`},
		{name: "@ start()", in: `rate(x[5m] @ start())`, want: `rate(x{cluster="p1"}[5m] @ start())`},
		{name: "@ end() + offset", in: `x @ end() offset 5m`, want: `x{cluster="p1"} @ end() offset 5m`},
		// ── offset ──
		{name: "negatif offset", in: `x offset -5m`, want: `x{cluster="p1"} offset -5m`},
		{name: "agregat içinde offset", in: `sum(x offset 1h) - sum(x)`, want: `sum(x{cluster="p1"} offset 1h) - sum(x{cluster="p1"})`},
		{name: "aralık + offset", in: `rate(x[5m] offset 1w)`, want: `rate(x{cluster="p1"}[5m] offset 1w)`},
		// ── UTF-8 tırnaklı adlar (Prometheus 3) ──
		{name: "tırnaklı metrik adı", in: `{"http.server.duration"}`, want: `{cluster="p1","http.server.duration"}`},
		{name: "tırnaklı metrik + tırnaklı etiket", in: `{"http.server.duration", "service.name"="api"}`,
			want: `{cluster="p1","http.server.duration", "service.name"="api"}`},
		{name: "tırnaklı gruplama etiketi", in: `sum by ("k8s.pod.name") (rate({"http.requests.total"}[5m]))`,
			want: `sum by ("k8s.pod.name") (rate({cluster="p1","http.requests.total"}[5m]))`},
		{name: "ASCII dışı dize", in: `{"ölçüm.süresi", "bölge"="İstanbul"}`, want: `{cluster="p1","ölçüm.süresi", "bölge"="İstanbul"}`},
		// ── absent() ──
		{name: "absent seçici", in: `absent(up{job="api"})`, want: `absent(up{cluster="p1",job="api"})`},
		{name: "absent çıplak", in: `absent(nonexistent_metric)`, want: `absent(nonexistent_metric{cluster="p1"})`},
		{name: "absent_over_time", in: `absent_over_time(x[5m])`, want: `absent_over_time(x{cluster="p1"}[5m])`},
		// ── ikili operatörler ──
		{name: "on + group_left listesi", in: `a / on(instance) group_left(node) b`,
			want: `a{cluster="p1"} / on(instance) group_left(node) b{cluster="p1"}`},
		{name: "ignoring + parantezsiz group_right", in: `a + ignoring(x) group_right b`,
			want: `a{cluster="p1"} + ignoring(x) group_right b{cluster="p1"}`},
		{name: "bool + on()", in: `a > bool on() b`, want: `a{cluster="p1"} > bool on() b{cluster="p1"}`},
		{name: "atan2", in: `a atan2 b`, want: `a{cluster="p1"} atan2 b{cluster="p1"}`},
		{name: "tekli eksi, üs, mod", in: `-a ^ 2 % b`, want: `-a{cluster="p1"} ^ 2 % b{cluster="p1"}`},
		{name: "or on() vector(0)", in: `sum(a) or on() vector(0)`, want: `sum(a{cluster="p1"}) or on() vector(0)`},
		{name: "== bool", in: `a == bool 1`, want: `a{cluster="p1"} == bool 1`},
		// ── agregatlar ──
		{name: "sonek gruplama", in: `sum(x) by (job)`, want: `sum(x{cluster="p1"}) by (job)`},
		{name: "without önek", in: `sum without (a, b) (x)`, want: `sum without (a, b) (x{cluster="p1"})`},
		{name: "büyük harf SUM BY", in: `SUM BY (job) (x)`, want: `SUM BY (job) (x{cluster="p1"})`},
		{name: "topk by önek", in: `topk by (ns) (5, x)`, want: `topk by (ns) (5, x{cluster="p1"})`},
		{name: "quantile", in: `quantile(0.9, x)`, want: `quantile(0.9, x{cluster="p1"})`},
		{name: "count_values", in: `count_values("v", x)`, want: `count_values("v", x{cluster="p1"})`},
		{name: "histogram_quantile", in: `histogram_quantile(0.99, sum by (le) (rate(x_bucket[5m])))`,
			want: `histogram_quantile(0.99, sum by (le) (rate(x_bucket{cluster="p1"}[5m])))`},
		{name: "çok satırlı", in: "sum by (pod) (\n  rate(x[5m])\n)", want: "sum by (pod) (\n  rate(x{cluster=\"p1\"}[5m])\n)"},
		// ── başarısız-kapalı: gramer anahtar kelimeleri metrik adı da olabilir ──
		{name: "offset adlı metrik", in: `offset`, want: `offset{cluster="p1"}`},
		{name: "agregat adlı metrikler", in: `sum + count`, want: `sum{cluster="p1"} + count{cluster="p1"}`},
		{name: "operand konumunda and", in: `x and and`, want: `x{cluster="p1"} and and{cluster="p1"}`},
		{name: "inf/nan sayıdır", in: `x > Inf or NaN`, want: `x{cluster="p1"} > Inf or NaN`},
		{name: "recording rule adı", in: `job:http_requests:rate5m`, want: `job:http_requests:rate5m{cluster="p1"}`},
		// ── matcher metni ──
		{name: "eski sözdizimine uymayan etiket adı tırnaklanır", in: `up`, label: "k8s.cluster.name", want: `up{"k8s.cluster.name"="p1"}`},
		{name: "değerde satır sonu kaçışlanır", in: `up`, value: "p\n1", want: `up{cluster="p\n1"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			label, value := c.label, c.value
			if label == "" {
				label = "cluster"
			}
			if value == "" {
				value = "p1"
			}
			got := withClusterMatcher(c.in, label, value)
			if got != c.want {
				t.Fatalf("\n girdi:    %q\n alınan:   %q\n beklenen: %q", c.in, got, c.want)
			}
			if n := bareSelectorCount(got); n != 0 {
				t.Fatalf("enjeksiyondan sonra %d çıplak seçici kaldı: %q", n, got)
			}
			if hasCommentOutsideStrings(got) {
				t.Fatalf("çıktıda dize dışı # kaldı: %q", got)
			}
		})
	}
}

// hasCommentOutsideStrings — test kapısı: dize dışında `#` var mı.
func hasCommentOutsideStrings(s string) bool {
	for i := 0; i < len(s); {
		switch {
		case isQuote(s[i]):
			i, _ = stringEnd(s, i)
		case s[i] == '#':
			return true
		default:
			i++
		}
	}
	return false
}

// v0.10.950 — etiket adı boşken (cluster başına URL) konsol sorgusu AYNEN
// gider: yorum silme bile yok (Prometheus yorumu kendisi yok sayar).
func TestEffectiveQueryPerURLModeUnchanged(t *testing.T) {
	c := ClusterConfig{Name: "cluster-a", URL: "http://thanos.example.invalid", Enabled: true}
	for _, q := range []string{"up # don't", "sum(rate(x[5m]))", "# only"} {
		if got := c.EffectiveQuery(q); got != q {
			t.Fatalf("etiket yokken ifade değişmemeli: %q → %q", q, got)
		}
	}
	shared := ClusterConfig{Name: "cluster-a", URL: "http://thanos.example.invalid", ThanosLabelName: "cluster", Enabled: true}
	if got := shared.EffectiveQuery("up # it's"); got != `up{cluster="cluster-a"}` {
		t.Fatalf("paylaşımlı querier: değer boşsa Name; alınan %q", got)
	}
	shared.ThanosLabelValue = "eu-1"
	if got := shared.EffectiveQuery("up"); got != `up{cluster="eu-1"}` {
		t.Fatalf("paylaşımlı querier: açık değer; alınan %q", got)
	}
}

// v0.10.950 — match[] enjeksiyonu (karar 6): her seçiciye AYNI matcher;
// seçici yoksa `{label="value"}` sentezi (aksi hâlde otomatik tamamlama
// bütün cluster'ları döndürür); boş girdiler atılır; dönüş asla nil.
func TestEffectiveMatchers(t *testing.T) {
	shared := ClusterConfig{Name: "cluster-a", ThanosLabelName: "cluster", Enabled: true}
	perURL := ClusterConfig{Name: "cluster-b", Enabled: true}
	cases := []struct {
		name  string
		c     ClusterConfig
		match []string
		want  []string
	}{
		{"paylaşımlı, seçici yok → sentez", shared, nil, []string{`{cluster="cluster-a"}`}},
		{"paylaşımlı, yalnız boşluk → sentez", shared, []string{"", "  "}, []string{`{cluster="cluster-a"}`}},
		{"paylaşımlı, her seçici enjekte", shared, []string{"up", `{__name__=~"http_.*"}`},
			[]string{`up{cluster="cluster-a"}`, `{cluster="cluster-a",__name__=~"http_.*"}`}},
		{"paylaşımlı, yorumlu seçici", shared, []string{"up # it's"}, []string{`up{cluster="cluster-a"}`}},
		{"URL başına, seçici yok → boş", perURL, nil, []string{}},
		{"URL başına, aynen (boşlar atılır)", perURL, []string{"up", "", `{job="a"}`}, []string{"up", `{job="a"}`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.c.EffectiveMatchers(c.match)
			if got == nil {
				t.Fatal("dönüş nil olmamalı")
			}
			if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") || len(got) != len(c.want) {
				t.Fatalf("alınan %q, beklenen %q", got, c.want)
			}
		})
	}
}

// v0.10.950 — Thanos'un "satır:sütun" konumu ETKİN sorguyu anlatır;
// originalPosition kullanıcının yazdığı sorgudaki konuma çevirir (yorum
// satırları ve enjekte matcher'lar geri alınır).
func TestOriginalPosition(t *testing.T) {
	cases := []struct {
		name, in, marker  string
		wantLine, wantCol int
	}{
		{"aynı satır, önde enjeksiyon", `up + BAD`, "BAD", 1, 6},
		{"silinen yorum satırı", "# it's a header\nup + BAD", "BAD", 2, 6},
		{"satır içi yorum + çok satır", "sum(rate(x[5m])) # don't\n  and BAD", "BAD", 2, 7},
		{"süslü içindeki enjeksiyon", `x{job="a"} / BAD`, "BAD", 1, 14},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eff := withClusterMatcher(c.in, "cluster", "p1")
			el, ec := lineColAt(eff, strings.Index(eff, c.marker))
			l, col, ok := originalPosition(c.in, "cluster", "p1", el, ec)
			if !ok || l != c.wantLine || col != c.wantCol {
				t.Fatalf("etkin %d:%d → %d:%d ok=%v, beklenen %d:%d\n etkin sorgu: %q", el, ec, l, col, ok, c.wantLine, c.wantCol, eff)
			}
			// Beklenen konum gerçekten işaretçinin orijinaldeki yeri.
			if wl, wc := lineColAt(c.in, strings.Index(c.in, c.marker)); wl != l || wc != col {
				t.Fatalf("orijinalde işaretçi %d:%d, çevrilen %d:%d", wl, wc, l, col)
			}
		})
	}
	// Sentetik bayta (enjekte matcher) düşen konum sonraki kaynak baytına.
	if l, col, ok := originalPosition(`up + x`, "cluster", "p1", 1, 4); !ok || l != 1 || col != 3 {
		t.Fatalf("sentetik konum → sonraki kaynak baytı: %d:%d ok=%v", l, col, ok)
	}
	// Etkin sorgunun sonu (ör. "unexpected end of input") → son kaynak baytının ardı.
	eff := withClusterMatcher("sum(up # c", "cluster", "p1")
	if l, col, ok := originalPosition("sum(up # c", "cluster", "p1", 1, len(eff)+1); !ok || l != 1 || col != 7 {
		t.Fatalf("sondaki konum: %d:%d ok=%v (etkin %q)", l, col, ok, eff)
	}
	// Etiket yoksa özdeşlik; etkin sorgu dışı konum → ok=false.
	if l, col, ok := originalPosition("up", "", "", 1, 2); !ok || l != 1 || col != 2 {
		t.Fatalf("etiket yok → özdeşlik: %d:%d ok=%v", l, col, ok)
	}
	if _, _, ok := originalPosition("up", "cluster", "p1", 5, 1); ok {
		t.Fatal("etkin sorguda olmayan satır ok=false dönmeli")
	}
}

// v0.10.950 — kaynak haritası ve kesik girdi dayanıklılığı: golden
// girdilerin HER öneki (kapanmamış dize/süslü/köşeli) panik atmaz; harita
// çıktıyla aynı uzunlukta ve eşlenen her bayt girdideki baytın ta kendisi.
func TestInjectMatcherSourceMapAndPrefixes(t *testing.T) {
	inputs := []string{
		"# it's a header\nsum by (pod) (rate(x{a=\"b\", # c }\n}[5m])) or on() vector(0)",
		"label_replace(x, \"a\", `C:\\`, \"b\", \"c\") or secret @ start() offset -5m",
		`{"http.server.duration", "service.name"="api"} / ignoring(x) group_left bool`,
		"x{re=~\"a\\\"b\"}[1h:5m] > bool Inf",
	}
	for _, in := range inputs {
		for n := 0; n <= len(in); n++ {
			p := in[:n]
			out, pos := injectMatcher(p, `cluster="p1"`, true)
			if len(pos) != len(out) {
				t.Fatalf("harita uzunluğu %d ≠ çıktı %d (girdi %q)", len(pos), len(out), p)
			}
			for k, src := range pos {
				if src < -1 || src >= len(p) || (src >= 0 && p[src] != out[k]) {
					t.Fatalf("harita bozuk: out[%d]=%q → src %d (girdi %q, çıktı %q)", k, out[k], src, p, out)
				}
			}
			if plain, _ := injectMatcher(p, `cluster="p1"`, false); plain != out {
				t.Fatalf("track=true/false farklı çıktı: %q vs %q", plain, out)
			}
		}
	}
}

// FuzzWithClusterMatcher — v0.10.950: rastgele girdide panik yok; dize
// dışı `#` kalmaz (yorum silme tam); harita tutarlı. `go test` yalnız
// tohumları koşar; derin tarama: go test -fuzz=FuzzWithClusterMatcher.
func FuzzWithClusterMatcher(f *testing.F) {
	for _, s := range []string{
		"up # don't\nsecret", "x{a=\"#\"} # c", "rate(x[5m])[1h:] @ end()", "`\\` or y",
		"sum by (a) (x) offset 5m", `{"a.b"}`, "a # c\r+b", "x{", `"unterminated`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out, pos := injectMatcher(in, `cluster="p1"`, true)
		if len(pos) != len(out) {
			t.Fatalf("harita uzunluğu %d ≠ çıktı %d", len(pos), len(out))
		}
		for k, src := range pos {
			if src < -1 || src >= len(in) || (src >= 0 && in[src] != out[k]) {
				t.Fatalf("harita bozuk: out[%d] → %d", k, src)
			}
		}
		if hasCommentOutsideStrings(out) {
			t.Fatalf("dize dışı # kaldı: %q → %q", in, out)
		}
	})
}
