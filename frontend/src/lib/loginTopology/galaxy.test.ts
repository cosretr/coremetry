// galaxy.test — v0.10.1928 (operatör: "veriye bağlı galaksi en iyisi").
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { mulberry32 } from './prng';
import { syntheticGraph, SYNTHETIC_SEED } from './synthetic';
import {
  buildGalaxy, depthOf, flowPos, flowSpeed, isDarkColor, nodeAlpha, nodeRadius, paceFrom, pickBackendLabels, pickLabelSet, project,
  sizeOf, spinRate, wantsLabel, DENSE_NODES, DENSE_SIZE_K, MAX_STARS, MIN_DUST, placeLabels, labelBox, LABEL_CARD_MARGIN, rebalanceArms, edgeVisible, trafficLevel, cardPlacement, MIN_ARMS, TILT0, DUST_ARM, DUST_CORE, FLOW_EDGES, type GNode,
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
  // v0.10.1944 — kol dengesi: BFF dalları en boş kola gider (gateway'inin kolunda olmayabilir);
  // gateway'ler kendi kolunda, servis bağlı olduğu BFF ile aynı kolda.
  it('her gateway bir kol; servis BFF\'iyle aynı kolda (dal bütün kalır)', () => {
    const gx = synth();
    const gws = gx.nodes.map((n, i) => ({ n, i })).filter(x => x.n.tier === 'gw');
    expect(gx.arms).toBe(gws.length);
    expect(new Set(gws.map(x => x.n.arm)).size).toBe(gws.length);
    for (const e of gx.edges) {
      const a = gx.nodes[e.a], b = gx.nodes[e.b];
      if (a.tier === 'bff' && b.tier === 'svc' && !gx.edges.some(x => (x.a === e.b || x.b === e.b) && gx.nodes[x.a === e.b ? x.b : x.a].tier === 'gw')) {
        expect(b.arm).toBe(a.arm);
      }
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

describe('tüm servisler: adsız yıldızlar (v0.10.1937)', () => {
  const g = syntheticGraph(SYNTHETIC_SEED);
  const base = buildGalaxy(g.nodes, g.edges, 7);
  it('yıldız yokken toz aynen, yıldız boş', () => {
    expect(base.stars.length).toBe(0);
    expect(base.dust.length / 5).toBe(DUST_ARM + DUST_CORE);
  });
  it('yıldızlar tozun yerini alır (en az MIN_DUST toz), komşusuna yakın yarıçapta durur', () => {
    const stars = Array.from({ length: 3000 }, (_, i) => ({ w: (i % 10) / 10, near: i % 2 ? 5 : -1 }));
    const gx = buildGalaxy(g.nodes, g.edges, 7, undefined, undefined, stars);
    expect(gx.stars.length / 5).toBe(3000);
    expect(gx.dust.length / 5).toBeGreaterThanOrEqual(MIN_DUST);
    expect(gx.dust.length / 5 + gx.stars.length / 5).toBeLessThanOrEqual(DUST_ARM + DUST_CORE + 2);
    for (let k = 1; k < 3000; k += 2) expect(Math.abs(gx.stars[k * 5] - gx.nodes[5].r)).toBeLessThan(0.4);
    for (let o = 3; o < gx.stars.length; o += 5) { expect(gx.stars[o]).toBeGreaterThanOrEqual(0); expect(gx.stars[o]).toBeLessThanOrEqual(1); }
  });
  it('tavan MAX_STARS; düğüm konumları yıldızlardan etkilenmez', () => {
    const many = Array.from({ length: MAX_STARS + 50 }, () => ({ w: 0.5, near: -1 }));
    const gx = buildGalaxy(g.nodes, g.edges, 7, undefined, undefined, many);
    expect(gx.stars.length / 5).toBe(MAX_STARS);
    expect(gx.nodes.map(n => [n.r, n.th])).toEqual(base.nodes.map(n => [n.r, n.th]));
  });
});

describe('etiket yerleşimi (v0.10.1943)', () => {
  const c = (i: number, x: number, y: number, tier: string, weight = 0.5, w = 60) => ({ i, x, y, w, tier, weight });
  it('kartın altına (pay dahil) giren etiket çizilmez', () => {
    const card = { l: 400, r: 1000, t: 200, b: 700 };
    // 0: kutu sağı 330+70=400 > 400-10 (paya uzanır) · 1: içinde · 2: kutu solu 1000+6 < 1000+10 (payda) · 3: dışarıda
    const out = placeLabels([c(0, 330, 300, 'svc'), c(1, 500, 300, 'svc'), c(2, 1000, 300, 'svc'), c(3, 1100, 300, 'svc')], card);
    expect(out.sort()).toEqual([3]);
    expect(placeLabels([c(0, 300, 300, 'svc')], card)).toEqual([0]); // kutu sağı 370 < 390
  });
  it('çakışmada öncelik: gateway > BFF > veri > servis, sonra ağırlık', () => {
    expect(placeLabels([c(0, 100, 100, 'svc', 1), c(1, 110, 102, 'gw', 0.1)], null)).toEqual([1]);
    expect(placeLabels([c(0, 100, 100, 'svc', 0.2), c(1, 110, 102, 'svc', 0.9)], null)).toEqual([1]);
    expect(placeLabels([c(0, 100, 100, 'db'), c(1, 110, 102, 'bff')], null)).toEqual([1]);
  });
  it('çakışmayanların hepsi kalır', () => {
    const out = placeLabels([c(0, 100, 100, 'svc'), c(1, 100, 140, 'svc'), c(2, 400, 100, 'svc')], null);
    expect(out.sort()).toEqual([0, 1, 2]);
    expect(labelBox(c(0, 100, 100, 'svc'))).toEqual({ l: 106, r: 170, t: 91, b: 106 });
  });
});

describe('kol dengesi (v0.10.1944)', () => {
  it('rebalanceArms: dallar büyükten küçüğe en boş kola; kol başları yerinde', () => {
    // 3 kol başı (0,1,2). Kol 0'ın 3 dalı: 10, 8, 2 düğüm; kol 1 ve 2'nin dalı yok.
    const arm: number[] = [0, 1, 2], branch: number[] = [-1, -1, -1];
    const add = (br: number, sz: number) => { for (let k = 0; k < sz; k++) { arm.push(0); branch.push(br); } };
    add(100, 10); add(101, 8); add(102, 2);
    const load = rebalanceArms(arm, branch, 3);
    expect(arm.slice(0, 3)).toEqual([0, 1, 2]);
    expect([...load].sort((a, b) => a - b)).toEqual([3, 9, 11]); // 10→kol0 (1+10), 8→kol1 (1+8), 2→kol2 (1+2)
    expect(new Set(arm.slice(3, 13))).toEqual(new Set([0])); // aynı dal aynı kolda
  });
  it('dengesiz grafikte hiçbir kol düğümlerin yarısından fazlasını almaz', () => {
    const nodes: { id: string; tier: 'gw' | 'bff' | 'svc'; weight: number }[] = [];
    const edges: { from: string; to: string; weight: number }[] = [];
    const gw = ['g0', 'g1', 'g2', 'g3'];
    gw.forEach(id => nodes.push({ id, tier: 'gw', weight: 1 }));
    const bffPer = [8, 1, 1, 1];
    gw.forEach((g, gi) => { for (let b = 0; b < bffPer[gi]; b++) {
      const bid = `${g}b${b}`; nodes.push({ id: bid, tier: 'bff', weight: 0.5 }); edges.push({ from: g, to: bid, weight: 1 });
      for (let s = 0; s < 6; s++) { const sid = `${bid}s${s}`; nodes.push({ id: sid, tier: 'svc', weight: 0.2 }); edges.push({ from: bid, to: sid, weight: 1 }); }
    } });
    const gx = buildGalaxy(nodes, edges, 3);
    const per = new Array(gx.arms).fill(0);
    gx.nodes.forEach(nd => { per[nd.arm]++; });
    expect(Math.max(...per) / gx.nodes.length).toBeLessThan(0.5);
  });
});

describe('sade çizgiler (v0.10.1945)', () => {
  it('kollar arası bağ yalnız odakta; aynı koldaki bağ hep', () => {
    expect(edgeVisible(1, 1, false)).toBe(true);
    expect(edgeVisible(1, 2, false)).toBe(false);
    expect(edgeVisible(1, 2, true)).toBe(true);
  });
  it('çizim kenarı edgeVisible ile süzer, akış süzülmez', () => {
    const src = readFileSync(resolve(__dirname, '../../pages/login/TopologyBackdrop.tsx'), 'utf8');
    expect(src).toContain('if (!edgeVisible(g.nodes[e.a].arm, g.nodes[e.b].arm, focus)) continue;');
    const flowLoop = src.slice(src.indexOf('for (const k of g.flows)'), src.indexOf('// düğümler: arkadan öne'));
    expect(flowLoop).not.toContain('edgeVisible');
  });
});

describe('üzerine gelince kart (v0.10.1946)', () => {
  it('trafik seviyesi ağırlık eşiklerinden', () => {
    expect(trafficLevel(0.9)).toBe('busy');
    expect(trafficLevel(0.4)).toBe('mid');
    expect(trafficLevel(0.1)).toBe('calm');
  });
  it('kart noktanın sağında; sığmazsa solunda; dikeyde tuvalde kalır', () => {
    expect(cardPlacement(100, 100, 120, 58, 1000, 800)).toEqual({ x: 114, y: 92 });
    expect(cardPlacement(950, 100, 120, 58, 1000, 800)).toEqual({ x: 816, y: 92 });
    expect(cardPlacement(100, 790, 120, 58, 1000, 800)).toEqual({ x: 114, y: 734 });
    expect(cardPlacement(100, 2, 120, 58, 1000, 800)).toEqual({ x: 114, y: 8 });
  });
});

describe('tüm servisler düğüm (v0.10.1947)', () => {
  it('yıldız başına ad ve komşu sayısı sırayla tutulur; ad yoksa undefined', () => {
    const g = syntheticGraph(SYNTHETIC_SEED);
    const gx = buildGalaxy(g.nodes, g.edges, 7, undefined, undefined, [
      { w: 0.5, near: 2, name: 'pricing-service', links: 3 }, { w: 0.1, near: -1 }, { w: 0.2, near: -1, name: '', links: 1 },
    ]);
    expect(gx.starMeta).toEqual([{ name: 'pricing-service', links: 3 }, { name: undefined, links: 0 }, { name: undefined, links: 1 }]);
    expect(gx.stars.length / 5).toBe(gx.starMeta.length);
  });
  it('çizim yıldızı odakta büyütür ve adlı yıldıza kart çizer; isabet adsız yıldızı atlar', () => {
    const src = readFileSync(resolve(__dirname, '../../pages/login/TopologyBackdrop.tsx'), 'utf8');
    expect(src).toContain("if (m?.name && p) hoverCard(ctx, f, p, m.name, 'svc'");
    expect(src).toContain('if (!q || !galaxy.starMeta[k]?.name) continue;');
  });
});

