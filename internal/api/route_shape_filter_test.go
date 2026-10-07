package api

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// route_shape_filter_test.go — v0.10.1117. /endpoints "Group by shape" satırı
// (ve endpoint detay sayfası) /traces'i `http.route_shape = <şekil>` çipiyle
// açar (RPC sekmesi + şekil: `name_shape`). Sınır: anahtar kabul edilir ve
// endpoints sayfasının grupladığı opSigWrap ifadesine derlenir (desenler +
// değer bağlı); kimlik dışı op 400. /api/traces, /count, /aggregate,
// /error-histogram ve metric-batch aynı ayrıştırıcıdan (parseFilters /
// parseFilterGroup) geçer.
func TestRouteShapeFilterAcceptedAtBoundary(t *testing.T) {
	q := url.Values{}
	q.Set("service", "svc-orders")
	q.Set("filters", `[{"k":"http.route_shape","op":"=","v":["/users/:id"]}]`)
	f, err := parseTraceFilter(q)
	if err != nil {
		t.Fatalf("http.route_shape kabul edilmeli: %v", err)
	}
	if len(f.Filters) != 1 || f.Filters[0].Key != "http.route_shape" {
		t.Fatalf("süzgeç korunmalı: %+v", f.Filters)
	}
	sql, args, err := f.Filters[0].SQL()
	if err != nil || !strings.HasPrefix(sql, "replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(http_route, ?") ||
		!strings.HasSuffix(sql, " = ?") || len(args) != 4 || args[3] != "/users/:id" {
		t.Fatalf("opSigWrap(http_route) = ? derlenmeli: %q %#v %v", sql, args, err)
	}
	if _, err := parseFilterGroup(`{"join":"OR","filters":[{"k":"name_shape","op":"IN","v":["process order/:id"]}]}`); err != nil {
		t.Fatalf("gruplu kök de kabul edilmeli: %v", err)
	}
	for _, bad := range []string{
		`[{"k":"http.route_shape","op":"LIKE","v":["users"]}]`,
		`[{"k":"name_shape","op":"=~","v":["process.*"]}]`,
	} {
		_, err = parseFilters(bad)
		if err == nil || !errors.Is(err, errBadRequest) || !strings.Contains(err.Error(), "supports only") {
			t.Fatalf("%s 400 olmalı: %v", bad, err)
		}
	}
}
