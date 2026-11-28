// topoFocusLayout.ts — odak merkezli sütun yerleşimi (Focus Column Layout), SAF.
//
// v0.10.1294 — operatör 2026-10-17: "Topology'de servisler arası oklar çok
// uzak ve sarmal bir path gibi gidiyor; optimize edelim, gerçek topoloji
// anlaşılır olsun; sektördeki en iyi gösterim."
//
// Kök neden: servis sayfasının Topology sekmesi ve /service-map?focus=
// (aynı FocusedNeighborhood) global haritanın katmanlı yerleşimini
// (lib/topoBfsLayout) kullanıyordu. assignFocusColumns'un işaretli
// sütunları (çağıran −1/−2, bağımlılık +1/+2) yalnız tavanı sürüyordu;
// yerleşim 2-hop alt grafı en uzun yolla baştan katmanlıyor, odak herhangi
// bir sütuna (çoğu kez en sağa) düşüyor, sütunlar kap genişliğine geriliyor
// (~590px), namespace şeritleri boş satır ayırıyor, uzun kenar her ara
// sütunda bir S-büküm alıp sarmal çiziyordu.
//
// Sektör deseni (Datadog isolate/resource map, Dynatrace service flow, New
// Relic, AppDynamics flow map): sabit sütunlar −2 | −1 | odak | +1 | +2,
// odak dikey ortada, komşu sütun kenarı TEK yumuşak eğri, sütun başına
// "+N more", namespace renkle (şerit yok). Global harita katmanlı kalır.
//
//  • SÜTUN = assignFocusColumns (değişmedi). x = PAD_X + (col + HL)·COL_W,
//    COL_W = pil genişliği (170) + oluk (110) = 280; ASLA gerilmez.
//  • GENİŞLİK (v0.10.1307): her taraf KENDİ hop sayısı kadar sütun —
//    HL = en dış çağıran sütunu, HR = en dış bağımlılık sütunu ("+N more"
//    dahil); lw = (HL+HR)·COL_W + 2·PAD_X. Simetrik grafta eskisiyle bayt
//    bayt aynı; asimetrikte odak ortada değil, açılışta viewport ortalar.
//  • KENAR SINIFI (cu → cv): flow cv = cu+1 · peer cu = cv · skip cv−cu ≥ 2 ·
//    back cv < cu. Çift yönlü çift soldan sağa yönünden sınıflanır.
//  • SIRA (dışa doğru geçişler, O(E + n log n)): ±1 sütunu bloklar —
//    servisler namespace'e göre, bağımlılıklar türe göre (db, queue, ext);
//    bloklar trafiğe göre sıralanıp ortadan dışa ("organ borusu") dizilir,
//    en ağır blok odak satırının yanında. ±2+: iç komşu indekslerinin
//    medyanı. Sonra ±1 blokları içinde ±2 çocuklarının barycenter'ı, sonra
//    ±2+ yeniden.
//  • DİKEY: şerit yok, ayrılmış satır yok. ±1 odak (y=0) etrafında kompakt
//    yığın, ns blokları arası BLOCK_GAP. ±2+: istenen y = iç flow
//    komşularının ortalaması; minimum aralıklı en yakın sıralı konumlar PAV
//    izotonik regresyonu ile (O(n), en az kayma, çakışma yok). lh = 2·Y +
//    pay → odak TAM dikey ortada.
//  • ROTA (≤1 ara nokta, sarmal yok): flow = iki noktalı kübik
//    (topoArrow.edgePathD), uçlar pil kenarında (x ± 85) → ok (t=0.78) ve
//    chip (t=0.5) olukta. Odakta port yayma. peer = dış olukta tek kübik
//    yay. skip/back = [a, şerit, b]; şerit ara sütunun yığınının hemen
//    üstünde/altında.
//
// DETERMİNİZM: her sıralamanın ad eşitlik-bozucusu var; çıktı girdinin
// düğüm/kenar SIRASINDAN bağımsız (testte karıştırılmış girdiyle pinli).

import { ROW_PITCH } from './topoLayout';
import type { TopoPt } from './topoArrow';

export const PILL_W = 170;            // .topo-node max-width (globals.css) — odak kipinde sabit genişlik
export const HALF_PILL = PILL_W / 2;  // port = pil kenarı
export const GUTTER = 110;
export const COL_W = PILL_W + GUTTER; // 280
export const PAD_X = 150;             // en dış sütunda peer yayı + ns ayracı payı
export const HEADER_H = 28;           // sütun başlığı satırı
export const PAD_Y = HEADER_H + ROW_PITCH / 2 + 10; // başlık + şerit (top − pitch/2) payı
export const BLOCK_GAP = 12;
// v0.10.2491 (Topology v2) — odakta giriş noktaları pil yüksekliğine yayılır
// (adım 6 px, pil ~50 px yüksek → ±14 px içinde kalır).
export const PORT_STEP = 6;
export const PORT_SPREAD_MAX = 14;
const PEER_BULGE = 22;                // peer yayının pil kenarından taşması (yaklaşık)
// ±2+ sütunu ebeveyn hizasına kayarken en uzun doğal sütun yarı-boyunun en çok
// bu kadar satır ötesine taşar (patolojik tek-ebeveynli kalabalık sütun tuvali
// uzatmasın; normal grafta bağlamaz).
const OUTER_SLACK = 2;

export const MORE_PREFIX = 'more:';
export const moreNodeId = (col: number) => `${MORE_PREFIX}${col}`;
export const isMoreNode = (id: string) => id.startsWith(MORE_PREFIX);
export const moreNodeCol = (id: string) => Number(id.slice(MORE_PREFIX.length));

export type EdgeClass = 'flow' | 'peer' | 'skip' | 'back';

export interface FocusLayoutNode { service: string; spanCount: number; kind?: string }
export interface FocusLayoutEdge { caller: string; callee: string }
export interface FocusLayoutInput {
  nodes: readonly FocusLayoutNode[];
  edges: readonly FocusLayoutEdge[];
  colOf: ReadonlyMap<string, number>;
  focus: string;
  nsOf?: (service: string) => string | undefined;
  pitch?: number;
}
export interface FocusEdgeGeom {
  cls: EdgeClass;
  /** uç noktalar pil kenarında; skip/back için ortada tek şerit noktası */
  pts: TopoPt[];
  /** yalnız peer: tek kübik yayın kontrol noktaları */
  ctrl?: [TopoPt, TopoPt];
}
export interface FocusColumn { col: number; x: number; count: number; top: number; bottom: number }
export interface FocusBracket { ns: string; col: number; x: number; y0: number; y1: number; side: -1 | 1 }
export interface FocusLayoutResult {
  pos: Map<string, { x: number; y: number }>;
  lw: number;
  lh: number;
  focusPt: { x: number; y: number };
  /** v0.10.1307 — her tarafın gerçek sütun sayısı: left = çağıran, right = bağımlılık */
  hops: { left: number; right: number };
  /** "caller|callee" → geometri (her iki ucu yerleşen her girdi kenarı) */
  edges: Map<string, FocusEdgeGeom>;
  columns: FocusColumn[];
  brackets: FocusBracket[];
}

const cmpStr = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

export function classifyEdge(cu: number, cv: number): EdgeClass {
  if (cv === cu + 1) return 'flow';
  if (cv === cu) return 'peer';
  if (cv > cu) return 'skip';
  return 'back';
}

/** Çift yönlü çift (hasReverse) soldan sağa yönünden sınıflanır — asla back değil. */
export function pairClass(cu: number, cv: number, hasReverse: boolean): EdgeClass {
  return hasReverse && cv < cu ? classifyEdge(cv, cu) : classifyEdge(cu, cv);
}

/** hl = sol (çağıran) tarafın sütun sayısı — odak x'i PAD_X + hl·COL_W. */
export function xOfCol(col: number, hl: number): number {
  return PAD_X + (col + hl) * COL_W;
}

// v0.10.1307 — operatör 2026-10-17: servis Topology sekmesinde hop'u az olan
// tarafa boş 280 px sütun ayrılıyordu. Artık o taraf hiç yer almıyor; çağıran
// hiç yoksa (giriş noktası ya da çağıranlar enstrümante değil) odağın soluna,
// başlık satırına tek soluk not düşülür — boşluk "eksik veri" diye okunmasın.
export const NO_CALLERS_LABEL = 'Çağıran yok (enstrümante değil)';
const EMPTY_HDR_LEFT = 8;        // içerik sol kenarından pay
const EMPTY_HDR_FOCUS_GAP = 24;  // "ODAK" başlığının yarısı + boşluk
const EMPTY_HDR_MIN_W = 96;      // iki satıra kırılmış metnin sığdığı en dar alan

/**
 * Çağıran tarafı boşsa (hops.left === 0) soluk başlığın yeri: sol kenar x ve
 * genişlik w (başlık satırında, "Odak" başlığının solunda). Yer yoksa null.
 */
export function noCallersHeader(r: Pick<FocusLayoutResult, 'hops' | 'focusPt'>): { x: number; w: number } | null {
  if (r.hops.left !== 0) return null;
  const w = r.focusPt.x - EMPTY_HDR_LEFT - EMPTY_HDR_FOCUS_GAP;
  return w >= EMPTY_HDR_MIN_W ? { x: EMPTY_HDR_LEFT, w } : null;
}

// İkincil kenar (peer/skip/back) varsayılanı: GİZLİ (null). Operatör soluk
// görmeyi tercih ederse tek sabit: SECONDARY_DEFAULT_OPACITY = 0.2.
export const SECONDARY_DEFAULT_OPACITY: number | null = null;
export const SECONDARY_HOVER_OPACITY = 0.45;

/**
 * Kenarın görünürlük çarpanı: null = çizilmez. flow her zaman tam; ikincil
 * kenar "Yan bağlantılar" açıkken tam, hover edilen düğüme değiyorsa soluk,
 * yoksa SECONDARY_DEFAULT_OPACITY.
 */
export function edgeVisibility(cls: EdgeClass, o: { showSecondary: boolean; incident: boolean }): number | null {
  if (cls === 'flow' || o.showSecondary) return 1;
  if (o.incident) return SECONDARY_HOVER_OPACITY;
  return SECONDARY_DEFAULT_OPACITY;
}

/** Yönsüz çift başına ikincil (flow olmayan) kenar sayısı — toolbar "Yan bağlantılar (N)". */
export function countSecondary(edges: readonly FocusLayoutEdge[], colOf: ReadonlyMap<string, number>): number {
  const dir = new Set<string>();
  for (const e of edges) dir.add(`${e.caller}|${e.callee}`);
  const seen = new Set<string>();
  let n = 0;
  for (const e of edges) {
    if (e.caller === e.callee) continue;
    const cu = colOf.get(e.caller), cv = colOf.get(e.callee);
    if (cu === undefined || cv === undefined) continue;
    const key = e.caller < e.callee ? `${e.caller}|${e.callee}` : `${e.callee}|${e.caller}`;
    if (seen.has(key)) continue;
    seen.add(key);
    if (pairClass(cu, cv, dir.has(`${e.callee}|${e.caller}`)) !== 'flow') n++;
  }
  return n;
}

// v0.10.2491 — operatör kararı (Topology v2): sütun başlıkları İngilizce,
// her dilde aynı (CSS büyük harfe çevirir: CALLERS · N · FOCUS · DEPENDENCIES · N).
export function focusColumnLabel(col: number): string {
  if (col === 0) return 'Focus';
  const base = col < 0 ? 'Callers' : 'Dependencies';
  return Math.abs(col) === 1 ? base : `${base} · ${Math.abs(col)} hops`;
}

/** PAV — eşit ağırlıklı izotonik regresyon: w'ye en yakın azalmayan dizi. O(n). */
export function pav(w: readonly number[]): number[] {
  const val: number[] = [], cnt: number[] = [];
  for (const x of w) {
    let v = x, c = 1;
    while (val.length && val[val.length - 1] > v) {
      const pv = val.pop()!, pc = cnt.pop()!;
      v = (pv * pc + v * c) / (pc + c);
      c += pc;
    }
    val.push(v); cnt.push(c);
  }
  const out: number[] = [];
  for (let i = 0; i < val.length; i++) for (let k = 0; k < cnt[i]; k++) out.push(val[i]);
  return out;
}

/**
 * placeOuter — sıralı düğümler için istenen y'lere (desired) en yakın, ardışık
 * aralığı ≥ pitch olan konumlar, hepsi [−Y, Y] içinde. z_i = y_i − i·pitch
 * azalmayan olmalı → PAV(d_i − i·pitch); ortak sınırla kırpma optimal kalır.
 */
export function placeOuter(desired: readonly number[], pitch: number, Y: number): number[] {
  const n = desired.length;
  if (n === 0) return [];
  const z = pav(desired.map((d, i) => d - i * pitch));
  const lo = -Y, hi = Math.max(lo, Y - (n - 1) * pitch);
  return z.map((v, i) => Math.min(hi, Math.max(lo, v)) + i * pitch);
}

/**
 * focusPath — hover edilen düğümden odağa giden flow zinciri (çağıran
 * tarafında ileri, bağımlılık tarafında geri yürür). Düğüm sayısı ≤ görünür
 * küme; hover başına bir BFS.
 */
export function focusPath(
  hover: string, edges: readonly FocusLayoutEdge[], colOf: ReadonlyMap<string, number>,
): { nodes: Set<string>; edges: Set<string> } {
  const nodes = new Set<string>([hover]);
  const keys = new Set<string>();
  const c0 = colOf.get(hover);
  if (c0 === undefined || c0 === 0) return { nodes, edges: keys };
  const downstream = c0 > 0; // bağımlılık: kendisini çağıran iç sütuna doğru geri yürü
  const step = new Map<string, FocusLayoutEdge[]>();
  for (const e of edges) {
    const cu = colOf.get(e.caller), cv = colOf.get(e.callee);
    if (cu === undefined || cv === undefined || cv !== cu + 1) continue; // yalnız flow
    const k = downstream ? e.callee : e.caller;
    (step.get(k) ?? step.set(k, []).get(k)!).push(e);
  }
  let frontier = [hover];
  while (frontier.length) {
    const next: string[] = [];
    for (const id of frontier) {
      for (const e of step.get(id) ?? []) {
        const nb = downstream ? e.caller : e.callee;
        const cn = colOf.get(nb)!;
        if (downstream ? cn < 0 : cn > 0) continue; // odağı geçme
        keys.add(`${e.caller}|${e.callee}`);
        if (!nodes.has(nb)) { nodes.add(nb); if (cn !== 0) next.push(nb); }
      }
    }
    frontier = next;
  }
  return { nodes, edges: keys };
}

const KIND_RANK: Record<string, number> = { db: 0, queue: 1, external: 2 };

export function focusColumnLayout(input: FocusLayoutInput): FocusLayoutResult {
  const pitch = input.pitch ?? ROW_PITCH;
  const { focus, colOf } = input;
  const nsOf = input.nsOf ?? (() => undefined);

  // ── indeksleme (ada göre) ────────────────────────────────────────────
  const info = new Map<string, FocusLayoutNode>();
  for (const n of input.nodes) if (!info.has(n.service) && colOf.has(n.service)) info.set(n.service, n);
  if (!info.has(focus)) info.set(focus, { service: focus, spanCount: 0 });
  const names = [...info.keys()].sort(cmpStr);
  const col = (s: string) => (s === focus ? 0 : colOf.get(s)!);
  const traffic = (s: string) => info.get(s)!.spanCount;

  // v0.10.1307 — her tarafın kendi derinliği ("+N more" düğümleri dahil);
  // H = ikisinin büyüğü yalnız ±k döngülerinin üst sınırı.
  let HL = 0, HR = 0;
  const byCol = new Map<number, string[]>();
  for (const s of names) {
    const c = col(s);
    if (-c > HL) HL = -c;
    if (c > HR) HR = c;
    (byCol.get(c) ?? byCol.set(c, []).get(c)!).push(s);
  }
  const H = Math.max(HL, HR);
  const hopsOf = (side: number) => (side < 0 ? HL : HR);

  // yönlü kenar kümesi + iç (flow) komşuluk: inner.get(v) = odağa doğru
  // komşu sütundaki flow komşuları; outer = tersi. Ad sıralı, tekil.
  const dir = new Set<string>();
  const placedEdges: FocusLayoutEdge[] = [];
  for (const e of input.edges) {
    if (e.caller === e.callee || !info.has(e.caller) || !info.has(e.callee)) continue;
    const k = `${e.caller}|${e.callee}`;
    if (dir.has(k)) continue;
    dir.add(k);
    placedEdges.push(e);
  }
  placedEdges.sort((a, b) => cmpStr(a.caller, b.caller) || cmpStr(a.callee, b.callee));
  const inner = new Map<string, string[]>();
  const anyInner = new Map<string, string[]>();
  const outer = new Map<string, string[]>();
  const push = (m: Map<string, string[]>, k: string, v: string) => {
    const l = m.get(k);
    if (!l) m.set(k, [v]); else if (!l.includes(v)) l.push(v);
  };
  for (const e of placedEdges) {
    const cu = col(e.caller), cv = col(e.callee);
    if (Math.abs(cu - cv) !== 1) continue;
    // u daha içte mi (|col| küçük)?
    const [inN, outN] = Math.abs(cu) < Math.abs(cv) ? [e.caller, e.callee] : [e.callee, e.caller];
    push(anyInner, outN, inN);
    // flow yönü: bağımlılık tarafında iç → dış, çağıran tarafında dış → iç
    const isFlow = cv === cu + 1;
    if (isFlow) { push(inner, outN, inN); push(outer, inN, outN); }
  }

  // ── sıra ──────────────────────────────────────────────────────────────
  const order = new Map<number, string[]>();
  const blocksOf = new Map<number, string[][]>();
  order.set(0, [focus]);
  const isMore = (s: string) => isMoreNode(s);
  const blockKey = (s: string) => {
    const k = info.get(s)!.kind;
    return k ? `k:${KIND_RANK[k] ?? 3}:${k}` : `n:${nsOf(s) ?? ''}`;
  };
  for (const side of [-1, 1]) {
    const list = (byCol.get(side) ?? []).filter(s => !isMore(s));
    const groups = new Map<string, string[]>();
    for (const s of list) {
      const k = blockKey(s);
      (groups.get(k) ?? groups.set(k, []).get(k)!).push(s);
    }
    const blocks = [...groups.entries()].map(([k, members]) => ({
      k, members: members.sort((a, b) => (traffic(b) - traffic(a)) || cmpStr(a, b)),
      t: members.reduce((acc, s) => acc + traffic(s), 0),
    }));
    blocks.sort((a, b) => {
      const da = a.k.startsWith('k:'), db = b.k.startsWith('k:');
      if (da !== db) return da ? 1 : -1;
      if (da) return cmpStr(a.k, b.k);
      return (b.t - a.t) || cmpStr(a.k, b.k);
    });
    // organ borusu: sıra 0 ortada, tekler üstte, çiftler altta
    const piped: string[][] = [];
    blocks.forEach((b, r) => { if (r > 0 && r % 2 === 1) piped.unshift(b.members); else piped.push(b.members); });
    const more = (byCol.get(side) ?? []).filter(isMore);
    if (more.length) piped.push(more);
    blocksOf.set(side, piped);
    order.set(side, piped.flat());
  }

  const indexIn = new Map<string, number>();
  const reindex = (c: number) => (order.get(c) ?? []).forEach((s, i) => indexIn.set(s, i));
  reindex(0); reindex(-1); reindex(1);

  const median = (xs: number[]) => {
    const a = xs.slice().sort((p, q) => p - q);
    const m = a.length >> 1;
    return a.length % 2 ? a[m] : (a[m - 1] + a[m]) / 2;
  };
  const nbOf = (s: string) => {
    const f = inner.get(s);
    return f && f.length ? f : anyInner.get(s) ?? [];
  };
  const orderOuter = () => {
    for (let k = 2; k <= H; k++) for (const side of [-1, 1]) {
      if (k > hopsOf(side)) continue;
      const c = side * k;
      const list = byCol.get(c);
      if (!list) continue;
      const key = new Map<string, number>();
      for (const s of list) {
        const nb = nbOf(s).map(x => indexIn.get(x)).filter((v): v is number => v !== undefined);
        key.set(s, nb.length ? median(nb) : Infinity);
      }
      const sorted = list.slice().sort((a, b) => {
        const ma = isMore(a), mb = isMore(b);
        if (ma !== mb) return ma ? 1 : -1;
        return (key.get(a)! - key.get(b)!) || cmpStr(nsOf(a) ?? '', nsOf(b) ?? '')
          || (traffic(b) - traffic(a)) || cmpStr(a, b);
      });
      order.set(c, sorted);
      reindex(c);
    }
  };
  orderOuter();
  // geçiş 2: ±1 blokları içinde ±2 çocuklarının barycenter'ı (bloklar bitişik kalır)
  if (H >= 2) {
    for (const side of [-1, 1]) {
      const blocks = blocksOf.get(side);
      if (!blocks) continue;
      for (const b of blocks) {
        if (b.length < 2 || isMore(b[0])) continue;
        const slots: number[] = [];
        const keyed: { s: string; k: number }[] = [];
        b.forEach((s, i) => {
          const ch = (outer.get(s) ?? []).map(x => indexIn.get(x)).filter((v): v is number => v !== undefined);
          if (ch.length) { slots.push(i); keyed.push({ s, k: ch.reduce((a, v) => a + v, 0) / ch.length }); }
        });
        keyed.sort((p, q) => (p.k - q.k) || ((traffic(q.s) - traffic(p.s)) || cmpStr(p.s, q.s)));
        slots.forEach((slot, j) => { b[slot] = keyed[j].s; });
      }
      order.set(side, blocks.flat());
      reindex(side);
    }
    orderOuter(); // geçiş 3
  }

  // ── dikey ─────────────────────────────────────────────────────────────
  const yRel = new Map<string, number>([[focus, 0]]);
  let Y = 0;
  for (const side of [-1, 1]) {
    const blocks = blocksOf.get(side);
    if (!blocks || blocks.length === 0) continue;
    const ys: [string, number][] = [];
    let cur = 0;
    blocks.forEach((b, bi) => {
      if (bi > 0) cur += pitch + BLOCK_GAP;
      b.forEach((s, i) => { if (i > 0) cur += pitch; ys.push([s, cur]); });
    });
    const half = cur / 2;
    for (const [s, y] of ys) yRel.set(s, y - half);
    if (half > Y) Y = half;
  }
  for (let k = 2; k <= H; k++) for (const side of [-1, 1]) {
    if (k > hopsOf(side)) continue;
    const n = byCol.get(side * k)?.length ?? 0;
    if (n > 0) Y = Math.max(Y, (n - 1) * pitch / 2);
  }
  for (let k = 2; k <= H; k++) for (const side of [-1, 1]) {
    if (k > hopsOf(side)) continue;
    const list = order.get(side * k);
    if (!list) continue;
    const desired: number[] = [];
    let prev: number | undefined;
    for (const s of list) {
      const nb = nbOf(s).map(x => yRel.get(x)).filter((v): v is number => v !== undefined);
      const d = nb.length ? nb.reduce((a, v) => a + v, 0) / nb.length : (prev !== undefined ? prev + pitch : 0);
      desired.push(d);
      prev = d;
    }
    const ys = placeOuter(desired, pitch, Y + OUTER_SLACK * pitch);
    list.forEach((s, i) => yRel.set(s, ys[i]));
  }
  for (const v of yRel.values()) if (Math.abs(v) > Y) Y = Math.abs(v);

  const yShift = Y + PAD_Y;
  const lh = 2 * yShift;
  // v0.10.1307 — eskiden 2·H·COL_W: az hop'lu tarafa boş sütun ayrılıyordu.
  const lw = (HL + HR) * COL_W + 2 * PAD_X;
  const pos = new Map<string, { x: number; y: number }>();
  for (const s of names) pos.set(s, { x: xOfCol(col(s), HL), y: yRel.get(s)! + yShift });
  const focusPt = pos.get(focus)!;

  // ── sütun özetleri ────────────────────────────────────────────────────
  const columns: FocusColumn[] = [];
  const extent = new Map<number, { top: number; bottom: number }>();
  for (let c = -HL; c <= HR; c++) {
    const list = order.get(c);
    if (!list || list.length === 0) continue;
    let top = Infinity, bottom = -Infinity;
    for (const s of list) { const y = pos.get(s)!.y; if (y < top) top = y; if (y > bottom) bottom = y; }
    extent.set(c, { top, bottom });
    columns.push({ col: c, x: xOfCol(c, HL), count: list.filter(s => !isMore(s)).length, top, bottom });
  }

  // ── ns ayraçları: ±1'de ≥2 üyeli bitişik servis koşuları ──────────────
  const brackets: FocusBracket[] = [];
  for (const side of [-1, 1] as const) {
    for (const b of blocksOf.get(side) ?? []) {
      if (b.length < 2 || isMore(b[0]) || info.get(b[0])!.kind) continue;
      const ns = nsOf(b[0]);
      if (!ns) continue;
      const ys = b.map(s => pos.get(s)!.y);
      brackets.push({
        ns, col: side, side,
        x: xOfCol(side, HL) + side * (HALF_PILL + 8),
        y0: Math.min(...ys) - pitch / 2 + 6,
        y1: Math.max(...ys) + pitch / 2 - 6,
      });
    }
  }

  // ── rotalar ───────────────────────────────────────────────────────────
  const port = (s: string, side: number): TopoPt => {
    const p = pos.get(s)!;
    return { x: p.x + side * HALF_PILL, y: p.y };
  };
  const edges = new Map<string, FocusEdgeGeom>();
  // odakta port yayma: [kenar anahtarı, hangi uç] → y kayması
  const focusEnds: { key: string; end: 0 | 1; side: number; far: number; farName: string }[] = [];
  for (const e of placedEdges) {
    const cu = col(e.caller), cv = col(e.callee);
    const cls = pairClass(cu, cv, dir.has(`${e.callee}|${e.caller}`));
    const a = pos.get(e.caller)!, b = pos.get(e.callee)!;
    const key = `${e.caller}|${e.callee}`;
    let g: FocusEdgeGeom;
    if (cls === 'peer') {
      const side = cu < 0 ? -1 : 1;
      const cx = a.x + side * (HALF_PILL + PEER_BULGE) / 0.75;
      const pa = port(e.caller, side), pb = port(e.callee, side);
      g = { cls, pts: [pa, pb], ctrl: [{ x: cx, y: pa.y }, { x: cx, y: pb.y }] };
    } else if (Math.abs(cv - cu) <= 1) {
      const s = Math.sign(b.x - a.x);
      g = { cls, pts: [port(e.caller, s), port(e.callee, -s)] };
    } else {
      // ara sütun: en kısa yığın, eşitlikte ortaya en yakın, sonra |col| küçük
      const lo = Math.min(cu, cv), hi = Math.max(cu, cv), mid = (cu + cv) / 2;
      let m = lo + 1, best = Infinity;
      for (let c = lo + 1; c < hi; c++) {
        const ex = extent.get(c);
        const h = ex ? ex.bottom - ex.top + 1 : 0;
        const score = h * 1e6 + Math.abs(c - mid) * 1e3 + Math.abs(c);
        if (score < best) { best = score; m = c; }
      }
      const lx = xOfCol(m, HL);
      const mean = (a.y + b.y) / 2;
      const ex = extent.get(m);
      let ly = mean;
      if (ex) {
        const up = ex.top - pitch / 2, down = ex.bottom + pitch / 2;
        ly = Math.abs(up - mean) <= Math.abs(down - mean) ? up : down;
      }
      const lane = { x: lx, y: ly };
      g = { cls, pts: [port(e.caller, Math.sign(lx - a.x)), lane, port(e.callee, Math.sign(lx - b.x))] };
    }
    edges.set(key, g);
    if (e.caller === focus) focusEnds.push({ key, end: 0, side: Math.sign(g.pts[0].x - focusPt.x), far: b.y, farName: e.callee });
    if (e.callee === focus) focusEnds.push({ key, end: 1, side: Math.sign(g.pts[g.pts.length - 1].x - focusPt.x), far: a.y, farName: e.caller });
  }
  for (const side of [-1, 1]) {
    const list = focusEnds.filter(f => f.side === side)
      .sort((p, q) => (p.far - q.far) || cmpStr(p.farName, q.farName) || cmpStr(p.key, q.key));
    const k = list.length;
    list.forEach((f, rank) => {
      const off = Math.max(-PORT_SPREAD_MAX, Math.min(PORT_SPREAD_MAX, (rank - (k - 1) / 2) * PORT_STEP));
      const g = edges.get(f.key)!;
      const i = f.end === 0 ? 0 : g.pts.length - 1;
      g.pts[i] = { x: g.pts[i].x, y: g.pts[i].y + off };
    });
  }

  return { pos, lw, lh, focusPt, hops: { left: HL, right: HR }, edges, columns, brackets };
}
