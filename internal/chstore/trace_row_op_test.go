package chstore

// trace_row_op_test.go — v0.10.1807 regression.
//
// Operator-reported (2026-11-05): /traces?service=orders-api'de Name kolonu
// kök span'in (gateway) adını, kök span'i olmayan trace'te "—" gösteriyordu.
// Kural: servis süzgeci varken o servisin giriş span'i > servisin herhangi
// span'i > kök > en erken; süzgeçsiz kök > en erken (traceSpanRank tek gövde).

import (
	"os"
	"strings"
	"testing"
)

func TestTraceRowOpSQL(t *testing.T) {
	cases := []struct {
		name     string
		service  string
		wantKey  string
		wantArgs []any
	}{
		{"with service", "orders-api",
			"(multiIf(service_name = ? AND kind IN ('server', 'consumer'), 0, service_name = ?, 1, " + rootSpanPredicate + ", 2, 3), time, span_id)",
			[]any{"orders-api", "orders-api", "orders-api", "orders-api", "orders-api", "orders-api"}},
		{"without service", "",
			"(multiIf(" + rootSpanPredicate + ", 0, 2), time, span_id)", []any{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sql, args := traceRowOpSQL(3, c.service)
			for _, want := range []string{
				"argMinIf(name, " + c.wantKey + ", name != '') AS op_name",
				"argMinIf(http_route, " + c.wantKey + ", name != '') AS op_route",
				"argMinIf(service_name, " + c.wantKey + ", name != '') AS op_service",
				"FROM spans WHERE time >= ? AND time <= ? AND trace_id IN (?,?,?)",
				"GROUP BY trace_id LIMIT 3", "SETTINGS max_execution_time = 15",
			} {
				if !strings.Contains(sql, want) {
					t.Fatalf("missing %q:\n%s", want, sql)
				}
			}
			if len(args) != len(c.wantArgs) {
				t.Fatalf("args = %v, want %v", args, c.wantArgs)
			}
			for i := range args {
				if args[i] != c.wantArgs[i] {
					t.Fatalf("args = %v, want %v", args, c.wantArgs)
				}
			}
			// Placeholder count = projection args + from/to + ids.
			if got, want := strings.Count(sql, "?"), len(args)+2+3; got != want {
				t.Fatalf("placeholders = %d, want %d", got, want)
			}
		})
	}
}

// Ek kolonlar (1806) ile satır etiketi aynı sırayı kullanır: tek gövde.
func TestTraceRowOpRankSharesExtrasRule(t *testing.T) {
	for _, svc := range []string{"", "orders-api"} {
		ex, _ := traceExtrasRankKey(svc)
		op, _ := traceRowOpRankKey(svc)
		if strings.TrimSuffix(ex, ")")+", span_id)" != op {
			t.Fatalf("service=%q: row key %q diverged from extras key %q", svc, op, ex)
		}
	}
	if traceRowOpRankKeyNoService != "(multiIf("+rootSpanPredicate+", 0, 2), time, span_id)" {
		t.Fatalf("no-service key = %q", traceRowOpRankKeyNoService)
	}
	if !strings.Contains(buildGetTracesListSQL("WHERE 1", "", "trace_start", "DESC"), "argMinIf(name, "+traceRowOpRankKeyNoService) {
		t.Fatal("raw list root_name must use the no-service row key")
	}
}

func TestMergeTraceRowOps(t *testing.T) {
	rows := []TraceRow{
		{TraceID: "a", ServiceName: "acme-gateway", RootName: "GET /api/v1/login", RootRoute: "/api/v1/login"}, // gateway root
		{TraceID: "b", RootName: ""},                         // no root (MV)
		{TraceID: "c", RootName: "keep", RootRoute: "/keep"}, // read found nothing
		{TraceID: "d", RootName: "keep2"},                    // read found no named span
	}
	mergeTraceRowOps(rows, map[string]traceRowOp{
		"a": {"POST", "/api/orders", "orders-api"},
		"b": {"POST", "/api/orders", ""},
		"d": {"", "", ""},
	})
	want := []TraceRow{
		{TraceID: "a", ServiceName: "orders-api", RootName: "POST", RootRoute: "/api/orders"}, // v0.10.1808 Service follows
		{TraceID: "b", RootName: "POST", RootRoute: "/api/orders"},
		{TraceID: "c", RootName: "keep", RootRoute: "/keep"},
		{TraceID: "d", RootName: "keep2"},
	}
	for i := range want {
		if rows[i].RootName != want[i].RootName || rows[i].RootRoute != want[i].RootRoute || rows[i].ServiceName != want[i].ServiceName {
			t.Fatalf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
}

// Kablolama: iki yol da süzgeç varken fill'i çağırır; ham yolda onarımdan
// SONRA, ek kolonlardan ÖNCE.
func TestGetTracesWiresServiceRowOps(t *testing.T) {
	body := funcBody(t, "repo.go", "func (s *Store) GetTraces(")
	if strings.Count(body, "s.fillServiceRowOps(ctx, out, f)") != 2 {
		t.Fatal("fillServiceRowOps must run on both the MV and the raw path")
	}
	mv := strings.Index(body, "s.getTracesFromMV(ctx, f)")
	i := strings.Index(body, "s.repairSpanScopedRows(ctx, out, f)")
	j := strings.LastIndex(body, "s.fillServiceRowOps(ctx, out, f)")
	k := strings.LastIndex(body, "s.fillTraceExtras(ctx, out, f.ExtraAttrs")
	first := strings.Index(body, "s.fillServiceRowOps(ctx, out, f)")
	if mv < 0 || !(mv < first && first < i && i < j && j < k) {
		t.Fatalf("order wrong: mv=%d mvFill=%d repair=%d rawFill=%d extras=%d", mv, first, i, j, k)
	}
	src, _ := os.ReadFile("trace_row_op.go")
	if !strings.Contains(string(src), "service := traceExtrasService(f)") {
		t.Fatal("fill must gate on traceExtrasService (RequireServices drops Service)")
	}
}
