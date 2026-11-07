// galaxy.test — v0.10.1928 (operatör: "veriye bağlı galaksi en iyisi").
import { describe, it, expect } from 'vitest';
import { mulberry32 } from './prng';
import { syntheticGraph, SYNTHETIC_SEED } from './synthetic';
import {
  buildGalaxy, depthOf, flowPos, flowSpeed, isDarkColor, nodeAlpha, nodeRadius, paceFrom, pickBackendLabels, pickLabelSet, project,
  sizeOf, spinRate, wantsLabel, DENSE_NODES, DENSE_SIZE_K, MIN_ARMS, TILT0, DUST_ARM, DUST_CORE, FLOW_EDGES, type GNode,
} from './galaxy';
import type { LoginSummaryEdge, LoginSummaryNode } from '@/lib/types';

const synth = () => { const g = syntheticGraph(SYNTHETIC_SEED); return buildGalaxy(g.nodes, g.edges, 7); };
const named = () => {
  const g = syntheticGraph(SYNTHETIC_SEED);
  return buildGalaxy(g.nodes.map((n, i) => ({ ...n, name: `svc-${i}` })), g.edges, 7);
};

describe('galaksi kurulumu', () => {
  it('aynı tohum → aynı galaksi; farklı tohum → farklı', () => {
    const g = syntheticGraph(SYNTHETIC_SEED);
    const a = buildGalaxy(g.nodes, g.edges, 7), b = buildGalaxy(g.nodes, g.edges, 7), c = buildGalaxy(g.nodes, g.edges, 8);
    expect(a.nodes.map(n => [n.r, n.th])).toEqual(b.nodes.map(n => [n.r, n.th]));
    expect(c.nodes.map(n => n.th)).not.toEqual(a.nodes.map(n => n.th));
  });
  it('her gateway bir kol; BFF ve servisler bağlı oldukları gateway\'in kolunda', () => {
    const gx = synth();
    const gws = gx.nodes.map((n, i) => ({ n, i })).filter(x => x.n.tier === 'gw');
    expect(gx.arms).toBe(gws.length);
    expect(new Set(gws.map(x => x.n.arm)).size).toBe(gws.length);
    // gateway → BFF kenarında BFF, gateway'in kolunda
    for (const e of gx.edges) {
      const a = gx.nodes[e.a], b = gx.nodes[e.b];
      if (a.tier === 'gw' && b.tier === 'bff') expect(b.arm).toBe(a.arm);
    }
  });
  it('gateway çekirdeğe yakın, BFF orta, servis ve veri dışta', () => {
    const gx = synth();
    const mean = (t: string) => { const xs = gx.nodes.filter(n => n.tier === t).map(n => n.r); return xs.reduce((a, b) => a + b, 0) / xs.length; };
    expect(mean('gw')).toBeLessThan(mean('bff'));
    expect(mean('bff')).toBeLessThan(mean('svc'));
    expect(mean('bff')).toBeLessThan(mean('db'));
    expect(gx.nodes.every(n => n.r > 0 && n.r <= 1.01 && Number.isFinite(n.th))).toBe(true);
  });
  it('gateway yoksa en ağır düğümler kol başı olur (en az MIN_ARMS)', () => {
    const nodes: LoginSummaryNode[] = Array.from({ length: 30 }, (_, i) => ({ id: `s${i}`, tier: 'svc', weight: i / 30 }));
    const gx = buildGalaxy(nodes, [], 3);
    expect(gx.arms).toBe(MIN_ARMS);
  });
  it('boş graf, bilinmeyen uçlu kenar ve öz-döngü patlamaz', () => {
    expect(buildGalaxy([], [], 1).nodes).toEqual([]);
    const nodes: LoginSummaryNode[] = [{ id: 'a', tier: 'gw', weight: 1 }, { id: 'b', tier: 'svc', weight: 0.2 }];
    const edges: LoginSummaryEdge[] = [{ from: 'a', to: 'b', weight: 0.5 }, { from: 'zz', to: 'a', weight: 1 }, { from: 'a', to: 'a', weight: 1 }];
    expect(buildGalaxy(nodes, edges, 1).edges).toHaveLength(1);
  });
  it('toz: kol + çekirdek parçacıkları; akan nokta kenarları en ağırlar', () => {
    const gx = synth();
    expect(gx.dust.length).toBe((DUST_ARM + DUST_CORE) * 5);
    expect(gx.flows.length).toBeLessThanOrEqual(FLOW_EDGES);
    const ws = gx.flows.map(i => gx.edges[i].w);
    expect([...ws].sort((a, b) => b - a)).toEqual(ws);
  });
});

// v0.10.1928 (operatör: "gezegenlerin bir kısmı daha küçük, bir kısmı daha büyük").
describe('boyut ve parlaklık = trafik', () => {
  it('boyut geniş aralıkta: sakin servis küçük, yoğun servis büyük', () => {
    expect(sizeOf(0, 0.5)).toBeCloseTo(0.45, 2);
    expect(sizeOf(1, 0.5)).toBeCloseTo(1.95, 2);
    expect(sizeOf(1, 0.5) / sizeOf(0, 0.5)).toBeGreaterThan(4);
    const gx = synth();
    const sizes = gx.nodes.map(n => n.size);
    expect(Math.max(...sizes) / Math.min(...sizes)).toBeGreaterThan(3);
  });
  it('öndeki büyük ve parlak, odak tam opak ve büyür', () => {
    const nd = { tier: 'svc', weight: 0.5, size: 1 } as GNode;
    expect(nodeRadius(nd, 1, false)).toBeGreaterThan(nodeRadius(nd, 0, false));
    expect(nodeAlpha(nd, 1, false)).toBeGreaterThan(nodeAlpha(nd, 0, false));
    expect(nodeAlpha(nd, 0, true)).toBe(1);
    expect(nodeRadius(nd, 0.5, true)).toBeGreaterThan(nodeRadius(nd, 0.5, false));
  });
});

describe('izdüşüm ve tempo', () => {
  it('dönüş bütün galaksiyi birlikte döndürür; eğim derinlik verir', () => {
    const v = { angle: 0, tilt: TILT0, cx: 500, cy: 300, R: 400 };
    const a = project(v, 0.5, 0, 0), b = project({ ...v, angle: Math.PI / 2 }, 0.5, 0, 0);
    expect(Math.hypot(a.x - b.x, a.y - b.y)).toBeGreaterThan(10);
    const front = project(v, 0.5, Math.PI / 2, 0), back = project(v, 0.5, -Math.PI / 2, 0);
    expect(depthOf(front.z)).toBeGreaterThan(depthOf(back.z));
    expect(project(v, 0, 0, 0)).toMatchObject({ x: 500, y: 300 });
  });
  it('tempo = son / 24 sa tepe; veri yoksa orta; dönüş ve akış tempoyla hızlanır', () => {
    expect(paceFrom([10, 20, 5])).toBeCloseTo(0.25);
    expect(paceFrom([10, 20])).toBe(1);
    expect(paceFrom(undefined)).toBe(0.6);
    expect(paceFrom([7])).toBe(0.6);
    expect(paceFrom([0, 0])).toBe(0.6);
    expect(spinRate(1)).toBeGreaterThan(spinRate(0));
    expect(flowSpeed(0.5, 1)).toBeGreaterThan(flowSpeed(0.5, 0));
    const p = flowPos(0.9, 3, 0.5, 0.6);
    expect(p).toBeGreaterThanOrEqual(0); expect(p).toBeLessThan(1);
  });
  it('zemin koyuluğu (parıltı gücü)', () => {
    expect(isDarkColor('#0f1117')).toBe(true);
    expect(isDarkColor('#f2f3f5')).toBe(false);
    expect(isDarkColor('rgb(255, 255, 255)')).toBe(false);
    expect(isDarkColor('#123')).toBe(true);
    expect(isDarkColor('garip')).toBe(true);
  });
});

describe('etiketler', () => {
  it('veri katmanı yalnız kümedeyse ve önde etiket alır (v0.10.1931); adı olmayan hiç', () => {
    const db = { tier: 'db', name: 'ordersdb1', weight: 1 } as GNode;
    const q = { tier: 'queue', name: 'orders.events', weight: 1 } as GNode;
    expect(wantsLabel(db, 1, true)).toBe(true);
    expect(wantsLabel(db, 1, false)).toBe(false);
    expect(wantsLabel(q, 0.6, true)).toBe(true);
    expect(wantsLabel(q, 0.3, true)).toBe(false);
    expect(wantsLabel({ tier: 'gw', weight: 1 } as GNode, 1, true)).toBe(false);
  });
  it('gateway önde, BFF yakın önde, servis yalnız rastgele kümede ve önde', () => {
    const gw = { tier: 'gw', name: 'api-gateway', weight: 1 } as GNode;
    const bff = { tier: 'bff', name: 'bff-web', weight: 1 } as GNode;
    const svc = { tier: 'svc', name: 'orders-api', weight: 1 } as GNode;
    expect(wantsLabel(gw, 0.5, false)).toBe(true);
    expect(wantsLabel(gw, 0.2, false)).toBe(false);
    expect(wantsLabel(bff, 0.7, false)).toBe(true);
    expect(wantsLabel(bff, 0.5, false)).toBe(false);
    expect(wantsLabel(svc, 0.8, true)).toBe(true);
    expect(wantsLabel(svc, 0.8, false)).toBe(false);
    expect(wantsLabel(svc, 0.6, true)).toBe(false);
  });
  it('veri katmanı etiketleri (v0.10.1931): her ad bir kez, en ağırında; "db"/"queue" yedek adı hiç', () => {
    const nodes = [
      { id: 'g', tier: 'gw' as const, weight: 1, name: 'api-gateway' },
      { id: 'a', tier: 'db' as const, weight: 0.4, name: 'ordersdb1' },
      { id: 'b', tier: 'db' as const, weight: 0.9, name: 'ordersdb1' },
      { id: 'c', tier: 'db' as const, weight: 0.8, name: 'db' },
      { id: 'd', tier: 'queue' as const, weight: 0.5, name: 'orders.events' },
      { id: 'e', tier: 'queue' as const, weight: 0.7, name: 'queue' },
    ];
    const gx = buildGalaxy(nodes, nodes.slice(1).map(n => ({ from: 'g', to: n.id, weight: 0.5 })), 3);
    const set = pickBackendLabels(gx);
    expect([...set].map(i => gx.nodes[i].id).sort()).toEqual(['b', 'd']);
    expect(pickBackendLabels(gx, 1).size).toBe(1);
  });
  it('rastgele etiket kümesi (v0.10.1918): yalnız adı olan servisler, tavan kadar; kaynak değişince değişir', () => {
    const gx = named();
    const a = pickLabelSet(gx, 24, mulberry32(1)), b = pickLabelSet(gx, 24, mulberry32(2));
    expect(a.size).toBe(24);
    expect([...a].every(i => gx.nodes[i].tier === 'svc')).toBe(true);
    expect([...a].sort()).not.toEqual([...b].sort());
    expect(pickLabelSet(synth(), 24, mulberry32(1)).size).toBe(0); // isimsiz kip: ad yok
  });
});

describe('yoğun grafik (v0.10.1934)', () => {
  it('çizilen düğüm DENSE_NODES\'u aşınca noktalar %15 küçülür', () => {
    const mk = (n: number) => Array.from({ length: n }, (_, i) => ({ id: `n${i}`, tier: (i < 6 ? 'gw' : 'svc') as 'gw' | 'svc', weight: 1 }));
    const maxSize = (n: number) => Math.max(...buildGalaxy(mk(n), [], 5).nodes.map(nd => nd.size));
    const top = sizeOf(1, 1);
    expect(maxSize(DENSE_NODES)).toBeGreaterThan(top * DENSE_SIZE_K);
    expect(maxSize(DENSE_NODES + 1)).toBeLessThanOrEqual(top * DENSE_SIZE_K + 1e-9);
  });
});
