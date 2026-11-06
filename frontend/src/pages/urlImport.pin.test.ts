import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1220 — tek-yön okuma: bu taslak/seçim state'leri URL'den yalnız
// mount'ta okunuyordu; geri/ileri ve kayıtlı görünüm uygulanmıyordu.
const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');

describe('dış URL değişimi içe alınır', () => {
  it.each([
    ['Logs.tsx', /if \(urlAsc !== asc\)/],
    ['Endpoints.tsx', /if \(urlSearch !== search\) setSearch\(urlSearch\)/],
    ['SlowQueries.tsx', /if \(q === lastQRef\.current\) return;/],
    ['ServiceMap.tsx', /if \(urlFocus && urlFocus !== focus\) setFocus\(urlFocus\)/],
    ['../components/coremetry/logs/CompactLogSearch.tsx', /if \(search !== lastSearch\.current\)/], // v0.10.1900 — servis Logs sekmesi
  ])('%s', (file, re) => {
    expect(read(file)).toMatch(re);
  });
  it('SlowQueries debounce yazımı yankıyı işaretler', () => {
    expect(read('SlowQueries.tsx')).toMatch(/lastQRef\.current = v\.trim\(\); setUrlParam\('q'/);
  });
});
