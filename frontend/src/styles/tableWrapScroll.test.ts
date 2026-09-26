// tableWrapScroll.test.ts — v0.10.930 (tablo standardı dilim 0, hata).
//
// `.table-wrap` tabanı `overflow-y: hidden`. Slos öneri önizlemesi kaba
// `maxHeight: '50vh'` verip iç kaydırmayı açmayı unutmuştu: 50vh'yi aşan
// satırlar kaydırma çubuğu olmadan KESİLİYORDU. Kural: yüksekliği sınırlanan
// tablo kabı `is-scroll` taşır ve `is-scroll` kabın kendisini kaydırır
// (yapışkan başlık da oradan gelir).
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { stripTsComments } from './zLayers.test';

const SRC = resolve(__dirname, '..');
const css = readFileSync(resolve(__dirname, 'globals.css'), 'utf8');

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !p.endsWith('.test.tsx')) out.push(p);
  }
  return out;
}

describe('tablo kabı yüksekliği sınırlanınca kayar (v0.10.930)', () => {
  it('.table-wrap.is-scroll kabın kendisini dikeyde kaydırır', () => {
    const m = css.match(/\n\.table-wrap\.is-scroll \{([^}]*)\}/);
    expect(m, '.table-wrap.is-scroll kuralı').not.toBeNull();
    expect(m![1]).toMatch(/overflow-y:\s*auto/);
  });

  it('maxHeight verilen her .table-wrap is-scroll taşır', () => {
    const offenders: string[] = [];
    for (const f of walk(SRC)) {
      const src = stripTsComments(readFileSync(f, 'utf8'));
      for (const m of src.matchAll(/<div\s+className="(table-wrap[^"]*)"([^>]*)>/g)) {
        if (/maxHeight/.test(m[2]) && !/\bis-scroll\b/.test(m[1])) {
          offenders.push(`${f.slice(SRC.length + 1)}:${src.slice(0, m.index).split('\n').length}`);
        }
      }
    }
    expect(offenders).toEqual([]);
  });

  it('Slos öneri önizlemesi is-scroll', () => {
    const s = readFileSync(resolve(SRC, 'pages/Slos.tsx'), 'utf8');
    expect(s).toContain(`<div className="table-wrap is-scroll" style={{ maxHeight: '50vh' }}>`);
  });
});
