// PodTracesTable — v0.10.160 (A anatomisi §5 + B aşısı). /api/traces,
// k8s.pod.name = pod süzgeci (ExceptionPodsPanel sözleşmesi). Çipler:
// Hatalı (VARSAYILAN — B'nin araştırma-önce aşısı) · Yavaş > p95 (eşik RED
// batch'inin p95 serisinden; gelmeden çip kapalı, sabit ms'e düşmez) · Tümü.
// Çip durumu URL'de (?spans=slow|all, replace:true; errors yazılmaz).
// Sunucu sıralı (zaman desc) → sütunlar SIRALANMAZ, yalnız yeniden boyutlanır
// (server-paged tabloda client sort yasak, LOG_COLS/v0.7.54). Sayfa 50, «Daha
// fazla yükle» sayfa ekler (offset), toplam sayım kapalı (count=skip):
// «50 · daha fazla var», «50 / N» değil.
import { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQueries } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { Chip, Button, Badge } from '@/components/ui';
import { Empty } from '@/components/Spinner';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { fmtDateTime } from '@/lib/utils';
import { fmtDur } from '@/components/traces/shared';
import { traceHref } from '@/lib/traceHref';
import type { TraceRow } from '@/lib/types';
import { parseSpansParam, writeSpansParam, podTraceParams, POD_TRACE_PAGE, type PodTraceCtx, type SpansMode } from './podPage';

const COLS: ColumnDef<TraceRow>[] = [
  { id: 'time', label: 'Zaman', width: 176 }, // v0.10.191 — fmtDateTime 19 karakter mono; 150'de saniye kırpılıyordu (operatör, prod)
  { id: 'trace', label: 'Trace', width: 150 },
  { id: 'service', label: 'Servis', width: 180 },
  { id: 'op', label: 'Operasyon', width: 320 },
  // v0.10.943 (tablo standardı dilim 3) — hatalı trace'in süresi kolon tonu (T9).
  { id: 'dur', label: 'Süre', width: 90, numeric: true, tone: r => (r.hasError ? 'err' : undefined) },
  { id: 'spans', label: 'Span', width: 70, numeric: true },
  { id: 'status', label: 'Durum', width: 90 },
  // v0.10.943 — satır sonu "→" bağlantısı eylem kolonu (T8); id/width aynı.
  { id: 'links', label: 'Eylemler', kind: 'actions', width: 50 },
];

export function PodTracesTable({ ctx, p95Ms }: {
  ctx: Omit<PodTraceCtx, 'mode' | 'p95Ms'>;
  /** «Yavaş» eşiği — RED batch'i gelene dek null (çip kapalı) */
  p95Ms: number | null;
}) {
  const [sp, setSp] = useSearchParams();
  const mode = parseSpansParam(sp.get('spans'));
  const setMode = (m: SpansMode) => setSp(prev => writeSpansParam(new URLSearchParams(prev), m), { replace: true });
  // Kapsam değişince birikim sıfırlanır (v0.7.81 kuralı). Render SIRASINDA
  // (inceleme #6): useEffect ile sıfırlamak, useQueries'in eski sayfa
  // sayısını yeni kapsamla bir kez daha abone etmesine — 2..N sayfa için
  // boşa /api/traces çağrısına — yol açıyordu.
  const scopeKey = `${ctx.pod}|${ctx.cluster}|${ctx.service}|${ctx.from}|${ctx.to}|${mode}`;
  const [pg, setPg] = useState({ key: scopeKey, pages: 1 });
  if (pg.key !== scopeKey) setPg({ key: scopeKey, pages: 1 });
  const pages = pg.key === scopeKey ? pg.pages : 1;
  const setPages = (f: (n: number) => number) => setPg(prev => ({ key: scopeKey, pages: f(prev.key === scopeKey ? prev.pages : 1) }));
  const full: PodTraceCtx = { ...ctx, mode, p95Ms };
  const qs = useQueries({
    queries: Array.from({ length: pages }, (_, i) => {
      const params = podTraceParams(full, i * POD_TRACE_PAGE);
      return {
        queryKey: ['pod-traces', params],
        queryFn: ({ signal }: { signal?: AbortSignal }) => api.traces(params!, signal),
        enabled: !!params && !!ctx.pod,
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
  const dt = useDataTable<TraceRow>({ storageKey: 'pod-traces', columns: COLS, rows });
  const slowOff = p95Ms === null;
  // v0.10.954 — tablo standardı T12: yükleniyor / hata / boş tablonun
  // İÇİNDE, başlık durur; «p95 eşiği yok» kapısı dışarıda (sorgu atılmadı).
  // Zincirin sırası aynı: hata, yüklenmiş sayfalar olsa da satırları gizler
  // (showRows). Hatalı / Yavaş çipi sunucu süzgecidir → sıfır satır "eşleşme
  // yok"; eski "Tümü çipiyle süzgeci kaldır" önerisi "Filtreleri temizle".
  const showRows = !error && rows.length > 0;
  const tableState: Omit<DataTableStateProps<TraceRow>, 'dt'> =
    rows.length === 0 && loading ? { kind: 'loading' }
    : error ? { kind: 'error', message: `Trace listesi yüklenemedi: ${String(error)}` }
    : mode !== 'all' ? {
      kind: 'no-match',
      message: mode === 'errors' ? 'Bu pencerede bu pod\'da hatalı trace yok' : 'p95 üstünde trace yok',
      onClearFilters: () => setMode('all'),
    }
    : { kind: 'empty', message: 'Bu pencerede bu pod\'a ait trace yok — span\'ler k8s.pod.name taşımıyor olabilir; Traces sayfasında host.name ile dene.' };
  return (
    <>
      <div className="pod-chips">
        <span className="field-hint">k8s.pod.name = <span className="mono">{ctx.pod}</span>{ctx.cluster ? <> · cluster <span className="mono">{ctx.cluster}</span></> : ' · cluster eşlenmemiş (ad çakışması olabilir)'}{ctx.service ? <> · servis <span className="mono">{ctx.service}</span></> : ''}</span>
        <Chip active={mode === 'errors'} tone="accent" onClick={() => setMode('errors')} title="hasError=true">Hatalı</Chip>
        <Chip active={mode === 'slow'} onClick={() => setMode('slow')} disabled={slowOff} title={slowOff ? 'p95 eşiği henüz yok (RED metrikleri gelmedi)' : `minMs = ${Math.round(p95Ms)} (pencere p95, rate-ağırlıklı)`}>
          Yavaş {slowOff ? '' : `> ${Math.round(p95Ms)} ms`}
        </Chip>
        <Chip active={mode === 'all'} onClick={() => setMode('all')}>Tümü</Chip>
        {mvGap && <Badge tone="warning" title="trace_summary_5m boş bir güne değiyor; liste ham span'lerden okundu (daha yavaş)">MV boşluğu</Badge>}
        {narrowed && <Badge tone="warning" title={`Sunucu pencereyi daralttı: ${fmtDateTime(new Date(narrowed / 1e6))} sonrası`}>pencere daraltıldı</Badge>}
      </div>
      {mode === 'slow' && slowOff ? (
        <Empty icon="—" title="p95 eşiği yok — sorgu atılmadı">RED metrikleri gelmeden «Yavaş» süzgeci çalışmaz (sabit bir ms'e düşmez). Hatalı ya da Tümü çipini seç.</Empty>
      ) : (
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
                  <DataTableCell dt={dt} col="links" row={r}><Link to={traceHref(r.traceId)} className="sec" title="Trace'i aç">→</Link></DataTableCell>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="pod-cap" style={{ display: 'flex', gap: 12, alignItems: 'center', flexWrap: 'wrap' }}>
        <span>
          <code className="mono">GET /api/traces</code> · sunucu sayfalı, {POD_TRACE_PAGE}/sayfa · {rows.length > 0 ? `${rows.length}${hasMore ? ' · daha fazla var' : ' · hepsi bu'}` : ''} · toplam sayım kapalı (count=skip)
        </span>
        {(hasMore || loading) && rows.length > 0 && <Button variant="secondary" size="xs" onClick={() => setPages(p => p + 1)} disabled={loading}>{loading ? 'yükleniyor…' : 'Daha fazla yükle'}</Button>}
      </div>
    </>
  );
}
