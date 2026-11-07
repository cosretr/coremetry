// depth — v0.10.1919 (operatör: "derinlik olsun, gerçekten güzel bir deneyim
// olsun"). SAF derinlik çekirdeği: 2 boyutlu canvas'ta üç katmanlı derinlik
// yanılsaması (WebGL / 3B kütüphane YOK). Fareyle kayan katman (parallax) YOK:
// şifre yazılırken dikkat dağıtmasın, tutulan nokta imlecin altından kaymasın.
//
//   0 = ön    : büyük, net, daha canlı salınım; isim etiketi yalnız burada
//   1 = orta  : orta boy, yarı soluk
//   2 = arka  : küçük, soluk, yavaş
//
// Katman ÖNEMLE belirlenir (giriş → BFF → veri → servis, sonra ağırlık):
// sistemin iskeleti önde, uzun kuyruk arkada. Çizim sırası arkadan öne.
import type { Layout } from './layout';

export type Depth = 0 | 1 | 2;

/** Ön ve orta katmanın düğüm payı; kalan arka katman. */
export const FRONT_SHARE = 0.3;
export const MID_SHARE = 0.35;

export interface DepthStyle {
  /** Düğüm yarıçapı çarpanı. */
  scale: number;
  nodeAlpha: number;
  edgeAlpha: number;
  edgeWidth: number;
  /** Salınım genliği ve hızı çarpanı (öndekiler daha canlı). */
  driftMul: number;
  speedMul: number;
}

export const DEPTH_STYLE: readonly DepthStyle[] = [
  { scale: 1, nodeAlpha: 0.8, edgeAlpha: 0.2, edgeWidth: 0.9, driftMul: 1.25, speedMul: 1.15 },
  { scale: 0.72, nodeAlpha: 0.42, edgeAlpha: 0.1, edgeWidth: 0.7, driftMul: 0.9, speedMul: 0.85 },
  { scale: 0.5, nodeAlpha: 0.2, edgeAlpha: 0.05, edgeWidth: 0.6, driftMul: 0.6, speedMul: 0.6 },
];

const RANK: Record<string, number> = { gw: 0, bff: 1, db: 2, svc: 3 };

/** Düğüm başına katman: önem sırasına göre ilk %30 ön, sonraki %35 orta, kalan arka. Kararlı. */
export function depthLayers(layout: Layout): Depth[] {
  const n = layout.nodes.length;
  const deg = new Array<number>(n).fill(0);
  for (const e of layout.edges) { deg[e.a]++; deg[e.b]++; }
  const order = layout.nodes.map((nd, i) => ({ nd, i }))
    .sort((a, b) => (RANK[a.nd.tier] ?? 9) - (RANK[b.nd.tier] ?? 9)
      || b.nd.weight - a.nd.weight || deg[b.i] - deg[a.i] || a.i - b.i)
    .map(x => x.i);
  const front = Math.round(n * FRONT_SHARE);
  const mid = Math.round(n * MID_SHARE);
  const out = new Array<Depth>(n).fill(2);
  order.forEach((i, rank) => { out[i] = rank < front ? 0 : rank < front + mid ? 1 : 2; });
  return out;
}

/** Kenar, iki ucundan DAHA DERİNDEKİNİN katmanında çizilir (öndeki nokta arkayı öne çekmez). */
export function edgeDepth(depth: readonly Depth[], a: number, b: number): Depth {
  return Math.max(depth[a] ?? 2, depth[b] ?? 2) as Depth;
}

/** Çizim sırası: arkadan öne; katman içinde dizin sırası (kararlı). */
export function drawOrder(depth: readonly Depth[]): number[] {
  return depth.map((d, i) => ({ d, i })).sort((a, b) => b.d - a.d || a.i - b.i).map(x => x.i);
}

/** Açılışta sahnenin belirmesi (ms). */
export const FADE_IN_MS = 900;

/** Açılış saydamlık çarpanı [0, 1], yumuşak (ease-out). Durağan kipte hep 1. */
export function fadeIn(elapsedMs: number, animated: boolean): number {
  if (!animated) return 1;
  const t = Math.min(1, Math.max(0, elapsedMs / FADE_IN_MS));
  return 1 - (1 - t) * (1 - t) * (1 - t);
}

/** Üzerine gelinen / tutulan düğümün büyüme çarpanı. */
export const FOCUS_SCALE = 1.35;
