// v0.10.1294 — Topology: odak merkezli sütun yerleşimi (operatör 2026-10-17:
// "Topology'de servisler arası oklar çok uzak ve sarmal bir path gibi
// gidiyor; optimize edelim, gerçek topoloji anlaşılır olsun").
//
// Kök neden: odaklı görünüm global haritanın katmanlı yerleşimini
// (lib/topoBfsLayout) kullanıyordu — en uzun yol katmanı odağı herhangi bir
// sütuna (çoğu kez en sağa) itiyor, sütunlar kap genişliğine geriliyor
// (~590px boşluk), namespace şeritleri boş satır ayırıyor ve uzun kenarlar
// her ara sütunda bir S-büküm alıp sarmal çiziyordu.
//
// Bu dosya yeni saf çekirdeğin (lib/topoFocusLayout) DEĞİŞMEZLERİNİ çiviler:
//   (1) x = xOfCol(col), odak sütun 0'da ve tam dikey ortada (y = lh/2)
//   (2) her sütunda ardışık düğümler arası dikey aralık ≥ pitch
//   (3) her flow kenarı tam bir sütun açar; hiçbir rota >1 ara nokta almaz
//   (4) skip/back şerit noktası ara sütunun pil aralığının DIŞINDA
//   (5) 2-hop düğüm ebeveyninin pitch·(sütun sayısı) yakınında; PAV optimal
//   (6) karıştırılmış girdi → aynı çıktı
//   (7) ekran görüntüsü biçimli fikstür (40 düğüm / 106 kenar, 2 hop, 3 ns,
//       2 paylaşılan db): genişlik/yükseklik sınırlı, sarmal yok, varsayılan
//       görünüm yalnız flow kenarlarını çizer
//   (8) kenar sınıflandırma tablosu
//   (9) perf: 165 düğüm / 600 kenar < 30 ms (makineye göre ölçekli)
import { describe, it, expect } from 'vitest';
import {
  focusColumnLayout, classifyEdge, pairClass, xOfCol, placeOuter, pav,
  focusPath, countSecondary, edgeVisibility, focusColumnLabel,
  moreNodeId, isMoreNode, moreNodeCol, noCallersHeader, NO_CALLERS_LABEL,
  COL_W, PAD_X, PAD_Y, BLOCK_GAP, HALF_PILL, PORT_SPREAD_MAX,
  type FocusLayoutInput, type FocusLayoutResult,
} from './topoFocusLayout';
import { ROW_PITCH, NODE_H, NODE_H_FOCUS } from './topoLayout';
import { assignFocusColumns } from '@/components/topology/FocusedNeighborhood';
import type { GraphEdge } from './types';

type N = { service: string; spanCount: number; kind?: string };
type E = { caller: string; callee: string };

const node = (service: string, spanCount = 100, kind?: string): N => ({ service, spanCount, ...(kind ? { kind } : {}) });
const edge = (caller: string, callee: string): E => ({ caller, callee });

function colsFrom(edges: E[], focus: string, hops: number): Map<string, number> {
  const ge: GraphEdge[] = edges.map(e => ({
    source: e.caller, target: e.callee, calls: 1, errors: 0, errorRate: 0, rate: 0, avgMs: 1, p99Ms: 1,
  }));
  return assignFocusColumns(ge, focus, hops);
}

function layout(nodes: N[], edges: E[], focus: string, hops = 2, nsOf?: (s: string) => string | undefined): FocusLayoutResult {
  return focusColumnLayout({ nodes, edges, colOf: colsFrom(edges, focus, hops), focus, nsOf });
}

function columnsOf(r: FocusLayoutResult, colOf: ReadonlyMap<string, number>): Map<number, number[]> {
  const m = new Map<number, number[]>();
  for (const [s, p] of r.pos) {
    const c = colOf.get(s)!;
    (m.get(c) ?? m.set(c, []).get(c)!).push(p.y);
  }
  for (const ys of m.values()) ys.sort((a, b) => a - b);
  return m;
}

// ── ekran görüntüsü biçimli fikstür ────────────────────────────────────
// 40 düğüm / 106 kenar, 2 hop, 3 namespace (a/b/c), 2 paylaşılan db.
const NS: Record<string, string> = {};
function screenshotFixture(): { nodes: N[]; edges: E[]; nsOf: (s: string) => string | undefined } {
  const nodes: N[] = [node('gw', 5000)];
  NS.gw = 'a';
  const c1 = Array.from({ length: 6 }, (_, i) => `caller-${i}`);
  const c2 = Array.from({ length: 5 }, (_, i) => `edge-${i}`);
  const s1 = Array.from({ length: 10 }, (_, i) => `dep-${i}`);
  const s2 = Array.from({ length: 14 }, (_, i) => `deep-${i}`);
  const dbs = ['db:shared-0', 'db:shared-1'];
  c1.forEach((s, i) => { nodes.push(node(s, 900 - i * 50)); NS[s] = i < 3 ? 'a' : 'b'; });
  c2.forEach((s, i) => { nodes.push(node(s, 300 - i * 10)); NS[s] = ['a', 'b', 'c'][i % 3]; });
  s1.forEach((s, i) => { nodes.push(node(s, 2000 - i * 100)); NS[s] = i < 4 ? 'a' : i < 7 ? 'b' : 'c'; });
  s2.forEach((s, i) => { nodes.push(node(s, 500 - i * 7)); NS[s] = ['a', 'b', 'c'][i % 3]; });
  nodes.push(node(dbs[0], 4000, 'db'), node(dbs[1], 3000, 'db'), node('queue:events', 800, 'queue'), node('ext:stripe', 200, 'external'));
  const es: E[] = [];
  const add = (a: string, b: string) => { if (!es.some(e => e.caller === a && e.callee === b)) es.push(edge(a, b)); };
  c2.forEach((s, i) => { add(s, c1[i % 6]); add(s, c1[(i + 2) % 6]); });           // 10  −2 → −1
  c1.forEach(s => add(s, 'gw'));                                                    // 6   −1 → 0
  [...s1, ...dbs, 'queue:events', 'ext:stripe'].forEach(s => add('gw', s));        // 14  0 → +1
  s2.forEach((s, i) => { add(s1[i % 10], s); add(s1[(i + 3) % 10], s); });          // 28  +1 → +2
  s1.forEach((s, i) => add(s, dbs[i % 2]));                                         // 10  peer +1
  add(c1[0], c1[1]); add(c1[3], c1[4]); add(c1[1], c1[2]);                         // 3   peer −1
  c1.forEach(s => add(s, dbs[0]));                                                  // 6   skip −1 → +1
  c2.slice(0, 3).forEach(s => add(s, s1[0]));                                       // 3   skip −2 → +1
  add(s2[0], s1[5]); add(s2[1], s1[6]);                                             // 2   back +2 → +1
  add(s1[2], c1[3]); add(s1[3], c1[4]);                                             // 2   back +1 → −1
  add(s2[2], s2[3]); add(s2[4], s2[5]); add(s2[6], s2[7]); add(s2[8], s2[9]);      // 4   peer +2
  s1.forEach(s => add(s, 'ext:stripe'));                                            // 10  peer +1
  s2.slice(0, 8).forEach(s => add(s, dbs[1]));                                      // 8   back +2 → +1
  return { nodes, edges: es, nsOf: s => NS[s] };
}

describe('focusColumnLayout — ekran görüntüsü fikstürü (v0.10.1294)', () => {
  const fx = screenshotFixture();
  const colOf = colsFrom(fx.edges, 'gw', 2);
  const input: FocusLayoutInput = { nodes: fx.nodes, edges: fx.edges, colOf, focus: 'gw', nsOf: fx.nsOf };
  const r = focusColumnLayout(input);

  it('fikstür operatörün ekranının biçiminde: 40 düğüm, 106 kenar, 2 hop', () => {
    expect(fx.nodes).toHaveLength(40);
    expect(fx.edges).toHaveLength(106);
    expect(Math.max(...[...colOf.values()].map(Math.abs))).toBe(2);
    expect(r.pos.size).toBe(40);
  });

  it('(1) x = xOfCol(col, hops.left); odak sütun 0, tam ortada (simetrik: 2 | 2)', () => {
    expect(r.hops).toEqual({ left: 2, right: 2 });
    for (const [s, p] of r.pos) expect(p.x, s).toBe(xOfCol(colOf.get(s)!, r.hops.left));
    const f = r.pos.get('gw')!;
    expect(f.y).toBe(r.lh / 2);
    expect(f.x).toBe(r.lw / 2);
    // v0.10.1307 — simetrik grafta eski 2·H·COL_W + 2·PAD_X genişliği aynen
    expect(r.lw).toBe(2 * 2 * COL_W + 2 * PAD_X);
    expect(r.focusPt).toEqual(f);
  });

  it('(2) her sütunda ardışık y aralığı ≥ pitch', () => {
    for (const [c, ys] of columnsOf(r, colOf)) {
      for (let i = 1; i < ys.length; i++) expect(ys[i] - ys[i - 1], `col ${c}`).toBeGreaterThanOrEqual(ROW_PITCH - 1e-9);
    }
  });

  it('(3) flow kenarı tam bir sütun açar; hiçbir rota >1 ara nokta almaz', () => {
    let flows = 0;
    for (const e of fx.edges) {
      const g = r.edges.get(`${e.caller}|${e.callee}`)!;
      expect(g, `${e.caller}|${e.callee}`).toBeDefined();
      expect(g.pts.length).toBeLessThanOrEqual(3);
      if (g.cls === 'flow') {
        flows++;
        expect(Math.abs(colOf.get(e.callee)! - colOf.get(e.caller)!)).toBe(1);
        expect(g.pts).toHaveLength(2);
      }
    }
    expect(flows).toBeGreaterThan(50);
  });

  it('(4) skip/back şerit noktası ara sütunun pil aralığının dışında', () => {
    const cols = columnsOf(r, colOf);
    let lanes = 0;
    for (const g of r.edges.values()) {
      if (g.pts.length !== 3) continue;
      expect(['skip', 'back']).toContain(g.cls);
      lanes++;
      const lane = g.pts[1];
      const c = [...cols.keys()].find(k => xOfCol(k, r.hops.left) === lane.x)!;
      const ys = cols.get(c)!;
      const half = (c === 0 ? NODE_H_FOCUS : NODE_H) / 2;
      const outside = lane.y < ys[0] - half || lane.y > ys[ys.length - 1] + half;
      expect(outside, `lane ${lane.x},${lane.y} col ${c}`).toBe(true);
    }
    expect(lanes).toBeGreaterThan(0);
  });

  it('(5) 2-hop düğüm ebeveyninin pitch·(sütun sayısı) yakınında', () => {
    const cols = columnsOf(r, colOf);
    for (const [s, c] of colOf) {
      if (Math.abs(c) !== 2) continue;
      const inner = c > 0 ? 1 : -1;
      // flow ebeveynleri: +2 için onu çağıran +1, −2 için onun çağırdığı −1
      const parents = fx.edges
        .filter(e => (c > 0 ? e.callee === s && colOf.get(e.caller) === inner : e.caller === s && colOf.get(e.callee) === inner))
        .map(e => (e.callee === s ? e.caller : e.callee));
      expect(parents.length, s).toBeGreaterThan(0);
      const py = parents.reduce((a, p) => a + r.pos.get(p)!.y, 0) / parents.length;
      expect(Math.abs(r.pos.get(s)!.y - py), s).toBeLessThanOrEqual(ROW_PITCH * cols.get(c)!.length);
    }
  });

  it('(7) genişlik ≤ 4·COL_W + 2·PAD, yükseklik ≤ en kalabalık sütun·pitch + boşluklar + pay', () => {
    expect(r.lw).toBeLessThanOrEqual(4 * COL_W + 2 * PAD_X);
    const maxCol = Math.max(...[...columnsOf(r, colOf).values()].map(ys => ys.length));
    expect(r.lh).toBeLessThanOrEqual(maxCol * ROW_PITCH + 6 * BLOCK_GAP + 2 * PAD_Y);
  });

  it('(7) ≥2 ara noktalı rota SIFIR', () => {
    expect([...r.edges.values()].filter(g => g.pts.length > 3)).toHaveLength(0);
  });

  it('(7) varsayılan görünüm yalnız flow kenarlarını çizer', () => {
    const drawn = [...r.edges.values()].filter(g => edgeVisibility(g.cls, { showSecondary: false, incident: false }) !== null);
    expect(drawn.length).toBeGreaterThan(0);
    expect(drawn.every(g => g.cls === 'flow')).toBe(true);
    // ikincil kenarlar sayılır (toolbar "Yan bağlantılar (N)")
    expect(countSecondary(fx.edges, colOf)).toBeGreaterThan(0);
  });

  it('flow kenarı uçları pil kenarında (x ± 85): ok ve chip boşlukta', () => {
    for (const [k, g] of r.edges) {
      if (g.cls !== 'flow') continue;
      const [a, b] = k.split('|');
      const pa = r.pos.get(a)!, pb = r.pos.get(b)!;
      expect(Math.abs(g.pts[0].x - pa.x), k).toBe(HALF_PILL);
      expect(Math.abs(g.pts[1].x - pb.x), k).toBe(HALF_PILL);
      // uç noktalar birbirine dönük (iç kenar)
      expect(Math.sign(g.pts[1].x - g.pts[0].x)).toBe(Math.sign(pb.x - pa.x));
    }
  });

  it('odakta port yayma: ±PORT_SPREAD_MAX içinde, uzak uç y sırasıyla', () => {
    const f = r.pos.get('gw')!;
    const right = [...r.edges.entries()]
      .filter(([k, g]) => k.startsWith('gw|') && g.cls === 'flow')
      .map(([k, g]) => ({ off: g.pts[0].y - f.y, far: r.pos.get(k.split('|')[1])!.y }));
    expect(right.length).toBe(14);
    for (const x of right) expect(Math.abs(x.off)).toBeLessThanOrEqual(PORT_SPREAD_MAX);
    right.sort((p, q) => p.far - q.far);
    for (let i = 1; i < right.length; i++) expect(right[i].off).toBeGreaterThanOrEqual(right[i - 1].off);
    expect(new Set(right.map(x => x.off)).size).toBeGreaterThan(1);
  });

  it('namespace blokları ±1 sütununda bitişik; köşeli ayraç ≥2 üyeli koşulara', () => {
    const plus1 = [...colOf].filter(([, c]) => c === 1).map(([s]) => s)
      .sort((a, b) => r.pos.get(a)!.y - r.pos.get(b)!.y);
    const seq = plus1.map(s => (s.includes(':') ? `kind:${s.split(':')[0]}` : `ns:${NS[s]}`));
    const seen = new Set<string>();
    for (let i = 0; i < seq.length; i++) {
      if (i > 0 && seq[i] === seq[i - 1]) continue;
      expect(seen.has(seq[i]), `blok bölünmüş: ${seq.join(',')}`).toBe(false);
      seen.add(seq[i]);
    }
    const br = r.brackets.filter(b => b.col === 1);
    expect(br.map(b => b.ns).sort()).toEqual(['a', 'b', 'c']);
    for (const b of br) expect(b.y1).toBeGreaterThan(b.y0);
  });

  it('sütun başlıkları her dolu sütun için', () => {
    expect(r.columns.map(c => c.col)).toEqual([-2, -1, 0, 1, 2]);
    expect(r.columns.find(c => c.col === 1)!.count).toBe(14);
  });
});

// v0.10.1307 — operatör 2026-10-17: servis Topology sekmesinde hop'u az olan
// tarafa boş 280 px sütun ayrılıyordu (lw = 2·max|col|·COL_W, odak lw/2'de).
// Kök neden: genişlik ve x tek bir H = max|col| ile hesaplanıyordu. Artık
// her taraf kendi derinliği (HL / HR) kadar yer alır; y yerleşimi değişmez.
describe('asimetrik derinlik — her taraf kendi hop sayısı kadar (v0.10.1307)', () => {
  // çağıran c (−1); bağımlılık p (+1), k (+2)
  const nodes = [node('f'), node('c'), node('p'), node('k')];
  const edges = [edge('c', 'f'), edge('f', 'p'), edge('p', 'k')];

  it('çağıran 1 / bağımlılık 2 → lw = 3·COL_W + 2·PAD_X, odak x = PAD_X + COL_W', () => {
    const r = layout(nodes, edges, 'f', 2);
    expect(r.hops).toEqual({ left: 1, right: 2 });
    expect(r.lw).toBe(3 * COL_W + 2 * PAD_X);
    expect(r.focusPt.x).toBe(PAD_X + COL_W);
    expect(r.pos.get('c')!.x).toBe(PAD_X);
    expect(r.pos.get('k')!.x).toBe(r.lw - PAD_X);
    expect(r.columns.map(c => c.col)).toEqual([-1, 0, 1, 2]);
    // dikey yerleşime dokunulmadı: odak hâlâ tam dikey ortada
    expect(r.focusPt.y).toBe(r.lh / 2);
  });

  it('çağıran yok → odak en solda, boş sütun ayrılmaz', () => {
    const r = layout([node('f'), node('p'), node('k')], [edge('f', 'p'), edge('p', 'k')], 'f', 2);
    expect(r.hops).toEqual({ left: 0, right: 2 });
    expect(r.lw).toBe(2 * COL_W + 2 * PAD_X);
    expect(r.focusPt.x).toBe(PAD_X);
    expect(Math.min(...[...r.pos.values()].map(p => p.x))).toBe(r.focusPt.x);
  });

  it('yalnız çağıran → odak en sağda', () => {
    const r = layout([node('f'), node('c'), node('g')], [edge('c', 'f'), edge('g', 'c')], 'f', 2);
    expect(r.hops).toEqual({ left: 2, right: 0 });
    expect(r.lw).toBe(2 * COL_W + 2 * PAD_X);
    expect(r.focusPt.x).toBe(r.lw - PAD_X);
    expect(Math.max(...[...r.pos.values()].map(p => p.x))).toBe(r.focusPt.x);
  });

  it('"+N more" düğümü kendi tarafının derinliğine sayılır', () => {
    const more = moreNodeId(-1);
    const r = focusColumnLayout({
      nodes: [node('f'), node('p'), node(more)], edges: [edge('f', 'p'), edge(more, 'f')],
      colOf: new Map([['f', 0], ['p', 1], [more, -1]]), focus: 'f',
    });
    expect(r.hops).toEqual({ left: 1, right: 1 });
  });

  it('skip şerit noktası ve ayraçlar aynı x ekseninde (xOfCol(·, hops.left))', () => {
    // f → a, f → b (+1, ns n), a → k (+2), c → f (−1), c → k skip (−1 → +2)
    const ns: Record<string, string> = { a: 'n', b: 'n' };
    const r = layout(
      [node('f'), node('a', 50), node('b', 40), node('k'), node('c')],
      [edge('f', 'a'), edge('f', 'b'), edge('a', 'k'), edge('c', 'f'), edge('c', 'k')], 'f', 2, s => ns[s],
    );
    expect(r.hops).toEqual({ left: 1, right: 2 });
    const lane = r.edges.get('c|k')!;
    expect(lane.cls).toBe('skip');
    expect([0, 1].map(c => xOfCol(c, 1))).toContain(lane.pts[1].x);
    const br = r.brackets.find(b => b.col === 1)!;
    expect(br.x).toBe(xOfCol(1, 1) + HALF_PILL + 8);
  });

  it('determinizm: karıştırılmış girdi → aynı çıktı', () => {
    const a = layout(nodes, edges, 'f', 2);
    const b = layout(nodes.slice().reverse(), edges.slice().reverse(), 'f', 2);
    expect([...b.pos.entries()].sort()).toEqual([...a.pos.entries()].sort());
    expect([...b.edges.entries()].sort()).toEqual([...a.edges.entries()].sort());
    expect(b.lw).toBe(a.lw);
    expect(b.hops).toEqual(a.hops);
  });

  it('noCallersHeader: yalnız çağıran tarafı boşken, odağın solunda yer varsa', () => {
    const none = layout([node('f'), node('p')], [edge('f', 'p')], 'f', 1);
    const h = noCallersHeader(none)!;
    expect(h).not.toBeNull();
    expect(h.x).toBeGreaterThanOrEqual(0);
    expect(h.x + h.w).toBeLessThan(none.focusPt.x); // "Odak" başlığına binmez
    expect(noCallersHeader(layout(nodes, edges, 'f', 2))).toBeNull();
    expect(noCallersHeader({ hops: { left: 0, right: 1 }, focusPt: { x: 40, y: 0 } })).toBeNull(); // yer yok
    expect(NO_CALLERS_LABEL).toBe('Çağıran yok (enstrümante değil)');
  });
});

describe('(6) determinizm', () => {
  it('karıştırılmış düğüm + kenar sırası → aynı çıktı', () => {
    const fx = screenshotFixture();
    const colOf = colsFrom(fx.edges, 'gw', 2);
    const a = focusColumnLayout({ nodes: fx.nodes, edges: fx.edges, colOf, focus: 'gw', nsOf: fx.nsOf });
    let seed = 42;
    const rnd = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648; };
    const shuffle = <T,>(xs: T[]) => { const c = xs.slice(); for (let i = c.length - 1; i > 0; i--) { const j = Math.floor(rnd() * (i + 1)); [c[i], c[j]] = [c[j], c[i]]; } return c; };
    const b = focusColumnLayout({ nodes: shuffle(fx.nodes), edges: shuffle(fx.edges), colOf, focus: 'gw', nsOf: fx.nsOf });
    expect([...b.pos.entries()].sort()).toEqual([...a.pos.entries()].sort());
    expect([...b.edges.entries()].sort()).toEqual([...a.edges.entries()].sort());
    expect(b.lw).toBe(a.lw);
    expect(b.lh).toBe(a.lh);
  });
});

describe('(8) kenar sınıflandırma', () => {
  it.each([
    [0, 1, 'flow'], [-1, 0, 'flow'], [-2, -1, 'flow'], [1, 2, 'flow'],
    [1, 1, 'peer'], [-1, -1, 'peer'],
    [-1, 1, 'skip'], [-2, 1, 'skip'], [0, 2, 'skip'],
    [1, 0, 'back'], [2, 1, 'back'], [1, -1, 'back'],
  ] as const)('%i → %i = %s', (cu, cv, cls) => {
    expect(classifyEdge(cu, cv)).toBe(cls);
  });
  it('çift yönlü çift soldan sağa yönden sınıflanır (asla back değil)', () => {
    expect(pairClass(1, 0, true)).toBe('flow');
    expect(pairClass(1, -1, true)).toBe('skip');
    expect(pairClass(1, 0, false)).toBe('back');
  });
  it('countSecondary çift yönlü çifti tek sayar', () => {
    const col = new Map([['f', 0], ['a', 1], ['b', 1], ['c', -1]]);
    expect(countSecondary([edge('f', 'a'), edge('a', 'f'), edge('a', 'b'), edge('b', 'a'), edge('c', 'a')], col)).toBe(2);
  });
  it('edgeVisibility: flow her zaman; ikincil yalnız açıkken ya da hover ucundayken', () => {
    expect(edgeVisibility('flow', { showSecondary: false, incident: false })).toBe(1);
    expect(edgeVisibility('peer', { showSecondary: false, incident: false })).toBeNull();
    expect(edgeVisibility('skip', { showSecondary: true, incident: false })).toBe(1);
    const faint = edgeVisibility('back', { showSecondary: false, incident: true });
    expect(faint).not.toBeNull();
    expect(faint!).toBeLessThan(1);
  });
});

describe('(5) PAV — dikey yerleşim', () => {
  it('pav: azalmayan en yakın dizi (havuzlama)', () => {
    expect(pav([1, 2, 3])).toEqual([1, 2, 3]);
    expect(pav([3, 1])).toEqual([2, 2]);
    expect(pav([5, 1, 3, 0])).toEqual([2.25, 2.25, 2.25, 2.25]);
    expect(pav([0, 4, 2, 6])).toEqual([0, 3, 3, 6]);
  });

  it('placeOuter: el fikstüründe optimal (kaba kuvvetle karşılaştırma)', () => {
    const pitch = 10;
    const d = [50, 0, 0];
    const y = placeOuter(d, pitch, 1000);
    // d − i·p = [50, −10, −20] → tek havuz, ortalama 20/3
    [0, 1, 2].forEach(i => expect(y[i]).toBeCloseTo(20 / 3 + i * pitch, 9));
    const cost = (ys: number[]) => ys.reduce((a, v, i) => a + (v - d[i]) ** 2, 0);
    // y0 ızgarası üzerinde kaba kuvvet: y1 ≥ y0+p, y2 ≥ y1+p
    let best = Infinity;
    for (let a = -20; a <= 60; a += 0.5) for (let b = a + pitch; b <= 80; b += 0.5) for (let c = b + pitch; c <= 100; c += 0.5) {
      best = Math.min(best, cost([a, b, c]));
    }
    expect(cost(y)).toBeLessThanOrEqual(best + 1e-6);
  });

  it('placeOuter: çakışma yok, istenen konum çakışmıyorsa birebir', () => {
    expect(placeOuter([0, 100, 200], 10, 1000)).toEqual([0, 100, 200]);
    const y = placeOuter([5, 5, 5, 5], 10, 1000);
    for (let i = 1; i < y.length; i++) expect(y[i] - y[i - 1]).toBeCloseTo(10, 9);
    expect((y[0] + y[3]) / 2).toBeCloseTo(5, 9); // ortalama korunur
  });

  it('placeOuter: ±Y sınırı içinde kalır', () => {
    const y = placeOuter([-500, -500, -500], 10, 40);
    expect(y[0]).toBeGreaterThanOrEqual(-40);
    expect(y[2]).toBeLessThanOrEqual(40);
  });
});

describe('sıra + yerleşim ayrıntıları', () => {
  it('organ borusu: en ağır blok odak satırının yanında, bağımlılıklar dışta', () => {
    const nodes = [node('f'), node('a1', 900), node('a2', 800), node('b1', 100), node('db:x', 50, 'db'), node('db:y', 40, 'db')];
    const edges = ['a1', 'a2', 'b1', 'db:x', 'db:y'].map(s => edge('f', s));
    const ns: Record<string, string> = { a1: 'a', a2: 'a', b1: 'b' };
    const r = layout(nodes, edges, 'f', 1, s => ns[s]);
    const fy = r.pos.get('f')!.y;
    const dist = (s: string) => Math.abs(r.pos.get(s)!.y - fy);
    // a bloğu (en ağır) odağa en yakın; db bloğu en uzak
    expect(Math.min(dist('a1'), dist('a2'))).toBeLessThan(dist('b1'));
    expect(Math.max(dist('a1'), dist('a2'))).toBeLessThan(Math.max(dist('db:x'), dist('db:y')));
    // blok arası BLOCK_GAP
    const ys = ['a1', 'a2', 'b1', 'db:x', 'db:y'].map(s => r.pos.get(s)!.y).sort((p, q) => p - q);
    const gaps = ys.slice(1).map((v, i) => v - ys[i]);
    expect(gaps.filter(g => Math.abs(g - (ROW_PITCH + BLOCK_GAP)) < 1e-9).length).toBe(2);
  });

  it('+N more düğümü sütunda EN ALTTA', () => {
    const more = moreNodeId(1);
    const nodes = [node('f'), node('a', 1), node('b', 999), node(more, 5000)];
    const edges = [edge('f', 'a'), edge('f', 'b'), edge('f', more)];
    const r = layout(nodes, edges, 'f', 1);
    expect(r.pos.get(more)!.y).toBeGreaterThan(Math.max(r.pos.get('a')!.y, r.pos.get('b')!.y));
    expect(isMoreNode(more)).toBe(true);
    expect(moreNodeCol(more)).toBe(1);
    expect(moreNodeCol(moreNodeId(-2))).toBe(-2);
    expect(isMoreNode('db:x')).toBe(false);
  });

  it('2-hop çocuk ebeveyninin hizasında (PAV), sütun genişliği SABİT', () => {
    const nodes = [node('f'), node('p1', 900), node('p2', 100), node('k1'), node('k2')];
    const edges = [edge('f', 'p1'), edge('f', 'p2'), edge('p2', 'k1'), edge('p2', 'k2')];
    const r = layout(nodes, edges, 'f', 2);
    const p2 = r.pos.get('p2')!.y;
    expect((r.pos.get('k1')!.y + r.pos.get('k2')!.y) / 2).toBeCloseTo(p2, 6);
    expect(r.pos.get('p1')!.x - r.pos.get('f')!.x).toBe(COL_W);
    expect(r.pos.get('k1')!.x - r.pos.get('p1')!.x).toBe(COL_W);
  });

  it('peer yayı odaktan uzak dış oluktan döner (kontrol x = x ± (85+22)/0.75)', () => {
    const nodes = [node('f'), node('a'), node('b'), node('c')];
    const edges = [edge('f', 'a'), edge('f', 'b'), edge('a', 'b'), edge('c', 'f')];
    const r = layout(nodes, edges, 'f', 1);
    const g = r.edges.get('a|b')!;
    expect(g.cls).toBe('peer');
    expect(g.pts).toHaveLength(2);
    const x = r.pos.get('a')!.x;
    expect(g.pts[0].x).toBe(x + HALF_PILL);
    expect(g.ctrl![0].x).toBeCloseTo(x + (HALF_PILL + 22) / 0.75, 9);
    expect(g.ctrl![1].x).toBeCloseTo(x + (HALF_PILL + 22) / 0.75, 9);
  });

  it('odak dışında tek düğüm yoksa (komşusuz) çökmez', () => {
    const r = focusColumnLayout({ nodes: [node('f')], edges: [], colOf: new Map([['f', 0]]), focus: 'f' });
    expect(r.pos.get('f')).toEqual({ x: r.lw / 2, y: r.lh / 2 });
  });
});

describe('focusPath — hover yol vurgusu', () => {
  it('2-hop bağımlılık → ebeveyn → odak zinciri', () => {
    const edges = [edge('f', 'p'), edge('f', 'q'), edge('p', 'k'), edge('q', 'z'), edge('c', 'f'), edge('g', 'c')];
    const col = colsFrom(edges, 'f', 2);
    const p = focusPath('k', edges, col);
    expect([...p.nodes].sort()).toEqual(['f', 'k', 'p']);
    expect([...p.edges].sort()).toEqual(['f|p', 'p|k']);
    const up = focusPath('g', edges, col);
    expect([...up.nodes].sort()).toEqual(['c', 'f', 'g']);
    expect([...up.edges].sort()).toEqual(['c|f', 'g|c']);
  });
  it('odak hover: yalnız odak', () => {
    const edges = [edge('f', 'p')];
    expect([...focusPath('f', edges, colsFrom(edges, 'f', 2)).nodes]).toEqual(['f']);
  });
});

describe('sütun başlıkları', () => {
  it.each([
    [-2, 'Callers · 2 hops'], [-1, 'Callers'], [0, 'Focus'], [1, 'Dependencies'], [2, 'Dependencies · 2 hops'],
  ] as const)('%i → %s', (c, l) => expect(focusColumnLabel(c)).toBe(l));
});

// ── (9) perf ───────────────────────────────────────────────────────────
// TopologyFlowGraph.perf.test.tsx layoutBudgetMs deseni: bütçe makine hızına
// göre ölçeklenir (sabit iş kalibrasyonu). 30 ms / ≈33 ms kalibrasyon = 0.9.
function calibrate(): number {
  const runs: number[] = [];
  for (let r = 0; r < 5; r++) {
    const t0 = performance.now();
    const arr = Array.from({ length: 200_000 }, (_, i) => (i * 2654435761) % 1000003);
    arr.sort((a, b) => a - b);
    let s = '';
    for (let i = 0; i < 60_000; i++) s += `<path d="M${arr[i]} ${i}L${arr[i + 1]} ${i + 1}"/>`;
    if (s.length === 0) throw new Error('calibration optimised away');
    runs.push(performance.now() - t0);
  }
  runs.sort((a, b) => a - b);
  return runs[2];
}
export function focusBudgetMs(calibMs: number): number {
  return Math.max(30, 0.9 * calibMs);
}

describe('(9) perf — odak yolu 165 düğüm / 600 kenar', () => {
  it('bütçe makineyle ölçeklenir', () => {
    expect(focusBudgetMs(10)).toBe(30);
    expect(focusBudgetMs(100)).toBe(90);
  });
  it('< 30 ms (ölçekli)', () => {
    // 2 hop: 12 çağıran, 40 2-hop çağıran, 40 bağımlılık, 72 2-hop bağımlılık
    const nodes: N[] = [node('f', 10_000)];
    const es: E[] = [];
    const add = (a: string, b: string) => es.push(edge(a, b));
    const c1 = Array.from({ length: 12 }, (_, i) => `c1-${i}`);
    const c2 = Array.from({ length: 40 }, (_, i) => `c2-${i}`);
    const d1 = Array.from({ length: 40 }, (_, i) => `d1-${i}`);
    const d2 = Array.from({ length: 72 }, (_, i) => `d2-${i}`);
    for (const s of [...c1, ...c2, ...d1, ...d2]) nodes.push(node(s, (s.length * 37) % 1000));
    c1.forEach(s => add(s, 'f'));
    d1.forEach(s => add('f', s));
    c2.forEach((s, i) => { add(s, c1[i % 12]); add(s, c1[(i * 5) % 12]); });
    d2.forEach((s, i) => { add(d1[i % 40], s); add(d1[(i * 7) % 40], s); });
    let k = 0;
    while (es.length < 600) {
      const a = d1[k % 40], b = d2[(k * 11) % 72], c = d1[(k * 3 + 1) % 40];
      if (k % 3 === 0) add(a, c); else if (k % 3 === 1) add(b, d1[(k * 13) % 40]); else add(c2[k % 40], d1[k % 40]);
      k++;
    }
    expect(nodes).toHaveLength(165);
    const colOf = colsFrom(es, 'f', 2);
    const ns = (s: string) => ['a', 'b', 'c'][s.length % 3];
    const run = () => focusColumnLayout({ nodes, edges: es, colOf, focus: 'f', nsOf: ns });
    run();
    const t: number[] = [];
    for (let r = 0; r < 5; r++) { const t0 = performance.now(); run(); t.push(performance.now() - t0); }
    t.sort((a, b) => a - b);
    const calib = calibrate();
    console.log(`\nTOPO_FOCUS_LAYOUT_PERF nodes=165 edges=${es.length} median=${t[2].toFixed(2)}ms min=${t[0].toFixed(2)} calibration=${calib.toFixed(1)}ms`);
    expect(t[2]).toBeLessThan(focusBudgetMs(calib));
  });
});
