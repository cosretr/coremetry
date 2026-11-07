// frame — v0.10.1909. SAF kare çekirdeği: kare hızı kapısı, düğüm stili,
// kayma/akış konumları, etiket seçimi ve arka plan KİP kararı. Canvas çizimi
// `pages/login/TopologyBackdrop.tsx`'te; burada DOM yok (düğüm ortamında test).
import type { Layout, LayoutNode } from './layout';

/** 30 fps tavanı: önceki çizimden bu kadar ms geçmediyse kare atlanır. */
export const FRAME_MIN_MS = 33;
export function shouldDraw(now: number, last: number): boolean {
  return last <= 0 || now - last >= FRAME_MIN_MS;
}

// v0.10.1917 (operatör: "çok dolu gözükmesin") — çizilen düğüm, etiket ve akan
// nokta tavanları düşürüldü (180 → 110 düğüm, 70 → 24 etiket, 120 → 40 nokta).
// v0.10.1927 (operatör-raporlu: "noktalar çok büyümüş ve seyrelmiş, eski hâli
// daha iyiydi") — düğüm tavanı yeniden 180 (sunucunun gönderdiği tamamı);
// sadeliği küçük düğüm ve derinlik sağlar, seyreltme değil.
export const MAX_DRAW_NODES = 180;
export const MAX_LABELS = 24;
export const MAX_FLOW_EDGES = 40;

const THIN_RANK: Record<string, number> = { gw: 0, bff: 1, db: 2, svc: 3 };
/**
 * Çizilecek düğümler: giriş → BFF → veri → servis (servisler ağırlığa göre),
 * en çok `max`. Girdi sırası korunur (tohum kararlı kalsın); kenarlar yalnız
 * iki ucu da kalanlar arasında.
 */
export function thinGraph<N extends { id: string; tier: string; weight: number }, E extends { from: string; to: string }>(
  nodes: readonly N[], edges: readonly E[], max = MAX_DRAW_NODES,
): { nodes: N[]; edges: E[] } {
  if (nodes.length <= max) return { nodes: [...nodes], edges: [...edges] };
  const keep = new Set(nodes.map((n, i) => ({ n, i }))
    .sort((a, b) => (THIN_RANK[a.n.tier] ?? 9) - (THIN_RANK[b.n.tier] ?? 9) || b.n.weight - a.n.weight || a.i - b.i)
    .slice(0, max).map(x => x.n.id));
  return {
    nodes: nodes.filter(n => keep.has(n.id)),
    edges: edges.filter(e => keep.has(e.from) && keep.has(e.to)),
  };
}

export interface Pt { x: number; y: number }
/**
 * Düğümlerin ekran (css px) konumları: yerleşim + kayma, ölçekli. Çizim ve
 * isabet testi AYNI konumu kullanır. v0.10.1919 — `motion` verilirse düğüm
 * başına salınım genliği/hız çarpanı (derinlik katmanı).
 */
export function screenPositions(
  layout: Layout, tSec: number, sx: number, sy: number,
  motion?: (i: number) => { amp: number; speed: number },
): Pt[] {
  return layout.nodes.map((n, i) => {
    const m = motion?.(i);
    const d = drift(layout, i, tSec, m?.amp ?? 1, m?.speed ?? 1);
    return { x: (n.x + d.dx) * sx, y: (n.y + d.dy) * sy };
  });
}

export interface NodeStyle { r: number; ring: boolean; square: boolean; labelAlpha: number }

/** Düğüm stili (audit §6.3): gw r 9–12 + halka, bff 5–6, servis 3–4, yaprak 2.2, db 6 px kare. */
export function nodeStyle(n: Pick<LayoutNode, 'tier' | 'weight' | 'deg'>): NodeStyle {
  switch (n.tier) {
    case 'gw': return { r: 9 + 3 * n.weight, ring: true, square: false, labelAlpha: 0.75 };
    case 'bff': return { r: 5 + n.weight, ring: false, square: false, labelAlpha: 0.75 };
    case 'db': return { r: 6, ring: false, square: true, labelAlpha: 0.55 };
    default: return n.deg <= 1
      ? { r: 2.2, ring: false, square: false, labelAlpha: 0.55 }
      : { r: 3 + n.weight, ring: false, square: false, labelAlpha: 0.55 };
  }
}

/** Düğümün zamana bağlı sinüs kayması (mantıksal birim). Aynı (i, t) → aynı sonuç. `ampMul`/`speedMul`: derinlik çarpanı. */
export function drift(layout: Layout, i: number, tSec: number, ampMul = 1, speedMul = 1): { dx: number; dy: number } {
  const ph = layout.nodePhase[i] ?? 0;
  const amp = (2 + 3 * (layout.nodes[i]?.weight ?? 0)) * ampMul;
  const period = 8 + (i % 7); // 8..14 sn
  const a = ((tSec * speedMul) / period) * Math.PI * 2 + ph;
  return { dx: Math.sin(a) * amp, dy: Math.cos(a * 0.8 + ph) * amp };
}

/** Kenar boyunca akan noktanın [0,1) konumu. */
export function flowPos(layout: Layout, ei: number, tSec: number): number {
  const v = 0.06 + 0.05 * (layout.edges[ei]?.weight ?? 0); // tur/sn
  const p = (layout.edgePhase[ei] ?? 0) + tSec * v;
  return p - Math.floor(p);
}

/** Akış noktası çizilecek kenarlar: ağırlığa göre ilk `max` (kararlı sıra). */
export function flowEdges(layout: Layout, max = MAX_FLOW_EDGES): number[] {
  return layout.edges.map((e, i) => ({ i, w: e.weight }))
    .sort((a, b) => b.w - a.w || a.i - b.i).slice(0, max).map(x => x.i);
}

const TIER_RANK: Record<string, number> = { gw: 0, bff: 1, svc: 2, db: 3 };
/**
 * Etiket yazılacak düğümler: yalnız ADI olanlar (isimli kip), en çok `max`.
 * `rand` verilmezse kararlı sıra (giriş/BFF önce, sonra ağırlık). v0.10.1918
 * (operatör: "isimler de random gelsin") — `rand` verilirse adı olanlar
 * arasından RASTGELE alt küme (Fisher–Yates); sayfa her açılışta farklı
 * servisleri etiketler. Dönüş dizin sırasına göre (çizim sırası kararlı).
 */
export function labelNodes(
  layout: Layout, max = MAX_LABELS, rand?: () => number, eligible?: (i: number) => boolean,
): number[] {
  // v0.10.1919 — `eligible`: yalnız ön katman etiketlenir (derinlik).
  const named = layout.nodes.map((n, i) => ({ i, n })).filter(x => !!x.n.name && (!eligible || eligible(x.i)));
  if (!rand) {
    return named
      .sort((a, b) => (TIER_RANK[a.n.tier] ?? 9) - (TIER_RANK[b.n.tier] ?? 9) || b.n.weight - a.n.weight || a.i - b.i)
      .slice(0, max).map(x => x.i);
  }
  const idx = named.map(x => x.i);
  for (let k = idx.length - 1; k > 0; k--) {
    const j = Math.floor(rand() * (k + 1));
    [idx[k], idx[j]] = [idx[j], idx[k]];
  }
  return idx.slice(0, max).sort((a, b) => a - b);
}

// ── Arka plan kipi (audit §6.3 + sözleşme: loginTopology) ───────────────────

export type TopologySetting = '' | 'off' | 'anonymous' | 'named' | undefined;
export type GraphSource = 'pending' | 'real' | 'synthetic';

export interface BackdropInput {
  setting: TopologySetting;
  /** ≥ 1100 px. */
  wide: boolean;
  /** Yüksek Kontrast teması (contrast === 'aaa'). */
  highContrast: boolean;
  /** `prefers-reduced-motion: reduce`. */
  reducedMotion: boolean;
  /** pending: özet henüz sonuçlanmadı (yerleşim ilk veriyi bekler). */
  source: GraphSource;
}
export type BackdropMode =
  | { kind: 'none' }
  | { kind: 'canvas'; animated: boolean; synthetic: boolean };

/** Canvas çizilsin mi, hangi kipte? `off`, dar ekran, Yüksek Kontrast, veri bekleniyor → canvas yok. */
export function backdropMode(i: BackdropInput): BackdropMode {
  if (i.setting === 'off' || !i.wide || i.highContrast || i.source === 'pending') return { kind: 'none' };
  return { kind: 'canvas', animated: !i.reducedMotion, synthetic: i.source === 'synthetic' };
}
