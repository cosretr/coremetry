package anomaly

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2452 — the explain replay and the DB / queue subject reads go
// through WeighSQL; with ingest.sampling.mode off (the default) the SQL
// stays byte-identical.
func TestSubjectAndExplainReadsModeOffIdentical(t *testing.T) {
	if chstore.CurrentIngestSampling().Mode == chstore.SamplingModeOn {
		t.Skip("test process runs with sampling mode on")
	}
	st := &chstore.Store{}
	from := time.Unix(1_800_000_000, 0)
	for _, m := range []string{"error_rate", "request_rate", "p99_ms"} {
		vexpr, err := metricValueExpr(m)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{buildExplainSpanQuery(vexpr)} {
			if st.WeighSQL(q, from, "service_summary_5m") != q {
				t.Fatalf("%s explain: SQL changed with mode off", m)
			}
		}
		for _, src := range subjectCHSources {
			for _, q := range []string{buildSubjectBucketsQuery(src, vexpr, 2), buildSubjectSeasonalQuery(src, vexpr, "UTC", 2)} {
				if st.WeighSQL(q, from, src.Table) != q || st.WeighSQL(q, time.Time{}, src.Table) != q {
					t.Fatalf("%s %s: SQL changed with mode off", m, src.Table)
				}
			}
		}
	}
	if !chstore.BaselineFloor(from, "db_summary_5m").Equal(from) {
		t.Fatal("BaselineFloor must be identity with mode off")
	}
}
