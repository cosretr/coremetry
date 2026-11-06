// LogsTableToolbar — v0.10.1502 (/logs Kibana-Discover dilim 4; operatör
// onaylı taslak 2026-10-24).
//
// Tablonun hemen üstündeki TEK araç satırı: yerel daraltma kutusu (v0.9.294 —
// yalnız yüklenmiş satırlar, yeni sorgu YOK) · Columns ▾ · Görünüm ▾ · sıra ·
// dışa aktarma ⋯. Hepsi tabloya etki eder, o yüzden tablonun yanında; sayfa
// düzeyi eylemler sorgu çubuğunda kalır. Saf sunum: durum Logs.tsx'te.
//
// v0.10.1830 (Log Search Faz 1 dilim 1.7) — shadcn dilimi (ui/ importu ve
// satır içi stil yok). "⤶ sar" anahtarı Görünüm ▾ içindeki gövde satır
// yüksekliğine katıldı (Otomatik = sarılı; mockup görünüm 1): düğmenin
// tarayıcı tercihi (`logs.wrap`) URL'de `rh` yokken varsayılanı belirler.
import type { ReactNode, RefObject } from 'react';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { Toggle } from '@/components/shadcn/toggle';
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
  /** v0.10.1880 — "Yalnız kritik hatalar" (yüklenen örnekte; kaynak tükenmesi ya da FATAL). */
  critOnly?: boolean;
  /** Örnekte exception özeti var mı (yoksa düğme çizilmez — CH log yolu). */
  critAvailable?: boolean;
  onCritOnly?: (v: boolean) => void;
  /** v0.10.1882 — "Trace'e göre grupla" (URL `group=trace`; yüklenen örnek içinde). */
  groupByTrace?: boolean;
  onGroupByTrace?: (v: boolean) => void;
  columns: string[];
  fields: string[];
  onColumns: (next: string[]) => void;
  /** Görünüm ▾ (coremetry/logs/DisplayOptions) — Columns'ın sağında. */
  display: ReactNode;
  /** v0.10.1834 — akışlı yükleme göstergesi (coremetry/logs/LoadStatus);
   *  sağ grubun başında: çip genişliği değişse de düğmeler yerinde kalır. */
  status?: ReactNode;
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
        <Input
          ref={p.narrowRef}
          className="ld-narrow h-7"
          value={p.narrow}
          aria-label="Yüklenen satırlarda süz"
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
      {p.critAvailable && p.onCritOnly && (
        <Toggle variant="outline" size="sm" pressed={!!p.critOnly} onPressedChange={p.onCritOnly} className="px-3"
          data-testid="logs-crit-only"
          title="Yüklenen satırlardan yalnız kritik hataları göster: kaynak tükenmesi (bellek, bağlantı havuzu, disk…) ya da FATAL. Yeni sorgu atmaz.">
          Yalnız kritik hatalar
        </Toggle>
      )}
      {p.critOnly && p.loadedCount > 0 && (
        <span className={p.shownCount === 0 ? 'ld-tbar-note is-warn' : 'ld-tbar-note'} data-testid="logs-crit-note">
          yüklenen {p.loadedCount.toLocaleString('tr-TR')} satırın {p.shownCount.toLocaleString('tr-TR')}'i kritik
        </span>
      )}
      {!p.live && p.onGroupByTrace && p.loadedCount > 0 && (
        <Toggle variant="outline" size="sm" pressed={!!p.groupByTrace} onPressedChange={p.onGroupByTrace} className="px-3"
          data-testid="logs-group-trace"
          title="Yüklenen satırları trace'e göre grupla (yeni sorgu yok). Başlıkta süre, hata ve kök span — trace önizlemesinden.">
          Trace'e göre grupla
        </Toggle>
      )}
      {p.groupByTrace && p.loadedCount > 0 && (
        <span className="ld-tbar-note" data-testid="logs-group-note">yüklenen {p.loadedCount.toLocaleString('tr-TR')} satır içinde</span>
      )}
      <span className="ld-tbar-right">
        {p.status}
        <LogsColumnPicker columns={p.columns} fields={p.fields} onChange={p.onColumns} />
        {p.display}
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
