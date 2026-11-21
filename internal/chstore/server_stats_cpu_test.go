package chstore

import (
	"os"
	"strings"
	"testing"
)

// v0.10.2434 — çekirdek sayısı ve ClickHouse süreç CPU'su panele gelir.
func TestServerStatsCPUCapacity(t *testing.T) {
	var st ServerStat
	applyServerMetric(&st, "CPUCores", 32)
	if st.CPUCores != 32 {
		t.Fatalf("CPUCores = %d", st.CPUCores)
	}
}

func TestServerStatsQueryCountsCores(t *testing.T) {
	b, err := os.ReadFile("server_stats.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{"'CPUCores'", "OSUserTimeCPU%%", "system.metric_log", "ProfileEvent_UserTimeMicroseconds"} {
		if !strings.Contains(src, want) {
			t.Fatalf("server_stats.go missing %q", want)
		}
	}
}
