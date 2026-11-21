package ml

// metric_volume_gate.go — metrik ailesi hacim kapısı (operatör hatası
// 2026-11-21; chstore/ml_metric_volume.go). Quantile / oran serisinin kovası
// eşlik eden çağrı sayısı chstore.MLMetricMinVolume'un altındaysa kova
// ml_series'e YAZILMAZ ve yargılanmaz: olay açılmaz / uzatılmaz. Yazılmayan
// kova tabanda da yoktur — seyrek (boşluklu) seri yeterli taban örneği
// biriktiremez ve judge "eksik veri: taban < MinBaselineSamples" der.
// Hacim sorgusu başarısızsa kapı AÇIK geçer (önceki davranış; anomali
// gizlenmez). Bastırma sayacı self-obs `suppressedVolume`.

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// volumeBySvc — SAF: hacim sorgusu satırları → servis → kova çağrı sayısı.
func volumeBySvc(rows []chstore.SpanMetricSeries, svcLabel string) map[string]float64 {
	out := make(map[string]float64, len(rows))
	for _, r := range rows {
		svc := groupKeyLabels(r.GroupKey)[svcLabel]
		if v, ok := instantValue(r); svc != "" && ok {
			out[svc] += v
		}
	}
	return out
}

// belowMinVolume — SAF: kova kapıya takılır mı. vol nil = kapı yok / hacim
// ölçülemedi (geçer); servis hacim sonucunda yoksa pencerede çağrı yok (takılır).
func belowMinVolume(vol map[string]float64, svc string) bool {
	if vol == nil {
		return false
	}
	return vol[svc] < chstore.MLMetricMinVolume
}

// metricVolume — pe'nin hacim haritası; kapı yok ya da sorgu başarısız → nil.
func metricVolume(ctx context.Context, deps Deps, pe plannedExpr, svcLabel string, at, now time.Time) map[string]float64 {
	if pe.vol == "" {
		return nil
	}
	rows, skip, err := vmInstant(ctx, deps, "metric:"+pe.family, pe.vol, at, now)
	if skip || err != nil {
		if err != nil {
			log.Printf("[ml] metric %s/%s hacim sorgusu: %v (kapı bu tik açık)", pe.family, pe.id, err)
		}
		return nil
	}
	return volumeBySvc(rows, svcLabel)
}
