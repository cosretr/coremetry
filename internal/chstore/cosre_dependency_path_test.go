package chstore

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// v0.10.2453 — dependency_path okumalarının SAF SQL sözleşmesi: MV-first
// (atlama topology_edges_5m FINAL, deploy service_version_5m, çağıran RED
// service_summary_5m), zaman sınırı + LIMIT + max_execution_time; tek ham
// okuma (örnek trace) servis önekli + satır tavanlı + GLOBAL IN.

func TestDependencyHopSQLShape(t *testing.T) {
	cur := PeriodWindow{From: time.Date(2026, 11, 22, 9, 2, 0, 0, time.UTC), To: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)}
	ref := PeriodWindow{From: cur.From.Add(-24 * time.Hour), To: cur.To.Add(-24 * time.Hour)}
	for _, c := range []struct {
		up    bool
		env   string
		known string
	}{{false, "", "parent_service"}, {true, "", "child_node"}, {false, "prod", "parent_service"}, {true, "prod", "child_node"}} {
		sql := DependencyHopSQL(c.up, c.env != "")
		args := DependencyHopArgs(DepHopQuery{Up: c.up, Nodes: []string{"svc-a", "queue:kafka:t1"}, Env: c.env, Cur: cur, Ref: ref})
		if got := strings.Count(sql, "?"); got != len(args) {
			t.Errorf("up=%v env=%q: %d yer tutucu, %d bind", c.up, c.env, got, len(args))
		}
		for _, want := range []string{"FROM topology_edges_5m FINAL", "AND " + c.known + " IN ?", "LIMIT " + strconv.Itoa(DepHopPerNode) + " BY " + c.known,
			"LIMIT ?", "max_execution_time = 5", "time_bucket >= ? AND time_bucket < ?", TopoCallEdgeFilterSQL} {
			if !strings.Contains(sql, want) {
				t.Errorf("up=%v: %q yok", c.up, want)
			}
		}
		if strings.Contains(sql, "FROM spans") {
			t.Error("atlama ham span okumamalı")
		}
		if args[0] != alignBucketStart(cur.From) || args[0].(time.Time).Minute() != 0 {
			t.Errorf("cur_from 5 dk kovasına hizalı olmalı: %v", args[0])
		}
		if args[len(args)-1] != 2*DepHopPerNode {
			t.Errorf("toplam LIMIT %v", args[len(args)-1])
		}
	}
}

func TestDependencyReadsBounded(t *testing.T) {
	d := dependencyDeploysSQL()
	if !strings.Contains(d, "FROM service_version_5m") || !strings.Contains(d, "LIMIT ?") || !strings.Contains(d, "max_execution_time") || strings.Count(d, "?") != 6 {
		t.Errorf("deploy okuması:\n%s", d)
	}
	r := dependencyServiceREDSQL()
	if !strings.Contains(r, "FROM service_summary_5m") || !strings.Contains(r, "LIMIT ?") || strings.Count(r, "?") != 7 {
		t.Errorf("çağıran RED okuması:\n%s", r)
	}
	from, to := time.Unix(0, 0), time.Unix(900, 0)
	for _, q := range []EdgeSampleQuery{
		{Parent: "svc-a", Child: "svc-b"}, {Parent: "svc-a", Child: "db:postgres@pg-1"}, {Parent: "svc-a", Child: "queue:kafka:t1"},
		{Parent: "queue:kafka:t1", Child: "svc-c"}, {Parent: "svc-a", Child: "ext:api.example.com", PreferError: true},
	} {
		q.From, q.To = from, to
		sql, args, ok := edgeSampleSQL(q)
		if !ok {
			t.Fatalf("%+v: okuma yok", q)
		}
		if strings.Count(sql, "?") != len(args) {
			t.Errorf("%s>%s: %d yer tutucu, %d bind", q.Parent, q.Child, strings.Count(sql, "?"), len(args))
		}
		for _, want := range []string{"PREWHERE service_name = ?", "time >= ? AND time < ?", "LIMIT 1", "max_execution_time",
			"max_rows_to_read = " + strconv.Itoa(LookupScopedRowsMax), "read_overflow_mode = 'break'"} {
			if !strings.Contains(sql, want) {
				t.Errorf("%s>%s: %q yok", q.Parent, q.Child, want)
			}
		}
		if strings.Contains(sql, "SELECT trace_id FROM spans\n    PREWHERE") && !strings.Contains(sql, "GLOBAL IN (SELECT") {
			t.Error("alt sorgu GLOBAL IN olmalı")
		}
		if q.PreferError && !strings.Contains(sql, "status_code = 'error' DESC") {
			t.Error("hata kenarında hatalı span önce gelmeli")
		}
	}
	if _, _, ok := edgeSampleSQL(EdgeSampleQuery{Parent: "svc-a", Child: "node:worker-1"}); ok {
		t.Error("node: kenarı için örnek okuması olmamalı")
	}
	if !strings.Contains(func() string { s, _, _ := edgeSampleSQL(EdgeSampleQuery{Parent: "svc-a", Child: "svc-b"}); return s }(), "trace_id GLOBAL IN (SELECT") {
		t.Error("servis→servis örnek okuması GLOBAL IN alt sorgusu taşımalı")
	}
}
