import { useMemo } from 'react';
import { rowActivation } from '@/lib/a11y'; // v0.10.455 (dış denetim D3 dilim 3)
import { Link, useNavigate } from 'react-router-dom';
import { readState } from '@/lib/readState';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableState, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { fmtNum, tsLong } from '@/lib/utils';
import type { RepeatedSpanRow } from '@/lib/types';
import { traceHref } from '@/lib/traceHref';
import { repeatCols } from './presets';

// RepeatsResult — the Explore "Repeats" result-mode table (N+1 /
// fan-out finder; the block that renders BELOW the query console).
//
// Phase-1 extraction (explore-v2): moved verbatim out of Explore.tsx.
// Adopts the shared sortable+resizable primitive (useDataTable) with
// the same storageKey ('explore-repeats') and the same initial sort
// (count desc — the backend returns heaviest-first, so this preserves
// that paint). The "Repeated shape" column label tracks the active
// split-by, so columns are memoised on groupBy.
// State ownership is unchanged:
//   • repeats / repeatMin / groupBy stay in the parent (Explore.tsx) —
//     they ride the fetch + URL-write effects, and the preset chips +
//     Min-repeats picker live in the console card.
// Zero behaviour diff vs the inline version.
export function RepeatsResult({
  repeats,
  repeatMin,
  groupBy,
  errorText,
  onRetry,
}: {
  repeats: RepeatedSpanRow[] | null | undefined;
  repeatMin: number;
  groupBy: string[];
  // v0.9.867 (tutarlılık denetimi MT1) — `repeats === null` (okuma hatası)
  // hiçbir render dalına girmiyordu: sorgu patladığında sonuç alanı bomboş
  // kalıyor, "N+1 yok, temiziz" diye okunuyordu.
  errorText?: string | null;
  onRetry?: () => void;
}) {
  const navigate = useNavigate();

  // Repeats (N+1 / fan-out) table. The "Repeated shape" column
  // label tracks the active split-by, so columns are memoised on
  // groupBy. Backend returns heaviest-first; initial sort = count
  // desc preserves that paint.
  const cols = useMemo(() => repeatCols(groupBy), [groupBy]);
  const repeatsDt = useDataTable<RepeatedSpanRow>({
    storageKey: 'explore-repeats',
    columns: cols,
    rows: repeats ?? [],
    initialSort: { id: 'count', dir: 'desc' },
    onOpen: r => navigate(traceHref(r.traceId)),
  });

  // v0.10.954 — tablo standardı T12: yükleniyor / hata / boş tablonun
  // İÇİNDE, başlık durur (readState sırası aynen). Hata satırı sunucu
  // metnini ve daraltma önerisini taşır, ↻ aynı onRetry'a bağlı; hata dalı
  // (MT1: "N+1 yok, temiziz" okunmasın) tablonun içinde yaşıyor.
  const rs = readState(repeats);
  const tableState: Omit<DataTableStateProps<RepeatedSpanRow>, 'dt'> =
    rs === 'loading' ? { kind: 'loading' }
    : rs === 'error' ? {
      kind: 'error',
      onRetry,
      message: errorText
        ? `Tekrarlanan span desenleri hesaplanamadı: ${errorText} — daha dar bir zaman aralığıyla yeniden dene.`
        : 'Tekrarlanan span desenleri hesaplanamadı — bu bir hata, temiz sonuç değil. Daha dar bir zaman aralığıyla yeniden dene.',
    }
    : {
      kind: 'empty',
      message: `Tekrarlanan span deseni bulunamadı — bu pencerede aynı (group-by) deseni ≥ ${repeatMin} kez tekrarlayan trace yok. Eşiği düşürmeyi ya da split-by'ı değiştirmeyi dene (ör. konuşkan RPC için name + peer.service, endpoint fan-out için http.route).`,
    };

  return (
    <>
      {repeats && repeats.length > 0 && (
        <div style={{ marginBottom: 6, fontSize: 12, color: 'var(--text2)' }}>
          {/* v0.9.469 (dürüstlük A17) — len==limit tavan işaretidir:
              "200 trace" tam sayım gibi okunmasın. */}
          {repeats.length >= 200
            ? `ilk ${repeats.length} trace (tavan doldu — daha fazlası olabilir), ≥ ${repeatMin} tekrar, en ağır üstte.`
            : `${repeats.length} trace${repeats.length === 1 ? '' : 's'} with ≥ ${repeatMin} repeats of the same span shape — heaviest at the top.`}
        </div>
      )}
      <div className="table-wrap">
        <table {...repeatsDt.tableProps}>
          <DataTableColgroup dt={repeatsDt} />
          <DataTableHead dt={repeatsDt} />
          <tbody>
            {repeatsDt.sortedRows.length === 0 ? <DataTableState dt={repeatsDt} {...tableState} /> : repeatsDt.sortedRows.map((r, i) => {
              const rp = repeatsDt.rowProps(i);
              return (
                <tr key={`${r.traceId}|${i}`} {...rp}
                    {...rowActivation(() => navigate(traceHref(r.traceId)))}
                    className={[rp.className, 'cv-row'].filter(Boolean).join(' ')}>
                  <td className="mono">
                    <Link to={traceHref(r.traceId)}
                          onClick={e => e.stopPropagation()}
                          style={{ fontSize: 11 }}>
                      {r.traceId.slice(0, 12)}…
                    </Link>
                  </td>
                  <td>
                    <span style={{ fontWeight: 600 }}>{r.service || '—'}</span>
                    {r.rootName && (
                      <span style={{ color: 'var(--text3)' }}> · {r.rootName}</span>
                    )}
                  </td>
                  <td className="mono cell-muted" title={(r.groupValues ?? []).join(' · ')}>
                    {(r.groupValues ?? []).filter(Boolean).join(' · ') ||
                      <span style={{ color: 'var(--text3)' }}>(empty)</span>}
                  </td>
                  {/* v0.10.945 — 700 ağırlığın sınıfı yok (.cell-strong 600): satır içi kalır. */}
                  <td className={`num ${r.count >= 50 ? 'cell-err' : r.count >= 20 ? 'cell-warn' : ''}`}
                    style={{ fontWeight: 700 }}>
                    {fmtNum(r.count)}
                  </td>
                  <td className="num">{r.totalDurationMs.toFixed(1)}ms</td>
                  <td className="mono cell-faint" title={tsLong(r.startedAt)}>
                    {tsLong(r.startedAt)}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}
