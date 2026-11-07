// drag — v0.10.1917 (operatör: "noktalar mouse ile tutularak hareket etsin").
// SAF sürükleme çekirdeği: DOM yok. Canvas katmanı (pages/login/TopologyBackdrop)
// fare olayını ekran konumuna çevirir, isabet testini ve konum güncellemesini
// buradan çağırır.
//
// Davranış: tutulan düğüm imleci izler; 1 adım komşuları yaylı (FOLLOW_1),
// 2 adım komşuları daha az (FOLLOW_2) peşinden gelir, uzaktakiler yerinde
// kalır. Her çağrı komşuları göreli konumlarına doğru bir adım yaklaştırır;
// kare başına çağrılınca esnek bir takip olur. Bırakılınca düğümler
// bıraktıkları yerde kalır (sayfa yenilenince yerleşim baştan kurulur).
import { MARGIN, type Layout } from './layout';
import { nodeStyle, type Pt } from './frame';
import { DEPTH_STYLE, type Depth } from './depth';

/** İsabet toleransı (css px): küçük düğümler de tutulabilsin. */
export const HIT_SLOP = 6;
export const FOLLOW_1 = 0.22;
export const FOLLOW_2 = 0.08;

/**
 * Ekran konumunda (css px) tutulabilecek düğüm; yoksa -1. v0.10.1919 —
 * `depth` verilirse yarıçap katman ölçeğiyle küçülür ve çakışmada ÖNDEKİ
 * kazanır (göze yakın olan tutulur); katman içinde en yakın.
 */
export function hitNode(
  layout: Layout, pos: readonly Pt[], x: number, y: number, slop = HIT_SLOP, depth?: readonly Depth[],
): number {
  let best = -1, bestD = Infinity, bestLayer = Infinity;
  for (let i = 0; i < pos.length; i++) {
    const p = pos[i];
    if (!p) continue;
    const layer = depth?.[i] ?? 0;
    const r = Math.max(nodeStyle(layout.nodes[i]).r * DEPTH_STYLE[layer].scale + slop, 10);
    const d = Math.hypot(p.x - x, p.y - y);
    if (d > r) continue;
    if (layer < bestLayer || (layer === bestLayer && d < bestD)) { best = i; bestD = d; bestLayer = layer; }
  }
  return best;
}

export interface Follower { j: number; k: number; rx: number; ry: number }
export interface Drag { i: number; follow: Follower[] }

/** Sürüklemeyi başlatır: komşuların tutulan düğüme göre göreli konumları sabitlenir. */
export function beginDrag(layout: Layout, i: number): Drag {
  const adj = new Map<number, number[]>();
  const link = (a: number, b: number) => { (adj.get(a) ?? adj.set(a, []).get(a)!).push(b); };
  for (const e of layout.edges) { link(e.a, e.b); link(e.b, e.a); }
  const hop1 = new Set(adj.get(i) ?? []);
  hop1.delete(i);
  const hop2 = new Set<number>();
  for (const j of hop1) for (const k of adj.get(j) ?? []) if (k !== i && !hop1.has(k)) hop2.add(k);
  const base = layout.nodes[i];
  const mk = (j: number, k: number): Follower => ({ j, k, rx: layout.nodes[j].x - base.x, ry: layout.nodes[j].y - base.y });
  return {
    i,
    follow: [
      ...[...hop1].sort((a, b) => a - b).map(j => mk(j, FOLLOW_1)),
      ...[...hop2].sort((a, b) => a - b).map(j => mk(j, FOLLOW_2)),
    ],
  };
}

function clampX(layout: Layout, x: number): number { return Math.min(layout.w - MARGIN, Math.max(MARGIN, x)); }
function clampY(layout: Layout, y: number): number { return Math.min(layout.h - MARGIN, Math.max(MARGIN, y)); }

/**
 * Tutulan düğümü mantıksal (x, y)'ye taşır; komşuları göreli konumlarına doğru
 * k oranında yaklaştırır. Yerinde günceller. Konumlar çerçeve içine kırpılır.
 */
export function dragTo(layout: Layout, d: Drag, x: number, y: number): void {
  const n = layout.nodes[d.i];
  if (!n || !Number.isFinite(x) || !Number.isFinite(y)) return;
  n.x = clampX(layout, x);
  n.y = clampY(layout, y);
  for (const f of d.follow) {
    const m = layout.nodes[f.j];
    if (!m) continue;
    const tx = clampX(layout, n.x + f.rx), ty = clampY(layout, n.y + f.ry);
    m.x += (tx - m.x) * f.k;
    m.y += (ty - m.y) * f.k;
  }
}
