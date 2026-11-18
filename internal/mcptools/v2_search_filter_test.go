package mcptools

// v2_search_filter_test.go — v0.10.2349 (investigation-design S2, §4.1).
// Adlar nötr (checkout, orders, payments); IP'ler yalnız belge aralıklarından.

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

func searchErrClass(err error) string {
	var se *SearchError
	if errors.As(err, &se) {
		return se.Class
	}
	return ""
}

// Plan tablosu: tam v4 / v6, önek, /24 kabul; /20 ve regex işleci red; 4. süzgeç red.
func TestPlanFindTracesSearchArgs(t *testing.T) {
	ok := []struct {
		name   string
		a      findTracesArgs
		value  string
		prefix bool
	}{
		{"tam IPv4", findTracesArgs{Service: "orders", ClientIP: "192.0.2.10"}, "192.0.2.10", false},
		{"tam IPv6", findTracesArgs{Service: "orders", ClientIP: "2001:db8::7"}, "2001:db8::7", false},
		{"nokta öneki", findTracesArgs{Service: "orders", ClientIP: "198.51.100."}, "198.51.100.", true},
		{"/24", findTracesArgs{Service: "orders", ClientIP: "203.0.113.9/24"}, "203.0.113.", true},
		{"/16", findTracesArgs{Service: "orders", ClientIP: "198.51.100.0/16"}, "198.51.", true},
		{"/8", findTracesArgs{Service: "orders", ClientIP: "192.0.2.0/8"}, "192.", true},
	}
	for _, c := range ok {
		p, err := planFindTraces(c.a, true)
		if err != nil || p.ClientIP.Value != c.value || p.ClientIP.Prefix != c.prefix {
			t.Errorf("%s: %+v %v", c.name, p.ClientIP, err)
		}
		f := p.traceFilter(time.Unix(0, 0), time.Unix(3600, 0))
		if f.FilterRoot == nil || len(f.FilterRoot.Groups) != 1 || f.FilterRoot.Groups[0].Join != "OR" || len(f.FilterRoot.Groups[0].Filters) != len(ClientIPKeys) {
			t.Fatalf("%s: OR grubu yok: %+v", c.name, f.FilterRoot)
		}
		for i, leaf := range f.FilterRoot.Groups[0].Filters {
			wantOp := "="
			if c.prefix {
				wantOp = "=~"
			}
			if leaf.Key != ClientIPKeys[i] || leaf.Op != wantOp {
				t.Errorf("%s: yaprak %d %+v", c.name, i, leaf)
			}
		}
		if err := f.FilterRoot.Validate(); err != nil {
			t.Errorf("%s: grup derlenmez: %v", c.name, err)
		}
		if c.prefix {
			a := c.a
			a.RangeS = 6 * 3600
			if p, _ := planFindTraces(a, true); p.WindowS != ftPrefixMaxRangeS || p.Narrowed == "" {
				t.Errorf("%s: önek penceresi %d %q", c.name, p.WindowS, p.Narrowed)
			}
		}
	}
	for _, k := range ClientIPKeys {
		if strings.Contains(strings.ToLower(k), "forwarded") {
			t.Fatal("x-forwarded-for listede olmamalı")
		}
	}
	bad := []struct {
		name  string
		a     findTracesArgs
		class string
	}{
		{"/20", findTracesArgs{Service: "orders", ClientIP: "192.0.2.0/20"}, ErrBadClientIP},
		{"IPv6 öneki", findTracesArgs{Service: "orders", ClientIP: "2001:db8::/32"}, ErrBadClientIP},
		{"joker", findTracesArgs{Service: "orders", ClientIP: "192.0.2.*"}, ErrBadClientIP},
		{"önek çok servis", findTracesArgs{Services: []string{"checkout", "orders"}, ClientIP: "192.0.2."}, ErrBadClientIP},
		{"regex işleci", findTracesArgs{Service: "orders", Filters: []SearchFilter{{Key: "tenant.tier", Op: "regex", Value: "g.*"}}}, mcp.ToolErrBadArgs},
		{"içerir işleci", findTracesArgs{Service: "orders", Filters: []SearchFilter{{Key: "tenant.tier", Op: "contains", Value: "g"}}}, mcp.ToolErrBadArgs},
		{"4. süzgeç", findTracesArgs{Service: "orders", Filters: []SearchFilter{{Key: "a", Value: "1"}, {Key: "b", Value: "1"}, {Key: "c", Value: "1"}, {Key: "d", Value: "1"}}}, mcp.ToolErrBadArgs},
		{"kontrol karakteri", findTracesArgs{Service: "orders", Filters: []SearchFilter{{Key: "tenant.tier", Value: "gold\x00"}}}, mcp.ToolErrBadArgs},
		{"uzun değer", findTracesArgs{Service: "orders", Filters: []SearchFilter{{Key: "tenant.tier", Value: strings.Repeat("g", sfMaxValueRunes+1)}}}, mcp.ToolErrBadArgs},
		{"11 değer", findTracesArgs{Service: "orders", Filters: []SearchFilter{{Key: "tenant.tier", Op: "in", Values: strings.Split("a,b,c,d,e,f,g,h,i,j,k", ",")}}}, mcp.ToolErrBadArgs},
		{"boşluklu anahtar", findTracesArgs{Service: "orders", Filters: []SearchFilter{{Key: "tenant tier", Value: "gold"}}}, mcp.ToolErrBadArgs},
		{"endpoint + operation", findTracesArgs{Service: "orders", Operation: "GET /x", Endpoint: "/api/orders"}, mcp.ToolErrBadArgs},
		{"endpoint çözücüsüz", findTracesArgs{Service: "orders", Endpoint: "/api/orders"}, ErrEndpointNotFound},
	}
	for _, c := range bad {
		if _, err := planFindTraces(c.a, true); searchErrClass(err) != c.class {
			t.Errorf("%s: %v (sınıf %q)", c.name, err, searchErrClass(err))
		}
	}
	// Kapalı işleçler AND yaprağı olur; değerler sınırda.
	p, err := planFindTraces(findTracesArgs{Service: "payments", Status: "error", Filters: []SearchFilter{
		{Key: "tenant.tier", Value: "gold"}, {Key: "region", Op: "ne", Value: "eu"}, {Key: "plan", Op: "in", Values: []string{"a", "b"}},
	}}, true)
	if err != nil || len(p.UserFilters) != 3 || p.UserFilters[0].Op != "=" || p.UserFilters[1].Op != "!=" || p.UserFilters[2].Op != "IN" || !p.Errors {
		t.Fatalf("süzgeç planı %+v %v", p.UserFilters, err)
	}
	p, _ = planFindTraces(findTracesArgs{Service: "payments", Filters: []SearchFilter{{Key: "tenant.tier", Op: "exists"}}}, true)
	if len(p.UserFilters) != 1 || p.UserFilters[0].Op != "EXISTS" || p.UserFilters[0].Values != nil {
		t.Fatalf("exists: %+v", p.UserFilters)
	}
}

// Uç nokta: çözüldü → name = (ya da yalnız rota → http.route); belirsiz ≤4 aday; bulunamadı ≤3 yakın.
func TestPlanFindTracesEndpoint(t *testing.T) {
	res := &EndpointResolution{Kind: EndpointResolved, Service: "orders", Operation: "POST /api/orders", Route: "/api/orders"}
	p, err := planFindTraces(findTracesArgs{Services: []string{"checkout", "orders"}, Endpoint: "POST /api/orders", ClientIP: "192.0.2.10", endpointRes: res}, true)
	if err != nil || p.Service != "orders" || p.Services != nil || p.Endpoint == nil {
		t.Fatalf("uç nokta tek servise iner: %+v %v", p, err)
	}
	if f := p.Filters[len(p.Filters)-1]; f.Key != "name" || f.Op != "=" || f.Values[0] != "POST /api/orders" {
		t.Fatalf("uç nokta süzgeci %+v", f)
	}
	routeOnly := &EndpointResolution{Kind: EndpointResolved, Service: "orders", Route: "/api/orders/{id}"}
	p, _ = planFindTraces(findTracesArgs{Service: "orders", Endpoint: "/api/orders/{id}", endpointRes: routeOnly}, true)
	if f := p.Filters[0]; f.Key != "http.route" || f.Values[0] != "/api/orders/{id}" {
		t.Fatalf("yalnız rota %+v", f)
	}
	// Kapsam dışına çözülen uç nokta kapsamı değiştirmez.
	p, _ = planFindTraces(findTracesArgs{Services: []string{"checkout", "payments"}, Endpoint: "/api/orders", endpointRes: res}, true)
	if p.Service != "" || len(p.Services) != 2 {
		t.Fatalf("kapsam dışı servis: %+v", p)
	}
	// Kapsamsız arama uç noktadan kapsam devşirmez (karar 9).
	if _, err := planFindTraces(findTracesArgs{Endpoint: "/api/orders", endpointRes: res}, true); !errors.Is(err, errFindTracesNoScope) {
		t.Fatalf("kapsamsız uç nokta: %v", err)
	}
	amb := &EndpointResolution{Kind: EndpointAmbiguous, Candidates: []string{"a", "b", "c", "d", "e"}}
	_, err = planFindTraces(findTracesArgs{Service: "orders", Endpoint: "/api/orders", endpointRes: amb}, true)
	var se *SearchError
	if !errors.As(err, &se) || se.Class != ErrEndpointAmbiguous || len(se.Candidates) != 4 {
		t.Fatalf("belirsiz: %v", err)
	}
	nf := &EndpointResolution{Kind: EndpointNotFound, Near: []string{"x", "y", "z", "w"}}
	_, err = planFindTraces(findTracesArgs{Service: "orders", Endpoint: "/api/order", endpointRes: nf}, true)
	if !errors.As(err, &se) || se.Class != ErrEndpointNotFound || len(se.Near) != 3 {
		t.Fatalf("bulunamadı: %v", err)
	}
	te := mcp.ClassifyToolError(err)
	b, _ := json.Marshal(te)
	if te.Error != ErrEndpointNotFound || te.Retryable || !strings.Contains(string(b), `"near":["x","y","z"]`) {
		t.Fatalf("sözleşme gövdesi %s", b)
	}
}

// Anahtar doğrulama üç yol: kolon, katalog, kesin prob; değilse unknown_attribute + ≤5 yakın.
func TestValidateFilterKeys(t *testing.T) {
	ctx := context.Background()
	reads := 0
	lk := attrKeyLookup{
		Catalog: func(context.Context) ([]string, error) {
			reads++
			return []string{"tenant.tier", "tenant.id", "tenant.region", "tenant.plan", "tenant.tie", "tenant.tiers", "resource.k8s.pod.name"}, nil
		},
		Probe: func(_ context.Context, key, value string) (uint64, error) {
			if key == "order.channel" && value == "web" {
				return 7, nil
			}
			return 0, nil
		},
	}
	pass := [][]chstore.FilterExpr{
		{{Key: "http.route", Op: "=", Values: []string{"/x"}}},     // tipli kolon: katalog okunmaz
		{{Key: "tenant.tier", Op: "=", Values: []string{"gold"}}},  // katalog
		{{Key: "span.tenant.tier", Op: "EXISTS"}},                  // kapsam önekli
		{{Key: "k8s.pod.name", Op: "!=", Values: []string{"a"}}},   // resource kataloğu
		{{Key: "order.channel", Op: "=", Values: []string{"web"}}}, // kesin prob > 0
	}
	for i, fs := range pass {
		if err := validateFilterKeys(ctx, fs, lk); err != nil {
			t.Errorf("geçmeli %d: %v", i, err)
		}
		if i == 0 && reads != 0 {
			t.Errorf("tipli kolon kataloğu okumamalı: %d okuma", reads)
		}
	}
	err := validateFilterKeys(ctx, []chstore.FilterExpr{{Key: "tenant.teir", Op: "=", Values: []string{"gold"}}}, lk)
	var se *SearchError
	if !errors.As(err, &se) || se.Class != ErrUnknownAttribute || se.Key != "tenant.teir" || len(se.Near) == 0 || len(se.Near) > sfNearMax || se.Near[0] != "tenant.tie" && se.Near[0] != "tenant.tier" {
		t.Fatalf("bilinmeyen anahtar: %v", err)
	}
	// Katalog okunamadı ve prob teyit etmedi → okuma hatası (bilinmeyen sayılmaz).
	boom := errors.New("timeout")
	err = validateFilterKeys(ctx, []chstore.FilterExpr{{Key: "x.y", Op: "EXISTS"}}, attrKeyLookup{Catalog: func(context.Context) ([]string, error) { return nil, boom }})
	if !errors.Is(err, boom) {
		t.Fatalf("katalog hatası: %v", err)
	}
}

// Gövde ≤ 2.048 B; window, entity, applied, start_ms; page_url aynı süzgeçlerle Traces sayfası.
func TestFindTracesPayloadSearchShape(t *testing.T) {
	res := &EndpointResolution{Kind: EndpointResolved, Service: "orders", Operation: "POST /api/orders", Route: "/api/orders"}
	p, err := planFindTraces(findTracesArgs{Service: "orders", Endpoint: "POST /api/orders", ClientIP: "192.0.2.10", endpointRes: res,
		Filters: []SearchFilter{{Key: "tenant.tier", Value: "gold"}}, Status: "error"}, true)
	if err != nil {
		t.Fatal(err)
	}
	p.Entity = &ServiceRef{Service: "orders", Cluster: "c1", Namespace: "shop"}
	from, to := time.UnixMilli(1_794_900_000_000), time.UnixMilli(1_794_903_600_000)
	var rows []chstore.TraceRow
	for i := 0; i < 11; i++ {
		rows = append(rows, chstore.TraceRow{TraceID: strings.Repeat("a", 31) + string(rune('0'+i%10)), RootName: "POST /api/orders " + strings.Repeat("x", 60),
			ServiceName: "orders", StartTime: from.Add(time.Minute).UnixNano(), DurationMs: 12.5, SpanCount: 9, HasError: true})
	}
	out := findTracesPayload(p, p.traceFilter(from, to), rows, true, from, to)
	b, _ := json.Marshal(out)
	if len(b) > V2ToolMaxBytes {
		t.Fatalf("gövde %d B", len(b))
	}
	var got struct {
		Window  map[string]int64 `json:"window"`
		Entity  ServiceRef       `json:"entity"`
		Applied struct {
			Endpoint map[string]string `json:"endpoint"`
			ClientIP struct {
				Value, Match string
				Keys         []string
			} `json:"client_ip"`
			Filters []chstore.FilterExpr `json:"filters"`
		} `json:"applied"`
		Traces  []ftRow `json:"traces"`
		Count   int     `json:"count"`
		HasMore bool    `json:"has_more"`
		PageURL string  `json:"page_url"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Window["from_ms"] != from.UnixMilli() || got.Window["to_ms"] != to.UnixMilli() || got.Entity.Namespace != "shop" ||
		got.Applied.Endpoint["operation"] != "POST /api/orders" || got.Applied.Endpoint["route"] != "/api/orders" ||
		got.Applied.ClientIP.Value != "192.0.2.10" || got.Applied.ClientIP.Match != "exact" || len(got.Applied.ClientIP.Keys) != len(ClientIPKeys) ||
		len(got.Applied.Filters) != 1 || !got.HasMore || got.Count == 0 || got.Count > ftMaxRows || got.Traces[0].StartMs != from.Add(time.Minute).UnixMilli() {
		t.Fatalf("şekil: %s", b)
	}
	u, err := url.Parse(got.PageURL)
	if err != nil || u.Path != "/traces" || u.Query().Get("service") != "orders" || u.Query().Get("filters") != "" || u.Query().Get("hasError") != "true" {
		t.Fatalf("page_url %q", got.PageURL)
	}
	var g chstore.FilterGroup
	if err := json.Unmarshal([]byte(u.Query().Get("filterGroup")), &g); err != nil || g.Join != "AND" || len(g.Filters) != 2 ||
		len(g.Groups) != 1 || g.Groups[0].Join != "OR" || g.Groups[0].Filters[0].Values[0] != "192.0.2.10" {
		t.Fatalf("filterGroup %q %v", u.Query().Get("filterGroup"), err)
	}
	// Süzgeçsiz arama: gövde ve bağlantı eski şekilde (applied yok, filters= düz).
	p2, _ := planFindTraces(findTracesArgs{Service: "orders", Operation: "GET /x"}, true)
	out2 := findTracesPayload(p2, p2.traceFilter(from, to), nil, false, from, to)
	if _, has := out2["applied"]; has || !strings.Contains(out2["page_url"].(string), "filters=") || strings.Contains(out2["page_url"].(string), "filterGroup") {
		t.Fatalf("eski şekil bozuldu: %v", out2)
	}
}

// Önbellek anahtarı istemci IP'sini de özetler (aynı uzunlukta farklı adres çakışmaz).
func TestFindTracesCacheKeyClientIP(t *testing.T) {
	to := time.Unix(1_700_000_000, 0)
	a, _ := planFindTraces(findTracesArgs{Service: "orders", ClientIP: "192.0.2.10"}, true)
	b, _ := planFindTraces(findTracesArgs{Service: "orders", ClientIP: "192.0.2.11"}, true)
	c, _ := planFindTraces(findTracesArgs{Service: "orders", ClientIP: "192.0.2."}, true)
	c.WindowS = a.WindowS
	if findTracesCacheKey(a, to) == findTracesCacheKey(b, to) || findTracesCacheKey(a, to) == findTracesCacheKey(c, to) {
		t.Fatal("IP anahtara girmiyor")
	}
}

// search_logs: yalnız tam adres; ES'e giden metin words ile AYNI tek gövde ifadesi.
func TestPlanSearchLogsClientIP(t *testing.T) {
	p, err := planSearchLogs(cosreSearchLogsArgs{Service: "checkout", ClientIP: "198.51.100.7"})
	if err != nil || p.Search != `"198.51.100.7"` || p.ClientIP != "198.51.100.7" {
		t.Fatalf("yalnız IP: %+v %v", p, err)
	}
	p, _ = planSearchLogs(cosreSearchLogsArgs{Service: "checkout", Words: "timeout", ClientIP: "2001:db8::7"})
	if f := p.filter(time.Unix(0, 0), time.Unix(60, 0)); f.Search != `"timeout" AND "2001:db8::7"` {
		t.Fatalf("words + IP: %q", f.Search)
	}
	for _, bad := range []string{"198.51.100.", "198.51.100.0/24", "198.51.100.*"} {
		if _, err := planSearchLogs(cosreSearchLogsArgs{Service: "checkout", ClientIP: bad}); searchErrClass(err) != ErrBadClientIP {
			t.Errorf("%q logda reddedilmeli: %v", bad, err)
		}
	}
	from, to := time.UnixMilli(1_794_900_000_000), time.UnixMilli(1_794_903_600_000)
	p, _ = planSearchLogs(cosreSearchLogsArgs{Service: "checkout", ClientIP: "198.51.100.7"})
	if u := SearchLogsPageURL(p, from, to); !strings.Contains(u, url.QueryEscape(`"198.51.100.7"`)) {
		t.Fatalf("logs bağlantısı aynı ifadeyi aramalı: %s", u)
	}
	// IP'siz plan eski metin (bayt).
	p, _ = planSearchLogs(cosreSearchLogsArgs{Service: "checkout", Words: "timeout"})
	if p.Search != `"timeout"` {
		t.Fatalf("words: %q", p.Search)
	}
}

// find_attribute_by_value: IP değeri "ip" şekli, istemci adresi anahtarları önce.
func TestValueShapeIP(t *testing.T) {
	if valueShape("192.0.2.10") != "ip" || valueShape("2001:db8::1") != "ip" || valueShape("apigateway.example.com") != "host" {
		t.Fatal("ip şekli")
	}
	if ks := dictionaryKeys("ip"); len(ks) < len(ClientIPKeys) || ks[0] != "client.address" {
		t.Fatalf("ip sözlüğü %v", ks)
	}
}

// Durum köprüsü: süzgeç → durum → argüman gidiş-dönüş; IP ayrıştırma.
func TestSearchStateBridge(t *testing.T) {
	in := []SearchFilter{{Key: "tenant.tier", Value: "gold"}, {Key: "plan", Op: "in", Values: []string{"a", "b"}}, {Key: "x", Op: "exists"}}
	back := V2SearchFiltersFrom(V2SearchFilterExprs(in))
	if b1, b2 := mustJSON(t, back), mustJSON(t, []SearchFilter{{Key: "tenant.tier", Op: "eq", Value: "gold"}, {Key: "plan", Op: "in", Values: []string{"a", "b"}}, {Key: "x", Op: "exists"}}); b1 != b2 {
		t.Fatalf("gidiş-dönüş %s ≠ %s", b1, b2)
	}
	if V2SearchFilterExprs([]SearchFilter{{Key: "a", Op: "regex", Value: "x"}}) != nil {
		t.Fatal("geçersiz süzgeç duruma yazılmamalı")
	}
	if v, pre, ok := ParseClientIP(" 198.51.100.7 ", true); !ok || pre || v != "198.51.100.7" {
		t.Fatal("tam adres")
	}
	if _, _, ok := ParseClientIP("192.0.2.", false); ok {
		t.Fatal("önek logda yok")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
