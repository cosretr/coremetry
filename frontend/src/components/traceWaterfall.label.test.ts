// v0.10.1643 — Operator-reported: kısa span'lerde süre etiketi ("2.26ms",
// "385µs") çubuğun üstüne biniyordu — ilk rakam renkli çubuğun içinde
// kalıp okunmuyordu. Kök neden: dış etiket `start% + width% + 4px`'e
// konuyordu ama .wf-bar'ın border-box `padding: 0 5px`'i çubuğu en az
// 10px çiziyor; içeri/dışarı kararı da piksel değil sabit %6 eşiğiydi.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { WF_TRACK_INSET_PX, wfBarAreaPx, wfBarVisualPx, wfLabelPlacement, wfOutsideRightLeft } from './traceWaterfall.label';

// v0.10.1724 — Operator-reported: iz sonunda/yakınında biten span'ın çubuğu
// sayfanın sağ kenarına yapışıyordu (süre etiketi hemen solunda). Zaman
// ölçeği sağdan --wf-track-inset (= --sp-6) kadar içeri alınır.
describe('wf zaman ölçeği sağ boşluğu', () => {
  const area = wfBarAreaPx(1000, 300);
  it.each([
    ['iz başı → 0', 0, 0],
    ['iz ortası orantılı', 0.5, (1000 - 300 - WF_TRACK_INSET_PX) / 2],
    ['iz sonu → boşluklu kenar', 1, 1000 - 300 - WF_TRACK_INSET_PX],
  ])('%s', (_n, frac, wantPx) => {
    expect(area * frac).toBe(wantPx);
  });
  it('ölçülmemiş / dar kap 0', () => {
    expect(wfBarAreaPx(0, 300)).toBe(0);
    expect(wfBarAreaPx(305, 300)).toBe(0);
  });
  it('iz sonuna yapışık kısa span etiketi sola alınır (boşluklu alanla)', () => {
    expect(wfLabelPlacement({ barAreaPx: area, startPct: 99.5, widthPct: 0.5, label: '385µs' })).toBe('left');
  });
  it('CSS boşluğu token ve JS sabitiyle aynı (cetvel + satır aynı kutu)', () => {
    const css = readFileSync(resolve(__dirname, '..', 'styles', 'globals.css'), 'utf8');
    expect(css).toMatch(/#wf-outer \{ --wf-track-inset: var\(--sp-6\); \}/);
    expect(css).toMatch(/\.wf-col-bar, \.wf-row-bar \{ margin-right: var\(--wf-track-inset\); \}/);
    expect(css).toMatch(new RegExp(`--sp-6: ${WF_TRACK_INSET_PX}px;`));
  });
});

describe('wfLabelPlacement', () => {
  const cases: Array<[string, Parameters<typeof wfLabelPlacement>[0], ReturnType<typeof wfLabelPlacement>]> = [
    ['long bar fits label', { barAreaPx: 800, startPct: 10, widthPct: 50, label: '2.26ms' }, 'inside'],
    // 6.5% of 600px = 39px < 6 mono chars + padding → was "inside" (>6%), clipped.
    ['narrow-but-over-6% bar goes outside', { barAreaPx: 600, startPct: 10, widthPct: 6.5, label: '2.26ms' }, 'right'],
    ['tiny bar → right', { barAreaPx: 800, startPct: 10, widthPct: 0.1, label: '385µs' }, 'right'],
    ['tiny bar at track end → left', { barAreaPx: 800, startPct: 99.5, widthPct: 0.1, label: '385µs' }, 'left'],
    ['tiny bar at track start, no room either side → right', { barAreaPx: 40, startPct: 0, widthPct: 1, label: '385µs' }, 'right'],
    ['full-width bar → inside', { barAreaPx: 800, startPct: 0, widthPct: 99.9, label: '1.20s' }, 'inside'],
    ['unmeasured container keeps % rule (inside)', { barAreaPx: 0, startPct: 0, widthPct: 50, label: '2.26ms' }, 'inside'],
    ['unmeasured container keeps % rule (right)', { barAreaPx: 0, startPct: 95, widthPct: 1, label: '2.26ms' }, 'right'],
  ];
  it.each(cases)('%s', (_n, input, want) => {
    expect(wfLabelPlacement(input)).toBe(want);
  });

  it("outside-right label starts past the bar's painted (padding-floored) edge", () => {
    // A 0.1% bar on 800px is 0.8px by %, but CSS paints it 10px wide.
    expect(wfBarVisualPx(800, 0.1)).toBe(10);
    expect(wfOutsideRightLeft('10', '0.1')).toBe('calc(10% + max(0.1%, 10px) + 4px)');
  });
});
