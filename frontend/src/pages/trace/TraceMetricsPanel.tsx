// TraceMetricsPanel — v0.10.913: Trace sayfası "Metrics" sekmesi (revize
// mockup Onay 2026-09-25). Trace'in geçtiği pod'lar servise göre gruplu;
// AYNI servisin en çok 4 pod'u CPU / bellek grafiğinde üst üste (hangi
// replika farklı?). Farklı servisler üst üste çizilmez (birim/limit farkı
// yanıltır). Veri Pod sayfasıyla aynı uç (clusterPodDetail, Thanos), yalnız
// sekme açıkken ve yalnız seçili pod'lar için. Trace anı grafikte işaret.
// Seçim + pencere URL'de (?mpod=a,b&mwin=15, replace).
import { useMemo } from 'react';
import { useSearchParams, Link } from 'react-router-dom';
import { useQueries } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { SpanMetricSeries, SpanRow } from '@/lib/types';
import { useEntityEnabled } from '@/lib/queries';
import { MultiLineChart, type DeployMarker } from '@/components/MultiLineChart';
import { Spinner, Empty } from '@/components/Spinner';
import { Chip, SegmentedControl } from '@/components/ui';
import { podDetailPath } from '@/pages/service/podDetailPath';
import {
  tracePods, podsByService, defaultPodSelection, togglePod, traceMetricsWindow, resolveCluster, shortPod,
  TRACE_METRICS_WINDOWS, TRACE_METRICS_DEFAULT_WINDOW, TRACE_METRICS_MAX_PODS, type TraceMetricsWindow,
} from './traceMetrics';

export function TraceMetricsPanel({ spans }: { spans: SpanRow[] }) {
  const [sp, setSp] = useSearchParams();
  const pods = useMemo(() => tracePods(spans), [spans]);
  const groups = useMemo(() => podsByService(pods), [pods]);
  const fallback = useMemo(() => defaultPodSelection(spans, pods), [spans, pods]);
  const urlSel = (sp.get('mpod') ?? '').split(',').filter(p => pods.some(x => x.pod === p));
  const selected = urlSel.length ? urlSel : fallback;
  const winRaw = Number(sp.get('mwin'));
  const win: TraceMetricsWindow = (TRACE_METRICS_WINDOWS as readonly number[]).includes(winRaw) ? winRaw as TraceMetricsWindow : TRACE_METRICS_DEFAULT_WINDOW;
  const { from, to, startNs } = useMemo(() => traceMetricsWindow(spans, win), [spans, win]);
  const { clusters, enabled: entitiesOn, loading } = useEntityEnabled(pods.length > 0);

  const set = (k: string, v: string | null) => setSp(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set(k, v); else next.delete(k);
    return next;
  }, { replace: true });

  const targets = selected.map(p => {
    const tp = pods.find(x => x.pod === p)!;
    return { ...tp, cluster: resolveCluster(tp.clusterValue, clusters) };
  });
  const queries = useQueries({
    queries: targets.map(t => ({
      queryKey: ['trace-pod-metrics', t.cluster, t.namespace, t.pod, from, to],
      queryFn: () => api.clusterPodDetail(t.cluster, t.namespace, t.pod, from, to),
      enabled: !!t.cluster && !!t.namespace,
      staleTime: 60_000,
      refetchOnWindowFocus: false,
    })),
  });

  if (pods.length === 0) {
    return <Empty icon="—" title="Bu trace'in span'larında k8s.pod.name yok — pod metrikleri gösterilemiyor." />;
  }

  const cpu: SpanMetricSeries[] = [];
  const mem: SpanMetricSeries[] = [];
  const notes: string[] = [];
  targets.forEach((t, i) => {
    if (!t.cluster) { notes.push(`${shortPod(t.pod)}: cluster "${t.clusterValue || '—'}" bir Remote Cluster kaydına eşlenmemiş`); return; }
    if (!t.namespace) { notes.push(`${shortPod(t.pod)}: span'larda k8s.namespace.name yok`); return; }
    const trend = queries[i]?.data?.trend ?? [];
    if (queries[i]?.isSuccess && trend.length === 0) { notes.push(`${shortPod(t.pod)}: bu pencerede Thanos örneği yok`); return; }
    const label = shortPod(t.pod);
    if (trend.length) {
      cpu.push({ groupKey: [label], points: trend.map(p => ({ time: p.bucket * 1e9, value: p.cpuCores })) });
      mem.push({ groupKey: [label], points: trend.map(p => ({ time: p.bucket * 1e9, value: p.memBytes })) });
    }
  });
  const pending = queries.some(q => q.isPending && q.fetchStatus !== 'idle');
  const failed = queries.some(q => q.isError);
  const marker: DeployMarker[] = [{ timeUnixNs: startNs, label: 'trace', description: 'trace başlangıcı' }];
  const xRange = { from: from / 1e9, to: to / 1e9 };
  const selSvc = targets[0]?.service ?? '';

  return (
    <div style={{ display: 'grid', gap: 12 }}>
      <div style={{ display: 'grid', gap: 6 }}>
        {groups.map(g => (
          <div key={g.service} style={{ display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap', fontSize: 12 }}>
            <span style={{ minWidth: 160, color: 'var(--text2)' }} className="mono">{g.service || '(servissiz)'}</span>
            {g.pods.map(p => {
              const on = selected.includes(p.pod);
              return (
                <Chip key={p.pod} active={on} tone="accent"
                  title={`${p.pod} · ${p.spans} span${p.errors ? ` · ${p.errors} hata` : ''}`}
                  onClick={() => set('mpod', togglePod(selected, p.pod, pods).join(','))}>
                  {shortPod(p.pod)} · {p.spans}{p.errors > 0 && <span style={{ color: 'var(--err)' }}> ⚠{p.errors}</span>}
                </Chip>
              );
            })}
          </div>
        ))}
      </div>
      <div style={{ display: 'flex', gap: 6, alignItems: 'center', fontSize: 12, flexWrap: 'wrap' }}>
        <span style={{ color: 'var(--text3)' }}>pencere: trace ±</span>
        <SegmentedControl size="sm" aria-label="Metrik penceresi" value={String(win)}
          onChange={v => set('mwin', Number(v) === TRACE_METRICS_DEFAULT_WINDOW ? null : v)}
          options={TRACE_METRICS_WINDOWS.map(w => ({ value: String(w), label: w === 60 ? '1 sa' : `${w} dk` }))} />
        <span style={{ flex: 1 }} />
        <span style={{ color: 'var(--text3)' }}>{selSvc} · {selected.length}/{TRACE_METRICS_MAX_PODS} pod · aynı servisin pod'ları üst üste</span>
        {targets[0]?.cluster && (
          <Link className="sec" to={podDetailPath({ pod: targets[0].pod, cluster: targets[0].cluster, namespace: targets[0].namespace, service: selSvc })}>Pod sayfasında aç ↗</Link>
        )}
      </div>
      {loading || pending ? <Spinner />
        : !entitiesOn ? <Empty icon="—" title="Cluster kayıtları (entity katmanı) kapalı — pod metrikleri Thanos'tan okunamıyor." />
        : failed ? <Empty icon="✗" title="Thanos pod metrikleri okunamadı." />
        : cpu.length === 0 ? <Empty icon="—" title="Seçili pod'lar için metrik yok.">{notes.join(' · ')}</Empty>
        : (
          <>
            {/* v0.10.916 (operator-reported) — bellek üstte; iki panel de y
                tabanı 0 (zeroBase): %0.1'lik oynama zirve gibi çizilmesin. */}
            <div>
              <div style={{ fontSize: 11, color: 'var(--text2)', marginBottom: 4 }}>Memory (bytes)</div>
              <MultiLineChart series={mem} height={180} syncKey={`trace-metrics-${selSvc}`} unit="bytes" deploys={marker} xRange={xRange} zeroBase />
            </div>
            <div>
              <div style={{ fontSize: 11, color: 'var(--text2)', marginBottom: 4 }}>CPU (cores)</div>
              <MultiLineChart series={cpu} height={180} syncKey={`trace-metrics-${selSvc}`} unit="cores" deploys={marker} xRange={xRange} zeroBase />
            </div>
            <div className="pod-cap">
              kaynak Thanos (Pod sayfasıyla aynı uç) · "trace" işareti trace başlangıcı
              {notes.length > 0 && <> · {notes.join(' · ')}</>}
            </div>
          </>
        )}
    </div>
  );
}
