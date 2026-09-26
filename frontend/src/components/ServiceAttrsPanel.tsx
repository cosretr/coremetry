import { useEffect, useMemo, useState } from 'react';
import { api } from '@/lib/api';
import { QueryErrorInline } from '@/components/QueryError';
import { timeRangeToNs, fmtNum } from '@/lib/utils';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, type ColumnDef } from '@/components/ui/DataTable';
import type { ServiceAttrRow, TimeRange } from '@/lib/types';

// ServiceAttrsPanel — v0.5.381. Surfaces "what attrs is my
// SDK actually emitting" for this service so the operator
// doesn't have to open a single trace and squint at the
// attribute table. Lives on the Service detail Details tab
// next to the other infra/breakdown panels.
//
// Rows are grouped by scope (span vs resource) because the
// operator-facing meaning is different: resource attrs are
// process-stable (k8s.pod.name, service.namespace,
// service.instance.id) and useful as filter dimensions;
// span attrs are per-request (http.route, db.statement,
// rpc.method) and useful as group-by dimensions.

export function ServiceAttrsPanel({ service, range }: {
  service: string;
  range: TimeRange;
}) {
  const [rows, setRows] = useState<ServiceAttrRow[] | null | undefined>(undefined);
  const [filter, setFilter] = useState('');
  // v0.9.866 (tutarlılık denetimi MT1) — Retry kaynağı: bu panel elle fetch
  // ediyor, react-query refetch'i yok. v0.9.858'in nonce deseni.
  const [retryNonce, setRetryNonce] = useState(0);
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);

  useEffect(() => {
    if (!service) return;
    setRows(undefined);
    api.serviceAttrs(service, from, to, { top: 80, samples: 5 })
      .then(r => setRows(r?.attrs ?? []))
      .catch(() => setRows(null));
  }, [service, from, to, retryNonce]);

  if (rows === undefined) {
    return (
      <div style={{
        marginTop: 14, padding: 12, borderRadius: 8,
        background: 'var(--bg1)', border: '1px solid var(--border)',
        fontSize: 12, color: 'var(--text3)',
      }}>
        Loading attributes…
      </div>
    );
  }
  // v0.9.866 (tutarlılık denetimi MT1) — `rows === null` bu koşulun içindeydi:
  // /api/service-attrs 500'lediğinde panel TAMAMEN YOK OLUYORDU, yani "bu
  // servis attr basmıyor" diye okunuyordu. Attr envanteri SDK teşhisinin
  // girdisi olduğu için yanlış-boş burada doğrudan yanlış teşhise götürüyor.
  if (rows === null) {
    return (
      <div style={{ marginTop: 14 }}>
        <QueryErrorInline
          text="Attributes could not be loaded — this is a failed read, not an instrumentation gap."
          onRetry={() => setRetryNonce(n => n + 1)} />
      </div>
    );
  }
  if (rows.length === 0) {
    // Self-hide when nothing — empty panel adds visual noise
    // to services that haven't pushed enough spans yet.
    // (KORUNDU: boş dal hâlâ sessiz, yalnız hata dalı ayrıldı.)
    return null;
  }

  const filtered = filter.trim()
    ? rows.filter(r =>
        r.key.toLowerCase().includes(filter.trim().toLowerCase()) ||
        r.sampleValues.some(v => v.toLowerCase().includes(filter.trim().toLowerCase())))
    : rows;
  const spanAttrs = filtered.filter(r => r.scope === 'span');
  const resAttrs  = filtered.filter(r => r.scope === 'resource');

  return (
    <div style={{
      marginTop: 14, padding: 12, borderRadius: 8,
      background: 'var(--bg1)', border: '1px solid var(--border)',
    }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, marginBottom: 10, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 13, fontWeight: 700 }}>⌥ Attributes emitted</span>
        <span style={{ fontSize: 11, color: 'var(--text3)' }}>
          {rows.length} key{rows.length === 1 ? '' : 's'} sampled
          {filter.trim() ? ` · ${filtered.length} matching` : ''}
        </span>
        <input value={filter} onChange={e => setFilter(e.target.value)}
          placeholder="Filter by key or value…"
          style={{ marginLeft: 'auto', width: 220, padding: '4px 10px', fontSize: 12,
                   background: 'var(--bg)', color: 'var(--text)',
                   border: '1px solid var(--border)', borderRadius: 4 }} />
      </div>
      <AttrSection title="Resource attrs (stable per-process)" rows={resAttrs} storageKey="svc-attrs-resource" />
      <AttrSection title="Span attrs (per-request)" rows={spanAttrs} storageKey="svc-attrs-span" />
      <div style={{ marginTop: 8, fontSize: 11, color: 'var(--text3)' }}>
        Sampled across up to 5k recent spans. Sample values shown
        per key (max 5). Resource attrs make stable filter keys;
        span attrs work better as group-by dimensions.
      </div>
    </div>
  );
}

// Sortable + resizable columns (shared useDataTable primitive). Sample
// values omits sortValue → not sortable, but still column-resizable.
// v0.10.947 — hücre görünümü kolon bayraklarında (tablo standardı T5):
// anahtar mono, sayı arayüz fontunda (S2), ikincil hücreler 11px yerine renk (S3).
const ATTR_COLS: ColumnDef<ServiceAttrRow>[] = [
  { id: 'key',          label: 'Key',          sortValue: r => r.key,         naturalDir: 'asc', width: 280, mono: true },
  { id: 'occurrences',  label: 'Occurrences',  sortValue: r => r.occurrences, numeric: true,     width: 120, tone: () => 'muted' },
  { id: 'sampleValues', label: 'Sample values', tone: () => 'muted' },
];

function AttrSection({ title, rows, storageKey }: { title: string; rows: ServiceAttrRow[]; storageKey: string }) {
  const dt = useDataTable<ServiceAttrRow>({
    storageKey, columns: ATTR_COLS, rows,
    initialSort: { id: 'occurrences', dir: 'desc' },
  });
  if (rows.length === 0) return null;
  return (
    <div style={{ marginTop: 6 }}>
      <div style={{ fontSize: 11, color: 'var(--text2)', fontWeight: 600,
                    textTransform: 'uppercase', letterSpacing: 0.4, marginBottom: 4 }}>
        {title}
      </div>
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {dt.sortedRows.map(r => (
              <tr key={`${r.scope}:${r.key}`} className="cv-row">
                <DataTableCell dt={dt} col="key" row={r} value={r.key} />
                <DataTableCell dt={dt} col="occurrences" row={r} value={fmtNum(r.occurrences)} />
                <DataTableCell dt={dt} col="sampleValues" row={r}>
                  {r.sampleValues.length === 0 ? '—' : (
                    <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
                      {r.sampleValues.map((v, i) => (
                        <code key={i} style={{
                          background: 'var(--bg2)', padding: '1px 6px',
                          borderRadius: 3, fontSize: 11,
                          maxWidth: 280, overflow: 'hidden',
                          textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                          display: 'inline-block',
                        }} title={v}>{v}</code>
                      ))}
                    </div>
                  )}
                </DataTableCell>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
