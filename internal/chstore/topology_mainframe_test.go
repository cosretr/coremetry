package chstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// v0.10.2414 — mainframe / IBM MQ topology contract.
//
// The OTLP converter (internal/otlp/mainframe.go) turns a CTG call into
// kind=client + peer_service=mainframe and IBM MQ-over-JMS into
// msg_system=ibmmq. It changes no SQL: the infra pass of
// WriteTopologyBucket already turns those columns into ext:mainframe and
// queue:ibmmq:<queue> nodes, and External dependencies read those nodes
// from topology_edges_5m (node_kind='external'). This test pins the
// branches the converter relies on, so a rewrite of the infra pass that
// drops them fails here instead of silently hiding mainframe calls.

type execRecorder struct {
	driver.Conn
	sqls []string
}

func (r *execRecorder) Exec(_ context.Context, q string, _ ...any) error {
	r.sqls = append(r.sqls, q)
	return nil
}

func TestTopologyInfraPassKeepsMainframeAndMQBranches(t *testing.T) {
	rec := &execRecorder{}
	s := &Store{conn: rec}
	if err := s.WriteTopologyBucket(context.Background(), time.Unix(1_700_000_000, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	var infra string
	for _, q := range rec.sqls {
		if strings.Contains(q, "AS unanswered") {
			infra = q
		}
	}
	if infra == "" {
		t.Fatalf("infra pass not found among %d statements", len(rec.sqls))
	}
	// Drop "-- …" comments (the pass is heavily annotated between
	// branches), then collapse whitespace.
	lines := strings.Split(infra, "\n")
	for i, l := range lines {
		if j := strings.Index(l, "--"); j >= 0 {
			lines[i] = l[:j]
		}
	}
	norm := strings.Join(strings.Fields(strings.Join(lines, "\n")), " ")
	for _, want := range []string{
		// CTG agent span after the converter: kind=client, peer_service=mainframe.
		"peer_service != '' AND kind = 'client', concat('ext:',",
		// IBM MQ via JMS after the converter: msg_system=ibmmq, per queue.
		"msg_system != '' AND kind != 'consumer' AND msg_dest != '', concat('queue:', msg_system, ':', msg_dest)",
		"startsWith(child, 'ext:'), 'external'",
	} {
		if !strings.Contains(norm, want) {
			t.Errorf("infra pass lost branch %q", want)
		}
	}
	// The peer_service branch must come before the semconv fallback, which
	// needs http/rpc + an unanswered leaf and would otherwise decide first.
	if strings.Index(norm, "peer_service != '' AND kind = 'client'") > strings.Index(norm, "AND unanswered") {
		t.Error("peer_service branch must precede the server.address fallback")
	}
}
