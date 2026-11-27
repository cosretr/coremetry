package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

// TestTraceLogTimesInViewerZone — v0.10.2483: anlatımda LOGLAR saatleri
// izleyicinin diliminde ve UI'ın biçiminde (SS:DD:ss.mmm); bölümlü biçim UTC
// (bayt bayt). Künye dilimi adıyla söyler; UTC künyesi eskisinin aynısı.
func TestTraceLogTimesInViewerZone(t *testing.T) {
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	if traceLogLocFor(false, ist) != nil {
		t.Fatal("bölümlü → UTC (nil)")
	}
	if traceLogLocFor(true, ist) != ist || traceLogLocFor(true, nil) != tzdefault.Location() {
		t.Fatal("anlatım → izleyici, yoksa uygulamanın gösterim dilimi")
	}
	ts := time.Date(2026, 11, 26, 17, 37, 2, 44e6, time.UTC).UnixNano()
	logs := []*logstore.LogRecord{{Timestamp: ts, Severity: 17, SeverityText: "ERROR", ServiceName: "orders-bff", Body: "customerNo=0 rejected"}}
	lim := traceLogLimits{Entries: 30, BodyBytes: 600, StackFirst: 1500, StackRest: 900, Budget: 24000}

	utc := selectTraceLogs(logs, nil, lim, traceLogsHeader(1, 0, nil, nil))
	if !strings.Contains(utc.Block, "\n- 17:37:02.044 ERROR orders-bff") || !strings.Contains(utc.Block, "saatler UTC;") {
		t.Fatalf("UTC blok: %s", utc.Block)
	}
	lim.Loc = ist
	loc := selectTraceLogs(logs, nil, lim, traceLogsHeader(1, 0, nil, ist))
	if !strings.Contains(loc.Block, "\n- 20:37:02.044 ERROR orders-bff") || !strings.Contains(loc.Block, "saatler Europe/Istanbul, ekrandaki saatle aynı;") {
		t.Fatalf("izleyici dilimi: %s", loc.Block)
	}
	if len(loc.Refs) != 1 || loc.Refs[0].FirstTs != ts || loc.Refs[0].Key != "customerNo=0 rejected" {
		t.Fatalf("künye: %+v", loc.Refs)
	}
	if traceLogZoneLabel(time.UTC) != "UTC" || traceLogZoneLabel(nil) != "UTC" {
		t.Fatal("UTC künyesi")
	}
	ctx := withViewerLoc(context.Background(), ist)
	if viewerLocFrom(ctx) != ist || viewerLocFrom(context.Background()) != nil || traceLogLocFrom(withTraceLogLoc(ctx, ist)) != ist {
		t.Fatal("ctx taşıma")
	}
}
