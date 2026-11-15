package ml

// metric_thanos_record.go — v0.10.2224, anomali kartı sözleşmesi H8
// (docs/ml/anomaly-card-audit.md §9–§10): Thanos kaynaklı metrik aile
// olayları (kod "<aile>|<ifade>@<cluster>") olay kaydını doldurur.
//
// Seri: dedektörün tikte koştuğu pod düzeyi ifade (renderThanosExpr, {win}
// = kova) + tik anında DONDURULAN pod kümesi (namespace/pod eşleyicisi) ve
// aynı pod → servis birleşimi (exprAgg + combine; ör. max/avg/sum). Kanıta
// tik anında yazılır: clusterId, thanosExpr, agg, pods (≤ thanosRecordMaxPods;
// fazlası dondurulmaz → kayıt yok, seri servis değerine eşit olmazdı).
// Snapshot aynı ifadenin 1 dk adımlı Thanos aralık sorgusu (WorkerQuery,
// aynı fail-closed token / tavan / kısmilik kuralları), olay ±30 dk; dakika
// başına pod değerleri combine ile birleşir. Onset, uç, tespit değeri, bitiş
// YALNIZ bu seriden; bant alarm bandı (judgeMM). Hacim null + neden. Tespit
// mantığı, eşikler, started_at değişmez; gates[] boş, sınıf service_anomaly.
// Maliyet: açık olay başına tik başına 1 Thanos aralık sorgusu, tik başına
// en çok thanosRecordMaxPerTick (cluster tik bütçesinden ayrı, tik dışı).

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

const (
	thanosRecordMaxPerTick = 10
	thanosRecordMaxPods    = 50
)

// podNamesByService — SAF: servis başına "namespace/pod" adları (sıralı,
// tekil); podValuesByService ile aynı süzgeç ve eşleme.
func podNamesByService(rows []ThanosSample, ix podIndex, nsRe *regexp.Regexp) map[string][]string {
	out := map[string][]string{}
	for _, r := range rows {
		if nsRe != nil && !nsRe.MatchString(r.Labels["namespace"]) {
			continue
		}
		if svc, ok := ix.serviceOf(r.Labels["pod"]); ok {
			out[svc] = append(out[svc], r.Labels["namespace"]+"/"+r.Labels["pod"])
		}
	}
	for svc, ps := range out {
		sort.Strings(ps)
		out[svc] = slices.Compact(ps)
	}
	return out
}

// thanosRecordQuery — SAF: pod düzeyi ifade + dondurulmuş pod kümesi
// eşleyicileri. ok=false: pod yok.
func thanosRecordQuery(expr string, pods []string) (string, bool) {
	if expr == "" || len(pods) == 0 {
		return "", false
	}
	nsSet, podAlt := map[string]bool{}, make([]string, 0, len(pods))
	var nsAlt []string
	for _, p := range pods {
		ns, pod, ok := strings.Cut(p, "/")
		if !ok || pod == "" {
			return "", false
		}
		podAlt = append(podAlt, regexp.QuoteMeta(pod))
		if !nsSet[ns] {
			nsSet[ns] = true
			nsAlt = append(nsAlt, regexp.QuoteMeta(ns))
		}
	}
	q := thanos.WithRegexMatcher(expr, "pod", strings.Join(podAlt, "|"))
	return thanos.WithRegexMatcher(q, "namespace", strings.Join(nsAlt, "|")), true
}

// combineRange — SAF: pod serileri → zaman başına combine(agg) (servis değeri).
func combineRange(rows []ThanosRangeSeries, agg string) []chstore.SpanMetricPoint {
	byT := map[int64][]float64{}
	for _, r := range rows {
		for _, p := range r.Points {
			byT[p.Time] = append(byT[p.Time], p.Value)
		}
	}
	out := make([]chstore.SpanMetricPoint, 0, len(byT))
	for t, vs := range byT {
		out = append(out, chstore.SpanMetricPoint{Time: t, Value: combine(agg, vs)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

// thanosRanger — recorder: Thanos dedektörünün okuyucusu aralık
// okuyabiliyorsa onu döndürür (yeni bağlantı yok); yoksa nil.
func (r *Recorder) thanosRanger() ThanosRanger {
	for _, d := range r.detectors {
		if mt, ok := d.(*MetricThanos); ok && mt.q != nil {
			if rg, ok := mt.q.(ThanosRanger); ok {
				return rg
			}
		}
	}
	return nil
}

// attachThanosMetricRecords — recorder: Thanos metrik olaylarına kayıt
// (yazım öncesi, applyRecordContract doğrular). attachMetricRecords ile aynı akış.
func attachThanosMetricRecords(ctx context.Context, q ThanosRanger, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
	detectedAt func(epID string) time.Time, confirmers func(ev chstore.MLEvent) []string, budget *int, now time.Time) {
	if q == nil {
		return
	}
	for i := range evs {
		ev := evs[i]
		if ev.Stage == chstore.MLStageChronic || *budget <= 0 {
			continue
		}
		fam, id, cluster := splitThanosPart(ev.Code)
		if cluster == "" || fam != ev.Detector {
			continue
		}
		m := evidenceMap(ev.Evidence)
		cid, _ := m["clusterId"].(string)
		expr, _ := m["thanosExpr"].(string)
		agg, _ := m["agg"].(string)
		var pods []string
		if raw, ok := m["pods"].([]any); ok {
			for _, p := range raw {
				if s, ok := p.(string); ok {
					pods = append(pods, s)
				}
			}
		}
		query, ok := thanosRecordQuery(expr, pods)
		if cid == "" || agg == "" || !ok {
			continue // tik kanıtı yok (eski olay) ya da pod kümesi dondurulmadı
		}
		var e chstore.MLMetricExpr
		for _, x := range cfg.MetricFamilies[fam] {
			if x.ID == id {
				e = x
			}
		}
		if e.ID == "" {
			continue
		}
		det := detectedAt(ev.ID)
		if det.IsZero() || det.After(now) {
			det = now
		}
		bucket := time.Duration(cfg.BucketSec) * time.Second
		from := det.Add(-time.Duration(cfg.DwellBuckets)*bucket - lagRecordPad)
		to := now
		if closed {
			to = time.Unix(0, ev.LastSeen).Add(lagRecordPad)
			if to.After(now) {
				to = now
			}
		}
		*budget--
		qctx, cancel := context.WithTimeout(ctx, sourceCheckTimeout)
		rows, err := q.Range(qctx, cid, query, from, to, time.Minute)
		cancel()
		if err != nil || len(rows) == 0 {
			continue // önceki kayıt (varsa) aynen kalır
		}
		mad, _ := m["mad"].(float64)
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildMetricRecord(MetricRecordInput{Service: ev.Service, Family: ev.Detector, Expr: e, Query: query,
			Source: "thanos:" + cluster, Agg: agg, Pods: len(pods),
			Direction: ev.Direction, BucketSec: cfg.BucketSec, CriticalZ: cfg.CriticalZ, Median: ev.Baseline, MAD: mad,
			DetectedAt: det, Closed: closed, Points: combineRange(rows, agg), From: from, To: to, Confirmers: conf})
		if !ok {
			continue
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		ev.Record, ev.Class, ev.OnsetAt, ev.PublishState = string(raw), rec.Class, rec.OnsetAt, PublishPublished
		evs[i] = ev
	}
}
