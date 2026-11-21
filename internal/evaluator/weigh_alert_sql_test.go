package evaluator

import (
	"testing"
	"time"
)

type fakeWeigher struct{ on bool }

func (f fakeWeigher) WeighSQL(sql string, _ time.Time, _ ...string) string {
	if f.on {
		return "MV:" + sql
	}
	return sql
}

func (f fakeWeigher) WeighRawSQL(sql string) string {
	if f.on {
		return "RAW:" + sql
	}
	return sql
}

// v0.10.2442 — sub-5m alert windows (raw spans) are weighted like the MV
// path; mode off leaves both byte-identical.
func TestWeighAlertSQLRoutesBothPaths(t *testing.T) {
	const sql = "SELECT count() FROM spans"
	now := time.Now()
	if got := weighAlertSQL(fakeWeigher{}, sql, false, now, "service_summary_5m"); got != sql {
		t.Fatalf("off raw: %s", got)
	}
	if got := weighAlertSQL(fakeWeigher{}, sql, true, now, "service_summary_5m"); got != sql {
		t.Fatalf("off mv: %s", got)
	}
	if got := weighAlertSQL(fakeWeigher{on: true}, sql, false, now, "service_summary_5m"); got != "RAW:"+sql {
		t.Fatalf("raw path must use WeighRawSQL: %s", got)
	}
	if got := weighAlertSQL(fakeWeigher{on: true}, sql, true, now, "service_summary_5m"); got != "MV:"+sql {
		t.Fatalf("mv path must use WeighSQL: %s", got)
	}
}
