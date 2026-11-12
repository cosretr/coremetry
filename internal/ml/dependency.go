package ml

// dependency.go — ML trace T2 (v0.10.1758): bağımlılık gecikmesi. Operatör
// kararı (2026-10-31): kaynak VictoriaMetrics OTel CLIENT histogramları —
// db_summary_1m / topology_edges_5m örneklenmiş span'lerden türediği için
// (Collector tail sampling) KULLANILMAZ. Aileler: HTTP client, DB client,
// messaging client; servis × hedef (server.address / db.namespace /
// messaging.destination) etiketleri saatte bir keşfedilir. Aile başına 2
// gruplu sorgu (count, p95), adım = kova. Seri ml_series signal=trace part
// `dep|<aile>|<hedef>|count|p95`; taban CH takvim sınıfı × ±1 saat, aynı
// judge (yukarı). Trafik tabanı (minBaselineRate) altındaki hedef sessiz.
// Ailenin metriği VM'de yoksa o aile atlanır; hiçbiri yoksa sessiz + lastError.
//
// v0.10.2063 (Davis S2) — dependency_error_rate: DB ve messaging client
// hata ORANI = _count{error_type!=""} / _count, aynı çağıran × hedef
// anahtarında (count sorgusunun topk K'sı; part `dep|<aile>|<hedef>|err_ratio`).
// db_summary_5m / messaging_summary_5m KULLANILMAZ: başarılı span'ler
// Collector'da %5 örneklenir, hatalılar %100 → o tablolardan oran ~20 kat
// şişik. error_type etiketi ailede yoksa (saatlik etiket keşfi, ek sorgu yok)
// dedektör o ailede SUSAR ve tik durumunda "ölçülemedi" yazar — sağlıklı
// da anomali de denmez. Sorgu: hata oranı açıkken aile başına +1 (db +
// messaging = tik başına +2; gecikme kapalıysa count sorgusu da bu dedektöre
// yazılır: +4).

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	detDependencyLatency   = "dependency_latency"
	detDependencyErrorRate = "dependency_error_rate"
	depPartPrefix          = "dep|"
	depSeriesCap           = 5000
	// depErrorLabel — OTel semconv error.type (Prometheus adı).
	depErrorLabel = "error_type"
	// depErrMinObserved / depErrMinCount — hata oranı olayının mutlak kapıları:
	// oran ≥ %1 ve kovada ≥ 5 hatalı çağrı (az çağrıda tek hata olay değil).
	depErrMinObserved = 0.01
	depErrMinCount    = 5
)

// depFamily — bir client histogram ailesi (SAF tanım).
type depFamily struct {
	ID      string
	Metric  string // VM taban adı (_count / _bucket eklenir)
	Targets []string
	Scale   float64 // ms'ye çarpan
	// ErrorRate — v0.10.2063: dependency_error_rate bu ailede değerlendirilir.
	ErrorRate bool
}

var depFamilies = []depFamily{
	{ID: "http", Metric: "http_client_request_duration_seconds", Targets: []string{"server_address", "net_peer_name", "peer_service", "http_host"}, Scale: 1000},
	{ID: "db", Metric: "db_client_operation_duration_seconds", Targets: []string{"db_namespace", "db_name", "server_address", "db_system"}, Scale: 1000, ErrorRate: true},
	{ID: "messaging", Metric: "messaging_client_operation_duration_seconds", Targets: []string{"messaging_destination_name", "server_address"}, Scale: 1000, ErrorRate: true},
}

// depPart — SAF.
func depPart(fam, target, measure string) string {
	return depPartPrefix + fam + "|" + target + "|" + measure
}

// depQueries — SAF: (count, p95) PromQL.
func depQueries(f depFamily, svc, target string, winSec int) (count, p95 string) {
	by := svc + ", " + target
	w := fmt.Sprintf("%ds", winSec)
	count = fmt.Sprintf("sum by (%s) (rate(%s_count[%s]))", by, f.Metric, w)
	p95 = fmt.Sprintf("histogram_quantile(0.95, sum by (%s, le) (rate(%s_bucket[%s])))", by, f.Metric, w)
	return
}

// depErrQuery — SAF (v0.10.2063): hatalı çağrı hızı (error_type dolu).
func depErrQuery(f depFamily, svc, target string, winSec int) string {
	return fmt.Sprintf("sum by (%s, %s) (rate(%s_count{%s!=\"\"}[%ds]))", svc, target, f.Metric, depErrorLabel, winSec)
}

// depErrRatios — SAF (v0.10.2063): seçili (count) çiftler için hata oranı.
// count: anahtar → istek/sn, errs: anahtar → hatalı istek/sn (hatasız çift
// sonuçta yok = 0; etiket ailede VAR olduğu için). count ≤ 0 → oran yok.
func depErrRatios(count, errs map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(count))
	for k, c := range count {
		if c <= 0 {
			continue
		}
		out[k] = math.Min(1, errs[k]/c)
	}
	return out
}

// depErrFire — SAF (v0.10.2063): hata oranı olayı kapısı. j: oran serisinin
// kararı (yukarı); errPerSec: son kovada hatalı istek/sn.
func depErrFire(j Judgement, errPerSec float64, bucketSec int) bool {
	return j.Fire && j.Direction == "up" && j.Observed >= depErrMinObserved && errPerSec*float64(bucketSec) >= depErrMinCount
}

type depLabels struct {
	svc, target string
	hasErr      bool // v0.10.2063: error_type etiketi ailede var
	at          time.Time
	ok          bool
}

// Dependency — dedektör.
type Dependency struct {
	mu     sync.Mutex
	labels map[string]depLabels
	// unmeasured — v0.10.2063: son tikte ölçülemeyen dedektör → gerekçe.
	unmeasured map[string]string
	capper
}

func NewDependency() *Dependency {
	return &Dependency{labels: map[string]depLabels{}, capper: capper{daily: map[string]int{}}}
}

func (d *Dependency) Name() string { return "dependency" }

func (d *Dependency) Enabled(cfg chstore.MLConfig) bool {
	return cfg.DetectorOn(detDependencyLatency) || cfg.DetectorOn(detDependencyErrorRate)
}

// Unmeasured — v0.10.2063 (unmeasuredReporter): son tikte kaynağı olmadığı
// için ölçülemeyen dedektörler (tik durumunda "ölçülemedi").
func (d *Dependency) Unmeasured() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.unmeasured) == 0 {
		return nil
	}
	out := make(map[string]string, len(d.unmeasured))
	for k, v := range d.unmeasured {
		out[k] = v
	}
	return out
}

func (d *Dependency) famLabels(ctx context.Context, deps Deps, f depFamily, from, to time.Time) depLabels {
	d.mu.Lock()
	l, ok := d.labels[f.ID]
	d.mu.Unlock()
	if ok && time.Since(l.at) < time.Hour {
		return l
	}
	l = depLabels{at: time.Now()}
	if names, _, err := deps.VM.MetricLabelNamesIn(ctx, f.Metric+"_count", from, to); err == nil {
		lm := deps.VM.MetricLabelMap()
		l.svc = pickLabel(names, append([]string{lm.Service}, svcLabelCandidates...))
		l.target = pickLabel(names, f.Targets)
		l.ok = l.svc != "" && l.target != ""
		l.hasErr = pickLabel(names, []string{depErrorLabel}) != ""
	}
	d.mu.Lock()
	d.labels[f.ID] = l
	d.mu.Unlock()
	return l
}

// depUnmeasuredNote — SAF (v0.10.2063): hata oranının ölçülemediği aileler
// → durum notu ("" = hepsi ölçüldü). measured: en az bir aile ölçüldü mü.
func depUnmeasuredNote(missing []string, measured bool) string {
	if len(missing) == 0 {
		return ""
	}
	why := strings.Join(missing, ", ") + " client histogramında " + depErrorLabel + " etiketi (ya da metrik) yok"
	if measured {
		return "kısmen ölçülüyor: " + why
	}
	return "ölçülemedi: " + why
}

func (d *Dependency) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	wantLat, wantErr := cfg.DetectorOn(detDependencyLatency), cfg.DetectorOn(detDependencyErrorRate)
	if deps.VM == nil || !deps.VM.Configured() {
		d.setUnmeasured(wantErr, "ölçülemedi: VictoriaMetrics yapılandırılmamış")
		return nil, fmt.Errorf("VictoriaMetrics yapılandırılmamış: bağımlılık ML sessiz (tail sampling nedeniyle rollup kullanılmaz)")
	}
	bucket := time.Duration(cfg.BucketSec) * time.Second
	last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec)
	from, to := last.Add(-time.Duration(cfg.DwellBuckets+1)*bucket), last.Add(bucket)
	series := map[string]map[int64]float64{}
	put := func(svc, part string, t int64, v float64) {
		k := chstore.MLSeriesKey(svc, part)
		if series[k] == nil {
			if len(series) >= depSeriesCap*3 {
				return
			}
			series[k] = map[int64]float64{}
		}
		series[k][t] = v
	}
	fams := 0
	win := fmt.Sprintf("%ds", cfg.BucketSec)
	var waiting, errMissing []string
	errMeasured := false
	errNow := map[string]float64{} // err_ratio anahtarı → hatalı istek/sn (son kova)
	for _, f := range depFamilies {
		famErr := wantErr && f.ErrorRate
		if !wantLat && !famErr {
			continue
		}
		l := d.famLabels(ctx, deps, f, from, to)
		if !l.ok {
			if famErr {
				errMissing = append(errMissing, f.ID)
			}
			continue
		}
		fams++
		// A: servis başına en çok K hedef — count serisi ucuz (le yok)
		cq := fmt.Sprintf("topk by (%s) (%d, sum by (%s, %s) (rate(%s_count[%s])))", l.svc, cfg.VMTargetCardinality, l.svc, l.target, f.Metric, win)
		rows, skip, err := vmInstant(ctx, deps, "dep:"+f.ID, cq, to, now)
		if skip {
			waiting = append(waiting, f.ID)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("vm dep %s/count: %w", f.ID, err)
		}
		bySvc := map[string][]string{}
		count := map[string]float64{} // svc\x00tgt → istek/sn
		for _, r := range rows {
			lb := groupKeyLabels(r.GroupKey)
			svc, tgt := lb[l.svc], lb[l.target]
			if v, ok := instantValue(r); ok && svc != "" && tgt != "" {
				put(svc, depPart(f.ID, tgt, "count"), last.Unix(), v)
				bySvc[svc] = append(bySvc[svc], tgt)
				count[svc+"\x00"+tgt] = v
			}
		}
		// B: p95 servis grupları halinde, yalnız seçili hedefler (ham bucket taraması sınırlı)
		if wantLat {
			for _, bt := range svcBatches(bySvc, cfg.VMServiceBatch) {
				pq := fmt.Sprintf("histogram_quantile(0.95, sum by (%s, %s, le) (rate(%s_bucket{%s=~%q, %s=~%q}[%s])))",
					l.svc, l.target, f.Metric, l.svc, bt.svcRe, l.target, bt.tgtRe, win)
				rows, skip, err := vmInstant(ctx, deps, "dep:"+f.ID, pq, to, now)
				if skip {
					waiting = append(waiting, f.ID)
					break
				}
				if err != nil {
					return nil, fmt.Errorf("vm dep %s/p95: %w", f.ID, err)
				}
				for _, r := range rows {
					lb := groupKeyLabels(r.GroupKey)
					svc, tgt := lb[l.svc], lb[l.target]
					if v, ok := instantValue(r); ok && bt.allowed[svc][tgt] {
						put(svc, depPart(f.ID, tgt, "p95"), last.Unix(), v*f.Scale)
					}
				}
			}
		}
		// C (v0.10.2063): hata oranı — aile başına TEK sorgu, sonuç count'un
		// seçtiği çiftlere süzülür (kardinalite = topk K × servis).
		if famErr {
			if !l.hasErr {
				errMissing = append(errMissing, f.ID)
				continue
			}
			rows, skip, err := vmInstant(ctx, deps, "dep:"+f.ID, depErrQuery(f, l.svc, l.target, cfg.BucketSec), to, now)
			if skip {
				waiting = append(waiting, f.ID)
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("vm dep %s/err: %w", f.ID, err)
			}
			errMeasured = true
			errs := map[string]float64{}
			for _, r := range rows {
				lb := groupKeyLabels(r.GroupKey)
				if v, ok := instantValue(r); ok {
					errs[lb[l.svc]+"\x00"+lb[l.target]] = v
				}
			}
			for k, ratio := range depErrRatios(count, errs) {
				svc, tgt, _ := strings.Cut(k, "\x00")
				put(svc, depPart(f.ID, tgt, "err_ratio"), last.Unix(), ratio)
				errNow[chstore.MLSeriesKey(svc, depPart(f.ID, tgt, "err_ratio"))] = errs[k]
			}
		}
	}
	d.setUnmeasured(wantErr, depUnmeasuredNote(errMissing, errMeasured))
	if fams == 0 {
		return nil, fmt.Errorf("VM'de client histogram ailesi/etiketi yok (http/db/messaging client duration)")
	}
	if len(waiting) > 0 && len(series) == 0 {
		return nil, fmt.Errorf("VM aileleri beklemede (backoff): %v", waiting)
	}
	var srows []chstore.MLSeriesRow
	for k, byB := range series {
		svc, part := splitKey(k)
		for b, v := range byB {
			srows = append(srows, chstore.MLSeriesRow{Signal: chstore.MLSignalTrace, Service: svc, Part: part, Bucket: time.Unix(b, 0), BucketSec: uint32(cfg.BucketSec), Count: v})
		}
	}
	if err := deps.insertSeries(ctx, srows); err != nil {
		return nil, err
	}
	cal, loc := calendarFor(deps.Store.AnomalySensitivityForDetectors())
	dates, class := baselineDates(last, cfg.BaselineDays, cal, loc)
	h := last.In(loc).Hour()
	t0 := time.Now()
	base, err := deps.Store.MLBaseline(ctx, chstore.MLBaselineRequest{Signal: chstore.MLSignalTrace, Dates: dates, HourFrom: max(h-1, 0), HourTo: min(h+1, 23),
		TargetSec: cfg.BucketSec, TZ: loc.String(), Exclude: deps.Labels.Exclude(cfg)})
	global.lastCHMs.Store(time.Since(t0).Milliseconds())
	if err != nil {
		return nil, err
	}
	dwellStart := last.Add(-time.Duration(cfg.DwellBuckets-1) * bucket).Unix()
	// dwell geçmişi CH'den (instant sorgu yalnız son kovayı verir)
	if hist, herr := deps.Store.MLSeriesRecent(ctx, chstore.MLSignalTrace, nil, depPartPrefix, "", time.Unix(dwellStart, 0), cfg.BucketSec); herr == nil {
		mergeHistory(series, hist, dwellStart, last.Unix())
	}
	c2 := cfg
	c2.MinBaselineRate = 0 // oran serisi: hacim tabanı count'tan
	var evs []chstore.MLEvent
	for k := range series {
		svc, part := splitKey(k)
		if !strings.HasSuffix(part, "|count") {
			continue
		}
		cb := base[k]
		if med, _ := medianMAD(cb); len(cb) < cfg.MinBaselineSamples || med < cfg.MinBaselineRate/float64(cfg.BucketSec) {
			continue // trafik tabanı (count = istek/sn)
		}
		stem := strings.TrimSuffix(part, "|count")
		target := strings.TrimPrefix(stem, depPartPrefix)
		ev := func(det string, j Judgement, unit string, extra map[string]any) chstore.MLEvent {
			e := map[string]any{"source": "vm", "target": target, "dayClass": class,
				"mad": j.MAD, "unit": unit, "dwell": cfg.DwellBuckets, "bucketSec": cfg.BucketSec, "baselineDays": cfg.BaselineDays}
			for x, v := range extra {
				e[x] = v
			}
			return chstore.MLEvent{ID: chstore.MLEventID(det, svc, target), Detector: det,
				Signal: chstore.MLSignalTrace, Stage: chstore.MLStageShadow, Service: svc, Code: target,
				StartedAt: last.UnixNano(), LastSeen: to.UnixNano(), WindowSec: uint32(cfg.BucketSec), Baseline: j.Median, Observed: j.Observed,
				Score: math.Abs(j.Z), Direction: "up", Evidence: mergeEvidence("", e)}
		}
		if wantLat {
			pk := chstore.MLSeriesKey(svc, stem+"|p95")
			if cur, ok := dwellWindow(series[pk], dwellStart, last.Unix(), int64(cfg.BucketSec), false); ok {
				if j := judge(cur, base[pk], cfg, true); j.Fire && d.take(detDependencyLatency, cfg, now) {
					evs = append(evs, ev(detDependencyLatency, j, "ms", nil))
				}
			}
		}
		ek := chstore.MLSeriesKey(svc, stem+"|err_ratio")
		if en, measured := errNow[ek]; wantErr && measured {
			if cur, ok := dwellWindow(series[ek], dwellStart, last.Unix(), int64(cfg.BucketSec), false); ok {
				if j := judge(cur, base[ek], c2, true); depErrFire(j, en, cfg.BucketSec) && d.take(detDependencyErrorRate, cfg, now) {
					evs = append(evs, ev(detDependencyErrorRate, j, "ratio", map[string]any{"errors": math.Round(en * float64(cfg.BucketSec)),
						"calls": math.Round(series[k][last.Unix()] * float64(cfg.BucketSec))}))
				}
			}
		}
	}
	return evs, nil
}

// setUnmeasured — dependency_error_rate'in ölçülemedi notu (açık değilse silinir).
func (d *Dependency) setUnmeasured(on bool, note string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !on || note == "" {
		d.unmeasured = nil
		return
	}
	d.unmeasured = map[string]string{detDependencyErrorRate: note}
}
