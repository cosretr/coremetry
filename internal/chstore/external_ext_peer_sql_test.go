package chstore

// v0.10.1266 — operatör 2026-10-17: /external k8s DNS ile çağrılan
// enstrümante servisleri dış sistem diye listeliyor.
//
// `ext:demo-sales-application-query-prod.sales.svc.cluster.local`
// /external'da üçüncü parti olarak görünüyordu; v0.10.1260 aynı sınıfı
// topoloji grafiğinde ExtPeerServiceCandidates/ResolveExtPeer ile
// kapatmıştı ama bu sayfanın dışlaması birebir `GLOBAL NOT IN`'di.
//
// Kilit adım sözleşmesi: dışlama SQL'de (LIMIT'ten önce) koşuyor ve Go
// normalizer'ıyla AYNI aday kümesini üretmek zorunda. Üç katman:
//  1. extPeerHostSteps'in Go aynası, ExtPeerServiceCandidates ile aynı
//     vaka tablosunda aynı adayları verir (her ortamda koşar).
//  2. SQL ifadesinin metni pinli (her ortamda koşar).
//  3. `clickhouse` ikilisi varsa: ifade canlı motorda aynı tabloyu
//     koşturur, ve üç gerçek okuma (liste + çekmece çağıranları + trend)
//     fikstür üzerinde ResolveExtPeer ile aynı kararı verir.

import (
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// extPeerCaseTable — Go fonksiyonu, Go aynası ve canlı SQL'in ORTAK
// girdi tablosu (öneksiz ham adlar; liste SQL'i substring(child_node, 5)
// görür). identity_ext_peer_test.go'nun girdileri + port/şema/boşluk
// köşeleri.
var extPeerCaseTable = []string{
	"demo-sales-application-query-prod.sales.svc.cluster.local",
	"demo-config-server.demo.svc",
	"demo-flow-engine-prod.flow.cluster.local",
	"bsa-pricing-benefit-prod.pricing.svc.cluster.local:50051",
	"dns:///bsa-cashmanagement.cash.svc:8080",
	"dns:orders.shop.svc.cluster.local:443",
	"DNS:///orders.shop.svc:443",
	"orders.shop.svc.cluster.local.",
	"Orders.Shop.SVC.Cluster.Local",
	"payments:8080",
	"payments",
	"api.stripe.com",
	"api.stripe.com:443",
	"https://api.stripe.com/v1/charges",
	"http://dns:foo",
	"grpc://payments.pay.svc:9090/pkg.Svc/Method",
	"Svc@10.0.0.1:8080",
	"svc.cluster.local",
	"SVC.Cluster.Local",
	"cluster.local",
	".svc.cluster.local",
	"..svc",
	"a..svc.cluster.local",
	"svc.svc",
	"svc.cluster.local.svc",
	"x.cluster.local:80",
	"fe80::1",
	"[fe80::1]:443",
	"host:port",
	":8080",
	"  orders.shop.svc  ",
	"10.0.0.7:5432",
	"",
}

// extPeerMirrorCandidates — extPeerHostSteps + k8s desenlerinin Go'da
// koşturulması: SQL ifadesinin ADIM ADIM aynısı (aynı desen dizeleri).
func extPeerMirrorCandidates(raw string) []string {
	var out []string
	add := func(c string) {
		if c == "" {
			return
		}
		for _, x := range out {
			if x == c {
				return
			}
		}
		out = append(out, c)
	}
	add(raw)
	if raw == "" || strings.Contains(raw, "@") {
		return out
	}
	h := raw
	backref := regexp.MustCompile(`\\([0-9])`)
	for _, st := range extPeerHostSteps {
		if st.Scheme {
			if i := strings.Index(h, "://"); i >= 0 {
				h = h[i+3:]
			} else if strings.HasPrefix(strings.ToLower(h), "dns:") {
				h = h[4:]
			}
			continue
		}
		re := regexp.MustCompile(st.Pattern)
		repl := backref.ReplaceAllString(st.Replace, `$${$1}`)
		if st.All {
			h = re.ReplaceAllString(h, repl)
			continue
		}
		if loc := re.FindStringSubmatchIndex(h); loc != nil {
			h = h[:loc[0]] + string(re.ExpandString(nil, repl, h, loc)) + h[loc[1]:]
		}
	}
	add(h)
	if lh := strings.ToLower(h); lh != extPeerK8sBareSuffix && regexp.MustCompile(extPeerK8sHostPattern).MatchString(lh) {
		add(strings.SplitN(h, ".", 2)[0])
	}
	return out
}

func TestExtPeerSQLMirrorMatchesGoNormalizer(t *testing.T) {
	for _, raw := range extPeerCaseTable {
		want := ExtPeerServiceCandidates("ext:" + raw)
		got := extPeerMirrorCandidates(raw)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: SQL adım aynası %q, ExtPeerServiceCandidates %q — "+
				"extPeerHostSteps ile Go normalizer'ı ayrıştı", raw, got, want)
		}
	}
}

// TestExtPeerCandidatesSQLPinned — ifadenin metni. Desen değişirse bu
// dize ve canlı-motor testi birlikte güncellenir.
func TestExtPeerCandidatesSQLPinned(t *testing.T) {
	const want = `arrayFilter(c -> c != '', if(position(substring(child_node, 5), '@') > 0, [substring(child_node, 5)], ` +
		`arrayMap(h -> [substring(child_node, 5), h, if(match(lowerUTF8(h), ` +
		`'^[^.]+\\.([\\s\\S]*\\.)*(svc\\.cluster\\.local|cluster\\.local|svc)$') AND lowerUTF8(h) != 'svc.cluster.local', ` +
		`splitByChar('.', h)[1], '')], [replaceRegexpOne(replaceRegexpOne(replaceRegexpOne(replaceRegexpOne(` +
		`arrayMap(s -> if(position(s, '://') > 0, substring(s, position(s, '://') + 3), ` +
		`if(startsWith(lowerUTF8(s), 'dns:'), substring(s, 5), s)), ` +
		`[replaceRegexpAll(substring(child_node, 5), '^[\\t\\n\\v\\f\\r ]+|[\\t\\n\\v\\f\\r ]+$', '')])[1], ` +
		`'^/+', ''), '/[\\s\\S]*$', ''), '^([^:]+):[0-9]+$', '\\1'), '\\.$', '')])[1]))`
	if got := extPeerCandidatesSQL("substring(child_node, 5)"); got != want {
		t.Errorf("extPeerCandidatesSQL metni değişti:\n got: %s\nwant: %s", got, want)
	}
	// clickhouse-go'nun konumsal bind'i dizgi değişmezi içindeki `?`'yi de
	// yer tutucu sayar (bindPositional): ifadede tek bir `?` olursa
	// argümanlar kayar ve okuma ya patlar ya YANLIŞ pencereyle koşar.
	// '{…:…}' de sunucu tarafı parametre moduna geçirir (gatewayPathRegex
	// notu) — süslü parantez de yok.
	if strings.ContainsAny(extPeerCandidatesSQL("substring(child_node, 5)"), "?{}") {
		t.Error("extPeerCandidatesSQL `?`/`{`/`}` içeriyor — sürücü bunu bind yer tutucusu sayar")
	}
	// Liste okuması dışlamayı GROUP BY'dan sonra, LIMIT'ten ÖNCE uygular.
	q := externalHostsSQL()
	h, l := strings.Index(q, "HAVING NOT arrayExists("), strings.Index(q, "LIMIT 500")
	if h < 0 || l < 0 || h > l {
		t.Errorf("liste okuması dışlamayı LIMIT'ten önce HAVING'de uygulamıyor:\n%s", q)
	}
	if strings.Contains(q, "GLOBAL NOT IN") {
		t.Error("liste okumasında birebir GLOBAL NOT IN kaldı — k8s DNS adları yine dış listelenir")
	}
	for name, q := range map[string]string{"callers": externalCallersSQL(), "trend": externalTrendSQL()} {
		if !strings.Contains(q, "AND NOT "+extPeerIsServiceSQL("?")) {
			t.Errorf("%s: çekmece okuması aday-kümesi dışlamasını taşımıyor", name)
		}
	}
}

// TestExternalHostExclusionCandidates — çekmece adayları Go'da, liste
// tarafının gördüğü ham adla (tek "ext:" öneki) hesaplanır.
func TestExternalHostExclusionCandidates(t *testing.T) {
	cases := []struct {
		host string
		want []string
	}{
		// v0.10.1344 — + etiket önekleri (ResolveExtPeer 2. aşaması; en çok 2 atım).
		{"demo-sales-application-query-prod.sales.svc.cluster.local",
			[]string{"demo-sales-application-query-prod.sales.svc.cluster.local", "demo-sales-application-query-prod",
				"demo-sales-application-query", "demo-sales-application"}},
		{"api.stripe.com", []string{"api.stripe.com"}},
		{"ext:payments", []string{"ext:payments"}}, // ham ad "ext:" ile başlıyorsa ikinci kez soyulmaz
		{"", []string{}},
	}
	for _, c := range cases {
		if got := externalHostExclusionCandidates(c.host); !reflect.DeepEqual(got, c.want) {
			t.Errorf("externalHostExclusionCandidates(%q) = %q, want %q", c.host, got, c.want)
		}
	}
}

func clickhouseLocal(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("clickhouse")
	if err != nil {
		t.Skip("clickhouse ikilisi yok — canlı motor karşılaştırması atlandı (ayna + pin testleri her ortamda koşar)")
	}
	return bin
}

func runCHLocal(t *testing.T, bin, sql string) []string {
	t.Helper()
	out, err := exec.Command(bin, "local", "--multiquery", "--query", sql).CombinedOutput()
	if err != nil {
		t.Fatalf("clickhouse local: %v\n%s\n--- sql ---\n%s", err, out, sql)
	}
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// TestExtPeerCandidatesSQLLiveEngine — aynı vaka tablosu, gerçek motor.
func TestExtPeerCandidatesSQLLiveEngine(t *testing.T) {
	bin := clickhouseLocal(t)
	var sels []string
	for _, raw := range extPeerCaseTable {
		sels = append(sels, "SELECT arrayStringConcat("+extPeerCandidatesSQL(chQuote(raw))+", '\\x01') FORMAT TSVRaw")
	}
	lines := runCHLocal(t, bin, strings.Join(sels, ";\n")+";")
	// Boş aday listesi boş satır üretir; TrimRight sondakini yer — tamamla.
	for len(lines) < len(extPeerCaseTable) {
		lines = append(lines, "")
	}
	for i, raw := range extPeerCaseTable {
		var got []string
		if lines[i] != "" {
			got = strings.Split(lines[i], "\x01")
		}
		got = dedupKeepOrder(got) // SQL tekrarı elemez; dışlama için önemsiz
		want := ExtPeerServiceCandidates("ext:" + raw)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: CH %q, Go %q — SQL ifadesi Go normalizer'ından ayrıştı", raw, got, want)
		}
	}
}

// TestExternalReadsExcludeInstrumentedServicesLiveEngine — üç gerçek
// okuma fikstür üzerinde; karar ResolveExtPeer ile aynı.
func TestExternalReadsExcludeInstrumentedServicesLiveEngine(t *testing.T) {
	bin := clickhouseLocal(t)
	from := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	in := from.Add(10 * time.Minute)
	out := to.Add(10 * time.Minute) // pencere DIŞI kova

	services := []string{"demo-sales-application-query-prod", "payments", "Billing", "dup", "DUP", "orders.shop.svc.cluster.local"}
	hosts := []string{
		"demo-sales-application-query-prod.sales.svc.cluster.local", // operatör vakası → dışlanır
		"payments.pay.svc:9090",                       // → dışlanır
		"dns:///payments.pay.svc.cluster.local:50051", // → dışlanır
		"billing.fin.svc.cluster.local",               // harf duyarsız → dışlanır
		"Dup.x.svc",                                   // belirsiz harf → KALIR
		"dup.x.svc",                                   // birebir → dışlanır
		"orders.shop.svc.cluster.local",               // birebir FQDN servis → dışlanır
		"api.stripe.com",                              // genel host → KALIR
		"payments.example.com",                        // genel host etiketi → KALIR
		"ghost.ns.svc.cluster.local",                  // bilinmeyen k8s → KALIR
		"Svc@10.0.0.1:8080",                           // geçit biçimi → KALIR
	}
	var fx strings.Builder
	fx.WriteString(`CREATE TABLE service_summary_5m (service_name LowCardinality(String), time_bucket DateTime) ENGINE = Memory;
CREATE TABLE topology_edges_5m (time_bucket DateTime, parent_service LowCardinality(String), child_node String,
  node_kind LowCardinality(String), top_labels Array(String), calls UInt64, sum_duration_ns UInt64, p99_ms Float64, errors UInt64)
  ENGINE = ReplacingMergeTree ORDER BY (time_bucket, parent_service, child_node);
`)
	for _, s := range services {
		fx.WriteString("INSERT INTO service_summary_5m VALUES (" + chQuote(s) + ", toDateTime('" + in.Format("2006-01-02 15:04:05") + "', 'UTC'));\n")
	}
	// Yalnız pencere DIŞINDA görülen servis: pencere çifti doğruysa
	// dışlama kümesine girmez, host'u listelenir.
	fx.WriteString("INSERT INTO service_summary_5m VALUES ('ghost', toDateTime('" + out.Format("2006-01-02 15:04:05") + "', 'UTC'));\n")
	for _, h := range hosts {
		fx.WriteString("INSERT INTO topology_edges_5m VALUES (toDateTime('" + in.Format("2006-01-02 15:04:05") +
			"', 'UTC'), 'caller', " + chQuote("ext:"+h) + ", 'external', ['GET /x'], 10, 1000000, 1.5, 1);\n")
	}

	set := NewServiceNameSet(services...)
	var wantList []string
	for _, h := range hosts {
		if _, ok := set.ResolveExtPeer("ext:" + h); !ok {
			wantList = append(wantList, h)
		}
	}
	sort.Strings(wantList)
	if len(wantList) != 5 {
		t.Fatalf("fikstür beklentisi kaydı: ResolveExtPeer %d host bırakıyor: %q", len(wantList), wantList)
	}

	withFormat := func(q string) string {
		return strings.Replace(q, "SETTINGS max_execution_time = 10", "FORMAT TSVRaw SETTINGS max_execution_time = 10", 1)
	}
	listSQL := withFormat(inlineDBHealthArgs(t, externalHostsSQL(), []any{from, to, from, to}))
	var gotList []string
	for _, ln := range runCHLocal(t, bin, fx.String()+listSQL) {
		gotList = append(gotList, strings.Split(ln, "\t")[0])
	}
	sort.Strings(gotList)
	if !reflect.DeepEqual(gotList, wantList) {
		t.Errorf("/external listesi %q, ResolveExtPeer'e göre beklenen %q", gotList, wantList)
	}

	// Çekmece: her host için çağıran + trend okuması; dışlanan host boş,
	// kalan host dolu döner.
	keep := map[string]bool{}
	for _, h := range wantList {
		keep[h] = true
	}
	for _, h := range hosts {
		args := []any{from, to, h, from, to, externalHostExclusionCandidates(h)}
		for name, q := range map[string]string{"callers": externalCallersSQL(), "trend": externalTrendSQL()} {
			rows := runCHLocal(t, bin, fx.String()+withFormat(inlineDBHealthArgs(t, q, args)))
			if got := len(rows) > 0; got != keep[h] {
				t.Errorf("%s(%q): satır var=%v, beklenen %v (liste ile çekmece aynı kararı vermeli)", name, h, got, keep[h])
			}
		}
	}
}

func dedupKeepOrder(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range in {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
