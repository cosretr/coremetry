import { describe, it, expect } from 'vitest';
import {
  mulberry32, seedOfIds, initLayout, layoutOf, LOGICAL_W, LOGICAL_H,
} from './layout';
import { syntheticGraph, SYNTHETIC_SEED } from './synthetic';
import {
  shouldDraw, FRAME_MIN_MS, nodeStyle, drift, flowPos, flowEdges, labelNodes, backdropMode, MAX_LABELS,
  thinGraph, screenPositions, MAX_DRAW_NODES,
} from './frame';
import { beginDrag, dragTo, hitNode, FOLLOW_1, FOLLOW_2 } from './drag';
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

// v0.10.1914 (operatör-raporlu: "noktalar bir yere öbeklenmiş") — eski
// yerleşimde düğümlerin %45–100'ü kenar bandına yığılıyordu (sınırsız itme +
// sert kırpma). Dört graf şeklinde: kenar bandı yığılmaz, 4×3 ızgaranın her
// hücresi dolu ve hiçbiri baskın değil, bağlı grafta kenarlar rastgele çiftlerden kısa.
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

function spread(g: { nodes: LoginSummaryNode[]; edges: LoginSummaryEdge[] }) {
  const lay = layoutOf(g.nodes, g.edges, 12345);
  const n = lay.nodes.length, band = 0.04;
  let border = 0;
  const grid = new Array<number>(12).fill(0);
  for (const p of lay.nodes) {
    if (p.x < LOGICAL_W * band || p.x > LOGICAL_W * (1 - band) || p.y < LOGICAL_H * band || p.y > LOGICAL_H * (1 - band)) border++;
    grid[Math.min(3, Math.floor((p.x / LOGICAL_W) * 4)) + 4 * Math.min(2, Math.floor((p.y / LOGICAL_H) * 3))]++;
  }
  let edgeSum = 0;
  for (const e of lay.edges) edgeSum += Math.hypot(lay.nodes[e.a].x - lay.nodes[e.b].x, lay.nodes[e.a].y - lay.nodes[e.b].y);
  let pairSum = 0, pairs = 0;
  for (let i = 0; i < n; i++) for (let j = i + 1; j < n; j++) {
    pairSum += Math.hypot(lay.nodes[i].x - lay.nodes[j].x, lay.nodes[i].y - lay.nodes[j].y); pairs++;
  }
  return {
    border: border / n, minCell: Math.min(...grid) / n, maxCell: Math.max(...grid) / n,
    edgeRatio: lay.edges.length ? (edgeSum / lay.edges.length) / (pairSum / pairs) : 0,
  };
}

// v0.10.1917 (operatör: "ekrana eşit dağılsın"; ekran görüntüsünde kartın
// çevresinde öbek) — evenOut sonrası her 4×3 hücre ≈ eşit pay (düzgün = %8.3).
describe('yerleşim yayılımı (ekrana eşit; kenara / ortaya yığılma yok)', () => {
  const cases: Array<[string, { nodes: LoginSummaryNode[]; edges: LoginSummaryEdge[] }, boolean]> = [
    ['sentetik (bağlı, hiyerarşik)', syntheticGraph(), true],
    ['seyrek: 150 düğüm, 60 kenar', sparseGraph(150, 60, 7), true],
    ['yoğun: 135 düğüm, 200 kenar', sparseGraph(135, 200, 9), true],
    ['bağlantısız: 180 düğüm', sparseGraph(180, 0, 3), false],
  ];
  it.each(cases)('%s', (_name, g, connected) => {
    const m = spread(g);
    expect(m.border).toBeLessThanOrEqual(0.06);      // kenar bandına yığılma yok
    expect(m.minCell).toBeGreaterThanOrEqual(0.05);  // boş bölge yok (düzgün ≈ %8.3)
    expect(m.maxCell).toBeLessThanOrEqual(0.12);     // öbek yok
    if (connected) expect(m.edgeRatio).toBeLessThan(0.6); // yapı korunur: komşular yakın
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
  // v0.10.1918 (operatör: "isimler de random gelsin").
  it('etiket rastgele: yalnız adı olanlardan, tavan kadar, tekrarsız; farklı kaynak farklı küme', () => {
    const many = Array.from({ length: 200 }, (_, i) => ({ id: `m${i}`, tier: (i < 5 ? 'gw' : 'svc') as 'gw' | 'svc', weight: 0.1, name: i % 2 ? `n${i}` : undefined }));
    const l = layoutOf(many, [], 1, 5);
    const a = labelNodes(l, MAX_LABELS, mulberry32(7));
    const b = labelNodes(l, MAX_LABELS, mulberry32(8));
    expect(a).toHaveLength(MAX_LABELS);
    expect(new Set(a).size).toBe(a.length);
    expect(a.every(i => !!l.nodes[i].name)).toBe(true);
    expect(a).toEqual([...a].sort((x, y) => x - y));
    expect(a).not.toEqual(b);
    expect(labelNodes(l, MAX_LABELS, mulberry32(7))).toEqual(a); // aynı kaynak → aynı küme
    expect(labelNodes(layoutOf(NODES, EDGES, 2, 10), MAX_LABELS, Math.random)).toEqual([]); // ad yoksa boş
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

// v0.10.1917 (operatör: "çok dolu gözükmesin") — çizilen düğüm seyreltmesi.
describe('seyreltme (thinGraph)', () => {
  it('tavan altında aynen; üstünde giriş/BFF/veri önce, servis ağırlığa göre; kenarlar iki ucu kalanlar', () => {
    const small = thinGraph(NODES, EDGES);
    expect(small.nodes).toHaveLength(NODES.length);
    const g = sparseGraph(260, 200, 4); // v0.10.1927: tavan 180, sentetik graf (160) altında kalır
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

// v0.10.1917 (operatör: "noktalar mouse ile tutularak hareket etsin").
describe('sürükleme çekirdeği', () => {
  const lay = () => layoutOf(NODES, EDGES, 3, 50);
  it('isabet: en yakın düğüm, tolerans içinde; uzakta -1', () => {
    const l = lay();
    const pos = screenPositions(l, 0, 1, 1);
    expect(hitNode(l, pos, pos[2].x + 3, pos[2].y - 2)).toBe(2);
    expect(hitNode(l, pos, -500, -500)).toBe(-1);
  });
  it('tutulan düğüm hedefe gider; 1 adım komşu 2 adımdan çok, ilgisiz düğüm hiç kıpırdamaz', () => {
    const l = layoutOf([...NODES, { id: 'z9', tier: 'svc', weight: 0.1 }], EDGES, 3, 50);
    const before = l.nodes.map(n => ({ x: n.x, y: n.y }));
    const d = beginDrag(l, 1); // b1: komşular a1, c1, c2 (1 adım), d1 (2 adım)
    expect(d.follow.filter(f => f.k === FOLLOW_1).map(f => f.j).sort()).toEqual([0, 2, 3]);
    expect(d.follow.filter(f => f.k === FOLLOW_2).map(f => f.j)).toEqual([4]);
    const tx = before[1].x + 200, ty = before[1].y + 100;
    dragTo(l, d, tx, ty);
    expect(l.nodes[1].x).toBeCloseTo(Math.min(tx, l.w - 24));
    const moved = (i: number) => Math.hypot(l.nodes[i].x - before[i].x, l.nodes[i].y - before[i].y);
    expect(moved(2)).toBeGreaterThan(moved(4));
    expect(moved(4)).toBeGreaterThan(0);
    expect(moved(5)).toBe(0); // z9 bağlantısız
  });
  it('tekrarlı adımlarda komşu göreli konumuna yakınsar; konumlar çerçeve içinde kalır', () => {
    const l = lay();
    const d = beginDrag(l, 0);
    const rel = d.follow.find(f => f.j === 1)!;
    for (let i = 0; i < 60; i++) dragTo(l, d, 700, 400);
    expect(l.nodes[1].x - l.nodes[0].x).toBeCloseTo(rel.rx, 0);
    for (let i = 0; i < 60; i++) dragTo(l, d, -9999, 99999);
    for (const n of l.nodes) {
      expect(n.x).toBeGreaterThanOrEqual(24); expect(n.x).toBeLessThanOrEqual(l.w - 24);
      expect(n.y).toBeGreaterThanOrEqual(24); expect(n.y).toBeLessThanOrEqual(l.h - 24);
    }
  });
  it('geçersiz hedef yok sayılır', () => {
    const l = lay();
    const x = l.nodes[0].x;
    dragTo(l, beginDrag(l, 0), NaN, 10);
    expect(l.nodes[0].x).toBe(x);
  });
});

