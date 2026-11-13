package ml

// explain_input.go — v0.10.1843 (audit §7.3, dilim 2.3): ExplainCard
// girdisinin SAF adaptörleri. Olay satırı (ml_events) + olay anı kaydı
// (ml_event_snapshots) + servis profili + yakın kayıtlar + yayınlar →
// CardInput. I/O çağıranda (internal/api/ml.go); burada yalnız çözümleme,
// böylece liste özeti ile detay kartı AYNI alanlardan kurulur.
//
// Eski kayıt (bant/seri/eşik yok) olduğu gibi geçer: üretici dürüst geri
// düşüşü kendisi seçer (explain_card.go).

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func evNum(m map[string]any, k string) (float64, bool) {
	v, ok := m[k].(float64)
	return v, ok && !math.IsNaN(v) && !math.IsInf(v, 0)
}

func evStr(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// influencerValues — SAF: log kanal kodu terfileri (MLLogInfluencer, etiketsiz
// JSON → "Value"; eski FE "value" okuyordu, ikisi de kabul).
func influencerValues(m map[string]any) []string {
	xs, _ := m["influencers"].([]any)
	var out []string
	seen := map[string]bool{}
	for _, x := range xs {
		o, _ := x.(map[string]any)
		v := evStr(o, "Value")
		if v == "" {
			v = evStr(o, "value")
		}
		if v != "" && !seen[v] && len(out) < 2 {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// CardInputFromEvent — SAF: canlı olay satırı → kart girdisi (liste özeti
// yalnız bununla kurulur).
func CardInputFromEvent(ev chstore.MLEvent, now time.Time, loc *time.Location) CardInput {
	m := evidenceMap(ev.Evidence)
	in := CardInput{EventID: ev.ID, Detector: ev.Detector, Signal: ev.Signal, Service: ev.Service, Part: eventPart(ev),
		Code: ev.Code, Pod: ev.Pod, Stage: ev.Stage, Direction: ev.Direction,
		StartedAt: time.Unix(0, ev.StartedAt), LastSeen: time.Unix(0, ev.LastSeen), BucketSec: int(ev.WindowSec),
		Observed: ev.Observed, Baseline: ev.Baseline, Score: ev.Score, Now: now, Loc: loc,
		DayClass: evStr(m, "dayClass"), ConfigHash: evStr(m, "configHash"), BaselineKind: evStr(m, "baselineKind")}
	if in.Signal == "" {
		in.Signal = chstore.MLDetectorSignal(ev.Detector)
	}
	if v, ok := evNum(m, "slowSec"); ok {
		in.SlowSec = v
	}
	if v, ok := evNum(m, "dwell"); ok {
		in.DwellBuckets = int(v)
	}
	if v, ok := evNum(m, "baselineDays"); ok {
		in.BaselineDays = int(v)
	}
	var im CardImpact
	if v, ok := evNum(m, "affected"); ok && v > 0 {
		im.Affected = v
	}
	if in.Signal == chstore.MLSignalLog {
		im.Channels = influencerValues(m)
	}
	if strings.HasSuffix(ev.Detector, "pod_outlier") {
		if v, ok := evNum(m, "pods"); ok && v > 0 {
			im.PodsTotal, im.PodsHit = int(v), 1
		}
	}
	if im.Affected > 0 || len(im.Channels) > 0 || im.PodsTotal > 0 {
		in.Impact = &im
	}
	in.Contrib = evidenceContrib(ev.Evidence)
	if ls := evStr(m, "logService"); ls != "" && ls != ev.Service {
		in.LogService = ls // v0.10.2045
	}
	in.LogQuery = evStr(m, "logQuery") // v0.10.2069: new_template (v0.10.2026 türeticisi)
	if ev.Detector == detSvcBreadth {  // v0.10.2086
		in.BreadthOps, in.BreadthTotal = breadthOpsOf(ev.Evidence)
	}
	if lt, ok := m["lowTraffic"].(map[string]any); ok {
		n, _ := evNum(lt, "n")
		k, _ := evNum(lt, "k")
		lim, _ := evNum(lt, "limitMs")
		if n > 0 {
			in.LowTraffic = &CardLowTraffic{N: int(n), K: int(k), LimitMs: lim}
		}
	}
	return in
}

// ParseSnapshotBand — SAF: thresholds.band ([t, q25, q50, q75][]) → bant.
func ParseSnapshotBand(thresholds string) []CardBand {
	var thr struct {
		Band [][]float64 `json:"band"`
	}
	if json.Unmarshal([]byte(thresholds), &thr) != nil {
		return nil
	}
	out := make([]CardBand, 0, len(thr.Band))
	for _, r := range thr.Band {
		if len(r) == 4 {
			out = append(out, CardBand{T: int64(r[0]), Lo: r[1], Mid: r[2], Hi: r[3]})
		}
	}
	return out
}

// ApplySnapshot — SAF: olay anı kaydının seri / bant / eşiklerini girdiye
// ekler (canlı olay alanlarını EZMEZ; yalnız boşları doldurur).
func ApplySnapshot(in *CardInput, snap chstore.MLSnapshot) {
	var pts []chstore.MLPoint
	if json.Unmarshal([]byte(snap.Series), &pts) == nil {
		sort.Slice(pts, func(i, j int) bool { return pts[i].T < pts[j].T })
		in.Series = pts
	}
	in.Band = ParseSnapshotBand(snap.Thresholds)
	var dwell []int64
	if json.Unmarshal([]byte(snap.Dwell), &dwell) == nil {
		sort.Slice(dwell, func(i, j int) bool { return dwell[i] < dwell[j] })
		in.Dwell = dwell
	}
	thr := evidenceMap(snap.Thresholds)
	if v, ok := evNum(thr, "criticalZ"); ok {
		in.CriticalZ = v
	}
	if v, ok := evNum(thr, "effectiveZ"); ok {
		in.EffectiveZ = v
	}
	if v, ok := evNum(thr, "dwellBuckets"); ok {
		in.DwellBuckets = int(v)
	}
	if v, ok := evNum(thr, "baselineDays"); ok {
		in.BaselineDays = int(v)
	}
	if v, ok := evNum(thr, "impactMinRel"); ok {
		in.ImpactMinRel = v
	}
	if in.Part == "" {
		in.Part = snap.Part
	}
	if in.DayClass == "" {
		in.DayClass = snap.DayClass
	}
	if in.ConfigHash == "" {
		in.ConfigHash = snap.ConfigHash
	}
	if in.BucketSec <= 0 {
		in.BucketSec = int(snap.BucketSec)
	}
}

// CardInputFromSnapshot — SAF: olay satırı TTL'le düşmüşse yalnız kayıttan.
// Gözlenen = son tetik kovası (yoksa kanıttaki değer, v0.10.1775 serisiz olay).
func CardInputFromSnapshot(snap chstore.MLSnapshot, now time.Time, loc *time.Location) CardInput {
	m := evidenceMap(snap.Evidence)
	ev := chstore.MLEvent{ID: snap.EventID, Detector: snap.Detector, Signal: snap.Signal, Service: snap.Service,
		StartedAt: snap.DetectedAt, LastSeen: snap.LastSeen, WindowSec: snap.BucketSec, Baseline: snap.BaselineMed,
		Code: evStr(m, "code"), Pod: evStr(m, "pod"), Direction: evStr(m, "direction"), Evidence: snap.Evidence, Stage: chstore.MLStageShadow}
	if snap.Complete {
		ev.Stage = chstore.MLStageClosed
	}
	if v, ok := evNum(m, "observed"); ok {
		ev.Observed = v
	}
	if v, ok := evNum(m, "baseline"); ok && snap.Series == "[]" {
		ev.Baseline = v
	}
	var pts []chstore.MLPoint
	_ = json.Unmarshal([]byte(snap.Series), &pts)
	var dwell []int64
	_ = json.Unmarshal([]byte(snap.Dwell), &dwell)
	hit := map[int64]bool{}
	for _, t := range dwell {
		hit[t] = true
	}
	for _, p := range pts {
		if hit[p.T] {
			ev.Observed = p.V // son tetik kovası (pts zaman sıralı yazılır)
		}
	}
	if ev.Direction == "" {
		ev.Direction = "up"
		if ev.Observed < ev.Baseline {
			ev.Direction = "down"
		}
	}
	in := CardInputFromEvent(ev, now, loc)
	in.Part = snap.Part
	ApplySnapshot(&in, snap)
	return in
}

// RolloutsFromDeploys — SAF: servis yayınları (zaman artan) → önceki sürümle.
func RolloutsFromDeploys(ds []chstore.Deploy) []CardRollout {
	sorted := append([]chstore.Deploy(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].TimeUnixNs < sorted[j].TimeUnixNs })
	out := make([]CardRollout, 0, len(sorted))
	for i, d := range sorted {
		r := CardRollout{At: time.Unix(0, d.TimeUnixNs), Version: d.Version}
		if i > 0 {
			r.PrevVersion = sorted[i-1].Version
		}
		out = append(out, r)
	}
	return out
}

var signalAnomalyTR = map[string]string{chstore.MLSignalLog: "Log anomalisi", chstore.MLSignalTrace: "Trace anomalisi", chstore.MLSignalMetric: "Metrik anomalisi"}

// sumSeries — SAF: kaydın [başlangıç, son) toplamı; seri yoksa 0.
func sumSeries(snap chstore.MLSnapshot) float64 {
	var pts []chstore.MLPoint
	if json.Unmarshal([]byte(snap.Series), &pts) != nil {
		return 0
	}
	from, to := snap.DetectedAt/1e9, snap.LastSeen/1e9
	s := 0.0
	for _, p := range pts {
		if p.T >= from && p.T < to {
			s += p.V
		}
	}
	return s
}

// EnrichFromRelated — SAF: aynı servisin yakın kayıtları (±2 sa okuması) →
// "yeni mesaj" (nadir şablon) + "aynı serviste" maddeleri. Pencere: olaydan
// 30 dk önce … bitiş. Href: ML olay detayı.
func EnrichFromRelated(in *CardInput, related []chstore.MLSnapshot, hrefOf func(id string) string) {
	open := cardOpen(*in)
	end := cardEnd(*in, open)
	from := in.StartedAt.Add(-30 * time.Minute)
	rel := append([]chstore.MLSnapshot(nil), related...)
	sort.Slice(rel, func(i, j int) bool { return rel[i].DetectedAt < rel[j].DetectedAt })
	for _, x := range rel {
		if x.EventID == in.EventID {
			continue
		}
		at := time.Unix(0, x.DetectedAt)
		if at.Before(from) || at.After(end) {
			continue
		}
		m := evidenceMap(x.Evidence)
		if (x.Detector == "rare_template" || x.Detector == detNewTemplate) && in.NewTemplate == nil {
			txt := evStr(m, "phrase")
			if txt == "" {
				txt = evStr(m, "code")
			}
			// v0.10.2026 — arama metni KESİLMEMİŞ ifadeden: kesik metin ("…")
			// tırnaklanınca hiçbir satırla eşleşmiyordu ve /logs boş açılıyordu.
			query := quoteQ(txt)
			if q := evStr(m, "logQuery"); q != "" { // v0.10.2069: new_template kalıbın kendi araması
				query = q
			}
			if r := []rune(txt); len(r) > 120 {
				txt = string(r[:120]) + "…"
			}
			if txt != "" {
				cnt := sumSeries(x)
				if cnt == 0 {
					cnt, _ = evNum(m, "observed")
				}
				in.NewTemplate = &CardTemplate{Text: txt, Query: query, FirstSeen: at, Count: cnt, Href: hrefOf(x.EventID)}
				continue
			}
		}
		sub := CardInput{Detector: x.Detector, Signal: x.Signal, Code: evStr(m, "code"), Pod: evStr(m, "pod"), Direction: evStr(m, "direction")}
		sp := specFor(sub)
		in.Related = append(in.Related, CardRelated{Relation: "same_service", Service: x.Service, Fact: fill(sp.past, sub), At: at,
			Href: hrefOf(x.EventID), Label: signalAnomalyTR[x.Signal]})
	}
}

// MetricLabelFor — SAF: metrik olayının etiketi + birimi (Settings
// MetricFamilies; part = "<aile>|<ifade id>"). Bulunamazsa ifade id'si.
func MetricLabelFor(cfg chstore.MLConfig, detector, part string) (label, unit string) {
	_, id, _ := splitThanosPart(part) // v0.10.2019: "@<cluster>" eki etiketi değiştirmez
	for _, e := range cfg.MetricFamilies[detector] {
		if e.ID == id {
			return e.Label, e.Unit
		}
	}
	return id, ""
}

// MetricWindowedFor — SAF (v0.10.2044): aile ifadesi pencereli mi (`{win}`:
// rate / increase → değer kova ortalaması; değilse kova sonundaki ölçüm).
func MetricWindowedFor(cfg chstore.MLConfig, detector, part string) bool {
	_, id, _ := splitThanosPart(part)
	for _, e := range cfg.MetricFamilies[detector] {
		if e.ID == id {
			return strings.Contains(e.Expr, "{win}")
		}
	}
	return false
}

// OperatorLocation — dedektörlerin kullandığı operatör dilimi (takvim
// ayarı; yoksa UTC) — kart saatleri tespitle aynı dilimde yazılır.
func OperatorLocation(sens chstore.AnomalySensitivityConfig) *time.Location {
	_, loc := calendarFor(sens)
	return loc
}

// CardSources — detay kartının ham kaynakları (okumalar çağıranda).
type CardSources struct {
	Event          *chstore.MLEvent
	Snapshot       *chstore.MLSnapshot
	Related        []chstore.MLSnapshot
	Deploys        []chstore.Deploy
	DeploysOK      bool
	DeployLookback time.Duration
	Profile        string
	Config         chstore.MLConfig
	Now            time.Time
	Loc            *time.Location
	HrefOf         func(id string) string
}

// SummaryInput — SAF: liste satırı özeti için girdi (yalnız olay + metrik etiketi).
func SummaryInput(ev chstore.MLEvent, cfg chstore.MLConfig, now time.Time, loc *time.Location) CardInput {
	in := CardInputFromEvent(ev, now, loc)
	if in.Signal == chstore.MLSignalMetric {
		in.MetricLabel, in.MetricUnit = MetricLabelFor(cfg, ev.Detector, in.Part)
		in.MetricWindowed = MetricWindowedFor(cfg, ev.Detector, in.Part)
	}
	return in
}

// AssembleCardInput — SAF: kaynaklar → kart girdisi. Canlı olay satırı
// önce (süre, durum güncel); yoksa olay anı kaydı. ok=false: ikisi de yok.
func AssembleCardInput(src CardSources) (CardInput, bool) {
	var in CardInput
	switch {
	case src.Event != nil:
		in = SummaryInput(*src.Event, src.Config, src.Now, src.Loc)
		if src.Snapshot != nil {
			ApplySnapshot(&in, *src.Snapshot)
		}
	case src.Snapshot != nil:
		in = CardInputFromSnapshot(*src.Snapshot, src.Now, src.Loc)
		if in.Signal == chstore.MLSignalMetric {
			in.MetricLabel, in.MetricUnit = MetricLabelFor(src.Config, in.Detector, in.Part)
			in.MetricWindowed = MetricWindowedFor(src.Config, in.Detector, in.Part)
		}
	default:
		return CardInput{}, false
	}
	in.Profile = src.Profile
	if src.DeploysOK {
		in.Rollouts, in.RolloutsKnown, in.RolloutLookback = RolloutsFromDeploys(src.Deploys), true, src.DeployLookback
	}
	if src.HrefOf == nil {
		src.HrefOf = func(string) string { return "" }
	}
	EnrichFromRelated(&in, src.Related, src.HrefOf)
	return in, true
}
