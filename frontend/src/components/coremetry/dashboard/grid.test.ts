import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { panelSpan, panelSpanClass, widthForSpan, SPAN_CHOICES } from './grid';
import type { PanelWidth } from '@/lib/types';

// v0.10.1999 (Dashboards Faz 3 dilim 3.5) — 12 kolon akış ızgarası.
describe('panelSpan', () => {
  it('span yoksa width × 3: eski 4 kolonlu panolar aynı oranda dizilir', () => {
    const cases: [PanelWidth, number][] = [[1, 3], [2, 6], [3, 9], [4, 12]];
    for (const [w, want] of cases) expect(panelSpan({ width: w })).toBe(want);
  });
  it('span verilince o; 2..12 kelepçe, yuvarlama', () => {
    expect(panelSpan({ width: 1, span: 8 })).toBe(8);
    expect(panelSpan({ width: 4, span: 1 })).toBe(2);
    expect(panelSpan({ width: 4, span: 40 })).toBe(12);
    expect(panelSpan({ width: 4, span: 6.4 })).toBe(6);
  });
  it('widthForSpan en yakın eski basamak', () => {
    expect([2, 3, 4, 6, 8, 9, 12].map(widthForSpan)).toEqual([1, 1, 1, 2, 3, 3, 4]);
  });
  it('her span için sınıf var ve Tailwind düz metin olarak görür', () => {
    const src = readFileSync(resolve(__dirname, 'grid.ts'), 'utf8');
    for (let n = 2; n <= 12; n++) {
      const cls = panelSpanClass({ width: 4, span: n });
      expect(cls).toContain(`col-span-${n}`);
      expect(src).toContain(`'${cls}'`);
    }
    for (const n of SPAN_CHOICES) expect(n >= 2 && n <= 12).toBe(true);
  });
});
