// EntityDetail.tsx — v0.10.135 (DETAY SAYFALARI adım 1; adım 5 node/namespace
// görünümleriyle büyütür). /entity?id=&at=&range= — pod dışı entity'lerin
// (node / namespace / workload / container) hedef sayfası: kimlik +
// geçerlilik + ebeveyn zinciri + etiketler + çocuk sayıları + bu entity
// altındaki servisler ve pod'lar (entity_seen_5m). Ölü entity 404 DEĞİL:
// "artık mevcut değil, son görülme X" + tarihçe. Bayrak kapalı → açık ilan.
import { useMemo, useState } from 'react';
import { DEFAULT_RANGE_PRESET } from '@/lib/useUrlRange';
import { Link } from 'react-router';
import { urlSpec, useUrlParams, zParam, zInt, z } from '@/lib/urlSchema';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { MultiLineChart } from '@/components/MultiLineChart';
import { Stat } from '@/features/dependencies/panels/shared';
import { namedSeriesToSeries } from '@/pages/clusters/trendSeries';
import { clampThanosWindow, THANOS_MAX_WINDOW_LABEL } from '@/lib/thanosWindow';
import { fmtBytes } from '@/lib/utils';
import { fmtCores } from '@/pages/clusters/thresholds';
import { ThanosTrendPanel } from '@/pages/clusters/TrendPanel';
import { Topbar } from '@/components/Topbar';
import { PageShell } from '@/components/coremetry/PageShell';
import { Stack, Row, Badge, Button } from '@/components/ui';
import { Spinner, Empty } from '@/components/Spinner';
import { usePageZoomRange } from '@/lib/chart/usePageZoomRange';
import { timeRangeToNs, fmtDateTime } from '@/lib/utils';
import { useEntityEnabled, useEntity, useEntityServices, useEntityLatency, useEntityLogCoverage } from '@/lib/queries';
import { entityLiveness } from '@/lib/entityHref';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef, type DataTableStateProps } from '@/components/ui/DataTable';
import type { EntityDetailResponse, EntityRecord, EntityServiceRow, EntitySeenAgg, EntityServicesResponse, TimeRange } from '@/lib/types';
import { t, tf, useLang, type Lang } from '@/lib/i18n';
import { duration } from '@/lib/format/duration';
import { percent } from '@/lib/format/percent';
import { count, fixed } from '@/lib/format/number';
import { errorTone } from '@/lib/format/tone';
import { EntityLink } from '@/components/coremetry/entity/EntityLink';

// 2026-11-21 — URL durumu zod şemasıyla (lib/urlSchema): bozuk `at` → 0
// (anlık görünüm), kimlik ham metin (≤1024).
const ENTITY_URL_SPEC = urlSpec({
  id: zParam(z.string().check(z.maxLength(1024)), ''),
  at: zParam(zInt({ min: 0 }), 0),
});

export default function EntityDetail() {
  const [{ id, at }] = useUrlParams(ENTITY_URL_SPEC);
  const lang = useLang();
  const { range, setRange } = usePageZoomRange(DEFAULT_RANGE_PRESET);
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);
  const { enabled, loading } = useEntityEnabled();
  const entQ = useEntity(id, at || undefined, enabled && !!id);
  // v0.10.1420 — sunucu wl: kimliğini envanter kaydına çözdüyse (resolvedFrom) çocuk sorguları ÇÖZÜLEN kimlikle.
  const effId = entQ.data?.entity.id || id;
  const svcQ = useEntityServices(effId, { from, to }, enabled && !!id && !!entQ.data);
  const name = entQ.data?.entity.name ?? (id.includes('/') ? id.slice(id.lastIndexOf('/') + 1) : id);
  const type = entQ.data?.entity.type ?? (id.includes(':') ? id.slice(0, id.indexOf(':')) : 'entity');
  return (
    <>
      <Topbar title={`${type} · ${name}`} range={range} onRangeChange={setRange} />
      <PageShell>
        {!id && <Empty icon="—" title={t('entityD.noId', lang)} />}
        {!!id && !enabled && !loading && (
          <Empty icon="—" title={t('entityD.disabled', lang)}>{t('entityD.disabledHint', lang)}</Empty>
        )}
        {!!id && enabled && (entQ.isPending
          ? <Spinner />
          : (entQ.error || !entQ.data)
            ? <Empty icon="—" title={t('entityD.notFound', lang)}>
                <span className="mono">{id}</span>
                <EntityNotFoundLinks id={id} />
              </Empty>
            : <Body data={entQ.data} svc={svcQ.data} svcError={svcQ.error ? String(svcQ.error) : undefined} at={at} pageRange={range} from={from} to={to} />)}
      </PageShell>
    </>
  );
}

// v0.10.1420 (operatör: "Deployment Workload basınca service'e girsin, şu an
// bulamıyor") — envanterde olmayan bir iş yükü kimliği çıkmaz sokak değil:
// kimlikten (wl:<cid>/<ns>/<tür>/<ad>) servis araması ve rollout listesi.
function EntityNotFoundLinks({ id }: { id: string }) {
  const lang = useLang();
  if (!id.startsWith('wl:')) return null;
  const parts = id.slice(3).split('/');
  if (parts.length < 4) return null;
  const [, ns, , ...rest] = parts;
  const name = rest.join('/');
  const svcQ = new URLSearchParams({ q: name, namespace: ns });
  const roQ = new URLSearchParams({ workload: name, namespace: ns });
  return (
    <div style={{ marginTop: 8, display: 'flex', gap: 12, justifyContent: 'center', flexWrap: 'wrap' }}>
      <Link to={`/services?${svcQ.toString()}`}>{tf('entityD.searchServices', lang, { name })}</Link>
      <Link to={`/rollouts?${roQ.toString()}`}>{t('entityD.workloadChanges', lang)}</Link>
    </div>
  );
}

// v0.10.947 (tablo standardı T1) — iki kayıt listesi (cluster entity'sinde
// penceredeki her servis / pod gelir) useDataTable'da: sıra `initialSort`
// verilmediği için sunucununki; sayılar arayüz fontunda, sağa yaslı (S2).
// i18n — başlıklar arayüz dilinde (PodServicesTable deseni).
const svcCols = (lang: Lang): ColumnDef<EntityServiceRow>[] => [
  { id: 'service', label: t('podpage.col.service', lang), sortValue: s => s.service, naturalDir: 'asc', flex: true, minWidth: 160 },
  { id: 'pods',    label: t('entityD.col.pods', lang),  sortValue: s => s.pods,    numeric: true, width: 80 },
  { id: 'spans',   label: t('podpage.col.spans', lang), sortValue: s => s.spans,   numeric: true, width: 100 },
  { id: 'errors',  label: t('podpage.col.errors', lang), sortValue: s => s.errors,  numeric: true, width: 90 },
  { id: 'avgMs',   label: t('podpage.col.avgMs', lang), sortValue: s => s.avgMs,   numeric: true, width: 90 },
];
const podSvcCols = (lang: Lang): ColumnDef<EntitySeenAgg>[] => [
  { id: 'pod',       label: t('entityD.col.pod', lang), sortValue: r => r.pod,       naturalDir: 'asc', flex: true, minWidth: 160 },
  { id: 'namespace', label: t('entityD.col.namespace', lang), sortValue: r => r.namespace, naturalDir: 'asc', width: 160 },
  { id: 'service',   label: t('podpage.col.service', lang), sortValue: r => r.service,   naturalDir: 'asc', width: 200 },
  { id: 'spans',     label: t('podpage.col.spans', lang), sortValue: r => r.spans,     numeric: true, width: 90 },
  { id: 'errors',    label: t('podpage.col.errors', lang), sortValue: r => r.errors,    numeric: true, width: 80 },
  { id: 'avgMs',     label: t('podpage.col.avgMs', lang), sortValue: r => r.avgMs,     numeric: true, width: 90 },
  { id: 'lastSeen',  label: t('podpage.col.lastSeen', lang), sortValue: r => Date.parse(r.lastSeen), width: 170 },
];

// v0.10.2263 — /api/logs?entity= kapsamı olan tipler (logs_entity.go).
const LOG_COVERAGE_TYPES = new Set(['pod', 'namespace', 'workload', 'service', 'cluster']);

function Body({ data, svc, svcError, at, pageRange, from, to }: { data: EntityDetailResponse; svc?: EntityServicesResponse; svcError?: string; at: number; pageRange: TimeRange; from: number; to: number }) {
  const lang = useLang();
  const { entity, parents, children, lifetimes, cluster, atMatch } = data;
  const [showLabels, setShowLabels] = useState(false);
  const live = entityLiveness(entity);
  // v0.10.2263 (S6 parça 4) — log kapsama işareti yalnız log kapsamı olan
  // tiplerde ve yalnız "şimdi" görünümünde (geçmiş an için anlamsız).
  const logCov = useEntityLogCoverage(entity.id, !at && LOG_COVERAGE_TYPES.has(entity.type)).data;
  const hrefOpts = { range: pageRange, at: at || undefined, clusterName: cluster?.name };
  const chain = [...parents].reverse().filter(p => p.type !== 'cluster');
  const labels = Object.entries(entity.labels ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const kids = Object.entries(children ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const rows = svc?.rows ?? [];
  const svcColsL = useMemo(() => svcCols(lang), [lang]);
  const podColsL = useMemo(() => podSvcCols(lang), [lang]);
  const svcDt = useDataTable<EntityServiceRow>({ storageKey: 'entity-detail-services', columns: svcColsL, rows: svc?.services ?? [] });
  const podDt = useDataTable<EntitySeenAgg>({ storageKey: 'entity-detail-pod-services', columns: podColsL, rows });
  // v0.10.954 — tablo standardı T12: servis tablosunun yükleniyor / hata /
  // boş durumu tablonun İÇİNDE, başlık durur (sunucu hata metni korunur).
  // Yenileme eski satırlar dururken düşerse hata eskisi gibi tablonun
  // üstünde (satırlar kazanır). Pod × servis tablosu bilinçli kendini
  // gizler (boşken çizilmez) — kalır.
  const svcState: Omit<DataTableStateProps<EntityServiceRow>, 'dt'> =
    svcError ? { kind: 'error', message: tf('entityD.svcReadErr', lang, { err: svcError }) }
    : !svc ? { kind: 'loading' }
    : { kind: 'empty', message: t('entityD.svcEmpty', lang) };
  return (
    <Stack gap={4}>
      <Row gap={2} wrap>
        {cluster && (
          <EntityLink to={{ kind: 'record', rec: { type: 'cluster', id: `cluster:${cluster.id}`, name: cluster.name, clusterId: cluster.id }, opts: hrefOpts }} className="sec" title={cluster.id}>{cluster.name}</EntityLink>
        )}
        {chain.map(p => (
          <Row key={p.id} gap={2}>
            <span className="field-hint">›</span>
            <EntityLink to={{ kind: 'record', rec: p, opts: hrefOpts }} className="sec" title={p.id}>{p.type === 'workload' ? `${p.labels?.kind ?? 'workload'}/${p.name}` : p.name}</EntityLink>
          </Row>
        ))}
        <span className="field-hint">›</span>
        <Badge tone="info" title={entity.id}>{entity.type === 'workload' ? `${entity.labels?.kind ?? 'workload'}/${entity.name}` : entity.name}</Badge>
        {/* v0.10.929 (K5) — 'live' olağan yaşam döngüsü: nötr; stale/gone renkli kalır. */}
        {live === 'live' && <Badge>{t('entityD.live', lang)}</Badge>}
        {live === 'stale' && <Badge tone="warning" title={t('entityD.staleTitle', lang)}>{t('entityD.stale', lang)}</Badge>}
        {live === 'gone' && <Badge tone="danger">{t('entityD.gone', lang)}</Badge>}
        {logCov?.hasLogs === true && <Badge title={tf('entityD.logsYesTitle', lang, { window: logCov.window, backend: logCov.backend ?? 'log', at: fmtDateTime(new Date(logCov.checkedAt)) })}>{t('entityD.logsYes', lang)}</Badge>}
        {logCov?.hasLogs === false && <Badge tone="neutral" title={tf('entityD.logsNoTitle', lang, { window: logCov.window, at: fmtDateTime(new Date(logCov.checkedAt)) })}>{t('entityD.logsNo', lang)}</Badge>}
      </Row>
      <Row gap={2} wrap>
        <span className="field-hint">
          {live === 'gone'
            ? tf('entityD.lifeGone', lang, { seen: fmtDateTime(new Date(entity.lastSeen)), from: fmtDateTime(new Date(entity.validFrom)), to: entity.validTo ? fmtDateTime(new Date(entity.validTo)) : '—' })
            : tf('entityD.lifeLive', lang, { from: fmtDateTime(new Date(entity.validFrom)), seen: fmtDateTime(new Date(entity.lastSeen)) })}
          {' · '}{tf('entityD.source', lang, { source: entity.source })}{entity.uid ? ` · uid ${entity.uid}` : ''}
        </span>
        {!!at && atMatch === false && (
          <Badge tone="warning" title={tf('entityD.atTitle', lang, { at: fmtDateTime(new Date(at)) })}>{t('entityD.atNoMatch', lang)}</Badge>
        )}
      </Row>
      {/* v0.10.139 (adım 5) — node / namespace: kapasite + kullanım (Thanos),
          CPU/Mem trend, giriş-span latency özeti, etki (pod/servis sayısı). */}
      {entity.type === 'node' && cluster && <NodePanel entity={entity} clusterName={cluster.name} from={from} to={to} svc={svc} />}
      {entity.type === 'namespace' && cluster && <NamespacePanel entity={entity} clusterName={cluster.name} from={from} to={to} svc={svc} />}
      {kids.length > 0 && (
        <Row gap={2} wrap>
          <span className="field-hint">{t('entityD.children', lang)}</span>
          {kids.map(([t, n]) => <Badge key={t} tone="neutral">{t} × {n}</Badge>)}
        </Row>
      )}
      {labels.length > 0 && (
        <Row gap={2} wrap>
          <span className="field-hint">{t('entityD.labels', lang)}</span>
          {(showLabels ? labels : labels.slice(0, 12)).map(([k, v]) => <Badge key={k} tone="neutral" title={`${k}=${v}`}>{k}={v}</Badge>)}
          {labels.length > 12 && (
            <Button variant="secondary" size="xs" onClick={() => setShowLabels(v => !v)}>{showLabels ? t('entityD.less', lang) : `+${labels.length - 12}`}</Button>
          )}
        </Row>
      )}
      <h3>{tf('entityD.servicesUnder', lang, { type: entity.type })}</h3>
      {svcError && svc && svc.services.length > 0 && <Empty icon="!" title={t('entityD.svcLoadFail', lang)} compact>{svcError}</Empty>}
      {/* v0.10.947 — .table-wrap: DataTableColgroup kabı bulamazsa fitColumnWidths
          çalışmaz; fixed düzende flex Service kolonu sıfıra inebilir. */}
      <div className="table-wrap">
        <table {...svcDt.tableProps}>
          <DataTableColgroup dt={svcDt} />
          <DataTableHead dt={svcDt} />
          <tbody>
            {/* v0.10.857 (scale-audit) — cluster entity'sinde penceredeki her servis gelir: >100 satırda content-visibility. */}
            {!svc || svcDt.sortedRows.length === 0 ? <DataTableState dt={svcDt} {...svcState} /> : svcDt.sortedRows.map(s => (
              <tr key={s.service} className={svc.services.length > 100 ? 'cv-row' : undefined}>
                <DataTableCell dt={svcDt} col="service" row={s} title={s.service}>
                  <EntityLink to={{ kind: 'service', name: s.service, opts: { range: pageRange } }} className="sec">{s.service}</EntityLink>
                </DataTableCell>
                <DataTableCell dt={svcDt} col="pods" row={s} value={s.pods} />
                <DataTableCell dt={svcDt} col="spans" row={s} value={s.spans} />
                <DataTableCell dt={svcDt} col="errors" row={s} value={s.errors} />
                <DataTableCell dt={svcDt} col="avgMs" row={s} value={fixed(s.avgMs, 1, lang)} />
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {/* v0.10.967 — dtNoState: durum satırı yok — pod × servis bölümü yalnız satır
          varken çizilir (kendini gizleme, tarif §2); aynı okumanın yükleniyor /
          hata durumu yukarıdaki servis tablosunun içinde. */}
      {rows.length > 0 && (
        <>
          <h3>{tf('entityD.podsXServices', lang, { n: rows.length })}</h3>
          {/* v0.10.190 — namespace'siz span satırları pod adıyla eşlendi; ilan */}
          {(svc?.nsMissingRows ?? 0) > 0 && <div className="field-hint">{tf('entityD.nsMissing', lang, { n: svc!.nsMissingRows ?? 0 })}</div>}
          <div className="table-wrap">
            <table {...podDt.tableProps}>
              <DataTableColgroup dt={podDt} />
              <DataTableHead dt={podDt} />
              <tbody>
                {/* v0.10.947 — ilk 200 satır SIRALANMIŞ listeden: sıralama tüm satırlara uygulanır, kırpma sonra. */}
                {podDt.sortedRows.slice(0, 200).map(r => (
                  <tr key={`${r.namespace}/${r.pod}/${r.service}`} className={rows.length > 100 ? 'cv-row' : undefined}>
                    <DataTableCell dt={podDt} col="pod" row={r}>
                      {r.namespace ? (
                        <EntityLink to={{ kind: 'record', rec: { type: 'pod', id: `pod:${entity.clusterId}/${r.namespace}/${r.pod}`, name: r.pod, namespace: r.namespace, clusterId: entity.clusterId }, opts: hrefOpts }} className="sec"
                          title={`${cluster?.name ?? entity.clusterId} / ${r.namespace} / ${r.pod}`}>{r.pod}</EntityLink>
                      ) : <span className="mono" title={t('entityD.noNsTitle', lang)}>{r.pod}</span>}
                    </DataTableCell>
                    <DataTableCell dt={podDt} col="namespace" row={r} value={r.namespace} />
                    <DataTableCell dt={podDt} col="service" row={r} title={r.service}>
                      <EntityLink to={{ kind: 'service', name: r.service, opts: { range: pageRange } }} className="sec">{r.service}</EntityLink>
                    </DataTableCell>
                    <DataTableCell dt={podDt} col="spans" row={r} value={r.spans} />
                    <DataTableCell dt={podDt} col="errors" row={r} value={r.errors} />
                    <DataTableCell dt={podDt} col="avgMs" row={r} value={fixed(r.avgMs, 1, lang)} />
                    <DataTableCell dt={podDt} col="lastSeen" row={r} value={fmtDateTime(new Date(r.lastSeen))} className="mono" />
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {rows.length > 200 && <span className="field-hint">{t('entityD.first200', lang)}</span>}
        </>
      )}
      {lifetimes.length > 1 && (
        <>
          <h3>{tf('entityD.lifetimes', lang, { n: lifetimes.length })}</h3>
          {/* v0.10.947 — statik tablo (T1): aynı entity'nin ömürleri, sunucu ≤ 50 (pratikte 1–3), sunucu sırası anlamlı; PodLifetimesTable ile aynı. */}
          <table>
            {/* i18n-exempt: uid alan adı */}
            <thead><tr><th>{t('entityD.validFrom', lang)}</th><th>{t('entityD.validTo', lang)}</th><th>{t('podpage.lt.source', lang)}</th><th>uid</th></tr></thead>
            <tbody>
              {lifetimes.map(l => (
                <tr key={`${l.validFrom}|${l.uid ?? ''}`}>
                  <td className="mono">{fmtDateTime(new Date(l.validFrom))}</td>
                  <td className="mono">{l.validTo ? fmtDateTime(new Date(l.validTo)) : t('entityD.open', lang)}</td>
                  <td>{l.source}</td>
                  <td className="mono">{l.uid ?? '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </Stack>
  );
}

// Etki + latency KPI şeridi: pod/servis sayısı entity_seen satırlarından,
// p50/p95/p99 + hata ham spans'ten (/api/entity/latency).
function ImpactStrip({ id, from, to, svc }: { id: string; from: number; to: number; svc?: EntityServicesResponse }) {
  const lang = useLang();
  const latQ = useEntityLatency(id, { from, to });
  const l = latQ.data?.latency;
  const rows = svc?.rows ?? [];
  const spans = rows.reduce((a, r) => a + r.spans, 0);
  const errors = rows.reduce((a, r) => a + r.errors, 0);
  // Sunucu tavanları (pod ≤ 500, satır ≤ 2000): dolunca toplam KISMİ — ilan (inceleme).
  const capped = !!svc && (svc.pods.length >= 500 || rows.length >= 2000);
  const lat = latQ.data;
  const msLabel = (v?: number) => (v === undefined || v <= 0 ? '—' : duration(v, lang));
  return (
    <Row gap={3} wrap>
      <Stat label={t('entityD.stat.pods', lang)} value={svc ? `${svc.pods.length}${capped ? '+' : ''}` : '—'} sub={capped ? t('entityD.capPods', lang) : undefined} tone={capped ? 'warn' : undefined} />
      <Stat label={t('entityD.stat.services', lang)} value={svc ? String(svc.services.length) : '—'} />
      <Stat label={t('entityD.stat.spans', lang)} value={svc ? `${count(spans, lang)}${capped ? '+' : ''}` : '—'} sub={capped ? t('entityD.capRows', lang) : 'entity_seen_5m'} />
      <Stat label={t('entityD.stat.errPct', lang)} value={svc && spans ? percent(errors / spans, lang, { from: 'ratio' }) : '—'} tone={spans ? errorTone((100 * errors) / spans) : undefined} />
      <Stat label={tf('entityD.stat.entry', lang, { q: 'p50' })} value={latQ.isError ? t('entityD.err', lang) : msLabel(l?.p50Ms)} />
      <Stat label={tf('entityD.stat.entry', lang, { q: 'p95' })} value={latQ.isError ? t('entityD.err', lang) : msLabel(l?.p95Ms)} />
      <Stat label={tf('entityD.stat.entry', lang, { q: 'p99' })} value={latQ.isError ? t('entityD.err', lang) : msLabel(l?.p99Ms)} sub={l ? `${tf('entityD.entrySpans', lang, { n: count(l.entrySpans, lang) })}${lat?.clamped ? t('entityD.last24', lang) : ''}` : undefined} />
    </Row>
  );
}

function NodePanel({ entity, clusterName, from, to, svc }: { entity: EntityRecord; clusterName: string; from: number; to: number; svc?: EntityServicesResponse }) {
  const lang = useLang();
  const { cFrom, cTo, clamped } = useMemo(() => clampThanosWindow(from, to), [from, to]);
  const nodesQ = useQuery({
    queryKey: ['entity-node-rows', clusterName],
    queryFn: () => api.clusterNodes(clusterName),
    staleTime: 60_000, enabled: !!clusterName,
  });
  // Tek node sorgusu (node= parametresi, topk yok — inceleme: cluster-genel
  // topk(8) 9. node'u boş bırakıyordu). Seçici node-exporter instance host'u:
  // entity kaydının internal_ip etiketi (kube_node_info) ya da relabel'lı
  // kurulumlarda node adı.
  const ip = entity.labels?.internal_ip ?? '';
  const nodeSel = ip || entity.name;
  const cpuQ = useQuery({
    queryKey: ['entity-node-trend', clusterName, 'cpu', nodeSel, cFrom, cTo],
    queryFn: () => api.clusterResourceTrend(clusterName, 'cpu', true, cFrom, cTo, nodeSel),
    staleTime: 60_000, enabled: !!clusterName,
  });
  const memQ = useQuery({
    queryKey: ['entity-node-trend', clusterName, 'mem', nodeSel, cFrom, cTo],
    queryFn: () => api.clusterResourceTrend(clusterName, 'mem', true, cFrom, cTo, nodeSel),
    staleTime: 60_000, enabled: !!clusterName,
  });
  const row = nodesQ.data?.nodes?.find(n => n.node === entity.name);
  // Sunucu tek node'a kapsadı: dönen seri(ler) bu node'undur (ad instance host).
  const pick = (series: { name: string; points: { bucket: number; value: number }[] }[] | null | undefined) =>
    namedSeriesToSeries(series ?? [], entity.name);
  const cpuSeries = useMemo(() => pick(cpuQ.data?.series), [cpuQ.data, entity.name]); // eslint-disable-line react-hooks/exhaustive-deps
  const memSeries = useMemo(() => pick(memQ.data?.series), [memQ.data, entity.name]); // eslint-disable-line react-hooks/exhaustive-deps
  const xRange = useMemo(() => ({ from: cFrom / 1e9, to: cTo / 1e9 }), [cFrom, cTo]);
  return (
    <Stack gap={3}>
      <Row gap={3} wrap>
        <Stat label={t('entityD.stat.role', lang)} value={row?.role ?? '—'} />
        {/* i18n-exempt: CPU kısaltması */}
        <Stat label="CPU" value={row ? fmtCores(row.cpuCores) : nodesQ.isPending ? '…' : '—'} sub={row?.cpuPct != null ? tf('entityD.ofCapacity', lang, { pct: percent(row.cpuPct, lang, { from: 'pct', digits: 0 }) }) : undefined} tone={row?.cpuPct != null && row.cpuPct >= 90 ? 'err' : undefined} />
        <Stat label={t('entityD.stat.mem', lang)} value={row ? fmtBytes(row.memBytes) : nodesQ.isPending ? '…' : '—'} sub={row?.memPct != null ? tf('entityD.ofCapacity', lang, { pct: percent(row.memPct, lang, { from: 'pct', digits: 0 }) }) : undefined} tone={row?.memPct != null && row.memPct >= 90 ? 'err' : undefined} />
        {row?.netInBps != null && <Stat label={t('entityD.stat.netIn', lang)} value={`${fmtBytes(row.netInBps)}/s`} />}
        {row?.netOutBps != null && <Stat label={t('entityD.stat.netOut', lang)} value={`${fmtBytes(row.netOutBps)}/s`} />}
        {!row && !nodesQ.isPending && <span className="field-hint">{tf('entityD.noCapacity', lang, { err: nodesQ.isError ? t('entityD.queryErr', lang) : '' })}</span>}
        {clamped && <span className="field-hint">{tf('entityD.trendClamp', lang, { w: THANOS_MAX_WINDOW_LABEL })}</span>}
      </Row>
      <ImpactStrip id={entity.id} from={from} to={to} svc={svc} />
      {/* v0.10.187 (F4) — iki seri de yoksa üst üste iki boş blok yerine tek satır */}
      {!cpuQ.isPending && !memQ.isPending && cpuSeries.length === 0 && memSeries.length === 0 ? (
        <Empty icon="—" title={t('entityD.noCpuMem', lang)} compact>{cpuQ.isError || memQ.isError ? t('entityD.thanosErr', lang) : tf('entityD.noMatch', lang, { sel: nodeSel })}</Empty>
      ) : (
      <div className="ov-grid ov-charts-3 ov-mb">
        <div>
          <div className="field-hint">{t('entityD.cpuCores', lang)}</div>
          {cpuQ.isPending ? <Spinner /> : cpuSeries.length === 0
            ? <Empty icon="—" title={t('entityD.noCpu', lang)} compact>{cpuQ.isError ? t('entityD.thanosErr', lang) : tf('entityD.noMatch', lang, { sel: nodeSel })}</Empty>
            : <MultiLineChart series={cpuSeries} height={180} unit="cores" xRange={xRange} syncKey={`entity-node:${entity.id}`} />}
        </div>
        <div>
          <div className="field-hint">{t('entityD.memBytes', lang)}</div>
          {memQ.isPending ? <Spinner /> : memSeries.length === 0
            ? <Empty icon="—" title={t('entityD.noMem', lang)} compact>{memQ.isError ? t('entityD.thanosErr', lang) : tf('entityD.noMatch', lang, { sel: nodeSel })}</Empty>
            : <MultiLineChart series={memSeries} height={180} unit="bytes" xRange={xRange} syncKey={`entity-node:${entity.id}`} />}
        </div>
      </div>
      )}
    </Stack>
  );
}

function NamespacePanel({ entity, clusterName, from, to, svc }: { entity: EntityRecord; clusterName: string; from: number; to: number; svc?: EntityServicesResponse }) {
  const lang = useLang();
  const { cFrom, cTo, clamped } = useMemo(() => clampThanosWindow(from, to), [from, to]);
  const nsQ = useQuery({
    queryKey: ['entity-ns-rows', clusterName],
    queryFn: () => api.clusterNamespaces(clusterName),
    staleTime: 60_000, enabled: !!clusterName,
  });
  const row = nsQ.data?.namespaces?.find(n => n.namespace === entity.name);
  return (
    <Stack gap={3}>
      <Row gap={3} wrap>
        {/* v0.10.187 (F3) — satır yokken beş «—» kutusu yerine tek cümle */}
        {(row || nsQ.isPending) && (
          <>
            <Stat label={t('entityD.stat.podsK8s', lang)} value={row?.pods != null ? String(row.pods) : '…'} />
            {/* i18n-exempt: CPU kısaltması */}
            <Stat label="CPU" value={row ? fmtCores(row.cpuCores) : '…'} />
            <Stat label={t('entityD.stat.mem', lang)} value={row ? fmtBytes(row.memBytes) : '…'} />
            <Stat label={t('entityD.stat.restarts', lang)} value={row?.restarts != null ? String(row.restarts) : '…'} tone={row?.restarts ? 'warn' : undefined} />
            <Stat label={t('entityD.stat.failing', lang)} value={row?.failing != null ? String(row.failing) : '…'} tone={row?.failing ? 'err' : undefined} />
          </>
        )}
        {!row && !nsQ.isPending && <span className="field-hint">{tf('entityD.noNsSeries', lang, { err: nsQ.isError ? t('entityD.queryErr', lang) : '' })}</span>}
        {clamped && <span className="field-hint">{tf('entityD.trendClamp', lang, { w: THANOS_MAX_WINDOW_LABEL })}</span>}
      </Row>
      <ImpactStrip id={entity.id} from={from} to={to} svc={svc} />
      <ThanosTrendPanel cluster={clusterName} namespace={entity.name} fromNs={cFrom} toNs={cTo} />
    </Stack>
  );
}
