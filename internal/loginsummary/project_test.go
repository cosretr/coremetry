package loginsummary

import (
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var allowedKeys = map[string]bool{
	"enabled": true, "ready": true, "generated_at": true, "spans_today": true, "traces_today": true,
	"services_monitored": true, "endpoints_monitored": true, "http_24h": true, "activity_24h": true, "topology": true, "components": true,
	"from": true, "to": true, "step_s": true, "throughput": true, "avg": true, "summary": true,
	"groupKey": true, "points": true, "time": true, "value": true,
	"now_rps": true, "peak_rps": true, "peak_at": true, "avg_now_s": true,
	"logins": true, "cosre_questions": true, // v0.10.1915: rollouts_tracked + traces_viewed kaldırıldı
	"window_s": true, "named": true, "nodes": true, "edges": true, "id": true, "tier": true, "weight": true, "name": true,
	"state": true, "checked_at": true, "clickhouse_uncompressed_bytes": true,
}

func walkKeys(t *testing.T, v any, path string) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			if !allowedKeys[k] {
				t.Errorf("izin listesi dışı anahtar %q (%s)", k, path)
			}
			walkKeys(t, c, path+"."+k)
		}
	case []any:
		for _, c := range x {
			walkKeys(t, c, path+"[]")
		}
	}
}

func flags(now time.Time, topo string) Flags {
	return Flags{Enabled: true, Topology: topo, Now: now}
}

func TestProjectAnonymousNeverLeaksNames(t *testing.T) {
	now := time.Now()
	snap := baseSnapshot(now)
	for _, mode := range []string{"", TopologyAnonymous, "weird-value"} {
		out := mustJSON(t, Project(snap, flags(now, mode)))
		assertAbsent(t, out, serviceNeedles, "isimsiz kip "+mode)
		assertAbsent(t, out, internalNeedles, "isimsiz kip "+mode)
		assertAbsent(t, out, []string{"oracle", "kafka"}, "isimsiz kip "+mode)
	}
}

func TestProjectNamedShowsServicesButNeverInternals(t *testing.T) {
	now := time.Now()
	snap := baseSnapshot(now)
	resp := Project(snap, flags(now, TopologyNamed))
	out := mustJSON(t, resp)
	assertAbsent(t, out, internalNeedles, "isimli kip")
	if resp.Topology == nil || !resp.Topology.Named {
		t.Fatal("isimli kip: topology.named=true beklenir")
	}
	names := map[string]bool{}
	for _, n := range resp.Topology.Nodes {
		names[n.Name] = true
		if n.Name == "" {
			t.Errorf("isimli kipte ad dolu olmalı: %+v", n)
		}
	}
	for _, want := range append(append([]string{}, serviceNeedles...), "oracle", "kafka") {
		if !names[want] {
			t.Errorf("isimli kipte %q beklenir (var: %v)", want, names)
		}
	}
}

func TestProjectTopologyOffOmitsTopology(t *testing.T) {
	now := time.Now()
	resp := Project(baseSnapshot(now), flags(now, TopologyOff))
	if resp.Topology != nil {
		t.Fatal("loginTopology=off: topology alanı olmamalı")
	}
	if m := mustJSON(t, resp); regexp.MustCompile(`"topology"`).MatchString(m) {
		t.Fatalf("topology anahtarı çıkmamalı: %s", m)
	}
}

func TestProjectResponseKeysAreSubsetOfAllowList(t *testing.T) {
	now := time.Now()
	snap := baseSnapshot(now)
	snap.Ring.MergeSum([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{{Time: (now.Unix() - now.Unix()%300 - 300) * 1e9, Value: 20}}}})
	for _, mode := range []string{TopologyAnonymous, TopologyNamed, TopologyOff} {
		var generic any
		if err := json.Unmarshal([]byte(mustJSON(t, Project(snap, flags(now, mode)))), &generic); err != nil {
			t.Fatal(err)
		}
		walkKeys(t, generic, mode)
	}
}

func TestProjectDisabledAndNotReady(t *testing.T) {
	now := time.Now()
	snap := baseSnapshot(now)
	if got := mustJSON(t, Project(snap, Flags{Enabled: false, Topology: TopologyOff, Now: now})); got != `{"enabled":false,"ready":false}` {
		t.Errorf("panel ve topoloji kapalı: yalnız enabled+ready, got %s", got)
	}
	// v0.10.1917 — panel kapalı, topoloji açık: YALNIZ topoloji.
	r := Project(snap, Flags{Enabled: false, Topology: TopologyAnonymous, Now: now})
	if r.Enabled || !r.Ready || r.Topology == nil || len(r.Topology.Nodes) == 0 {
		t.Fatalf("panel kapalı + topoloji açık: topoloji beklenir: %+v", r)
	}
	if r.SpansToday != nil || r.TracesToday != nil || r.Services != nil || r.Endpoints != nil ||
		r.HTTP24h != nil || r.Activity != nil || r.Components != nil {
		t.Errorf("panel kapalıyken panel alanı sızmamalı: %s", mustJSON(t, r))
	}
	assertAbsent(t, mustJSON(t, r), serviceNeedles, "panel kapalı + isimsiz topoloji")
	if got := mustJSON(t, Project(nil, Flags{Enabled: false, Topology: TopologyAnonymous, Now: now})); got != `{"enabled":false,"ready":false}` {
		t.Errorf("panel kapalı + anlık görüntü yok: got %s", got)
	}
	if got := mustJSON(t, Project(nil, flags(now, ""))); got != `{"enabled":true,"ready":false}` {
		t.Errorf("anlık görüntü yok: got %s", got)
	}
	old := baseSnapshot(now.Add(-StaleAfter - time.Minute))
	if got := mustJSON(t, Project(old, flags(now, ""))); got != `{"enabled":true,"ready":false}` {
		t.Errorf("15 dk'dan eski: got %s", got)
	}
	wrong := baseSnapshot(now)
	wrong.V = 99
	if r := Project(wrong, flags(now, "")); r.Ready {
		t.Error("bilinmeyen sürüm hazır sayılmamalı")
	}
}

// Anlık görüntü zehirli olsa bile (bozuk Redis blob'u) ham kimlikler çıkmaz.
func TestProjectPoisonedSnapshotNeverEmitsRawIdentifiers(t *testing.T) {
	now := time.Now()
	snap := baseSnapshot(now)
	snap.Topo = &SnapTopology{WindowS: 900, Nodes: []SnapNode{
		{ID: "aaaaaaaaaa", Name: "orders-api", Tier: "svc", Weight: 0.5},
		{ID: "bbbbbbbbbb", Name: "ext:partner.example.com", Tier: "svc", Weight: 0.5},
		{ID: "cccccccccc", Name: "db:oracle@db-host-1.acme.internal", Tier: "db", Weight: 0.5},
		{ID: "dddddddddd", Name: "oracle@db-host-1", Tier: "db", Weight: 0.5},
		{ID: "eeeeeeeeee", Name: "Some Host.acme.internal", Tier: "db", Weight: 0.5},
		{ID: "ffffffffff", Name: "node:worker-1", Tier: "svc", Weight: 0.5},
		{ID: "gggggggggg", Name: "bff-web", Tier: "evil-tier", Weight: 7},
	}, Edges: []Edge{
		{From: "aaaaaaaaaa", To: "bbbbbbbbbb", Weight: 1},
		{From: "aaaaaaaaaa", To: "eeeeeeeeee", Weight: 1},
		{From: "aaaaaaaaaa", To: "gggggggggg", Weight: 3},
		{From: "aaaaaaaaaa", To: "unknown000", Weight: 1},
	}}
	resp := Project(snap, flags(now, TopologyNamed))
	out := mustJSON(t, resp)
	assertAbsent(t, out, []string{"ext:", "partner.example.com", "db-host-1", "acme", "worker-1", "node:", "db:", "@", "Some Host", "unknown000", "bbbbbbbbbb", "cccccccccc"}, "zehirli")
	for _, n := range resp.Topology.Nodes {
		if n.Weight < 0 || n.Weight > 1 {
			t.Errorf("ağırlık 0..1 olmalı: %+v", n)
		}
		if n.Tier != "gw" && n.Tier != "bff" && n.Tier != "svc" && n.Tier != "db" {
			t.Errorf("bilinmeyen katman çıkmamalı: %+v", n)
		}
	}
	for _, e := range resp.Topology.Edges {
		if e.Weight > 1 {
			t.Errorf("kenar ağırlığı ≤ 1: %+v", e)
		}
	}
}

// v0.10.1914 — cevapta TEK toplam hız serisi vardır ve grup anahtarı boştur:
// VM'den gelen hiçbir etiket (durum kodu, servis adı…) anonim cevaba taşınmaz.
func TestProjectThroughputCarriesNoLabels(t *testing.T) {
	now := time.Now()
	snap := baseSnapshot(now)
	tt := (now.Unix() - now.Unix()%300 - 300) * 1e9
	snap.Ring.MergeRate([]chstore.SpanMetricSeries{
		{GroupKey: []string{`service="evil-service"`}, Points: []chstore.SpanMetricPoint{{Time: tt, Value: 5}}},
		{GroupKey: []string{`http_response_status_code="503"`}, Points: []chstore.SpanMetricPoint{{Time: tt, Value: 1}}},
	})
	resp := Project(snap, flags(now, ""))
	if resp.HTTP24h == nil || resp.HTTP24h.Throughput == nil {
		t.Fatal("http_24h.throughput beklenir")
	}
	if len(resp.HTTP24h.Throughput.GroupKey) != 0 {
		t.Errorf("grup anahtarı boş olmalı: %v", resp.HTTP24h.Throughput.GroupKey)
	}
	assertAbsent(t, mustJSON(t, resp), []string{"evil-service", "503", "http_response_status_code"}, "hız serisi etiketi")
}

func TestProjectActivityAndComponents(t *testing.T) {
	now := time.Now()
	snap := baseSnapshot(now)
	snap.State = "garbage"
	resp := Project(snap, flags(now, ""))
	if resp.Components == nil || resp.Components.State != StateDegraded {
		t.Errorf("bilinmeyen durum degraded sayılır: %+v", resp.Components)
	}
	if resp.Activity == nil || resp.Activity.Logins != 12 || resp.Activity.CosreQuestions != 5 {
		t.Errorf("aktivite: %+v", resp.Activity)
	}
	if resp.Endpoints == nil || *resp.Endpoints != 420 {
		t.Errorf("endpoint sayısı: %v", resp.Endpoints)
	}
	// v0.10.1915 — kaldırılan alanlar cevapta geçmez.
	assertAbsent(t, mustJSON(t, resp), []string{"rollouts_tracked", "traces_viewed"}, "kaldırılan aktivite alanı")
	snap.Activity = nil
	if Project(snap, flags(now, "")).Activity != nil {
		t.Error("aktivite okunamadıysa alan yok")
	}
}
