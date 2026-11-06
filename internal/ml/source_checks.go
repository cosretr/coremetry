package ml

// source_checks.go — v0.10.1886 (operatör 2026-11-06: "anomali ayarları çok
// karmaşık, sen default kur"). Önerilen kurulumun girdisi: ML dedektörlerini
// besleyen kaynaklardan hangisi VictoriaMetrics'te GERÇEKTEN var.
//
// Tik DIŞI, yalnız admin "Önerilen kurulum"u açınca koşar: histogram aileleri
// için etiket keşfi (son 1 saat; tespit kodunun kullandığı aynı çağrı), metrik
// aileleri için ailenin kendi ifadeleri (anlık sorgu; ilk veri döndüren yeter).
// En çok 6 keşif + aile ifadesi sayısı kadar sorgu, her biri 5 sn tavanlı,
// aynı anda en çok 4 istek.
// Aile backoff'una YAZMAZ (tespiti etkilemez). Elasticsearch'e istek YOK.
// Hata = "ölçülemedi" (Checked=false); "yok" denmez.

import (
	"context"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	sourceCheckTimeout     = 5 * time.Second
	sourceCheckLookback    = time.Hour
	sourceCheckConcurrency = 4
)

var metricFamilyLabels = map[string]string{
	"metric_jvm":         "JVM",
	"metric_pool":        "Connection pool",
	"metric_kafka":       "Kafka",
	"metric_http_client": "HTTP client (metrik)",
	"metric_k8s":         "Kubernetes",
}

var depSourceIDs = map[string][2]string{
	"http":      {chstore.MLSourceHTTPClient, "HTTP client"},
	"db":        {chstore.MLSourceDBClient, "Veritabanı client"},
	"messaging": {chstore.MLSourceMessagingClient, "Mesajlaşma client"},
}

// SourceChecks — kaynak durumları, sabit sırada (sunucu → client → metrik aileleri).
func SourceChecks(ctx context.Context, vm anomaly.REDVM, cfg chstore.MLConfig, now time.Time) []chstore.MLSourceCheck {
	out := []chstore.MLSourceCheck{
		{ID: chstore.MLSourceHTTPServer, Label: "HTTP sunucu"},
		{ID: chstore.MLSourceGRPCServer, Label: "gRPC sunucu"},
	}
	for _, f := range depFamilies {
		out = append(out, chstore.MLSourceCheck{ID: depSourceIDs[f.ID][0], Label: depSourceIDs[f.ID][1]})
	}
	for _, fam := range chstore.MLMetricDetectorNames {
		out = append(out, chstore.MLSourceCheck{ID: fam, Label: metricFamilyLabels[fam]})
	}
	if vm == nil || !vm.Configured() {
		for i := range out {
			out[i].Note = "VictoriaMetrics bağlı değil"
		}
		return out
	}
	from := now.Add(-sourceCheckLookback)
	// probe — metriğin son 1 saatte etiketi var mı (seri var mı).
	probe := func(metrics ...string) (present, checked bool, note string) {
		for _, m := range metrics {
			pctx, cancel := context.WithTimeout(ctx, sourceCheckTimeout)
			names, _, err := vm.MetricLabelNamesIn(pctx, m, from, now)
			cancel()
			if err != nil {
				note = "ölçülemedi: " + err.Error()
				continue
			}
			checked = true
			if len(names) > 0 {
				return true, true, ""
			}
		}
		if checked {
			note = ""
		}
		return false, checked, note
	}
	var httpProbes, grpcProbes []string
	for _, p := range anomaly.REDFamilyPins() {
		if p.ID == "http" {
			httpProbes = append(httpProbes, p.Probe)
		} else {
			grpcProbes = append(grpcProbes, p.Probe)
		}
	}
	svcLabel := vm.MetricLabelMap().Service
	if svcLabel == "" {
		svcLabel = "service_name"
	}
	at := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec).Add(time.Duration(cfg.BucketSec) * time.Second)
	// family — ailenin kendi ifadeleri; ilk veri döndüren yeter.
	family := func(fam string) (present, checked bool, note string) {
		for _, e := range cfg.MetricFamilies[fam] {
			qctx, cancel := context.WithTimeout(ctx, sourceCheckTimeout)
			rows, err := vm.QueryPromQLInstant(qctx, renderExpr(e.Expr, svcLabel, cfg.BucketSec), at)
			cancel()
			if err != nil {
				note = "ölçülemedi: " + err.Error()
				continue
			}
			checked = true
			if len(rows) > 0 {
				return true, true, ""
			}
		}
		switch {
		case checked:
			note = "ailenin ifadeleri veri döndürmedi"
		case len(cfg.MetricFamilies[fam]) == 0:
			checked, note = true, "ailede ifade yok"
		}
		return false, checked, note
	}
	// Görev başına bir satır (ayrı indeks — yarış yok); en çok
	// sourceCheckConcurrency eşzamanlı VM isteği.
	tasks := make([]func() (bool, bool, string), len(out))
	for i := range out {
		id := out[i].ID
		switch id {
		case chstore.MLSourceHTTPServer:
			tasks[i] = func() (bool, bool, string) { return probe(httpProbes...) }
		case chstore.MLSourceGRPCServer:
			tasks[i] = func() (bool, bool, string) { return probe(grpcProbes...) }
		default:
			tasks[i] = func() (bool, bool, string) { return family(id) }
			for _, f := range depFamilies {
				if depSourceIDs[f.ID][0] == id {
					metric := f.Metric + "_count"
					tasks[i] = func() (bool, bool, string) { return probe(metric) }
				}
			}
		}
	}
	sem := make(chan struct{}, sourceCheckConcurrency)
	var wg sync.WaitGroup
	for i := range tasks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i].Present, out[i].Checked, out[i].Note = tasks[i]()
		}(i)
	}
	wg.Wait()
	return out
}
