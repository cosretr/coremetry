// frame — v0.10.1909. SAF kare çekirdeği: kare hızı kapısı, çizim tavanları,
// seyreltme ve arka plan KİP kararı. v0.10.1928 — galaksi (galaxy.ts) ile düğüm
// stili / kayma / etiket seçimi oraya taşındı. Canvas çizimi
// `pages/login/TopologyBackdrop.tsx`'te; burada DOM yok.

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
