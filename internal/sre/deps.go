package sre

// deps.go — v0.10.1287 (AI SRE Faz 1b): bağımlılık adımı. Üç MV okuması
// paralel (adım tavanı 8 s):
//
//	aşağı akış    GetServiceGraphTopN(servis, pencere, 20) — topology_edges_5m
//	              (ServiceNeighbors DEĞİL: ham spans)
//	korelasyon    GetCorrelatedChangesMVTop(başlangıç, w, 4w, havuz 50) +
//	              MarkCorrelationCauses (kenar + yön kuralı; causeEligible)
//	yukarı akış   GetBlastRadiusBatch([servis]) — service_callers_5m
//
// Bulgu = YALNIZ "olası neden" uygun korele servis (kenarı doğrulanmış aşağı
// akış kötüleşmesi / trafik kaybı ya da yukarı akış yük sıçraması; ≤3, uyarı).
// Korelasyon nedensellik değildir — gerekçe bunu söyler. Topoloji okunamazsa
// hiçbir satır uygun değildir (kenarsız yayılım iddiası yok).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

const depsCausesMax = 3

// depsPayload — adım yükü.
type depsPayload struct {
	Downstream []depsEdge   `json:"downstream"`
	Causes     []depsCause  `json:"causes"`
	Callers    int          `json:"callers"`
	Cascading  int          `json:"cascading"`
	Upstream   []depsCaller `json:"upstream,omitempty"`
	// Exact — follow-up 2026-11-21: hedef başına giden RED (VM, örnekleme öncesi; ≤5).
	Exact []runtimesig.TargetRED `json:"exact,omitempty"`
	// Path — v0.10.2453: dependency_path özeti (çok-atlamalı, dün aynı saatler
	// tabanı). EK kanıt: bulgu / skor / adım durumu ETKİLEMEZ.
	Path *mcptools.DepPathSummary `json:"path,omitempty"`
}

const depsExactPayloadMax = 5

type depsEdge struct {
	Target    string  `json:"target"`
	Calls     uint64  `json:"calls"`
	ErrorRate float64 `json:"errorRate"`
	AvgMs     float64 `json:"avgMs"`
}

type depsCause struct {
	Service   string `json:"service"`
	Relation  string `json:"relation"`
	Direction string `json:"direction"`
}

type depsCaller struct {
	Service        string  `json:"service"`
	RPS            float64 `json:"rps"`
	ErrorRate      float64 `json:"errorRate"`
	HasOpenProblem bool    `json:"hasOpenProblem,omitempty"`
}

// relationTR — SAF.
func relationTR(r string) string {
	switch r {
	case chstore.RelationDownstream:
		return "aşağı akış bağımlılığı (servis onu çağırıyor)"
	case chstore.RelationUpstream:
		return "yukarı akış çağıran (servisi çağırıyor)"
	case chstore.RelationBoth:
		return "iki yönlü bağımlılık"
	}
	return "bağlantı doğrulanamadı"
}

// depsResult — üç okumanın ham sonucu (SAF birleştirici girdisi).
type depsResult struct {
	edges   []chstore.ServiceEdge
	edgeErr error
	changes []chstore.ChangedService
	chgErr  error
	blast   *chstore.BlastRadius
	blstErr error
	// exact — follow-up 2026-11-21: hedef başına giden RED (VM client
	// histogramı, örnekleme öncesi). Okuyucu yoksa ikisi de nil (künye yok);
	// adım durumunu ETKİLEMEZ (topoloji / korelasyon / blast sayımı aynen).
	exact    *runtimesig.DownstreamReport
	exactErr error
	// path — v0.10.2453: dependency_path özeti; okuyucu yoksa ikisi de nil.
	// Adım durumunu, bulguları ve skoru ETKİLEMEZ (yalnız yük + künye).
	path    *mcptools.DepPathSummary
	pathErr error
}

// runDeps — adım (okumalar paralel, birleştirme SAF depsStep).
func runDeps(ctx context.Context, rd readers, req depsRequest) (Step, []Finding, *DepsFacts) {
	var res depsResult
	var wg sync.WaitGroup
	durs := make([]int64, 5)
	if rd.depPath != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := time.Now()
			sum, err := rd.depPath(ctx, req.Service, req.From, req.To)
			durs[4] = time.Since(s).Milliseconds()
			if err != nil {
				res.pathErr = err
				return
			}
			res.path = &sum
		}()
	}
	if rd.downstreamRED != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := time.Now()
			rep, err := rd.downstreamRED(ctx, req.Service, req.From, req.To)
			durs[3] = time.Since(s).Milliseconds()
			if err != nil {
				res.exactErr = err
				return
			}
			res.exact = &rep
		}()
	}
	if rd.graph != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := time.Now()
			res.edges, res.edgeErr = rd.graph(ctx, req.Service, req.From, req.To)
			durs[0] = time.Since(s).Milliseconds()
		}()
	} else {
		res.edgeErr = fmt.Errorf("topoloji okuyucusu bağlı değil")
	}
	if rd.changes != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := time.Now()
			res.changes, res.chgErr = rd.changes(ctx, req.At, req.WindowSec)
			durs[1] = time.Since(s).Milliseconds()
		}()
	} else {
		res.chgErr = fmt.Errorf("korelasyon okuyucusu bağlı değil")
	}
	if rd.blast != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := time.Now()
			m, err := rd.blast(ctx, []string{req.Service}, req.From, req.To)
			durs[2] = time.Since(s).Milliseconds()
			if err != nil {
				res.blstErr = err
				return
			}
			if br, ok := m[req.Service]; ok {
				res.blast = &br
			}
		}()
	} else {
		res.blstErr = fmt.Errorf("blast radius okuyucusu bağlı değil")
	}
	wg.Wait()
	st, f := depsStep(req, res)
	st.Evidence = depsEvidence(req, res, durs)
	return st, f, depsFactsOf(req, res)
}

// depsStep — SAF: üç okuma → adım + bulgular.
func depsStep(req depsRequest, res depsResult) (Step, []Finding) {
	st := Step{Name: stepDeps}
	var parts []string
	failed := 0
	p := depsPayload{Downstream: []depsEdge{}, Causes: []depsCause{}}
	if res.edgeErr != nil {
		failed++
		parts = append(parts, "topoloji okunamadı")
	} else {
		for _, e := range res.edges {
			if e.Source == req.Service && e.Target != req.Service && len(p.Downstream) < 10 {
				p.Downstream = append(p.Downstream, depsEdge{Target: e.Target, Calls: e.CallCount, ErrorRate: round2(e.ErrorRate), AvgMs: round2(e.AvgMs)})
			}
		}
		parts = append(parts, fmt.Sprintf("%d aşağı akış bağımlılığı", len(p.Downstream)))
	}
	var findings []Finding
	if res.chgErr != nil {
		failed++
		parts = append(parts, "korele değişiklikler okunamadı")
	} else {
		marked := chstore.MarkCorrelationCauses(res.changes, req.Service, res.edges, res.edgeErr == nil)
		var names []string
		for _, c := range marked {
			if !c.CauseEligible {
				continue
			}
			if len(findings) >= depsCausesMax {
				break
			}
			reasons := []string{relationTR(c.Relation) + " — korelasyon, nedensellik değil"}
			for i, r := range c.Reasons {
				if i >= 2 {
					break
				}
				reasons = append(reasons, r)
			}
			findings = append(findings, Finding{Kind: "dependency", Name: c.Service, Level: "warn", Reasons: reasons,
				EvidenceRefs: []string{"evidences[1]"}, Step: stepDeps})
			p.Causes = append(p.Causes, depsCause{Service: c.Service, Relation: c.Relation, Direction: c.Direction})
			names = append(names, c.Service)
		}
		if len(names) > 0 {
			parts = append(parts, fmt.Sprintf("olası neden %d (%s)", len(names), strings.Join(names, ", ")))
		} else {
			parts = append(parts, "olası neden olabilecek korele bağımlılık yok")
		}
	}
	if res.blstErr != nil {
		failed++
		parts = append(parts, "çağıranlar okunamadı")
	} else if res.blast != nil {
		p.Callers, p.Cascading = res.blast.TotalCallers, res.blast.CascadingCallers
		for i, c := range res.blast.Callers {
			if i >= 5 {
				break
			}
			p.Upstream = append(p.Upstream, depsCaller{Service: c.Service, RPS: round2(c.RPS), ErrorRate: round2(c.ErrorRate), HasOpenProblem: c.HasOpenProblem})
		}
		parts = append(parts, fmt.Sprintf("%d çağıran (%d'inde açık problem)", p.Callers, p.Cascading))
	} else {
		parts = append(parts, "çağıran yok")
	}
	if res.exact != nil && len(res.exact.Targets) > 0 {
		p.Exact = res.exact.Targets
		if len(p.Exact) > depsExactPayloadMax {
			p.Exact = p.Exact[:depsExactPayloadMax]
		}
		parts = append(parts, fmt.Sprintf("giden çağrı tam sayımı %d hedef (VM)", len(res.exact.Targets)))
	}
	if res.path != nil {
		p.Path = res.path
		if s := res.path.Source; s != nil {
			parts = append(parts, fmt.Sprintf("bağımlılık yolunda olası kaynak kenar %s → %s (hop %d)", s.Edge.From, s.Edge.To, s.Edge.Hop))
		}
	}
	switch failed {
	case 0:
		st.State = StepOK
	case 3:
		st.State = StepError
	default:
		st.State = StepPartial
	}
	st.Summary = truncRunes(strings.Join(parts, "; "), 500)
	// v0.10.1301 — üç okuma da OKUNDU ve üçü de boş: pencere rollup'ların
	// saklamasının dışında (ya da servis o an hiç trafik almadı). Okunamadı ≠ yok.
	if failed == 0 && len(res.edges) == 0 && len(res.changes) == 0 && (res.blast == nil || len(res.blast.Callers) == 0) {
		st.NoData = true
		st.Summary = truncRunes("veri yok: "+st.Summary, 500)
	}
	if raw, err := json.Marshal(p); err == nil && len(raw) <= maxStepPayload {
		st.Payload = raw
	}
	return st, findings
}

// depsEvidence — SAF: üç okumanın künyesi (sorgu özeti + pencere + satır).
func depsEvidence(req depsRequest, res depsResult, durs []int64) []mcptools.ChatEvidence {
	win := mcptools.EvidenceWindow{FromMs: req.From.UnixMilli(), ToMs: req.To.UnixMilli()}
	n := func(k int) *int { return &k }
	at := req.At.UTC().Format(time.RFC3339)
	out := []mcptools.ChatEvidence{
		{Tool: "get_topology", Source: "ClickHouse", QueryKind: "summary", Window: win, DurationMs: durs[0],
			Query: fmt.Sprintf("GetServiceGraphTopN FROM topology_edges_5m WHERE service = '%s' (≤%d kenar, çağrı sayısına göre)", req.Service, depsGraphTopN)},
		{Tool: "get_correlated_changes", Source: "ClickHouse", QueryKind: "summary", DurationMs: durs[1],
			Window: mcptools.EvidenceWindow{FromMs: req.At.UnixMilli(), ToMs: req.At.Add(time.Duration(req.WindowSec) * time.Second).UnixMilli()},
			Query:  fmt.Sprintf("GetCorrelatedChangesMVTop at=%s pencere %d s, taban 4× (service_summary_5m) + MarkCorrelationCauses (kenar + yön)", at, req.WindowSec)},
		{Tool: "get_blast_radius", Source: "ClickHouse", QueryKind: "summary", Window: win, DurationMs: durs[2],
			Query: fmt.Sprintf("GetBlastRadiusBatch FROM service_callers_5m WHERE service = '%s' (+ açık problem eşlemesi)", req.Service)},
	}
	if res.edgeErr == nil {
		out[0].Rows = n(len(res.edges))
	}
	if res.chgErr == nil {
		out[1].Rows = n(len(res.changes))
	}
	if res.blstErr == nil && res.blast != nil {
		out[2].Rows = n(len(res.blast.Callers))
	}
	if res.exact != nil || res.exactErr != nil {
		win := 0
		if res.exact != nil {
			win = res.exact.WindowSec
		}
		e := mcptools.ChatEvidence{Tool: "downstream_red", Source: "VictoriaMetrics", QueryKind: "summary",
			Window: mcptools.EvidenceWindow{FromMs: req.From.UnixMilli(), ToMs: req.To.UnixMilli()}, DurationMs: durs[3],
			Query: fmt.Sprintf("topk(%d) hedef × (istek/sn, hata %%, p95) FROM %s_{count,bucket}{service = '%s'} rate[%ds] — örnekleme öncesi tam sayım",
				runtimesig.DownstreamTopK, runtimesig.DownstreamMetric, req.Service, win)}
		if res.exact != nil {
			e.Rows = n(len(res.exact.Targets))
		}
		out = append(out, e)
	}
	if res.path != nil || res.pathErr != nil {
		e := mcptools.ChatEvidence{Tool: mcptools.DependencyPathName, Source: "ClickHouse", QueryKind: "summary",
			Window: mcptools.EvidenceWindow{FromMs: req.From.UnixMilli(), ToMs: req.To.UnixMilli()}, DurationMs: durs[4],
			Query: fmt.Sprintf("dependency_path service = '%s' direction=down depth=2 FROM topology_edges_5m FINAL (şimdiki vs dün aynı saatler, ≤60 kenar) + service_version_5m + tek sınırlı örnek trace", req.Service)}
		if res.path != nil {
			n := res.path.Walked
			e.Rows = &n
		}
		out = append(out, e)
	}
	return out
}

func round2(v float64) float64 {
	if v != v { // NaN
		return 0
	}
	return float64(int64(v*100+0.5)) / 100
}
