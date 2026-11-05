package chstore

// v0.10.1812 — ML tik ölçümü: CH sorgu sayacı tracedConn kapısında
// (applyQueryTag) sayar; etiketsiz ctx de sayılır, sayaçsız ctx dokunulmaz.

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestQueryCounterCountsAtGate(t *testing.T) {
	var n atomic.Int64
	ctx := WithQueryCounter(context.Background(), &n)
	applyQueryTag(ctx)                       // etiketsiz
	applyQueryTag(WithQueryTag(ctx, "ml:x")) // etiketli
	if n.Load() != 2 {
		t.Fatalf("sayaç %d, want 2", n.Load())
	}
	applyQueryTag(context.Background()) // sayaçsız: panik yok
	if WithQueryCounter(context.Background(), nil) == nil {
		t.Fatal("nil sayaç ctx'i bozmamalı")
	}
}
