// UnifiedAnomalies — /anomalies TEK liste (v0.10.2027; operatör: "Anomalies
// hâlâ karışık", "tekilleştirme çalışmadı"). Mockup anomaly-unified.html
// "Liste: tek sayfa": Başlangıç · Servis · Ne oldu · Sinyaller · Durum.
// Satır = olay (ML grubu + aynı servis/aynı zamandaki eski dedektör olayları).
// Süren olaylar üstte; bitenler kapalı "Bitenler (N)" bölümünde.
//
// v0.10.2051 — birleştirme, sıralama ve sayfalama SUNUCUDA
// (/api/anomalies/unified, internal/ml/unified.go). İstemci birleştirmesi
// kaynak başına 200 satırda sessizce kesiyordu ve sayfalanmıyordu. Tablo
// sunucu sayfalı → yalnız boyutlandırma (client sort YOK; sıra sunucunun:
// süren önce sonra yeni başlayan, bitenlerde yeni başlayan önce). Sayfa
// URL'de (?page= / ?donePage=, sinyal değişince sıfırlanır).
//
// v0.10.2083 — başlık sıralaması geri (operatör 2026-11-13), SUNUCUDA: DataTable
// serverSort kipi (`sort.server` + `urlOnly`), sıra URL'de (?sort= süren,
// ?doneSort= bitenler; varsayılan yazılmaz), sorgu doğrudan URL'den
// (lib/unifiedAnomalySort.ts). Sıra değişince O tablonun sayfası başa döner.
// Sıralanan: Başlangıç, Servis, Sinyaller, Durum. "Ne oldu" cümlesi tarayıcıda
// kurulur → sunucu sıralayamaz, başlığı düz kalır (sortValue yok).
import { useMemo, type ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { DataTable } from '@/components/coremetry/DataTable';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { SignalKindBadge } from '@/components/coremetry/SignalKindBadge';
import { Pager } from '@/components/Pager';
import { useUnifiedAnomalies } from '@/lib/queries';
import { fmtClock, fmtDateTime } from '@/lib/utils';
import type { SignalKind } from '@/lib/signalKind';
import type { UnifiedAnomalyRow } from '@/lib/types';
import { groupStatusText } from './mlGroupRows';
import { useUrlPage } from '@/lib/useUrlPage';
import { listHeadline, rowHref } from './unifiedRows';
import {
  UNIFIED_SORT_DEFAULT, UNIFIED_SORT_IDS, UNIFIED_SORT_URL_KEY, parseUnifiedSort, unifiedSortApiParams,
} from '@/lib/unifiedAnomalySort';

const fmtTs = (ns: number) => fmtDateTime(new Date(ns / 1e6));

/** Sunucu sayfası boyu (uç tavanı 100). */
const UNIFIED_PAGE_SIZE = 50;

// v0.10.2027 — sütun kimlikleri v0.10.2021 ML listesiyle aynı; fazla sütun yok.
// v0.10.2083 — sortValue serverSort kipinde yalnız "sıralanabilir" işareti +
// naturalDir taşıyıcısı (hiç çağrılmaz); sıra sunucunun (SortUnified).
const UNIFIED_COLS: ColumnDef<UnifiedAnomalyRow>[] = [
  { id: 'started', label: 'Başlangıç', width: 110, priority: 1, sortValue: r => r.start, naturalDir: 'desc' },
  { id: 'service', label: 'Servis', width: 260, minWidth: 160, priority: 1, sortValue: r => r.service, naturalDir: 'asc' },
  { id: 'headline', label: 'Ne oldu', flex: true, minWidth: 280 },
  { id: 'signals', label: 'Sinyaller', width: 190, minWidth: 120, priority: 2, sortValue: r => r.signals.join(','), naturalDir: 'asc' },
  { id: 'status', label: 'Durum', width: 170, minWidth: 130, priority: 1, sortValue: r => r.status, naturalDir: 'desc' },
];

type SignalFilter = '' | Exclude<SignalKind, 'group'>;

/** detectorStatus: sayfanın en altındaki KAPALI "Teknik ayrıntı / Dedektör
 *  durumu" bölümünün içeriği (ML karnesi + eski dedektör kartları). */
export function UnifiedAnomalies({ signal = '', onSignal, detectorStatus }: {
  signal?: SignalFilter; onSignal?: (s: SignalFilter) => void; detectorStatus?: ReactNode;
}) {
  const [openPage, setOpenPage] = useUrlPage('page');
  const [donePage, setDonePage] = useUrlPage('donePage');
  const [sp] = useSearchParams();
  const openSort = parseUnifiedSort('open', sp.get(UNIFIED_SORT_URL_KEY.open));
  const doneSort = parseUnifiedSort('done', sp.get(UNIFIED_SORT_URL_KEY.done));

  const openQ = useUnifiedAnomalies({ section: 'open', signal, offset: openPage * UNIFIED_PAGE_SIZE, limit: UNIFIED_PAGE_SIZE, ...unifiedSortApiParams('open', openSort) });
  const doneTotal = openQ.data?.doneTotal ?? 0;
  const doneQ = useUnifiedAnomalies({ section: 'done', signal, offset: donePage * UNIFIED_PAGE_SIZE, limit: UNIFIED_PAGE_SIZE, ...unifiedSortApiParams('done', doneSort) }, doneTotal > 0);
  const open = useMemo(() => openQ.data?.items ?? [], [openQ.data]);
  const done = useMemo(() => doneQ.data?.items ?? [], [doneQ.data]);
  const nowNs = Date.now() * 1e6;

  const renderCell = (r: UnifiedAnomalyRow, col: ColumnDef<UnifiedAnomalyRow>) => {
    switch (col.id) {
      case 'started': return <span className="font-mono" title={fmtTs(r.start)}>{fmtClock(new Date(r.start / 1e6)).slice(0, 5)}</span>;
      case 'service': return <span className="dt-trunc font-mono" title={r.service}>{r.service || '—'}</span>;
      case 'headline': { const h = listHeadline(r); return <Link to={rowHref(r)} className="dt-trunc" title={h}>{h}</Link>; }
      case 'signals': return (
        <span className="inline-flex flex-wrap items-center gap-2">
          {r.signals.map(k => <SignalKindBadge key={k} kind={k} />)}
        </span>
      );
      case 'status': {
        const t = groupStatusText(r, nowNs);
        return r.status === 'running' ? <span className="text-[color:var(--warn)]">{t}</span> : <span className="text-muted-foreground">{t}</span>;
      }
      default: return null;
    }
  };

  const state = openQ.isLoading ? { kind: 'loading' as const }
    : openQ.isError ? { kind: 'error' as const, message: 'Anomaliler okunamadı' }
      : { kind: 'empty' as const, message: 'Şu an süren anomali yok.' };
  const partialSrc = openQ.data?.partial;
  const partial = partialSrc === 'ml' ? 'ML olayları' : partialSrc === 'legacy' ? 'eski dedektör olayları' : '';
  const doneState = doneQ.isError ? { kind: 'error' as const, message: 'Biten anomaliler okunamadı' }
    : doneQ.isLoading ? { kind: 'loading' as const } : { kind: 'empty' as const, message: 'Biten anomali yok.' };
  const openTotal = openQ.data?.total ?? 0;
  // Sıra değişince o tablonun sayfası başa (server.onSort yalnız başlık
  // eylemiyle koşar; geri/ileri ile gelen URL sayfayı silmez).
  const openSortOpts = {
    initial: UNIFIED_SORT_DEFAULT.open, ids: UNIFIED_SORT_IDS, urlKey: UNIFIED_SORT_URL_KEY.open, urlOnly: true,
    server: { page: openPage, pageSize: UNIFIED_PAGE_SIZE, hasMore: (openPage + 1) * UNIFIED_PAGE_SIZE < openTotal, onPage: setOpenPage, onSort: () => setOpenPage(0) },
  };
  const doneSortOpts = {
    initial: UNIFIED_SORT_DEFAULT.done, ids: UNIFIED_SORT_IDS, urlKey: UNIFIED_SORT_URL_KEY.done, urlOnly: true,
    server: { page: donePage, pageSize: UNIFIED_PAGE_SIZE, hasMore: (donePage + 1) * UNIFIED_PAGE_SIZE < doneTotal, onPage: setDonePage, onSort: () => setDonePage(0) },
  };

  return (
    <div data-testid="anomalies-unified" className="flex flex-col gap-3">
      {onSignal && (
        <SegmentedControl<SignalFilter> value={signal} onChange={onSignal} aria-label="Sinyal" size="sm"
          options={[{ value: '', label: 'Hepsi' }, { value: 'trace', label: 'Trace' }, { value: 'log', label: 'Log' }, { value: 'metric', label: 'Metrik' }]} />
      )}
      {partial && <p className="m-0 text-sm text-[color:var(--warn)]">{partial} okunamadı; liste eksik.</p>}
      {/* v0.10.2030 — sayfada DOĞRUDAN duran tablo, diğer liste sayfaları gibi
          .table-wrap yüzeyinde (Inbox emsali; coremetry DataTable çıplak <table>). */}
      <div className="table-wrap">
        <DataTable<UnifiedAnomalyRow>
          storageKey="anomalies-unified" columns={UNIFIED_COLS} rows={open} state={state} sort={openSortOpts}
          getRowHref={rowHref}
          getRowKey={r => r.key} renderCell={renderCell} ariaLabel="Süren anomaliler" />
      </div>
      {openTotal > UNIFIED_PAGE_SIZE && (
        <Pager mode="offset" count="exact" total={openTotal} page={openPage} pageSize={UNIFIED_PAGE_SIZE}
          onPage={setOpenPage} />
      )}
      {doneTotal > 0 && (
        <details data-testid="anomalies-done" className="rounded-md border border-border bg-card px-2.5 py-1.5">
          <summary className="cursor-pointer text-sm font-semibold">Bitenler ({doneTotal})</summary>
          <div className="mt-2">
            <DataTable<UnifiedAnomalyRow>
              storageKey="anomalies-unified-done" columns={UNIFIED_COLS} rows={done} state={doneState} sort={doneSortOpts}
              getRowHref={rowHref}
              getRowKey={r => r.key} renderCell={renderCell} ariaLabel="Biten anomaliler" />
            {doneTotal > UNIFIED_PAGE_SIZE && (
              <Pager mode="offset" count="exact" total={doneTotal} page={donePage} pageSize={UNIFIED_PAGE_SIZE}
                onPage={setDonePage} />
            )}
          </div>
        </details>
      )}
      {openQ.data?.truncated && <p className="m-0 text-xs text-muted-foreground">Liste kaynak tavanında kesildi (son 24 saat).</p>}
      {detectorStatus && (
        <details data-testid="anomalies-detector-status" className="mt-2 rounded-md border border-border bg-card px-2.5 py-1.5">
          <summary className="cursor-pointer text-sm font-semibold">Teknik ayrıntı / Dedektör durumu</summary>
          <div className="mt-2 flex flex-col gap-4">{detectorStatus}</div>
        </details>
      )}
    </div>
  );
}
