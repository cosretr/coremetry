// frame.test — v0.10.1928: login arka planının galaksiden bağımsız çekirdekleri
// (PRNG/tohum, sentetik graf, kare kapısı, kip kararı, seyreltme). Önceki
// layout.test.ts'ten taşındı; kuvvet yerleşimi / derinlik / nokta sürükleme
// testleri galaksiyle (galaxy.test.ts) değişti.
import { describe, it, expect } from 'vitest';
import { mulberry32, seedOfIds } from './prng';
import { syntheticGraph, SYNTHETIC_SEED } from './synthetic';
import { shouldDraw, FRAME_MIN_MS, backdropMode, thinGraph, MAX_DRAW_NODES } from './frame';
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

function sparseGraph(n: number, e: number, seed: number) {
  const r = mulberry32(seed);
  const nodes: LoginSummaryNode[] = Array.from({ length: n }, (_, i) => ({
    id: `n${i}`, tier: i < 6 ? 'gw' : i < 20 ? 'bff' : 'svc', weight: r(),
  }));
  const edges: LoginSummaryEdge[] = [];
  for (let i = 0; i < e; i++) {
    edges.push({ from: `n${Math.floor(r() * 20)}`, to: `n${20 + Math.floor(r() * (n - 20))}`, weight: r() });
  }
  return { nodes, edges };
}

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
    expect(new Set(a.nodes.map(n => n.tier))).toEqual(new Set(['gw', 'bff', 'svc', 'db', 'queue']));
    expect(a.nodes.every(n => n.weight >= 0 && n.weight <= 1)).toBe(true);
  });
});

describe('kare kapısı', () => {
  it('shouldDraw: 30 fps tavanı', () => {
    expect(shouldDraw(1000, 0)).toBe(true);
    expect(shouldDraw(1000 + FRAME_MIN_MS - 1, 1000)).toBe(false);
    expect(shouldDraw(1000 + FRAME_MIN_MS, 1000)).toBe(true);
    expect(FRAME_MIN_MS).toBeGreaterThanOrEqual(33);
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

describe('seyreltme (thinGraph)', () => {
  it('tavan altında aynen; üstünde giriş/BFF/veri önce, servis ağırlığa göre; kenarlar iki ucu kalanlar', () => {
    const small = thinGraph(NODES, EDGES);
    expect(small.nodes).toHaveLength(NODES.length);
    const g = sparseGraph(520, 400, 4); // v0.10.1934: tavan 360, sentetik graf (160) altında kalır
    expect(g.nodes.length).toBeGreaterThan(MAX_DRAW_NODES);
    const t = thinGraph(g.nodes, g.edges);
    expect(t.nodes).toHaveLength(MAX_DRAW_NODES);
    for (const tier of ['gw', 'bff', 'db'] as const) {
      expect(t.nodes.filter(n => n.tier === tier)).toHaveLength(g.nodes.filter(n => n.tier === tier).length);
    }
    const kept = new Set(t.nodes.map(n => n.id));
    expect(t.edges.every(e => kept.has(e.from) && kept.has(e.to))).toBe(true);
    const order = g.nodes.filter(n => kept.has(n.id)).map(n => n.id);
    expect(t.nodes.map(n => n.id)).toEqual(order); // girdi sırası korunur
    const minKeptSvc = Math.min(...t.nodes.filter(n => n.tier === 'svc').map(n => n.weight));
    const maxDroppedSvc = Math.max(...g.nodes.filter(n => n.tier === 'svc' && !kept.has(n.id)).map(n => n.weight));
    expect(minKeptSvc).toBeGreaterThanOrEqual(maxDroppedSvc);
  });
});
