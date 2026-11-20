// pivotContext.ts — v0.10.1229 (paylaşılabilir URL programı, Faz 1-A).
//
// Sayfalar arası pivotların ORTAK bağlam taşıyıcısı. Hedefe özgü linki
// mevcut üreticiler kurar (tracesPivotHref, logsHref, serviceHref, …); bu
// katman yalnız eksik kalan BAĞLAMI ekler: range, env, cluster, namespace.
// Kurallar:
//   · href'te zaten olan param EZİLMEZ (üretici bilerek koymuştur — örn.
//     olaya sabitlenmiş pencere).
//   · pinAtMs verilirse göreli range ("30m") o ana göre mutlak pencereye
//     dondurulur (shareUrl.frozenRangeValue) — zaman noktası olan bir
//     kaynaktan pivot, link sonra açılınca başka saate kaymasın.
//   · Hedefin anlamadığı boyut taşınmaz (carry listesi).
import { useMemo } from 'react';
import { useLocation } from 'react-router';
import { frozenRangeValue } from './shareUrl';

export type PivotDim = 'range' | 'env' | 'cluster' | 'namespace';
export const PIVOT_DIMS: readonly PivotDim[] = ['range', 'env', 'cluster', 'namespace'];
export type PivotCtx = Partial<Record<PivotDim, string>>;

/** SAF: geçerli URL'den bağlam. */
export function pivotCtxFromSearch(search: string): PivotCtx {
  const sp = new URLSearchParams(search);
  const out: PivotCtx = {};
  for (const d of PIVOT_DIMS) {
    const v = (sp.get(d) ?? '').trim();
    if (v) out[d] = v;
  }
  return out;
}

export interface WithPivotOpts {
  /** Taşınacak boyutlar (varsayılan: hepsi). */
  carry?: readonly PivotDim[];
  /** Göreli range'i bu ana göre mutlak pencereye dondur. */
  pinAtMs?: number;
}

/** SAF: href + bağlam → href (eksik boyutlar eklenir, var olanlar korunur). */
export function withPivotCtx(href: string, ctx: PivotCtx, opts: WithPivotOpts = {}): string {
  const hashAt = href.indexOf('#');
  const hash = hashAt < 0 ? '' : href.slice(hashAt);
  const head = hashAt < 0 ? href : href.slice(0, hashAt);
  const qAt = head.indexOf('?');
  const path = qAt < 0 ? head : head.slice(0, qAt);
  const sp = new URLSearchParams(qAt < 0 ? '' : head.slice(qAt + 1));
  for (const d of opts.carry ?? PIVOT_DIMS) {
    const v = ctx[d];
    if (!v || sp.has(d)) continue;
    sp.set(d, v);
  }
  if (opts.pinAtMs !== undefined) {
    const r = sp.get('range');
    const frozen = r ? frozenRangeValue(r, opts.pinAtMs) : null;
    if (frozen) sp.set('range', frozen);
  }
  const q = sp.toString();
  return `${path}${q ? '?' + q : ''}${hash}`;
}

/** Geçerli sayfanın bağlamı (location değişince yenilenir). */
export function usePivotCtx(): PivotCtx {
  const { search } = useLocation();
  return useMemo(() => pivotCtxFromSearch(search), [search]);
}
