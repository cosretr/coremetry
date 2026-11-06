package loginsummary

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Nötr fikstür adları (kurum/banka/host/ekip adı yok).
func fixtureEdges() []chstore.ServiceTopologyEdge {
	e := func(parent, child, kind string, calls uint64) chstore.ServiceTopologyEdge {
		return chstore.ServiceTopologyEdge{ParentService: parent, ChildNode: child, NodeKind: kind, Calls: calls}
	}
	return []chstore.ServiceTopologyEdge{
		e("api-gateway", "bff-web", "service", 1000),
		e("bff-web", "orders-api", "service", 800),
		e("bff-web", "payments-mobile", "service", 400),
		e("branch-teller", "orders-api", "service", 10),
		e("branch-teller", "payments-mobile", "service", 10),
		e("branch-teller", "bff-web", "service", 10),
		e("orders-api", "db:oracle@db-host-1.acme.internal", "db", 500),
		e("orders-api", "queue:kafka@orders-topic", "queue", 300),
		e("payments-mobile", "ext:partner.example.com", "external", 100),
		e("orders-api", "node:worker-1", "node", 50),
		e("orders-api", "orders-api", "service", 5), // öz-döngü
	}
}

// leakNeedles — hiçbir kipte cevapta geçmemesi gereken dizgiler.
var internalNeedles = []string{
	"db-host-1", "orders-topic", "partner.example.com", "worker-1", "acme", "ext:", "node:", "db:", "queue:", "@",
}
var serviceNeedles = []string{"orders-api", "payments-mobile", "api-gateway", "bff-web", "branch-teller"}

func u64(v uint64) *uint64 { return &v }

func baseSnapshot(now time.Time) *Snapshot {
	sil := PickSilhouette(fixtureEdges(), 180)
	salt := "00112233445566778899aabbccddeeff"
	ring := NewRing()
	nowSec := now.Unix()
	var ptsRate []chstore.SpanMetricPoint
	for i := 0; i < 10; i++ {
		t := (nowSec - nowSec%300 - int64(10-i)*300) * 1e9
		ptsRate = append(ptsRate, chstore.SpanMetricPoint{Time: t, Value: 102 + float64(i)})
	}
	ring.MergeRate([]chstore.SpanMetricSeries{{Points: ptsRate}})
	return &Snapshot{
		V: SnapshotVersion, GeneratedAt: now.UnixMilli(), Salt: salt,
		SpansToday: u64(5000), TracesToday: u64(900), Services: u64(5),
		Ring:     ring,
		Activity: &SnapActivity{Logins: 12, TracesViewed: 34, CosreQuestions: 5, Rollouts: u64(7)},
		Topo:     BuildTopology(sil, salt),
		State:    StateOK, CheckedAt: now.UnixMilli(), CHBytes: u64(1 << 40),
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertAbsent(t *testing.T, haystack string, needles []string, ctx string) {
	t.Helper()
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			t.Errorf("%s: %q cevapta geçmemeli: %s", ctx, n, haystack)
		}
	}
}
