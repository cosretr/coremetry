// lib/palette/usePaletteEndpoints — v0.10.1658 (etkileşim dilimi P2, plan §1.1).
// v0.10.1653 gövdesinin endpoint (operation) ve Oracle operasyon aramaları
// AYNEN: sunucu taraflı, 200 ms debounce + stale guard; endpoint ≥2 karakter,
// Oracle ≥3 (oracleOperationQuery); trace id ise ikisi de boş.
import { useEffect, useState } from 'react';
import { api } from '@/lib/api';
import { operationTracesHref } from '@/lib/pivotHref';
import { currentRange } from '@/lib/useUrlRange';
import { oracleOperationQuery, oraclePaletteResults } from '@/lib/paletteOracle';
import type { PaletteResult } from './pages';
import { TRACE_ID_RE } from './results';
import { PALETTE_DEBOUNCE_MS, paletteServiceQuery } from './usePaletteServices';

export const PALETTE_ENDPOINT_LIMIT = 6;
export const PALETTE_ORACLE_LIMIT = 6;

/** Oracle araması için sorgu (pure): ≥3 karakter ve trace id değil. */
export function paletteOracleQuery(query: string): string | null {
  const q = oracleOperationQuery(query);
  return !q || TRACE_ID_RE.test(q) ? null : q;
}

export function usePaletteEndpoints(query: string, open: boolean, locationSearch: string): PaletteResult[] {
  const [endpoints, setEndpoints] = useState<PaletteResult[]>([]);
  // Endpoint search — server-debounced operation lookup WITHOUT eager-loading
  // the operation catalogue (10k+ ops; picker = server-side search constraint).
  useEffect(() => {
    const q = paletteServiceQuery(query);
    if (!open || !q) { setEndpoints([]); return; }
    let cancelled = false;
    const t = window.setTimeout(() => {
      api.operationNames(undefined, q, PALETTE_ENDPOINT_LIMIT)
        .then(r => {
          if (cancelled) return;
          // v0.9.855 (UX denetimi K4) — kesin isim filtresi + pencere
          // (pivotHref ailesi); `?operation=` /traces'te SİLİNİYORDU.
          setEndpoints((r?.names ?? []).map(name => ({
            kind: 'endpoint' as const,
            label: name,
            hint: 'Endpoint',
            to: operationTracesHref({ window: currentRange(locationSearch), operation: name }),
          })));
        })
        .catch(() => { if (!cancelled) setEndpoints([]); });
    }, PALETTE_DEBOUNCE_MS);
    return () => { cancelled = true; clearTimeout(t); };
  }, [query, open, locationSearch]);
  return endpoints;
}

export function usePaletteOracleOps(query: string, open: boolean, locationSearch: string): PaletteResult[] {
  const [oracleOps, setOracleOps] = useState<PaletteResult[]>([]);
  // v0.10.1002 — Oracle operasyon adı araması: "TRANSFER_SERVICE" yazan
  // operatör o operasyonun trace'lerine gider. Etkin Oracle kaynağı yoksa
  // sunucu CH'ye gitmeden boş döner.
  useEffect(() => {
    const q = paletteOracleQuery(query);
    if (!open || !q) { setOracleOps([]); return; }
    let cancelled = false;
    const t = window.setTimeout(() => {
      api.oracleOperations(q, PALETTE_ORACLE_LIMIT)
        .then(r => { if (!cancelled) setOracleOps(oraclePaletteResults(r, currentRange(locationSearch))); })
        .catch(() => { if (!cancelled) setOracleOps([]); });
    }, PALETTE_DEBOUNCE_MS);
    return () => { cancelled = true; clearTimeout(t); };
  }, [query, open, locationSearch]);
  return oracleOps;
}
