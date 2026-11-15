// podSectionOrder.test.ts — v0.10.177 (operatör, prod pod görüntüsü): Thanos
// CPU/Bellek trend bölümü sayfanın başında, servis (RED) metriklerinin ÜSTÜNDE
// — pod'un kendi sinyali önce; span'siz pod'da RED boş. v0.10.2178 (Clusters
// S10, D8): KPI şeridi kalktı, yerine meta satırı; RED "İstek metrikleri"
// kapalı bölümünde ve trendin altında.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('pod sayfası bölüm sırası (v0.10.177, S10)', () => {
  const src = readFileSync(resolve(__dirname, '../Pod.tsx'), 'utf8');
  it('meta satırı → CPU / Bellek · Thanos → Son trace\'ler (v0.10.2195: İstek metrikleri kalktı)', () => {
    const meta = src.indexOf('<PodMetaRow');
    const infra = src.indexOf('CPU / Bellek · Thanos');
    const trend = src.indexOf('<ThanosTrendPanel');
    const red = src.indexOf('id="pod-traces"');
    expect(src).not.toContain('PodKpiStrip');
    expect(src).not.toContain('İstek metrikleri');
    expect(src).not.toContain('spanMetricBatch');
    expect(meta).toBeGreaterThan(0);
    expect(infra).toBeGreaterThan(meta);
    expect(trend).toBeGreaterThan(infra);
    expect(red).toBeGreaterThan(trend);
  });
});
