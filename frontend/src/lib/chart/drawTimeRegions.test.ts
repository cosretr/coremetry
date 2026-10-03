// drawTimeRegions.test.ts — v0.10.168: çizim fonksiyonu sahte uPlot/canvas ile.
// v0.10.169: pencerenin ≥%90'ını kaplayan (kronik) bölge DOLGUSUZ — şerit +
// «· pencere boyu» etiketi kalır; kısa bölge dolgusunu korur.
// Sözleşme: etiket SOLA hizalı x1+4'te yazılır (uPlot eksen çiziminden sızan
// textAlign='right' etkisiz), şerit+etiket şerit numarası kadar aşağıda, dolgu
// birleşik aralıkla tek kat, sn bölgeleri xUnit=1000 ile ms eksenine oturur.
import { describe, it, expect } from 'vitest';
import type uPlot from 'uplot';
import { drawTimeRegions, isChronic, regionAt, regionLabelPlacement } from './overlays';

// Renkler LİTERAL: resolveVar yalnız `var(--x)` için DOM'a gider; node ortamında getComputedStyle yok.
const C = '#d29922';

type Call = { fn: string; args: unknown[]; textAlign: string; alpha: number };

function fakeU(xMinMs: number, xMaxMs: number) {
  const calls: Call[] = [];
  const bbox = { left: 100, top: 20, width: 1000, height: 300 };
  const ctx = {
    textAlign: 'right', textBaseline: 'middle', font: '', fillStyle: '', globalAlpha: 1,
    save() { calls.push({ fn: 'save', args: [], textAlign: this.textAlign, alpha: this.globalAlpha }); },
    restore() { calls.push({ fn: 'restore', args: [], textAlign: this.textAlign, alpha: this.globalAlpha }); },
    fillRect(...args: unknown[]) { calls.push({ fn: 'fillRect', args, textAlign: this.textAlign, alpha: this.globalAlpha }); },
    fillText(...args: unknown[]) { calls.push({ fn: 'fillText', args, textAlign: this.textAlign, alpha: this.globalAlpha }); },
    measureText(s: string) { return { width: s.length * 6 }; },
  };
  const u = {
    ctx, bbox,
    scales: { x: { min: xMinMs, max: xMaxMs } },
    valToPos: (v: number) => bbox.left + ((v - xMinMs) / (xMaxMs - xMinMs)) * bbox.width,
  } as unknown as uPlot;
  return { u, calls };
}

describe('drawTimeRegions (v0.10.168)', () => {
  const xMin = 1_700_000_000_000, xMax = 1_700_003_600_000; // 1 saat, ms
  it('etiket sola hizalı, x1+4\'te; sızan textAlign=right etkisiz', () => {
    const { u, calls } = fakeU(xMin, xMax);
    drawTimeRegions(u, [{ fromSec: 1_699_990_000, toSec: 1_700_010_000, label: 'trace_op ×175', color: C }], 1000);
    const t = calls.filter(c => c.fn === 'fillText');
    expect(t.length).toBe(1);
    expect(t[0].textAlign).toBe('left');
    expect(t[0].args[1]).toBe(100 + 4); // x1 = bbox.left (pencereye kırpılmış) + 4
  });
  it('üç tam-pencere (kronik) bölge: DOLGU YOK, şerit+etiket 3 satır, etikette «· pencere boyu»', () => {
    const { u, calls } = fakeU(xMin, xMax);
    const rg = [0, 1, 2].map(i => ({ fromSec: 1_699_000_000 + i, toSec: 1_700_010_000, label: `a${i}`, color: C }));
    drawTimeRegions(u, rg, 1000);
    const fills = calls.filter(c => c.fn === 'fillRect' && c.args[3] === 300);
    expect(fills.length).toBe(0);
    const strips = calls.filter(c => c.fn === 'fillRect' && c.args[3] === 3);
    expect(strips.map(s => s.args[1])).toEqual([20, 34, 48]);
    const texts = calls.filter(c => c.fn === 'fillText');
    expect(texts.map(c => c.args[2])).toEqual([33, 47, 61]);
    expect(texts[0].args[0]).toBe('▮ a0 · pencere boyu');
  });
  it('iki çakışan kısa bölge → dolgu TEK kat; kronik + kısa karışımında yalnız kısa dolgulu', () => {
    const { u, calls } = fakeU(xMin, xMax);
    drawTimeRegions(u, [
      { fromSec: 1_700_000_600, toSec: 1_700_001_200, label: 'k1', color: C },
      { fromSec: 1_700_000_900, toSec: 1_700_001_500, label: 'k2', color: C },
    ], 1000);
    expect(calls.filter(c => c.fn === 'fillRect' && c.args[3] === 300).length).toBe(1);
    const m = fakeU(xMin, xMax);
    drawTimeRegions(m.u, [
      { fromSec: 1_699_000_000, toSec: 1_700_010_000, label: 'kronik', color: C },
      { fromSec: 1_700_000_600, toSec: 1_700_000_900, label: 'kısa', color: C },
    ], 1000);
    const fills = m.calls.filter(c => c.fn === 'fillRect' && c.args[3] === 300);
    expect(fills.length).toBe(1);
    expect(fills[0].args[0]).toBeCloseTo(100 + (600 / 3600) * 1000, 3); // kısa bölgenin x1'i
    const labels = m.calls.filter(c => c.fn === 'fillText').map(c => String(c.args[0]));
    expect(labels).toEqual(['▮ kronik · pencere boyu', '▮ kısa']);
  });
  it('isChronic: %90 eşiği; sıfır pencere → false', () => {
    expect(isChronic(0, 90, 0, 100)).toBe(true);
    expect(isChronic(0, 89, 0, 100)).toBe(false);
    expect(isChronic(0, 100, 50, 50)).toBe(false);
  });
  it('pencere dışı bölge hiç çizilmez; xUnit=1 (saniye motoru) aynı davranır', () => {
    const { u, calls } = fakeU(xMin, xMax);
    drawTimeRegions(u, [{ fromSec: 1_600_000_000, toSec: 1_600_000_100, label: 'eski', color: C }], 1000);
    expect(calls.filter(c => c.fn !== 'save' && c.fn !== 'restore').length).toBe(0);
    const s = fakeU(1_700_000_000, 1_700_003_600);
    drawTimeRegions(s.u, [{ fromSec: 1_700_000_100, toSec: 1_700_000_200, label: 'x', color: C }]);
    expect(s.calls.filter(c => c.fn === 'fillText').length).toBe(1);
  });
});

// v0.10.180 — regionAt: CSS px (u.over uzayı) → şerit satırındaki bölge.
describe('regionAt (v0.10.180)', () => {
  const xMin = 1_700_000_000_000, xMax = 1_700_003_600_000;
  const u = { scales: { x: { min: xMin, max: xMax } }, valToPos: (v: number) => ((v - xMin) / (xMax - xMin)) * 1000 } as unknown as uPlot;
  const rg = [
    { id: 'a', fromSec: 1_700_000_600, toSec: 1_700_001_200, label: 'a', color: C },   // px 166..333, lane 0
    { id: 'b', fromSec: 1_700_000_900, toSec: 1_700_001_500, label: 'b', color: C },   // px 250..416, lane 1
    { id: 'old', fromSec: 1_600_000_000, toSec: 1_600_000_100, label: 'old', color: C }, // pencere dışı
  ];
  it('şerit satırına göre bölge; alt satırda ikinci; seri alanında null; pencere dışı asla', () => {
    expect(regionAt(u, rg, 1000, 200, 4)?.id).toBe('a');
    expect(regionAt(u, rg, 1000, 300, 4)?.id).toBe('a');
    expect(regionAt(u, rg, 1000, 300, 16)?.id).toBe('b');
    expect(regionAt(u, rg, 1000, 300, 60)).toBeNull();
    expect(regionAt(u, rg, 1000, 5, 4)).toBeNull();
    expect(regionAt(u, undefined, 1000, 200, 4)).toBeNull();
  });
});

// v0.10.1077 — 2 dk'lık açık problemde "başladı" bandı sağ kenarda ince
// şerit; etiket bant içine sığmayınca bandın SOLUNA, başa sağdan hizalı.
describe('regionLabelPlacement (v0.10.1077)', () => {
  it.each([
    ['geniş bant → içeride', { labelW: 54, x1: 100, x2: 400, floorX: 0, pad: 4 }, 'inside'],
    ['tam sığan (2×pad dahil) → içeride', { labelW: 54, x1: 100, x2: 162, floorX: 0, pad: 4 }, 'inside'],
    ['dar bant, solda yer var → solda', { labelW: 54, x1: 1066, x2: 1100, floorX: 100, pad: 4 }, 'left'],
    ['dar bant, solda tam yer (sınır dahil) → solda', { labelW: 54, x1: 162, x2: 170, floorX: 104, pad: 4 }, 'left'],
    ['dar bant, çizim alanının sol kenarında → içeride (kısaltılır)', { labelW: 54, x1: 120, x2: 130, floorX: 100, pad: 4 }, 'inside'],
    ['dar bant, soldaki komşu bant engelliyor → içeride', { labelW: 54, x1: 1066, x2: 1100, floorX: 1040, pad: 4 }, 'inside'],
  ] as const)('%s', (_n, o, want) => { expect(regionLabelPlacement(o)).toBe(want); });

  const xMin = 1_700_000_000_000, xMax = 1_700_003_600_000; // 1 sa, ms
  const endSec = xMax / 1000;
  it('2 dk\'lık bant sağ kenarda: etiket bandın solunda, sağ ucu x1−4\'te; kısaltılmamış', () => {
    const { u, calls } = fakeU(xMin, xMax);
    drawTimeRegions(u, [{ fromSec: endSec - 120, toSec: endSec, label: 'başladı', color: C }], 1000);
    const t = calls.filter(c => c.fn === 'fillText');
    expect(t.length).toBe(1);
    expect(t[0].args[0]).toBe('▮ başladı');
    const x1 = 100 + (3480 / 3600) * 1000;
    expect(t[0].args[1]).toBeCloseTo(x1 - 4 - '▮ başladı'.length * 6, 6);
    expect(t[0].textAlign).toBe('left');
    // Şerit yine bandın kendisinde (renk/konum değişmedi).
    const strip = calls.find(c => c.fn === 'fillRect' && c.args[3] === 3);
    expect(strip?.args[0]).toBeCloseTo(x1, 6);
  });
  it('aynı şeritte solda bitişik komşu varsa sola taşmaz — içeride, x1+4 (eski yol)', () => {
    const { u, calls } = fakeU(xMin, xMax);
    drawTimeRegions(u, [
      { fromSec: endSec - 600, toSec: endSec - 125, label: 'önce', color: C },
      { fromSec: endSec - 120, toSec: endSec, label: 'başladı', color: C },
    ], 1000);
    const t = calls.filter(c => c.fn === 'fillText');
    const x1 = 100 + (3480 / 3600) * 1000;
    const late = t.find(c => String(c.args[0]).startsWith('▮ b'));
    expect(late?.args[1]).toBeCloseTo(x1 + 4, 6);
    expect(String(late?.args[0])).toMatch(/…$/);
  });
});
