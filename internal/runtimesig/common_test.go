package runtimesig

import "testing"

func TestCommonSignalsMicrometerBFF(t *testing.T) {
	// JVM BFF: OTel semconv + Micrometer birlikte; aile başına tek seçim.
	cat := []string{"jvm_memory_used_bytes", "jvm_memory_max_bytes", "jvm_thread_count", "jvm_threads_live_threads",
		"process_files_open_files", "process_files_max_files",
		"httpcomponents_httpclient_pool_total_pending", "httpcomponents_httpclient_pool_total_connections", "httpcomponents_httpclient_pool_total_max"}
	p := Resolve(RuntimeJVM, cat)
	if th, _ := p.Get(SigThreads); th.Metric != "jvm_thread_count" {
		t.Fatalf("threads must prefer OTel semconv, got %q", th.Metric)
	}
	for _, k := range []SignalKey{SigFDOpen, SigFDMax, SigHTTPPending, SigHTTPLeased, SigHTTPMax} {
		if !p.Has(k) {
			t.Errorf("%s çözülmedi", k)
		}
	}
	if l, _ := p.Get(SigHTTPLeased); l.FilterKey != "state" || l.FilterValue != "leased" {
		t.Errorf("leased filter %+v", l)
	}
	if got := detectSignalsFor(RuntimeJVM); len(got) != 1+len(commonDetect) {
		t.Errorf("jvm detect signals = %v", got)
	}
	if g, _ := Resolve(RuntimeJVM, []string{"kafka_consumer_records_lag_max"}).Get(SigKafkaLag); g.Agg != "max" {
		t.Errorf("lag agg = %+v", g)
	}
	for _, n := range []string{"process_files_open_files", "httpcomponents_httpclient_pool_total_pending", "kafka_consumer_records_lag_max"} {
		if !IsRuntimeMetric(n) {
			t.Errorf("catalog filter drops %s", n)
		}
	}
	// Bilinmeyen runtime da ortak sinyalleri alır.
	if q := Resolve(RuntimeUnknown, cat); !q.Has(SigFDOpen) {
		t.Error("unknown runtime should resolve common signals")
	}
}

func TestFDAndHTTPPoolSaturation(t *testing.T) {
	d := PodData{Service: "svc-bff", Pod: "pod-1", Series: map[SignalKey][]Point{
		SigFDOpen:      series(step(2000, 3950)),
		SigFDMax:       series(step(4096, 4096)),
		SigHTTPPending: series(step(0, 12)),
		SigHTTPLeased:  series(step(10, 50)),
		SigHTTPMax:     series(step(50, 50)),
	}}
	sigs := EvaluatePod(d, fixNow, th())
	if s := only(t, sigs, CheckFDSaturation); s.Level != LevelCritical {
		t.Fatalf("fd = %+v", s)
	}
	if s := only(t, sigs, CheckHTTPPoolSaturn); s.Level != LevelCritical || s.Extra["poolUtilPct"] != 100 {
		t.Fatalf("pool = %+v", s)
	}
	// Kafka: producer thread'leri buffer bekliyor + consumer lag 200 → 5000.
	k := PodData{Service: "svc-bff", Pod: "pod-1", Series: map[SignalKey][]Point{
		SigKafkaWaiting: series(step(0, 3)),
		SigKafkaLag:     series(step(200, 5000)),
	}}
	ks := EvaluatePod(k, fixNow, th())
	if s := only(t, ks, CheckKafkaBackpressure); s.Level != LevelWarning {
		t.Fatalf("backpressure = %+v", s)
	}
	only(t, ks, CheckKafkaLag)
	// Bekleyen var ama havuz %40 dolu → doygunluk değil.
	d.Series[SigHTTPLeased] = series(step(10, 20))
	none(t, EvaluatePod(d, fixNow, th()), CheckHTTPPoolSaturn)
}
