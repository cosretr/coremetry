package chstore

import (
	"strings"
	"testing"
)

func TestServiceClusterEnvsSQL(t *testing.T) {
	q := serviceClusterEnvsSQL()
	for _, w := range []string{"FROM service_env_summary_5m", "cluster = ?", "service_name = ?", "LIMIT 50", "max_execution_time"} {
		if !strings.Contains(q, w) {
			t.Fatalf("eksik %q", w)
		}
	}
}
