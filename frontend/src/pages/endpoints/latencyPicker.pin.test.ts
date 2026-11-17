import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { bucketsToSeries } from './series';
import type { EndpointRow } from '@/lib/types';

// v0.10.1199 (operatör) — Endpoint "Avg latency" kartında p50/p95/p99 seçimi.
import { t } from '@/lib/i18n';
describe('endpoint gecikme seçicisi', () => {
  it('seri p50/p95/p99 aynı kovalardan', () => {
    const row = { sparkline: [1, 2], errorsSparkline: [0, 0], p99Sparkline: [90, 99], p50Sparkline: [10, 11], p95Sparkline: [50, 55] } as unknown as EndpointRow;
    const s = bucketsToSeries(row, { preset: 'custom', fromMs: 0, toMs: 60_000 });
    expect(s.p50[0].points.map(p => p.value)).toEqual([10, 11]);
    expect(s.p95[0].points.map(p => p.value)).toEqual([50, 55]);
    expect(s.p99[0].points.map(p => p.value)).toEqual([90, 99]);
  });
  it('eski sunucu (p50/p95 yok) → boş seri, kart boş durum der', () => {
    const row = { sparkline: [1], p99Sparkline: [9] } as unknown as EndpointRow;
    expect(bucketsToSeries(row, { preset: 'custom', fromMs: 0, toMs: 60_000 }).p50).toEqual([]);
  });
  it('seçici URL\'de (?lat=), varsayılan p99', () => {
    const src = readFileSync(new URL('../EndpointDetail.tsx', import.meta.url), 'utf8');
    expect(src).toContain("params.get('lat')");
    expect(src).toContain('series={series[latQ]}');
    expect(src).toContain("aria-label={t('hm.latQ', lang)}"); // v0.10.2305 — katalogda
    expect(t('hm.latQ', 'tr')).toBe('Gecikme yüzdeliği');
  });
});

// v0.10.1201 (operatör) — kırılımda deployment.environment değeri sayfayı süzer.
import { withEnvParam } from './detailSections';
describe('kırılımdan ortam süzgeci', () => {
  it('env yazılır, diğer parametreler korunur', () => {
    expect(withEnvParam('?service=a&path=%2Fx&range=3h', 'kubey-prod')).toBe('?service=a&path=%2Fx&range=3h&env=kubey-prod');
    expect(withEnvParam('?env=old&service=a', 'kubey-prod')).toBe('?env=kubey-prod&service=a');
  });
});
