// mlDetectorLabels — ML dedektörlerinin Türkçe kısa adları (ml_events.detector).
// v0.10.2163: components/coremetry/anomalies/MLTab.tsx'ten buraya taşındı —
// Problem detayının olay çizelgesi (features/anomalies/problemEvents.ts) de
// okuyor ve o sayfa Problems parçasında; adlar için ML sekmesinin tamamı o
// parçaya girmesin. MLTab aynı adı yeniden dışa açar (mevcut içe aktarmalar
// değişmedi).
import { t, type Lang } from './i18n';

export const ML_DETECTOR_TR: Record<string, string> = {
  log_volume: 'Log hacmi', error_rate: 'Hata oranı', service_silent: 'Servis sustu',
  template_spike: 'Şablon spike', rare_template: 'Nadir şablon', new_template: 'Yeni log kalıbı', rare_error_code: 'Nadir hata kodu',
  pod_outlier: 'Aykırı pod', change_point: 'Kırılma',
  op_latency: 'Endpoint gecikmesi', op_error_rate: 'Endpoint hata oranı', throughput_drop: 'Trafik düşüşü',
  metric_jvm: 'JVM', metric_pool: 'Connection pool', metric_kafka: 'Kafka', metric_http_client: 'HTTP client', metric_k8s: 'Kubernetes',
  dependency_latency: 'Bağımlılık gecikmesi', trace_pod_outlier: 'Aykırı pod (trace)', trace_change_point: 'Kırılma (trace)', trace_error_count: 'Operasyon hata sayısı',
  dependency_error_rate: 'Bağımlılık hata oranı', // v0.10.2063 Davis S2
  svc_error_rate: 'Servis hata oranı', // v0.10.2077 (spec-ml-service-level dilim 1)
  svc_breadth: 'Yaygın yavaşlama', // v0.10.2086 (dilim 2)
  svc_response_time: 'Servis yanıt süresi', // v0.10.2385 (etki olayı I4: medyan + en yavaş %10)
  endpoint_impact: 'Uç nokta etkisi', // v0.10.2474 (uç nokta hata oranı + yanıt süresi; etki kuralı)
  pod_oom: 'OOMKilled pod', // v0.10.2476 (kube-state-metrics; iş yükü başına, servisin Problem'ine üye)
  svc_volume_spike: 'Servis hacmi sıçradı', // v0.10.2119 — evaluator yazar (24 sa / önceki 24 sa span sayısı)
  svc_telemetry_volume: 'Telemetri hacmi', // v0.10.2242 (Dilim B) — ML hacim dedektörü (istek başına span)
};

/** v0.10.2282 — arayüz dilinde dedektör adı (TR yukarıdaki harita, EN i18n mlDetector.*);
 *  bilinmeyen dedektör kimliğiyle döner. */
export function mlDetectorLabel(d: string, lang: Lang = 'tr'): string {
  if (lang === 'tr') return ML_DETECTOR_TR[d] ?? d;
  const k = `mlDetector.${d}`;
  const v = t(k, lang);
  return v === k ? d : v;
}
