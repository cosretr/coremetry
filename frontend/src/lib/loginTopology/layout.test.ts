import { describe, it, expect } from 'vitest';
import {
  mulberry32, seedOfIds, initLayout, layoutOf, LOGICAL_W, LOGICAL_H,
} from './layout';
import { syntheticGraph, SYNTHETIC_SEED } from './synthetic';
import {
  shouldDraw, FRAME_MIN_MS, nodeStyle, drift, flowPos, flowEdges, labelNodes, backdropMode, MAX_LABELS,
} from './frame';
import type { LoginSummaryEdge, LoginSummaryNode } from '@/lib/types';

const NODES: LoginSummaryNode[] = [
  { id: 'a1', tier: 'gw', weight: 1 }, { id: 'b1', tier: 'bff', weight: 0.6 },
  { id: 'c1', tier: 'svc', weight: 0.4 }, { id: 'c2', tier: 'svc', weight: 0.2 },
  { id: 'd1', tier: 'db', weight: 0.3 },
];
const EDGES: LoginSummaryEdge[] = [
  { from: 'a1', to: 'b1', weight: 0.9 }, { from: 'b1', to: 'c1', weight: 0.5 },
  { from: 'b1', to: 'c2', weight: 0.4 }, { from: 'c1', to: 'd1', weight: 0.3 },
  { from: 'zz', to: 'a1', weight: 1 }, // bilinmeyen uç: atılır
];

describe('PRNG ve tohum', () => {
  it('mulberry32 aynı tohum → aynı dizi, [0,1)', () => {
    const a = mulberry32(42), b = mulberry32(42), c = mulberry32(43);
    const xs = Array.from({ length: 50 }, () => a());
    expect(xs).toEqual(Array.from({ length: 50 }, () => b()));
    expect(xs).not.toEqual(Array.from({ length: 50 }, () => c()));
    expect(xs.every(v => v >= 0 && v < 1)).toBe(true);
  });
  it('seedOfIds kararlı ve sıra/içerik duyarlı', () => {
    expect(seedOfIds(['a', 'b'])).toBe(seedOfIds(['a', 'b']));
    expect(seedOfIds(['a', 'b'])).not.toBe(seedOfIds(['b', 'a']));
    expect(seedOfIds(['ab'])).not.toBe(seedOfIds(['a', 'b']));
  });
});

describe('yerleşim', () => {
  it('aynı tohum → aynı yerleşim; farklı tohum → farklı', () => {
    const a = layoutOf(NODES, EDGES, 7, 120), b = layoutOf(NODES, EDGES, 7, 120), c = layoutOf(NODES, EDGES, 8, 120);
    expect(a.nodes.map(n => [n.x, n.y])).toEqual(b.nodes.map(n => [n.x, n.y]));
    expect(a.nodes.map(n => [n.x, n.y])).not.toEqual(c.nodes.map(n => [n.x, n.y]));
    expect(a.nodePhase).toEqual(b.nodePhase);
  });
  it('bilinmeyen uçlu kenar atılır; derece hesaplanır', () => {
    const l = initLayout(NODES, EDGES, LOGICAL_W, LOGICAL_H, 1);
    expect(l.edges).toHaveLength(4);
    expect(l.nodes.find(n => n.id === 'b1')!.deg).toBe(3);
    expect(l.nodes.find(n => n.id === 'a1')!.deg).toBe(1);
  });
  it('düğümler sınır içinde, sayılar sonlu', () => {
    const l = layoutOf(NODES, EDGES, 3);
    for (const n of l.nodes) {
      expect(Number.isFinite(n.x) && Number.isFinite(n.y)).toBe(true);
      expect(n.x).toBeGreaterThanOrEqual(0); expect(n.x).toBeLessThanOrEqual(LOGICAL_W);
      expect(n.y).toBeGreaterThanOrEqual(0); expect(n.y).toBeLessThanOrEqual(LOGICAL_H);
    }
  });
  it('boş graf ve tek düğüm patlamaz', () => {
    expect(layoutOf([], [], 1).nodes).toEqual([]);
    expect(layoutOf([NODES[0]], [], 1).nodes).toHaveLength(1);
  });
  it('180 düğümde deterministik ve sınır içinde', () => {
    const nodes: LoginSummaryNode[] = Array.from({ length: 180 }, (_, i) => ({
      id: `n${i}`, tier: i < 4 ? 'gw' : i < 20 ? 'bff' : 'svc', weight: (i % 10) / 10,
    }));
    const edges: LoginSummaryEdge[] = nodes.slice(1).map((n, i) => ({ from: nodes[Math.floor(i / 3)].id, to: n.id, weight: 0.5 }));
    const a = layoutOf(nodes, edges, 99), b = layoutOf(nodes, edges, 99);
    expect(a.nodes.map(n => n.x)).toEqual(b.nodes.map(n => n.x));
    expect(a.nodes.every(n => n.x >= 0 && n.x <= LOGICAL_W && n.y >= 0 && n.y <= LOGICAL_H)).toBe(true);
  });
  it('itme: üst üste başlayan iki düğüm ayrılır', () => {
    const l = layoutOf(
      [{ id: 'p', tier: 'svc', weight: 0 }, { id: 'q', tier: 'svc', weight: 0 }], [], 5, 50);
    expect(Math.hypot(l.nodes[0].x - l.nodes[1].x, l.nodes[0].y - l.nodes[1].y)).toBeGreaterThan(20);
  });
});

describe('sentetik graf', () => {
  it('deterministik, makul boyut, adsız, geçerli kenarlar, dört kademe', () => {
    const a = syntheticGraph(), b = syntheticGraph(SYNTHETIC_SEED), c = syntheticGraph(1);
    expect(a).toEqual(b);
    expect(c.nodes.map(n => n.weight)).not.toEqual(a.nodes.map(n => n.weight));
    expect(a.nodes.length).toBeGreaterThan(80);
    expect(a.nodes.length).toBeLessThanOrEqual(180);
    expect(a.nodes.every(n => n.name === undefined)).toBe(true);
    const ids = new Set(a.nodes.map(n => n.id));
    expect(ids.size).toBe(a.nodes.length);
    expect(a.edges.every(e => ids.has(e.from) && ids.has(e.to) && e.from !== e.to)).toBe(true);
    expect(new Set(a.nodes.map(n => n.tier))).toEqual(new Set(['gw', 'bff', 'svc', 'db']));
    expect(a.nodes.every(n => n.weight >= 0 && n.weight <= 1)).toBe(true);
  });
  it('sentetik grafın yerleşimi de deterministik', () => {
    const g = syntheticGraph();
    const a = layoutOf(g.nodes, g.edges, SYNTHETIC_SEED), b = layoutOf(g.nodes, g.edges, SYNTHETIC_SEED);
    expect(a.nodes.map(n => n.x)).toEqual(b.nodes.map(n => n.x));
  });
});

describe('kare çekirdeği', () => {
  it('shouldDraw: 30 fps tavanı', () => {
    expect(shouldDraw(1000, 0)).toBe(true);
    expect(shouldDraw(1000 + FRAME_MIN_MS - 1, 1000)).toBe(false);
    expect(shouldDraw(1000 + FRAME_MIN_MS, 1000)).toBe(true);
    expect(FRAME_MIN_MS).toBeGreaterThanOrEqual(33);
  });
  it('düğüm stili aralıkları', () => {
    expect(nodeStyle({ tier: 'gw', weight: 0, deg: 5 }).r).toBe(9);
    expect(nodeStyle({ tier: 'gw', weight: 1, deg: 5 }).r).toBe(12);
    expect(nodeStyle({ tier: 'gw', weight: 1, deg: 5 }).ring).toBe(true);
    expect(nodeStyle({ tier: 'bff', weight: 0, deg: 4 }).r).toBe(5);
    expect(nodeStyle({ tier: 'bff', weight: 1, deg: 4 }).r).toBe(6);
    expect(nodeStyle({ tier: 'svc', weight: 1, deg: 3 }).r).toBe(4);
    expect(nodeStyle({ tier: 'svc', weight: 1, deg: 1 }).r).toBe(2.2);
    expect(nodeStyle({ tier: 'db', weight: 1, deg: 1 })).toMatchObject({ r: 6, square: true });
  });
  it('kayma ve akış: deterministik, sınırlı', () => {
    const l = layoutOf(NODES, EDGES, 2, 30);
    expect(drift(l, 1, 3.5)).toEqual(drift(l, 1, 3.5));
    expect(Math.abs(drift(l, 0, 1).dx)).toBeLessThanOrEqual(5);
    for (const t of [0, 1.7, 100]) { const p = flowPos(l, 0, t); expect(p).toBeGreaterThanOrEqual(0); expect(p).toBeLessThan(1); }
    expect(flowEdges(l, 2)).toHaveLength(2);
    expect(flowEdges(l, 2)[0]).toBe(0); // en ağır kenar
  });
  it('etiket: yalnız adı olanlar, giriş önce, tavan', () => {
    const named = NODES.map(n => ({ ...n, name: `svc-${n.id}` }));
    const l = layoutOf(named, EDGES, 2, 10);
    const order = labelNodes(l).map(i => l.nodes[i].tier);
    expect(order).toEqual(['gw', 'bff', 'svc', 'svc', 'db']);
    expect(labelNodes(layoutOf(NODES, EDGES, 2, 10))).toEqual([]);
    const many = Array.from({ length: 200 }, (_, i) => ({ id: `m${i}`, tier: 'svc' as const, weight: 0.1, name: `n${i}` }));
    expect(labelNodes(layoutOf(many, [], 1, 5))).toHaveLength(MAX_LABELS);
  });
});

describe('arka plan kipi', () => {
  const base = { setting: '' as const, wide: true, highContrast: false, reducedMotion: false, source: 'real' as const };
  it.each([
    ['gerçek veri, varsayılan ayar', {}, { kind: 'canvas', animated: true, synthetic: false }],
    ['anonymous', { setting: 'anonymous' as const }, { kind: 'canvas', animated: true, synthetic: false }],
    ['named', { setting: 'named' as const }, { kind: 'canvas', animated: true, synthetic: false }],
    ['off → canvas yok', { setting: 'off' as const }, { kind: 'none' }],
    ['dar ekran → yok', { wide: false }, { kind: 'none' }],
    ['Yüksek Kontrast → yok', { highContrast: true }, { kind: 'none' }],
    ['veri bekleniyor → yok', { source: 'pending' as const }, { kind: 'none' }],
    ['veri yok → sentetik', { source: 'synthetic' as const }, { kind: 'canvas', animated: true, synthetic: true }],
    ['reduced-motion → tek durağan kare', { reducedMotion: true }, { kind: 'canvas', animated: false, synthetic: false }],
    ['reduced-motion + sentetik', { reducedMotion: true, source: 'synthetic' as const }, { kind: 'canvas', animated: false, synthetic: true }],
    ['off önceliklidir (sentetik de çizilmez)', { setting: 'off' as const, source: 'synthetic' as const }, { kind: 'none' }],
  ])('%s', (_n, over, want) => {
    expect(backdropMode({ ...base, ...over })).toEqual(want);
  });
});
