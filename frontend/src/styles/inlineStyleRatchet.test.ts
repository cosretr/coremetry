import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join, relative, sep } from 'node:path';
import { stripTsComments } from './zLayers.test';
import { PAGE_FILES, inPage, type Page } from './pageFiles';

// inlineStyleRatchet — v0.10.1614 (shadcn Faz 1 R6, plan §3.2, audit §11 n).
//
// Birim: yorumları ayıklanmış .tsx'te `style={{` literal'i (`style={expr}`
// SAYILMAZ). Kapsam: testler, components/ui (dondurulmuş atomlar) ve
// components/shadcn (üretilen) hariç. Sayfa başına tavan + bütün ağaç için
// TOTAL (listede olmayan sayfa da büyüyemesin). Geçirilmiş sayfa = tavanı 0
// VE MIGRATED'da; ikisi birlikte doğrulanır. Hesaplanan değer kaçışı
// (`lib/cssVars.ts`) ilk sayfa diliminde gelir (kullanımı olmayan yardımcı
// yok). Yalnız AŞAĞI iner; tavanı geçirilen dilim düşürür. Göç dışı süpürme
// YOK (karar n) — yalnız geçirilmiş sayfalar sıfıra iner.
const SRC = resolve(__dirname, '..');

// Taban: R6 commit'inde ölçüldü.
const CEILINGS: Record<Page, number> = {
  Settings: 845, // 63 dosya — Faz 2 dilim 1 R7: 1182 → 1132 (Remote Cluster 50 → 0); CoSRE 2b B1: 1132 → 1131 (CosreV2StatusLine); R8 (backends): 1131 → 950; R9 (bildirim): 950 → 845
  RemoteCluster: 0, // 2 dosya (Settings alt kümesi) — Faz 2 dilim 1 R7: 50 → 0, MIGRATED
  Logs: 94, // 14
  Trace: 150, // 17
  Traces: 103, // 13
  Explore: 141, // 17
  Metrics: 50, // 3
  Shell: 16, // 5 — etkileşim P2: 43 → 16 (⌘K cmdk)
};
const TOTAL = 4894; // 449 dosya (plan §3.2 v0.10.1604 ölçümü 5,244; aradaki sürümler +15) — Faz 2 dilim 1 R7: 5259 → 5209; CoSRE 2b B1: 5209 → 5208; etkileşim P2: 5180 (⌘K −27, aradaki sürümler −1); R8: 5180 → 4999; R9: 4999 → 4894
const MIGRATED: Page[] = ['RemoteCluster']; // Faz 2 dilim 1 R7 — pilot

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !p.endsWith('.test.tsx')) out.push(p);
  }
  return out;
}
const EXEMPT = ['ui', 'shadcn'].map(d => join(SRC, 'components', d) + sep);
const counts = new Map<string, number>();
for (const p of walk(SRC).filter(p => !EXEMPT.some(d => p.startsWith(d)))) {
  const n = stripTsComments(readFileSync(p, 'utf8')).split('style={{').length - 1;
  counts.set(relative(SRC, p).split(sep).join('/'), n);
}
const pageCount = (page: Page) => [...counts].reduce((a, [f, n]) => a + (inPage(page, f) ? n : 0), 0);

describe('inline style={{ mandalı (sayfa başına)', () => {
  it('her PAGE_FILES girdisi var olan bir dosya/dizine işaret ediyor', () => {
    const rels = [...counts.keys()];
    for (const [page, entries] of Object.entries(PAGE_FILES)) {
      for (const e of entries) expect(rels.some(r => (e.endsWith('/') ? r.startsWith(e) : r === e)), `${page}: ${e}`).toBe(true);
    }
  });

  for (const page of Object.keys(PAGE_FILES) as Page[]) {
    it(`${page}: style={{ tavanı aşmaz`, () => {
      expect(pageCount(page), 'geçirilmiş sayfada inline stil yok — Tailwind / token / cssVars').toBeLessThanOrEqual(CEILINGS[page]);
    });
  }

  it('TOTAL: bütün ağaç tavanı aşmaz', () => {
    const total = [...counts.values()].reduce((a, n) => a + n, 0);
    expect(total).toBeLessThanOrEqual(TOTAL);
  });

  it('MIGRATED sayfaların tavanı 0 ve ölçülen 0', () => {
    for (const p of MIGRATED) {
      expect(CEILINGS[p]).toBe(0);
      expect(pageCount(p)).toBe(0);
    }
  });

  it('inPage: dosya tam eşleşir, dizin önek eşleşir', () => {
    expect(inPage('Trace', 'pages/Trace.tsx')).toBe(true);
    expect(inPage('Trace', 'pages/Traces.tsx')).toBe(false);
    expect(inPage('Settings', 'pages/settings/ClustersTab.tsx')).toBe(true);
    expect(inPage('RemoteCluster', 'pages/settings/ClustersTab.tsx')).toBe(true);
  });
});
