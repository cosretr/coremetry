// problemGraph.ts — v0.10.2274 (entity grafı ile Problem birleştirme S1,
// docs/problems/entity-graph-merge-design.md): gölge bağ blobunun tipleri
// ve admin raporunun SAF metin yardımcıları. Kural kimlikleri (M-*, N-*)
// sunucuyla sözleşmedir; cümleler burada kimlikten kurulur.

import { t as tr, tf, type Lang } from './i18n';

export const GRAPH_LINK_RULES = ['M-SVC', 'M-CALL', 'M-DEP', 'M-WL', 'M-NODE', 'M-HOST'] as const;
export const GRAPH_NOLINK_REASONS = ['N-REL', 'N-TIME', 'N-CAP', 'N-HUB', 'N-KIND', 'N-SIZE', 'N-GRAPH'] as const;

export interface ProblemGraphMember {
  id: string;
  entity: string;
  type: string; // service | database | queue | external
  name: string;
  ruleId?: string;
  title?: string;
  severity?: string;
  startedAt: number; // unix ns
  owner?: boolean;
  link?: string;
  peer?: string;
  via?: string;
  path?: string[];
  strict?: boolean;
  graphMissing?: boolean;
  late?: boolean; // v0.10.2341 (S4, yalnız açıkken): aynı servis, emme sınırından sonra
}

export interface ProblemGraphNearMiss {
  id: string;
  name: string;
  type: string;
  startedAt: number;
  reason: string;
  hint?: string;
  peer?: string;
  via?: string;
  groupSize?: number;
}

export interface ProblemGraphReads {
  passes: number; skippedFew: number; refreshes: number; relations: number;
  maxRelPerPass: number; runs: number; evidence: number; errors: number;
  rollouts?: number; blast?: number; // v0.10.2278 (S2)
}

export interface ProblemGraphShadowReport {
  at: number; since: number; windowH: number; units: number;
  links?: Record<string, number>;
  callStrict: number;
  noLink?: Record<string, number>;
  sizes?: Record<string, number>;
  sizesStrict?: Record<string, number>;
  age?: Record<string, number>;
  ageByRule?: Record<string, Record<string, number>>;
  graphMissing: number;
  familyOff?: Record<string, number>;
  originWins?: Record<string, number>; // v0.10.2278 (S2)
  originUnclear?: number;
  originConf?: Record<string, number>;
  reads: ProblemGraphReads;
  truncated?: boolean;
  frequentReport?: FrequentReport; // v0.10.2280 (S3)
  mergeReport?: ProblemGraphMergeReport; // v0.10.2341 (S4)
}

/** v0.10.2341 (S4) — sonradan bağlanan üyelerin 24 sa sayaçları. */
export interface ProblemGraphMergeReport {
  enforce: boolean; joined: number; wouldHold: number; heldBecameP1: number;
  becameP1?: { id: string; name: string; family?: string }[]; late: number;
}

/** v0.10.2341 — uygulama dilimlerinin kod sabitleri ve geçiş ölçütü sayıları. */
export interface ProblemGraphModes { s4: boolean; s5: boolean }
export interface ProblemGraphCriterion {
  windowStart: number; mergeJoined: number; mergeWouldHold: number; mergeHeldP1: number;
  frequentWouldHold: number; frequentHeldP1: number; late: number;
}

export interface ProblemGraphTopGroup {
  ownerId: string; ownerName: string; entities: number; startedAt: number; rules: Record<string, number>;
  origin?: string; confidence?: string; // v0.10.2278 (S2)
}

export interface ProblemGraphShadowResponse {
  available: boolean;
  report: ProblemGraphShadowReport;
  largest: ProblemGraphTopGroup[];
  modes?: ProblemGraphModes; // v0.10.2341
  criterion?: ProblemGraphCriterion;
}

export const ORIGIN_RULES = ['K-ROLLOUT', 'K-SHARED', 'K-DEEP', 'K-FIRST'] as const;
const CONF_KEYS = new Set(['strong', 'weak', 'unclear']); // metin: pgShadow.conf.*

const SIZE_ORDER = ['2', '3', '4-5', '6-10', '11-25'];
const AGE_ORDER = ['0-30', '30-60', '60-90', '>90'];

function kv(m: Record<string, number> | undefined, order: readonly string[], fmt: (k: string) => string = k => k, lang: Lang = 'tr'): string {
  const parts = order.filter(k => (m?.[k] ?? 0) > 0).map(k => `${fmt(k)} ${m![k]}`);
  return parts.length ? parts.join(' · ') : tr('pgShadow.none2', lang);
}

/** SAF: admin gölge raporunun satırları (Anomalies › ML › Teknik detay). */
// v0.10.2282 — satırlar arayüz dilinde (lib/i18n pgShadow.*; TR metin aynı, varsayılan 'tr').
export function graphShadowLines(r: ProblemGraphShadowReport, lang: Lang = 'tr'): string[] {
  const L = (k: string, v: Record<string, string | number>) => tf(`pgShadow.l.${k}`, lang, v);
  const ent = (k: string) => tf('pgShadow.entities', lang, { c: k });
  const since = new Date(r.since * 1000);
  const hm = `${String(since.getHours()).padStart(2, '0')}:${String(since.getMinutes()).padStart(2, '0')}`;
  const off = r.familyOff && Object.keys(r.familyOff).length
    ? Object.keys(r.familyOff).sort().map(k => `${k} ${r.familyOff![k]}`).join(' · ')
    : tr('pgShadow.none2', lang);
  const rd = r.reads;
  return [
    L('window', { w: r.windowH, hm, u: r.units }),
    L('links', { links: kv(r.links, GRAPH_LINK_RULES, undefined, lang), cs: r.callStrict }),
    L('noLink', { nl: kv(r.noLink, GRAPH_NOLINK_REASONS, undefined, lang) }),
    L('sizes', { s: kv(r.sizes, SIZE_ORDER, ent, lang), ss: kv(r.sizesStrict, SIZE_ORDER, ent, lang) }),
    L('age', { a: kv(r.age, AGE_ORDER, k => L('ageMin', { k }), lang) }),
    L('missing', { g: r.graphMissing, off }),
    ...frequentReportLines(r.frequentReport, lang),
    L('origin', { w: kv(r.originWins, ORIGIN_RULES, undefined, lang), u: r.originUnclear ?? 0, c: kv(r.originConf, ['strong', 'weak', 'unclear'], k => (CONF_KEYS.has(k) ? tr(`pgShadow.conf.${k}`, lang) : k), lang) }),
    L('reads', { p: rd.passes, sf: rd.skippedFew, r: rd.refreshes, rel: rd.relations, mx: rd.maxRelPerPass, runs: rd.runs, ev: rd.evidence, ro: rd.rollouts ?? 0, b: rd.blast ?? 0, e: rd.errors }),
  ];
}

// ── v0.10.2275 — Problem detayı "Neden tek Problem" (gölge) ─────────────

// ── v0.10.2279 (S2) — olası köken, etki alanı, köken kanıtı ────────────

export interface ProblemGraphOriginRule {
  rule: string; // K-*
  separated: boolean;
  off?: boolean;
  leader?: string;
  others?: string[];
  via?: string;
  at?: number;     // unix ns
  runner?: string;
  runAt?: number;  // unix ns
  source: string;  // rollout | calls | dep_events | onset
}

export interface ProblemGraphBlast {
  affected: string[];
  atRisk1?: string[]; atRisk1N: number;
  atRisk2?: string[]; atRisk2N: number;
  cut?: boolean; off?: boolean; namesDrop?: boolean;
}

export interface ProblemGraphOrigin {
  entity?: string; name?: string; type?: string;
  confidence: 'strong' | 'weak' | 'unclear' | string;
  rule?: string;
  leads?: string[];
  onsetAt?: number;
  earliest: string;
  earliestAt: number;
  rules: ProblemGraphOriginRule[];
  blast?: ProblemGraphBlast;
}

export interface ProblemGraphResponse {
  problemId: string;
  shadow: boolean;
  at?: number;
  ownerId?: string;
  entities: number;
  members: ProblemGraphMember[];
  nearMisses: ProblemGraphNearMiss[];
  truncated?: boolean;
  origin?: ProblemGraphOrigin; // v0.10.2278 (S2)
  frequent?: FrequentVerdict;  // v0.10.2280 (S3) — yalnız sık ise
}

/** Çeviri işlevi (tf'nin dil bağlanmış hali) — saf yardımcılar dile bağlı değil. */
export type GraphT = (key: string, vars?: Record<string, string | number>) => string;

/** SAF: bölümün kipi — üyeler tablosu, yalnız bağlanmayanlar bloğu ya da hiçbir şey. */
export function graphSectionMode(d: ProblemGraphResponse | null | undefined): 'members' | 'near' | null {
  if (!d) return null;
  if (d.members.length > 1) return 'members';
  if (d.nearMisses.length > 0) return 'near';
  return null;
}

/** SAF: varlık türü etiketi. */
export function graphTypeLabel(type: string, t: GraphT): string {
  return ['service', 'database', 'queue', 'external'].includes(type) ? t(`pg.type.${type}`) : type;
}

/** SAF: birleşme gerekçesi cümlesi — kural kimliğinden ve adlardan kurulur. */
export function graphLinkText(m: ProblemGraphMember, t: GraphT): string {
  if (m.owner) return t('pg.rule.owner');
  const caller = m.path?.[0] ?? m.peer ?? '';
  const callee = m.path?.[m.path.length - 1] ?? m.name;
  switch (m.link) {
    case 'M-SVC': return t('pg.rule.M-SVC');
    case 'M-CALL': return t('pg.rule.M-CALL', { a: caller, b: callee }) + (m.strict ? t('pg.rule.strict') : '');
    case 'M-DEP': return t('pg.rule.M-DEP', { via: m.via ?? '' });
    case 'M-WL': return t('pg.rule.M-WL', { peer: m.peer ?? '', via: m.via ?? '' });
    case 'M-NODE': return t('pg.rule.M-NODE', { via: m.via ?? '' });
    case 'M-HOST': return t('pg.rule.M-HOST', { via: m.via ?? '' });
  }
  return m.link ?? '';
}

/** SAF: graftaki yol ("checkout-api → orders-db"); yoksa "—". */
export function graphPathText(m: ProblemGraphMember): string {
  return m.path && m.path.length > 1 ? m.path.join(' → ') : '—';
}

/** SAF: bağlanmama nedeni cümlesi (N-* kimliği + ilişki ipucu). */
export function graphReasonText(n: ProblemGraphNearMiss, t: GraphT): string {
  if (n.reason === 'N-REL' && n.hint && ['M-DEP', 'M-NODE', 'M-HOST'].includes(n.hint)) {
    return t(`pg.hint.${n.hint}`, { via: n.via ?? '' });
  }
  if (GRAPH_NOLINK_REASONS.includes(n.reason as (typeof GRAPH_NOLINK_REASONS)[number])) {
    return t(`pg.reason.${n.reason}`, { via: n.via ?? '' });
  }
  return n.reason;
}

/** SAF: bölüm alt başlığı sayıları (varlık, kural). */
export function graphCounts(d: ProblemGraphResponse): { entities: number; rules: number } {
  const rules = new Set(d.members.map(m => m.link).filter((l): l is string => !!l));
  return { entities: d.entities, rules: rules.size };
}

// ── v0.10.2279 (S2) — köken cümlesi, kanıt listesi, etki alanı, liste alt satırı ──

/** SAF: güven rozeti metni. */
export function originConfText(o: ProblemGraphOrigin, t: GraphT): string {
  if (o.confidence === 'strong') return t('pg.conf.strong', { n: o.leads?.length ?? 2 });
  if (o.confidence === 'weak') return t('pg.conf.weak');
  return t('pg.conf.unclear');
}

/** SAF: manşet altı köken cümlesi (kural kimliklerinden). */
export function originLineText(o: ProblemGraphOrigin, t: GraphT, clock: (ns: number) => string): string {
  if (o.confidence === 'unclear' || !o.name) return t('pg.origin.unclear', { name: o.earliest, time: clock(o.earliestAt) });
  const why = (o.leads ?? []).map(id => {
    const r = o.rules.find(x => x.rule === id);
    switch (id) {
      case 'K-ROLLOUT': return t('pg.origin.why.K-ROLLOUT', { via: r?.via ?? '', time: r?.at ? clock(r.at) : '' });
      case 'K-SHARED': return t('pg.origin.why.K-SHARED', { others: (r?.others ?? []).join(', ') });
      case 'K-DEEP': return t('pg.origin.why.K-DEEP');
      case 'K-FIRST': return t('pg.origin.why.K-FIRST');
    }
    return id;
  });
  const first = o.onsetAt ? t('pg.origin.first', { time: clock(o.onsetAt) }) : '.';
  return t('pg.origin.line', { name: o.name, why: why.join(t('pg.origin.and')) }) + first;
}

/** SAF: köken kanıtı satırının cümlesi. */
export function originRuleText(r: ProblemGraphOriginRule, t: GraphT, clock: (ns: number) => string): string {
  if (r.off) return t('pg.ev.off');
  const others = (r.others ?? []).join(', ');
  const v = { leader: r.leader ?? '', others, via: r.via ?? '', time: r.at ? clock(r.at) : '',
    t1: r.at ? clock(r.at) : '', runner: r.runner ?? '', t2: r.runAt ? clock(r.runAt) : '' };
  if (r.separated) return t(`pg.ev.${r.rule}.sep`, v);
  if (r.rule === 'K-FIRST') return r.runner ? t('pg.ev.K-FIRST.tie', v) : t('pg.ev.K-FIRST.one', v);
  if ((r.rule === 'K-ROLLOUT' || r.rule === 'K-DEEP') && others) return t(`pg.ev.${r.rule}.many`, v);
  return t(`pg.ev.${r.rule}.none`, v);
}

/** SAF: kanıt kaynağı etiketi. */
export function originSourceText(src: string, t: GraphT): string {
  return ['rollout', 'calls', 'dep_events', 'onset'].includes(src) ? t(`pg.src.${src}`) : src;
}

/** SAF: ilk N ad + "+M". */
export function namesPlus(names: string[] | undefined, total: number): string {
  const ns = names ?? [];
  const rest = total - ns.length;
  return ns.join(', ') + (rest > 0 ? `${ns.length ? ' ' : ''}+${rest}` : '');
}

export interface ProblemGraphRowSummary {
  origin?: string; confidence: string; earliest?: string; entities: number; events: number;
  frequent?: { kind: string; prior24h: number; days7d: number }; // v0.10.2280 (S3)
}
export interface ProblemGraphSummaryResponse {
  shadow: boolean; at?: number; rows: Record<string, ProblemGraphRowSummary>;
  frequentEnforce?: boolean; // v0.10.2342 (S5): yalnız açıkken gelir
}

/** SAF: Problems listesi "Ne bozuk" alt satırı; tek servisli Problem'de null. */
export function graphRowSubText(s: ProblemGraphRowSummary | undefined, t: GraphT): string | null {
  if (!s || s.entities < 2) return null;
  if (s.confidence === 'unclear' || !s.origin) return t('pg.row.unclear', { name: s.earliest ?? '', n: s.entities, e: s.events });
  return t('pg.row.origin', { name: s.origin, n: s.entities, e: s.events });
}

// ── bağımlılık yolu çizimi (SAF yerleşim; çizim ProblemGraphDiagram) ──

export const DIAGRAM_MAX_NODES = 25;
export const DIA = { colW: 200, nodeW: 160, nodeH: 40, rowH: 56, pad: 16, top: 28 } as const;

export interface DiaNode {
  id: string; name: string; type: string; kind: 'origin' | 'member' | 'calm';
  order?: number; onset?: number; x: number; y: number;
}
export interface DiaEdge { from: string; to: string; kind: 'call' | 'link' | 'calm'; label?: string }
export interface DiaLayout { nodes: DiaNode[]; edges: DiaEdge[]; width: number; height: number; truncated: boolean }

/** SAF: düğümler (üyeler varlık başına bir, köken, sapmasız çağıranlar) + kenarlar
 *  + sütun yerleşimi (köken sağda, çağıranlar solda). En çok 25 düğüm. */
export function graphDiagramLayout(members: ProblemGraphMember[], origin?: ProblemGraphOrigin): DiaLayout {
  const byName = new Map<string, DiaNode>();
  const nodes: DiaNode[] = [];
  let truncated = false;
  const add = (n: Omit<DiaNode, 'x' | 'y'>) => {
    if (byName.has(n.name)) return byName.get(n.name)!;
    if (nodes.length >= DIAGRAM_MAX_NODES) { truncated = true; return undefined; }
    const d = { ...n, x: 0, y: 0 };
    byName.set(n.name, d); nodes.push(d);
    return d;
  };
  const originName = origin?.confidence !== 'unclear' ? origin?.name : undefined;
  let order = 0;
  for (const m of members) {
    if (byName.has(m.name)) continue;
    order += 1;
    add({ id: m.entity, name: m.name, type: m.type, kind: m.name === originName ? 'origin' : 'member', order, onset: m.startedAt });
  }
  if (originName && !byName.has(originName)) {
    add({ id: origin!.entity ?? originName, name: originName, type: origin!.type ?? '', kind: 'origin', onset: origin!.onsetAt || undefined });
  }
  const edges: DiaEdge[] = [];
  const seen = new Set<string>();
  const edge = (e: DiaEdge) => {
    const k = `${e.from}|${e.to}|${e.kind}`;
    if (e.from === e.to || seen.has(k) || !byName.has(e.from) || !byName.has(e.to)) return;
    seen.add(k); edges.push(e);
  };
  for (const m of members) {
    if (!m.link || m.link === 'M-SVC') continue;
    if (m.link === 'M-CALL' && m.path && m.path.length === 2) edge({ from: m.path[0], to: m.path[1], kind: 'call' });
    else if (m.link === 'M-DEP' && m.via && byName.has(m.via)) {
      edge({ from: m.name, to: m.via, kind: 'call' });
      if (m.peer) edge({ from: m.peer, to: m.via, kind: 'call' });
    } else if (m.peer) edge({ from: m.peer, to: m.name, kind: 'link', label: m.via });
  }
  if (originName) {
    for (const c of origin?.blast?.atRisk1 ?? []) {
      if (add({ id: `calm:${c}`, name: c, type: 'service', kind: 'calm' })) edge({ from: c, to: originName, kind: 'calm' });
    }
  }
  // Seviye: köken (yoksa giden çağrısı olmayanlar) 0; çağıran = çağrılan + 1; bağ = eş seviye.
  const level = new Map<string, number>();
  const outCall = new Set(edges.filter(e => e.kind === 'call').map(e => e.from));
  for (const n of nodes) {
    if (n.kind === 'calm') continue;
    if (originName ? n.name === originName : !outCall.has(n.name)) level.set(n.name, 0);
  }
  for (let pass = 0; pass < nodes.length + 1; pass++) {
    let changed = false;
    for (const e of edges) {
      if (e.kind === 'calm') continue;
      const lt = level.get(e.to), lf = level.get(e.from);
      if (e.kind === 'call' && lt !== undefined && lf === undefined) { level.set(e.from, lt + 1); changed = true; }
      if (e.kind === 'link') {
        if (lf !== undefined && lt === undefined) { level.set(e.to, lf); changed = true; }
        if (lt !== undefined && lf === undefined) { level.set(e.from, lt); changed = true; }
      }
    }
    if (!changed) break;
  }
  let maxL = 0;
  for (const n of nodes) if (n.kind !== 'calm') maxL = Math.max(maxL, level.get(n.name) ?? 0);
  const hasCalm = nodes.some(n => n.kind === 'calm');
  const cols = maxL + 1 + (hasCalm ? 1 : 0);
  const rows = new Map<number, number>();
  for (const n of nodes) {
    const l = n.kind === 'calm' ? maxL + 1 : (level.get(n.name) ?? 0);
    const col = cols - 1 - l;
    const r = rows.get(col) ?? 0;
    rows.set(col, r + 1);
    n.x = DIA.pad + col * DIA.colW;
    n.y = DIA.top + r * DIA.rowH;
  }
  const maxRows = Math.max(1, ...rows.values());
  return {
    nodes, edges, truncated,
    width: DIA.pad * 2 + (cols - 1) * DIA.colW + DIA.nodeW,
    height: DIA.top + maxRows * DIA.rowH,
  };
}

// ── v0.10.2281 (S3) — "sık tekrar eden" (gölge) ─────────────────────────

export interface FrequentEpisode { id: string; startedAt: number; resolvedAt?: number; priority?: string; magnitude?: number }

export interface FrequentVerdict {
  signature: string; entity: string; name: string; family: string; direction?: string; viaOrigin?: boolean;
  /** v0.10.2475 — yalnız görünüm: "Tür" satırı ailenin yerine ölçüyü söyler ('consumer_lag'). */
  measure?: string;
  prior24h: number; prior7d: number; days7d: number; frequent: boolean;
  kind?: 'hold' | 'severity' | string; bypass?: string;
  priority?: string; maxPriority?: string; magnitude?: number; maxMagnitude?: number;
  episodes?: FrequentEpisode[]; partial?: boolean;
}

export interface FrequentReport {
  frequent: number; wouldHold: number; severityUp: number; heldBecameP1: number;
  becameP1?: { id: string; name: string; family: string }[];
  atThreshold?: Record<string, number>;
  topSignatures?: { signature: string; name: string; family: string; direction?: string; held: number }[];
  reads: { passes: number; reads: number; rows: number; maxRows: number; capped: number; errors: number };
}

/** SAF: admin raporunun S3 satırları (rapor yoksa boş). */
export function frequentReportLines(f: FrequentReport | undefined, lang: Lang = 'tr'): string[] {
  if (!f) return [];
  const top = (f.topSignatures ?? []).slice(0, 5).map(s => `${s.name} · ${s.family}${s.direction ? ` ${s.direction}` : ''} ${s.held}`).join(' · ') || tr('pgShadow.none2', lang);
  const p1 = (f.becameP1 ?? []).map(x => `${x.name} (${x.id.slice(0, 12)})`).join(', ');
  const F = (k: string, v: Record<string, string | number>) => tf(`pgShadow.f.${k}`, lang, v);
  return [
    F('head', { f: f.frequent, h: f.wouldHold, su: f.severityUp, a: f.atThreshold?.['2'] ?? 0, b: f.atThreshold?.['3'] ?? 0, c: f.atThreshold?.['5'] ?? 0 }),
    F('p1', { x: f.heldBecameP1, list: p1 ? ` — ${p1}` : '' }),
    F('top', { top }),
    F('reads', { r: f.reads.reads, rows: f.reads.rows, mx: f.reads.maxRows, cap: f.reads.capped, e: f.reads.errors }),
  ];
}

/** SAF: aile (+ yön) etiketi. */
export function familyLabel(family: string, direction: string | undefined, t: GraphT): string {
  const known = ['error', 'latency', 'traffic', 'availability', 'exception', 'db', 'external', 'runtime', 'cluster', 'rule'];
  const f = known.includes(family) ? t(`pg.fam.${family}`) : family;
  return direction ? `${f} ${t(`pg.dir.${direction}`)}` : f;
}

/** SAF: çip ipucu — gölge ölçümü olduğunu ve bildirimin değişmediğini söyler. */
export function frequentChipTitle(f: { prior24h: number; days7d: number }, t: GraphT, enforce?: boolean): string {
  return t(enforce ? 'pg.freq.tipOn' : 'pg.freq.tip', { p: f.prior24h, d: f.days7d });
}

/** v0.10.2475 — "Tür" satırı: ölçü biliniyorsa (consumer lag) ailenin yerine
 *  ölçü adı ("alarm kuralı" ML grubunu yanlış anlatıyordu); yoksa aile. */
export function repeatKindLabel(v: Pick<FrequentVerdict, 'family' | 'direction' | 'measure'>, t: GraphT): string {
  return v.measure === 'consumer_lag' ? t('pg.measure.consumer_lag') : familyLabel(v.family, v.direction, t);
}

const two2 = (n: number) => String(n).padStart(2, '0');

/**
 * v0.10.2475 — SAF: bu bölüm + önceki bölümler EN AZ 2 farklı yerel günde ve
 * günün saatinde en çok `spanMin` dakikalık bir dilimde başladıysa dilim
 * ("12:35", "13:50"); değilse null. Yalnız gösterilen bölümlere bakar (≤ 5
 * en yeni) — "benzer saatlerde" iddiası onlarla sınırlı. Saat tarayıcı diliminde.
 */
export function similarHoursWindow(startsNs: number[], spanMin = 120): { from: string; to: string } | null {
  const ds = startsNs.filter(n => n > 0).map(n => new Date(n / 1e6));
  if (new Set(ds.map(d => d.toDateString())).size < 2) return null;
  const mins = ds.map(d => d.getHours() * 60 + d.getMinutes()).sort((a, b) => a - b);
  if (mins[mins.length - 1] - mins[0] > spanMin) return null;
  const hm = (m: number) => `${two2(Math.floor(m / 60))}:${two2(m % 60)}`;
  return { from: hm(mins[0]), to: hm(mins[mins.length - 1]) };
}

/** SAF: "Tekrar" bloğunun satırları (anahtar → değer). startedAt: bu bölümün
 *  başlangıcı (unix ns; "benzer saatlerde" kıyası için, yoksa kıyas yok). */
export function repeatRows(v: FrequentVerdict, t: GraphT, startedAt?: number): { k: string; v: string }[] {
  const mag = (n?: number) => (n === undefined ? '—' : String(Math.round(n * 100) / 100));
  const sev = v.kind === 'severity'
    ? t('pg.rep.sevUp', { why: t(`pg.rep.bypass.${v.bypass ?? 'priority_up'}`), p: v.priority ?? '—', pm: v.maxPriority || '—', m: mag(v.magnitude), mm: mag(v.maxMagnitude) })
    : t('pg.rep.sevSame', { p: v.priority ?? '—', pm: v.maxPriority || '—', m: mag(v.magnitude), mm: mag(v.maxMagnitude) });
  const hours = startedAt ? similarHoursWindow([startedAt, ...(v.episodes ?? []).slice(0, 5).map(e => e.startedAt)]) : null;
  return [
    { k: t('pg.rep.kind'), v: `${v.name} · ${repeatKindLabel(v, t)}${v.viaOrigin ? ` · ${t('pg.rep.viaOrigin')}` : ''}` },
    { k: t('pg.rep.d1'), v: t('pg.rep.d1v', { n: v.prior24h + 1, min: 3 }) },
    { k: t('pg.rep.d7'), v: hours ? t('pg.rep.d7vHours', { d: v.days7d, from: hours.from, to: hours.to }) : t('pg.rep.d7v', { d: v.days7d }) },
    { k: t('pg.rep.sev'), v: sev },
    { k: t('pg.rep.rule'), v: v.kind === 'hold' ? t('pg.rep.ruleHold') : t('pg.rep.ruleSend') },
  ];
}

/** SAF: önceki bölüm satırı. */
export function episodeText(e: FrequentEpisode, t: GraphT, when: (ns: number) => string): string {
  const parts = [when(e.startedAt)];
  if (e.resolvedAt && e.resolvedAt > e.startedAt) parts.push(t('pg.rep.lasted', { m: Math.max(1, Math.round((e.resolvedAt - e.startedAt) / 6e10)) }));
  if (e.priority) parts.push(e.priority);
  return parts.join(' · ');
}

/** v0.10.2341 — SAF: admin raporunun geçiş ölçütü satırları (tasarım §9).
 *  Operatör ekranı: yalnız Türkçe, i18n anahtarı yok. since = sayaç penceresi
 *  başlangıcının biçimlenmiş hâli (çağıran biçimler). */
export function graphCriterionLines(modes: ProblemGraphModes | undefined, c: ProblemGraphCriterion | undefined, since: string): string[] {
  const on = (b?: boolean) => (b ? 'açık' : 'kapalı');
  const out = [
    `S4: ${on(modes?.s4)} / S5: ${on(modes?.s5)}`,
    'Açma ölçütü (tasarım §9): en az 7 gün gölge; tutulacak bildirimler arasında sonradan P1\'e çıkan ya da onaylı vakaya dönen SIFIR; yanlış bağ örnekleri (aşağıdaki en büyük 20 küme) operatörce gözden geçirilmiş.',
  ];
  if (!c) return out;
  out.push(
    `Sayaç penceresi: ${since} başından, en çok 24 saat (lider değişince sıfırlanır; 7 günlük süreyi operatör sayar).`,
    `Birleşme (S4): sonradan bağlanan ${c.mergeJoined} üye; açılış bildirimi tutulacak ${c.mergeWouldHold}; bunlardan sonradan P1'e çıkan ${c.mergeHeldP1} (hedef 0); geç katılan ${c.late}.`,
    `Sık tekrar (S5): açılış bildirimi tutulacak ${c.frequentWouldHold}; bunlardan sonradan P1'e çıkan ${c.frequentHeldP1} (hedef 0).`,
    'Onaylı vakaya dönüş ürün içinde sayılmıyor: tutulacak üyeler listesi operatörce gözden geçirilir.',
  );
  return out;
}
