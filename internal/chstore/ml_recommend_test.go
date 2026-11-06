package chstore

// ml_recommend_test.go — v0.10.1886. Önerilen ML kurulumu: verisi olan açılır,
// olmayan kapanır, ölçülemeyene ve log dedektörlerine dokunulmaz; terfi /
// eşik / ifade değişmez; girdi değişmez.

import (
	"reflect"
	"testing"
)

func chk(id, label string, checked, present bool) MLSourceCheck {
	return MLSourceCheck{ID: id, Label: label, Checked: checked, Present: present}
}

func TestRecommendMLConfig(t *testing.T) {
	cur := DefaultMLConfig()
	cur.Detectors["log_volume"] = true // log: dokunulmaz
	cur.Detectors["metric_jvm"] = true // verisi yok: kapanır
	cur.Promotion["metric_kafka"] = MLPromotion{Enabled: true, MinHitRate: 0.9, MinSamples: 30}
	cur.MaxServices = 2000
	checks := []MLSourceCheck{
		chk(MLSourceHTTPServer, "HTTP sunucu", true, true),
		chk(MLSourceGRPCServer, "gRPC sunucu", true, true),
		chk(MLSourceHTTPClient, "HTTP client", true, false),
		chk(MLSourceDBClient, "DB client", true, false),
		chk(MLSourceMessagingClient, "Mesajlaşma client", false, false), // ölçülemedi
		chk("metric_jvm", "JVM", true, false),
		chk("metric_kafka", "Kafka", true, true),
		chk("metric_pool", "Connection pool", false, false),
	}
	got, changes := RecommendMLConfig(cur, checks)
	if !got.Enabled {
		t.Fatal("ana anahtar açılmalı")
	}
	for _, d := range []string{"op_latency", "op_error_rate", "throughput_drop", "trace_pod_outlier", "trace_change_point", "metric_kafka", "log_volume"} {
		if !got.Detectors[d] {
			t.Errorf("%s açık olmalı", d)
		}
	}
	for _, d := range []string{"metric_jvm", "dependency_latency", "metric_pool", "metric_k8s", "error_rate", "template_spike"} {
		if got.Detectors[d] {
			t.Errorf("%s kapalı olmalı", d)
		}
	}
	keys := []string{}
	for _, c := range changes {
		keys = append(keys, c.Key)
	}
	want := []string{"enabled", "op_latency", "op_error_rate", "throughput_drop", "trace_pod_outlier", "trace_change_point", "metric_jvm", "metric_kafka"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("farklar: %v, want %v", keys, want)
	}
	// dependency_latency: bir kaynağı ölçülemedi → kapatma kararı yok (zaten kapalıydı, fark yok).
	// Terfi, eşik, ifade aynen.
	if got.Promotion["metric_kafka"] != cur.Promotion["metric_kafka"] || got.MaxServices != 2000 ||
		!reflect.DeepEqual(got.MetricFamilies, cur.MetricFamilies) {
		t.Fatal("terfi / eşik / ifade değişmemeli")
	}
	// Girdi değişmez.
	if cur.Enabled || cur.Detectors["op_latency"] || !cur.Detectors["metric_jvm"] {
		t.Fatal("girdi değişmemeli")
	}
}

// Ölçülemeyen kaynak "yok" sayılmaz: çalışan dedektör kapatılmaz; VM hiç
// ölçülemediyse öneri bugünkü ayarın aynısıdır.
func TestRecommendMLConfigUnmeasuredLeavesAsIs(t *testing.T) {
	cur := DefaultMLConfig()
	cur.Enabled = true
	cur.Detectors["dependency_latency"] = true
	cur.Detectors["metric_kafka"] = true
	got, changes := RecommendMLConfig(cur, []MLSourceCheck{
		chk(MLSourceHTTPClient, "HTTP client", true, false),
		chk(MLSourceDBClient, "DB client", false, false),
		chk("metric_kafka", "Kafka", false, false),
	})
	if len(changes) != 0 || !got.Detectors["dependency_latency"] || !got.Detectors["metric_kafka"] {
		t.Fatalf("ölçülemeyen kaynak yüzünden değişiklik: %+v", changes)
	}
	if _, changes := RecommendMLConfig(cur, nil); len(changes) != 0 {
		t.Fatalf("ölçüm yokken öneri = bugünkü ayar: %+v", changes)
	}
}

// Her log-dışı dedektörün kaynağı tanımlı olmalı (yeni dedektör eklenince
// öneri onu sessizce atlamasın) ve hiçbir log dedektörü haritada olmamalı.
func TestMLDetectorSourcesCoverNonLogDetectors(t *testing.T) {
	for _, d := range append(append([]string{}, MLTraceDetectorNames...), MLMetricDetectorNames...) {
		if len(mlDetectorSources[d]) == 0 {
			t.Errorf("%s: kaynak tanımsız", d)
		}
	}
	for _, d := range MLLogDetectorNames {
		if _, ok := mlDetectorSources[d]; ok {
			t.Errorf("%s: log dedektörü öneriye girmemeli (Elasticsearch yükü)", d)
		}
	}
}
