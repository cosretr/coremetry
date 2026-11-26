package api

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// TestInboxStatusPivotProblemFilter — v0.10.2476: her pivotun Problem
// durumu kararı. Açık görünüm kapanmışı ASLA tutmaz; Kapanan yalnız
// kapanmışı tutar ve SQL'de tek kovayı (resolved) ister.
func TestInboxStatusPivotProblemFilter(t *testing.T) {
	cases := []struct {
		pivot, status string
		keep          bool
	}{
		{"open", "open", true},
		{"open", "acknowledged", true},
		{"open", "", true},
		{"open", "resolved", false},
		{"closed", "resolved", true},
		{"closed", "open", false},
		{"closed", "acknowledged", false},
		{"closed", "", false},
		{"all", "resolved", true},
		{"all", "open", true},
	}
	for _, c := range cases {
		if got := inboxKeepsProblem(c.status, c.pivot); got != c.keep {
			t.Errorf("inboxKeepsProblem(%q, %q) = %v, want %v", c.status, c.pivot, got, c.keep)
		}
	}
	if got := pickStatus(inboxStatusClosed); got != "resolved" {
		t.Errorf("pickStatus(closed) = %q, want resolved", got)
	}
	if got := pickExcludedStatuses(inboxStatusClosed); got != nil {
		t.Errorf("pickExcludedStatuses(closed) = %v, want nil", got)
	}
	if got := pickExcludedStatuses("open"); len(got) != 1 || got[0] != "resolved" {
		t.Errorf("pickExcludedStatuses(open) = %v, want [resolved]", got)
	}
}

func TestInboxClosedWindow(t *testing.T) {
	for since, want := range map[string]time.Duration{
		"": 24 * time.Hour, "bogus": 24 * time.Hour, "2h": 2 * time.Hour, "7d": 7 * 24 * time.Hour,
	} {
		if got := inboxClosedWindow(since); got != want {
			t.Errorf("inboxClosedWindow(%q) = %v, want %v", since, got, want)
		}
	}
}

func TestClosedInboxRowsAndOrder(t *testing.T) {
	at := func(ns int64) *int64 { return &ns }
	open := problemToInbox(chstore.Problem{ID: "p-open", Status: "open", ResolvedAt: at(5)})
	if open.ResolvedAt != 0 {
		t.Fatalf("açık satır kapanış anı taşımamalı: %d", open.ResolvedAt)
	}
	a := problemToInbox(chstore.Problem{ID: "a", Status: "resolved", ResolvedAt: at(100), Service: "checkout"})
	b := problemToInbox(chstore.Problem{ID: "b", Status: "resolved", ResolvedAt: at(300), Service: "payments"})
	c := problemToInbox(chstore.Problem{ID: "c", Status: "resolved", ResolvedAt: at(200), Service: "checkout"})
	items := []InboxItem{a, b, c}
	sortInboxClosed(items)
	if items[0].Problem.ID != "b" || items[1].Problem.ID != "c" || items[2].Problem.ID != "a" {
		t.Fatalf("en son kapanan önce değil: %s %s %s", items[0].Problem.ID, items[1].Problem.ID, items[2].Problem.ID)
	}
	if got := closedInboxNarrow(items, "CHECK", ""); len(got) != 2 {
		t.Fatalf("servis daraltması: %d satır, want 2", len(got))
	}
}

// TestFoldGraphClustersSkipsResolved — kapanmış satır açık küme üyesiyle
// katlanmaz (Tümü görünümünde açık üye kapanmış sahibin altına gizlenmez).
func TestFoldGraphClustersSkipsResolved(t *testing.T) {
	row := func(id, status string) InboxItem {
		return InboxItem{ID: "problem:" + id, Kind: "problem", Status: status, Problem: &InboxProblemRef{ID: id}}
	}
	items := []InboxItem{row("owner", "resolved"), row("m1", "open")}
	groups := []chstore.ProblemGraphGroup{{OwnerID: "owner", Entities: 2, Members: []chstore.ProblemGraphMember{
		{ProblemID: "owner", Owner: true}, {ProblemID: "m1"}}}}
	got := foldGraphClusters(items, groups)
	if len(got) != 2 || got[0].Cluster != nil || got[1].Cluster != nil {
		t.Fatalf("kapanmış sahip açık üyeyi katladı: %+v", got)
	}
}
