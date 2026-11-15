import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { assignFocusColumns, externalPinHost, graphEdgeToMapEdge, viaHostsInto } from './FocusedNeighborhood';
import type { GraphEdge, GraphNode } from '@/lib/types';

// v0.8.39 — Operator-reported: the focused topology graph "won't branch" at
// 2 hops — every node piled into ONE vertical column instead of fanning out
// (focus centre, callers left, deps right). Root cause: the old single
// bidirectional BFS summed the ±1 steps along the path, so a node reached as
// caller(-1) → that caller's OTHER dependency(+1) landed at column 0,
// dumping every sibling of the focus into the focus's own column (against
// real data ~26 nodes piled at 0). assignFocusColumns walks the two
// directions SEPARATELY (upstream IN-only → -hop, downstream OUT-only →
// +hop), so callers fan strictly left, deps strictly right, and only the
// focus is at column 0. These cases would fail on the pre-fix path-sum code.

const e = (source: string, target: string): GraphEdge => ({
  source, target, calls: 1, errors: 0, errorRate: 0, rate: 0, avgMs: 1, p99Ms: 1,
});

describe('assignFocusColumns', () => {
  it('keeps a caller-sibling OFF the focus column at 2 hops (the bug)', () => {
    const edges = [
      e('caller', 'focus'),     // caller is upstream of focus
      e('caller', 'sibling'),   // sibling shares the caller — NOT on a focus up/down path
      e('focus', 'dep'),        // dep is downstream of focus
      e('dep', 'deepdep'),      // 2-hop downstream
    ];
    const col = assignFocusColumns(edges, 'focus', 2);

    expect(col.get('focus')).toBe(0);
    expect(col.get('caller')).toBe(-1);   // upstream → left
    expect(col.get('dep')).toBe(1);       // downstream → right
    expect(col.get('deepdep')).toBe(2);   // 2-hop downstream
    // the sibling is reachable only via caller(-1)→sibling, i.e. col 0 under
    // the old path-sum — it must NOT be pinned to the focus column.
    expect(col.get('sibling')).not.toBe(0);
    // in fact a pure up/down walk never reaches it → excluded entirely.
    expect(col.has('sibling')).toBe(false);
    // INVARIANT: nothing but the focus sits at column 0.
    expect([...col.values()].filter(v => v === 0)).toEqual([0]);
  });

  it('fans callers left (negative) and deps right (positive) by hop depth', () => {
    const edges = [e('gp', 'caller'), e('caller', 'focus'), e('focus', 'dep'), e('dep', 'gd')];
    const col = assignFocusColumns(edges, 'focus', 2);
    expect(col.get('caller')).toBe(-1);
    expect(col.get('gp')).toBe(-2);
    expect(col.get('dep')).toBe(1);
    expect(col.get('gd')).toBe(2);
  });

  it('bounds the walk by hops', () => {
    const col = assignFocusColumns([e('focus', 'a'), e('a', 'b'), e('b', 'c')], 'focus', 1);
    expect(col.get('a')).toBe(1);
    expect(col.has('b')).toBe(false); // beyond 1 hop
  });

  it('a cycle takes the closer side by absolute hop distance', () => {
    // focus → a → focus (a is both a 1-hop dep and a 1-hop caller); |+1| == |-1|,
    // downstream is assigned first so a settles on +1 — never 0.
    const col = assignFocusColumns([e('focus', 'a'), e('a', 'focus')], 'focus', 2);
    expect(col.get('focus')).toBe(0);
    expect(Math.abs(col.get('a')!)).toBe(1);
    expect(col.get('a')).not.toBe(0);
  });
});

// v0.10.1294 — capPerColumn (capPerSide'ın yerine; operatör 2026-10-17:
// "oklar uzak ve sarmal"). Odak görünümü artık sabit sütunlu (lib/
// topoFocusLayout); tavan da SÜTUN başına: kalabalık sütun (> MAX_PER_COL)
// açılmamışsa 11 düğüm tutar (önce hatalı >%1, sonra çağrı, sonra ad), kalanı
// sütunun sonundaki sentetik "+N more" düğümüne iner; gizli düğümlere giden
// kenarlar toplam kenara birleşir. v0.9.359 değişmezi korunur: hiçbir taraf
// (çağıranlar / bağımlılıklar) tavanla SIFIRLANAMAZ.
import { capPerColumn, MAX_PER_COL, parseTexp, encodeTexp } from './FocusedNeighborhood';
import { moreNodeId } from '@/lib/topoFocusLayout';

describe('capPerColumn', () => {
  const gn = (id: string, calls = 10, errorRate = 0): GraphNode => ({
    id, name: id, kind: 'service', calls, errors: Math.round(calls * errorRate / 100), errorRate, rate: calls,
  });
  const ge = (source: string, target: string, calls = 10, errors = 0, p99Ms = 5): GraphEdge => ({
    source, target, calls, errors, errorRate: calls ? errors / calls * 100 : 0, rate: calls, avgMs: 2, p99Ms,
  });
  // odak + `callers` çağıran (−1) + `deps` bağımlılık (+1)
  const mk = (callers: number, deps: number) => {
    const nodes = [gn('focus', 1000)];
    const edges: GraphEdge[] = [];
    for (let i = 0; i < callers; i++) { nodes.push(gn(`c${String(i).padStart(2, '0')}`, 100 + i)); edges.push(ge(`c${String(i).padStart(2, '0')}`, 'focus', 100 + i)); }
    for (let i = 0; i < deps; i++) { nodes.push(gn(`d${String(i).padStart(2, '0')}`, 100 + i)); edges.push(ge('focus', `d${String(i).padStart(2, '0')}`, 100 + i)); }
    const col = new Map<string, number>([['focus', 0]]);
    for (const n of nodes) if (n.id !== 'focus') col.set(n.id, n.id.startsWith('c') ? -1 : 1);
    return { nodes, edges, col };
  };

  it('tavan altında dokunulmaz', () => {
    const { nodes, edges, col } = mk(5, MAX_PER_COL);
    const r = capPerColumn(nodes, edges, col, 'focus', new Set());
    expect(r.nodes).toHaveLength(nodes.length);
    expect(r.edges).toHaveLength(edges.length);
    expect(r.collapsed).toBe(0);
    expect(r.hidden.size).toBe(0);
  });

  it('45 bağımlılık + 20 çağıran: İKİ taraf da kalır (v0.9.359 değişmezi)', () => {
    const { nodes, edges, col } = mk(20, 45);
    const r = capPerColumn(nodes, edges, col, 'focus', new Set());
    const callers = r.nodes.filter(n => (r.col.get(n.id) ?? 0) < 0);
    const deps = r.nodes.filter(n => (r.col.get(n.id) ?? 0) > 0);
    expect(callers).toHaveLength(MAX_PER_COL);   // 11 + "+9 more"
    expect(deps).toHaveLength(MAX_PER_COL);      // 11 + "+34 more"
    expect(r.hidden.get(-1)).toBe(9);
    expect(r.hidden.get(1)).toBe(34);
    expect(r.collapsed).toBe(9 + 34);
    expect(r.nodes.map(n => n.id)).toContain('focus');
  });

  it('+N more düğümü gizlenen sayıyı ve toplamı taşır', () => {
    const { nodes, edges, col } = mk(0, 20);
    const r = capPerColumn(nodes, edges, col, 'focus', new Set());
    const more = r.nodes.find(n => n.id === moreNodeId(1))!;
    expect(more).toBeDefined();
    expect(more.name).toBe('+9 more');
    expect(r.col.get(more.id)).toBe(1);
    // gizlenenler en az çağrılı 9'u: d00..d08 (100..108)
    expect(more.calls).toBe([100, 101, 102, 103, 104, 105, 106, 107, 108].reduce((a, v) => a + v, 0));
    for (let i = 0; i < 9; i++) expect(r.nodes.some(n => n.id === `d0${i}`)).toBe(false);
  });

  it('önce hatalılar tutulur (errorRate > %1), sonra çağrı, sonra ad', () => {
    const { nodes, edges, col } = mk(0, 20);
    nodes.find(n => n.id === 'd00')!.errorRate = 5;   // en az çağrılı ama hatalı
    nodes.find(n => n.id === 'd01')!.errorRate = 0.5; // eşik altı
    const r = capPerColumn(nodes, edges, col, 'focus', new Set());
    expect(r.nodes.some(n => n.id === 'd00')).toBe(true);
    expect(r.nodes.some(n => n.id === 'd01')).toBe(false);
  });

  it('gizli düğümlere giden kenarlar toplam kenara birleşir (calls/errors toplanır)', () => {
    const { nodes, edges, col } = mk(0, 14);
    // gizlenecek iki düğüm (d00, d01, d02 en az çağrılı) — hatalarıyla
    edges.find(e => e.target === 'd00')!.errors = 3;
    edges.find(e => e.target === 'd01')!.errors = 4;
    const r = capPerColumn(nodes, edges, col, 'focus', new Set());
    const agg = r.edges.filter(e => e.target === moreNodeId(1));
    expect(agg).toHaveLength(1);
    expect(agg[0].source).toBe('focus');
    expect(agg[0].calls).toBe(100 + 101 + 102);
    expect(agg[0].errors).toBe(7);
    expect(agg[0].errorRate).toBeCloseTo(7 / 303 * 100, 9);
    // gizli uçlu ham kenar kalmaz
    expect(r.edges.some(e => ['d00', 'd01', 'd02'].includes(e.target))).toBe(false);
  });

  it('gizli → gizli (farklı sütun) kenarı more → more olur; aynı sütun içi düşer', () => {
    const { nodes, edges, col } = mk(0, 14);
    for (let i = 0; i < 14; i++) { nodes.push(gn(`x${String(i).padStart(2, '0')}`, 1 + i)); col.set(`x${String(i).padStart(2, '0')}`, 2); }
    edges.push(ge('d00', 'x00', 7), ge('d00', 'd01', 2));
    const r = capPerColumn(nodes, edges, col, 'focus', new Set());
    const mm = r.edges.find(e => e.source === moreNodeId(1) && e.target === moreNodeId(2));
    expect(mm?.calls).toBe(7);
    expect(r.edges.some(e => e.source === moreNodeId(1) && e.target === moreNodeId(1))).toBe(false);
  });

  it('açılmış (expanded) sütun kesilmez', () => {
    const { nodes, edges, col } = mk(30, 30);
    const r = capPerColumn(nodes, edges, col, 'focus', new Set([1]));
    expect(r.nodes.filter(n => (r.col.get(n.id) ?? 0) > 0)).toHaveLength(30);
    expect(r.hidden.has(1)).toBe(false);
    expect(r.hidden.get(-1)).toBe(19);
  });

  it('?texp kodeki: sıralı, tekil, geçersizler düşer', () => {
    expect([...parseTexp('2,-2,x,2,0')]).toEqual([-2, 2]);
    expect(parseTexp(null).size).toBe(0);
    expect(encodeTexp(new Set([2, -1]))).toBe('-1,2');
  });
});

// ── v0.9.1255 — pin'li dış düğümün yol kırılımı fetch KAPISI ──────────
//
// Operator-reported: dış düğümde host değil hangi UÇ olduğu anlamlı. Pin
// kartı artık /api/external/host çağırıyor — ve o ucun yol yarısı HAM
// spans okuması. Kapı bu yüzden düğüm TÜRÜNDE: servis / db / queue
// düğümü pin'lendiğinde istek ATILMAMALI. Atılsaydı her pin bir ham
// tarama tetiklerdi ve dönen cevap da boş olurdu (sunucu tarafındaki
// GLOBAL NOT IN enstrümante servisleri zaten eliyor) — bedeli ödeyip
// cevapsızlık almak.
describe('externalPinHost — pin kartı fetch kapısı', () => {
  const node = (over: Partial<GraphNode>): GraphNode => ({
    id: 'x', name: 'x', kind: 'service',
    calls: 0, errors: 0, errorRate: 0, rate: 0, ...over,
  });

  it('dış düğüm host üretir', () => {
    expect(externalPinHost(node({ id: 'ext:esbprod.example.internal', name: 'esbprod.example.internal', kind: 'external' })))
      .toBe('esbprod.example.internal');
  });

  it('SERVİS düğümü pin\'lenince istek atılmaz (asıl regresyon)', () => {
    expect(externalPinHost(node({ id: 'payments', name: 'payments', kind: 'service' }))).toBeNull();
  });

  it('db / queue / internal düğümleri de istek atmaz', () => {
    for (const kind of ['database', 'queue', 'internal'] as GraphNode['kind'][]) {
      expect(externalPinHost(node({ id: `db:oracle@o`, name: 'oracle@o', kind }))).toBeNull();
    }
  });

  it('pin YOKSA (hover) istek atılmaz — hover davranışı değişmedi', () => {
    expect(externalPinHost(null)).toBeNull();
    expect(externalPinHost(undefined)).toBeNull();
  });

  it('adı boş gelen dış düğümde id ön eki soyulur', () => {
    expect(externalPinHost(node({ id: 'ext:api.stripe.com', name: '', kind: 'external' })))
      .toBe('api.stripe.com');
  });
});

// Kapının GERÇEKTEN sorguya bağlı olduğunu pinler. Saf fonksiyonun
// doğru cevap vermesi, bileşenin o cevabı KULLANDIĞINI kanıtlamaz —
// `enabled` satırı düşerse useQuery her pin'de koşardı ve yukarıdaki
// beş test yine yeşil kalırdı.
describe('externalPinHost bağlantısı', () => {
  const src = readFileSync(
    fileURLToPath(new URL('./FocusedNeighborhood.tsx', import.meta.url)), 'utf8');

  it('kapı pinnedNode üstünden kuruluyor (hoverNode DEĞİL)', () => {
    expect(src).toContain('const extHost = externalPinHost(pinnedNode)');
  });

  it('useQuery kapıya bağlı', () => {
    expect(src).toContain('enabled: !!extHost');
  });

  it('staleTime sunucu TTL\'ine eşit veya üstünde (30s)', () => {
    const q = src.slice(src.indexOf('const extDetail = useQuery'));
    const m = /staleTime:\s*([\d_]+)/.exec(q);
    expect(m).not.toBeNull();
    expect(Number(m![1].replace(/_/g, ''))).toBeGreaterThanOrEqual(30_000);
  });
});

// v0.10.1344 — operatör raporu 2026-10-18 (card-web-api komşuluğu, 2 hop):
// enstrümante servisler `ext:acm-notify-dispatch-prod-grpc.channel.svc.cluster.local`
// ve `ext:acm-customer-lookup-prod-app.<kurum>` adıyla "dış bağımlılık"
// çiziliyordu. Sunucu artık bu kenarları gerçek servise katlıyor ve host'u
// GraphEdge.viaHosts'ta bırakıyor; inline adapter bu alanı DÜŞÜRÜRSE
// (v0.8.322 sınıfı) hover kartı "via <host>" satırını hiç basamaz.
describe('graphEdgeToMapEdge (v0.10.1344)', () => {
  const full: Required<GraphEdge> = {
    source: 'card-web-api', target: 'acm-customer-lookup-prod', calls: 80, errors: 2,
    errorRate: 2.5, rate: 1.3, avgMs: 12, p99Ms: 200, protocol: 'http', priorP99Ms: 150,
    viaHosts: ['acm-customer-lookup-prod-app.bank.example', 'acm-customer-lookup-prod-web.bank.example'],
  };
  it('carries viaHosts through the adapter', () => {
    const m = graphEdgeToMapEdge(full);
    expect(m.viaHosts).toEqual(full.viaHosts);
    expect(m).toMatchObject({ caller: full.source, callee: full.target, errorCount: 2, p99Ms: 200, rate: 1.3, avgMs: 12 });
    expect(m.errorRate).toBeCloseTo(0.025);
  });
  it('leaves viaHosts undefined for a plain edge', () => {
    expect(graphEdgeToMapEdge(e('a', 'b')).viaHosts).toBeUndefined();
  });
});

describe('viaHostsInto (v0.10.1344)', () => {
  it('unions incoming viaHosts, sorted + deduped; outgoing edges ignored', () => {
    const edges: GraphEdge[] = [
      { ...e('card-web-api', 'svc'), viaHosts: ['svc-prod-web.bank.example', 'svc-prod-app.bank.example'] },
      { ...e('other', 'svc'), viaHosts: ['svc-prod-app.bank.example'] },
      { ...e('svc', 'db'), viaHosts: ['nope.example'] },
    ];
    expect(viaHostsInto(edges, 'svc')).toEqual(['svc-prod-app.bank.example', 'svc-prod-web.bank.example']);
    expect(viaHostsInto(edges, 'ext:api.stripe.com')).toEqual([]);
  });
});
