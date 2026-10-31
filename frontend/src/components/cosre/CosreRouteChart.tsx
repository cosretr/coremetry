// CosreRouteChart — v0.10.1738 (operatör onayı, mockup "Endpoint seç" ikinci
// adım). Sohbetin mevcut grafik yolunun (typed chart bloğu → chartBlocks →
// CosreChart) source 'route' dalı; CosreChart tembel yükler.
//   • Throughput (agg 'rate'): toplam + 5xx req/s; Response time: p50 + p95 ms.
//   • Kaynak etiketi başlıkta (VM / CH): sunucu karar verir — VM okuması
//     yapılandırılmışsa VictoriaMetrics, yoksa route MV (spanmetrics_1m).
//   • İki grafik AYNI sorgu anahtarını paylaşır → tek istek. Pencere spec'te
//     mutlak (sunucu yazar); now() yalnız queryFn içinde (v0.5.184).
//   • Yükleniyor / hata CorePanel'de; seri yok / çizilemez → Empty + gerekçe;
//     sunucu notları (env süzgeci uygulanmadı vb.) grafiğin altında.
import { lazy, Suspense } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { Empty, Spinner } from '@/components/Spinner';
import type { CosreChartSpec } from '@/components/cosreChartSpec';
import { routeChartItems, routeChartKind, routeChartParams, routeChartTitle, routeChartUnit } from '@/components/cosreChartSpec';

// CorePanel @grafana/data'ya bağlı: statik bağlamak vendor'ı şişirir (CosreChart notu).
const CorePanelMulti = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

export function CosreRouteChart({ spec }: { spec: CosreChartSpec }) {
  const kind = routeChartKind(spec);
  const q = useQuery({
    queryKey: ['cosre-route-red', spec.service, spec.route ?? '', spec.method ?? '', spec.env ?? '', spec.fromNs ?? 0, spec.toNs ?? 0, spec.rangeS ?? 0],
    queryFn: ({ signal }) => api.cosreRouteRED(routeChartParams(spec, Date.now() * 1e6), signal),
    enabled: !!spec.service && !!spec.route,
    staleTime: 30_000,
  });
  const title = routeChartTitle(spec, q.data?.source);
  const items = routeChartItems(q.data, spec);
  const note = q.data?.skip ?? (q.isSuccess && items.length === 0 ? `${spec.route} için bu pencerede seri yok` : '');
  const src = q.data?.source ?? spec.src ?? '';
  if (!spec.service || !spec.route || note) {
    return (
      <div className="my-2.5 max-w-[560px]" data-route-chart={kind} data-route-src={src}>
        <Empty compact icon={null} title={`${title} — veri yok`}>{note || 'Grafik kurulamadı'}</Empty>
      </div>
    );
  }
  return (
    <div className="my-2.5 max-w-[560px]" data-route-chart={kind} data-route-src={src}>
      <Suspense fallback={<Spinner />}>
        <CorePanelMulti title={title} height={160}
          storageKey={`cosre-route-chart:${spec.service}:${spec.route}:${kind}`}
          loading={q.isLoading} error={q.isError ? 'Grafik verisi alınamadı' : undefined}
          items={items} unit={routeChartUnit(spec)} />
      </Suspense>
      {(q.data?.notes ?? []).map(n => <div key={n} className="mt-0.5 text-[10px] text-muted-foreground">{n}</div>)}
    </div>
  );
}
