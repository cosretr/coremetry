package mcptools

// dependency_path.go — v0.10.2453 (operatör onayı 2026-11-22): CoSRE
// dependency_path. "neden yavaş, zincir", "hangi downstream yüzünden",
// "kim çağırıyor / etki alanı", "bağımlılık zinciri" soruları.
//
// Servisten başlayıp topology_edges_5m üstünde (çağrı, DB, messaging kenarları)
// her yönde ≤3 atlama yürür: atlama başına TEK MV okuması, düğüm başına ≤20
// kenar (trafiğe göre), atlama başına ≤20 genişletilen düğüm, toplam ≤60 kenar
// (iki yönde 40 aşağı + 20 yukarı). Döngü güvenli (ziyaret kümesi). Her kenar:
// şimdiki pencere vs taban (dün aynı saatler ya da önceki dönem — compare
// aracıyla aynı ReferenceWindow), farklar, iki uçta pencere içi deploy
// (service_version_5m; olası kaynağın uçları için rollout olayları da).
// HTTP kenarlarında sorulan servis ve olası kaynağın üst ucu için değerler
// örnekleme ÖNCESİ VM client histogramından (Deps.DownstreamRED), yoksa MV
// değerleri "sampled" etiketiyle.
//
// Olası kaynak: sorulan servisten başlayan, HER kenarı kötüleşmiş bir yolun en
// derin kenarı (eşitlikte skor). Kuyruk düğümü asenkron sınırdır: üretici →
// kuyruk kenarı kaynak olabilir, zincir tüketicilere geçmez. Kanıt: farklar,
// deploy işareti, tek sınırlı örnek trace (EdgeSampleTrace, ≤15 dk ham okuma).
// Yukarı yön (etki alanı): çağıranlar trafiğe göre + çağıranın KENDİ RED'i de
// kötüleşti mi (service_summary_5m, ≤20 servis).
// Sayılar okumadan aynen; okunamayan bölüm `unread` (okunamadı ≠ yok).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

// DependencyPathName — araç adı.
const DependencyPathName = "dependency_path"

const (
	dpDefaultRangeS = 3600
	dpMinRangeS     = 300
	dpMaxRangeS     = 86400
	dpDefaultDepth  = 2
	dpMaxDepth      = 3
	dpEdgeCap       = 60
	dpEdgeCapDownB  = 40 // iki yönde aşağı payı; yukarı = dpEdgeCap - bu
	dpOutEdges      = 12 // cevaptaki kenar satırı tavanı (yön başına)
	dpOutCallers    = 10
	dpMinCalls      = 20  // yargı için en az çağrı (pencere)
	dpErrPPMin      = 2.0 // hata artışı eşiği (yüzde puan)
	dpLatRatioMin   = 1.5 // gecikme oranı eşiği
	dpLatAbsMinMs   = 20.0
	dpTrafficDrop   = 0.5
	dpVMParentsMax  = 2 // VM okunan üst uç servis sayısı (sorulan + kaynak)
)

type dependencyPathArgs struct {
	Service   string `json:"service"`
	Env       string `json:"env,omitempty"`
	Cluster   string `json:"cluster,omitempty"`
	RangeS    int    `json:"range_s,omitempty"`
	Direction string `json:"direction,omitempty"`
	Depth     int    `json:"depth,omitempty"`
	Reference string `json:"reference,omitempty"`
}

// depPathReader — *chstore.Store karşılar; test sahte verir.
type depPathReader interface {
	DependencyHop(ctx context.Context, q chstore.DepHopQuery) ([]chstore.DepHopEdge, error)
	DependencyDeploys(ctx context.Context, services []string, from, to time.Time) ([]chstore.DepDeploy, bool, error)
	DependencyServiceRED(ctx context.Context, services []string, cur, ref chstore.PeriodWindow) ([]chstore.DepServiceRED, error)
	EdgeSampleTrace(ctx context.Context, q chstore.EdgeSampleQuery) (string, error)
	BaselineDeploys(ctx context.Context, service string, from, to time.Time) ([]chstore.Deploy, bool, error)
}

// dpPlan — doğrulanmış argümanlar + pencereler.
type dpPlan struct {
	Service, Env, Cluster, Dir, Kind string
	Depth, RangeS                    int
	Cur, Ref                         chstore.PeriodWindow
}

// planDependencyPath — SAF.
func planDependencyPath(a dependencyPathArgs, end time.Time) (dpPlan, error) {
	p := dpPlan{Service: strings.TrimSpace(a.Service), Env: strings.TrimSpace(a.Env), Cluster: strings.TrimSpace(a.Cluster)}
	if p.Service == "" {
		return p, errors.New("service is required — ask the user which service, never guess")
	}
	switch d := strings.ToLower(strings.TrimSpace(a.Direction)); d {
	case "", "down", "downstream":
		p.Dir = "down"
	case "up", "upstream":
		p.Dir = "up"
	case "both":
		p.Dir = "both"
	default:
		return p, fmt.Errorf("direction geçersiz: down | up | both (gelen %q)", a.Direction)
	}
	p.Depth = a.Depth
	if p.Depth <= 0 {
		p.Depth = dpDefaultDepth
	}
	p.Depth = min(p.Depth, dpMaxDepth)
	p.RangeS = a.RangeS
	if p.RangeS <= 0 {
		p.RangeS = dpDefaultRangeS
	}
	p.RangeS = min(max(p.RangeS, dpMinRangeS), dpMaxRangeS)
	p.RangeS = (p.RangeS + 299) / 300 * 300
	p.Kind = strings.ToLower(strings.TrimSpace(a.Reference))
	if p.Kind == "" {
		p.Kind = "day_before"
	}
	if p.Kind != "day_before" && p.Kind != "previous" {
		return p, fmt.Errorf("reference geçersiz: day_before | previous (gelen %q)", a.Reference)
	}
	to := end.UTC().Truncate(5 * time.Minute) // yalnız tamamlanmış kovalar
	p.Cur = chstore.PeriodWindow{From: to.Add(-time.Duration(p.RangeS) * time.Second), To: to}
	ref, err := ReferenceWindow(p.Kind, p.Cur)
	if err != nil {
		return p, err
	}
	p.Ref = ref
	return p, nil
}

// dpEdge — yürüyüşün kenarı (ham düğüm kimlikleri) + yargı.
type dpEdge struct {
	chstore.DepHopEdge
	Hop int
	Up  bool
	// m — yargıda kullanılan ölçü (MV ya da VM).
	m        dpMeasure
	Degraded bool
	Signals  []string
	Score    float64
}

// dpMeasure — kenarın kıyas ölçüsü; kaynağı etiketli.
type dpMeasure struct {
	Source             string // "vm" | "mv"
	CurVol, RefVol     float64
	CurErrPct, RefErrP float64
	CurLat, RefLat     float64
	LatLabel           string // avg_ms (mv) | p95_ms (vm)
}

// dpExpandable — SAF: düğüm genişletilir mi? Servis ve kuyruk düğümleri
// (kuyruk aşağıda tüketicilere, yukarıda üreticilere götürür); db / ext / node yaprak.
func dpExpandable(id string) bool {
	k, _ := chstore.TopologyNodeIdentity(id)
	return k == "" || k == chstore.NodeKindQueue
}

// dpFetch — bir atlamanın okuyucusu (yürüyüşü SQL'den ayırır).
type dpFetch func(ctx context.Context, up bool, frontier []string) ([]chstore.DepHopEdge, error)

// walkDependencies — SAF yürüyüş (fetch dışında yan etkisiz): BFS, ≤depth
// atlama, ziyaret kümesi (döngü), atlama başına ≤DepHopFrontierMax düğüm,
// toplam ≤edgeCap kenar. truncated = bir tavan devreye girdi.
func walkDependencies(ctx context.Context, fetch dpFetch, root string, up bool, depth, edgeCap int) (edges []dpEdge, truncated bool, err error) {
	visited := map[string]bool{root: true}
	seen := map[string]bool{}
	frontier := []string{root}
	for hop := 1; hop <= depth && len(frontier) > 0; hop++ {
		rows, ferr := fetch(ctx, up, frontier)
		if ferr != nil {
			return edges, truncated, fmt.Errorf("hop %d: %w", hop, ferr)
		}
		// Deterministik: şimdiki çağrı azalan, sonra ad.
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Cur.Calls != rows[j].Cur.Calls {
				return rows[i].Cur.Calls > rows[j].Cur.Calls
			}
			if rows[i].Parent != rows[j].Parent {
				return rows[i].Parent < rows[j].Parent
			}
			return rows[i].Child < rows[j].Child
		})
		perNode := map[string]int{}
		var next []string
		for _, r := range rows {
			if r.Parent == r.Child {
				continue
			}
			known, other := r.Parent, r.Child
			if up {
				known, other = r.Child, r.Parent
			}
			key := r.Parent + "\x00" + r.Child
			if seen[key] {
				continue
			}
			if perNode[known] >= chstore.DepHopPerNode {
				truncated = true
				continue
			}
			if len(edges) >= edgeCap {
				return edges, true, nil
			}
			seen[key] = true
			perNode[known]++
			edges = append(edges, dpEdge{DepHopEdge: r, Hop: hop, Up: up})
			if !visited[other] && dpExpandable(other) {
				visited[other] = true
				if len(next) >= chstore.DepHopFrontierMax {
					truncated = true
					continue
				}
				next = append(next, other)
			}
		}
		frontier = next
	}
	return edges, truncated, nil
}

// dpMeasureMV — SAF: MV hücrelerinden ölçü.
func dpMeasureMV(e chstore.DepHopEdge) dpMeasure {
	return dpMeasure{Source: "mv", CurVol: float64(e.Cur.Calls), RefVol: float64(e.Ref.Calls),
		CurErrPct: pct(e.Cur.Errors, e.Cur.Calls), RefErrP: pct(e.Ref.Errors, e.Ref.Calls),
		CurLat: e.Cur.AvgMs, RefLat: e.Ref.AvgMs, LatLabel: "avg_ms"}
}

// judgeEdge — SAF: kötüleşme yargısı. Kural (sabitler yukarıda):
//   - errors:  hata % artışı ≥ 2 puan VE şimdiki ≥ 2× taban;
//   - latency: taban > 0, şimdiki ≥ 1.5× taban VE +20 ms;
//   - traffic_drop (yalnız işaret): şimdiki hacim < taban × 0.5;
//   - new_edge (yalnız işaret): tabanda hiç trafik yok.
//
// Hacim < 20 çağrı → yargı yok (low_volume). skor = hata puanı + 20 × göreli
// gecikme artışı (≤10×), sıralama içindir.
func judgeEdge(m dpMeasure, curCalls float64) (degraded bool, signals []string, score float64) {
	if curCalls < dpMinCalls {
		if m.RefVol > 0 && m.CurVol < m.RefVol*dpTrafficDrop {
			return false, []string{"traffic_drop"}, 0
		}
		return false, []string{"low_volume"}, 0
	}
	if m.RefVol <= 0 {
		return false, []string{"new_edge"}, 0
	}
	pp := m.CurErrPct - m.RefErrP
	if pp >= dpErrPPMin && m.CurErrPct >= 2*m.RefErrP {
		signals = append(signals, "errors")
		score += pp
	}
	if m.RefLat > 0 && m.CurLat >= dpLatRatioMin*m.RefLat && m.CurLat-m.RefLat >= dpLatAbsMinMs {
		signals = append(signals, "latency")
		score += 20 * math.Min((m.CurLat-m.RefLat)/m.RefLat, 10)
	}
	degraded = len(signals) > 0
	if m.CurVol < m.RefVol*dpTrafficDrop {
		signals = append(signals, "traffic_drop")
	}
	return degraded, signals, round4(score)
}

// dpCurCalls — yargı hacmi: MV çağrı sayısı ya da VM rps × pencere.
func dpCurCalls(m dpMeasure, winS int) float64 {
	if m.Source == "vm" {
		return m.CurVol * float64(winS)
	}
	return m.CurVol
}

func judgeAll(edges []dpEdge, winS int) {
	for i := range edges {
		if edges[i].m.Source == "" {
			edges[i].m = dpMeasureMV(edges[i].DepHopEdge)
		}
		edges[i].Degraded, edges[i].Signals, edges[i].Score = judgeEdge(edges[i].m, dpCurCalls(edges[i].m, winS))
	}
}

// likelySource — SAF: sorulan servisten başlayan, her kenarı kötüleşmiş
// aşağı yolların en derin kenarı (eşitlikte skor, sonra ad). Kuyruk düğümünde
// zincir durur (asenkron). -1 = yok. path = kök dahil düğüm dizisi.
func likelySource(root string, edges []dpEdge) (idx int, path []string) {
	out := map[string][]int{}
	for i, e := range edges {
		if !e.Up && e.Degraded {
			out[e.Parent] = append(out[e.Parent], i)
		}
	}
	best, bestDepth := -1, 0
	var bestPath []string
	var dfs func(node string, depth int, onPath map[string]bool, trail []string)
	dfs = func(node string, depth int, onPath map[string]bool, trail []string) {
		for _, i := range out[node] {
			e := edges[i]
			if onPath[e.Child] {
				continue
			}
			d := depth + 1
			t := append(append([]string{}, trail...), e.Child)
			better := best < 0 || d > bestDepth || (d == bestDepth && (e.Score > edges[best].Score ||
				(e.Score == edges[best].Score && e.Parent+e.Child < edges[best].Parent+edges[best].Child)))
			if better {
				best, bestDepth, bestPath = i, d, t
			}
			if k, _ := chstore.TopologyNodeIdentity(e.Child); k == chstore.NodeKindQueue || !dpExpandable(e.Child) {
				continue
			}
			onPath[e.Child] = true
			dfs(e.Child, d, onPath, t)
			delete(onPath, e.Child)
		}
	}
	dfs(root, 0, map[string]bool{root: true}, []string{root})
	return best, bestPath
}

// applyVM — SAF: VM hedef satırlarını (şimdiki + taban) üst ucu `parent`
// olan HTTP kenarlarına uygular (hedef = çözülmüş servis ya da adres).
func applyVM(edges []dpEdge, parent string, cur, ref []runtimesig.TargetRED) int {
	find := func(ts []runtimesig.TargetRED, name string) (runtimesig.TargetRED, bool) {
		for _, t := range ts {
			if t.Service == name || t.Address == name {
				return t, true
			}
		}
		return runtimesig.TargetRED{}, false
	}
	n := 0
	for i := range edges {
		e := &edges[i]
		if e.Up || e.Parent != parent {
			continue
		}
		k, name := chstore.TopologyNodeIdentity(e.Child)
		if k != "" && k != chstore.NodeKindExternal {
			continue // db / kuyruk: HTTP histogramı yok
		}
		c, ok := find(cur, name)
		if !ok || c.P95Ms < 0 {
			continue
		}
		r, _ := find(ref, name)
		refLat := r.P95Ms
		if refLat < 0 {
			refLat = 0
		}
		e.m = dpMeasure{Source: "vm", CurVol: c.RPS, RefVol: r.RPS, CurErrPct: c.ErrorPct, RefErrP: r.ErrorPct,
			CurLat: c.P95Ms, RefLat: refLat, LatLabel: "p95_ms"}
		n++
	}
	return n
}

// dpNodeView — SAF: ham kimlik → (gösterim adı, tür).
func dpNodeView(id string) (string, string) {
	k, name := chstore.TopologyNodeIdentity(id)
	if k == "" {
		k = chstore.NodeKindService
	}
	return name, k
}

// dpEdgeView — cevap satırı.
type dpEdgeView struct {
	From       string   `json:"from"`
	To         string   `json:"to"`
	ToKind     string   `json:"to_kind"`
	Hop        int      `json:"hop"`
	Source     string   `json:"source"`
	Calls      *uint64  `json:"calls,omitempty"`
	RefCalls   *uint64  `json:"ref_calls,omitempty"`
	RPS        *float64 `json:"rps,omitempty"`
	RefRPS     *float64 `json:"ref_rps,omitempty"`
	ErrPct     float64  `json:"error_pct"`
	RefErrPct  float64  `json:"ref_error_pct"`
	ErrPP      float64  `json:"error_pct_change_pp"`
	LatMs      float64  `json:"latency_ms"`
	RefLatMs   float64  `json:"ref_latency_ms"`
	LatKind    string   `json:"latency_kind"`
	LatPct     *float64 `json:"latency_change_pct,omitempty"`
	P99Worst   *float64 `json:"p99_worst_bucket_ms,omitempty"`
	Degraded   bool     `json:"degraded"`
	Signals    []string `json:"signals,omitempty"`
	Score      float64  `json:"score,omitempty"`
	Deploys    []string `json:"deploys_in_window,omitempty"`
	LikelySrc  bool     `json:"likely_source,omitempty"`
	TrafficPct *float64 `json:"traffic_change_pct,omitempty"`
}

func dpView(e dpEdge, deploys map[string][]string) dpEdgeView {
	from, _ := dpNodeView(e.Parent)
	to, kind := dpNodeView(e.Child)
	m := e.m
	v := dpEdgeView{From: from, To: to, ToKind: kind, Hop: e.Hop, ErrPct: round4(m.CurErrPct), RefErrPct: round4(m.RefErrP),
		ErrPP: round4(m.CurErrPct - m.RefErrP), LatMs: round4(m.CurLat), RefLatMs: round4(m.RefLat), LatKind: m.LatLabel,
		Degraded: e.Degraded, Signals: e.Signals, Score: e.Score, LatPct: relPct(m.CurLat, m.RefLat), TrafficPct: relPct(m.CurVol, m.RefVol)}
	if m.Source == "vm" {
		v.Source = "vm_http_client (unsampled)"
		c, r := round4(m.CurVol), round4(m.RefVol)
		v.RPS, v.RefRPS = &c, &r
	} else {
		v.Source = "topology_edges_5m (" + spanLabel(e.Weighted) + ")"
		c, r := e.Cur.Calls, e.Ref.Calls
		v.Calls, v.RefCalls = &c, &r
		if e.Cur.P99Ms > 0 {
			p := round4(e.Cur.P99Ms)
			v.P99Worst = &p
		}
	}
	for _, id := range []string{e.Parent, e.Child} {
		if name, k := dpNodeView(id); k == chstore.NodeKindService {
			v.Deploys = append(v.Deploys, deploys[name]...)
		}
	}
	return v
}

// rankEdges — SAF: kötüleşenler önce (skor azalan), sonra trafik; ≤n.
func rankEdges(edges []dpEdge, n int) []int {
	idx := make([]int, len(edges))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ea, eb := edges[idx[a]], edges[idx[b]]
		if ea.Degraded != eb.Degraded {
			return ea.Degraded
		}
		if ea.Score != eb.Score {
			return ea.Score > eb.Score
		}
		return ea.Cur.Calls > eb.Cur.Calls
	})
	if len(idx) > n {
		idx = idx[:n]
	}
	return idx
}

// dpServices — SAF: kenarlardaki servis düğümleri (sıralı, ≤max).
func dpServices(root string, edges []dpEdge, maxN int) []string {
	set := map[string]bool{root: true}
	out := []string{root}
	for _, e := range edges {
		for _, id := range []string{e.Parent, e.Child} {
			if name, k := dpNodeView(id); k == chstore.NodeKindService && !set[name] && len(out) < maxN {
				set[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

func runDependencyPath(ctx context.Context, d Deps, r depPathReader, a dependencyPathArgs, end time.Time) (map[string]any, error) {
	p, err := planDependencyPath(a, end)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"tool": DependencyPathName, "service": p.Service, "direction": p.Dir, "depth": p.Depth,
		"window":    map[string]any{"from": iso(p.Cur.From), "to": iso(p.Cur.To), "range_s": p.RangeS},
		"reference": map[string]any{"kind": p.Kind, "from": iso(p.Ref.From), "to": iso(p.Ref.To)}}
	notes := []string{
		"numbers are quoted from the reads; do not compute values that are not in the result",
		"mv rows are stored (sampled) spans: calls are not request totals; latency_kind avg_ms (mv) or p95_ms (vm, unsampled)",
		"degraded = error % up ≥2 pp and ≥2× baseline, or latency ≥1.5× baseline and +20 ms; <20 calls is not judged",
	}
	if p.Env != "" {
		out["env"] = p.Env
	}
	if p.Cluster != "" {
		notes = append(notes, "cluster: topology edges carry no k8s cluster dimension — cluster filter NOT applied to edges")
	}
	if r == nil {
		out["unread"] = []string{"topology: ClickHouse reader not configured"}
		out["notes"] = notes
		return out, nil
	}
	var unread []string
	fetch := func(c context.Context, up bool, frontier []string) ([]chstore.DepHopEdge, error) {
		return r.DependencyHop(c, chstore.DepHopQuery{Up: up, Nodes: frontier, Env: p.Env, Cur: p.Cur, Ref: p.Ref})
	}
	var down, upE []dpEdge
	truncated := false
	downCap, upCap := dpEdgeCap, dpEdgeCap
	if p.Dir == "both" {
		downCap, upCap = dpEdgeCapDownB, dpEdgeCap-dpEdgeCapDownB
	}
	if p.Dir != "up" {
		es, tr, werr := walkDependencies(ctx, fetch, p.Service, false, p.Depth, downCap)
		down, truncated = es, truncated || tr
		if werr != nil {
			unread = append(unread, "downstream "+werr.Error())
		}
	}
	if p.Dir != "down" {
		es, tr, werr := walkDependencies(ctx, fetch, p.Service, true, p.Depth, upCap)
		upE, truncated = es, truncated || tr
		if werr != nil {
			unread = append(unread, "upstream "+werr.Error())
		}
	}
	winS := int(p.Cur.To.Sub(p.Cur.From).Seconds())
	judgeAll(down, winS)
	judgeAll(upE, winS)
	// v0.10.2457 — sampling weight: MV calls are represented calls.
	weighted := dpAnyWeighted(down) || dpAnyWeighted(upE)
	if weighted {
		notes[1] = dpWeightedNote
	}

	// VM (örnekleme öncesi) — sorulan servis + olası kaynağın üst ucu.
	src, path := likelySource(p.Service, down)
	vmStatus := "not_configured: no VictoriaMetrics HTTP client histogram reader — edge values are " + spanLabel(weighted) + " MV values"
	if d.DownstreamRED != nil && len(down) > 0 {
		parents := []string{p.Service}
		if src >= 0 {
			if name, k := dpNodeView(down[src].Parent); k == chstore.NodeKindService && name != p.Service {
				parents = append(parents, name)
			}
		}
		applied := 0
		for i, par := range parents {
			if i >= dpVMParentsMax {
				break
			}
			cur, cerr := d.DownstreamRED(ctx, par, p.Cur.From, p.Cur.To)
			if cerr != nil {
				unread = append(unread, "vm "+par+": "+cerr.Error())
				continue
			}
			ref, rerr := d.DownstreamRED(ctx, par, p.Ref.From, p.Ref.To)
			if rerr != nil {
				unread = append(unread, "vm baseline "+par+": "+rerr.Error())
				continue
			}
			applied += applyVM(down, par, cur.Targets, ref.Targets)
		}
		vmStatus = fmt.Sprintf("applied to %d HTTP edge(s) (rate window ≤1 h ending at each window end)", applied)
		if applied > 0 {
			judgeAll(down, winS)
			src, path = likelySource(p.Service, down)
		}
	}
	out["vm_status"] = vmStatus

	// Deploy işaretleri (pencere içinde doğan sürümler) — tüm servis uçları.
	deploys := map[string][]string{}
	svcs := dpServices(p.Service, append(append([]dpEdge{}, down...), upE...), 40)
	if ds, covered, derr := r.DependencyDeploys(ctx, svcs, p.Cur.From, p.Cur.To); derr != nil {
		unread = append(unread, "deploys: "+derr.Error())
	} else if !covered {
		unread = append(unread, "deploys: service_version_5m does not cover the 24 h lookback yet (unread, not 'no deploy')")
	} else {
		for _, dp := range ds {
			deploys[dp.Service] = append(deploys[dp.Service], dp.Service+" → "+dp.Version+" @ "+iso(time.Unix(0, dp.FirstSeenNs)))
		}
	}

	if p.Dir != "up" {
		rows := []dpEdgeView{}
		for _, i := range rankEdges(down, dpOutEdges) {
			v := dpView(down[i], deploys)
			v.LikelySrc = i == src
			rows = append(rows, v)
		}
		out["downstream"] = rows
		out["downstream_edges_walked"] = len(down)
		nDeg := 0
		for _, e := range down {
			if e.Degraded {
				nDeg++
			}
		}
		out["downstream_degraded"] = nDeg
		if src >= 0 {
			out["likely_source"] = buildLikelySource(ctx, r, p, down[src], path, deploys, &unread)
		} else {
			out["likely_source"] = nil
			notes = append(notes, "no degraded edge on any path starting at the service — say so; do not name a source")
		}
	}
	if p.Dir != "down" {
		out["upstream"] = buildBlastRadius(ctx, r, p, upE, deploys, &unread)
	}
	if truncated {
		notes = append(notes, fmt.Sprintf("edge caps hit (≤%d edges, ≤%d per node, ≤%d nodes per hop) — the walk is partial", dpEdgeCap, chstore.DepHopPerNode, chstore.DepHopFrontierMax))
		out["truncated"] = true
	}
	notes = append(notes, "a queue node is an async boundary: the chain does not continue to its consumers")
	if len(unread) > 0 {
		out["unread"] = unread
	}
	out["notes"] = notes
	return out, nil
}

// buildLikelySource — olası kaynak kanıtı: farklar, deploy (MV + rollout
// olayları iki uçta), tek sınırlı örnek trace.
func buildLikelySource(ctx context.Context, r depPathReader, p dpPlan, e dpEdge, path []string, deploys map[string][]string, unread *[]string) map[string]any {
	v := dpView(e, deploys)
	v.LikelySrc = true
	names := make([]string, 0, len(path))
	for _, id := range path {
		n, _ := dpNodeView(id)
		names = append(names, n)
	}
	ls := map[string]any{"label": "olası kaynak", "edge": v, "path": names}
	// Rollout olayları (deploy olay tablosu) — yalnız kaynağın servis uçları.
	var evs []string
	for _, id := range []string{e.Parent, e.Child} {
		name, k := dpNodeView(id)
		if k != chstore.NodeKindService {
			continue
		}
		ds, _, err := r.BaselineDeploys(ctx, name, p.Cur.From, p.Cur.To)
		if err != nil {
			*unread = append(*unread, "rollout events "+name+": "+err.Error())
			continue
		}
		for _, dd := range ds {
			if dd.Source == "event" { // deployEventEntries; MV sürümleri deploys_in_window'da
				evs = append(evs, name+" → "+dd.Version+" @ "+iso(time.Unix(0, dd.TimeUnixNs))+" ("+dd.Source+")")
			}
		}
	}
	if len(evs) > 0 {
		ls["rollout_events"] = evs
	}
	preferErr := false
	for _, s := range e.Signals {
		if s == "errors" {
			preferErr = true
		}
	}
	id, err := r.EdgeSampleTrace(ctx, chstore.EdgeSampleQuery{Parent: e.Parent, Child: e.Child, From: p.Cur.From, To: p.Cur.To, PreferError: preferErr})
	switch {
	case err != nil:
		*unread = append(*unread, "sample trace: "+err.Error())
	case id != "":
		ls["sample_trace_id"] = id
	default:
		ls["sample_trace_note"] = "no stored trace through this edge in the last ≤15 min of the window (sampled store; not proof of absence)"
	}
	return ls
}

// dpCallerView — etki alanı satırı.
type dpCallerView struct {
	dpEdgeView
	SelfDegraded *bool    `json:"caller_degraded_too,omitempty"`
	SelfSignals  []string `json:"caller_signals,omitempty"`
}

// buildBlastRadius — SAF olmayan ince sarmal: çağıranların kendi RED'i okunur.
func buildBlastRadius(ctx context.Context, r depPathReader, p dpPlan, edges []dpEdge, deploys map[string][]string, unread *[]string) map[string]any {
	idx := make([]int, len(edges))
	for i := range idx {
		idx[i] = i
	}
	// Etki alanı: trafiğe göre (hop önce).
	sort.SliceStable(idx, func(a, b int) bool {
		ea, eb := edges[idx[a]], edges[idx[b]]
		if ea.Hop != eb.Hop {
			return ea.Hop < eb.Hop
		}
		return ea.Cur.Calls > eb.Cur.Calls
	})
	if len(idx) > dpOutCallers {
		idx = idx[:dpOutCallers]
	}
	var callers []string
	for _, i := range idx {
		if name, k := dpNodeView(edges[i].Parent); k == chstore.NodeKindService {
			callers = append(callers, name)
		}
	}
	self := map[string]chstore.DepServiceRED{}
	selfRead := false
	if len(callers) > 0 {
		rows, err := r.DependencyServiceRED(ctx, callers, p.Cur, p.Ref)
		if err != nil {
			*unread = append(*unread, "caller RED: "+err.Error())
		} else {
			selfRead = true
			for _, s := range rows {
				self[s.Service] = s
			}
		}
	}
	rows := []dpCallerView{}
	nDeg := 0
	for _, i := range idx {
		v := dpCallerView{dpEdgeView: dpView(edges[i], deploys)}
		if name, k := dpNodeView(edges[i].Parent); k == chstore.NodeKindService && selfRead {
			if s, ok := self[name]; ok {
				m := dpMeasure{Source: "mv", CurVol: float64(s.CurCalls), RefVol: float64(s.RefCalls),
					CurErrPct: pct(s.CurErrs, s.CurCalls), RefErrP: pct(s.RefErrs, s.RefCalls), CurLat: s.CurP95Ms, RefLat: s.RefP95Ms}
				deg, sig, _ := judgeEdge(m, float64(s.CurCalls))
				v.SelfDegraded, v.SelfSignals = &deg, sig
				if deg {
					nDeg++
				}
			}
		}
		rows = append(rows, v)
	}
	direct := 0
	for _, e := range edges {
		if e.Hop == 1 {
			direct++
		}
	}
	return map[string]any{"callers": rows, "direct_callers": direct, "edges_walked": len(edges), "callers_degraded_too": nDeg,
		"note": "callers ranked by hop then traffic; caller_degraded_too judges the caller's OWN service RED (service_summary_5m p95 / error %) with the same rule"}
}

// dpCacheKey — SAF: TÜM girdiler.
func dpCacheKey(p dpPlan) string {
	return fmt.Sprintf("mcp:dependency_path:v1:%s:%d:%d:%d", fnvHex(p.Service, p.Env, p.Cluster, p.Dir, p.Kind), p.Depth, p.Cur.To.Unix(), p.RangeS)
}

func dependencyPathTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             DependencyPathName,
		ShortDescription: "Bağımlılık zinciri (≤3 atlama, aşağı/yukarı): kenar RED'i vs dün, olası kaynak + örnek trace, etki alanı.",
		Description: "Answer 'why is X slow — which dependency in the chain', 'hangi downstream yüzünden', 'who calls X / blast radius' by walking topology edges (calls, DB, messaging) up to depth 3 from ONE service. " +
			"Returns downstream[] (≤12 edges, degraded first: from, to, to_kind, hop, source mv|vm, calls or rps vs ref, error_pct vs ref_error_pct, latency_ms vs ref_latency_ms, signals, deploys_in_window), " +
			"likely_source (the deepest degraded edge on an all-degraded path from the service: edge, path, sample_trace_id, rollout_events) or null, and upstream (callers ranked by traffic, caller_degraded_too from the caller's own RED). " +
			"Baseline: day_before (default) or previous. HTTP edges of the service use unsampled VictoriaMetrics client metrics when configured; other values are sampled 5-minute MV values. " +
			"COST: ≤3 MV reads per direction (≤60 edges), one deploy MV read, ≤4 metric queries, one bounded raw read for the sample trace; cached 60 s. " +
			"Never name a source that is not likely_source; unread lists sections that failed (not 'none'). For one hop of HTTP targets only use downstream_calls; for 'what changed' on the service itself use compare_baseline.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service":   map[string]any{"type": "string", "description": "Exact service name (from list_services / resolve_entity)."},
				"env":       map[string]any{"type": "string", "description": "Deployment environment of the walked services (deploy_env). Empty = all."},
				"cluster":   map[string]any{"type": "string", "description": "Accepted for scope; topology edges have no cluster dimension, so it is reported, not applied."},
				"direction": map[string]any{"type": "string", "enum": []string{"down", "up", "both"}, "description": "down (default): dependencies and likely source; up: callers / blast radius; both."},
				"depth":     map[string]any{"type": "integer", "minimum": 1, "maximum": dpMaxDepth, "description": "Hops per direction. Default 2."},
				"range_s":   map[string]any{"type": "integer", "minimum": dpMinRangeS, "maximum": dpMaxRangeS, "description": "Window seconds ending at the chat's window end. Default 3600."},
				"reference": map[string]any{"type": "string", "enum": []string{"day_before", "previous"}, "description": "Baseline: day_before (default, same hours yesterday) or previous (same length right before)."},
			},
			"required": []string{"service"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a dependencyPathArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			var r depPathReader
			if d.Store != nil {
				r = d.Store
			}
			end := nowOrAnchor(ctx)
			p, err := planDependencyPath(a, end)
			if err != nil {
				return nil, err
			}
			run := func(c context.Context) (any, error) { return runDependencyPath(c, d, r, a, end) }
			if d.Memo == nil || r == nil {
				return run(ctx)
			}
			body, err := d.Memo(ctx, dpCacheKey(p), cbCacheTTL, func(c context.Context) (any, bool, error) {
				out, err := run(c)
				ok := err == nil
				if m, isMap := out.(map[string]any); isMap && m["unread"] != nil {
					ok = false // kısmi sonuç önbelleğe girmez
				}
				return out, ok, err
			})
			if err != nil {
				return nil, err
			}
			return json.RawMessage(body), nil
		},
	}
}

// DepPathSummary — v0.10.2453: CoSRE faz-B bağımlılık adımının EK kanıtı
// (skora girmez). dependency_path'in aşağı yön çıktısından türetilir.
type DepPathSummary struct {
	Walked   int `json:"walked"`
	Degraded int `json:"degraded"`
	// Source — olası kaynak kenarı (yoksa nil).
	Source *DepPathSource `json:"likelySource,omitempty"`
	Unread []string       `json:"unread,omitempty"`
}

// DepPathSource — olası kaynak özeti (değerler araç çıktısından aynen).
type DepPathSource struct {
	Path          []string   `json:"path"`
	Edge          dpEdgeView `json:"edge"`
	SampleTraceID string     `json:"sampleTraceId,omitempty"`
}

// DependencyPathSummary — servis için aşağı yön, derinlik 2, taban dün aynı
// saatler; pencere = [from, to) (5 dk kovalarına yuvarlanır, ≤24 sa).
func DependencyPathSummary(ctx context.Context, d Deps, service string, from, to time.Time) (DepPathSummary, error) {
	var out DepPathSummary
	if d.Store == nil {
		return out, errors.New("ClickHouse reader not configured")
	}
	res, err := runDependencyPath(ctx, d, d.Store, dependencyPathArgs{Service: service, Direction: "down", Depth: dpDefaultDepth,
		RangeS: int(to.Sub(from).Seconds())}, to)
	if err != nil {
		return out, err
	}
	return depPathSummaryOf(res), nil
}

// depPathSummaryOf — SAF: araç çıktısı → özet.
func depPathSummaryOf(res map[string]any) DepPathSummary {
	var out DepPathSummary
	out.Walked, _ = res["downstream_edges_walked"].(int)
	out.Degraded, _ = res["downstream_degraded"].(int)
	out.Unread, _ = res["unread"].([]string)
	if ls, ok := res["likely_source"].(map[string]any); ok {
		src := &DepPathSource{}
		src.Path, _ = ls["path"].([]string)
		src.Edge, _ = ls["edge"].(dpEdgeView)
		src.SampleTraceID, _ = ls["sample_trace_id"].(string)
		out.Source = src
	}
	return out
}
