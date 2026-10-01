package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestFilterOpenProblemsSince_ExcludesStartedBeforeDeploy(t *testing.T) {
	sinceNs := int64(1000)
	snapshot := []*chstore.Problem{
		{ID: "p1", Service: "svcA", StartedAt: 500}, // before deploy
	}
	got := filterOpenProblemsSince(snapshot, sinceNs)
	if len(got) != 0 {
		t.Fatalf("expected 0 problems (started before deploy), got %d", len(got))
	}
}

func TestFilterOpenProblemsSince_IncludesStartedAfterDeploy(t *testing.T) {
	sinceNs := int64(1000)
	snapshot := []*chstore.Problem{
		{ID: "p1", Service: "svcA", StartedAt: 1500}, // after deploy
	}
	got := filterOpenProblemsSince(snapshot, sinceNs)
	if len(got) != 1 || got[0].ID != "p1" {
		t.Fatalf("expected 1 problem p1, got %+v", got)
	}
}

func TestFilterOpenProblemsSince_BoundaryIsInclusive(t *testing.T) {
	sinceNs := int64(1000)
	snapshot := []*chstore.Problem{
		{ID: "p1", Service: "svcA", StartedAt: 1000}, // exactly at deploy
	}
	got := filterOpenProblemsSince(snapshot, sinceNs)
	if len(got) != 1 {
		t.Fatalf("expected StartedAt == since to be included (inclusive boundary), got %d", len(got))
	}
}

func TestFilterOpenProblemsSince_SortedByStartedAtDesc(t *testing.T) {
	sinceNs := int64(1000)
	snapshot := []*chstore.Problem{
		{ID: "older", Service: "svcA", StartedAt: 1500},
		{ID: "newer", Service: "svcA", StartedAt: 2500},
	}
	got := filterOpenProblemsSince(snapshot, sinceNs)
	if len(got) != 2 || got[0].ID != "newer" || got[1].ID != "older" {
		t.Fatalf("expected [newer, older] order, got %+v", got)
	}
}

func TestIntersectServices_NilTeamSvcsPassesThroughUnfiltered(t *testing.T) {
	svcOrder := []string{"svcA", "svcB"}
	got := intersectServices(svcOrder, nil)
	if len(got) != 2 {
		t.Fatalf("expected the unfiltered list when no team is selected, got %+v", got)
	}
}

func TestIntersectServices_EmptyTeamSvcsCollapsesToEmpty(t *testing.T) {
	svcOrder := []string{"svcA", "svcB"}
	got := intersectServices(svcOrder, []string{}) // team selected, zero member services
	if len(got) != 0 {
		t.Fatalf("expected a team with no member services to collapse the result to empty, got %+v", got)
	}
}

func TestIntersectServices_KeepsOnlyMatchingPreservesOrder(t *testing.T) {
	svcOrder := []string{"svcC", "svcA", "svcB"}
	got := intersectServices(svcOrder, []string{"svcA", "svcB"})
	if len(got) != 2 || got[0] != "svcA" || got[1] != "svcB" {
		t.Fatalf("expected [svcA, svcB] preserving svcOrder's order, got %+v", got)
	}
}

// TestRedComparisonWindow_SymmetricDuration — v0.10.1028: sözleşme "eşit
// süre"den "eşit KOVA SAYISI + gerçek kapsama paydası"na döndü
// (redComparisonPlan). Eski hâli `beforeTo == since` ve eşit süreyi
// sınıyordu ve yalnız fikstürü 5 dk hizalı olduğu için geçiyordu; gerçek
// deploy anı hizasızdır ve o kurulumda floor5(since) kovası iki tarafa da
// sayılıyordu.
func TestRedComparisonWindow_SymmetricDuration(t *testing.T) {
	// Deploy 10 dk önce, HİZASIZ bir anda (10:03:17).
	since := time.Date(2026, 10, 2, 10, 3, 17, 0, time.UTC)
	now := since.Add(10 * time.Minute)
	p := redComparisonPlan(since.UnixNano(), now.UnixNano(), now.UnixNano())

	// After okuması değişmedi: [since, now].
	if !p.AfterFrom.Equal(since) || !p.AfterTo.Equal(now) {
		t.Fatalf("after [%v, %v], beklenen [since, now]", p.AfterFrom, p.AfterTo)
	}
	// Before, after'ın ilk kovasında (floor5(since) = 10:00) biter — since'te değil.
	floor := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	if !p.BeforeTo.Equal(floor) {
		t.Fatalf("before sonu %v, beklenen floor5(since) = %v", p.BeforeTo, floor)
	}
	// Simetri = KOVA SAYISI: after 10:00, 10:05, 10:10 → before 09:45, 09:50, 09:55.
	if a, b := len(mv5Read(p.AfterFrom, p.AfterTo)), len(mv5Read(p.BeforeFrom, p.BeforeTo)); a != 3 || b != 3 {
		t.Fatalf("kova sayısı after %d / before %d, beklenen 3 / 3", a, b)
	}
	// Paydalar ortak plandan: before 3 tam kova, after 10:00 → now (797 sn).
	if p.BeforeSec != 900 || p.AfterSec != 797 {
		t.Fatalf("paydalar before %v / after %v, beklenen 900 / 797", p.BeforeSec, p.AfterSec)
	}
}

func TestRedComparisonWindow_DeployJustHappened(t *testing.T) {
	// v0.10.1028 — since == now. After penceresi süre olarak sıfır ama okuyucu
	// deploy kovasını (10:00) okur: before onun önündeki TEK kova, after
	// paydası o kovanın dolu kısmı (197 sn).
	since := time.Date(2026, 10, 2, 10, 3, 17, 0, time.UTC)
	p := redComparisonPlan(since.UnixNano(), since.UnixNano(), since.UnixNano())
	if !p.AfterFrom.Equal(p.AfterTo) {
		t.Fatalf("since==now'da after penceresi sıfır genişlikte olmalı, %v..%v", p.AfterFrom, p.AfterTo)
	}
	if p.BeforeTo.Sub(p.BeforeFrom) != 5*time.Minute || p.BeforeSec != 300 || p.AfterSec != 197 {
		t.Fatalf("before [%v, %v) %v sn / after %v sn, beklenen tek kova 300 / 197",
			p.BeforeFrom, p.BeforeTo, p.BeforeSec, p.AfterSec)
	}
	// Hizalı deploy anında since == now: after hiç kova okumaz → before boş,
	// before paydası 0 (throughput 0), after paydası tabanda 1 sn.
	aligned := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	q := redComparisonPlan(aligned.UnixNano(), aligned.UnixNano(), aligned.UnixNano())
	if !q.BeforeFrom.Equal(q.BeforeTo) || q.BeforeSec != 0 || q.AfterSec != 1 {
		t.Fatalf("hizalı since==now: before [%v, %v) %v sn / after %v sn, beklenen boş / 0 / 1",
			q.BeforeFrom, q.BeforeTo, q.BeforeSec, q.AfterSec)
	}
}

func TestNonNilSlice_NilBecomesEmpty(t *testing.T) {
	var s []chstore.Problem
	got := nonNilSlice(s)
	if got == nil {
		t.Fatal("expected a non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected an empty slice, got %d items", len(got))
	}
}

func TestNonNilSlice_PreservesNonNil(t *testing.T) {
	s := []chstore.Problem{{ID: "p1"}}
	got := nonNilSlice(s)
	if len(got) != 1 || got[0].ID != "p1" {
		t.Fatalf("expected the original slice preserved, got %+v", got)
	}
}

// Operator-reported: a service with no anomalies/new-errors serialized
// those fields as JSON `null` (Go nil-slice default), and the frontend's
// `s.anomalies.map(...)` threw "TypeError: Cannot read properties of
// null (reading 'map')" on exactly that response shape. Locks the fix:
// every array field on ServiceReportSection must marshal as `[]`.
func TestServiceReportSection_EmptySlicesMarshalAsArrayNotNull(t *testing.T) {
	sec := ServiceReportSection{
		Service:   "checkout-service",
		Health:    "red",
		Problems:  nonNilSlice[chstore.Problem](nil),
		Anomalies: nonNilSlice[chstore.AnomalyEvent](nil),
		NewErrors: nonNilSlice[chstore.ExceptionGroup](nil),
	}
	b, err := json.Marshal(sec)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	got := string(b)
	for _, want := range []string{`"problems":[]`, `"anomalies":[]`, `"newErrors":[]`} {
		if !strings.Contains(got, want) {
			t.Fatalf("regression: expected %s in JSON, got %s", want, got)
		}
	}
}
