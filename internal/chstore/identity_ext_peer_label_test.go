package chstore

// v0.10.1344 — operatör raporu 2026-10-18 (servis sayfası → Topology,
// "card-web-api neighborhood", 2 hop): enstrümante iki servis "dış
// bağımlılık" diye çiziliyordu —
// `ext:bsa-channel-channelutility-prod-grpc.channel.svc.cluster.local`
// (56 span, %0) ve `ext:bsa-customer-maininq-prod-kon.<kurum>` (66 span,
// %3.03); aynı servis GSLB/route host'ları `<svc>-prod-mobil.<kurum>` ve
// `<svc>-prod-sube.<kurum>` adıyla da geliyordu. Gerçek servisler
// bsa-channel-channelutility-prod ve bsa-customer-maininq-prod. Kök neden:
// ResolveExtPeer yalnız ExtPeerServiceCandidates'i deniyordu (ham, host,
// k8s'te ilk DNS etiketi AYNEN → `…-prod-grpc` diye servis yok; GSLB
// host'unda etiket adayı hiç yok). Kurum alan adı repoya yazılmaz
// (v0.10.1146) — testlerde `.bank.example`.

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestExtPeerLabel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ext:bsa-channel-channelutility-prod-grpc.channel.svc.cluster.local", "bsa-channel-channelutility-prod-grpc"},
		{"ext:bsa-customer-maininq-prod-kon.bank.example", "bsa-customer-maininq-prod-kon"},
		{"ext:dns:///bsa-customer-maininq-prod-sube.bank.example:443", "bsa-customer-maininq-prod-sube"},
		{"https://bsa-customer-maininq-prod-mobil.bank.example/v1/x", "bsa-customer-maininq-prod-mobil"},
		{"ext:api.stripe.com", "api"},
		{"ext:payments", ""},               // tek etiket
		{"ext:payments:8080", ""},          // tek etiket + port
		{"ext:10.0.0.7:5432", ""},          // IPv4
		{"ext:10.0.0.7", ""},               // IPv4
		{"ext:fe80::1", ""},                // IPv6
		{"ext:[fe80::1]:443", ""},          // köşeli IPv6
		{"ext:Svc@10.0.0.1:8080", ""},      // geçit biçimi
		{"ext:Svc@foo-prod.ns.svc:80", ""}, // geçit biçimi, host'lu
		{"ext:..svc", ""},
		{"ext:foo..", ""},
		{"ext:", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := ExtPeerLabel(c.in); got != c.want {
			t.Errorf("ExtPeerLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtPeerLabelPrefixes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"bsa-channel-channelutility-prod-grpc", []string{"bsa-channel-channelutility-prod-grpc", "bsa-channel-channelutility-prod", "bsa-channel-channelutility"}},
		{"bsa-customer-maininq-prod-kon", []string{"bsa-customer-maininq-prod-kon", "bsa-customer-maininq-prod", "bsa-customer-maininq"}},
		{"foo-prod-grpc", []string{"foo-prod-grpc", "foo-prod"}},                         // R3: "foo" '-' içermez
		{"api-grpc", []string{"api-grpc"}},                                               // R3
		{"foo-prod-a-b-c", []string{"foo-prod-a-b-c", "foo-prod-a-b", "foo-prod-a"}},     // en çok 2 atım
		{"Foo-Prod-GRPC", []string{"Foo-Prod-GRPC", "Foo-Prod"}},                         // harf duyarsız belirteç
		{"foo-prod-abcdefghijklmnop", []string{"foo-prod-abcdefghijklmnop", "foo-prod"}}, // 16 bayt: geçer
		{"foo-prod-abcdefghijklmnopq", []string{"foo-prod-abcdefghijklmnopq"}},           // 17 bayt: atılmaz
		{"foo-prod-x_y", []string{"foo-prod-x_y"}},                                       // belirteç alfanümerik değil
		{"foo--bar-baz", []string{"foo--bar-baz", "foo--bar"}},                           // "foo-" '-' ile biterdi
		{"foo-bar-", []string{"foo-bar-"}},                                               // boş belirteç
		{"payments", []string{"payments"}},
		{"", nil},
	}
	for _, c := range cases {
		if got := ExtPeerLabelPrefixes(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ExtPeerLabelPrefixes(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// extPeerLabelResolveCases — ResolveExtPeer'in 2. aşama tablosu; Go ve
// canlı SQL eşliği (TestExtPeerIsServiceSQLMirrorMatchesResolve) aynı
// tabloyu koşar.
var extPeerLabelSet = []string{"bsa-channel-channelutility-prod", "bsa-customer-maininq-prod", "payments", "api", "foo", "foo-prod"}

var extPeerLabelResolveCases = []struct {
	name, peer, want string
	ok               bool
}{
	{"operator: grpc k8s host", "ext:bsa-channel-channelutility-prod-grpc.channel.svc.cluster.local", "bsa-channel-channelutility-prod", true},
	{"operator: -prod-kon GSLB", "ext:bsa-customer-maininq-prod-kon.bank.example", "bsa-customer-maininq-prod", true},
	{"operator: -prod-mobil GSLB", "ext:bsa-customer-maininq-prod-mobil.bank.example", "bsa-customer-maininq-prod", true},
	{"operator: -prod-sube GSLB", "ext:bsa-customer-maininq-prod-sube.bank.example", "bsa-customer-maininq-prod", true},
	{"GSLB with port", "ext:bsa-customer-maininq-prod-kon.bank.example:443", "bsa-customer-maininq-prod", true},
	{"dns:/// k8s form", "ext:dns:///bsa-channel-channelutility-prod-grpc.channel.svc.cluster.local:50051", "bsa-channel-channelutility-prod", true},
	{"dns:/// GSLB form", "ext:dns:///bsa-customer-maininq-prod-sube.bank.example:443", "bsa-customer-maininq-prod", true},
	{"case-insensitive GSLB", "ext:BSA-Customer-Maininq-Prod-Kon.bank.example", "bsa-customer-maininq-prod", true},
	{"two-label host", "ext:bsa-customer-maininq-prod-kon.local", "bsa-customer-maininq-prod", true},
	{"longest prefix wins", "ext:foo-prod-grpc.ns.svc", "foo-prod", true},
	{"corp host without '-' label", "ext:nyp1.bank.example", "", false},
	{"public host", "ext:api.stripe.com", "", false},
	{"R3: api-grpc never becomes api", "ext:api-grpc.ns.svc", "", false},
	{"existing pin: public host label", "ext:payments.example.com", "", false},
	{"more than 2 strips", "ext:foo-prod-a-b-c.ns.svc", "", false},
	{"token over 16 bytes", "ext:foo-prod-abcdefghijklmnopq.ns.svc", "", false},
	{"catalogue host never shortened", "ext:foo-prod-api.stripe.com", "", false},
	{"IP", "ext:10.0.0.7:5432", "", false},
	{"gateway form", "ext:Svc@bsa-customer-maininq-prod-kon.bank.example:443", "", false},
	{"unknown prefix", "ext:ghost-prod-kon.bank.example", "", false},
}

func TestServiceNameSetResolveExtPeerLabelStage(t *testing.T) {
	set := NewServiceNameSet(extPeerLabelSet...)
	for _, c := range extPeerLabelResolveCases {
		got, ok := set.ResolveExtPeer(c.peer)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: ResolveExtPeer(%q) = (%q, %v), want (%q, %v)", c.name, c.peer, got, ok, c.want, c.ok)
		}
	}
	// Harf çakışması: iki yazım aynı küçük harfe düşüyorsa harf duyarsız yol kapalı.
	amb := NewServiceNameSet("Bar-prod", "bar-PROD")
	if got, ok := amb.ResolveExtPeer("ext:BAR-PROD-grpc.ns.svc"); ok {
		t.Errorf("ambiguous case collision must not merge, got %q", got)
	}
	if got, ok := amb.ResolveExtPeer("ext:Bar-prod-grpc.ns.svc"); !ok || got != "Bar-prod" {
		t.Errorf("exact prefix still merges under collision, got (%q, %v)", got, ok)
	}
	// 1. aşama önce: tam etiket bir servisse önek denenmez.
	both := NewServiceNameSet("foo-prod-grpc", "foo-prod")
	if got, _ := both.ResolveExtPeer("ext:foo-prod-grpc.ns.svc"); got != "foo-prod-grpc" {
		t.Errorf("stage 1 must win over label prefixes, got %q", got)
	}
}

// ResolveExtPeer = "adaylardan biri çözülüyor" — SQL boolean ikizinin
// (extPeerIsServiceSQL ∘ extPeerResolveCandidatesSQL) dayandığı öncül.
func TestExtPeerResolveCandidatesMatchResolve(t *testing.T) {
	set := NewServiceNameSet(extPeerLabelSet...)
	for _, raw := range extPeerLabelParityTable() {
		_, ok := set.ResolveExtPeer("ext:" + raw)
		any := false
		for _, c := range ExtPeerResolveCandidates("ext:" + raw) {
			if _, hit := set.lookup(c); hit {
				any = true
				break
			}
		}
		if any != ok {
			t.Errorf("%q: ResolveExtPeer ok=%v, aday kümesi %v", raw, ok, any)
		}
	}
}

func extPeerLabelParityTable() []string {
	out := append([]string{}, extPeerCaseTable...)
	for _, c := range extPeerLabelResolveCases {
		out = append(out, strings.TrimPrefix(c.peer, "ext:"))
	}
	return append(out,
		"BAR-PROD-grpc.ns.svc", "x-y.svc.cluster.local", "10.0.0.7", "a-b.c", "-x.y.z", "a-.b.c",
		"foo-prod-x_y.ns.svc", "foo--bar-baz.ns.svc", "my-svc-prod.cognito-idp.example", "  foo-prod-kon.bank.example  ")
}

// TestExtPeerIsServiceSQLMirrorMatchesResolve — 2. aşamanın SQL ikizi
// canlı motorda: (a) aday listesi ExtPeerResolveCandidates ile aynı,
// (b) extPeerIsServiceSQL kararı ResolveExtPeer ile aynı.
func TestExtPeerIsServiceSQLMirrorMatchesResolve(t *testing.T) {
	q := extPeerResolveCandidatesSQL("substring(child_node, 5)")
	if strings.ContainsAny(q, "?{}") {
		t.Fatalf("extPeerResolveCandidatesSQL `?`/`{`/`}` içeriyor — sürücü bunu bind yer tutucusu sayar")
	}
	bin := clickhouseLocal(t)
	table := extPeerLabelParityTable()
	services := append(append([]string{}, extPeerLabelSet...), "Bar-prod", "bar-PROD")
	set := NewServiceNameSet(services...)
	groups := map[string][]string{}
	for _, s := range services {
		groups[strings.ToLower(s)] = append(groups[strings.ToLower(s)], s)
	}
	var exact, lower []string
	for _, s := range services {
		exact = append(exact, chQuote(s))
	}
	for l, g := range groups {
		if len(g) == 1 {
			lower = append(lower, chQuote(l))
		}
	}
	with := "WITH tuple([" + strings.Join(exact, ", ") + "], [" + strings.Join(lower, ", ") + "]) AS svc_names "
	var sels []string
	for _, raw := range table {
		c := extPeerResolveCandidatesSQL(chQuote(raw))
		sels = append(sels, with+"SELECT concat(toString("+extPeerIsServiceSQL(c)+"), '\\x02', arrayStringConcat("+c+", '\\x01')) FORMAT TSVRaw")
	}
	lines := runCHLocal(t, bin, strings.Join(sels, ";\n")+";")
	if len(lines) != len(table) {
		t.Fatalf("satır sayısı %d, beklenen %d", len(lines), len(table))
	}
	for i, raw := range table {
		parts := strings.SplitN(lines[i], "\x02", 2)
		var got []string
		if parts[1] != "" {
			got = dedupKeepOrder(strings.Split(parts[1], "\x01"))
		}
		if want := ExtPeerResolveCandidates("ext:" + raw); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: CH adaylar %q, Go %q", raw, got, want)
		}
		_, ok := set.ResolveExtPeer("ext:" + raw)
		if gotOK := parts[0] == "1"; gotOK != ok {
			t.Errorf("%q: CH is-service=%v, ResolveExtPeer=%v", raw, gotOK, ok)
		}
	}
}

func TestExtPeerLabelStageSQLPinned(t *testing.T) {
	// Liste okuması iki aşamayı birlikte, LIMIT'ten önce uygular.
	q := externalHostsSQL()
	if !strings.Contains(q, "HAVING NOT "+extPeerIsServiceSQL(extPeerResolveCandidatesSQL("substring(child_node, 5)"))) {
		t.Error("liste okuması etiket-önek aşamasını taşımıyor — `<svc>-prod-kon.<kurum>` /external'da kalır")
	}
	if strings.ContainsAny(labelExtChildRegex, "?{}") || strings.ContainsAny(extPeerLabelStageSQL("x"), "?{}") {
		t.Error("etiket-önek SQL'inde `?`/`{`/`}` olmamalı")
	}
	const strip = `if(match(p, '[^-]-[A-Za-z0-9]+$') AND length(extract(p, '-([A-Za-z0-9]+)$')) <= 16 ` +
		`AND position(replaceRegexpOne(p, '-[A-Za-z0-9]+$', ''), '-') > 0, replaceRegexpOne(p, '-[A-Za-z0-9]+$', ''), '')`
	if got := extPeerLabelStripSQL("p"); got != strip {
		t.Errorf("extPeerLabelStripSQL metni değişti:\n got: %s\nwant: %s", got, strip)
	}
	if !strings.Contains(extPeerCatalogueSQL("r"), "'stripe.com'") || strings.Count(extPeerCatalogueSQL("r"), "'") != 2*len(externalCatalogue) {
		t.Error("katalog ikizi externalCatalogue'un tamamını taşımalı")
	}
}

func TestTopologyNextFrontierLabelPrefixes(t *testing.T) {
	edges := []ServiceTopologyEdge{
		{ParentService: "card-web-api", ChildNode: "ext:bsa-channel-channelutility-prod-grpc.channel.svc.cluster.local"},
		{ParentService: "card-web-api", ChildNode: "ext:bsa-customer-maininq-prod-kon.bank.example"},
		{ParentService: "card-web-api", ChildNode: "ext:api.stripe.com"},
	}
	seen := map[string]bool{"card-web-api": true}
	got := topologyNextFrontier(edges, seen, 300)
	for _, want := range []string{"bsa-channel-channelutility-prod", "bsa-customer-maininq-prod", "bsa-channel-channelutility-prod-grpc"} {
		found := false
		for _, n := range got {
			found = found || n == want
		}
		if !found {
			t.Errorf("2. hop sınırı %q içermeli: %q", want, got)
		}
	}
	for _, n := range got {
		if n == "api" {
			t.Errorf("katalog host'u etiket önekine açılmamalı: %q", got)
		}
	}
}

func TestTopologyTouchWhereLabelPrefixes(t *testing.T) {
	w, args := topologyTouchWhere([]string{"bsa-customer-maininq-prod"}, []string{"bsa-customer-maininq-prod"})
	if strings.Count(w, "?") != len(args) {
		t.Fatalf("yer tutucu (%d) / argüman (%d) uyuşmuyor: %s", strings.Count(w, "?"), len(args), w)
	}
	want := []any{"bsa-customer-maininq-prod", "bsa-customer-maininq-prod",
		"ext:bsa-customer-maininq-prod.", "ext:dns:///bsa-customer-maininq-prod.",
		"ext:bsa-customer-maininq-prod-", "ext:dns:///bsa-customer-maininq-prod-"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	if !strings.Contains(w, "match(child_node, '"+labelExtChildRegex+"')") {
		t.Fatalf("GSLB etiket kapısı eksik: %s", w)
	}
	// Kapının Go aynası (RE2 = CH motoru): GSLB + k8s geçer, genel/tek
	// etiketli host geçmez. k8s adları zaten k8sExtChildRegex'ten geçer.
	re := regexp.MustCompile(labelExtChildRegex)
	for in, ok := range map[string]bool{
		"ext:bsa-customer-maininq-prod-kon.bank.example":                     true,
		"ext:dns:///bsa-customer-maininq-prod-sube.bank.example:443":         true,
		"ext:bsa-channel-channelutility-prod-grpc.channel.svc.cluster.local": true,
		"ext:api.stripe.com":    false,
		"ext:payments-x.local":  false,
		"ext:Svc@10.0.0.1:8080": false,
	} {
		if re.MatchString(in) != ok {
			t.Errorf("labelExtChildRegex(%q) = %v, want %v", in, !ok, ok)
		}
	}
}

// TestResolveExtPeerCallSitesPinned — v0.10.1344: ResolveExtPeer artık
// etiket öneklerini de deniyor (daha geniş birleştirme). Yeni bir tüketici
// bunu BİLEREK seçmeli: sayı değişirse bu liste ve DECISIONS.md birlikte
// güncellenir.
func TestResolveExtPeerCallSitesPinned(t *testing.T) {
	want := map[string]int{
		"chstore/service_map.go": 1,
		// v0.10.2240 (Entity S5): calls kenarı servis grafiğiyle aynı katlamayı seçer.
		"chstore/entity_calls.go": 1,
		"api/servicegraph.go":     1,
		"api/topology.go":         1,
	}
	got := map[string]int{}
	root := filepath.Join("..")
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		n := strings.Count(string(b), ".ResolveExtPeer(")
		if n > 0 {
			rel, _ := filepath.Rel(root, p)
			got[filepath.ToSlash(rel)] = n
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveExtPeer( çağrı yerleri %v, pinli %v — yeni tüketici 2. aşamayı (etiket önekleri) bilerek seçmeli", got, want)
	}
}

// TestKnownServiceNamesSQLPinned — bilinen-servis yedeğinin okuması: MV,
// zaman sınırlı, LIMIT + max_execution_time (CLAUDE.md CH sınırları).
func TestKnownServiceNamesSQLPinned(t *testing.T) {
	const want = `SELECT DISTINCT service_name FROM service_summary_5m WHERE time_bucket >= now() - INTERVAL 1 DAY LIMIT 20000 SETTINGS max_execution_time = 5`
	if knownServiceNamesSQL != want {
		t.Errorf("knownServiceNamesSQL değişti:\n got: %s\nwant: %s", knownServiceNamesSQL, want)
	}
}
