package ml

// breadth_record.go — v0.10.2237, anomali kartı sözleşmesi H12
// (docs/ml/anomaly-card-audit.md §9–§10): svc_breadth (svc_breadth.go)
// olay kaydını doldurur.
//
// Seri (§9 tablosu "seri tanımı gerek"): servisin "yavaş operasyon sayısı" —
// her tikte breadthOpsBySvc'nin kuralını (p95 ölçülü op_latency olayı, p95 ≥
// 1000 ms VE ≥ 2 × taban) sağlayan farklı operasyon sayısı. Türetilmiş: yeni
// sorgu yok, tik başına sayım süreç-içi geçmişte (breadthHist; lider
// değişince sıfır — onset görünmezse kayıt doğrulayıcıdan kalır, kart eski
// yola düşer). 1 dk ızgarada basamak (sayım bir sonraki tike dek geçerli).
// Bant sabit kural: alt 0, üst breadthMinOps − 1 (≥ 3 operasyon bant dışı).
// Üye op_latency olayları kanıtta BAĞIMSIZ DEĞİL (§9: türetilmiş dedektör
// kendi üyelerinden bağımsız sayılmaz); teyit listesi eklenmez (üyeler
// aynı servisin op_latency olaylarıdır). Hacim tanımsız (gerekçeli).
// Tespit mantığı, eşikler, started_at (koşulun ilk tiki) değişmez; gates[]
// boş, sınıf service_anomaly. Maliyet: ek sorgu yok.

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

const (
	breadthBandMethod   = "fixed_rule_min_ops"
	breadthVolumeReason = "türetilmiş sayım (operasyon sayısı); çağrı hacmi üye op_latency kayıtlarında"
	breadthHistKeep     = 6 * time.Hour
)

// breadthHist — süreç-içi tik geçmişi: tik anları + servis → tik → sayım
// (yalnız > 0; tikte yoksa 0).
type breadthHist struct {
	ticks  []int64
	counts map[string]map[int64]int
}

// observe — SAF olmayan (alıcıyı değiştirir): bu tikin sayımlarını ekler,
// pencere dışını budar.
func (h *breadthHist) observe(ops map[string][]BreadthOp, now time.Time) {
	t := now.Unix()
	if h.counts == nil {
		h.counts = map[string]map[int64]int{}
	}
	h.ticks = append(h.ticks, t)
	cut := t - int64(breadthHistKeep.Seconds())
	for len(h.ticks) > 0 && h.ticks[0] < cut {
		h.ticks = h.ticks[1:]
	}
	for svc, list := range ops {
		if h.counts[svc] == nil {
			h.counts[svc] = map[int64]int{}
		}
		h.counts[svc][t] = len(list)
	}
	for svc, m := range h.counts {
		for at := range m {
			if at < cut {
				delete(m, at)
			}
		}
		if len(m) == 0 {
			delete(h.counts, svc)
		}
	}
}

// grid — SAF: servisin sayım serisi 1 dk ızgarada (from'dan son tike).
// ok=false: geçmiş yok.
func (h *breadthHist) grid(svc string, from int64) (t0 int64, v []float64, ok bool) {
	var ticks []int64
	for _, t := range h.ticks {
		if t >= from {
			ticks = append(ticks, t)
		}
	}
	if len(ticks) == 0 {
		return 0, nil, false
	}
	sort.Slice(ticks, func(i, j int) bool { return ticks[i] < ticks[j] })
	t0 = ticks[0] / 60 * 60
	n := int((ticks[len(ticks)-1]/60*60-t0)/60) + 1
	if n > recordMaxPoints {
		t0 += int64(n-recordMaxPoints) * 60
		n = recordMaxPoints
	}
	v = make([]float64, n)
	k := 0
	for i := range v {
		at := t0 + int64(i)*60
		for k+1 < len(ticks) && ticks[k+1]/60*60 <= at {
			k++
		}
		v[i] = float64(h.counts[svc][ticks[k]])
	}
	return t0, v, true
}

// BreadthRecordInput — BuildBreadthRecord girdisi (saf).
type BreadthRecordInput struct {
	Service    string
	BucketSec  int
	T0         int64
	V          []float64
	Ops        []BreadthOp
	DetectedAt time.Time
	Closed     bool
}

// BuildBreadthRecord — SAF.
func BuildBreadthRecord(in BreadthRecordInput) (EventRecord, bool) {
	n := len(in.V)
	if n == 0 || in.BucketSec <= 0 {
		return EventRecord{}, false
	}
	lo, hi := make([]float64, n), make([]float64, n)
	for i := range hi {
		hi[i] = breadthMinOps - 1
	}
	det := min(max(int((in.DetectedAt.Unix()-in.T0)/recordSnapshotStepSec), 0), n-1)
	onset, end, peak := bandWindow(in.V, lo, hi, "up", det, in.Closed)
	at := func(i int) int64 { return (in.T0 + int64(i)*recordSnapshotStepSec) * 1e9 }
	rec := EventRecord{
		V:       RecordVersion,
		Subject: RecordSubject{EntityID: entity.ServiceID(in.Service), Service: in.Service, Resolved: true},
		Series: RecordSeries{Source: "ml:op_latency", Aggregation: "count", Unit: "ops", StepSec: uint32(in.BucketSec),
			Query: fmt.Sprintf("count(distinct operation) of open op_latency events WHERE service = %s AND measure = 'p95' AND p95 >= %g ms AND p95 >= %g × baseline",
				sqlLit(in.Service), breadthMinP95Ms, breadthMinRatio)},
		Baseline:  RecordBaseline{Expected: 0, Lo: 0, Hi: breadthMinOps - 1, Method: breadthBandMethod, StepSec: uint32(in.BucketSec)},
		Direction: "up", OnsetAt: at(onset), DetectedAt: at(det),
		PeakValue: peak, ValueAtDetection: in.V[det], ExpectedValue: 0,
		Class:        ClassServiceAnomaly,
		VolumeReason: breadthVolumeReason,
		Snapshot:     RecordSnapshot{T0: in.T0, StepSec: recordSnapshotStepSec, Aggregation: "count", V: in.V, Lo: lo, Hi: hi},
	}
	if in.Closed {
		rec.EndedAt = at(end)
	}
	rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "series", Source: "ml:op_latency",
		Summary: "Servisin yavaş operasyon sayısı, tik başına (türetilmiş)", Independent: false})
	for i, o := range in.Ops {
		if i >= breadthShowOps {
			break
		}
		rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "member", Source: "ml:op_latency",
			Summary: "Üye operasyon (op_latency p95): " + o.Op, Independent: false})
	}
	f := func(x float64) string { return trInt(x) + " operasyon" }
	rec.Title = fmt.Sprintf("Yavaş operasyon sayısı tespit anında %s, normal üst sınır %s", f(rec.ValueAtDetection), f(rec.Baseline.Hi))
	if in.Closed {
		rec.Title += " · tepe " + f(peak)
	}
	return rec, true
}

// attachBreadthRecords — recorder: svc_breadth olaylarına kayıt (yazım
// öncesi, applyRecordContract doğrular). Sorgu yok.
func attachBreadthRecords(h *breadthHist, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool, detectedAt func(epID string) time.Time, now time.Time) {
	if h == nil {
		return
	}
	for i := range evs {
		ev := evs[i]
		if ev.Detector != detSvcBreadth || ev.Stage == chstore.MLStageChronic {
			continue
		}
		det := detectedAt(ev.ID)
		if det.IsZero() || det.After(now) {
			det = now
		}
		from := time.Unix(0, ev.StartedAt).Add(-lagRecordPad).Unix()
		t0, v, ok := h.grid(ev.Service, from)
		if !ok {
			continue
		}
		ops, _ := breadthOpsOf(ev.Evidence)
		rec, ok := BuildBreadthRecord(BreadthRecordInput{Service: ev.Service, BucketSec: cfg.BucketSec, T0: t0, V: v, Ops: ops, DetectedAt: det, Closed: closed})
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
