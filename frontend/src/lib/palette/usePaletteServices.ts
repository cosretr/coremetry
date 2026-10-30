// lib/palette/usePaletteServices — v0.10.1658 (etkileşim dilimi P2, plan §1.1).
// v0.10.1653 gövdesinin servis araması AYNEN: sunucu taraflı, 200 ms debounce
// + stale guard, ≥2 karakter, trace id değilse. ASLA eager katalog değil.
import { useEffect, useState } from 'react';
import { api } from '@/lib/api';
import { serviceHref } from '@/lib/serviceHref';
import type { PaletteResult } from './pages';
import { TRACE_ID_RE } from './results';

export const PALETTE_DEBOUNCE_MS = 200;
export const PALETTE_SERVICE_LIMIT = 20;

/** Sorgu servis araması için uygun mu (pure). */
export function paletteServiceQuery(query: string): string | null {
  const q = query.trim();
  return q.length < 2 || TRACE_ID_RE.test(q) ? null : q;
}

export function usePaletteServices(query: string, open: boolean): PaletteResult[] {
  const [services, setServices] = useState<PaletteResult[]>([]);
  // v0.8.518 (perf raporu #11) — servis araması SUNUCUDA. Eski eager
  // /api/services fetch'i 200 ile kesiyordu: 1400+ servisli prod'da
  // katalogun çoğu ⌘K'da hiç bulunamıyordu (fiilen ölü) ve her ilk
  // açılış tam liste indiriyordu.
  useEffect(() => {
    const q = paletteServiceQuery(query);
    if (!open || !q) { setServices([]); return; }
    let cancelled = false;
    const t = window.setTimeout(() => {
      api.serviceNames(q, PALETTE_SERVICE_LIMIT)
        .then(r => {
          if (cancelled) return;
          setServices((r?.names ?? []).map(name => ({
            kind: 'service' as const,
            label: name,
            hint: 'Service',
            // Penceresiz — pencere katmanı navHref'te (v0.9.967/968).
            to: serviceHref(name),
          })));
        })
        .catch(() => { if (!cancelled) setServices([]); });
    }, PALETTE_DEBOUNCE_MS);
    return () => { cancelled = true; clearTimeout(t); };
  }, [query, open]);
  return services;
}
