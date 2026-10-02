import { lazy, Suspense, useMemo } from 'react';
import { Link } from 'react-router-dom';
import { SectionUnavailable, StatTile } from '@/components/ui';
import { Spinner } from '@/components/Spinner';
import { LazyMount } from '@/components/LazyMount';
import { TrendDelta } from '@/components/TrendDelta';
import { msSyncKey } from '@/lib/chart/syncNamespace';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { fmtNum } from '@/lib/utils';
import type { TimeRange, DBStmtDetail, DBStmtCaller } from '@/lib/types';
import { stmtTrendGrid, stmtTrendSeries, stmtBucketLabel } from './stmtTrend';
import { serviceHref } from '@/lib/serviceHref';
import { repeatsExploreHref } from '@/lib/pivotHref';
import { traceHref } from '@/lib/traceHref';

// stmtDetailSections — the statement detail BODY (v0.9.1374).
//
// Extracted verbatim from StmtDetailDrawer, which is deleted in the same
// release. Same move, same reason as /database one page-conversion
// earlier (v0.9.840): a 620px drawer had to hold the statement text, a
// six-tile summary, three sparkline rows, a six-column caller table AND
// the exemplar pivots. Every one of those is a table or a chart, and a
// drawer is the one surface that cannot give any of them width.
//
// The sections themselves are unchanged — this is a MOVE, not a redesign.
// v0.10.1058 — tek istisna Trend: Sparkline şeritleri standart zaman
// grafiğine (CorePanelMulti) geçti, gerekçe StmtTrendSection başında.

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <div style={{
      fontSize: 12, fontWeight: 700, color: 'var(--text2)',
      margin: '16px 0 8px',
    }}>{children}</div>
  );
}

/** Wall-clock total in the largest unit that keeps it readable. */
function fmtTotal(totalMs: number): string {
  const sec = totalMs / 1000;
  if (sec >= 60) return `${(sec / 60).toFixed(1)} min`;
  if (sec >= 1) return `${sec.toFixed(1)} s`;
  return `${totalMs.toFixed(0)} ms`;
}

/** Normalized statement text + the real literal-bearing sample. */
export function StmtText({ statement, sample }: { statement: string; sample: string }) {
  return (
    <>
      {statement ? (
        <pre style={{
          margin: '0 0 10px',
          whiteSpace: 'pre-wrap', wordBreak: 'break-word',
          color: 'var(--text)', maxHeight: 260, overflowY: 'auto',
          padding: 12, background: 'var(--bg1)',
          border: '1px solid var(--border)', borderRadius: 6,
        }}>{statement}</pre>
      ) : (
        <div style={{ fontSize: 11, color: 'var(--text3)', marginBottom: 10 }}>
          Statement text unavailable — no data for this class in the window.
        </div>
      )}
      {sample && (
        <details style={{ marginBottom: 14 }}>
          <summary style={{ fontSize: 10, color: 'var(--text3)', cursor: 'pointer',
            textTransform: 'uppercase', letterSpacing: 0.5 }}>
            Real sample (literals shown)
          </summary>
          <pre style={{
            margin: '6px 0 0', fontSize: 11,
            whiteSpace: 'pre-wrap', wordBreak: 'break-word',
            color: 'var(--text2)', maxHeight: 200, overflowY: 'auto',
            padding: 8, background: 'var(--bg2)', borderRadius: 4,
          }}>{sample}</pre>
        </details>
      )}
    </>
  );
}

// StmtSummarySection — window totals; deltas ride the payload's prior*
// fields when compare is on (kind conventions match Endpoints: calls
// neutral, latency/errors lowerBetter).
export function StmtSummarySection({ detail, compare }: {
  detail: DBStmtDetail; compare: boolean;
}) {
  const s = detail.summary;
  if (!s) return <SectionUnavailable what="Window summary" />;
  const errTone = s.calls > 0 && s.errors / s.calls >= 0.05 ? 'err'
    : s.errors > 0 ? 'warn' : undefined;
  return (
    <div style={{
      display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(100px, 1fr))',
      gap: 10,
    }}>
      <StatTile label="Calls">
        {fmtNum(s.calls)}
        {compare && <TrendDelta cur={s.calls} prior={s.priorCalls} kind="neutral" />}
      </StatTile>
      <StatTile label="Errors" tone={errTone}>
        {fmtNum(s.errors)}
        {compare && <TrendDelta cur={s.errors} prior={s.priorErrors} kind="lowerBetter" />}
      </StatTile>
      <StatTile label="Avg">
        {s.avgMs.toFixed(1)} ms
        {compare && <TrendDelta cur={s.avgMs} prior={s.priorAvgMs} kind="lowerBetter" />}
      </StatTile>
      <StatTile label="P95">
        {s.p95Ms.toFixed(0)} ms
        {compare && <TrendDelta cur={s.p95Ms} prior={s.priorP95Ms} kind="lowerBetter" />}
      </StatTile>
      <StatTile label="Max">
        {s.maxMs.toFixed(0)} ms
      </StatTile>
      <StatTile label="Total time">
        {fmtTotal(s.totalMs)}
      </StatTile>
    </div>
  );
}

// StmtTrendSection — v0.10.1058: standart zaman grafiği.
//
// Operatör (prod): "Statement detail grafikleri de çok kötü, Coremetry
// geneline uymuyor. Ayrıca zaman yok vs., hiç olmamış." Eski hâl üç
// Sparkline şeridiydi — zaman ekseni yok, değer ekseni yok, ipucu
// "bucket 17/37: 3.8k". Ürünün geri kalanı (Databases detay, servis
// Overview, Endpoint detay) RED serilerini CorePanel'le çiziyor; bu
// bölüm artık Databases detayın DatabaseTrendCards düzenini birebir
// kullanıyor: `ov-charts-3` ızgarası (dar ekranda alta kayar), LazyMount
// + lazy CorePanelMulti, 150 px, tek senkron grubu (crosshair üç
// grafikte birlikte). Sürükle-yakınlaştır sayfanın ?range='ine yazar
// (usePageZoomRange; çift tık bir adım geri — Endpoint detay kalıbı).
//
// "vs prior": uç önceki pencerenin KOVA serisini döndürmüyor (yalnız
// özet + çağıran toplamları, chstore.PriorWindow). Grafik bu yüzden
// yalnız bu pencereyi çizer, fark özet karolarında kalır — ve başlık
// bunu söyler. Hayalet seri (CorePanelMulti ghostItems) sunucuda ikinci
// bir trend okuması ister; bu sürümde yok.
const CorePanelMultiLazy = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

export function StmtTrendSection({ detail, compare = false, onZoom, onZoomReset }: {
  detail: DBStmtDetail;
  compare?: boolean;
  onZoom?: (fromSec: number, toSec: number) => void;
  onZoomReset?: () => void;
}) {
  const view = useMemo(() => {
    const g = stmtTrendGrid(detail.trend ?? [], detail.fromNs, detail.toNs,
      detail.trendBucketSec ?? 0);
    return g ? stmtTrendSeries(g, detail.toNs) : null;
  }, [detail]);
  if (!detail.trend) return (
    <div>
      <SectionTitle>Trend</SectionTitle>
      <SectionUnavailable what="Trend" />
    </div>
  );
  const bucket = stmtBucketLabel(detail.trendBucketSec ?? 0);
  const empty = view?.hasCalls ? undefined : 'Bu pencerede bu ifade için çağrı yok';
  const charts = [
    { key: 'calls', title: 'Calls / s', unit: 'reqps', role: 'data' as const,
      series: view?.callsPerSec ?? [] },
    { key: 'errors', title: bucket ? `Errors / ${bucket}` : 'Errors', unit: 'short',
      role: 'error' as const, series: view?.errors ?? [] },
    { key: 'p95', title: 'P95 latency', unit: 'ms', role: 'data' as const,
      series: view?.p95Ms ?? [] },
  ];
  return (
    <div>
      <SectionTitle>
        Trend
        {bucket && (
          <span style={{ fontWeight: 400, fontSize: 10, color: 'var(--text3)', marginLeft: 6 }}>
            {bucket} buckets
            {compare && ' · vs prior yalnız özet karolarında'}
          </span>
        )}
      </SectionTitle>
      <div className="ov-grid ov-charts-3 ov-mb">
        {charts.map(c => (
          <LazyMount key={c.key} minHeight={170}>
            <Suspense fallback={<div style={{ height: 170, display: 'grid', placeItems: 'center' }}><Spinner /></div>}>
              <CorePanelMultiLazy
                title={c.title}
                storageKey={`stmt-detail-${c.key}`}
                height={150}
                unit={c.unit}
                xRange={view?.xRange ?? null}
                // '-ms' = motor ad alanı (v0.9.789); tek grup, crosshair üçünde.
                syncKey={msSyncKey('statement-detail')}
                onZoom={onZoom} onZoomReset={onZoomReset}
                emptyReason={empty}
                items={[{ name: c.title, role: c.role, series: c.series }]} />
            </Suspense>
          </LazyMount>
        ))}
      </div>
    </div>
  );
}

// v0.10.943 — tablo standardı dilim 3: hücre görünümü kolon bayraklarında
// (numeric / tone); sayılar arayüz fontunda (S2).
const CALLER_COLS: ColumnDef<DBStmtCaller>[] = [
  { id: 'service', label: 'Service',    sortValue: r => r.service, naturalDir: 'asc', width: 170 },
  { id: 'calls',   label: 'Calls',      sortValue: r => r.calls,   numeric: true, width: 76 },
  { id: 'errors',  label: 'Errors',     sortValue: r => r.errors,  numeric: true, width: 66,
    tone: r => (r.errors > 0 ? 'err' : 'faint') },
  { id: 'avgMs',   label: 'Avg',        sortValue: r => r.avgMs,   numeric: true, width: 70 },
  { id: 'p95Ms',   label: 'P95',        sortValue: r => r.p95Ms,   numeric: true, width: 70 },
  { id: 'totalMs', label: 'Total time', sortValue: r => r.totalMs, numeric: true, width: 90 },
];

// StmtCallersSection — which services issue this statement class
// (service_name is a real dimension in db_statement_summary_5m, so this
// is a pure MV read). Top 20 by total wall-clock time.
export function StmtCallersSection({ detail, compare, range }: {
  detail: DBStmtDetail; compare: boolean;
  // v0.9.967 — the page's window. Its whole point is "vs prior"; sending
  // the operator to a service page on a DIFFERENT window undoes that.
  range: TimeRange;
}) {
  const rows = detail.callers ?? [];
  const dt = useDataTable<DBStmtCaller>({
    storageKey: 'dbstmt-callers',
    columns: CALLER_COLS,
    rows,
    initialSort: { id: 'totalMs', dir: 'desc' },
  });
  // v0.10.954 — tablo standardı T12: bölüm notu tablonun İÇİNDE, başlık
  // durur. `callers: null` = bölüm okuması düştü (sunucu bölüm başına hata
  // toleransı; boş sonuç [] döner) → hata satırı; [] → boş.
  const tableState: Omit<DataTableStateProps<DBStmtCaller>, 'dt'> = !detail.callers
    ? { kind: 'error' }
    : { kind: 'empty', message: 'Bu pencerede bu ifadeyi çalıştıran servis yok' };
  return (
    <div>
      <SectionTitle>Callers</SectionTitle>
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {dt.sortedRows.length === 0 ? <DataTableState dt={dt} {...tableState} /> : dt.sortedRows.map(c => (
              <tr key={c.service}>
                <DataTableCell dt={dt} col="service" row={c}>
                  <Link to={serviceHref(c.service, { range })}
                    className="mono" style={{ fontSize: 11 }}>
                    {c.service}
                  </Link>
                </DataTableCell>
                <DataTableCell dt={dt} col="calls" row={c}>
                  {fmtNum(c.calls)}
                  {compare && <TrendDelta cur={c.calls} prior={c.priorCalls} kind="neutral" />}
                </DataTableCell>
                <DataTableCell dt={dt} col="errors" row={c} value={fmtNum(c.errors)} />
                <DataTableCell dt={dt} col="avgMs" row={c}>
                  {c.avgMs.toFixed(1)}
                  {compare && <TrendDelta cur={c.avgMs} prior={c.priorAvgMs} kind="lowerBetter" />}
                </DataTableCell>
                <DataTableCell dt={dt} col="p95Ms" row={c} value={c.p95Ms.toFixed(0)} />
                <DataTableCell dt={dt} col="totalMs" row={c} value={fmtTotal(c.totalMs)} className="cell-strong" />
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// StmtExemplarsSection — the TRUE trace pivots: slowest + worst-error span
// of THIS statement class (spans.db_stmt_hash = hash), not a LIKE-prefix
// approximation.
//
// v0.9.1277 — üçüncü pivot: "N+1 trace'leri →". İlk ikisi TEK bir
// trace'e götürür ("bu ifadenin en yavaş örneği"); bu ise DESENE
// götürür ("bu ifadeyi tek istek içinde defalarca çağıran trace'ler").
// Yavaş bir sorgunun asıl hikâyesi çoğu zaman ikincisi: 4ms'lik bir
// SELECT, istek başına 200 kez çağrıldığında 800ms'dir.
//
// Kapsam KASITLI olarak servis + db.system: normalize ifade metni
// span'lerdeki ham `db.statement` ile eşleşmez, gerekçe
// repeatsExploreHref'in başında.
export function StmtExemplarsSection({ detail, range }: {
  detail: DBStmtDetail; range: TimeRange;
}) {
  const ex = detail.exemplars;
  // Bu ifadeyi GERÇEKTEN çağıran servisler — MV'nin kendi boyutu, tahmin
  // değil. Yoksa link servis filtresi olmadan gider (dürüst: daha geniş
  // bir soru sorar, YANLIŞ bir soru değil).
  const callerServices = (detail.callers ?? []).map(c => c.service).filter(Boolean);
  const repeatsHref = repeatsExploreHref({
    window: range,
    services: callerServices,
    dbSystem: detail.summary?.dbSystem || undefined,
  });
  return (
    <div>
      <SectionTitle>Exemplar traces</SectionTitle>
      {!ex && (
        <div style={{ fontSize: 11, color: 'var(--text3)' }}>
          No exemplar spans for this statement in the window.
        </div>
      )}
      <div style={{ display: 'flex', gap: 16, fontSize: 12, flexWrap: 'wrap',
        marginTop: ex ? 0 : 8 }}>
        {ex?.slowTraceId && (
          <Link to={traceHref(ex.slowTraceId, { pageRange: range })}
            style={{ color: 'var(--accent2)' }}
            title={`Slowest span of this statement class in the window (trace ${ex.slowTraceId})`}>
            slowest →
          </Link>
        )}
        {ex?.errorTraceId && (
          <Link to={traceHref(ex.errorTraceId, { pageRange: range })}
            style={{ color: 'var(--err)' }}
            title={`Slowest ERRORED span of this statement class in the window (trace ${ex.errorTraceId})`}>
            worst error →
          </Link>
        )}
        <Link to={repeatsHref}
          style={{ color: 'var(--warn)' }}
          title={callerServices.length
            ? `Explore → Repeats: aynı istek içinde ≥5 kez DB çağıran trace'ler`
              + ` (kapsam: ${callerServices.slice(0, 4).join(', ')}`
              + `${callerServices.length > 4 ? ` +${callerServices.length - 4}` : ''}`
              + `, gruplama db.statement, aynı zaman penceresi).`
              + ` İfade metni FİLTREYE konmaz — buradaki SQL normalize, span'lerdeki ham.`
            : `Explore → Repeats: aynı istek içinde ≥5 kez DB çağıran trace'ler`
              + ` (bu pencerede çağıran servis listelenemedi, kapsam tüm servisler).`}>
          N+1 trace'leri →
        </Link>
      </div>
    </div>
  );
}
