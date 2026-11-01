package ml

// snapshot.go — ML replay kaydı yazıcısı (v0.10.1752). Koşu başında ön
// kayıt (−2 sa seri + sınırlı ham örnek), koşu başından +2 sa sonra tam
// kayıt (±2 sa seri + rollout'lar) aynı satırı değiştirir. Seri ml_series'ten
// (CH), ES/rollup'a bağımlılık yok. Ham örnek: trace ID'ler CH spans'ten
// (LIMIT 5, zaman sınırlı); log satırı yalnız tik ES bütçesi kalmışsa.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	snapshotSpan   = 2 * time.Hour
	snapshotPerTik = 50
	sampleBodyCap  = 300
)

// eventPart — SAF: olayın ml_series parçası.
func eventPart(ev chstore.MLEvent) string {
	switch ev.Signal {
	case chstore.MLSignalTrace:
		switch ev.Detector {
		case detDependencyLatency:
			return depPartPrefix + ev.Code + "|p95"
		case detTraceChangePoint:
			return tracePart(ev.Code, "p95")
		case detTracePodOutlier:
			return "" // kesitsel karar: pod serisi tutulmaz
		case detOpLatency:
			return tracePart(ev.Code, "slow")
		case detOpErrorRate:
			return tracePart(ev.Code, "err_ratio")
		default:
			return tracePart(ev.Code, "count")
		}
	case chstore.MLSignalMetric:
		return ev.Code
	}
	if ev.Detector == detTemplateSpike || ev.Detector == detRareTemplate {
		return tplPartPrefix + ev.Code
	}
	if ev.Level == "error" {
		return partError
	}
	return partTotal
}

// triggeredBuckets — SAF: [from, to) içinde bandı (med ± z·effMAD) yön
// doğrultusunda aşan kova başları.
func triggeredBuckets(pts []chstore.MLPoint, med, mad, z float64, from, to int64, dir string) []int64 {
	eff := effMAD(med, mad)
	out := []int64{}
	for _, p := range pts {
		if p.T < from || p.T >= to {
			continue
		}
		d := (p.V - med) / eff
		if (dir != "down" && d >= z) || (dir == "down" && d <= -z) {
			out = append(out, p.T)
		}
	}
	return out
}

func evidenceMap(raw string) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &m)
	return m
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// buildSnapshot — olay kaydı. complete=false: ön kayıt (seri tespite kadar).
func buildSnapshot(ctx context.Context, deps Deps, ev chstore.MLEvent, cfg chstore.MLConfig, now time.Time, complete bool, samples string) chstore.MLSnapshot {
	det := time.Unix(0, ev.StartedAt)
	to := det.Add(snapshotSpan)
	if !complete || to.After(now) {
		to = now
	}
	part := eventPart(ev)
	pts := []chstore.MLPoint{}
	if all, err := deps.Store.MLSeriesRange(ctx, ev.Signal, ev.Service, det.Add(-snapshotSpan), to, cfg.BucketSec); err == nil && all[part] != nil {
		pts = all[part]
	}
	evm := evidenceMap(ev.Evidence)
	mad, _ := evm["mad"].(float64)
	class, _ := evm["dayClass"].(string)
	hash, _ := evm["configHash"].(string)
	var confirm []string
	if xs, ok := evm["confirmedBy"].([]any); ok {
		for _, x := range xs {
			if s, ok := x.(string); ok {
				confirm = append(confirm, s)
			}
		}
	}
	neg, pos := deps.Labels.Feedback(ev.Detector, ev.Service)
	q := cfg.Quality
	thr, _ := json.Marshal(map[string]any{"criticalZ": cfg.CriticalZ, "effectiveZ": feedbackZ(cfg.CriticalZ, neg, pos, q),
		"dwellBuckets": cfg.DwellBuckets, "baselineDays": cfg.BaselineDays, "minBaselineSamples": cfg.MinBaselineSamples,
		"minBaselineRate": cfg.MinBaselineRate, "impactMinRel": q.ImpactMinRel, "impactMinAbs": q.ImpactMinAbs, "regimeHours": q.RegimeHours})
	step := int64(cfg.BucketSec)
	dwell := triggeredBuckets(pts, ev.Baseline, mad, cfg.CriticalZ, det.Unix()-int64(cfg.DwellBuckets-1)*step, ev.LastSeen/1e9, ev.Direction)
	series, _ := json.Marshal(pts)
	dw, _ := json.Marshal(dwell)
	x := chstore.MLSnapshot{EventID: ev.ID, Detector: ev.Detector, Signal: ev.Signal, Service: ev.Service, Part: part,
		DetectedAt: ev.StartedAt, LastSeen: ev.LastSeen, BucketSec: uint32(cfg.BucketSec), Series: string(series),
		BaselineMed: ev.Baseline, BaselineMAD: mad, Thresholds: string(thr), ConfigHash: hash, DayClass: class,
		Dwell: string(dw), ConfirmIDs: confirm, Evidence: ev.Evidence, Samples: samples, Deploys: "[]", Complete: complete}
	if complete {
		if ds, err := deps.Store.GetDeploysInWindow(ctx, det.Add(-snapshotSpan), det.Add(snapshotSpan), 200); err == nil {
			var mine []chstore.RecentDeployEntry
			for _, d := range ds {
				if d.Service == ev.Service && len(mine) < 10 {
					mine = append(mine, d)
				}
			}
			if raw, err := json.Marshal(mine); err == nil && mine != nil {
				x.Deploys = string(raw)
			}
		}
	}
	return x
}

// collectSamples — sınırlı ham örnek: ≤5 trace ID (CH) + ES bütçesi kaldıysa ≤3 log satırı.
func collectSamples(ctx context.Context, deps Deps, ev chstore.MLEvent, cfg chstore.MLConfig, esLeft bool) string {
	det := time.Unix(0, ev.StartedAt)
	bucket := time.Duration(cfg.BucketSec) * time.Second
	from, to := det.Add(-time.Duration(cfg.DwellBuckets)*bucket), time.Unix(0, ev.LastSeen)
	errOnly := ev.Detector == detErrorRate || ev.Detector == detOpErrorRate
	route := ""
	if ev.Signal == chstore.MLSignalTrace {
		route = ev.Code
	}
	out := []chstore.MLSample{}
	sctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if xs, err := deps.Store.MLSampleTraces(sctx, ev.Service, route, from, to, errOnly); err == nil {
		out = append(out, xs...)
	}
	if esLeft && deps.Logs != nil {
		f := logstore.Filter{Service: ev.Service, From: from, To: to, Limit: 3, SkipTotal: true, LeanSource: true, SoftTimeout: 3 * time.Second}
		if errOnly || ev.Level == "error" {
			f.SeverityMin = 17
		}
		t0 := time.Now()
		page, err := deps.Logs.Search(sctx, f)
		if deps.ObserveES != nil {
			deps.ObserveES(time.Since(t0))
		}
		if err == nil && page != nil {
			for _, l := range page.Logs {
				out = append(out, chstore.MLSample{T: l.Timestamp, TraceID: l.TraceID, Body: truncRunes(l.Body, sampleBodyCap), Kind: "log"})
			}
		}
	}
	raw, _ := json.Marshal(out)
	return string(raw)
}

// snapshotState — koşu başına replay kaydı durumu (tracker içinde).
type snapshotState struct {
	pre, done bool
	samples   string
	// extra — T1b bağlam referansları (koşu başına bir kez; her tik kanıta eklenir).
	extra map[string]any
}

// enrichTraceContext — T1b: yeni koşunun bağlamını bir kez hesaplar
// (BubbleUp bütçesi bittiyse sonraki tikte yeniden dener), her tik olay
// kanıtına ekler. Örnekler burada toplanırsa replay ön kaydı aynısını kullanır.
func (r *Recorder) enrichTraceContext(ctx context.Context, deps Deps, cfg chstore.MLConfig, kept []chstore.MLEvent, esLeft int) {
	bubbleLeft := traceCtxBubblePerTick
	for i := range kept {
		ex, bub := needsTraceContext(kept[i].Detector)
		if !ex && !bub {
			continue
		}
		st := r.runs.snap(kept[i].ID)
		if st == nil {
			continue
		}
		if st.extra == nil {
			if st.samples == "" {
				st.samples = collectSamples(ctx, deps, kept[i], cfg, esLeft > 0)
				esLeft--
			}
			var samples []chstore.MLSample
			_ = json.Unmarshal([]byte(st.samples), &samples)
			x := traceContext(ctx, deps, kept[i], samples, &bubbleLeft)
			if _, ok := x["bubbleUp"]; ok || !bub {
				st.extra = x
			} else {
				kept[i].Evidence = mergeEvidence(kept[i].Evidence, x)
			}
		}
		if st.extra != nil {
			kept[i].Evidence = mergeEvidence(kept[i].Evidence, st.extra)
		}
		r.runs.setEvent(kept[i])
	}
}

// writeSnapshots — recorder tik sonu: yeni koşulara ön kayıt, +2 sa dolmuş
// koşulara tam kayıt. Tik başına en çok snapshotPerTik yazım.
func (r *Recorder) writeSnapshots(ctx context.Context, deps Deps, cfg chstore.MLConfig, kept []chstore.MLEvent, now time.Time, esLeft int) (n int, firstErr error) {
	for _, ev := range kept {
		if n >= snapshotPerTik {
			return n, firstErr
		}
		st := r.runs.snap(ev.ID)
		if st == nil || st.pre {
			continue
		}
		if st.samples == "" {
			st.samples = collectSamples(ctx, deps, ev, cfg, esLeft > 0)
			esLeft--
		}
		if err := deps.Store.UpsertMLSnapshot(ctx, buildSnapshot(ctx, deps, ev, cfg, now, false, st.samples)); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		st.pre = true
		n++
	}
	for _, ev := range r.runs.dueComplete(now, snapshotSpan) {
		if n >= snapshotPerTik {
			break
		}
		st := r.runs.snap(ev.ID)
		if err := deps.Store.UpsertMLSnapshot(ctx, buildSnapshot(ctx, deps, ev, cfg, now, true, st.samples)); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		st.done = true
		n++
	}
	return n, firstErr
}
