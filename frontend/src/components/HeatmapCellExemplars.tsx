import { useEffect, useRef, useState } from 'react';
import { useEscLayer } from '@/lib/escLayer';
import { Link } from 'react-router-dom';
import { Spinner } from './Spinner';
import { api } from '@/lib/api';
import { fmtSmart } from '@/lib/chartFmt';
import { fmtClock, tsLong } from '@/lib/utils';
import type { TraceRow, FilterExpr } from '@/lib/types';
import { traceHref } from '@/lib/traceHref';
import { IconButton } from '@/components/ui/IconButton';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, type ColumnDef } from '@/components/ui/DataTable';

// HeatmapCellExemplars — v0.5.260. Honeycomb-classic "click the
// slow band → see what traces ran there" workflow. Modal opens
// when the operator clicks a non-empty cell on LatencyHeatmap;
// shows up to 20 traces matching the cell's (time bucket,
// latency band) under the same filter the heatmap was rendered
// with, so the result is exactly the cohort the operator wanted
// to drill into.
//
// Why a modal vs. inline expansion: the heatmap is dense (60×28
// cells); inline would push the rest of the page around with
// every click. Modal stays out of the way + is closable with
// Esc / backdrop click.

export interface HeatmapCellRef {
  timeNs:    number;  // bucket centre time
  lowDurMs:  number;  // duration band lower bound (exclusive)
  highDurMs: number;  // duration band upper bound (inclusive)
  count:     number;  // for the modal header
  // v0.9.393 — hücrenin sunucu-tarafı temsilci trace'i (opsiyonel).
  exemplarTraceId?: string;
}

interface Props {
  cell: HeatmapCellRef;
  // Half-width of one heatmap time bucket in ns. The cell's
  // [time-half, time+half] window is the trace lookup range.
  bucketWidthNs: number;
  // Same filter set the heatmap was rendered with — applied to
  // the trace lookup so the modal stays consistent with the
  // surface the operator clicked through from.
  filters: FilterExpr[];
  // Optional advanced-DSL string when the operator is in DSL
  // mode. Passed through verbatim; backend AND-joins with
  // the FilterExpr[].
  dsl?: string;
  onClose: () => void;
}

// v0.10.947 — tablo standardı T1: hücrenin trace'leri kayıt listesi → useDataTable,
// ama YALNIZ genişlik (hiçbir kolonda sortValue yok): sırayı sunucu belirliyor
// (en çok 20, süreye göre azalan) ve üstteki metin bunu vaat ediyor. Başlık
// sıralaması `s_heatmap-cell-exemplars`i URL'e yazardı; /explore'da bu
// ExploreInner'ı remount edip modalı kapatırdı. "!" hata işareti başlıksız trailing hücre.
const EXEMPLAR_COLS: ColumnDef<TraceRow>[] = [
  { id: 'trace',    label: 'Trace',    mono: true, width: 150 },
  { id: 'service',  label: 'Service',  flex: true },
  { id: 'duration', label: 'Duration', numeric: true, width: 84 },
  { id: 'spans',    label: 'Spans',    numeric: true, width: 70 },
  { id: 'started',  label: 'Started',  width: 168, tone: () => 'faint' },
];

export function HeatmapCellExemplars({ cell, bucketWidthNs, filters, dsl, exemplarTraceId, onClose }: Props & {
  // v0.9.393 — hücrenin SUNUCU-tarafı temsilci trace'i (heatmap yanıtındaki
  // exemplars ızgarasından). Arama sampled pencerede "No traces matched"
  // ölü ucuna düşse bile bu link her zaman GERÇEK bir trace'e gider.
  exemplarTraceId?: string;
}) {
  const [traces, setTraces] = useState<TraceRow[] | null | undefined>(undefined);
  // sortIds: [] — bayat bir `?s_heatmap-cell-exemplars=` / kayıtlı sıralama
  // hiçbir başlığı `sorted` göstermesin (sıralanabilir kolon yok).
  const dt = useDataTable<TraceRow>({ storageKey: 'heatmap-cell-exemplars', columns: EXEMPLAR_COLS, rows: traces ?? [], sortIds: [] });
  // v0.10.947 — kolon tutamağı diyalog dışında bırakılınca click ortak ataya
  // (arka plan) düşer; kapanma yalnız basış da arka planda başladıysa. Capture
  // şart: startResize pointerdown'ı stopPropagation'la kesiyor.
  const pressOnBackdrop = useRef(false);

  // Esc closes the modal — standard chrome on every modal in this app.
  // v0.9.950 (E2/Ö28) — KATMAN: modalın ALTINDAKİ çekmece/panel aynı
  // Esc'le kapanmasın.
  useEscLayer(true, onClose);

  useEffect(() => {
    const half = Math.max(bucketWidthNs / 2, 30 * 1e9); // 30s floor
    const from = cell.timeNs - half;
    const to = cell.timeNs + half;
    setTraces(undefined);
    api.traces({
      from, to,
      // The heatmap bins are upper-bound; the cell's actual
      // duration window is (low, high]. minMs is inclusive on the
      // backend, so we add 0.001 to the low bound to keep the
      // semantics tight.
      minMs: cell.lowDurMs > 0 ? cell.lowDurMs + 0.001 : 0,
      maxMs: cell.highDurMs,
      filters: filters.length > 0 ? JSON.stringify(filters) : undefined,
      dsl: dsl,
      sort: 'duration',
      order: 'desc',
      limit: 20,
    })
      .then(r => setTraces(r.traces ?? []))
      .catch(() => setTraces(null));
  }, [cell.timeNs, cell.lowDurMs, cell.highDurMs, bucketWidthNs, filters, dsl]);

  return (
    <div
      onPointerDownCapture={e => { pressOnBackdrop.current = e.target === e.currentTarget; }}
      onClick={e => { if (pressOnBackdrop.current && e.target === e.currentTarget) onClose(); }}
      style={{
        position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.55)',
        display: 'grid', placeItems: 'center', zIndex: 'var(--z-modal)',
      }}>
      {/* v0.9.988 (D6.2) — genişlik artık görüntü alanına KELEPÇELİ.
          Sabit 720px telefonda (390px) diyaloğun üçte ikisini ekran
          dışında bırakıyordu ve `place-items:center` onu iki yandan
          eşit taşırdığı için kapatma affordance'ı da görünmüyordu.
          `min()` masaüstünde bit bit aynı (720 < 100vw-32), yalnız dar
          ekranda devreye giriyor — `.modal-dialog`ın CSS'te zaten
          yaptığının satır içi karşılığı. */}
      <div onClick={e => e.stopPropagation()} style={{
        width: 'min(720px, calc(100vw - 32px))', maxHeight: '80vh', overflow: 'auto',
        padding: 20, borderRadius: 8,
        background: 'var(--bg2)', border: '1px solid var(--border)',
      }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 12, marginBottom: 4 }}>
          <span style={{ fontSize: 15, fontWeight: 700 }}>
            Trace exemplars
          </span>
          <span style={{ fontSize: 12, color: 'var(--text3)', flex: 1 }}>
            {fmtSmart(cell.lowDurMs, 'ms')} – {fmtSmart(cell.highDurMs, 'ms')} ·{' '}
            {fmtClock(cell.timeNs / 1e6)} ·{' '}
            {cell.count.toLocaleString()} spans in cell
          </span>
          <IconButton variant="secondary" size="sm" aria-label="Close exemplars"
            tooltip="Close" onClick={onClose} icon="✕" />
        </div>
        <div style={{ fontSize: 11, color: 'var(--text3)', marginBottom: 14 }}>
          {/* v0.9.869 (tutarlılık denetimi MT3 sınıfı, YENİ bulgu) — eski
              metin "Click a row to open the waterfall" diyordu. Satırda
              onClick YOK; yalnız trace-id hücresi bir <Link>. Satır gövdesine
              tıklayan operatör hiçbir şey görmüyor ve modal'ın bozuk
              olduğunu sanıyor. MT3'le aynı karar: vaat düzeltilir, tık
              bağlanmaz. */}
          Top 20 traces ordered by duration desc, applying the heatmap's
          current filter set. Click a trace id to open its waterfall.
        </div>

        {traces === undefined && <Spinner />}
        {traces === null && (
          <div style={{ color: 'var(--err)', fontSize: 12 }}>
            Failed to load traces — try widening filters or check the time range.
          </div>
        )}
        {traces && traces.length === 0 && (
          <div style={{ color: 'var(--text3)', fontSize: 12, padding: '12px 0' }}>
            No traces matched. The heatmap's count reflects raw spans; this lookup
            joins by trace id. If the heatmap is sampled, exemplars may be missing
            (the cell still represents real spans, just not all reachable as
            trace rows here).
            {exemplarTraceId && (
              <div style={{ marginTop: 8 }}>
                {/* v0.9.393 — ölü uç kapandı: hücrenin sunucu-tarafı temsilci
                    trace'i aramadan bağımsız her zaman elimizde. */}
                Yine de hücrenin temsilci trace&#39;i elimizde:{' '}
                <Link className="mono" to={traceHref(exemplarTraceId)}
                  >
                  ◆ {exemplarTraceId.slice(0, 16)}… (hücredeki en yavaş)
                </Link>
              </div>
            )}
          </div>
        )}
        {traces && traces.length > 0 && (
          <>
          {exemplarTraceId && (
            <div style={{ fontSize: 11, color: 'var(--text3)', marginBottom: 6 }}>
              ◆ temsilci (en yavaş):{' '}
              <Link className="mono" to={traceHref(exemplarTraceId)}>
                {exemplarTraceId.slice(0, 16)}…
              </Link>
            </div>
          )}
          {/* v0.10.947 — table.dt (fixed) düzeninde flex Service kolonu yalnız
              kalan alanı alır; dar diyalogda sabitlerin toplamı kabı aşınca 0'a
              çöküyordu. Taban genişlikle tablo taşar, diyaloğun overflow:auto'su
              yatay kaydırır; masaüstünde (~665px içerik) görünüm aynı. Trailing
              32: 12+12 hücre dolgusu 24'lük kolonda "!"i sıfıra kırpıyordu. */}
          <table {...dt.tableProps} style={{ minWidth: 640 }}>
            <DataTableColgroup dt={dt} trailing={[32]} />
            <DataTableHead dt={dt} trailing={<th />} />
            <tbody>
              {dt.sortedRows.map(t => (
                <tr key={t.traceId}>
                  <DataTableCell dt={dt} col="trace" row={t}>
                    <Link to={traceHref(t.traceId)}
                      onClick={onClose}
                      style={{ color: 'var(--accent2)', textDecoration: 'none' }}>
                      {t.traceId.slice(0, 16)}…
                    </Link>
                  </DataTableCell>
                  <DataTableCell dt={dt} col="service" row={t}
                    title={[t.serviceName, t.rootName].filter(Boolean).join(' / ') || undefined}>
                    {t.serviceName || <span className="cell-empty">—</span>}
                    {t.rootName && (
                      <span className="cell-faint" style={{ marginLeft: 6 }}>
                        / {t.rootName}
                      </span>
                    )}
                  </DataTableCell>
                  <DataTableCell dt={dt} col="duration" row={t} value={fmtSmart(t.durationMs ?? 0, 'ms')} />
                  <DataTableCell dt={dt} col="spans" row={t} value={(t.spanCount ?? 0).toLocaleString()} />
                  <DataTableCell dt={dt} col="started" row={t} value={tsLong(t.startTime)} className="mono" />
                  <td>
                    {t.hasError && (
                      <span title="root span errored"
                        style={{ color: 'var(--err)', fontWeight: 700 }}>!</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          </>
        )}
      </div>
    </div>
  );
}
