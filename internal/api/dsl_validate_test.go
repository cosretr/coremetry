package api

import (
	"errors"
	"net/url"
	"testing"
)

// dsl_validate_test.go — v0.10.1117 (inceleme bulgusu). `dsl=` yaprakları
// doğrulanmadan ekleniyordu: op kısıtlı bir anahtarda (`http.route_shape ~ x`,
// op_group, db_stmt_hash) derleme hatası ApplyFilters'ta loglanıp ATLANIYOR,
// sorgu süzgeçsiz koşuyordu. Artık errBadRequest → 400 (çağıranların hepsi
// parseFiltersAndDSL hatasını StatusBadRequest ile yazar; dashboards bundle'ı
// hatayı slot gövdesine koyar, JSON filters ile aynı sözleşme).
func TestDSLFiltersValidatedAtBoundary(t *testing.T) {
	for _, dsl := range []string{
		"http.route_shape ~ users",
		"name_shape ~ process",
		"op_group ~ orders",
		"db_stmt_hash ~ 123",
		"service.name = svc-orders and http.route_shape !~ users",
	} {
		_, err := parseFiltersAndDSL("", dsl)
		if err == nil || !errors.Is(err, errBadRequest) {
			t.Errorf("%q: errBadRequest (400) bekleniyordu, %v", dsl, err)
		}
	}
	// /api/traces yolu: parseTraceFilter hatası handler'da 400 yazılır.
	q := url.Values{}
	q.Set("dsl", "http.route_shape ~ users")
	if _, err := parseTraceFilter(q); err == nil || !errors.Is(err, errBadRequest) {
		t.Fatalf("parseTraceFilter dsl LIKE: %v", err)
	}

	// Olağan DSL değişmedi: nitelik LIKE + şekil eşitliği geçer, JSON çiplerine eklenir.
	fs, err := parseFiltersAndDSL(`[{"k":"service.name","op":"=","v":["svc-orders"]}]`,
		"http.target ~ users and http.route_shape = /users/:id")
	if err != nil {
		t.Fatalf("olağan DSL reddedilmemeli: %v", err)
	}
	if len(fs) != 3 || fs[1].Key != "http.target" || fs[1].Op != "LIKE" || fs[2].Key != "http.route_shape" || fs[2].Op != "=" {
		t.Fatalf("süzgeçler: %+v", fs)
	}
	if fs, err := parseFiltersAndDSL("", "  "); err != nil || len(fs) != 0 {
		t.Fatalf("boş DSL: %+v %v", fs, err)
	}
}
