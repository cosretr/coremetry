package mcptools

// v0.10.2482 (DECISIONS 2026-11-27, "açık konu" sözleşmesi revizyonu) —
// open_problems Problems rozetinin türlerini sayar; açık anomaliler ayrı
// alan (open_signals), total_open'a karışmaz. Varsayılan okuma AllKinds
// istemez; brifing kalem 3 ve sağlık kontrolü eski kümeyi açıkça ister.

import (
	"context"
	"testing"
)

func TestOpenProblemsSignalsSeparate(t *testing.T) {
	res := InboxOpenResult{Rows: []InboxRow{{ID: "problem:p", Kind: "problem", Service: "orders-api"}}, Total: 1, Signals: 3}
	out := openProblemsPayload("", "", []string{"orders-api"}, 0, 0, res, 25)
	if out["total_open"] != 1 || out["open_signals"] != 3 {
		t.Fatalf("total_open=%v open_signals=%v", out["total_open"], out["open_signals"])
	}
	if _, ok := openProblemsPayload("", "", []string{"orders-api"}, 0, 0, InboxOpenResult{}, 25)["open_signals"]; ok {
		t.Fatal("sinyal yokken alan yazılmamalı")
	}
}

func TestReadInboxOpenKinds(t *testing.T) {
	var got []InboxOpenQuery
	d := Deps{InboxOpen: func(_ context.Context, q InboxOpenQuery) (InboxOpenResult, error) {
		got = append(got, q)
		return InboxOpenResult{Signals: 2}, nil
	}}
	if _, _, err := ReadInboxOpen(context.Background(), d, "", []string{"a-api", "b-api"}, "", 20); err != nil {
		t.Fatal(err)
	}
	r, _, err := ReadInboxOpenAllKinds(context.Background(), d, "acme", nil, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].AllKinds || got[1].AllKinds || !got[2].AllKinds || r.Signals != 2 {
		t.Fatalf("sorgular %+v", got)
	}
	if !ServiceOpenItemsQuery("orders-api").AllKinds || ServiceAttentionQuery("orders-api").AllKinds {
		t.Fatal("sağlık kontrolü eski kümeyi, şerit rozetin türlerini okur")
	}
}
