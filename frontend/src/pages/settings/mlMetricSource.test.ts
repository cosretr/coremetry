// v0.10.2019 — metrik ailesi kaynağı metni (salt-okunur).
import { describe, it, expect } from 'vitest';
import { metricSourceLabel } from './mlMetricSource';

describe('metricSourceLabel', () => {
  it('kaynak türleri', () => {
    expect(metricSourceLabel({ source: 'vm' }, true)).toBe('kaynak: VictoriaMetrics');
    expect(metricSourceLabel({ source: 'thanos', clusters: ['prod-east', 'prod-west', 'dr'] }, true)).toBe('kaynak: Thanos · 3 cluster (prod-east, prod-west, dr)');
    expect(metricSourceLabel({ source: 'none' }, true)).toBe('kaynak bulunamadı');
    expect(metricSourceLabel({ source: '' }, true)).toContain('ölçülemedi');
    expect(metricSourceLabel(undefined, true)).toContain('henüz ölçülmedi');
    expect(metricSourceLabel({ source: 'vm' }, false)).toContain('kapalı');
  });
});
