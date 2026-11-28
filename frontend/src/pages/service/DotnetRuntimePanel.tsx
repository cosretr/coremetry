// DotnetRuntimePanel — 2026-11-21 (.NET runtime paritesi; operatör: "Aynı
// Elastic APM olduğu gibi"). LAZY chunk (ServiceRuntimeSection yalnız .NET
// serviste yükler).
//
// Metrik adları ELLE yazılmaz: sunucunun runtime profili
// (/api/services/{name}/runtime-signals → runtimesig.Resolve) katalogda
// bulunan yazımı verir — eski OpenTelemetry.Instrumentation.Runtime
// (process.runtime.dotnet.*) ya da .NET 9 semconv (dotnet.*), VM'de Prom adı
// (…_count_total), CH'de noktalı ad. Kart, profilde sinyali olmayan yerde
// çizilmez (boş .NET kartı duvarı yok).
//
// v0.10.2436 (Service › Metrics sekmesi) — kartlar RuntimePodChart'la
// (CorePanel: ipucu, Series listesi, Shift+tık sabitleme) çizilir; pod boyutlu
// her kart medyan + en kötü + eşik üstü pod görünümünde, "Tüm pod'lar" hepsi.
// Tüm kartlar RuntimeCharts ile aynı syncKey (runtime:<servis>) → ortak imleç.
//
// v0.10.2443 — (1) GC payı / thread pool / CPython CPU kartlarına servis p99'u
// geri geldi: CorePanel'in sağ ekseninde, kesikli, etiketli. (2) Nesil kartları
// pod × nesil okur (groupBy pod anahtarları + nesil etiketi); varsayılan nesil
// başına pod medyanı, seçici o pod'un nesilleri, "Tüm pod'lar" hepsi
// (runtimeGenReduce). (4) GC payı eşiği operatörün runtime_alerts ayarından
// (/runtime-signals runtimeAlerts; yoksa varsayılan).
import { useMemo } from 'react';
import { useQueries, useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import { panelMaxDataPoints } from '@/lib/chartStep';
import { useServiceDeploys } from '@/lib/queries';
import { Spinner } from '@/components/Spinner';
import { RuntimePodChart, type RuntimeExtraLine, type RuntimeRightLine } from './RuntimePodChart';
import type { PodSeries } from './runtimePodReduce';
import type { GenPodSeries } from './runtimeGenReduce';
import { runtimeThresholds } from './runtimeThresholds';
import type { FilterExpr, RuntimePodEntity, RuntimeResolvedSignal, RuntimeSignal, SpanMetricSeries } from '@/lib/types';
import { podLineLabel, podScopeLabel, isAnonPodSeries, divideByCount, POD_SCOPE_KEYS, GEN_KEY_IDX } from './runtimePodLabel';
import { useRuntimeProfile } from './runtimeFamily';
import { runtimeSourceBadge } from './runtimeSource';
import { EntityLink } from '@/components/coremetry/entity/EntityLink';

const MB = 1 / (1024 * 1024);
// Follow-up 2026-11-21 (operatör, prod gerçeği): VM serileri host_name (= pod
// adı) taşır — pod boyutu önce host.name, sonra k8s.pod.name (Go
// runtimesig.PodGroupBy ile aynı sıra). Hiçbiri yoksa servis toplamı yedeği.
// v0.10.2464 — kapsam anahtarları (ortam + cluster etiketleri) [4..6]; nesil [7].
const POD_GROUP = `resource.host.name,resource.k8s.pod.name,resource.service.instance.id,cluster,${POD_SCOPE_KEYS}`;

type Group = 'pod' | 'gen' | 'none';
// share — oran sinyali (GC payı, CPU): pod etiketi yoksa toplam pod sayısına
// bölünür (10 pod × %5 = %50 okunmasın).
interface LineSpec { signal: string; labelKey: string; scale: number; group: Group; unit: string; color?: string; dash?: number[]; share?: boolean }
// thresholdOf — sinyalin mutlak eşiği (kart biriminde; GC payı = operatörün
// runtime_alerts GCShareWarnPct'i, yoksa varsayılan). Yoksa pod görünümü
// göreli eşik kullanır. latency — v0.10.2443: servis p99'u sağ eksende.
type Thresholds = ReturnType<typeof runtimeThresholds>;
interface CardSpec { key: string; titleKey: string; lines: LineSpec[]; mode: 'area' | 'line'; thresholdOf?: (t: Thresholds) => number; latency?: boolean }

// Kart sırası Elastic APM .NET görünümünün sırası: GC → bellek → thread
// pool → çekişme → exception → working set.
export const DOTNET_CARDS: CardSpec[] = [
  { key: 'gc', titleKey: 'rt.card.gcShare', mode: 'line', thresholdOf: t => t.gcSharePct, latency: true, lines: [
    { signal: 'gc_time', labelKey: 'rt.line.gcShare', scale: 100, group: 'pod', unit: '%', share: true },
  ] },
  { key: 'gcgen', titleKey: 'rt.card.gcCollections', mode: 'line', lines: [
    { signal: 'gc_collections', labelKey: 'rt.line.collections', scale: 60, group: 'gen', unit: '/min' },
  ] },
  { key: 'heap', titleKey: 'rt.card.heapGen', mode: 'area', lines: [
    { signal: 'heap_used', labelKey: 'rt.line.heap', scale: MB, group: 'gen', unit: 'MB' },
    { signal: 'heap_committed', labelKey: 'rt.line.committed', scale: MB, group: 'none', unit: 'MB', color: 'var(--err)', dash: [5, 4] },
  ] },
  { key: 'alloc', titleKey: 'rt.card.alloc', mode: 'area', lines: [
    { signal: 'alloc_rate', labelKey: 'rt.line.alloc', scale: MB, group: 'pod', unit: 'MB/s' },
  ] },
  { key: 'pool', titleKey: 'rt.card.pool', mode: 'line', latency: true, lines: [
    { signal: 'pool_queue', labelKey: 'rt.line.queue', scale: 1, group: 'pod', unit: '' },
    { signal: 'threads', labelKey: 'rt.line.threads', scale: 1, group: 'none', unit: '', color: 'var(--teal)' },
  ] },
  { key: 'lock', titleKey: 'rt.card.lock', mode: 'line', lines: [
    { signal: 'lock_contention', labelKey: 'rt.line.lock', scale: 1, group: 'pod', unit: '/s' },
  ] },
  { key: 'exc', titleKey: 'rt.card.exceptions', mode: 'line', lines: [
    { signal: 'exceptions', labelKey: 'rt.line.exceptions', scale: 1, group: 'pod', unit: '/s' },
  ] },
  { key: 'ws', titleKey: 'rt.card.workingSet', mode: 'area', lines: [
    { signal: 'working_set', labelKey: 'rt.line.workingSet', scale: MB, group: 'pod', unit: 'MB' },
  ] },
];

// CPython (2026-11-21) — kompakt görünüm: CPU, RSS, GC (nesil), thread, bağlam değişimi.
export const PYTHON_CARDS: CardSpec[] = [
  { key: 'cpu', titleKey: 'rt.card.cpu', mode: 'line', latency: true, lines: [
    { signal: 'cpu_util', labelKey: 'rt.line.cpu', scale: 100, group: 'pod', unit: '%', share: true },
  ] },
  { key: 'rss', titleKey: 'rt.card.rss', mode: 'area', lines: [
    { signal: 'memory_rss', labelKey: 'rt.line.rss', scale: MB, group: 'pod', unit: 'MB' },
  ] },
  { key: 'gcgen', titleKey: 'rt.card.gcCollections', mode: 'line', lines: [
    { signal: 'gc_collections', labelKey: 'rt.line.collections', scale: 60, group: 'gen', unit: '/min' },
  ] },
  { key: 'threads', titleKey: 'rt.card.threads', mode: 'line', lines: [
    { signal: 'threads', labelKey: 'rt.line.threads', scale: 1, group: 'pod', unit: '' },
  ] },
  { key: 'ctx', titleKey: 'rt.card.ctxSwitches', mode: 'line', lines: [
    { signal: 'ctx_switches', labelKey: 'rt.line.ctxSwitches', scale: 1, group: 'pod', unit: '/s' },
  ] },
];

export type ProfilePanelRuntime = 'dotnet' | 'python';
const CARDS_BY_RUNTIME: Record<ProfilePanelRuntime, CardSpec[]> = { dotnet: DOTNET_CARDS, python: PYTHON_CARDS };

/** SAF — profilde sinyali olan kartlar ve onların çözülmüş çizgileri. */
export function plannedCards(resolved: RuntimeResolvedSignal[], runtime: ProfilePanelRuntime = 'dotnet'): { card: CardSpec; lines: { line: LineSpec; res: RuntimeResolvedSignal }[] }[] {
  const by = new Map(resolved.map(r => [r.signal, r]));
  return CARDS_BY_RUNTIME[runtime].map(card => ({
    card,
    lines: card.lines.flatMap(line => {
      const res = by.get(line.signal);
      if (!res) return [];
      // Nesil etiketi çözülemediyse kırılım yok, tek toplam çizgi.
      return [{ line: line.group === 'gen' && !res.genLabel ? { ...line, group: 'none' as Group } : line, res }];
    }),
  })).filter(c => c.lines.length > 0);
}

/** SAF — sinyalin kendi süzgeci + sunucunun ortam kapsamı → JSON (yoksa undefined). */
export function scopedFilters(res: Pick<RuntimeResolvedSignal, 'filterKey' | 'filterValue'>, scope?: FilterExpr[]): string | undefined {
  const fs: FilterExpr[] = [...(scope ?? [])];
  if (res.filterKey) fs.push({ k: res.filterKey, op: '=', v: [res.filterValue ?? ''] });
  return fs.length ? JSON.stringify(fs) : undefined;
}

const LEVEL_BADGE: Record<RuntimeSignal['level'], string> = { critical: 'b-err', warning: 'b-warn', info: 'b-gray' };
const fmt = (v: number) => (Math.abs(v) >= 100 ? v.toFixed(0) : v.toFixed(1));

/** Akıllı sinyal satırları (sunucu hükmü; metin i18n şablonundan). */
export function RuntimeSignalList({ signals, mode }: { signals: RuntimeSignal[]; mode: string }) {
  const tr = useT();
  return (
    <div className="card" data-testid="rt-signals">
      <div className="ov-card-h">
        <h3>{tr('rt.sig.title')}</h3>
        {mode !== 'on' && <span className="badge b-gray" title={tr('rt.sig.modeHint')}>{tr(mode === 'off' ? 'rt.sig.off' : 'rt.sig.shadow')}</span>}
      </div>
      <div className="ov-card-b">
        {signals.length === 0 ? (
          <div className="kc-line">{tr(mode === 'off' ? 'rt.sig.disabled' : 'rt.sig.none')}</div>
        ) : signals.map(s => (
          <div key={s.check} className="kc-line" data-testid={`rt-sig-${s.check}`}>
            <span className={`badge ${LEVEL_BADGE[s.level] ?? 'b-gray'}`}>{tr(`rt.sig.level.${s.level}`)}</span>{' '}
            <strong>{tr(`rt.sig.${s.check}`)}</strong>{' — '}
            <span className="mono">
              {tr('rt.sig.detail')
                .replace('{v}', `${fmt(s.value)}${s.unit === 'items' ? '' : s.unit}`)
                .replace('{b}', `${fmt(s.baseline)}${s.unit === 'items' ? '' : s.unit}`)
                .replace('{t}', `${fmt(s.threshold)}${s.unit === 'items' ? '' : s.unit}`)}
            </span>
            {s.check === 'threadpool-starvation' && s.extra?.completedRatio != null && (
              <> · {tr('rt.sig.completed').replace('{r}', `×${s.extra.completedRatio.toFixed(2)}`)}</>
            )}
            {s.pod && <> · {tr('rt.sig.pod').replace('{pod}', s.pod).replace('{n}', String(s.pods))}</>}
            {s.podEntity && (
              <> · <EntityLink to={{ kind: 'pod', ref: { pod: s.podEntity.pod, cluster: s.podEntity.clusterId, namespace: s.podEntity.namespace, service: s.service, from: 'infra' } }}
                title={s.podEntity.id}>{tr('rt.sig.podLink')}</EntityLink></>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

const POD_LINKS_MAX = 12;

/** Follow-up 2026-11-21 — grafik çizgilerindeki pod adları (host_name) → pod
 *  varlığı bağlantısı; dizin sunucudan (entity katmanı), ad eşlemesi yok. */
export function RuntimePodLinks({ service, entities }: { service: string; entities?: Record<string, RuntimePodEntity> }) {
  const tr = useT();
  const names = useMemo(() => Object.keys(entities ?? {}).sort(), [entities]);
  if (!entities || names.length === 0) return null;
  return (
    <div className="kc-line" data-testid="rt-pod-links">
      <strong>{tr('rt.pods.title')}:</strong>{' '}
      {names.slice(0, POD_LINKS_MAX).map((n, i) => {
        const e = entities[n];
        return (
          <span key={n}>{i > 0 && ' · '}
            <EntityLink className="mono" to={{ kind: 'pod', ref: { pod: e.pod, cluster: e.clusterId, namespace: e.namespace, service, from: 'infra' } }} title={e.id}>{n}</EntityLink>
          </span>
        );
      })}
      {names.length > POD_LINKS_MAX && <> · {tr('rt.pods.more').replace('{n}', String(names.length - POD_LINKS_MAX))}</>}
    </div>
  );
}

export default function DotnetRuntimePanel({ service, from, to, onZoom, onZoomReset, runtime = 'dotnet' }: {
  service: string;
  /** 2026-11-21 — aynı panel CPython kartlarıyla da (profil çözümü ortak). */
  runtime?: ProfilePanelRuntime;
  from: number;
  to: number;
  onZoom?: (fromSec: number, toSec: number) => void;
  onZoomReset?: () => void;
}) {
  const tr = useT();
  const profQ = useRuntimeProfile(service);
  const resolved = useMemo(() => profQ.data?.profile?.signals ?? [], [profQ.data]);
  const planned = useMemo(() => plannedCards(resolved, runtime), [resolved, runtime]);
  const specs = useMemo(() => planned.flatMap(p => p.lines.map(l => ({ card: p.card.key, ...l }))), [planned]);
  // Follow-up 2026-11-21 — sunucunun çözdüğü ortam / küme kapsamı (aynen eklenir).
  const scope = profQ.data?.scopeFilters;
  const filtersOf = (res: RuntimeResolvedSignal) => scopedFilters(res, scope);

  const queries = useQueries({
    queries: specs.map(({ line, res }) => ({
      queryKey: ['svc-runtime-metric', service, res.metric, line.group, res.genLabel ?? '', res.agg, from, to, filtersOf(res) ?? ''],
      queryFn: () => api.metricQuery({
        name: res.metric, service, agg: res.agg,
        filters: filtersOf(res),
        // v0.10.2443 — nesil kartı pod × nesil: pod anahtarları [0..6], nesil [GEN_KEY_IDX].
        groupBy: line.group === 'pod' ? POD_GROUP : line.group === 'gen' ? `${POD_GROUP},${res.genLabel}` : undefined,
        from, to, step: 0, maxDataPoints: panelMaxDataPoints(1),
      }).then(r => r ?? []),
      staleTime: 60_000,
    })),
  });
  // Anonim (pod etiketsiz) oran serisi → aynı gruplamayla seri SAYISI (yalnız
  // o durumda; prod'da host_name olduğundan sorgu çoğu zaman hiç atılmaz).
  const countIdx = useMemo(() => specs.flatMap((sp, i) => (sp.line.group === 'pod' && sp.line.share && sp.res.agg === 'rate'
    && isAnonPodSeries((queries[i]?.data ?? []) as SpanMetricSeries[])) ? [i] : []), [specs, queries]);
  const countQs = useQueries({
    queries: countIdx.map(i => {
      const { res } = specs[i];
      return {
        queryKey: ['svc-runtime-metric-count', service, res.metric, from, to, scopedFilters(res, scope) ?? ''],
        queryFn: () => api.metricQuery({
          name: res.metric, service, agg: 'count', groupBy: POD_GROUP,
          filters: scopedFilters(res, scope),
          from, to, step: 0, maxDataPoints: panelMaxDataPoints(1),
        }).then(r => r ?? []),
        staleTime: 60_000,
      };
    }),
  });
  const countBySpec = useMemo(() => new Map(countIdx.map((i, k) => [i, (countQs[k]?.data ?? []) as SpanMetricSeries[]])), [countIdx, countQs]);

  // v0.10.2443 — servis p99'u (sağ eksen), yalnız bindirmeli kart çizilecekse.
  const wantLatency = planned.some(p => p.card.latency);
  const latQ = useQuery({
    queryKey: ['svc-runtime-latency', service, from, to],
    queryFn: () => api.spanMetricBatch({
      from, to, maxDataPoints: panelMaxDataPoints(1),
      dsl: `service.name = "${service.replace(/"/g, '\\"')}"`,
      aggs: [{ name: 'p99', agg: 'p99', field: 'duration_ms' }],
    }),
    enabled: !!service && wantLatency,
    staleTime: 60_000,
  });
  const latLine = useMemo<RuntimeRightLine | null>(() => {
    const s = latQ.data?.series?.p99?.[0];
    return s ? { label: tr('rt.line.p99'), unit: 'ms', points: s.points.map(p => ({ time: p.time, value: p.value })) } : null;
  }, [latQ.data, tr]);
  const thresholds = useMemo(() => runtimeThresholds(profQ.data?.runtimeAlerts), [profQ.data]);

  const deploysQ = useServiceDeploys(service, from, to);
  const deployNs = useMemo(() => (deploysQ.data?.length ? deploysQ.data.map(x => x.timeUnixNs) : undefined), [deploysQ.data]);

  const cards = useMemo(() => planned.map(({ card }) => {
    const pods: PodSeries[] = [];
    const gens: GenPodSeries[] = [];
    let genCard = false;
    const extra: RuntimeExtraLine[] = [];
    let podless = false;
    let pending = false, failed = false;
    specs.forEach((sp, qi) => {
      if (sp.card !== card.key) return;
      const q = queries[qi];
      if (q?.isPending) pending = true;
      if (q?.isError) failed = true;
      const data = (q?.data ?? []) as SpanMetricSeries[];
      const scale = sp.res.scale * sp.line.scale;
      const pts = (s: SpanMetricSeries) => s.points.map(p => ({ time: p.time, value: p.value == null ? null : p.value * scale }));
      if (sp.line.group === 'pod' && isAnonPodSeries(data)) {
        // Pod etiketi yok: tek toplam çizgi, ne olduğu adında yazar. Oran +
        // rate → pod sayısına bölünmüş ortalama; gauge (avg) zaten ortalama.
        podless = true;
        const cnt = countBySpec.get(qi);
        const divided = sp.line.share && sp.res.agg === 'rate';
        const p0 = pts(data[0]);
        pods.push({
          label: `${tr(sp.line.labelKey)} · ${tr(divided || sp.res.agg !== 'rate' ? 'rt.line.svcAvg' : 'rt.line.svcTotal')}`,
          points: divided && cnt?.[0] ? divideByCount(p0, cnt[0].points) : p0,
        });
      } else if (sp.line.group === 'pod') {
        data.forEach(s => pods.push({
          label: podScopeLabel(podLineLabel(s.groupKey, sp.line.signal), s.groupKey),
          points: pts(s),
        }));
      } else if (sp.line.group === 'gen') {
        genCard = true;
        // Pod etiketi yok → tek "pod" (ad boş): nesil çizgileri aynen, kart not düşer.
        const anon = isAnonPodSeries(data);
        if (anon) podless = true;
        data.forEach(s => gens.push({
          pod: anon ? '' : podScopeLabel(podLineLabel(s.groupKey, sp.line.signal), s.groupKey),
          gen: (s.groupKey[GEN_KEY_IDX] ?? '').trim() || tr(sp.line.labelKey),
          points: pts(s),
        }));
      } else if (data[0]) {
        extra.push({ label: tr(sp.line.labelKey), color: sp.line.color, dashed: !!sp.line.dash, points: pts(data[0]) });
      }
    });
    // Follow-up 2026-11-21 — kaynak ipucu: kartın çözülmüş metrik adları + kaynak.
    const src = runtimeSourceBadge(specs.filter(sp => sp.card === card.key).map(sp => sp.res.metric), tr('rt.src.tip'));
    const unit = card.lines[0]?.unit ?? '';
    const status: 'loading' | 'error' | 'ready' = failed ? 'error' : pending ? 'loading' : 'ready';
    const threshold = card.thresholdOf?.(thresholds);
    const right = card.latency ? latLine : null;
    return { card, pods, gens: genCard ? gens : undefined, extra, podless, src, unit, status, threshold, right };
  }), [planned, specs, queries, countBySpec, tr, thresholds, latLine]);

  if (profQ.isPending) return <div className="rt-slot"><Spinner /></div>;
  if (profQ.isError || !profQ.data) return <div className="kc-line">{tr('rt.err')}</div>;
  if (planned.length === 0) return <div className="kc-line">{tr(runtime === 'python' ? 'rt.python.none' : 'rt.dotnet.none')}</div>;

  return (
    <div className="rt-cards" data-testid={`${runtime}-runtime`}>
      <RuntimeSignalList signals={profQ.data.signals ?? []} mode={profQ.data.mode} />
      <RuntimePodLinks service={service} entities={profQ.data.podEntities} />
      {cards.map(({ card, pods, gens, extra, podless, src, unit, status, threshold, right }) => (
        <RuntimePodChart key={card.key} testId={`rt-card-${card.key}`}
          title={tr(card.titleKey)} storageKey={`rt-${runtime}-${card.key}`} unit={unit}
          service={service} from={from} to={to}
          pods={pods} gens={gens} podless={podless} extra={extra} threshold={threshold} right={right}
          status={status} errorText={tr('rt.err')} deploys={deployNs} mode={card.mode}
          source={src} onZoom={onZoom} onZoomReset={onZoomReset} />
      ))}
    </div>
  );
}
