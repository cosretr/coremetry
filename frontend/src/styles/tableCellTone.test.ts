// tableCellTone.test.ts — v0.10.931 (tablo standardı dilim 0, hata; T9).
//
// ServiceBacktrace ve AdminStats hata oranı hücrelerine `num err` /
// `num warn` basıyordu. `.err` / `.warn` globals.css'te YALNIZ bileşik
// seçicilerde (`.pb-pill.err`, …) var: undefinedCssRefs sınıfı "tanımlı"
// sayıyordu ama tablo hücresine hiçbir kural uymuyordu — %5 üstü hata
// oranı düz metin görünüyordu. Kural: hücre tonu `.cell-err` / `.cell-warn`
// (yalnız metin rengi, dolgu yok); çıplak `err` / `warn` hücrede yasak.
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

describe('tablo hücresi tonu (v0.10.931, T9)', () => {
  it('.cell-err / .cell-warn yalnız metin rengi', () => {
    expect(css).toMatch(/\n\.cell-err\s*\{\s*color:\s*var\(--err\);\s*\}/);
    expect(css).toMatch(/\n\.cell-warn\s*\{\s*color:\s*var\(--warn\);\s*\}/);
  });

  it('hiçbir <td> çıplak err / warn sınıfı taşımaz', () => {
    const offenders: string[] = [];
    for (const f of walk(SRC)) {
      const src = stripTsComments(readFileSync(f, 'utf8'));
      for (const m of src.matchAll(/<td\b[^>]*?className=(\{`[^`]*`\}|"[^"]*")/g)) {
        const cls = m[1];
        if (/['"\s`](err|warn)['"\s`]/.test(cls)) {
          offenders.push(`${f.slice(SRC.length + 1)}:${src.slice(0, m.index).split('\n').length}`);
        }
      }
    }
    expect(offenders).toEqual([]);
  });

  it('iki eski site yeni sınıfları kullanır', () => {
    const bt = readFileSync(resolve(SRC, 'pages/ServiceBacktrace.tsx'), 'utf8');
    const st = readFileSync(resolve(SRC, 'pages/AdminStats.tsx'), 'utf8');
    expect(bt).toContain("errBad ? 'cell-err' : errWarn ? 'cell-warn' : ''");
    expect(st).toContain("errPct >= 5 ? 'cell-err' : errPct > 0 ? 'cell-warn' : ''");
  });
});
