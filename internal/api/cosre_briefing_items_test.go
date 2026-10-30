package api

// cosre_briefing_items_test.go — briefing kalem 3/4 dikişleri (nötr fikstür).

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestSplitInboxRowsOneBucketEach(t *testing.T) {
	rows := []mcptools.InboxRow{
		{ID: "exception:new", Kind: "exception", Service: "orders-api", SinceUnix: 2000},
		{ID: "httperror:reg", Kind: "httperror", Service: "orders-api", Status: "regressed", SinceUnix: 10},
		{ID: "exception:old", Kind: "exception", Service: "orders-api", Status: "open", SinceUnix: 10},
		{ID: "problem:p", Kind: "problem", Service: "payments-core", SinceUnix: 2000},
	}
	ex, rest := splitInboxRows(rows, 1000)
	if len(ex) != 2 || len(rest) != 2 || rest[0].ID != "exception:old" || rest[1].ID != "problem:p" {
		t.Fatalf("ex=%+v rest=%+v", ex, rest)
	}
	if got := briefingLogsURL("acme team", 10800); got != "/logs?team=acme+team&range=3h" {
		t.Fatal(got)
	}
}
