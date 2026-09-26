// ui/DataTable/types.ts — v0.10.246 DataTable dilim 1 (audit §9).
//
// ColumnDef, lib/dataTable.ts DataTableColumn'u AYNEN genişletir — saf
// çekirdek (sıralama, genişlik mühürleme, fit) değişmez; eklenenler
// kimlik / görünüm bilgileri. useDataTable seçenekleri ADDİTİF:
// hiçbiri verilmediğinde bugünkü 109 çağrı yeri bayt-bayt aynı davranır.
//
// v0.10.939 (tablo standardı dilim 2) — hücre GÖRÜNÜMÜ bayrakları (mono /
// tone / truncate / kind). Sayfa bunları sınıf yazmadan `dt.cellProps` ya
// da `<DataTableCell>` ile benimser; bayrağı olmayan kolon bugünkü gibi.

import type { DataTableColumn, SortState } from '@/lib/dataTable';
import type { ColumnModel } from '@/lib/columnModel';

export type { ColumnModel, ColumnSource, ColumnSpec } from '@/lib/columnModel';

/** v0.10.939 (tablo standardı T9/T5) — hücrenin metin tonu. `err`/`warn`
 *  yalnız SAPAN değerde (dolgu yok); `muted` (--text2) / `faint` (--text3)
 *  hiyerarşi rengi — satır içi küçük punto yerine (T5). */
export type CellTone = 'err' | 'warn' | 'muted' | 'faint';

/** v0.10.939 (tablo standardı T11) — hücre kırpma kipi. `end` (varsayılan)
 *  sonu "…"; `middle` kimlikte ortadan (son ek görünür, `<MiddleEllipsis>`);
 *  `wrap` sarar (overflow-wrap: anywhere, harf ortasından kırmaz). */
export type CellTruncate = 'end' | 'middle' | 'wrap';

/** v0.10.939 (tablo standardı T8) — özel kolon türü. Bugün tek değer. */
export type ColumnKind = 'actions';

// v0.10.939 (tablo standardı dilim 2, inceleme) — `accessor` / `cell` /
// `CellContext` SİLİNDİ: v0.10.246'dan beri 0 benimseyen (yazılmış-
// bağlanmamış). Hücre çizimi `<DataTableCell>` + `dt.cellProps`; uyum
// katmanı bırakılmadı.
export interface ColumnDef<T> extends DataTableColumn<T> {
  /** false = kimlik kolonu (Traces: time, operation) — gizlenemez. */
  hideable?: boolean;
  reorderable?: boolean;
  /** Sunucu ORDER BY anahtarı (istemci sıralaması serverSort ile yasak). */
  serverSortKey?: string;
  /** Renderer kendi <a>'sını basar; satır-link sarmalamaz. */
  ownLink?: boolean;
  /** v0.10.939 (tablo standardı T5) — kimlik/kod kolonu (id, hash, SQL,
   *  öznitelik anahtarı): hücre `.mono`. Sayı kolonunda KULLANILMAZ (T4:
   *  sayılar arayüz fontunda, hizayı `numeric`in tabular-nums'ı tutar). */
  mono?: boolean;
  /** v0.10.939 (tablo standardı T9) — satıra göre ton: `.cell-err` /
   *  `.cell-warn` / `.cell-muted` / `.cell-faint`. `undefined` = nötr. */
  tone?: (row: T) => CellTone | undefined;
  /** v0.10.939 (tablo standardı T11) — varsayılan `'end'`. `end`/`middle`
   *  kırpar ve dizge değer verilince tam değeri `title`a koyar. */
  truncate?: CellTruncate;
  /** v0.10.939 (tablo standardı T8) — `'actions'`: başlık etiketi BOŞ
   *  (`label` yalnız aria-label olur), sıralanmaz (sortValue yok sayılır),
   *  boyutlanmaz, sağa yaslı `td/th.col-actions`; satır linkine sarılmaz.
   *  (Hücre İÇİNDEKİ eylem satırı sarmalayıcısı `.cell-actions` ayrıdır.) */
  kind?: ColumnKind;
}

export interface ColumnModelBinding {
  value: ColumnModel | null;
  onChange: (next: ColumnModel) => void;
}

export interface SelectionBinding<T> {
  mode: 'single' | 'multi';
  getRowId: (row: T) => string;
  value?: ReadonlySet<string>;
  onChange?: (ids: ReadonlySet<string>) => void;
}

export interface ServerBinding {
  page: number;
  pageSize: number;
  hasMore: boolean;
  onPage: (p: number) => void;
  onSort?: (s: SortState) => void;
}
