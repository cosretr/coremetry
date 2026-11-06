package chstore

// self_written_metrics.go — v0.10.1858. Binary'nin KENDİ yazdığı metrik
// serilerinin kaydı.
//
// NEDEN: self-ingest-stall ("Coremetry veri almıyor", P1) metrik tarafını
// metric_catalog'un "en taze nokta"sından okuyor. v0.10.911'den beri
// evaluator her tikte ClickHouse disk doluluğunu metric_points'e yazıyor;
// Oracle hata sayacı da aynı tabloya yazıyor. İkisi de kataloğu HEP taze
// tuttuğu için "metrik akıyor" dalı hiç susmuyordu ve alarm, span'ler
// tamamen dursa bile AÇILAMIYORDU (kural "iki sinyal de sustu" ister).
//
// "Dışarıdan telemetri geliyor mu" sorusuna binary'nin kendi yazdığı seri
// cevap olamaz. Bu kayıt o serileri adlandırır; ingest tazeliği okuması
// onları dışlar. metric_points'e yazan YENİ bir iç yazar eklenirse adı
// buraya girer — TestInsertMetricsCallersAreRegistered bunu zorlar.

import "strings"

// OracleErrorCounterMetric — internal/oracle sayaç poller'ının yazdığı seri
// ("ext:" + oracle.CounterQuery). chstore oracle'ı import edemez (ters
// yön); eşitlik internal/oracle testinde çivili.
const OracleErrorCounterMetric = "ext:error_count"

// SelfWrittenMetrics — OTLP alıcısından DEĞİL, binary'nin içinden
// metric_points'e yazılan serilerin adları.
func SelfWrittenMetrics() []string {
	return []string{
		SelfDiskUsedMetric, OracleErrorCounterMetric,
		// v0.10.1878 — ES istek sayacı serileri (self_es_series.go).
		SelfESRequestsMetric, SelfESErrorsMetric, SelfESDurMsMetric,
		// v0.10.1902 — giriş / trace görüntüleme sayacı (self_activity_series.go).
		SelfActivityMetric,
	}
}

// ingestMetricLastSeenSQL — metric_catalog'dan "dışarıdan gelen en taze
// metrik noktası". İç yazarların serileri dışlanır. Katalog (ya da süzgeç
// sonrası küme) boşsa maxMerge 0 döner ve satır YİNE gelir; 0 = "hiç
// metrik gelmemiş" ve karar katmanı onu iddia olarak kullanmaz.
func ingestMetricLastSeenSQL() (string, []any) {
	names := SelfWrittenMetrics()
	args := make([]any, 0, len(names))
	for _, n := range names {
		args = append(args, n)
	}
	ph := strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ")
	return `
		SELECT toUnixTimestamp64Nano(maxMerge(last_seen_state)) AS last_seen,
		       toUnixTimestamp64Nano(now64(9))                  AS now_ns
		FROM metric_catalog
		WHERE metric NOT IN (` + ph + `)
		SETTINGS max_execution_time = 5`, args
}
