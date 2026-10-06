package api

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// op_group_filter_test.go — v0.10.1115. Service › Operations (Normalized) satırı
// /traces'i `op_group = <değer>` çipiyle açar. Bu paket hiç Store kurmaz, yani
// chstore'un kolon probu bayrağı kapalıdır — "op_group kolonu olmayan kurulum"
// (dış Distributed `spans`, cluster_name boş) hâli. Orada Normalized tablo ham
// span adlarını gösterir; süzgeç `name`e düşer ve istek 400 ALMAZ (bugün
// çalışan tıklama kırılmaz). Op kısıtı (=, !=, IN, NOT IN) her hâlde 400'le
// korunur. /api/traces, /count, /aggregate, /error-histogram ve metric-batch
// aynı ayrıştırıcıdan (parseFilters / parseFilterGroup) geçer.
func TestOpGroupFilterWithoutColumnIsAccepted(t *testing.T) {
	q := url.Values{}
	q.Set("service", "svc-orders")
	q.Set("filters", `[{"k":"op_group","op":"=","v":["GET /orders/8421"]}]`)
	f, err := parseTraceFilter(q)
	if err != nil {
		t.Fatalf("kolonsuz kurulumda op_group 400 olmamalı (name'e düşer): %v", err)
	}
	if len(f.Filters) != 1 || f.Filters[0].Key != "op_group" {
		t.Fatalf("süzgeç korunmalı: %+v", f.Filters)
	}
	sql, args, err := f.Filters[0].SQL()
	if err != nil || sql != "name = ?" || len(args) != 1 || args[0] != "GET /orders/8421" {
		t.Fatalf("kolonsuz kurulumda name'e derlenmeli: %q %#v %v", sql, args, err)
	}
	if _, err := parseFilterGroup(`{"join":"OR","filters":[{"k":"op_group","op":"IN","v":["GET /orders/8421"]}]}`); err != nil {
		t.Fatalf("gruplu kök de kabul edilmeli: %v", err)
	}
	// Kimlik dışı op her hâlde 400 (op mesajı).
	_, err = parseFilters(`[{"k":"op_group","op":"LIKE","v":["orders"]}]`)
	if err == nil || !errors.Is(err, errBadRequest) || !strings.Contains(err.Error(), "supports only") {
		t.Fatalf("LIKE 400 olmalı: %v", err)
	}
}
