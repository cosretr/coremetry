// endpointPick — v0.10.1737 (operatör onayı, mockup "Endpoint seç"). SAF.
// CoSRE v2'de tek servisi anıp rota söylemeyen soru → sunucu tek cümle + data-pick
// (kind=endpoint) yayınlar; kart bu yükten çizilir. Kararın kaynağı SUNUCU
// (needsEndpointPick, api chat_endpoint_pick.go). Burada: yükün okunması, satır
// biçimi, arama sonucunun aynı sıraya dizilmesi ve iki düğmenin gönderimi.
//   • "İncele" → seçilen route AYNI sohbette yeni tur: metin "<servis> <route>",
//     bağlam servis + env + pencere (kartın) + skipEndpointPick (kart tekrar çıkmaz).
//   • "Hepsini özetle" → aynı soru skipEndpointPick ile yeniden sorulur (bugünkü
//     servis özeti; 1725'in teamPick yeniden-sor deseni).
import type { CosreEndpointPick, CosreEndpointPickItem, EndpointRow } from '@/lib/types';
import { parseDataPick } from '@/lib/cosre/protocol';

type Part = { type: string; data?: unknown };

/** Mesajın seçici yükü (son geçerli data-pick; yoksa null). */
export function pickOf(parts: readonly Part[]): CosreEndpointPick | null {
  let out: CosreEndpointPick | null = null;
  for (const p of parts) {
    if (p.type === 'data-pick') out = parseDataPick(p) ?? out;
  }
  return out;
}

/** Kart çizilsin mi: endpoint yükü + en az iki endpoint (sunucu ≤1'de yük basmaz). */
export function showEndpointPick(p: CosreEndpointPick | null | undefined): p is CosreEndpointPick {
  return !!p && p.kind === 'endpoint' && p.total >= 2 && p.items.length > 0;
}

/** Sunucuyla aynı sıra: hata % azalan, p95 azalan, route artan. */
export function sortPickItems(items: readonly CosreEndpointPickItem[]): CosreEndpointPickItem[] {
  return [...items].sort((a, b) => (b.errPct - a.errPct) || (b.p95Ms - a.p95Ms) || a.route.localeCompare(b.route));
}

/** Arama cevabı (/api/endpoints satırları) → kart satırları; req/s = çağrı / pencere. */
export function pickItemsFromRows(rows: readonly EndpointRow[] | null | undefined, rangeS: number): CosreEndpointPickItem[] {
  const w = rangeS > 0 ? rangeS : 1;
  const out: CosreEndpointPickItem[] = [];
  for (const r of rows ?? []) {
    const route = `${r.method ?? ''} ${r.path ?? ''}`.trim();
    if (!route || !(r.calls > 0)) continue;
    out.push({ route, rps: r.calls / w, p95Ms: r.p95Ms ?? 0, errPct: r.errorRate ?? 0 });
  }
  return sortPickItems(out);
}

const tr1 = (v: number) => v.toLocaleString('tr-TR', { minimumFractionDigits: 1, maximumFractionDigits: 1 });

/** "42,1" — req/s; 0,1 altı "<0,1". */
export function fmtRps(v: number): string {
  return v > 0 && v < 0.1 ? '<0,1' : tr1(v);
}

/** "210 ms" / "1,9 sn". */
export function fmtP95(ms: number): string {
  return ms >= 1000 ? `${tr1(ms / 1000)} sn` : `${Math.round(ms)} ms`;
}

/** "%4,8". */
export function fmtErrPct(v: number): string {
  return `%${tr1(v)}`;
}

/** Yalnız sapan değer renklenir: hata ≥ %1, p95 ≥ 1 sn. */
export const isBadErr = (v: number) => v >= 1;
export const isBadP95 = (ms: number) => ms >= 1000;

/** "+10 endpoint daha" sayısı (tavanlıysa "+N+"). */
export function moreLabel(p: CosreEndpointPick): string {
  const n = Math.max(0, p.total - p.items.length);
  return n > 0 ? `+${n}${p.capped ? '+' : ''} endpoint daha · yazarak ara` : '';
}

/** "İncele" turunun metni: servis + route (sunucu çapası; mockup'ta route). */
export function endpointInspectText(p: CosreEndpointPick, route: string): string {
  return `${p.service} ${route.trim()}`;
}

/** "İncele" turunun bağlamı: base korunur, servis + env + pencere kartınki, kart atlanır. */
export function withEndpointInspect(base: Record<string, unknown> | undefined, p: CosreEndpointPick): Record<string, unknown> {
  return { ...(base ?? {}), service: p.service, ...(p.env ? { env: p.env } : {}), rangeS: p.rangeS, skipEndpointPick: true };
}

/** "Hepsini özetle": aynı soru kart atlanarak (bugünkü servis özeti). */
export function withEndpointSummarize(base: Record<string, unknown> | undefined): Record<string, unknown> {
  return { ...(base ?? {}), skipEndpointPick: true };
}
