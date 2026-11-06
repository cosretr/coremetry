import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { logsBucketSec } from '@/lib/chartStep';
import { logsMarkerRegions, logsMarkers, type LogsMarker } from '@/lib/logsMarkers';
import type { ChartTimeRegion } from '@/lib/chart/overlays';

// useLogsMarkers — v0.10.1895 (Log Search Faz 4 dilim 4.1). /logs histogram
// işaretlerinin verisi: `/api/annotations` (ClickHouse + API, ES YOK), sunucu
// önbelleği 30 sn. Sayfa açılışına en çok BİR istek ekler ve yalnız TEK servis
// seçiliyken (virgüllü çoklu seçimde / servissizken istek YOK). `from/to`
// anahtarlı: global yenileme saati pencereyi taşıdığında yeniden çeker;
// kendi refetchInterval'ı yok (frontend-design-system: aralık anahtarlı
// sorguya refetchInterval eklenmez). queryKey ServiceAnnotationLane ile aynı
// şekil → servis sayfasıyla önbellek paylaşılır.
export function logsMarkerService(service: string | undefined): string {
  const s = (service ?? '').trim();
  return s && !s.includes(',') ? s : '';
}

export function useLogsMarkers(o: { service?: string; fromNs?: number; toNs?: number; enabled: boolean }): {
  markers: LogsMarker[]; regions: ChartTimeRegion[]; truncated: boolean;
} {
  const svc = logsMarkerService(o.service);
  const on = o.enabled && !!svc && !!o.fromNs && !!o.toNs;
  const q = useQuery({
    queryKey: ['annotations', svc, o.fromNs, o.toNs],
    queryFn: () => api.annotations(svc, o.fromNs!, o.toNs!),
    enabled: on, staleTime: 30_000,
  });
  const items = on ? q.data?.items : undefined;
  return useMemo(() => {
    if (!items || !o.fromNs || !o.toNs) return { markers: [], regions: [], truncated: false };
    const markers = logsMarkers(items, o.fromNs, o.toNs);
    const bucket = logsBucketSec((o.toNs - o.fromNs) / 1e9);
    return { markers, regions: logsMarkerRegions(markers, bucket), truncated: !!q.data?.truncated };
  }, [items, o.fromNs, o.toNs, q.data?.truncated]);
}
