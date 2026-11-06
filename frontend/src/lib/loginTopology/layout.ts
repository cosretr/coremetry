// layout — v0.10.1909 (Login topoloji arka planı, audit §6.3). SAF yerleşim
// çekirdeği: DOM/canvas yok; tohumlu PRNG ile kuvvet yönlendirmeli yerleşim
// (kısa menzilli itme + kenar yayı + duvar itmesi; v0.10.1914). Aynı
// (düğüm sırası, kenarlar, tohum) → AYNI yerleşim. Yerleşim sabit MANTIKSAL bir
// çerçevede (1600×900) bir kez çözülür; canvas boyutuna çizimde ölçeklenir,
// yeniden boyutlandırma yerleşimi bozmaz.
import type { LoginSummaryEdge, LoginSummaryNode } from '@/lib/types';

export const LOGICAL_W = 1600;
export const LOGICAL_H = 900;
const MARGIN = 24;

/** mulberry32 — küçük, hızlı, tohumlu PRNG ([0,1)). */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** FNV-1a: düğüm kimliklerinden kararlı tohum (kimlikler sunucuda tuzlu ve kararlı). */
export function seedOfIds(ids: readonly string[]): number {
  let h = 0x811c9dc5;
  for (const id of ids) {
    for (let i = 0; i < id.length; i++) {
      h ^= id.charCodeAt(i);
      h = Math.imul(h, 0x01000193) >>> 0;
    }
    h ^= 0xff; h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

export interface LayoutNode {
  id: string;
  tier: LoginSummaryNode['tier'];
  weight: number;
  name?: string;
  x: number; y: number;
  /** Kenar derecesi (yaprak = 1 → küçük çizilir). */
  deg: number;
}
export interface LayoutEdge { a: number; b: number; weight: number }
export interface Layout {
  w: number; h: number;
  nodes: LayoutNode[];
  edges: LayoutEdge[];
  /** Sinüs kayması/akan nokta evreleri (düğüm/kenar başına, tohumdan). */
  nodePhase: number[];
  edgePhase: number[];
  seed: number;
}

interface Work extends Layout { dx: Float64Array; dy: Float64Array; rand: () => number; step: number; steps: number }

/** Başlangıç yerleşimi: tohumlu rastgele konumlar. Girdi sırası korunur. */
export function initLayout(
  nodes: readonly LoginSummaryNode[], edges: readonly LoginSummaryEdge[],
  w: number, h: number, seed: number,
): Layout {
  const rand = mulberry32(seed);
  const index = new Map<string, number>();
  nodes.forEach((n, i) => index.set(n.id, i));
  const ledges: LayoutEdge[] = [];
  for (const e of edges) {
    const a = index.get(e.from), b = index.get(e.to);
    if (a == null || b == null || a === b) continue;
    ledges.push({ a, b, weight: clamp01(e.weight) });
  }
  const deg = new Array<number>(nodes.length).fill(0);
  for (const e of ledges) { deg[e.a]++; deg[e.b]++; }
  const lnodes: LayoutNode[] = nodes.map((n, i) => ({
    id: n.id, tier: n.tier, weight: clamp01(n.weight), name: n.name,
    x: MARGIN + rand() * (w - 2 * MARGIN), y: MARGIN + rand() * (h - 2 * MARGIN), deg: deg[i],
  }));
  const nodePhase = lnodes.map(() => rand() * Math.PI * 2);
  const edgePhase = ledges.map(() => rand());
  return { w, h, nodes: lnodes, edges: ledges, nodePhase, edgePhase, seed };
}

function clamp01(v: number): number {
  return Number.isFinite(v) ? Math.min(1, Math.max(0, v)) : 0;
}

/** Çözücü durumu: Layout üstüne ivme tamponları (aynı nesne yerinde güncellenir). */
export function beginSolve(layout: Layout, steps = 200): Work {
  const n = layout.nodes.length;
  return Object.assign(layout, {
    dx: new Float64Array(n), dy: new Float64Array(n),
    rand: mulberry32(layout.seed ^ 0x9e3779b9), step: 0, steps,
  }) as Work;
}

/** İtmenin menzili (k katı): bundan uzak düğümler birbirini itmez. */
const REPULSE_RANGE = 2.5;
/** Duvar itmesinin gücü (k² katı) ve menzili (k katı). */
const WALL_FORCE = 1.2;
const WALL_RANGE = 1.5;

/**
 * Bir yerleşim adımı: KISA MENZİLLİ itme O(n²), kenar çekimi, duvar itmesi, soğuma.
 *
 * v0.10.1914 (operatör-raporlu: "noktalar bir yere öbeklenmiş") — klasik
 * Fruchterman–Reingold'da itme sınırsız menzilliydi ve sınır SERT kırpmaydı:
 * bağlantısız / seyrek bağlı düğümler (gerçek topolojide çoğunluk) birbirini
 * çerçevenin dışına itiyor, kırpma hepsini kenar çizgisine yığıyordu
 * (ölçüm: düğümlerin %45–100'ü kenar bandında). Şimdi:
 *  - itme yalnız REPULSE_RANGE·k içinde (uzak düğümler birbirini duvara sürmez;
 *    tohumlu rastgele başlangıç zaten çerçeveye yayılmıştır),
 *  - duvarlar düğümü İTER (WALL_RANGE·k içinde, mesafeyle artan), sert kırpma
 *    yalnız emniyet,
 *  - merkez çekimi yok (ortayı kart kapatıyor; öbek ortada toplanmasın).
 */
export function stepLayout(s: Work): void {
  const { nodes, edges, w, h, dx, dy } = s;
  const n = nodes.length;
  if (n === 0) { s.step++; return; }
  const k = Math.sqrt((w * h) / n) * 0.9;
  const k2 = k * k;
  const range2 = (REPULSE_RANGE * k) * (REPULSE_RANGE * k);
  dx.fill(0); dy.fill(0);
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      let ex = nodes[i].x - nodes[j].x, ey = nodes[i].y - nodes[j].y;
      let d2 = ex * ex + ey * ey;
      if (d2 > range2) continue;
      if (d2 < 0.01) { ex = s.rand() - 0.5; ey = s.rand() - 0.5; d2 = ex * ex + ey * ey + 0.01; }
      const f = k2 / d2; // itme büyüklüğü k²/d, ex/d birim vektörüyle çarpılınca ex·k²/d²
      dx[i] += ex * f; dy[i] += ey * f; dx[j] -= ex * f; dy[j] -= ey * f;
    }
  }
  for (const e of edges) {
    const ex = nodes[e.a].x - nodes[e.b].x, ey = nodes[e.a].y - nodes[e.b].y;
    const d = Math.sqrt(ex * ex + ey * ey) + 0.001;
    // Yay bilerek GEVŞEK (0.12–0.32): sıkı yay bağlı grafı ortada tek öbeğe topluyordu
    // (ortayı kart kapatır). Kenarlar yine rastgele çiftlerden belirgin kısa kalır.
    const f = (d / k) * (0.12 + 0.2 * e.weight); // (d²/k) / d
    dx[e.a] -= ex * f; dy[e.a] -= ey * f; dx[e.b] += ex * f; dy[e.b] += ey * f;
  }
  // duvar itmesi: duvara WALL_RANGE·k'den yakın düğüm içeri itilir (k²/mesafe).
  const wr = WALL_RANGE * k, wf = WALL_FORCE * k2;
  const wall = (dist: number) => (dist < wr ? wf / Math.max(dist, 1) : 0);
  // soğuma: sıcaklık çerçevenin ~%8'inden 0'a doğrusal
  const temp = Math.max(0.5, (w * 0.08) * (1 - s.step / Math.max(1, s.steps)));
  for (let i = 0; i < n; i++) {
    dx[i] += wall(nodes[i].x) - wall(w - nodes[i].x);
    dy[i] += wall(nodes[i].y) - wall(h - nodes[i].y);
    const len = Math.sqrt(dx[i] * dx[i] + dy[i] * dy[i]) || 1;
    const m = Math.min(len, temp) / len;
    nodes[i].x = Math.min(w - MARGIN, Math.max(MARGIN, nodes[i].x + dx[i] * m));
    nodes[i].y = Math.min(h - MARGIN, Math.max(MARGIN, nodes[i].y + dy[i] * m));
  }
  s.step++;
}

/** Başlangıç + `steps` adım → çözülmüş yerleşim (bir kez, boşta). */
export function layoutOf(
  nodes: readonly LoginSummaryNode[], edges: readonly LoginSummaryEdge[],
  seed: number, steps = 200, w = LOGICAL_W, h = LOGICAL_H,
): Layout {
  const lay = initLayout(nodes, edges, w, h, seed);
  const work = beginSolve(lay, steps);
  for (let i = 0; i < steps; i++) stepLayout(work);
  // çözücü tamponlarını bırak: dönen nesne düz Layout
  const out: Layout = {
    w: lay.w, h: lay.h, nodes: lay.nodes, edges: lay.edges,
    nodePhase: lay.nodePhase, edgePhase: lay.edgePhase, seed: lay.seed,
  };
  return out;
}
