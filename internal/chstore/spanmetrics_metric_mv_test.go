package chstore

import (
	"os"
	"strings"
	"testing"
)

func TestSpanmetricsMetricFamily(t *testing.T) {
	cases := []struct {
		metric string
		calls  bool
		want   string
	}{
		{"traces.span.metrics.calls", true, spanmetricsFamilyConnector},
		{"traces_span_metrics_calls_total", true, spanmetricsFamilyConnector},
		{"traces.span.metrics.duration", false, spanmetricsFamilyConnector},
		{"traces_span_metrics_duration_seconds", false, spanmetricsFamilyConnector},
		{"traces_spanmetrics_calls_total", true, spanmetricsFamilyLegacy},
		{"traces.spanmetrics.duration", false, spanmetricsFamilyLegacy},
		{"calls", true, spanmetricsFamilyLegacy},
		{"duration", false, spanmetricsFamilyLegacy},
		// counter names never feed the duration MVs and vice versa
		{"traces.span.metrics.calls", false, ""},
		{"traces.span.metrics.duration", true, ""},
		{"http.server.request.duration", false, ""},
	}
	for _, c := range cases {
		if got := spanmetricsMetricFamily(c.metric, c.calls); got != c.want {
			t.Errorf("%s calls=%v: got %q want %q", c.metric, c.calls, got, c.want)
		}
	}
}

// Every name in the lists appears in its MV's WHERE and the connector list
// drives the family expression; families do not overlap.
func TestSpanmetricsMetricMVDDL(t *testing.T) {
	for _, c := range []struct {
		ddl          string
		conn, legacy []string
	}{
		{spanmetricsCallsMVDDL(), spanmetricsConnectorCalls, spanmetricsLegacyCalls},
		{spanmetricsHistMVDDL(), spanmetricsConnectorDuration, spanmetricsLegacyDuration},
		{spanmetricsDurationMVDDL(), spanmetricsConnectorDuration, spanmetricsLegacyDuration},
	} {
		name := mvNameFromDDL(c.ddl)
		for _, n := range append(append([]string{}, c.conn...), c.legacy...) {
			if !strings.Contains(c.ddl, "'"+n+"'") {
				t.Errorf("%s: name %q missing", name, n)
			}
		}
		for _, n := range c.conn {
			for _, l := range c.legacy {
				if n == l {
					t.Errorf("%s: %q in both families", name, n)
				}
			}
		}
		for _, want := range []string{
			"ORDER BY (service_name, time_bucket, source_family)",
			"AS source_family",
			"GROUP BY service_name, time_bucket, source_family",
			"'SPAN_KIND_SERVER', 'SPAN_KIND_CONSUMER'",
			"FROM metric_points",
		} {
			if !strings.Contains(c.ddl, want) {
				t.Errorf("%s: missing %q", name, want)
			}
		}
		if mvSelectBody(c.ddl) == "" {
			t.Errorf("%s: SELECT body not extractable (MODIFY QUERY)", name)
		}
		if mvDDLByName(canonicalMVs(), name) != c.ddl {
			t.Errorf("%s: canonicalMVs() does not carry the generated DDL", name)
		}
	}
	if d := spanmetricsCallsMVDDL(); !strings.Contains(d, "= 'STATUS_CODE_ERROR'") || !strings.Contains(d, "'status_code'") {
		t.Errorf("calls: error predicate must read status.code / status_code")
	}
	// ms normalisation: seconds-unit bounds, sum and max are scaled.
	if d := spanmetricsHistMVDDL(); !strings.Contains(d, "b * "+spanmetricsMsFactor) {
		t.Errorf("hist: bounds not normalised to ms")
	}
	if d := spanmetricsDurationMVDDL(); strings.Count(d, spanmetricsMsFactor) != 2 {
		t.Errorf("duration: sum and max must both be normalised to ms")
	}
}

func TestSpanmetricsKeyState(t *testing.T) {
	for _, c := range []struct {
		hosts, keyed uint64
		alter, err   bool
	}{
		{0, 0, false, true},
		{4, 0, true, false},
		{4, 4, false, false}, // idempotent: already keyed → no ALTER
		{4, 2, false, true},  // partial → refuse, never guess
		{1, 1, false, false},
	} {
		alter, err := spanmetricsKeyState(c.hosts, c.keyed)
		if alter != c.alter || (err != nil) != c.err {
			t.Errorf("%d/%d: alter=%v err=%v", c.keyed, c.hosts, alter, err)
		}
	}
}

func TestSpanmetricsInnerAlter(t *testing.T) {
	got := spanmetricsInnerAlter(".inner_id.abc", " ON CLUSTER c")
	want := "ALTER TABLE `.inner_id.abc` ON CLUSTER c ADD COLUMN IF NOT EXISTS source_family LowCardinality(String) CODEC(ZSTD(3)), MODIFY ORDER BY (service_name, time_bucket, source_family)"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Shared with the VM read path — the constants are the family's names.
func TestSpanmetricsSharedNames(t *testing.T) {
	if spanmetricsMetricFamily(SpanMetricsConnectorDuration, false) != spanmetricsFamilyConnector ||
		spanmetricsMetricFamily(SpanMetricsConnectorDurationProm, false) != spanmetricsFamilyConnector {
		t.Fatal("shared connector names must be in the connector duration list")
	}
}

// TestDumpSpanmetricsMVDDL — writes the DDL for a manual run against a
// scratch ClickHouse (SPANMETRICS_DDL_OUT=<file>); skipped otherwise.
func TestDumpSpanmetricsMVDDL(t *testing.T) {
	out := os.Getenv("SPANMETRICS_DDL_OUT")
	if out == "" {
		t.Skip("SPANMETRICS_DDL_OUT unset")
	}
	var b strings.Builder
	for _, d := range []string{spanmetricsCallsMVDDL(), spanmetricsHistMVDDL(), spanmetricsDurationMVDDL()} {
		b.WriteString(d + ";\n")
		b.WriteString("-- SELECT: " + strings.ReplaceAll(mvSelectBody(d), "\n", " ") + "\n")
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}
