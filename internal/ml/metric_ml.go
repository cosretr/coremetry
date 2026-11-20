package ml

// metric_ml.go — ML metrik M1: aileler (JVM, connection pool, Kafka, HTTP
// client, k8s) = dedektörler; her ailenin MetricsQL serileri Settings'ten
// (cfg.MetricFamilies, varsayılan + düzenlenebilir). Kaynak VictoriaMetrics;
// `{svc}` servis etiketi (LabelMap.Service, yoksa service_name), `{win}` kova.
// Counter reset: ifadeler rate/increase ile; scrape boşluğu: nokta yok = örnek
// yok (eksik veri kuralı judge'da); pod etiketi ifadede servis düzeyine
// toplanır (pod_outlier hariç — T3). Seri ml_series (signal=metric, part
// `family|exprID`), taban CH takvim sınıfı × ±1 saat, log/trace ile aynı judge.
// İSTİSNA (v0.10.2382, etki olayı I5): metric_kafka consumer_lag kova yargısına
// girmez; 1 dakikalık örneklerle etki kuralı karar verir (kafka_lag_impact.go).

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const metricSeriesCap = 20000

type MetricML struct {
	mu    sync.Mutex
	daily map[string]int
	day   string
	// src — v0.10.2019: aile başına kaynak (MetricThanos tespiti); nil =
	// her açık aile VM'den (önceki davranış). Kaynağı Thanos/none olan aile
	// VM'e sorulmaz.
	src *MetricSources
	// kafkaSel — v0.10.2064 (Davis S3): consumer lag topic seçimi (30 dk; kafka_topic.go).
	kafkaSel kafkaTopicSel
	// lag — v0.10.2382 (I5): consumer lag etki kuralının yoklama durumu (kafka_lag_impact.go).
	lag lagImpactState
}

func NewMetricML() *MetricML { return &MetricML{daily: map[string]int{}} }

// WithSources — v0.10.2019: kaynak tespitini paylaş.
func (d *MetricML) WithSources(s *MetricSources) *MetricML { d.src = s; return d }

func (d *MetricML) Name() string { return "metric_ml" }

func (d *MetricML) Enabled(cfg chstore.MLConfig) bool {
	for _, f := range chstore.MLMetricDetectorNames {
		if cfg.DetectorOn(f) {
			return true
		}
	}
	return false
}

// renderExpr — SAF: {svc} / {win} yer tutucuları.
func renderExpr(expr, svcLabel string, winSec int) string {
	return strings.NewReplacer("{svc}", svcLabel, "{win}", fmt.Sprintf("%ds", winSec)).Replace(expr)
}

// metricPart — ml_series part.
func metricPart(family, exprID string) string { return family + "|" + exprID }

func (d *MetricML) dayCap(det string, cfg chstore.MLConfig, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	day := now.Format("2006-01-02")
	if d.day != day {
		d.day, d.daily = day, map[string]int{}
	}
	if d.daily[det] >= cfg.DailyEventCap {
		return false
	}
	d.daily[det]++
	return true
}

func (d *MetricML) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	src := d.src.Sources()
	onVM := false
	for _, fam := range chstore.MLMetricDetectorNames {
		onVM = onVM || (cfg.DetectorOn(fam) && familyOnVM(src, fam))
	}
	if !onVM { // v0.10.2019: açık ailelerin hepsi Thanos'ta ya da kaynaksız
		return nil, nil
	}
	if deps.VM == nil || !deps.VM.Configured() {
		return nil, fmt.Errorf("VictoriaMetrics yapılandırılmamış: metrik ML sessiz")
	}
	svcLabel := deps.VM.MetricLabelMap().Service
	if svcLabel == "" {
		svcLabel = "service_name"
	}
	bucket := time.Duration(cfg.BucketSec) * time.Second
	last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec)
	to := last.Add(bucket)
	series := map[string]map[int64]float64{}
	dirOf := map[string]string{} // part → direction
	var waiting []string
	// v0.10.2382 (I5): consumer lag KARARI 5 dakikalık kovayla değil, etki
	// kuralıyla (kafka_lag_impact.go). lagQ = bu tikte 1 dk adımla sorulacak
	// ifade (dedektörün kendi ifadesi); nil = ölçü yok / beklemede ya da
	// operatör ifadesi 1 dk'da örneklenemiyor (o kurulumda eski kova yargısı).
	// Aşağıdaki anlık sorgular DEĞİŞMEDİ: 5 dk seri taban için yazılmaya devam eder.
	var lagQ *lagQuery
	_, lagSafe := lagImpactExpr(cfg)
	for _, pe := range metricExprs(cfg, svcLabel, cfg.BucketSec) {
		if !familyOnVM(src, pe.family) {
			continue
		}
		isLag := pe.family == detMetricKafka && pe.id == kafkaLagExprID
		// v0.10.2064 (Davis S3): varsayılan consumer_lag ifadesi servis × topic
		// (kafka_topic.go); topic etiketli seri yoksa aşağıdaki servis düzeyi ifade.
		if isLag && kafkaTopicDefault(cfg) {
			tv, ok, skip, err := d.kafkaTopicSeries(ctx, deps, cfg, svcLabel, to, now)
			if skip {
				if len(waiting) == 0 || waiting[len(waiting)-1] != pe.family {
					waiting = append(waiting, pe.family)
				}
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("vm %s/%s topic: %w", pe.family, pe.id, err)
			}
			if ok {
				for svc, ts := range tv {
					for t, v := range ts {
						part := kafkaTopicPart(t)
						dirOf[part] = pe.direction
						k := chstore.MLSeriesKey(svc, part)
						if series[k] == nil {
							if len(series) >= metricSeriesCap {
								continue
							}
							series[k] = map[int64]float64{}
						}
						series[k][last.Unix()] = v
					}
				}
				lagQ = &lagQuery{expr: kafkaTopicExpr(svcLabel), svcLabel: svcLabel, topic: true}
				continue
			}
		}
		rows, skip, err := vmInstant(ctx, deps, "metric:"+pe.family, pe.expr, to, now)
		if skip {
			if len(waiting) == 0 || waiting[len(waiting)-1] != pe.family {
				waiting = append(waiting, pe.family)
			}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("vm %s/%s: %w", pe.family, pe.id, err)
		}
		if isLag && lagSafe { // servis düzeyi ifade ({win} yok: çözülmüş hali adımdan bağımsız)
			lagQ = &lagQuery{expr: pe.expr, svcLabel: svcLabel}
		}
		part := metricPart(pe.family, pe.id)
		dirOf[part] = pe.direction
		for _, r := range rows {
			svc := groupKeyLabels(r.GroupKey)[svcLabel]
			v, ok := instantValue(r)
			if svc == "" || !ok {
				continue
			}
			k := chstore.MLSeriesKey(svc, part)
			if series[k] == nil {
				if len(series) >= metricSeriesCap {
					break
				}
				series[k] = map[int64]float64{}
			}
			series[k][last.Unix()] = v
		}
	}
	if len(waiting) > 0 && len(series) == 0 {
		return nil, fmt.Errorf("VM metrik aileleri beklemede (backoff): %v", waiting)
	}
	// 1 dk'lık lag örnekleri: TEK aralık sorgusu (yoklama aynı pencere bitişi
	// için aldıysa sıfır). Hatası YALITILIR: tik düşmez, tik hatası olarak
	// DÖNMEZ; yalnız lag bu tik sessiz kalır ve "kısmen ölçülüyor" görünür.
	lagEnd := svcImpactEnd(now, cfg.IngestLagSec)
	var lagMin map[string]map[int64]float64
	var lagErr error
	var owned func(part string) bool
	if lagQ != nil {
		lagMin, lagErr = d.lagMinutes(ctx, deps, *lagQ, lagEnd, now)
		owned = isLagImpactPart // 5 dk kova yargısı bu part'lar için yok
	}
	evs, mb, err := evalMetricSeriesOwned(ctx, deps, cfg, last, now, series, dirOf, func(fam string) bool { return d.dayCap(fam, cfg, now) },
		func(string) map[string]any { return nil }, owned)
	if err != nil {
		return nil, err
	}
	return append(evs, d.lagImpactTick(deps, lagQ, lagMin, lagErr, mb, lagEnd, cfg, now)...), nil
}
