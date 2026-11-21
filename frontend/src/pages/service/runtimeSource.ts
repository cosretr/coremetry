// runtimeSource — follow-up 2026-11-21 (operatör onayı): her runtime grafik
// kartının başlığında kaynak ipucu — hangi metrik ailesi, hangi kaynaktan
// (VM OTel / Micrometer / Thanos JMX / ClickHouse metric_points). SAF; ad
// biçiminden çıkarılır (sunucu profili katalogdaki yazımı AYNEN verir: VM'de
// Prometheus adı, CH'de noktalı OTLP adı).

export type RuntimeSourceKind = 'micrometer' | 'jmx' | 'vm-otel' | 'otel';

const MICROMETER = /^(jvm_memory_bytes_|jvm_threads_|jvm_gc_pause_seconds|process_files_|httpcomponents_|hikaricp_|tomcat_|executor_)/;
const JMX = /^(jboss_|java_lang_|wildfly_|jmx_)/;

/** SAF — metrik adlarından kaynak sınıfı (ilk eşleşen kazanır: JMX > Micrometer > VM OTel > OTel). */
export function runtimeSourceKind(metrics: string[]): RuntimeSourceKind {
  if (metrics.some(m => JMX.test(m))) return 'jmx';
  if (metrics.some(m => MICROMETER.test(m))) return 'micrometer';
  if (metrics.length > 0 && metrics.every(m => !m.includes('.'))) return 'vm-otel';
  return 'otel';
}

export const RUNTIME_SOURCE_LABEL: Record<RuntimeSourceKind, string> = {
  micrometer: 'Micrometer · VM',
  jmx: 'Thanos JMX',
  'vm-otel': 'VM OTel',
  otel: 'OTel · VM / metric_points',
};

/** SAF — rozet metni + ipucu birlikte (kart başlığındaki gri rozet). */
export function runtimeSourceBadge(metrics: string[], tpl: string): { label: string; tip: string } {
  return { label: RUNTIME_SOURCE_LABEL[runtimeSourceKind(metrics.filter(Boolean))], tip: runtimeSourceTip(metrics, tpl) };
}

/** SAF — kart başlığı ipucu: aile (metrik adları) + kaynak. */
export function runtimeSourceTip(metrics: string[], tpl: string): string {
  const uniq = [...new Set(metrics.filter(Boolean))];
  return tpl.replace('{m}', uniq.join(', ')).replace('{s}', RUNTIME_SOURCE_LABEL[runtimeSourceKind(uniq)]);
}
