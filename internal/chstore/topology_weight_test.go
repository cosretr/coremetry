package chstore

import (
	"os"
	"strings"
	"testing"
)

func TestTopoWeightSQLWith(t *testing.T) {
	cross := `INSERT INTO topology_edges_5m
			(time_bucket, calls,
			 sum_duration_ns, p99_ms, errors,
			 parent_env, child_env, version)
		SELECT toUInt64(count()) AS calls, x,
			toUInt64(countIf(c.status_code = 'error')) AS errors,
			any(p.env) AS parent_env
		FROM spans AS c`
	got := topoWeightSQLWith(cross)
	for _, frag := range []string{
		"sum_duration_ns, p99_ms, errors, w_calls, w_errors,",
		"AS errors,\n\t\t\ttoFloat64(sum(c.sample_weight)) AS w_calls,\n\t\t\ttoFloat64(sumIf(c.sample_weight, c.status_code = 'error')) AS w_errors,",
	} {
		if !strings.Contains(got, frag) {
			t.Fatalf("cross pass lacks %q:\n%s", frag, got)
		}
	}
	runsOn := "sum_duration_ns, p99_ms, errors,\n toUInt64(countIf(status_code = 'error')) AS errors,\n FROM (SELECT duration, status_code,\n p_env FROM spans)"
	if g := topoWeightSQLWith(runsOn); !strings.Contains(g, "duration, status_code, sample_weight,") || !strings.Contains(g, "sum(sample_weight)) AS w_calls") {
		t.Fatalf("runs-on pass:\n%s", g)
	}
	infra := "sum_duration_ns, p99_ms, errors,\n toUInt64(countIf(status_code = 'error')) AS errors,\n FROM (SELECT x,\n\t\t\t\tduration,\n\t\t\t\tstatus_code, cl\n FROM spans)"
	if g := topoWeightSQLWith(infra); !strings.Contains(g, "\t\t\t\tstatus_code, sample_weight, cl") {
		t.Fatalf("infra pass:\n%s", g)
	}
	// Not a writer pass → untouched.
	if s := "SELECT 1"; topoWeightSQLWith(s) != s {
		t.Fatal("non-writer SQL rewritten")
	}
}

// Every WriteTopologyBucket pass goes through topoWeightSQL, and the
// unweighted service-map select is unchanged byte-for-byte.
func TestTopoWeightWrapsAllPasses(t *testing.T) {
	src, err := os.ReadFile("topology.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	i := strings.Index(body, "func (s *Store) WriteTopologyBucket(")
	j := strings.Index(body[i+1:], "\nfunc ")
	fn := body[i : i+1+j]
	if n := strings.Count(fn, "s.conn.Exec(ctx, s.topoWeightSQL(`"); n != 4 {
		t.Fatalf("WriteTopologyBucket: %d of 4 passes weighted", n)
	}
	if topoCallsSel(false) != "toUInt64(sum(calls)) AS total_calls,\n\t\t\t\ttoUInt64(sum(errors)) AS total_errors" {
		t.Fatal("mode-off service-map select changed")
	}
}
