package mcptools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

// v0.10.2453 — dependency_path: SAF yürüyüş / yargı / olası kaynak + araç
// sözleşmesi. Sentetik graf (svc-a … svc-z, db:postgres@pg-1, queue:kafka:t1).

type dpGraph map[string][]chstore.DepHopEdge // bilinen uç → kenarlar

func dpE(parent, child string, cur, ref chstore.DepPathCell) chstore.DepHopEdge {
	return chstore.DepHopEdge{Parent: parent, Child: child, Cur: cur, Ref: ref}
}

func dpC(calls, errs uint64, avg float64) chstore.DepPathCell {
	return chstore.DepPathCell{Calls: calls, Errors: errs, AvgMs: avg}
}

// fetch — graf üstünde SQL'in yaptığını taklit eder (yön + düğüm başına tavan).
func (g dpGraph) fetch(calls *int) dpFetch {
	return func(_ context.Context, up bool, frontier []string) ([]chstore.DepHopEdge, error) {
		*calls++
		var out []chstore.DepHopEdge
		for _, n := range frontier {
			var mine []chstore.DepHopEdge
			for _, es := range g {
				for _, e := range es {
					known := e.Parent
					if up {
						known = e.Child
					}
					if known == n {
						mine = append(mine, e)
					}
				}
			}
			// SQL: ORDER BY cc DESC … LIMIT 20 BY <bilinen uç>.
			sort.SliceStable(mine, func(i, j int) bool {
				if mine[i].Cur.Calls != mine[j].Cur.Calls {
					return mine[i].Cur.Calls > mine[j].Cur.Calls
				}
				return mine[i].Parent+mine[i].Child < mine[j].Parent+mine[j].Child
			})
			if len(mine) > chstore.DepHopPerNode {
				mine = mine[:chstore.DepHopPerNode]
			}
			out = append(out, mine...)
		}
		return out, nil
	}
}

func (g dpGraph) add(es ...chstore.DepHopEdge) dpGraph {
	for _, e := range es {
		g[e.Parent] = append(g[e.Parent], e)
	}
	return g
}

var dpOK = dpC(1000, 1, 10)

func TestWalkDependenciesCycleAndDepth(t *testing.T) {
	g := dpGraph{}.add(dpE("svc-a", "svc-b", dpOK, dpOK), dpE("svc-b", "svc-c", dpOK, dpOK), dpE("svc-c", "svc-a", dpOK, dpOK),
		dpE("svc-c", "svc-d", dpOK, dpOK))
	n := 0
	edges, tr, err := walkDependencies(context.Background(), g.fetch(&n), "svc-a", false, 3, dpEdgeCap)
	if err != nil || tr {
		t.Fatalf("err=%v truncated=%v", err, tr)
	}
	if len(edges) != 4 || n != 3 {
		t.Fatalf("kenar %d (4 beklenir: döngü kenarı bir kez), okuma %d", len(edges), n)
	}
	hops := map[string]int{}
	for _, e := range edges {
		hops[e.Parent+">"+e.Child] = e.Hop
	}
	if hops["svc-c>svc-a"] != 3 || hops["svc-a>svc-b"] != 1 {
		t.Errorf("atlamalar %v", hops)
	}
	// depth 1 → tek okuma.
	n = 0
	edges, _, _ = walkDependencies(context.Background(), g.fetch(&n), "svc-a", false, 1, dpEdgeCap)
	if len(edges) != 1 || n != 1 {
		t.Errorf("depth 1: %d kenar, %d okuma", len(edges), n)
	}
	// Yukarı: svc-a'yı svc-c çağırıyor; svc-c'yi svc-b.
	n = 0
	edges, _, _ = walkDependencies(context.Background(), g.fetch(&n), "svc-a", true, 2, dpEdgeCap)
	if len(edges) != 2 || edges[0].Parent != "svc-c" || edges[1].Parent != "svc-b" || !edges[0].Up {
		t.Errorf("yukarı yürüyüş %+v", edges)
	}
}

func TestWalkDependenciesFanOutCaps(t *testing.T) {
	g := dpGraph{}
	for i := 0; i < 30; i++ {
		g.add(dpE("svc-root", fmt.Sprintf("svc-%02d", i), dpC(uint64(100+i), 0, 5), dpOK))
		for j := 0; j < 5; j++ {
			g.add(dpE(fmt.Sprintf("svc-%02d", i), fmt.Sprintf("leaf-%02d-%d", i, j), dpOK, dpOK))
		}
	}
	// db / ext yapraklar genişletilmez.
	g.add(dpE("svc-root", "db:postgres@pg-1", dpC(5000, 0, 2), dpOK))
	g.add(dpE("db:postgres@pg-1", "svc-never", dpOK, dpOK))
	n := 0
	edges, tr, err := walkDependencies(context.Background(), g.fetch(&n), "svc-root", false, 3, dpEdgeCap)
	if err != nil {
		t.Fatal(err)
	}
	if !tr || len(edges) != dpEdgeCap {
		t.Fatalf("tavan: truncated=%v kenar=%d (≤%d)", tr, len(edges), dpEdgeCap)
	}
	hop1 := 0
	for _, e := range edges {
		if e.Hop == 1 {
			hop1++
		}
		if e.Child == "svc-never" {
			t.Error("db düğümü genişletildi")
		}
	}
	if hop1 != chstore.DepHopPerNode {
		t.Errorf("hop1 %d kenar, düğüm başına tavan %d", hop1, chstore.DepHopPerNode)
	}
	if edges[0].Child != "db:postgres@pg-1" {
		t.Errorf("trafiğe göre ilk kenar %s", edges[0].Child)
	}
}

func TestJudgeEdge(t *testing.T) {
	cases := []struct {
		name string
		m    dpMeasure
		deg  bool
		sig  string
	}{
		{"errors", dpMeasure{CurVol: 100, RefVol: 100, CurErrPct: 8, RefErrP: 1, CurLat: 10, RefLat: 10}, true, "errors"},
		{"latency", dpMeasure{CurVol: 100, RefVol: 100, CurLat: 90, RefLat: 40}, true, "latency"},
		{"small latency abs", dpMeasure{CurVol: 100, RefVol: 100, CurLat: 3, RefLat: 1}, false, ""},
		{"noise errors", dpMeasure{CurVol: 100, RefVol: 100, CurErrPct: 5, RefErrP: 4}, false, ""},
		{"new edge", dpMeasure{CurVol: 100, RefVol: 0, CurErrPct: 50}, false, "new_edge"},
		{"low volume", dpMeasure{CurVol: 5, RefVol: 5, CurErrPct: 100}, false, "low_volume"},
		{"traffic drop only", dpMeasure{CurVol: 40, RefVol: 100, CurLat: 10, RefLat: 10}, false, "traffic_drop"},
	}
	for _, c := range cases {
		deg, sig, score := judgeEdge(c.m, c.m.CurVol)
		if deg != c.deg || (c.sig != "" && !strings.Contains(strings.Join(sig, ","), c.sig)) {
			t.Errorf("%s: deg=%v sig=%v", c.name, deg, sig)
		}
		if deg && score <= 0 {
			t.Errorf("%s: skor %v", c.name, score)
		}
	}
}

func dpJudged(es ...chstore.DepHopEdge) []dpEdge {
	out := make([]dpEdge, 0, len(es))
	for _, e := range es {
		out = append(out, dpEdge{DepHopEdge: e})
	}
	judgeAll(out, 3600)
	return out
}

func TestLikelySourceDeepestDegraded(t *testing.T) {
	slow := dpC(1000, 1, 300)
	edges := dpJudged(
		dpE("svc-a", "svc-b", slow, dpOK),
		dpE("svc-b", "svc-c", slow, dpOK),
		dpE("svc-c", "svc-d", dpOK, dpOK),              // sağlıklı: zincir burada biter
		dpE("svc-a", "svc-x", dpOK, dpOK),              // sağlıklı kardeş
		dpE("svc-x", "svc-y", dpC(1000, 0, 900), dpOK), // kötü ama yolu sağlıklı kenardan geçiyor
		dpE("svc-c", "svc-b", slow, dpOK),              // döngü
	)
	i, path := likelySource("svc-a", edges)
	// svc-c→svc-b döngü kenarı yol üstündeki svc-b'ye geri dönemez.
	if i < 0 || edges[i].Parent != "svc-b" || edges[i].Child != "svc-c" {
		t.Fatalf("olası kaynak %d %+v", i, edges[max(i, 0)].DepHopEdge)
	}
	if strings.Join(path, ">") != "svc-a>svc-b>svc-c" {
		t.Errorf("yol %v", path)
	}
	// Kök kenarı sağlıklıysa kaynak yok.
	if i, _ := likelySource("svc-a", dpJudged(dpE("svc-a", "svc-x", dpOK, dpOK), dpE("svc-x", "svc-y", slow, dpOK))); i != -1 {
		t.Errorf("sağlıklı kökte kaynak seçildi: %d", i)
	}
	// Kuyruk asenkron sınır: üretici → kuyruk kenarı kaynak, tüketici değil.
	qs := dpJudged(dpE("svc-a", "queue:kafka:t1", slow, dpOK), dpE("queue:kafka:t1", "svc-cons", slow, dpOK))
	if i, _ := likelySource("svc-a", qs); i != 0 {
		t.Errorf("kuyruk sınırı: %d", i)
	}
}

func TestApplyVMOverridesHTTPEdges(t *testing.T) {
	edges := dpJudged(dpE("svc-a", "svc-b", dpOK, dpOK), dpE("svc-a", "db:postgres@pg-1", dpOK, dpOK))
	n := applyVM(edges, "svc-a",
		[]runtimesig.TargetRED{{Address: "svc-b.local:8080", Service: "svc-b", RPS: 10, P95Ms: 400, ErrorPct: 0}},
		[]runtimesig.TargetRED{{Address: "svc-b.local:8080", Service: "svc-b", RPS: 10, P95Ms: 100, ErrorPct: 0}})
	judgeAll(edges, 3600)
	if n != 1 || edges[0].m.Source != "vm" || !edges[0].Degraded || edges[1].m.Source != "mv" {
		t.Fatalf("VM uygulaması n=%d %+v / %+v", n, edges[0].m, edges[1].m)
	}
	v := dpView(edges[0], nil)
	if v.RPS == nil || v.Calls != nil || v.LatKind != "p95_ms" || !strings.Contains(v.Source, "unsampled") {
		t.Errorf("VM görünümü %+v", v)
	}
}

// fakeDepPath — depPathReader.
type fakeDepPath struct {
	g        dpGraph
	hopErr   error
	deploys  []chstore.DepDeploy
	self     []chstore.DepServiceRED
	sample   string
	sampleQ  *chstore.EdgeSampleQuery
	hopQs    []chstore.DepHopQuery
	baseDeps []chstore.Deploy
}

func (f *fakeDepPath) DependencyHop(ctx context.Context, q chstore.DepHopQuery) ([]chstore.DepHopEdge, error) {
	f.hopQs = append(f.hopQs, q)
	if f.hopErr != nil {
		return nil, f.hopErr
	}
	n := 0
	return f.g.fetch(&n)(ctx, q.Up, q.Nodes)
}
func (f *fakeDepPath) DependencyDeploys(context.Context, []string, time.Time, time.Time) ([]chstore.DepDeploy, bool, error) {
	return f.deploys, true, nil
}
func (f *fakeDepPath) DependencyServiceRED(context.Context, []string, chstore.PeriodWindow, chstore.PeriodWindow) ([]chstore.DepServiceRED, error) {
	return f.self, nil
}
func (f *fakeDepPath) EdgeSampleTrace(_ context.Context, q chstore.EdgeSampleQuery) (string, error) {
	f.sampleQ = &q
	return f.sample, nil
}
func (f *fakeDepPath) BaselineDeploys(context.Context, string, time.Time, time.Time) ([]chstore.Deploy, bool, error) {
	return f.baseDeps, true, nil
}

func TestDependencyPathContractDown(t *testing.T) {
	errs := dpC(1000, 120, 15)
	f := &fakeDepPath{
		g:        dpGraph{}.add(dpE("svc-a", "svc-b", errs, dpOK), dpE("svc-b", "db:postgres@pg-1", errs, dpOK), dpE("svc-a", "svc-c", dpOK, dpOK)),
		deploys:  []chstore.DepDeploy{{Service: "svc-b", Version: "2.4.1", FirstSeenNs: lkAnchor.Add(-20 * time.Minute).UnixNano()}},
		sample:   "4bf92f3577b34da6a3ce929d0e0e4736",
		baseDeps: []chstore.Deploy{{Service: "svc-b", Version: "2.4.1", TimeUnixNs: lkAnchor.Add(-21 * time.Minute).UnixNano(), Source: "event"}},
	}
	out, err := runDependencyPath(lkCtx(), Deps{}, f, dependencyPathArgs{Service: "svc-a", Env: "prod"}, lkAnchor)
	if err != nil {
		t.Fatal(err)
	}
	js := lkJSON(t, out)
	for _, want := range []string{`"likely_source":{`, `"label":"olası kaynak"`, `"path":["svc-a","svc-b","postgres@pg-1"]`,
		`"sample_trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"`, `svc-b → 2.4.1 @`, `"rollout_events"`, `"error_pct":12`,
		`"source":"topology_edges_5m (sampled)"`, `"vm_status":"not_configured`, `"kind":"day_before"`} {
		if !strings.Contains(js, want) {
			t.Errorf("çıktıda %s yok:\n%s", want, js)
		}
	}
	if f.sampleQ == nil || !f.sampleQ.PreferError || f.sampleQ.Child != "db:postgres@pg-1" {
		t.Errorf("örnek trace sorgusu %+v", f.sampleQ)
	}
	if len(f.hopQs) == 0 || f.hopQs[0].Env != "prod" || f.hopQs[0].Up || f.hopQs[0].Ref.To.After(f.hopQs[0].Cur.From) {
		t.Errorf("atlama sorgusu %+v", f.hopQs)
	}
	if f.hopQs[0].Cur.From.Sub(f.hopQs[0].Ref.From) != 24*time.Hour {
		t.Error("taban varsayılanı dün aynı saatler olmalı")
	}
}

func TestDependencyPathContractUpAndUnread(t *testing.T) {
	f := &fakeDepPath{
		g:    dpGraph{}.add(dpE("svc-gw", "svc-a", dpC(5000, 400, 80), dpC(5000, 5, 20)), dpE("svc-batch", "svc-a", dpC(50, 0, 10), dpC(50, 0, 10))),
		self: []chstore.DepServiceRED{{Service: "svc-gw", CurCalls: 9000, CurErrs: 500, CurP95Ms: 300, RefCalls: 9000, RefErrs: 10, RefP95Ms: 120}},
	}
	out, err := runDependencyPath(lkCtx(), Deps{}, f, dependencyPathArgs{Service: "svc-a", Direction: "up", Cluster: "ocp-a"}, lkAnchor)
	if err != nil {
		t.Fatal(err)
	}
	js := lkJSON(t, out)
	for _, want := range []string{`"upstream":{`, `"from":"svc-gw"`, `"caller_degraded_too":true`, `"direct_callers":2`, `cluster filter NOT applied`} {
		if !strings.Contains(js, want) {
			t.Errorf("çıktıda %s yok:\n%s", want, js)
		}
	}
	if strings.Contains(js, `"likely_source"`) {
		t.Error("yukarı kipte olası kaynak olmamalı")
	}
	// Okuma hatası: unread, uydurma yok.
	f2 := &fakeDepPath{hopErr: errors.New("timeout")}
	out, _ = runDependencyPath(lkCtx(), Deps{}, f2, dependencyPathArgs{Service: "svc-a"}, lkAnchor)
	js = lkJSON(t, out)
	if !strings.Contains(js, `"unread":["downstream hop 1: timeout"]`) || !strings.Contains(js, `"likely_source":null`) {
		t.Errorf("unread sözleşmesi:\n%s", js)
	}
}

func TestDependencyPathArgs(t *testing.T) {
	for _, a := range []dependencyPathArgs{{}, {Service: "svc-a", Direction: "sideways"}, {Service: "svc-a", Reference: "week_before"}} {
		if _, err := planDependencyPath(a, lkAnchor); err == nil {
			t.Errorf("%+v kabul edildi", a)
		}
	}
	p, err := planDependencyPath(dependencyPathArgs{Service: "svc-a", Depth: 9, RangeS: 999999, Direction: "both", Reference: "previous"}, lkAnchor)
	if err != nil || p.Depth != dpMaxDepth || p.RangeS != dpMaxRangeS || p.Ref.To != p.Cur.From {
		t.Errorf("plan %+v %v", p, err)
	}
	if !strings.Contains(dpCacheKey(p), "dependency_path:v1") || dpCacheKey(p) == dpCacheKey(dpPlan{Service: "svc-b", Depth: p.Depth, Cur: p.Cur, RangeS: p.RangeS}) {
		t.Error("önbellek anahtarı tüm girdileri taşımalı")
	}
}
