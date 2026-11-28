import { usePivotCtx, withPivotCtx } from '@/lib/pivotContext';
import { useEffect, useMemo, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { timeRangeToNs, fmtNum } from '@/lib/utils';
import { healthToken } from '@/lib/health';
import { encodeRange } from '@/lib/urlState';
import { Spinner, Empty } from '@/components/Spinner';
import { TopologyFlowGraph } from '@/components/TopologyFlowGraph';
import type { TimeRange, ServiceGraphResponse, GraphNode, GraphEdge, ServiceMap, ServiceMapEdge, ServiceMapNode } from '@/lib/types';
import { Button } from '@/components/ui/Button';
import { IconButton } from '@/components/ui/IconButton';
import { SegmentedControl } from '@/components/ui'; // v0.10.914 dilim 2 (buton bütünlüğü)
import { nodeDetailHref } from '@/components/topology/nodeDetailHref';
import { nodeLinkLabel } from '@/components/topology/nodeLinkLabel';
import { ExternalPaths } from '@/components/ExternalPaths';
import { countSecondary, moreNodeId, moreNodeCol, isMoreNode } from '@/lib/topoFocusLayout';
import { EntityLink } from '@/components/coremetry/entity/EntityLink';
import { useT } from '@/lib/i18n';
import { CheckboxField } from '@/components/coremetry/Field/Field';

// FocusedNeighborhood — the focused topology graph (service-detail Topology
// tab). Since v0.8.294 the neighborhood is walked SERVER-side
// (/api/servicegraph?scope=neighborhood&hops=N — same direction-separated
// BFS contract, see neighborhoodKeepSet) so this component no longer
// downloads the entire global graph (20k edges at 1000+ services) for a
// ≤40-node view. The client keeps assignFocusColumns on the returned
// subgraph for the signed caller/dependency columns, and renders via
// TopologyFlowGraph (v0.8.108): pill nodes + bezier edges + hover-blue
// direct-edge emphasis. Toolbar (hops / errors-only) + hover inspector stay.
//
// v0.10.1294 — operatör 2026-10-17 ("oklar çok uzak ve sarmal bir path gibi
// gidiyor"): the signed columns now ARE the x-placement. TopologyFlowGraph
// gets `columns` and switches to the Focus Column Layout (lib/
// topoFocusLayout): fixed columns −2 | −1 | focus | +1 | +2, focus vertically
// centred, one smooth curve per adjacent-column edge, peer/skip/back edges
// behind "Yan bağlantılar (N)" (?tside=1), a per-column "+N more" pill
// (capPerColumn, expand → ?texp=). The nearest-first capPerSide/CAP=40 cut is
// gone with it.

function kindLabel(n: GraphNode): string {
  if (n.kind === 'database') return n.system ? `db · ${n.system}` : 'database';
  if (n.kind === 'queue')    return n.system ? `mq · ${n.system}` : 'queue';
  if (n.kind === 'external') return 'external';
  return 'service';
}
function fmtMs(ms: number): string {
  if (!ms) return '—';
  if (ms >= 1000) return (ms / 1000).toFixed(ms >= 10_000 ? 0 : 1) + 's';
  return Math.round(ms) + 'ms';
}

// assignFocusColumns — the signed-column assignment for the focused graph.
// Returns id → column where 0 = focus, NEGATIVE = callers (upstream, reached
// by walking IN edges) and POSITIVE = dependencies (downstream, OUT edges).
//
// The two directions are walked SEPARATELY and each step keeps its sign. A
// single bidirectional BFS (the pre-v0.8.39 code) summed the ±1 steps along
// the path, so a node reached as caller(-1) → that caller's OTHER dependency
// (+1) landed at column 0 — piling every "sibling" of the focus into the
// focus's own column. Against real data a 2-hop view dumped ~26 nodes at 0
// (operator-reported "the graph won't branch at 2 hops"). Walking upstream
// via IN-only and downstream via OUT-only keeps the fan: callers strictly
// left, deps strictly right, and nothing but the focus at 0. A node reachable
// both ways (a cycle) takes the side with the smaller hop distance.
//
// Since v0.10.1294 the columns are the x-placement again (Focus Column
// Layout) and drive the per-column cap below.

// capPerColumn — v0.10.1294 (replaces v0.9.359 capPerSide). A column with
// more than MAX_PER_COL nodes that the operator has not expanded (?texp=)
// keeps MAX_PER_COL−1: errored first (errorRate > 1%), then calls, then id.
// The rest collapse into a synthetic `more:<col>` node ("+N more"), which the
// layout puts last in its column; edges touching hidden nodes merge into
// aggregate edges (calls/errors/rate summed, p99 max) so the traffic stays
// visible. v0.9.359's invariant survives by construction: every column keeps
// ≥ 11 nodes, so neither callers nor dependencies can be emptied — "nothing
// calls this service" is never fabricated by the cap. Pure + unit-tested.
export const MAX_PER_COL = 12;

export function capPerColumn(
  nodes: readonly GraphNode[], edges: readonly GraphEdge[], col: ReadonlyMap<string, number>,
  focus: string, expanded: ReadonlySet<number>, maxPerCol = MAX_PER_COL,
): {
  nodes: GraphNode[]; edges: GraphEdge[]; col: Map<string, number>;
  /** sütun → gizlenen düğüm sayısı */
  hidden: Map<number, number>;
  collapsed: number;
} {
  const byCol = new Map<number, GraphNode[]>();
  for (const n of nodes) {
    const c = col.get(n.id);
    if (c === undefined || n.id === focus || c === 0) continue;
    (byCol.get(c) ?? byCol.set(c, []).get(c)!).push(n);
  }
  const repOf = new Map<string, string>(); // gizli id → more:<col>
  const moreNodes: GraphNode[] = [];
  const hidden = new Map<number, number>();
  for (const c of [...byCol.keys()].sort((a, b) => a - b)) {
    const list = byCol.get(c)!;
    if (list.length <= maxPerCol || expanded.has(c)) continue;
    const ranked = list.slice().sort((a, b) => {
      const ea = a.errorRate > 1 ? 0 : 1, eb = b.errorRate > 1 ? 0 : 1;
      return (ea - eb) || (b.calls - a.calls) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
    });
    const drop = ranked.slice(maxPerCol - 1);
    const id = moreNodeId(c);
    let calls = 0, errors = 0, rate = 0;
    for (const n of drop) { repOf.set(n.id, id); calls += n.calls; errors += n.errors; rate += n.rate; }
    hidden.set(c, drop.length);
    moreNodes.push({
      id, name: `+${drop.length} more`, kind: 'service',
      calls, errors, errorRate: calls ? errors / calls * 100 : 0, rate,
    });
  }
  const keepNodes = nodes.filter(n => col.has(n.id) && !repOf.has(n.id));
  const outCol = new Map<string, number>();
  for (const n of keepNodes) outCol.set(n.id, col.get(n.id)!);
  for (const m of moreNodes) outCol.set(m.id, moreNodeCol(m.id));
  if (repOf.size === 0) {
    return { nodes: keepNodes, edges: edges.filter(e => outCol.has(e.source) && outCol.has(e.target)), col: outCol, hidden, collapsed: 0 };
  }
  const outEdges: GraphEdge[] = [];
  const agg = new Map<string, GraphEdge & { wAvg: number }>();
  for (const e of edges) {
    const s = repOf.get(e.source) ?? e.source, t = repOf.get(e.target) ?? e.target;
    if (!outCol.has(s) || !outCol.has(t)) continue;
    if (s === e.source && t === e.target) { outEdges.push(e); continue; }
    if (s === t) continue; // aynı sütunun gizlileri arası
    const k = `${s}|${t}`;
    const a = agg.get(k);
    if (!a) {
      agg.set(k, { source: s, target: t, calls: e.calls, errors: e.errors, errorRate: 0, rate: e.rate, avgMs: 0, p99Ms: e.p99Ms, protocol: e.protocol, wAvg: e.avgMs * e.calls });
    } else {
      a.calls += e.calls; a.errors += e.errors; a.rate += e.rate;
      a.wAvg += e.avgMs * e.calls;
      if (e.p99Ms > a.p99Ms) a.p99Ms = e.p99Ms;
      if (a.protocol !== e.protocol) a.protocol = undefined;
    }
  }
  for (const k of [...agg.keys()].sort()) {
    const { wAvg, ...a } = agg.get(k)!;
    a.errorRate = a.calls ? a.errors / a.calls * 100 : 0;
    a.avgMs = a.calls ? wAvg / a.calls : 0;
    outEdges.push(a);
  }
  return {
    nodes: [...keepNodes, ...moreNodes], edges: outEdges, col: outCol, hidden,
    collapsed: [...hidden.values()].reduce((x, v) => x + v, 0),
  };
}

/** ?texp= kodeki (açılmış sütunlar, ör. "-2,2") — sıralı, tekil, sıfırsız tamsayılar. */
export function parseTexp(raw: string | null): Set<number> {
  const out = new Set<number>();
  for (const p of (raw ?? '').split(',')) {
    const t = p.trim();
    if (!/^-?\d+$/.test(t)) continue;
    const n = Number(t);
    if (n !== 0) out.add(n);
  }
  return new Set([...out].sort((a, b) => a - b));
}
export function encodeTexp(cols: ReadonlySet<number>): string {
  return [...cols].sort((a, b) => a - b).join(',');
}

/**
 * externalPinHost — v0.9.1255. Pin'li düğümden, yol kırılımı için
 * sorgulanacak dış host. Kapı DÜĞÜM TÜRÜNDE: yalnız
 * `kind === 'external'` bir host üretir.
 *
 * Kapının varlık sebebi maliyet: /api/external/host'un yol yarısı ham
 * `spans` okuması. Servis / db / queue düğümü pin'lendiğinde o istek
 * ATILMAMALI — atılsaydı her pin bir ham tarama tetiklerdi ve dönen
 * cevap da anlamsız olurdu (servis adı dış host değildir; sunucu
 * tarafındaki GLOBAL NOT IN onu zaten eler, yani bedeli ödeyip BOŞ
 * cevap alırdık).
 *
 * Host = düğümün prefix'i soyulmuş adı (`ext:osbprod` → `osbprod`);
 * sunucu decodeNodeName ile aynı soyma zaten yapıyor, `id` fallback'i
 * yalnız o alan boş gelirse devreye girer.
 */
export function externalPinHost(node: GraphNode | null | undefined): string | null {
  if (!node || node.kind !== 'external') return null;
  const host = node.name || (node.id.startsWith('ext:') ? node.id.slice(4) : node.id);
  return host || null;
}

/**
 * graphEdgeToMapEdge — GraphEdge → ServiceMapEdge (TopologyFlowGraph'ın
 * sözleşmesi). v0.10.1344'te inline adapter'dan çıkarıldı ve TESTLENDİ:
 * bu adapter alan düşürme sınıfının (v0.8.322) evi — yeni bir GraphEdge
 * alanı burada taşınmazsa grafik onu hiç görmez.
 */
export function graphEdgeToMapEdge(e: GraphEdge): ServiceMapEdge {
  return {
    caller: e.source,
    callee: e.target,
    traceCount: e.calls,
    spanCount: e.calls,
    errorCount: e.errors,
    // v0.8.322 — carry the MV's per-edge RED (the v0.8.281 chip
    // contract). This inline adapter dropped them, so the service
    // Topology tab never showed the "N/dk · p99 · err%" chips its
    // /service-map twin renders (TopologyFlowGraph gates the chip on
    // p99Ms != null). errorRate rescales to ServiceMap's 0..1.
    rate: e.rate,
    errorRate: (e.errorRate ?? 0) / 100,
    avgMs: e.avgMs,
    p99Ms: e.p99Ms,
    // v0.10.1344 — gerçek servise katlanmış ext host'ları.
    viaHosts: e.viaHosts,
  };
}

/**
 * graphNodeToMapNode — GraphNode → ServiceMapNode (TopologyFlowGraph'ın
 * sözleşmesi). v0.10.1392'de mapData'nın satır-içi adapter'ından çıkarıldı:
 * CoSRE sohbet kartı (ChatTopologyCard) sekmenin AYNI görünümünü çizer, iki
 * yerde ayrı adapter alan düşürme sınıfını (v0.8.322) geri getirirdi.
 */
export function graphNodeToMapNode(n: GraphNode): ServiceMapNode {
  return {
    service: n.id,
    spanCount: n.calls,
    errorRate: n.errorRate / 100,
    kind: n.kind === 'database' ? 'db'
      : n.kind === 'queue' ? 'queue'
      : n.kind === 'external' || n.kind === 'internal' ? 'external'
      : undefined,
    // v0.8.297 — dep pill'in ana satırı motor adını okur ("oracle",
    // "kafka"); instance/db.name alt satıra iner (depInstanceLabel).
    subkind: (n.kind === 'database' || n.kind === 'queue')
      ? (n.system || n.name)
      : (n.name !== n.id ? n.name : undefined),
    dbName: n.dbName || undefined,
    // v0.8.383 — carry the env annotation so the service tab's
    // neighborhood shows the same env chips as /service-map's focus
    // view (this inline adapter dropping fields is the v0.8.322 bug
    // class).
    env: n.env || undefined,
    // v0.10.2491 — düğüm özeti (çağrı/dk · hata · p99) için.
    rate: n.rate,
  };
}

/**
 * viaHostsInto — v0.10.1344. Düğüme GELEN kenarların viaHosts birleşimi
 * (sıralı, tekil). Sunucu `ext:<host>` kenarını gerçek servise katladığında
 * host'u kenarda bırakır; hover kartı "via <host>" satırını bundan basar.
 * Gerçek dış düğümde kenarlar viaHosts taşımaz → boş, kart "external" kalır.
 */
export function viaHostsInto(edges: readonly GraphEdge[], id: string): string[] {
  const out = new Set<string>();
  for (const e of edges) if (e.target === id) for (const h of e.viaHosts ?? []) out.add(h);
  return [...out].sort();
}

export function assignFocusColumns(edges: GraphEdge[], focus: string, hops: number): Map<string, number> {
  const out = new Map<string, GraphEdge[]>(); // source → edges (downstream)
  const inc = new Map<string, GraphEdge[]>(); // target → edges (upstream)
  for (const e of edges) {
    (out.get(e.source) ?? out.set(e.source, []).get(e.source)!).push(e);
    (inc.get(e.target) ?? inc.set(e.target, []).get(e.target)!).push(e);
  }
  const col = new Map<string, number>([[focus, 0]]);
  const setCloser = (id: string, v: number) => {
    const c = col.get(id);
    if (c === undefined || Math.abs(v) < Math.abs(c)) col.set(id, v);
  };
  // dir 0 = downstream (OUT edges, +hop); dir 1 = upstream (IN edges, -hop).
  for (let dir = 0; dir < 2; dir++) {
    const adj = dir === 0 ? out : inc;
    const sign = dir === 0 ? 1 : -1;
    const neighbour = dir === 0 ? (e: GraphEdge) => e.target : (e: GraphEdge) => e.source;
    let frontier = [focus];
    const seen = new Set([focus]); // per-direction so a cycle can reach both sides
    for (let h = 1; h <= hops; h++) {
      const next: string[] = [];
      for (const id of frontier) {
        for (const e of adj.get(id) ?? []) {
          const nb = neighbour(e);
          if (!seen.has(nb)) { seen.add(nb); setCloser(nb, sign * h); next.push(nb); }
        }
      }
      frontier = next;
    }
  }
  return col;
}

export function FocusedNeighborhood({ range, focus, hops, errorsOnly, onHops, onErrorsOnly, onRecenter, onClear, onDrillFocus }: {
  range: TimeRange;
  focus: string;
  hops: number;
  errorsOnly: boolean;
  onHops: (h: number) => void;
  onErrorsOnly: (v: boolean) => void;
  onRecenter: (svc: string) => void;
  onClear: () => void;
  /** v0.10.2491 — servis düğümüne çift tık: o servisin topology'si, geçmişe
   *  YENİ giriş (tarayıcı geri döndürür). Verilmezse onRecenter. */
  onDrillFocus?: (svc: string) => void;
}) {
  const tn = useT();
  const pctx = usePivotCtx(); // v0.10.1237
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);
  // v0.9.226 — Structure/Flow toggle kaldırıldı (operatör: "sadece flow
  // olan yeterli"). Tek görünüm: MV-kenarlı akış grafiği.
  // hops rides the query key (bounded: server clamps to 1..3, UI offers 1|2)
  // so widening the radius refetches the wider subgraph; 30s server cache.
  // Tek görünüm kaldığı için `enabled` gate'i de kalktı — sorgu artık her
  // zaman çizilen şeyi besliyor.
  const graph = useQuery<ServiceGraphResponse>({
    queryKey: ['servicegraph', 'neighborhood', focus, hops, from, to],
    queryFn: () => api.serviceGraph({ focus, scope: 'neighborhood', hops, from, to }),
    staleTime: 30_000,
  });

  // v0.9.381 (redesign D5, mockup af7419e5) — pin'li inspector: düğüm
  // TIK = kart sabitlenir (📌/✕), hover-only davranış tamamlanır. Recenter
  // artık karttaki buton; tık-recenter kalktı.
  const [pinned, setPinned] = useState<string | null>(null);
  useEffect(() => { setPinned(null); }, [focus]);

  // v0.10.1294 — sütun başına "+N more ▸" (açılan sütunlar ?texp=-2,2) ve
  // "Yan bağlantılar" (?tside=1) URL'de (house rule §4: kopyalanan link aynı
  // görünümü açar). Yerel kopya YOK: durum doğrudan paramdan türer ve yalnız
  // KENDİ param dizesine bağlı memo'dur (sig-guard) — ilgisiz bir URL yazımı
  // (range) açılmış sütunları sıfırlamaz. Yazımlar replace:true, yabancı
  // paramlar korunur. Açma yalnız istemci yeniden-kesimi: kenar kümesi zaten
  // odak-kapsamlı fetch'te, yeni istek YOK.
  const [params, setParams] = useSearchParams();
  const texpRaw = params.get('texp');
  const texp = useMemo(() => parseTexp(texpRaw), [texpRaw]);
  const showSide = params.get('tside') === '1';
  const writeTexp = (next: ReadonlySet<number>) => setParams(prev => {
    const p = new URLSearchParams(prev);
    if (next.size) p.set('texp', encodeTexp(next)); else p.delete('texp');
    return p;
  }, { replace: true });
  const expandColumn = (c: number) => writeTexp(new Set([...parseTexp(params.get('texp')), c]));
  const setShowSide = (v: boolean) => setParams(prev => {
    const p = new URLSearchParams(prev);
    if (v) p.set('tside', '1'); else p.delete('tside');
    return p;
  }, { replace: true });
  // Odak değişince açılmış sütunlar başka bir servisin sütunlarıdır — düşer.
  const prevFocus = useRef(focus);
  useEffect(() => {
    if (prevFocus.current === focus) return;
    prevFocus.current = focus;
    if (texpRaw) writeTexp(new Set());
    // eslint-disable-next-line react-hooks/exhaustive-deps -- yalnız odak geçişinde
  }, [focus]);

  // ── signed columns + per-column cap over the server-walked subgraph ──
  const nb = useMemo(() => {
    const allNodes = new Map<string, GraphNode>();
    for (const n of graph.data?.nodes ?? []) allNodes.set(n.id, n);
    const edges = (graph.data?.edges ?? []).filter(e => !errorsOnly || e.errorRate > 1);
    // Signed-column assignment — extracted + unit-tested (assignFocusColumns
    // above). Walks callers (upstream) and deps (downstream) as SEPARATE
    // directional BFS so a caller's other dependency no longer collapses onto
    // the focus column (the pre-v0.8.39 "won't branch at 2 hops" bug).
    const col0 = assignFocusColumns(edges, focus, hops);
    // v0.10.1294 — sütun başına tavan (capPerColumn): kalabalık sütun "+N
    // more" düğümüne iner; hiçbir taraf sıfırlanamaz (v0.9.359 değişmezi).
    const inCol = [...col0.keys()].map(id => allNodes.get(id)).filter((n): n is GraphNode => !!n);
    const capped = capPerColumn(inCol, edges, col0, focus, texp);
    const { nodes, col, collapsed } = capped;
    const shown = capped.edges;
    // node p99 ≈ max p99 of its incoming edges (fall back to outgoing).
    const p99Of = (id: string) => {
      let m = 0;
      for (const e of shown) if (e.target === id && e.p99Ms > m) m = e.p99Ms;
      if (!m) for (const e of shown) if (e.source === id && e.p99Ms > m) m = e.p99Ms;
      return m;
    };
    const secondary = countSecondary(shown.map(e => ({ caller: e.source, callee: e.target })), col);
    return { nodes, edges: shown, col, collapsed, p99Of, secondary };
  }, [graph.data, focus, hops, errorsOnly, texp]);

  const [hover, setHover] = useState<string | null>(null);
  // v0.10.2491 — kenar etiketi kipi: varsayılan vurguda/sabitte, "hep" seçenek.
  const [labelsAlways, setLabelsAlways] = useState(false);

  // v0.9.1255 — pin'li düğüm + hover düğümü ERKEN türetiliyor (eskiden
  // erken-return'lerin ALTINDAydı). Sebep: dış düğümün yol kırılımı bir
  // hook ve hook koşullu olamaz; gate'in girdisi de pin'li düğümün ta
  // kendisi.
  const pinnedNode = pinned && !isMoreNode(pinned) ? nb.nodes.find(n => n.id === pinned) : null;
  const hoverNode = pinnedNode ?? (hover && !isMoreNode(hover) ? nb.nodes.find(n => n.id === hover) : null);

  // v0.9.1255 — Operator-reported: dış düğümde host değil hangi UÇ
  // olduğu anlamlı. Kırılım PIN'de yükleniyor, HOVER'da değil: hover
  // grafikte gezinirken düğüm başına bir ham-spans okuması tetiklerdi
  // (ES/CH maliyet disiplini — "aç/genişlet üzerine fetch"). Hover
  // davranışı bu yüzden aynen duruyor, kart yalnız SABİTLENİNCE
  // zenginleşiyor.
  //
  // queryKey /external çekmecesiyle BİREBİR aynı → aynı hostu iki
  // yüzeyde de açmak tek istek. staleTime = sunucu TTL'i (30s).
  const hoverVia = hoverNode ? viaHostsInto(nb.edges, hoverNode.id) : [];
  const extHost = externalPinHost(pinnedNode);
  const extDetail = useQuery({
    queryKey: ['external-host', extHost, from, to],
    queryFn: () => api.externalHost(extHost!, from, to),
    enabled: !!extHost,
    staleTime: 30_000,
  });

  // GraphNode/GraphEdge → ServiceMap adapter (TopologyFlowGraph's contract).
  // errorRate: /api/servicegraph returns PERCENT, ServiceMapNode is a
  // FRACTION (the component thresholds at 0.05 / 0.01). subkind carries the
  // prefix-decoded display name so dep pills read "h2", not "db:h2".
  const mapData = useMemo<ServiceMap>(() => ({
    // sampledFrom/totalSpans are /api/service-map sampling metadata —
    // required by the type, not consumed by TopologyFlowGraph.
    sampledFrom: 0,
    totalSpans: 0,
    nodes: nb.nodes.map(graphNodeToMapNode),
    edges: nb.edges.map(graphEdgeToMapEdge),
  }), [nb]);

  if (graph.isLoading) return <div style={{ padding: 60, display: 'grid', placeItems: 'center' }}><Spinner /></div>;

  // v0.9.958 (G3-b) — düğümün detay/katalog hedefi. null = kimlik
  // türetilemedi; o hâlde link HİÇ çizilmez (uydurma bir instance ile
  // sorgulamak sessizce boş bir sayfa açardı).
  // v0.9.972 — kuyruk düğümleri de artık bir hedef üretiyor (katalog),
  // o yüzden ad `dbHref` değil.
  const detailHref = hoverNode ? nodeDetailHref(hoverNode, { range: encodeRange(range) }) : null;
  const height = Math.round(window.innerHeight * 0.74);

  // v0.9.363 — 500, CH max_execution_time timeout'u ve gerçekten komşusuz
  // servis AYNI boş tuvali üretiyordu; operatör hangisine baktığını
  // bilemiyordu. Başarısızlık ve boşluk artık kendini söylüyor.
  if (graph.isError) {
    return (
      <Empty icon="⚠" title="Topoloji yüklenemedi">
        <span className="mono">{String(graph.error)}</span>
      </Empty>
    );
  }
  if (!graph.isPending && nb.nodes.length === 0) {
    return (
      <Empty icon="◇" title={`${focus} için topoloji verisi yok`}>
        Yalnız trace'i Coremetry'ye ULAŞAN çağıran ve bağımlılıklar burada
        görünebilir — enstrümante edilmemiş bir istemci buradan görünmezdir.
      </Empty>
    );
  }

  return (
    <div style={{ position: 'relative' }}>
      {/* ── toolbar ─────────────────────────────────────────────────────── */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', marginBottom: 10 }}>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, padding: '4px 8px', borderRadius: 6, background: 'var(--bg2)', border: '1px solid var(--border)', fontSize: 12, fontWeight: 600 }}>
          <span style={{ width: 8, height: 8, borderRadius: '50%', background: healthToken(focusErr(nb.nodes, focus)) }} />
          {focus}
          <Button variant="ghost" size="sm" onClick={onClear}
            title="Back to the service picker" style={{ marginLeft: 2 }}>✕</Button>
        </span>
        <>
            {/* v0.9.868 (tutarlılık denetimi BB1) — `.seg` ve `.on` HİÇBİR
                CSS'te tanımlı değildi. Sonuç: iki buton element-seviyesi
                global `button` kuralına düşüyor (dolu mavi primary) ve AKTİF
                HOP SEÇİMİ GÖRSEL OLARAK AYIRT EDİLEMİYORDU — topolojide "kaç
                hop bakıyorum" sorusunun tek cevabı bu çift. Ev deseni
                `.segmented` + `.active` (globals.css:510-537). */}
            <SegmentedControl aria-label="Komşuluk derinliği" value={String(hops)}
              onChange={v => onHops(v === '2' ? 2 : 1)}
              options={[{ value: '1', label: '1 hop' }, { value: '2', label: '2 hops' }]} />
            <label className="topo-tb-check">
              <input type="checkbox" checked={errorsOnly} onChange={e => onErrorsOnly(e.target.checked)} /> Errors only
            </label>
            {/* v0.10.1294 — peer/skip/back kenarlar varsayılan GİZLİ: "kim kimi
                çağırıyor" tek bakışta okunsun. Sayı her zaman görünür, hover
                edilen düğüm kendi yan bağlantılarını soluk gösterir. */}
            <label className="topo-tb-check"
              title="Aynı sütun içi, sütun atlayan ve döngü (geri) kenarları — varsayılan gizli; hover edilen düğümün kendi yan bağlantıları soluk görünür">
              <input type="checkbox" checked={showSide} onChange={e => setShowSide(e.target.checked)} /> Yan bağlantılar ({nb.secondary})
            </label>
            <span title={tn('topo.labelsAlwaysTitle')}>
              <CheckboxField label={tn('topo.labelsAlways')} checked={labelsAlways} onCheckedChange={setLabelsAlways} />
            </span>
            <span className="topo-tb-count">
              {nb.nodes.filter(n => !isMoreNode(n.id)).length} of {graph.data?.nodes.length ?? 0} nodes · {nb.edges.length} edges
              {nb.collapsed > 0 && <> · {nb.collapsed} gizli (sütun başına “+N more ▸”)</>}
            </span>
            {texp.size > 0 && (
              <Button variant="ghost" size="xs" onClick={() => writeTexp(new Set())}
                title="Açılmış sütunları yeniden “+N more” altında topla">Sütunları daralt</Button>
            )}
        </>
      </div>

      {/* ── flow graph canvas ───────────────────────────────────────────── */}
      {/* v0.10.1294 — `columns` → Focus Column Layout (lib/topoFocusLayout):
          sabit sütunlar, odak ortada, akış kenarları durağan (animasyon yalnız
          hover'da). */}
      <TopologyFlowGraph
        data={mapData}
        focus={focus}
        hoverNode={hover}
        onHoverNode={setHover}
        onSelectNode={id => {
          if (id === focus) return;
          setPinned(p => (p === id ? null : id));
        }}
        height={height}
        dropMessaging={false}
        columns={nb.col}
        animate="hover"
        showSecondary={showSide}
        onExpandColumn={expandColumn}
        pinnedNode={pinned}
        onClearPin={() => setPinned(null)}
        edgeLabels={labelsAlways ? 'always' : 'hover'}
        onOpenNode={id => {
          const n = nb.nodes.find(x => x.id === id);
          if (!n || n.kind !== 'service' || id === focus) return;
          setHover(null);
          (onDrillFocus ?? onRecenter)(n.name);
        }}
        legend={<>
          {/* v0.10.929 (K5) — lejant healthToken ile aynı: sağlıklı nötr. */}
          <Kind c="var(--text3)" l="healthy" /><Kind c="var(--warn)" l=">1% err" /><Kind c="var(--err)" l=">5% err" />
          {/* v0.10.1294 — kenar türleri; namespace renklerini grafik ekler. */}
          <EdgeKind kind="call" l="çağrı" /><EdgeKind kind="back" l="döngü" /><EdgeKind kind="new" l="yeni" />
        </>}
      />

      {/* ── hover inspector ─────────────────────────────────────────────── */}
      {/* v0.9.1255 — kart yalnız PIN'li dış düğümde genişliyor: yol
          tablosu 240px'e sığmıyor ve sığdırmak için yolu daha da
          kırpmak, kırpmanın çözdüğü sorunu geri getirirdi. */}
      {hoverNode && (
        <div style={{ position: 'absolute', left: 10, bottom: 44, zIndex: 5, width: extHost ? 300 : 240, padding: 12, borderRadius: 8, background: 'var(--bg2)', border: '1px solid var(--border)', boxShadow: '0 6px 20px rgba(0,0,0,.28)', fontSize: 12 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 7, marginBottom: 6 }}>
            <span style={{ width: 9, height: 9, borderRadius: '50%', background: healthToken(hoverNode.errorRate) }} />
            <span style={{ fontWeight: 700, color: 'var(--text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{hoverNode.name}</span>
            {pinnedNode && <span title="Sabitlendi — ✕ ile bırak">📌</span>}
            {/* v0.9.958 (UX denetimi G3/Ö10) — Recenter yalnız SERVİS
                düğümünde. onRecenter düğüm adını servis adı sanıp
                `/service?name=oracle@oracle` açıyordu: var olmayan bir
                sayfa, ve db/queue pill'inde görünen TEK eylem buydu.
                Kapatılan şey bir özellik değil, yanlış bir vaat. */}
            {hoverNode.kind === 'service' && (
              <Button variant="secondary" size="sm" onClick={() => onRecenter(hoverNode.name)} style={{ marginLeft: 'auto' }}>Recenter</Button>
            )}
            {pinnedNode && (
              <IconButton variant="ghost" size="sm" aria-label="Unpin node"
              tooltip="Unpin" onClick={() => setPinned(null)} icon="✕" />
            )}
          </div>
          <div style={{ fontSize: 10, color: 'var(--text3)', fontFamily: 'var(--font-mono)', marginBottom: 6 }}>
            {kindLabel(hoverNode)} · {hoverNode.kind === 'service' ? 'service.name' : hoverNode.system ? (hoverNode.kind === 'database' ? 'db.system' : 'messaging.system') : hoverNode.kind}
            {hoverNode.kind === 'database' && hoverNode.dbName ? ` · db.name=${hoverNode.dbName}` : ''}
          </div>
          {/* v0.10.1344 — servis, ext host'u (k8s gRPC / GSLB adı) üstünden
              çağrıldıysa o host tek soluk satırda; gerçek dış düğümde boş. */}
          {hoverVia.length > 0 && (
            <div className="mono" title={hoverVia.join('\n')}
              style={{ fontSize: 10, color: 'var(--text3)', marginBottom: 6, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              via {hoverVia[0]}{hoverVia.length > 1 ? ` +${hoverVia.length - 1}` : ''}
            </div>
          )}
          <div className="grid-3" style={{ display: 'grid', gap: 6, marginBottom: 8 }}>
            {/* v0.9.367 — sayının HANGİ popülasyondan geldiği görünür:
                ↓ inbound (normal), ↑ outbound = giriş-servis fallback'i;
                gateway'in ERR'i bağımlılıklarının hatası olabilir. */}
            <Stat l={hoverNode.callsBasis === 'outbound' ? 'CALLS ↑' : 'CALLS ↓'} v={fmtNum(hoverNode.calls)} />
            <Stat l="P99" v={fmtMs(nb.p99Of(hoverNode.id))} />
            <Stat l={hoverNode.callsBasis === 'outbound' ? 'ERR ↑' : 'ERR'} v={`${hoverNode.errorRate.toFixed(1)}%`} tone={healthToken(hoverNode.errorRate)} />
          </div>
          {hoverNode.callsBasis === 'outbound' && (
            <div style={{ fontSize: 10, color: 'var(--text3)', marginBottom: 8 }}>
              ↑ enstrümante çağıranı yok — sayılar bu servisin YAPTIĞI
              çağrılardan (bağımlılıklarının döndürdükleri).
            </div>
          )}
          {hoverNode.kind === 'service' && (
            <span style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
              <EntityLink to={{ kind: 'service', name: hoverNode.name, opts: { range } }} style={{ fontSize: 11, textDecoration: 'none' }}>Open service →</EntityLink>
              {/* v0.9.381 (D5) — kenar pivotunun düğüm hali: odak VE bu
                  komşuyu birlikte İÇEREN trace'ler (Traces'ın mevcut
                  ?services= requireServices filtresi). Etiket bilerek
                  "içeren" der — doğrudan kenar garantisi yok. */}
              {hoverNode.name !== focus && (
                <Link
                  to={withPivotCtx(`/traces?services=${encodeURIComponent(focus)},${encodeURIComponent(hoverNode.name)}&range=${encodeRange(range)}&view=list&rootOnly=false`, pctx, { carry: ['env'] })}
                  title="İki servisi birlikte içeren trace'ler — doğrudan kenar garantisi değil"
                  style={{ fontSize: 11, textDecoration: 'none' }}>
                  Traces ({focus} ∧ {hoverNode.name}) →
                </Link>
              )}
            </span>
          )}
          {/* v0.9.958 (UX denetimi G3-b/Ö10) — DB düğümünün detay çıkışı.
              CANLI VERİYLE doğrulandı: düğüm adı `<system>@<instance>` ve
              o instance /api/databases satırıyla birebir eşleşiyor.
              dbName BİLEREK taşınmıyor — düğüm instance düzeyinde
              toplanmış, taşıdığı dbName yalnız bir örnek; linke koymak
              soruyu sessizce daraltırdı. Etiket bu yüzden "instance" der.
              v0.9.972 — kuyruk düğümü artık KATALOĞA köprülüyor:
              çekmece kimliği cluster ister ve düğüm taşımaz, ama katalog
              sahip olunan boyutlarla (msys + q) daraltılabilir. Etiket
              "topiği aç" DEMEZ — katalogda ilk-200 tavanı var, aranan
              satır listede olmayabilir. */}
          {hoverNode.kind === 'database' && detailHref && (
            <span style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
              <Link to={detailHref} style={{ fontSize: 11, textDecoration: 'none' }}>
                {/* v0.9.1337 — karar nodeLinkLabel.ts'e taşındı ve TESTLENDİ.
                    Buradayken hiçbir testi yoktu: v0.9.1326'da ölçüldü,
                    `/databases` dalını ters çevirmek tüm suite'i yeşil
                    bırakıyordu. İlke değişmedi (v0.9.1026 + v0.9.1326):
                    etiket LİNKİN NE YAPTIĞINI söyler. */}
                {nodeLinkLabel('database', detailHref)}
              </Link>
            </span>
          )}
          {hoverNode.kind === 'queue' && detailHref && (
            <span style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
              <Link to={detailHref} style={{ fontSize: 11, textDecoration: 'none' }}>
                {/* v0.9.1337 — karar nodeLinkLabel.ts'te (db kardeşiyle
                    aynı dosya, aynı ilke). v0.9.1026'nın dürüstlük kararı
                    değişmedi, yalnız artık testli. */}
                {nodeLinkLabel('queue', detailHref)}
              </Link>
            </span>
          )}
          {/* v0.9.1255 — dış düğümün İÇİ. Düğüm host-anahtarlı kalıyor
              (url bazlı düğüm grafiği binlerce düğüme patlatırdı); bu
              blok "esbprod.example.internal altında HANGİ uç" sorusunu
              cevaplıyor. Yalnız PIN'de: extHost gate'i pinnedNode'dan
              türüyor, hover hiçbir istek atmıyor. */}
          {extHost && (
            <div style={{ marginTop: 8, paddingTop: 8, borderTop: '1px solid var(--divider)' }}>
              <div style={{ fontSize: 10, color: 'var(--text3)', letterSpacing: '0.3px', marginBottom: 4 }}>
                EN ÇOK ÇAĞRILAN YOLLAR
              </div>
              {extDetail.isPending ? <Spinner /> : (
                <ExternalPaths
                  dense
                  limit={5}
                  paths={extDetail.data?.paths}
                  error={extDetail.isError ? String(extDetail.error) : extDetail.data?.pathsError}
                  windowS={extDetail.data?.pathsWindowS}
                />
              )}
              <Link to={`/external?host=${encodeURIComponent(extHost)}&range=${encodeRange(range)}`}
                style={{ fontSize: 10.5, textDecoration: 'none' }}>
                Dış bağımlılık detayı →
              </Link>
            </div>
          )}
        </div>
      )}

      {/* ── legend: v0.10.1294'den beri tuvalin içinde tek satır (TopologyFlowGraph `legend`) ── */}

      {/* ── footer caption ──────────────────────────────────────────────── */}
      <div style={{ position: 'absolute', right: 10, bottom: 10, zIndex: 3, maxWidth: '62%', fontSize: 9.5, color: 'var(--text3)', textAlign: 'right', lineHeight: 1.4 }}>
        Built from OTel span semantics — nodes by service.name, type from db.system/messaging.system, edges from CLIENT→SERVER spans.
        {/* v0.9.374 — grafiğin NEGATİF uzayı da beyan: "payments'ı kimse
            çağırmıyor" ile "çağıranı enstrümante değil" aynı görüntüydü ve
            operatörün ilk şüphesi kapsama değil servis oluyordu. */}
        Only callers/dependencies whose traces reach Coremetry appear — an uninstrumented client is invisible here; operator-hidden patterns (Settings → Topology) are excluded.
        Callers left, dependencies right · click a node or edge to pin its highlight · double-click a service to open its topology · hover for its path to the focus · dashed pill = external dependency.
      </div>
    </div>
  );
}

function focusErr(nodes: GraphNode[], focus: string): number {
  return nodes.find(n => n.id === focus)?.errorRate ?? 0;
}
function Kind({ c, l }: { c: string; l: string }) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
      <span style={{ width: 8, height: 8, borderRadius: '50%', background: c }} />{l}
    </span>
  );
}
// v0.10.1294 — lejant kenar örneği: çizgi deseni TopologyFlowGraph'ın
// kenarıyla aynı (düz çağrı, noktalı döngü .back, 12 4 yeni).
function EdgeKind({ kind, l }: { kind: 'call' | 'back' | 'new'; l: string }) {
  return (
    <span className="topo-lg-edge">
      <svg width="22" height="8" aria-hidden="true">
        <line x1="1" y1="4" x2="21" y2="4" className={'topo-lg-line ' + kind} />
      </svg>{l}
    </span>
  );
}
function Stat({ l, v, tone }: { l: string; v: string; tone?: string }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column' }}>
      <span className="mono" style={{ fontWeight: 700, color: tone ?? 'var(--text)' }}>{v}</span>
      <span style={{ fontSize: 8.5, color: 'var(--text3)', letterSpacing: '0.4px' }}>{l}</span>
    </div>
  );
}
