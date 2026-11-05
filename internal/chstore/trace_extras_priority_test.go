package chstore

// v0.10.1805 regression (operator-reported): on /traces the added
// attribute columns (http.route, deployment.environment, any "+ Add
// column" key) showed a DIFFERENT value on every load for the same trace.
// traceExtrasProjection used any()/anyIf() over ALL spans of the trace, so
// the value came from an arbitrary span. Contract now, per key per trace:
//  1. the ROOT span's value,
//  2. else the ENTRY span (server/consumer) of the filtered service —
//     rank omitted when there is no service filter,
//  3. else the earliest-starting span with a non-empty value.
// Implemented as argMinIf(value, (rank, time), value-present).

import (
	"strings"
	"testing"
)

// traceExtrasNoSvcRank — the (rank, time) key without a service filter;
// shared by the older projection tests that pin exact fragments.
var traceExtrasNoSvcRank = "(multiIf(" + rootSpanPredicate + ", 0, 2), time)"

func TestTraceExtrasProjection_DeterministicPriority(t *testing.T) {
	rankNoSvc := traceExtrasNoSvcRank
	rankSvc := "(multiIf(service_name = ? AND kind IN ('server', 'consumer'), 0, service_name = ?, 1, " + rootSpanPredicate + ", 2, 3), time)"
	arrVal := "coalesce(nullIf(attr_values[indexOf(attr_keys, ?)], ''), res_values[indexOf(res_keys, ?)])"
	arrHas := "(attr_values[indexOf(attr_keys, ?)] != '' OR res_values[indexOf(res_keys, ?)] != '')"

	cases := []struct {
		name     string
		key      string
		service  string
		wantSel  string
		wantArgs []any
	}{
		{"well-known, no service", "http.route", "",
			", argMinIf(http_route, " + rankNoSvc + ", http_route != '') AS extra_0", nil},
		{"well-known, service", "deployment.environment", "orders-api",
			", argMinIf(deploy_env, " + rankSvc + ", deploy_env != '') AS extra_0", []any{"orders-api", "orders-api"}},
		{"cluster alias, no service", "k8s.cluster.name", "",
			", argMinIf(cluster, " + rankNoSvc + ", cluster != '') AS extra_0", nil},
		{"status, service", "http.status_code", "acme-gateway",
			", argMinIf(toString(http_status), " + rankSvc + ", http_status != 0) AS extra_0", []any{"acme-gateway", "acme-gateway"}},
		{"array key, no service", "order.id", "",
			", argMinIf(" + arrVal + ", " + rankNoSvc + ", " + arrHas + ") AS extra_0",
			[]any{"order.id", "order.id", "order.id", "order.id"}},
		{"array key, service", "order.id", "orders-api",
			", argMinIf(" + arrVal + ", " + rankSvc + ", " + arrHas + ") AS extra_0",
			[]any{"order.id", "order.id", "orders-api", "orders-api", "order.id", "order.id"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sel, args := traceExtrasProjection([]string{c.key}, c.service)
			if sel != c.wantSel {
				t.Fatalf("projection:\n got %s\nwant %s", sel, c.wantSel)
			}
			if len(args) != len(c.wantArgs) {
				t.Fatalf("args: got %v want %v", args, c.wantArgs)
			}
			for i := range args {
				if args[i] != c.wantArgs[i] {
					t.Fatalf("args[%d]: got %v want %v (placeholder order)", i, args[i], c.wantArgs[i])
				}
			}
			for _, banned := range []string{"any(", "anyIf("} {
				if strings.Contains(sel, banned) {
					t.Fatalf("non-deterministic %s survived: %s", banned, sel)
				}
			}
			if (c.service == "") == strings.Contains(sel, "service_name = ?") {
				t.Fatalf("service rank presence wrong for service=%q: %s", c.service, sel)
			}
		})
	}
}

// Promoted columns take the same priority; SQL keeps its hard bounds and
// the service bind sits in projection order (before from/to/ids).
func TestTraceExtrasSQL_PriorityKeepsBounds(t *testing.T) {
	withPromoted(t, "order.channel", "attr_order_channel")
	sql, args := traceExtrasSQL(2, []string{"order.channel", "http.route"}, "orders-api")
	for _, want := range []string{
		"argMinIf(attr_order_channel, (multiIf(service_name = ? AND kind IN ('server', 'consumer'), 0, service_name = ?, 1, " + rootSpanPredicate + ", 2, 3), time), attr_order_channel != '') AS extra_0",
		"argMinIf(http_route, ",
		"time >= ? AND time <= ? AND trace_id IN (?,?)",
		"GROUP BY trace_id",
		"LIMIT 2",
		"max_execution_time = 15",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %q in:\n%s", want, sql)
		}
	}
	if len(args) != 4 || args[0] != "orders-api" || args[3] != "orders-api" {
		t.Fatalf("two service binds per key expected, got %v", args)
	}
}

// RequireServices drops Service in GetTraces, so it must not rank either.
func TestTraceExtrasService(t *testing.T) {
	cases := []struct {
		f    TraceFilter
		want string
	}{
		{TraceFilter{}, ""},
		{TraceFilter{Service: "orders-api"}, "orders-api"},
		{TraceFilter{Service: "orders-api", RequireServices: []string{"acme-gateway"}}, ""},
	}
	for _, c := range cases {
		if got := traceExtrasService(c.f); got != c.want {
			t.Errorf("%+v: got %q want %q", c.f, got, c.want)
		}
	}
}
