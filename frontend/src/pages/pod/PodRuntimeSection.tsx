// PodRuntimeSection — 2026-11-21 (operatör: "pod OOM / restart heap büyümesine
// karşı okunabilsin"). Pod sayfasında, pod'un CPU / bellek / restart
// metriklerinin yanında runtime sinyalleri: servisin runtime profili
// (/runtime-signals, sunucu 60 s önbellek) — sinyal satırları YALNIZ varlık
// katmanının bu pod'a çözdüğü sinyaller (podEntity; ad eşlemesi yok) +
// profilden çözülen metriklerin bu pod'a süzülmüş grafikleri (GC payı, heap,
// thread-pool kuyruğu, HTTP havuzu bekleyen, working set / RSS).
// LAZY: Pod.tsx kapalı bölüm açılınca yükler.
import { useMemo } from 'react';
import { useQueries } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import { panelMaxDataPoints } from '@/lib/chartStep';
import { Spinner } from '@/components/Spinner';
import { TimeSeriesPanel, type TSSeries } from '@/components/viz/TimeSeriesPanel';
import type { RuntimeResolvedSignal, SpanMetricSeries } from '@/lib/types';
import { useRuntimeProfile } from '@/pages/service/runtimeFamily';
import { RuntimeSignalList } from '@/pages/service/DotnetRuntimePanel';

const MB = 1 / (1024 * 1024);

// Pod düzeyi kartlar: sinyal → (başlık anahtarı, ölçek, birim).
const POD_CARDS: { signal: string; titleKey: string; scale: number; unit: string }[] = [
  { signal: 'gc_time', titleKey: 'rt.card.gcShare', scale: 100, unit: '%' },
  { signal: 'heap_used', titleKey: 'rt.pod.heap', scale: MB, unit: 'MB' },
  { signal: 'pool_queue', titleKey: 'rt.line.queue', scale: 1, unit: '' },
  { signal: 'http_pending', titleKey: 'rt.sig.http-pool-saturation', scale: 1, unit: '' },
  { signal: 'working_set', titleKey: 'rt.card.workingSet', scale: MB, unit: 'MB' },
  { signal: 'memory_rss', titleKey: 'rt.card.rss', scale: MB, unit: 'MB' },
];

/** SAF — pod'un çizilecek kartları (profilde çözülenler). */
export function podRuntimeCards(resolved: RuntimeResolvedSignal[]) {
  const by = new Map(resolved.map(r => [r.signal, r]));
  return POD_CARDS.flatMap(c => {
    const res = by.get(c.signal);
    return res ? [{ ...c, res }] : [];
  });
}

export default function PodRuntimeSection({ service, pod, from, to }: { service: string; pod: string; from: number; to: number }) {
  const tr = useT();
  const profQ = useRuntimeProfile(service, !!service && !!pod);
  const cards = useMemo(() => podRuntimeCards(profQ.data?.profile?.signals ?? []), [profQ.data]);
  // Pod kimliği metrikte host.name (prod VM: host_name = pod adı) — UI POD_GROUP'un
  // ikinci anahtarı; k8s.pod.name yalnız collector k8sattributes ile gelir.
  const queries = useQueries({
    queries: cards.map(c => ({
      queryKey: ['pod-runtime-metric', service, pod, c.res.metric, from, to],
      queryFn: () => api.metricQuery({
        name: c.res.metric, service, agg: c.res.agg,
        filters: JSON.stringify([
          { k: 'resource.host.name', op: '=', v: [pod] },
          ...(c.res.filterKey ? [{ k: c.res.filterKey, op: '=', v: [c.res.filterValue ?? ''] }] : []),
        ]),
        groupBy: c.res.genLabel || undefined,
        from, to, step: 0, maxDataPoints: panelMaxDataPoints(2),
      }).then(r => r ?? []),
      staleTime: 60_000,
    })),
  });
  if (!service) return <div className="pod-cap">{tr('rt.pod.noService')}</div>;
  if (profQ.isPending) return <Spinner />;
  const sigs = (profQ.data?.signals ?? []).filter(s => s.podEntity?.pod === pod);
  return (
    <div className="rt-cards" data-testid="pod-runtime">
      {profQ.data && <RuntimeSignalList signals={sigs} mode={profQ.data.mode} />}
      {cards.length === 0 && <div className="pod-cap">{tr('rt.pod.none')}</div>}
      {cards.map((c, i) => {
        const data = (queries[i]?.data ?? []) as SpanMetricSeries[];
        const series: TSSeries[] = data.map(s => ({
          label: s.groupKey.filter(Boolean).join('/') || tr(c.titleKey),
          unit: c.unit,
          points: s.points.map(p => ({ time: p.time, value: p.value == null ? null : p.value * c.res.scale * c.scale })),
        }));
        return (
          <div key={c.signal} className="card" data-testid={`pod-rt-${c.signal}`}>
            <div className="ov-card-h"><h3 title={`${c.res.metric} · host.name = ${pod}`}>{tr(c.titleKey)}</h3></div>
            <div className="ov-card-b rt-card-b">
              {queries[i]?.isPending ? <div className="rt-slot"><Spinner /></div>
                : series.length === 0 ? <div className="rt-slot rt-slot--empty">{tr('rt.noData')}</div>
                : <TimeSeriesPanel series={series} height={180} mode="line" syncKey={`podrt:${pod}`} smooth />}
            </div>
          </div>
        );
      })}
    </div>
  );
}
