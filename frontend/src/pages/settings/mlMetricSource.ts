// mlMetricSource — v0.10.2019: Settings › ML metrik ailesi satırının
// SALT-OKUNUR kaynak metni. Kaynak ayar değildir; ML lideri saatte bir
// otomatik seçer (VictoriaMetrics varsa VM, yoksa metriği taşıyan Thanos
// cluster'ları) ve tik durumuna yazar.
import type { MLMetricSource } from '@/lib/types';

/** SAF (test). */
export function metricSourceLabel(src: MLMetricSource | undefined, enabled: boolean): string {
  if (!enabled) return 'kaynak: dedektör kapalı, ölçülmedi';
  if (!src) return 'kaynak: henüz ölçülmedi (ilk ML tikinde)';
  switch (src.source) {
    case 'vm': return 'kaynak: VictoriaMetrics';
    case 'thanos': {
      const n = src.clusters?.length ?? 0;
      return `kaynak: Thanos · ${n} cluster${n ? ` (${(src.clusters ?? []).join(', ')})` : ''}`;
    }
    case 'none': return 'kaynak bulunamadı';
    default: return 'kaynak: ölçülemedi (VictoriaMetrics deneniyor)';
  }
}
