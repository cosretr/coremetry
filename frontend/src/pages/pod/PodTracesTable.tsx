// PodTracesTable — v0.10.160 (A anatomisi §5 + B aşısı). /api/traces,
// k8s.pod.name = pod süzgeci (ExceptionPodsPanel sözleşmesi). Çipler:
// Hatalı · Tümü. Çip durumu URL'de (?spans=all, replace:true; errors yazılmaz).
// v0.10.2195 — «Yavaş» çipi RED bölümüyle kalktı (p95 kaynağı yok). Kapsam
// entity modelinden (podExact=false → servis geneli, pod süzgeci yok). URL'de
// çip yokken penceredeki hatalı trace sayısı sıfırsa varsayılan «Tümü».
// Sunucu sıralı (zaman desc) → sütunlar SIRALANMAZ, yalnız yeniden boyutlanır
// (server-paged tabloda client sort yasak, LOG_COLS/v0.7.54). Sayfa 50, «Daha
// fazla yükle» sayfa ekler (offset), toplam sayım kapalı (count=skip):
// «50 · daha fazla var», «50 / N» değil.
import { useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router';
import { useQueries, useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { Chip, Button, Badge } from '@/components/ui';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { fmtDateTime } from '@/lib/utils';
import { t, tf, useLang, type Lang } from '@/lib/i18n';
import { fmtDur } from '@/components/traces/shared';
import { traceHref } from '@/lib/traceHref';
import type { TraceRow } from '@/lib/types';
import { effectiveSpansMode, writeSpansParam, podTraceParams, POD_TRACE_PAGE, type PodTraceCtx, type SpansMode } from './podPage';

// v0.10.2246 — başlıklar arayüz dilinde (podpage.col.*).
const cols = (lang: Lang): ColumnDef<TraceRow>[] => [
  { id: 'time', label: t('podpage.col.time', lang), width: 176 }, // v0.10.191 — fmtDateTime 19 karakter mono; 150'de saniye kırpılıyordu (operatör, prod)
  { id: 'trace', label: 'Trace', width: 150 },
  { id: 'service', label: t('podpage.col.service', lang), width: 180 },
  { id: 'op', label: t('podpage.col.op', lang), width: 320 },
  // v0.10.943 (tablo standardı dilim 3) — hatalı trace'in süresi kolon tonu (T9).
  { id: 'dur', label: t('podpage.col.dur', lang), width: 90, numeric: true, tone: r => (r.hasError ? 'err' : undefined) },
  { id: 'spans', label: t('podpage.col.spans', lang), width: 70, numeric: true },
  { id: 'status', label: t('podpage.col.status', lang), width: 90 },
  // v0.10.943 — satır sonu "→" bağlantısı eylem kolonu (T8); id/width aynı.
  { id: 'links', label: t('podpage.col.actions', lang), kind: 'actions', width: 50 },
];

export function PodTracesTable({ ctx }: { ctx: Omit<PodTraceCtx, 'mode'> }) {
  const lang = useLang();
  const [sp, setSp] = useSearchParams();
  const rawSpans = sp.get('spans');
  // Hatalı ilk sayfa (asıl liste errors kipindeyken AYNI queryKey — ek istek yok).
  const errProbeParams = podTraceParams({ ...ctx, mode: 'errors' }, 0);
  const errProbe = useQuery({
    queryKey: ['pod-traces', errProbeParams],
    queryFn: ({ signal }) => api.traces(errProbeParams, signal),
    enabled: rawSpans == null && !!ctx.pod, staleTime: 30_000, retry: 1,
  });
  const mode = effectiveSpansMode(rawSpans, errProbe.data ? errProbe.data.traces.length : null);
  const setMode = (m: SpansMode) => setSp(prev => writeSpansParam(new URLSearchParams(prev), m), { replace: true });
  // Kapsam değişince birikim sıfırlanır (v0.7.81 kuralı). Render SIRASINDA
  // (inceleme #6): useEffect ile sıfırlamak, useQueries'in eski sayfa
  // sayısını yeni kapsamla bir kez daha abone etmesine — 2..N sayfa için
  // boşa /api/traces çağrısına — yol açıyordu.
  const scopeKey = `${ctx.pod}|${ctx.cluster}|${ctx.service}|${ctx.podExact}|${ctx.from}|${ctx.to}|${mode}`;
  const [pg, setPg] = useState({ key: scopeKey, pages: 1 });
  if (pg.key !== scopeKey) setPg({ key: scopeKey, pages: 1 });
  const pages = pg.key === scopeKey ? pg.pages : 1;
  const setPages = (f: (n: number) => number) => setPg(prev => ({ key: scopeKey, pages: f(prev.key === scopeKey ? prev.pages : 1) }));
  const full: PodTraceCtx = { ...ctx, mode };
  const qs = useQueries({
    queries: Array.from({ length: pages }, (_, i) => {
      const params = podTraceParams(full, i * POD_TRACE_PAGE);
      return {
        queryKey: ['pod-traces', params],
        queryFn: ({ signal }: { signal?: AbortSignal }) => api.traces(params, signal),
        enabled: !!ctx.pod && (rawSpans != null || !errProbe.isPending),
        staleTime: 30_000,
        retry: 1,
      };
    }),
  });
  const rows = qs.flatMap(q => q.data?.traces ?? []);
  // Son VERİLİ sayfa (inceleme #5): uçuştaki sayfa data=undefined iken
  // «hepsi bu» yazıp düğmeyi söküyordu.
  const lastData = [...qs].reverse().find(q => q.data)?.data;
  const hasMore = !!lastData?.hasMore;
  const loading = qs.some(q => q.isPending && q.fetchStatus !== 'idle');
  const error = qs.find(q => q.error)?.error;
  const mvGap = qs.some(q => q.data?.mvGap);
  const narrowed = qs.find(q => q.data?.narrowedFromNs)?.data?.narrowedFromNs;
  const columns = useMemo(() => cols(lang), [lang]);
  const dt = useDataTable<TraceRow>({ storageKey: 'pod-traces', columns, rows });
  // v0.10.954 — tablo standardı T12: yükleniyor / hata / boş tablonun
  // İÇİNDE, başlık durur; «p95 eşiği yok» kapısı dışarıda (sorgu atılmadı).
  // Zincirin sırası aynı: hata, yüklenmiş sayfalar olsa da satırları gizler
  // (showRows). Hatalı çipi sunucu süzgecidir → sıfır satır "eşleşme
  // yok"; eski "Tümü çipiyle süzgeci kaldır" önerisi "Filtreleri temizle".
  const showRows = !error && rows.length > 0;
  const tableState: Omit<DataTableStateProps<TraceRow>, 'dt'> =
    rows.length === 0 && loading ? { kind: 'loading' }
    : error ? { kind: 'error', message: tf('podpage.tr.loadErr', lang, { err: String(error) }) }
    : mode !== 'all' ? {
      kind: 'no-match',
      message: t(ctx.podExact ? 'podpage.tr.noErrPod' : 'podpage.tr.noErrSvc', lang),
      onClearFilters: () => setMode('all'),
    }
    : { kind: 'empty', message: t(ctx.podExact ? 'podpage.tr.emptyPod' : 'podpage.tr.emptySvc', lang) };
  return (
    <>
      <div className="pod-chips">
        <span className="field-hint">{ctx.podExact
          ? <>k8s.pod.name = <span className="mono">{ctx.pod}</span></>
          : <><Badge tone="warning" title={t('podpage.tr.wideTitle', lang)}>{t('podpage.tr.wide', lang)}</Badge> {t('podpage.tr.svc', lang)} <span className="mono">{ctx.service}</span></>}
          {ctx.cluster ? <> · cluster <span className="mono">{ctx.cluster}</span></> : t('podpage.tr.noCluster', lang)}{ctx.podExact && ctx.service ? <> · {t('podpage.tr.svc', lang)} <span className="mono">{ctx.service}</span></> : ''}</span>
        <Chip active={mode === 'errors'} tone="accent" onClick={() => setMode('errors')} title="hasError=true">{t('podpage.tr.errors', lang)}</Chip>
        <Chip active={mode === 'all'} onClick={() => setMode('all')}>{t('podpage.all', lang)}</Chip>
        {mvGap && <Badge tone="warning" title={t('podpage.tr.mvGapTitle', lang)}>{t('podpage.tr.mvGap', lang)}</Badge>}
        {narrowed && <Badge tone="warning" title={tf('podpage.tr.narrowedTitle', lang, { t: fmtDateTime(new Date(narrowed / 1e6)) })}>{t('podpage.tr.narrowed', lang)}</Badge>}
      </div>
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {!showRows ? <DataTableState dt={dt} {...tableState} /> : dt.sortedRows.map(r => (
              <tr key={r.traceId} className={rows.length > 100 ? 'cv-row' : undefined}>
                <td className="mono">{fmtDateTime(new Date(r.startTime / 1e6))}</td>
                <td className="mono"><Link to={traceHref(r.traceId)} className="sec" title={r.traceId}>{r.traceId.slice(0, 12)}…</Link></td>
                <td title={r.serviceName}>{r.serviceName}</td>
                <td title={r.rootName}>{r.rootName || '—'}</td>
                <DataTableCell dt={dt} col="dur" row={r} value={fmtDur(r.durationMs)} />
                <td className="num">{r.spanCount}</td>
                {/* v0.10.929 (K5) — sağlıklı satır görsel boş, kelime sr-only (Traces listesi emsali). */}
                <td>{r.hasError
                  ? <span className="badge b-err">error</span>
                  : <span className="sr-only">ok</span>}</td>
                <DataTableCell dt={dt} col="links" row={r}><Link to={traceHref(r.traceId)} className="sec" title={t('podpage.tr.open', lang)}>→</Link></DataTableCell>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="pod-cap" style={{ display: 'flex', gap: 12, alignItems: 'center', flexWrap: 'wrap' }}>
        <span>
          <code className="mono">GET /api/traces</code>{tf('podpage.tr.paged', lang, { n: POD_TRACE_PAGE })}{rows.length > 0 ? `${rows.length}${t(hasMore ? 'podpage.tr.more' : 'podpage.tr.allShown', lang)}` : ''}{t('podpage.tr.noCount', lang)}
        </span>
        {(hasMore || loading) && rows.length > 0 && <Button variant="secondary" size="xs" onClick={() => setPages(p => p + 1)} disabled={loading}>{t(loading ? 'podpage.loading' : 'podpage.loadMore', lang)}</Button>}
      </div>
    </>
  );
}
