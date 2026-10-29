// LogsTableToolbar — v0.10.1502 (/logs Kibana-Discover dilim 4; operatör
// onaylı taslak 2026-10-24).
//
// Tablonun hemen üstündeki TEK araç satırı: yerel daraltma kutusu (v0.9.294 —
// yalnız yüklenmiş satırlar, yeni sorgu YOK) · Columns ▾ · ⤶ sar · sıra ·
// dışa aktarma ⋯. Hepsi tabloya etki eder, o yüzden tablonun yanında; sayfa
// düzeyi eylemler sorgu çubuğunda kalır. Saf sunum: durum Logs.tsx'te.
import type { RefObject } from 'react';
import { Button } from '@/components/ui/Button';
import { LogsColumnPicker } from './LogsColumnPicker';
import { LogsExportMenu } from './LogsActionsMenus';

export interface LogsTableToolbarProps {
  /** Yüklenmiş satır sayısı (0 → daraltma kutusu çizilmez). */
  loadedCount: number;
  /** Daraltma sonrası görünen satır. */
  shownCount: number;
  narrow: string;
  onNarrow: (v: string) => void;
  narrowRef: RefObject<HTMLInputElement | null>;
  columns: string[];
  fields: string[];
  onColumns: (next: string[]) => void;
  wrap: boolean;
  onToggleWrap: () => void;
  live: boolean;
  asc: boolean;
  onToggleAsc: () => void;
  onExport: (kind: 'csv' | 'ndjson') => void;
}

export function LogsTableToolbar(p: LogsTableToolbarProps) {
  return (
    <div className="ld-tbar" role="toolbar" aria-label="Log tablosu">
      {/* v0.9.294 — narrow within results: it says LOADED in the placeholder
          because it filters the rows already in the page; it does not
          re-query. At 10B docs/day the cheapest query is the one you don't send. */}
      {p.loadedCount > 0 && (
        <input
          ref={p.narrowRef}
          className="ld-narrow"
          value={p.narrow}
          onChange={e => p.onNarrow(e.target.value)}
          placeholder={`Filter the ${p.loadedCount.toLocaleString()} loaded rows… (no new query)`}
          title="Filters the rows already loaded into this page — body, service, severity and trace id. It does NOT search the rest of the window; widen the query above for that." />
      )}
      {p.loadedCount > 0 && p.narrow.trim() && (
        <>
          <span className={p.shownCount === 0 ? 'ld-tbar-note is-warn' : 'ld-tbar-note'}>
            {p.shownCount.toLocaleString()} of {p.loadedCount.toLocaleString()} loaded rows match
          </span>
          <Button variant="ghost" size="sm" onClick={() => p.onNarrow('')}>clear</Button>
        </>
      )}
      <span className="ld-tbar-right">
        <LogsColumnPicker columns={p.columns} fields={p.fields} onChange={p.onColumns} />
        {/* v0.10.417 (B5) — satır sarma; v0.10.1318 (K3) aç/kapa = `active`. */}
        <Button variant="secondary" size="sm" active={p.wrap}
          onClick={p.onToggleWrap}
          title={p.wrap
            ? 'Mesajlar sarılı. Tıkla: tek satır + … (tam metin hücre başlığında).'
            : 'Mesajlar tek satır (…). Tıkla: sar — uzun satırlar tam görünür, satır yüksekliği değişir.'}>
          ⤶ sar
        </Button>
        {/* v0.9.295 — sort direction; hidden in live tail (the stream is by
            definition newest-arriving). Flipping returns to page one — the
            keyset cursor is tied to its direction. */}
        {!p.live && (
          <Button variant="secondary" size="sm" onClick={p.onToggleAsc}
            title={p.asc
              ? 'Showing oldest first. Click for newest first. Changing the direction returns you to the first page — the keyset cursor is tied to the order it was created in.'
              : 'Showing newest first. Click for oldest first — useful for reading an incident forwards from where it started. Returns you to the first page.'}>
            {p.asc ? '↑ oldest first' : '↓ newest first'}
          </Button>
        )}
        <LogsExportMenu exportCount={p.shownCount} onExport={p.onExport} />
      </span>
    </div>
  );
}
