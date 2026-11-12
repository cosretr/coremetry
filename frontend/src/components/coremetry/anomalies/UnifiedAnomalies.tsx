// UnifiedAnomalies — /anomalies TEK liste (v0.10.2027; operatör: "Anomalies
// hâlâ karışık", "tekilleştirme çalışmadı"). Mockup anomaly-unified.html
// "Liste: tek sayfa": Başlangıç · Servis · Ne oldu · Sinyaller · Durum.
// Satır = olay (ML grubu + aynı servis/aynı zamandaki eski dedektör olayları,
// unifiedRows.ts). Süren olaylar üstte; bitenler kapalı "Bitenler (N)"
// bölümünde. Veri iki mevcut uçtan (ML olayları + eski olay geçmişi; ikisi de
// sunucu önbellekli) — yeni uç, yeni sorgu yok.
import { useMemo, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { DataTable } from '@/components/coremetry/DataTable';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { SignalKindBadge } from '@/components/coremetry/SignalKindBadge';
import { useAnomalyEvents, useMLEvents } from '@/lib/queries';
import { fmtClock, fmtDateTime } from '@/lib/utils';
import type { SignalKind } from '@/lib/signalKind';
import { groupStatusRank, groupStatusText } from './mlGroupRows';
import { isOpenRow, listHeadline, unifiedRows, type UnifiedRow } from './unifiedRows';

const fmtTs = (ns: number) => fmtDateTime(new Date(ns / 1e6));

// v0.10.2027 — sütun kimlikleri v0.10.2021 ML listesiyle aynı; fazla sütun yok.
export const UNIFIED_COLS: ColumnDef<UnifiedRow>[] = [
  { id: 'started', label: 'Başlangıç', width: 110, priority: 1, sortValue: r => r.start },
  { id: 'service', label: 'Servis', width: 260, minWidth: 160, priority: 1, sortValue: r => r.service, naturalDir: 'asc' },
  { id: 'headline', label: 'Ne oldu', flex: true, minWidth: 280, sortValue: r => listHeadline(r), naturalDir: 'asc' },
  { id: 'signals', label: 'Sinyaller', width: 190, minWidth: 120, priority: 2, sortValue: r => r.signals.length },
  { id: 'status', label: 'Durum', width: 170, minWidth: 130, priority: 1, sortValue: r => groupStatusRank(r.status) * 1e19 + r.start },
];

type SignalFilter = '' | Exclude<SignalKind, 'group'>;

/** detectorStatus: sayfanın en altındaki KAPALI "Teknik ayrıntı / Dedektör
 *  durumu" bölümünün içeriği (ML karnesi + eski dedektör kartları). */
export function UnifiedAnomalies({ signal = '', onSignal, detectorStatus }: {
  signal?: SignalFilter; onSignal?: (s: SignalFilter) => void; detectorStatus?: ReactNode;
}) {
  const ml = useMLEvents('');
  const legacy = useAnomalyEvents();
  const all = useMemo(() => unifiedRows(ml.data?.items ?? [], legacy.data?.items ?? []), [ml.data, legacy.data]);
  const rows = useMemo(() => (signal ? all.filter(r => r.signals.includes(signal)) : all), [all, signal]);
  const open = useMemo(() => rows.filter(isOpenRow), [rows]);
  const done = useMemo(() => rows.filter(r => !isOpenRow(r)), [rows]);
  const nowNs = Date.now() * 1e6;

  const renderCell = (r: UnifiedRow, col: ColumnDef<UnifiedRow>) => {
    switch (col.id) {
      case 'started': return <span className="font-mono" title={fmtTs(r.start)}>{fmtClock(new Date(r.start / 1e6)).slice(0, 5)}</span>;
      case 'service': return <span className="dt-trunc font-mono" title={r.service}>{r.service || '—'}</span>;
      case 'headline': { const h = listHeadline(r); return <Link to={r.href} className="dt-trunc" title={h}>{h}</Link>; }
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

  const loading = ml.isLoading || legacy.isLoading;
  const failed = ml.isError && legacy.isError;
  const state = loading ? { kind: 'loading' as const }
    : failed ? { kind: 'error' as const, message: 'Anomaliler okunamadı' }
      : { kind: 'empty' as const, message: 'Şu an süren anomali yok.' };
  const partial = (ml.isError ? 'ML olayları' : legacy.isError ? 'eski dedektör olayları' : '');

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
        <DataTable<UnifiedRow>
          storageKey="anomalies-unified" columns={UNIFIED_COLS} rows={open} state={state}
          getRowHref={r => r.href}
          sort={{ initial: { id: 'status', dir: 'desc' } }}
          getRowKey={r => r.key} renderCell={renderCell} ariaLabel="Süren anomaliler" />
      </div>
      {done.length > 0 && (
        <details data-testid="anomalies-done" className="rounded-md border border-border bg-card px-2.5 py-1.5">
          <summary className="cursor-pointer text-sm font-semibold">Bitenler ({done.length})</summary>
          <div className="mt-2">
            <DataTable<UnifiedRow>
              storageKey="anomalies-unified-done" columns={UNIFIED_COLS} rows={done} state={{ kind: 'empty', message: 'Biten anomali yok.' }}
              getRowHref={r => r.href}
              sort={{ initial: { id: 'started', dir: 'desc' } }}
              getRowKey={r => r.key} renderCell={renderCell} ariaLabel="Biten anomaliler" />
          </div>
        </details>
      )}
      {(ml.data?.truncated || legacy.data?.truncated) && <p className="m-0 text-xs text-muted-foreground">Liste kaynak tavanında kesildi (son 24 saat).</p>}
      {detectorStatus && (
        <details data-testid="anomalies-detector-status" className="mt-2 rounded-md border border-border bg-card px-2.5 py-1.5">
          <summary className="cursor-pointer text-sm font-semibold">Teknik ayrıntı / Dedektör durumu</summary>
          <div className="mt-2 flex flex-col gap-4">{detectorStatus}</div>
        </details>
      )}
    </div>
  );
}
