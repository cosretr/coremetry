package api

// cosre_inbox_open_test.go — v0.10.1691: open_problems Service şeridiyle aynı
// kaynağı (/api/inbox çekirdeği) okur (operatör kararı 2026-10-31). Belirti:
// v0.10.1690 yalnız problems tablosunu sayıyordu — şerit exception/anomali
// satırlarını da sayarken araç "açık problem yok" diyebiliyordu.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestInboxOpenValues(t *testing.T) {
	for _, c := range []struct {
		q    mcptools.InboxOpenQuery
		want string
	}{
		{mcptools.InboxOpenQuery{Team: "acme", Env: "prod", Limit: 50}, "env=prod&limit=50&status=open&team=acme"},
		{mcptools.InboxOpenQuery{Service: "orders-api"}, "limit=500&service=orders-api&status=open"},
		{mcptools.InboxOpenQuery{Service: "orders-api", Limit: 9999}, "limit=500&service=orders-api&status=open"},
	} {
		if got := inboxOpenValues(c.q).Encode(); got != c.want {
			t.Errorf("%+v → %q, want %q", c.q, got, c.want)
		}
	}
}

func TestInboxRowsFromKindsAndHref(t *testing.T) {
	rows := inboxRowsFrom([]InboxItem{
		{ID: "problem:p-1", Kind: "problem", Service: "orders-api", Problem: &InboxProblemRef{ID: "p-1"}, PriorityReason: "P1: eşik 3×", StartedAt: 2e18},
		{ID: "exception:f", Kind: "exception", Service: "payments-core", Exception: &InboxExceptionRef{Fingerprint: "f 1"}, Title: "NullPointerException"},
		{ID: "httperror:h", Kind: "httperror", Service: "payments-core", Exception: &InboxExceptionRef{Fingerprint: "h"}},
		{ID: "anomaly:a", Kind: "anomaly", Service: "orders-api", Anomaly: &InboxAnomalyRef{ID: "a"}},
		{ID: "problem:x", Kind: "problem", Service: "orders-api"},
	})
	want := []string{"/problems?problem=p-1", "/problems?exc=f+1", "/problems?exc=h", "/inbox?anomaly=a", ""}
	for i, w := range want {
		if rows[i].PageURL != w {
			t.Errorf("%d: %q want %q", i, rows[i].PageURL, w)
		}
	}
	if rows[0].Reason != "P1: eşik 3×" || rows[1].Reason != "NullPointerException" || rows[0].SinceUnix != 2e9 {
		t.Fatalf("neden/zaman %+v", rows[:2])
	}
}
