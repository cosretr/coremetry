// coremetry/DataTable — DONDURULMUŞ API (v0.10.1617, shadcn Faz 1 R9, plan
// §6). Faz 2 dilimleri bu tiplere karşı yazar. Arka uçtan BAĞIMSIZ: hiçbir
// prop useDataTable'ı ya da TanStack'i adlandırmıyor — gövde Traces
// diliminde (§11 g) değişirse yalnız DataTable.tsx değişir. Kolon/sıra/
// sunucu/seçim/kolon-modeli tipleri BUGÜNKÜ tipler (ui/DataTable'dan yeniden
// dışa aktarım): hiçbir çağıran kolon dizisini yeniden yazmaz.
import type { ReactNode } from 'react';
import type { LucideIcon } from 'lucide-react';
import type {
  ColumnDef, SortState, ServerBinding, SelectionBinding, ColumnModelBinding,
  DataTableStateProps as UiDataTableStateProps, DataTableStateStaticProps,
} from '@/components/ui/DataTable';
import type { Density } from '@/lib/density';

export type { ColumnDef, SortState, ServerBinding, SelectionBinding, ColumnModelBinding, DataTableStateStaticProps };

/** Satır yokken basılan durum (loading / empty / no-match / error …); `dt`
 *  ve kolon sayımı bileşenin. Bugünkü sayfaların hesapladığı tipin aynısı
 *  (`Omit<DataTableStateProps<Row>, 'dt'>`). */
export type DataTableStateInput<T> = Omit<UiDataTableStateProps<T>, 'dt' | 'leading' | 'trailing' | 'colSpan'>;

export interface MenuItemSpec {
  id: string;
  label: string;
  onSelect(): void;
  icon?: LucideIcon;
  danger?: boolean;
  disabled?: boolean;
}

export interface DataTableSortOptions {
  initial?: SortState;
  /** Sunucunun kabul ettiği sıralama kimlikleri (bayat URL değeri düşer). */
  ids?: readonly string[];
  /** URL parametre adı (varsayılan `s_<storageKey>`). */
  urlKey?: string;
  /** Verilince sıralama sunucuda; satırlar olduğu gibi çizilir. */
  server?: ServerBinding;
  onChange?(s: SortState): void;
}

export interface DataTableProps<T> {
  storageKey: string;
  columns: ColumnDef<T>[];
  rows: T[];
  sort?: DataTableSortOptions;
  columnModel?: ColumnModelBinding;
  selection?: SelectionBinding<T>;
  getRowHref?(row: T): string | undefined;
  rowLinkReplace?: boolean;
  onOpen?(row: T): void;
  /** Satır yokken ne basılacağı — ZORUNLU: boş panel yok (CLAUDE.md §7). */
  state: DataTableStateInput<T>;
  virtual?: { rowHeight?: number; overscan?: number; height?: number | string };
  density?: Density;
  /** Hücre içeriği; verilmezse `col.sortValue(row)` düz değer olarak. */
  renderCell?(row: T, col: ColumnDef<T>): ReactNode;
  /** v0.10.1674 (ADDITIVE): başlık içeriği; verilmezse `col.label`. Kolon
   *  tipi (`label: string`) değişmez — sıra/boyut/storageKey `id`'ye bağlı,
   *  başlık yalnız GÖRÜNTÜ (ör. ipuçlu "Sağlık ⚠"). `kind: 'actions'`
   *  kolonunun başlığı yine boş. ui/DataTableHead `renderLabel`'ın aynısı. */
  renderHeader?(col: ColumnDef<T>): ReactNode;
  /** Satır başına ⋯ menüsü — tabloda TEK paylaşılan menü (satır başına Radix kökü yok).
   *  Faz 1: `virtual` ile birlikte çizilmez (VirtualTable trailing kolonu yok). */
  rowActions?: {
    items(row: T): MenuItemSpec[];
    label: string;
    /** R15 (v0.10.X, ADDITIVE, operatör onayı 2026-10-30): satırın ⋯
     *  düğmesinin erişilebilir adı (verilmezse `label`, ör. "zeta-prod eylemleri"). */
    triggerLabel?(row: T): string;
    /** R15: ⋯ düğmesinin `id`'si — sayfanın odak hedefi (engellenen eylemden
     *  sonra odak o satırın düğmesine döner). Verilmezse id basılmaz. */
    triggerId?(row: T): string;
    /** R15: ⋯ düğmesi devre dışı (ör. sabitlenmiş boş "yeni" satır). */
    isDisabled?(row: T): boolean;
  };
  /** Satır anahtarı (verilmezse selection.getRowId, yoksa sıra). */
  getRowKey?(row: T, index: number): string;
  /** Faz 2 dilim 1 R5 (G4, operatör onaylı; ADDITIVE): satır başına açılır
   *  ayrıntı satırı — veri satırının hemen altında ikinci bir `<tr>`, tek
   *  hücre, `colSpan` = görünür kolonlar (+1 rowActions). Açık/kapalı durumu
   *  SAYFANIN (`isOpen`). `virtual` ile çizilmez (rowActions'la aynı Faz 1
   *  sınırı: VirtualTable'ın satır başına ikinci satırı yok). */
  detail?: {
    isOpen(row: T): boolean;
    render(row: T): ReactNode;
    /** R15 (ADDITIVE): ayrıntı hücresinin sınıfı (ör. T6 `row-detail` —
     *  bg2 + dolgu + sarar; `td-full` — tam genişlik, sarar). Verilmezse
     *  sınıfsız `<td>` (bugünkü işaretleme). */
    cellClassName?(row: T): string | undefined;
  };
  /** R15 (ADDITIVE, operatör onayı 2026-10-30): sıralanan satırların
   *  ARDINDAN, sıralamanın DIŞINDA çizilen satırlar (ör. Argo CD'nin boş
   *  "yeni instance" satırı — yön ne olursa olsun en sonda). Aynı hücre /
   *  rowActions / detail / openRow; boş olmayan liste boş durumu bastırır.
   *  `virtual` kipinde çizilmez. */
  pinnedRows?: T[];
  /** R15 (ADDITIVE): açık satır — `isOpen` satırı `row-selected` vurgusu
   *  alır (satır içi form / ayrıntı açıkken). `onActivate` verilince satır
   *  etkinleştirilir (role=button, tabIndex 0, tık + Enter/Space —
   *  lib/a11y `rowActivation`; içteki düğmelerin tıkı satıra çıkmaz). onOpen'ın
   *  global j/k gezinmesi YOK. `virtual` kipinde çizilmez. */
  openRow?: { isOpen(row: T): boolean; onActivate?(row: T): void };
  /** R15 (ADDITIVE): `<table aria-label>` — tablonun erişilebilir adı (bir
   *  sayfada birden çok tablo: "hub-1 keşif adayları"). Verilmezse basılmaz.
   *  `virtual` kipinde çizilmez. */
  ariaLabel?: string;
}
