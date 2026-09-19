package api

import (
	"os"
	"strings"
	"testing"
)

// v0.10.810 — kablolama pini: stub bulununca önce TTL kararı, TTL içindeyse
// tüm-replika yedek okuma; boşsa stubReason=replica_miss, TTL dışında aged_out.
func TestTraceDetailReplicaMissWired(t *testing.T) {
	src, err := os.ReadFile("trace_routes.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	iStub := strings.Index(s, "s.store.GetTraceAggregateStub(ctx, id); ok {")
	iAged := strings.Index(s, "if !s.traceAgedOut(ctx, stub.StartTimeNs) {")
	iAll := strings.Index(s, "s.store.GetTraceAllReplicas(ctx, id, stub.StartTimeNs, stub.EndTimeNs)")
	iMiss := strings.Index(s, `"stubReason": "replica_miss"`)
	iOld := strings.Index(s, `"stubReason": "aged_out"`)
	if iStub < 0 || iAged < 0 || iAll < 0 || iMiss < 0 || iOld < 0 {
		t.Fatalf("eksik kablo: stub=%d aged=%d all=%d miss=%d old=%d", iStub, iAged, iAll, iMiss, iOld)
	}
	if !(iStub < iAged && iAged < iAll && iAll < iMiss && iMiss < iOld) {
		t.Error("sıra: stub → TTL kararı → tüm replika → replica_miss → aged_out")
	}
	if !strings.Contains(s, `"source": traceSourceAllReplicas, "replicaMiss": true`) {
		t.Error("tüm-replika isabeti kaynak + replicaMiss taşımalı")
	}
	if traceSourceAllReplicas != "clickhouse_all_replicas" {
		t.Error("FE union ile aynı dize")
	}
}
