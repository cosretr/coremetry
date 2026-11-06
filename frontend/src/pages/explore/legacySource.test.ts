// v0.10.1867 — eski Explore `source=metrics|logs` linkleri /metrics ve
// /logs'a yönlenir (Faz 2 dilim 2.7). Metrik yönü Metrics Explorer'ın
// tek-model içe aktarımıyla aynı soruyu açar (legacy.ts zinciri).
import { describe, expect, it } from 'vitest';
import { exploreLegacySourceHref } from './legacySource';
import { importLegacyMetricsUrl } from '@/lib/metricsExplorer/legacy';

describe('exploreLegacySourceHref', () => {
  it.each([
    ['?q=x', null],
    ['', null],
    ['?source=spans', null],
    ['?source=metrics&service=orders-api&metric=jvm_memory_used_bytes&range=6h&viz=bar', '/metrics?metric=jvm_memory_used_bytes&service=orders-api&range=6h'],
    ['?source=metrics&service=orders-api&cluster=acme-prod-1', '/metrics?editor=0&service=orders-api&cluster=acme-prod-1'],
    ['?source=metrics', '/metrics?editor=0'],
    ['?source=logs&service=payments-mobile&range=1h&env=prod', '/logs?service=payments-mobile&range=1h&env=prod'],
    ['source=logs', '/logs'],
  ])('%s → %s', (search, want) => expect(exploreLegacySourceHref(search)).toBe(want));

  it('metrik yönü Metrics Explorer içe aktarımında sorgu A olur', () => {
    const href = exploreLegacySourceHref('?source=metrics&service=orders-api&metric=jvm_memory_used_bytes')!;
    const imp = importLegacyMetricsUrl(new URLSearchParams(href.split('?')[1]))!;
    expect(imp.model!.queries[0]).toMatchObject({
      metric: 'jvm_memory_used_bytes', fn: 'raw', agg: 'avg', filters: [{ k: 'service_name', op: '=', v: 'orders-api' }],
    });
  });
  it('metriksiz → Keşif, servis süzgeçli', () => {
    const href = exploreLegacySourceHref('?source=metrics&service=orders-api')!;
    expect(importLegacyMetricsUrl(new URLSearchParams(href.split('?')[1]))).toMatchObject({ tab: 'discover', service: 'orders-api' });
  });
});
