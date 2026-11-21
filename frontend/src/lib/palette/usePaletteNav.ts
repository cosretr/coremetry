// usePaletteNav — wave 3: komut paletinin gezinme katmanı verisi.
//   • Kayıtlı görünümler — saved_views (GET /api/views, sayfa süzgeçsiz =
//     kullanıcının + paylaşılanlar, sunucu ≤200 satır). Her AÇILIŞTA bir istek;
//     sorgu süzmesi istemcide (navLayer.savedViewRows).
//   • Pod'lar — entity modeli (GET /api/entities?type=pod&q=, küme başına
//     sunucu araması). Küme listesi (GET /api/entities/clusters) modülde bir
//     kez önbelleklenir; en çok PALETTE_POD_CLUSTERS küme sorgulanır. Eager
//     katalog yok: ≥2 karakter, trace kimliği değil, 200 ms debounce.
import { useEffect, useState } from 'react';
import { api } from '@/lib/api';
import type { EntityClusterInfo, EntityRecord, SavedView } from '@/lib/types';
import { PALETTE_DEBOUNCE_MS, paletteServiceQuery } from './usePaletteServices';

export const PALETTE_POD_CLUSTERS = 3;
export const PALETTE_POD_LIMIT = 5;

export function usePaletteViews(open: boolean, openSeq: number): SavedView[] {
  const [views, setViews] = useState<SavedView[]>([]);
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    api.savedViews('')
      .then(r => { if (!cancelled) setViews(Array.isArray(r) ? r : []); })
      .catch(() => { if (!cancelled) setViews([]); });
    return () => { cancelled = true; };
  }, [open, openSeq]);
  return views;
}

let clustersOnce: Promise<EntityClusterInfo[]> | null = null;
function entityClusters(): Promise<EntityClusterInfo[]> {
  clustersOnce ??= api.entityClusters()
    .then(r => (r?.disabled ? [] : r?.clusters ?? []))
    .catch(() => { clustersOnce = null; return []; });
  return clustersOnce;
}

export type PalettePod = { rec: EntityRecord; clusterName: string };

export function usePalettePods(query: string, open: boolean): PalettePod[] {
  const [pods, setPods] = useState<PalettePod[]>([]);
  useEffect(() => {
    const q = paletteServiceQuery(query);
    if (!open || !q) { setPods([]); return; }
    let cancelled = false;
    const tm = window.setTimeout(async () => {
      const clusters = (await entityClusters()).slice(0, PALETTE_POD_CLUSTERS);
      const lists = await Promise.all(clusters.map(c =>
        api.entities({ cluster: c.id, type: 'pod', q, limit: PALETTE_POD_LIMIT })
          .then(r => (r?.items ?? []).map(rec => ({ rec, clusterName: c.name || c.id })))
          .catch(() => [] as PalettePod[])));
      if (!cancelled) setPods(lists.flat());
    }, PALETTE_DEBOUNCE_MS);
    return () => { cancelled = true; clearTimeout(tm); };
  }, [query, open]);
  return pods;
}
