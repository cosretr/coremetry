package runtimesig

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Follow-up 2026-11-21: HikariCP serileri service_name taşımaz; servisin
// varlık pod'ları (host_name) ile okunur.
func TestAnalyzeHikariPodScoped(t *testing.T) {
	now := time.Unix(0, fixNow)
	gk := func(pod, pool string) []string { return []string{pod, "", "", pool} }
	r := &fakeReader{
		names: []string{"hikaricp_connections_active", "hikaricp_connections_max", "hikaricp_connections_pending",
			"hikaricp_connections_acquire_seconds_sum", "hikaricp_connections_acquire_seconds_count"},
		series: map[string][]chstore.SpanMetricSeries{
			"hikaricp_connections_active":                {toSMS(gk("svc-a-1", "HikariPool-1"), series(step(5, 10)))},
			"hikaricp_connections_max":                   {toSMS(gk("svc-a-1", "HikariPool-1"), series(step(10, 10)))},
			"hikaricp_connections_pending":               {toSMS(gk("svc-a-1", "HikariPool-1"), series(step(0, 8)))},
			"hikaricp_connections_acquire_seconds_sum":   {toSMS(gk("svc-a-1", "HikariPool-1"), series(step(0.01, 2)))},
			"hikaricp_connections_acquire_seconds_count": {toSMS(gk("svc-a-1", "HikariPool-1"), series(step(1, 1)))},
		},
	}
	rep, err := AnalyzeWith(context.Background(), r, "svc-a", "java", "", th(), now, AnalyzeOpts{Pods: []string{"svc-a-1", "svc-a-2"}})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Profile.Has(SigDBPoolPending) {
		t.Fatalf("profile = %+v", rep.Profile)
	}
	for _, res := range rep.Profile.Signals {
		if res.Signal == SigDBPoolActive && !res.PodScoped {
			t.Fatal("hikari signals must be pod scoped")
		}
	}
	hik := 0
	for _, q := range r.queries {
		if len(q.Name) < 8 || q.Name[:8] != "hikaricp" {
			continue
		}
		hik++
		if q.Service != "" {
			t.Errorf("hikari query must not filter by service: %+v", q)
		}
		if len(q.Filters) != 1 || q.Filters[0].Key != "resource.host.name" || q.Filters[0].Op != "IN" || len(q.Filters[0].Values) != 2 {
			t.Errorf("hikari pod filter = %+v", q.Filters)
		}
	}
	if hik != 5 {
		t.Fatalf("hikari queries = %d", hik)
	}
	var got *Signal
	for i := range rep.Signals {
		if rep.Signals[i].Check == CheckDBPoolSaturation {
			got = &rep.Signals[i]
		}
	}
	if got == nil || got.Level != LevelCritical || got.Pod != "svc-a-1" || got.Extra["poolUtilPct"] != 100 || got.Extra["acquireMs"] != 2000 {
		t.Fatalf("db pool signal = %+v (all %+v)", got, rep.Signals)
	}
}

// Follow-up 2026-11-21: ortam kapsamı ilk çözülen runtime metriğiyle çözülür,
// her runtime sorgusuna eklenir ve yanıtta taşınır (UI aynı süzgeci kullanır).
func TestAnalyzeScopeFilters(t *testing.T) {
	r := &fakeReader{names: []string{"process_runtime_dotnet_thread_pool_queue_length"}}
	env := chstore.FilterExpr{Key: "deployment_environment_name", Op: "IN", Values: []string{"clusterB", "prod-clusterA"}}
	probe := ""
	rep, err := AnalyzeWith(context.Background(), r, "svc-a", "dotnet", "", th(), time.Unix(0, fixNow), AnalyzeOpts{
		ScopeFor: func(_ context.Context, p string) ([]chstore.FilterExpr, string) {
			probe = p
			return []chstore.FilterExpr{env}, ""
		},
	})
	if err != nil || probe != "process_runtime_dotnet_thread_pool_queue_length" || len(rep.ScopeFilters) != 1 {
		t.Fatalf("probe=%q rep=%+v err=%v", probe, rep, err)
	}
	if len(r.queries) == 0 {
		t.Fatal("no queries")
	}
	for _, q := range r.queries {
		if len(q.Filters) == 0 || q.Filters[0].Key != "deployment_environment_name" {
			t.Errorf("scope filter missing: %+v", q)
		}
	}
	// Uygulanamadı → not, süzgeç yok.
	r2 := &fakeReader{names: r.names}
	rep, _ = AnalyzeWith(context.Background(), r2, "svc-a", "dotnet", "", th(), time.Unix(0, fixNow), AnalyzeOpts{
		ScopeFor: func(context.Context, string) ([]chstore.FilterExpr, string) {
			return nil, "ortam kapsamı uygulanmadı"
		},
	})
	if rep.ScopeNote == "" || len(rep.ScopeFilters) != 0 || len(r2.queries[0].Filters) != 0 {
		t.Fatalf("unapplied scope = %+v / %+v", rep, r2.queries)
	}
}

func TestHikariNoPodsNoRead(t *testing.T) {
	r := &fakeReader{names: []string{"hikaricp_connections_pending"}}
	rep, err := AnalyzeWith(context.Background(), r, "svc-a", "java", "", th(), time.Unix(0, fixNow), AnalyzeOpts{})
	if err != nil || rep.Profile.Has(SigDBPoolPending) || len(r.queries) != 0 {
		t.Fatalf("rep=%+v queries=%v err=%v", rep.Profile, r.queries, err)
	}
	if _, ok := HikariPodFilter([]string{"", ""}); ok {
		t.Fatal("empty pods → no filter")
	}
}

func TestEvaluateHikariNotSaturated(t *testing.T) {
	d := PodData{Service: "svc-a", Pod: "p", Series: map[SignalKey][]Point{
		SigDBPoolPending: series(step(0, 2)),
		SigDBPoolActive:  series(step(2, 4)),
		SigDBPoolMax:     series(step(10, 10)),
	}}
	none(t, EvaluatePod(d, fixNow, th()), CheckDBPoolSaturation)
}
