// apiQs.pin.test.ts — v0.10.2200. Operator-reported: ML servis profilleri
// listesi 404 (`/api/ml/profilesoffset=0&limit=50`). api.ts `qs()` sorguyu
// "?" OLMADAN döndürür; v0.10.1816'dan beri mlProfiles `?`'i unutuyordu.
// Kaynak taraması: api.ts'te her `${qs(` hemen `?` ya da `&` ardından gelir.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { api } from './api';

describe('api.ts qs() çağrıları (v0.10.2200)', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('her ${qs( öncesinde ? ya da & var', () => {
    const src = readFileSync(resolve(__dirname, 'api.ts'), 'utf8');
    const bad: string[] = [];
    src.split('\n').forEach((line, i) => {
      for (const m of line.matchAll(/\$\{qs\(/g)) {
        const prev = line[(m.index ?? 0) - 1];
        if (prev !== '?' && prev !== '&') bad.push(`api.ts:${i + 1}: ${line.trim()}`);
      }
    });
    expect(src.includes('${qs(')).toBe(true); // tarama boşa dönmesin
    expect(bad).toEqual([]);
  });

  it('mlProfiles /api/ml/profiles?offset=0&limit=50 ister', async () => {
    const urls: string[] = [];
    vi.stubGlobal('fetch', (url: unknown) => {
      urls.push(String(url));
      return Promise.resolve(new Response(JSON.stringify({ items: [], total: 0 }), {
        status: 200, headers: { 'content-type': 'application/json' },
      }));
    });
    await api.mlProfiles({ offset: 0, limit: 50 });
    expect(urls).toHaveLength(1);
    expect(urls[0]).toMatch(/\/api\/ml\/profiles\?offset=0&limit=50$/);
  });
});
