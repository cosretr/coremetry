// esRequests — v0.10.1878 (Admin → Elasticsearch "giden istekler"). SAF
// yardımcılar; esRequests.test.ts.
import type { ESRequestSurface } from '@/lib/types';

/** Yüzey etiketi → okunur ad. `route:GET /api/logs` → `GET /api/logs`;
 *  `worker:templater` → `arka plan · templater`; etiketsiz → `etiketsiz`. */
export function esSurfaceText(surface: string): string {
  if (surface === 'diğer') return 'etiketsiz';
  if (surface.startsWith('route:')) return surface.slice('route:'.length);
  if (surface.startsWith('worker:')) return `arka plan · ${surface.slice('worker:'.length)}`;
  return surface;
}

export type EsChange =
  | { kind: 'none' }                       // karşılaştıracak taban ya da trafik yok
  | { kind: 'new' }                        // tabanda yoktu, şimdi var
  | { kind: 'ratio'; ratio: number; up: boolean };

/** Son 15 dk hızının taban hıza oranı. */
export function esChange(recentPerMin: number, basePerMin: number): EsChange {
  if (recentPerMin <= 0) return { kind: 'none' };
  if (basePerMin <= 0) return { kind: 'new' };
  const ratio = recentPerMin / basePerMin;
  return { kind: 'ratio', ratio, up: ratio > 1 };
}

/** Dikkat çekilecek artış mı: en az 2 kat VE dakikada en az 1 istek (gürültü
 *  değil). Renk yalnız bu sapmada (tablo standardı). */
export function esChangeIsSpike(recentPerMin: number, basePerMin: number): boolean {
  const c = esChange(recentPerMin, basePerMin);
  if (recentPerMin < 1) return false;
  return c.kind === 'new' || (c.kind === 'ratio' && c.ratio >= 2);
}

const nf1 = (v: number) => v.toLocaleString('tr-TR', { maximumFractionDigits: 1 });

export function esChangeText(recentPerMin: number, basePerMin: number): string {
  const c = esChange(recentPerMin, basePerMin);
  if (c.kind === 'none') return '—';
  if (c.kind === 'new') return 'yeni';
  return `×${nf1(c.ratio)}`;
}

export function fmtPerMin(v: number): string {
  if (v <= 0) return '0';
  if (v < 0.1) return '< 0,1';
  return nf1(v);
}

/** Tam sayım (kısaltmasız): artışı arayan göz "1.4K" değil "1.350" görmeli. */
export function fmtCount(v: number): string {
  return Math.round(v).toLocaleString('tr-TR');
}

export function fmtMs(v: number): string {
  if (!(v > 0)) return '—';
  if (v >= 1000) return `${nf1(v / 1000)} sn`;
  return `${Math.round(v)} ms`;
}

/** Tablo sırası: sunucu son 15 dk hızına göre sıralı gönderir; kimlik anahtarı. */
export const esSurfaceKey = (r: ESRequestSurface) => r.surface;
