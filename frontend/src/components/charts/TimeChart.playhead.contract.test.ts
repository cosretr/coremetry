// TimeChart.playhead.contract — v0.10.1846 (ADDITIVE prop, öğrenme döngüsü
// Faz 2 "olayı oynat"). Sözleşme: (1) imleç geometrisi saf ve alan dışında
// hiçbir şey çizmez; (2) `playhead` yapı imzasına GİRMEZ (her kare uPlot'u
// yeniden kurardı) — değişimi yalnız `redraw(false)` tetikler; (3) imleç
// çizimi bölge gölgesinden SONRA, deploy erken dönüşünden ÖNCE (deploy
// işareti olmayan grafikte de çizilir).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { playheadGeometry } from '@/lib/chart/overlays';
import { stripTsComments } from '@/styles/zLayers.test';

const lin = (v: number) => (v - 1000) / 10 + 50; // 1000 sn → 50 px, 10 sn/px
const bbox = { left: 50, width: 200 };

describe('playheadGeometry', () => {
  it('cursor + elapsed fill, clamped to the plot box', () => {
    expect(playheadGeometry({ cursorSec: 2000, fromSec: 1500 }, lin, bbox)).toEqual({ x: 150, fillX: 100, fillW: 50 });
    expect(playheadGeometry({ cursorSec: 2000, fromSec: 0 }, lin, bbox)).toEqual({ x: 150, fillX: 50, fillW: 100 });
    expect(playheadGeometry({ cursorSec: 2000 }, lin, bbox)).toEqual({ x: 150 });
    expect(playheadGeometry({ cursorSec: 1500, fromSec: 2000 }, lin, bbox)).toEqual({ x: 100 });
  });
  it('outside the plot draws nothing', () => {
    expect(playheadGeometry({ cursorSec: 99999 }, lin, bbox)).toBeNull();
    expect(playheadGeometry({ cursorSec: Number.NaN }, lin, bbox)).toBeNull();
  });
});

describe('TimeChart playhead wiring (source contract)', () => {
  const src = stripTsComments(readFileSync(resolve(__dirname, 'TimeChart.tsx'), 'utf8'));
  it('playhead is NOT part of the build signature', () => {
    const i = src.indexOf('timeChartBuildSignature({');
    const sig = src.slice(i, src.indexOf('});', i));
    expect(i).toBeGreaterThan(0);
    expect(sig).not.toMatch(/playhead/);
  });
  it('a playhead change only redraws', () => {
    expect(src).toMatch(/useEffect\(\(\) => \{ plotRef\.current\?\.redraw\(false\); \}, \[plotRef, playhead\?\.cursorSec, playhead\?\.fromSec\]\)/);
  });
  it('drawn after regions and before the deploy early-return', () => {
    const r = src.indexOf('drawTimeRegions(u, regions)');
    const p = src.indexOf('drawPlayhead(u, playheadRef.current)');
    const d = src.indexOf('if (!deployMarkers?.length) return;');
    expect(r).toBeGreaterThan(0);
    expect(p).toBeGreaterThan(r);
    expect(d).toBeGreaterThan(p);
  });
});
