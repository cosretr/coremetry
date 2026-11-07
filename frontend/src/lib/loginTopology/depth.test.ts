// depth.test — v0.10.1919 (operatör: "derinlik olsun, gerçekten güzel bir deneyim olsun").
import { describe, it, expect } from 'vitest';
import { layoutOf, mulberry32 } from './layout';
import { syntheticGraph } from './synthetic';
import { thinGraph, screenPositions, labelNodes, MAX_LABELS } from './frame';
import { hitNode } from './drag';
import {
  DEPTH_STYLE, depthLayers, drawOrder, edgeDepth, fadeIn, FADE_IN_MS, FRONT_SHARE, MID_SHARE, type Depth,
} from './depth';

const synth = () => { const g = thinGraph(syntheticGraph().nodes, syntheticGraph().edges); return layoutOf(g.nodes, g.edges, 7); };

describe('derinlik katmanları', () => {
  it('payları: ön %30, orta %35, kalan arka; kararlı', () => {
    const l = synth();
    const d = depthLayers(l);
    const n = l.nodes.length;
    expect(d.filter(x => x === 0)).toHaveLength(Math.round(n * FRONT_SHARE));
    expect(d.filter(x => x === 1)).toHaveLength(Math.round(n * MID_SHARE));
    expect(depthLayers(l)).toEqual(d);
  });
  it('önem sırası: giriş ve BFF önde, en derin katman yalnız servislerden', () => {
    const l = synth();
    const d = depthLayers(l);
    l.nodes.forEach((nd, i) => {
      if (nd.tier === 'gw' || nd.tier === 'bff') expect(d[i]).toBe(0);
      if (d[i] === 2) expect(nd.tier).toBe('svc');
    });
  });
  it('stil: ön → arka küçülür, solar, yavaşlar', () => {
    for (const k of ['scale', 'nodeAlpha', 'edgeAlpha', 'driftMul', 'speedMul'] as const) {
      expect(DEPTH_STYLE[0][k]).toBeGreaterThan(DEPTH_STYLE[1][k]);
      expect(DEPTH_STYLE[1][k]).toBeGreaterThan(DEPTH_STYLE[2][k]);
    }
  });
  it('kenar daha derin ucun katmanında; çizim sırası arkadan öne', () => {
    const d: Depth[] = [0, 2, 1, 0];
    expect(edgeDepth(d, 0, 1)).toBe(2);
    expect(edgeDepth(d, 0, 3)).toBe(0);
    expect(drawOrder(d)).toEqual([1, 2, 0, 3]);
  });
  it('açılış: 0 → 1 yumuşak; durağan kipte hep 1', () => {
    expect(fadeIn(0, true)).toBe(0);
    expect(fadeIn(FADE_IN_MS / 2, true)).toBeGreaterThan(0.5); // ease-out: yarıda yarıdan fazla
    expect(fadeIn(FADE_IN_MS * 2, true)).toBe(1);
    expect(fadeIn(0, false)).toBe(1);
  });
});

describe('derinlikle konum, isabet ve etiket', () => {
  it('arkadaki düğüm öndekinden az salınır', () => {
    const l = synth();
    const d = depthLayers(l);
    const motion = (i: number) => ({ amp: DEPTH_STYLE[d[i]].driftMul, speed: DEPTH_STYLE[d[i]].speedMul });
    const base = screenPositions(l, 0, 1, 1, motion);
    const maxMove = (layer: Depth) => {
      let m = 0;
      for (let t = 0.5; t < 30; t += 0.5) {
        const p = screenPositions(l, t, 1, 1, motion);
        l.nodes.forEach((_, i) => { if (d[i] === layer) m = Math.max(m, Math.hypot(p[i].x - base[i].x, p[i].y - base[i].y)); });
      }
      return m;
    };
    expect(maxMove(2)).toBeLessThan(maxMove(0));
    expect(screenPositions(l, 3, 1, 1)).toEqual(screenPositions(l, 3, 1, 1, () => ({ amp: 1, speed: 1 })));
  });
  it('çakışmada öndeki tutulur', () => {
    const l = synth();
    const pos = l.nodes.map(() => ({ x: 100, y: 100 }));
    const d: Depth[] = l.nodes.map((_, i) => (i === 5 ? 0 : 2));
    expect(hitNode(l, pos, 100, 100, undefined, d)).toBe(5);
  });
  it('etiket yalnız ön katmandan, rastgele', () => {
    const named = syntheticGraph();
    const nodes = named.nodes.map((n, i) => ({ ...n, name: `svc-${i}` }));
    const g = thinGraph(nodes, named.edges);
    const l = layoutOf(g.nodes, g.edges, 7);
    const d = depthLayers(l);
    const lab = labelNodes(l, MAX_LABELS, mulberry32(3), i => d[i] === 0);
    expect(lab.length).toBeGreaterThan(0);
    expect(lab.length).toBeLessThanOrEqual(MAX_LABELS);
    expect(lab.every(i => d[i] === 0)).toBe(true);
  });
});
