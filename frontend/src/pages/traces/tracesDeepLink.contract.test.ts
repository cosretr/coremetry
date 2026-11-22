// tracesDeepLink.contract.test — v0.10.2465 (operator-reported, prod
// 2026-11-22): CoSRE traces_search'ün "Traces'te aç" linki Traces sayfasının
// KENDİ çözücüleriyle okunur ve süzgeçleri birebir verir. Fixture Go'da
// üretilir (internal/mcptools traces_search_test.go TestTracesDeepLinkFixture,
// mcptools.TracesSearchLink); bu test aynı dosyayı sayfanın okuduğu
// parametrelerle (Traces.tsx searchParams.get + decodeFilters, useUrlEnv
// resolveEnv, decodeRange) çözer. Link ya da sayfa kodeki ayrışırsa iki taraf
// da kırmızı olur.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { decodeFilters, decodeRange } from '@/lib/urlState';
import { resolveEnv } from '@/lib/useUrlEnv';

const ROOT = resolve(__dirname, '../../../..');

interface Case {
  name: string;
  href: string;
  expect: {
    service: string; search: string; cluster: string; env: string; hasError: boolean;
    minMs: number; fromMs: number; toMs: number; filters: unknown[];
  };
}

const fixture = JSON.parse(readFileSync(join(ROOT, 'testdata', 'cosre', 'traces-deeplink-v1.json'), 'utf8')) as { version: number; cases: Case[] };

describe('traces_search deep link ↔ Traces page codec', () => {
  it('fixture has cases', () => {
    expect(fixture.version).toBe(1);
    expect(fixture.cases.length).toBeGreaterThan(0);
  });
  for (const c of fixture.cases) {
    it(c.name, () => {
      expect(c.href.startsWith('/traces?')).toBe(true);
      const sp = new URLSearchParams(c.href.slice('/traces?'.length));
      expect(sp.get('service') ?? '').toBe(c.expect.service);
      expect(sp.get('search') ?? '').toBe(c.expect.search);
      expect(sp.get('cluster') ?? '').toBe(c.expect.cluster);
      expect(sp.get('hasError') === 'true').toBe(c.expect.hasError);
      expect(Number(sp.get('minMs') ?? 0)).toBe(c.expect.minMs);
      expect(decodeFilters(sp.get('filters'))).toEqual(c.expect.filters);
      // env: açık boş param kullanıcının yapışkan env seçimini EZER (tüm ortamlar).
      expect(resolveEnv(sp.get('env'), 'sticky-env')).toBe(c.expect.env);
      const r = decodeRange(sp.get('range'), { preset: '1h' });
      expect(r).toEqual({ preset: 'custom', fromMs: c.expect.fromMs, toMs: c.expect.toMs });
    });
  }
});
