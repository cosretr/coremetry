package api

// inbox_impact_test.go — v0.10.2482 (Problems Etki sütunu, yalnız gösterim):
// liste yolu etki özetini TEK toplu okumayla getirir, öncelik içinde etkiye
// göre sıralar; öncelik alanı değişmez; önbellek anahtarları sürüm atladı.

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func impactTestRows(now time.Time) ([]InboxItem, []chstore.MLEvent) {
	start := now.Add(-10 * time.Minute).UnixNano()
	errP := chstore.Problem{ID: "anomaly-auto:svc:orders", RuleID: "anomaly-auto:svc:orders", Service: "orders", Metric: "error_rate",
		Status: "open", Priority: "P2", StartedAt: start}
	grp := chstore.Problem{ID: "anomaly-auto:group:lag-1:billing", RuleID: "anomaly-auto:group:lag-1", Service: "billing", Metric: "ml_group_signals",
		Value: 1, Threshold: 1, Status: "open", Priority: "P2", StartedAt: start}
	oom := chstore.Problem{ID: "anomaly-auto:svc:cart:oom", RuleID: "anomaly-auto:svc:cart", Service: "cart", Metric: chstore.MLOOMMetric,
		Value: 4, Threshold: 5, Status: "open", Priority: "P2", StartedAt: start, Description: chstore.MLPodOOMSentence("cart-api", 3, 4, 512)}
	rule := chstore.Problem{ID: "r1", RuleID: "rule-1", Service: "orders", Metric: "p95_ms", Status: "open", Priority: "P2", StartedAt: start - 1}
	items := []InboxItem{}
	for _, p := range []chstore.Problem{rule, oom, grp, errP} {
		p := p
		items = append(items, InboxItem{ID: "problem:" + p.ID, Kind: "problem", Priority: p.Priority, Service: p.Service, StartedAt: p.StartedAt,
			stdSrc: &inboxStdSrc{problem: &p}})
	}
	evs := []chstore.MLEvent{
		{ID: "e1", Detector: "svc_error_rate", Service: "orders", Baseline: 0.01, StartedAt: start, LastSeen: now.UnixNano(),
			Evidence: `{"source":"vm","rule":"impact","threshold":0.05,"violations":4,"window":5,"minViolations":3,"minLoad":10,` +
				`"ratios":[0.1,0.1,0.1],"loads":[2000,2000,2000],"svcShadow":{"live":true,"problemId":"` + errP.ID + `"}}`},
		{ID: "lag-1", Detector: "metric_kafka", Service: "billing", Code: "metric_kafka|consumer_lag#billing.events", Baseline: 600,
			StartedAt: start, LastSeen: now.UnixNano(),
			Evidence: `{"source":"vm","rule":"impact","threshold":1200,"violations":4,"window":5,"minViolations":3,"sampleSec":60,"values":[600,4000]}`},
	}
	return items, evs
}

func TestInboxImpactPlan(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	items, _ := impactTestRows(now)
	plan, ok := inboxImpactPlan(items, now)
	if !ok || !reflect.DeepEqual(plan.services, []string{"orders"}) || !reflect.DeepEqual(plan.ids, []string{"lag-1"}) ||
		!reflect.DeepEqual(plan.detectors, []string{"endpoint_impact", "svc_error_rate"}) {
		t.Fatalf("plan: %+v", plan)
	}
	if want := now.Add(-10*time.Minute - chstore.ProblemEventSlack); !plan.since.Equal(want) {
		t.Errorf("since %v, beklenen %v", plan.since, want)
	}
	// 3 gün önce açılmış Problem: okuma 24 saatle sınırlı.
	old := items[3]
	op := *old.stdSrc.problem
	op.StartedAt = now.Add(-72 * time.Hour).UnixNano()
	old.stdSrc = &inboxStdSrc{problem: &op}
	if p, _ := inboxImpactPlan([]InboxItem{old}, now); !p.since.Equal(now.Add(-inboxImpactLookback)) {
		t.Errorf("geri sınır: %v", p.since)
	}
	// Yalnız kural / OOM satırları: sorgu yok.
	if _, ok := inboxImpactPlan(items[:2], now); ok {
		t.Error("etki olayı istemeyen satırlar okuma yapmamalı")
	}
}

// TestInboxListImpactAndSort — liste: özet satırda, aynı öncelikte büyük etki
// önce, özetsiz en sonda; öncelik alanları değişmez.
func TestInboxListImpactAndSort(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	items, evs := impactTestRows(now)
	before := make([]string, len(items))
	for i, it := range items {
		before[i] = it.ID + "=" + it.Priority
	}
	(&Server{}).attachInboxStandards(context.Background(), items, now) // store yok: OOM yine özetli (okuma istemez)
	for _, it := range items {
		if it.Std == nil {
			t.Fatalf("%s: std yok", it.ID)
		}
	}
	if s := items[1].Std.ImpactSummary; s == nil || s.Text != "3/5 pod" {
		t.Fatalf("OOM özeti: %+v", s)
	}
	attachInboxImpacts(items, evs)
	got := map[string]string{}
	for _, it := range items {
		if s := it.Std.ImpactSummary; s != nil {
			got[it.ID] = s.Text
		}
	}
	want := map[string]string{
		"problem:anomaly-auto:svc:orders":          "~200 istek/dk başarısız · trafiğin %10'u",
		"problem:anomaly-auto:group:lag-1:billing": "4.000 mesaj birikmiş · +3.400/dk",
		"problem:anomaly-auto:svc:cart:oom":        "3/5 pod",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("özetler:\n%v\n%v", got, want)
	}
	sorted, _ := inboxSortAndCap(items, "priority", "desc", 0)
	var order []string
	for _, it := range sorted {
		order = append(order, it.ID)
	}
	wantOrder := []string{"problem:anomaly-auto:group:lag-1:billing", "problem:anomaly-auto:svc:orders", "problem:anomaly-auto:svc:cart:oom", "problem:r1"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Errorf("sıra:\n%v\n%v", order, wantOrder)
	}
	after := map[string]bool{}
	for _, it := range sorted {
		after[it.ID+"="+it.Priority] = true
	}
	for _, b := range before {
		if !after[b] {
			t.Errorf("öncelik değişti: %s", b)
		}
	}
	// Etki kolonu: artan sırada özetsiz önce.
	asc, _ := inboxSortAndCap(sorted, "impact", "asc", 0)
	if asc[0].ID != "problem:r1" || asc[3].ID != "problem:anomaly-auto:group:lag-1:billing" {
		t.Errorf("impact asc: %v %v", asc[0].ID, asc[3].ID)
	}
	if id, _ := normalizeInboxSort("impact", "desc"); id != "impact" {
		t.Errorf("impact sıralama kimliği tanınmalı")
	}
}

// TestInboxPriorityStillPrimary — etki öncelik sırasını ASLA aşmaz.
func TestInboxPriorityStillPrimary(t *testing.T) {
	big := &chstore.ProblemStandard{ImpactSummary: &chstore.StdImpactSummary{Rank: 1e6}}
	small := &chstore.ProblemStandard{ImpactSummary: &chstore.StdImpactSummary{Rank: 1}}
	items := []InboxItem{
		{ID: "p2-big", Priority: "P2", StartedAt: 1, Std: big},
		{ID: "p1-none", Priority: "P1", StartedAt: 3},
		{ID: "p1-small", Priority: "P1", StartedAt: 2, Std: small},
	}
	sortInboxItems(items, "priority", "desc")
	if items[0].ID != "p1-small" || items[1].ID != "p1-none" || items[2].ID != "p2-big" {
		t.Errorf("sıra: %s %s %s", items[0].ID, items[1].ID, items[2].ID)
	}
}

func TestImpactCacheKeysBumped(t *testing.T) {
	if k := problemStandardKey("problem:x"); !strings.HasPrefix(k, "pstd:v6:") {
		t.Errorf("pstd anahtarı: %s", k)
	}
	if k := inboxListKey("open", "", "", "", "", "", "", 300, "priority", "desc", 0, nil, nil, "service"); !strings.HasPrefix(k, "inbox:v15:") {
		t.Errorf("inbox anahtarı: %s", k)
	}
}
