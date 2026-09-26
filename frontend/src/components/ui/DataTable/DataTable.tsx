import {
  useCallback, useEffect, useMemo, useRef, useState,
  type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent, type ReactNode, type RefObject,
} from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { orderColumnsByModel, type ColumnModel } from '@/lib/columnModel';
import { EMPTY_SELECTION, toggleRow, rangeSelect, selectAll, pruneSelection, type SelectionState } from '@/lib/rowSelection';
import { useTableNav, type TableNav } from '@/lib/useTableNav';
import { useShortcuts } from '@/lib/keyboard';
import { useIsNarrow } from '@/lib/useNarrow';
import {
  columnLayoutSig, computeSortedRows, fitColumnWidths, formatSortParam,
  parseSortParam, readPersistedWidths, resolveToggle, sortIdKnown, visibleColumns,
  type DataTableColumn, type SortState,
  stickyLeftOffsets,
} from '@/lib/dataTable';
import { getItem, setItem, dtSortKey, dtWidthKey } from '@/lib/storage';
import { rowClickHandlers } from '@/lib/utils';
import type { CellTone, ColumnDef } from './types';
import { DataTableHeadMenu } from './HeadMenu';
import { DEFAULT_W } from './rowHeight';

// DataTable — Coremetry's shared sortable + column-resizable table
// primitive (v0.7.53). Project principle: EVERY data table is
// column-sortable AND column-resizable. Adoption is three lines:
//
//   const dt = useDataTable({ storageKey: 'slowqueries', columns, rows,
//                             initialSort: { id: 'totalMs', dir: 'desc' } });
//   <table {...dt.tableProps}>
//     <DataTableColgroup dt={dt} leading={[36]} />
//     <DataTableHead dt={dt} leading={<th style={{ width: 36 }} />} />
//     <tbody>{dt.sortedRows.map(renderRow)}</tbody>
//   </table>
//
// v0.10.939 (tablo standardı dilim 2) — hücre ve satır sözleşmesi:
//   • `dt.tableProps` = `{ className: 'dt' }` — satır içi `{ tableLayout:
//     'fixed', width: '100%' }` yazımının (125 site) sınıf karşılığı (T10).
//   • Kolon bayrakları (types.ts ColumnDef): `numeric` (td.num), `mono`
//     (kimlik/kod), `tone(row)` (.cell-err/-warn/-muted/-faint),
//     `truncate: 'end'|'middle'|'wrap'`, `kind: 'actions'`. Sayfa sınıf
//     YAZMAZ: `<td {...dt.cellProps(r, 'p95', text)}>` ya da
//     `<DataTableCell dt={dt} col="p95" row={r} value={text} />`
//     (kırpılan dizge değer `title`a gider — T11 "tam değer ipucunda";
//     `numeric` kolonda ipucu YOK, `truncate` açıkça verilmedikçe).
//     v0.10.939 (tablo standardı T7) — argüman sırası TEK: önce satır, sonra
//     kolon — `cellProps(row, colId, value?)` ve `rowLink(row, colId)`.
//     Sabit kolon (`stickyLeft/Right`) sınıfı ve `left` ofseti SAYFADA
//     kalır: leading hücre genişliğini primitif bilmez.
//   • Başlık satırının sağ ucunda ⋯ → "Kolonları sıfırla" (S8,
//     HeadMenu.tsx): başlık satırının üstüne gelince / odakta belirir,
//     etiketin üstüne biner (yer ayırmaz). Sayfa başına sıfırlama düğmesi YOK.
//
// SATIR = BAĞLANTI (T7, `getRowHref`) — gezinen satırın deseni:
//
//   const dt = useDataTable({ …, getRowHref: r => serviceHref(r.name) });
//   <tr {...dt.rowProps(i, r)}>                    // data-row-action + yedek tık
//     <DataTableCell dt={dt} col="name" row={r} value={r.name} />  // <Link>
//     <DataTableCell dt={dt} col="p95" row={r} value={fmtMs(r.p95)} />
//     <td {...dt.cellProps(r, 'links')}><SubjectLink …/></td>       // ownLink
//   </tr>
//
//   • Her düz hücre AYNI href'i taşıyan gerçek `<Link className="row-link">`
//     (`dt.rowLink(row, colId)`): orta tık / ⌘-tık / sağ tık "yeni sekmede
//     aç" tarayıcıdan gelir. Yalnız İLK link hücresi Tab durağı; diğerleri
//     `tabIndex=-1` (satır başına tek durak — Traces'te 8'di). Linkli
//     hücrenin `row-cell` sınıfını (dolgu linke) `<DataTableCell>` basar —
//     `cellProps` basmaz; linki elle çizen sayfa (önerilmez) onu da yazar.
//   • Kendi linkini/düğmesini taşıyan kolon `ownLink: true` (ya da `kind:
//     'actions'`): SARILMAZ — `<a>` içinde `<a>`/`<button>` geçersiz
//     (problemsRowLink.test kuralı).
//   • `rowProps(i, row)` satıra `data-row-action` + YEDEK fare işleyicisi
//     basar (lib/utils `rowClickHandlers` — Services'in yardımcısı, ikinci
//     kopya yok): ownLink/eylem hücresinin BOŞLUĞUNA tık da açar (el imleci
//     yalan söylemez, rowAction.contract ters yön). Etkileşimli bir öğeden
//     (a, button, input, role=button…) gelen tık yedeği tetiklemez; link
//     hücreleri `stopPropagation` taşır — aynı tık iki kez gezinmez.
//   • getRowHref'li tabloda `row` ZORUNLU: verilmezse `dt.sortedRows[i]`
//     varsayılır ve gruplu/süzülmüş/sayfalanmış bir listede bu BAŞKA bir
//     satırın href'idir (sessizce yanlış yere gider). Geliştirmede
//     (import.meta.env.DEV) tablo başına bir kez console.error basılır.
//   • URL-parametreli çekmece (?problem=…) için `rowLinkReplace: true`:
//     linkler ve yedek `replace` ile gezinir (geçmiş sözleşmesi).
//   • Seçim (T2): `selection` verilmiş tabloda seçili satır `rowProps`tan
//     `row-selected` alır (klavye imleciyle AYNI tek seçili görünüm).
//
// Sort is CLIENT-SIDE by default (for the common "fetched array of
// ≤ a few k rows" table). Server-paged tables have two options:
// enable `serverSort` (v0.8.251 — the hook keeps the full sort UX +
// URL/localStorage state but the page forwards `sort` to its fetch;
// Services is the template) or keep their own headers and adopt only
// the resize half (Traces). Sort + width state persist to
// localStorage under the storageKey, so the operator's layout
// survives reloads — same contract as the Traces v0.7.47 cols.

// v0.10.939 (tablo standardı T12) — DEFAULT_W rowHeight.ts'te (DataTableState
// iskeleti aynı değeri okur); burada yalnız alt sınır.
const DEFAULT_MIN = 48;

/** v0.10.939 (tablo standardı T9/T5) — ton → sınıf. Sınıflar globals.css'te
 *  (.cell-err/.cell-warn v0.10.931, .cell-muted/.cell-faint v0.10.939). */
const TONE_CLASS: Record<CellTone, string> = {
  err: 'cell-err',
  warn: 'cell-warn',
  muted: 'cell-muted',
  faint: 'cell-faint',
};

/** v0.10.939 (tablo standardı T10) — `dt.tableProps`; sabit düzen `table.dt`. */
const TABLE_PROPS = { className: 'dt' } as const;

/** v0.10.939 (tablo standardı T4/T5/T9/T11) — `dt.cellProps` dönüşü (`<td {...}>`). */
export interface CellProps {
  className?: string;
  /** Kırpılan (`truncate` end/middle) hücrenin TAM dizge değeri (T11). */
  title?: string;
}

/** v0.10.939 (tablo standardı T7) — `dt.rowLink` dönüşü: `<Link {...link}>`. */
export interface RowLinkProps {
  to: string;
  className: string;
  /** Satırın İLK link hücresi Tab durağıdır; diğerleri -1 (satır başına tek durak). */
  tabIndex?: -1;
  replace?: boolean;
  /** Satırın yedek tık işleyicisi (rowProps) aynı tıkla ikinci kez gezinmesin. */
  onClick: (e: ReactMouseEvent<HTMLElement>) => void;
}

/** `dt.rowProps(i)` dönüşü — `<tr {...}>`. İşleyiciler yalnız getRowHref'li satırda. */
export interface RowProps {
  'data-row-idx': number;
  'data-table-id': string;
  'data-row-action'?: true;
  className?: string;
  onClick?: (e: ReactMouseEvent<HTMLElement>) => void;
  onAuxClick?: (e: ReactMouseEvent<HTMLElement>) => void;
  onMouseDown?: (e: ReactMouseEvent<HTMLElement>) => void;
}

// v0.10.939 (tablo standardı T7) — satırın yedek işleyicisi `rowClickHandlers`
// (lib/utils) — etkileşimli öğeden (a, button, input, role=button…) gelen
// olayı YOK SAYAN koruma orada, Services/TracesResult/OperationsTable ile
// ORTAK (ikinci kopya yok). Link hücresi tıkını yine kendisi durdurur:
// VirtualTable'ın korumasız `onRowClick`i de aynı tıkla ikinci kez gezinmesin.
const stopRowClick = (e: ReactMouseEvent<HTMLElement>) => e.stopPropagation();

export interface DataTable<T> {
  // v0.9.928 — tablonun kimliği. `rowProps` zaten satıra basıyordu ama
  // BAŞLIK tıkı (sıralama) hiçbir kimlik taşımıyordu: j/k arbitrajı
  // "operatör bu tabloyla etkileşti" sinyalini kaçırıyordu. DataTableHead
  // aynı damgayı `<thead>`e basmak için buradan okuyor.
  storageKey: string;
  // v0.10.939 — ColumnDef (DataTableColumn'un ADDİTİF genişlemesi): hücre
  // bayrakları (mono/tone/truncate/kind) buradan okunur.
  columns: ColumnDef<T>[];
  // v0.9.988 (D6) — `<colgroup>`/`<thead>`de gerçekten çizilen kolonlar:
  // `headerHidden` düşmüş, dar ekranda `mobileHide` de düşmüş. Bir
  // kolonu `mobileHide` işaretleyen sayfa GÖVDE hücresini de buradan
  // (ya da `narrow`dan) sürmek zorunda — yoksa hücreler kayar.
  visibleColumns: ColumnDef<T>[];
  // Telefon genişliği (<640px, D2'nin eşiğiyle aynı). Gövde hücresini
  // kolon dizisinden sürmeyen tablolar için ham kanca.
  narrow: boolean;
  sortedRows: T[];
  sort: SortState;
  toggleSort: (id: string) => void;
  setSort: (s: SortState) => void;
  colWidths: Record<string, number>;
  startResize: (id: string, e: ReactPointerEvent) => void;
  /** v0.10.249 — klavye/programatik genişlik değişimi (minWidth kelepçeli). */
  resizeBy: (id: string, deltaPx: number) => void;
  resetLayout: () => void;
  // Keyboard nav (UX#4). Always present; inert (selected = -1, no key
  // bindings) unless the caller supplied onOpen. Spread `rowProps(i)` on each
  // <tr> for data-row-idx + the .row-selected accent.
  nav: TableNav<T>;
  // v0.10.933 (tablo standardı T2) — `data-row-action` yalnız onOpen verilmiş
  // tabloda: satır açılabilir, imleç + hover globals.css'ten bu işaretle gelir.
  // v0.10.939 (T7) — getRowHref'li tabloda href'i olan satır da işaret alır
  // ve YEDEK fare işleyicisi taşır (ownLink/eylem hücresinin boşluğu).
  // `row`: `index` dt.sortedRows'taki sıradır; gruplu/süzülmüş bir listede
  // satırı AÇIKÇA verin (ServicePodsTable gibi). v0.10.939 (T7) —
  // getRowHref'li tabloda `row` verilmezse geliştirmede console.error.
  // v0.10.939 (T2) — seçim API'si satırı seçili sayıyorsa `row-selected`.
  rowProps: (index: number, row?: T) => RowProps;
  // v0.10.249 (DataTable dilim 4) — ADDİTİF. Seçenek verilmediğinde
  // bugünkü davranış bayt-bayt aynı (109 çağrı yeri).
  /** Bildirilen kolonların tamamı (columnModel gizlemiş olsa da). */
  allColumns: ColumnDef<T>[];
  /** Satır seçimi (selection verilmişse); yoksa null. */
  selection: DataTableSelection<T> | null;
  /** Sunucu sayfalama demeti (server verilmişse); j son satırda onPage(page+1). */
  server: DataTableServer | null;
  /** Satır = link (v0.10.216); VirtualTable ownLink olmayan hücreyi sarar. */
  getRowHref: ((row: T) => string | null) | null;
  /** v0.10.939 (T10) — `<table {...dt.tableProps}>`: sabit düzen + tam genişlik (`table.dt`). */
  tableProps: { className: string };
  /** v0.10.939 (T4/T5/T8/T9/T11) — hücre sınıfı + kırpılan değerin `title`ı.
   *  `value` yalnız DİZGE ise ipucu olur (`numeric` kolonda yalnız `truncate`
   *  açıkça verilmişse); bilinmeyen kolon → `{}`. Argüman sırası `rowLink` ile
   *  aynı: önce satır. `row-cell` BASMAZ — linki çizen `<DataTableCell>` basar. */
  cellProps: (row: T, colId: string, value?: unknown) => CellProps;
  /** v0.10.939 (T7) — hücrenin satır linki; getRowHref yok / href null /
   *  ownLink / actions kolonu → null. */
  rowLink: (row: T, colId: string) => RowLinkProps | null;
}

export interface DataTableSelection<T> {
  ids: ReadonlySet<string>;
  isSelected: (row: T) => boolean;
  toggle: (row: T) => void;
  range: (row: T) => void;
  all: () => void;
  clear: () => void;
  mode: 'single' | 'multi';
  getRowId: (row: T) => string;
}

export interface DataTableServer {
  page: number;
  pageSize: number;
  hasMore: boolean;
  onPage: (p: number) => void;
  onSort?: (s: SortState) => void;
}

export function useDataTable<T>({ storageKey, columns: declaredColumns, rows, initialSort, persistSort = true, sortIds, serverSort, onSortChange, urlSortFallback, onOpen, searchRef, columnModel, selection: selectionOpt, server, getRowHref, rowLinkReplace = false }: {
  storageKey: string;
  columns: ColumnDef<T>[];
  rows: T[];
  initialSort?: SortState;
  // persistSort (v0.10.669) — false: sıralama localStorage'dan OKUNMAZ ve
  // YAZILMAZ; URL `s_<storageKey>` yine kazanır, oturum içi tıklama çalışır,
  // yeni ziyaret initialSort'a döner. Traces gibi "en yeni önce" anlamı
  // taşıyan tablolar için; genişlikler etkilenmez. Varsayılan true.
  persistSort?: boolean;
  // sortIds (v0.10.831) — SUNUCUNUN/SAYFANIN kabul ettiği sıralama kimlikleri.
  //
  // Verildiğinde `s_<storageKey>`, urlSortFallback ve localStorage
  // basamaklarının KİMLİĞİ doğrulanır; tanınmayan değer bir sonraki basamağa
  // düşer ve en dipte `initialSort` durur. Reddedilen parametre URL'de
  // BIRAKILIR (inceleme kararı 2026-09-20: yeniden yazmak churn'dü) — durum
  // ve başlık okları zaten etkin sıralamayı gösterir.
  //
  // Operatör-bildirimli gerekçe (/traces): bayat bir
  // `?s_traces-list=startTime.desc` tablonun sıralama durumu oluyordu; hiçbir
  // başlık aktif görünmüyor, sayfanın dt.sort → sunucu çevirici efekti
  // tanımadığı kimlikte erken dönüyor ve sunucu kendi varsayılanıyla
  // çekiyordu. Sessiz ıraksama.
  //
  // OPT-IN, çünkü kolon listesi bir kimlik kümesi DEĞİL: /endpoints hook'a
  // yalnız GÖRÜNÜR kolonları verir ve gizli bir kolona göre sıralama bilerek
  // yaşar. Küme verilmeyen 67 tablo bayt bayt eski davranışta kalır.
  sortIds?: readonly string[];
  /** v0.10.249 — sıra/gizli modeli (lib/columnModel). Genişlik imzası bildirilen kolonlardan. */
  columnModel?: { value: ColumnModel | null; onChange?: (next: ColumnModel) => void };
  /** v0.10.249 — satır seçimi; getRowId zorunlu (indeks anahtarı sıralamada kırılır). */
  selection?: { mode: 'single' | 'multi'; getRowId: (row: T) => string; value?: ReadonlySet<string>; onChange?: (ids: ReadonlySet<string>) => void };
  /** v0.10.249 — sunucu sayfalama/sıralama demeti. */
  server?: DataTableServer;
  // v0.10.939 (tablo standardı T7) — satır = gerçek bağlantı. Verilince
  // `dt.rowLink` / `<DataTableCell>` düz hücreleri `<Link class="row-link">`
  // ile sarar, `rowProps` işaret + yedek tık basar (dosya başı deseni).
  getRowHref?: (row: T) => string | null;
  // v0.10.939 (T7) — URL-parametreli çekmece hedefi (?problem=…): linkler ve
  // yedek tık `replace` ile gezinir, geri tuşu çekmece geçmişiyle dolmaz.
  rowLinkReplace?: boolean;
  // serverSort (v0.8.251) — for server-paged tables (Services first): the
  // ORDER BY runs on the backend, so the hook keeps EVERY piece of the sort
  // UX — URL `s_<storageKey>` param, localStorage persistence, header click /
  // arrow semantics, all identical to client mode — but never reorders rows
  // itself: `sortedRows` is the `rows` prop verbatim. The page watches the
  // returned `sort` (or supplies onSortChange) and re-fetches with it. A
  // column still needs `sortValue` to be click-sortable; in this mode the
  // accessor is never invoked — it's the sortable marker + naturalDir carrier.
  serverSort?: boolean;
  // Fired with the new state on every sort change the hook applies — header
  // click, programmatic setSort, or an inbound URL (back/forward) restore.
  // Alternative to watching the returned `sort` in an effect dep; both see
  // the same state.
  onSortChange?: (s: SortState) => void;
  // Back-compat URL bridge: treated as an inbound URL sort when the
  // `s_<storageKey>` param is absent — ranks ABOVE localStorage (a shared
  // link's intent beats the viewer's personal default) but BELOW
  // `s_<storageKey>`. Services feeds decodeLegacyServicesSort() through this
  // so pre-v0.8.251 `?sort=&dir=` links keep landing on the sender's sort.
  urlSortFallback?: SortState | null;
  // When provided, the table gains app-wide keyboard nav: j/k move row
  // selection, Enter/o open the row (calls onOpen), gg/G jump, Esc clears,
  // and "/" focuses searchRef. Omit for a plain display table. (UX#4)
  onOpen?: (row: T, index: number) => void;
  searchRef?: RefObject<HTMLInputElement | null>;
}): DataTable<T> {
  const sortLSKey = dtSortKey(storageKey);
  const widthLSKey = dtWidthKey(storageKey);
  // columnModel: görünür sıra buradan; genişlik imzası (layoutSig) ve LS
  // anahtarı BİLDİRİLEN kolonlardan — bir kolonu gizlemek herkesin
  // genişliklerini sıfırlamaz (audit §9).
  const modelValue = columnModel?.value ?? null;
  const columns = useMemo(() => orderColumnsByModel(declaredColumns, modelValue), [declaredColumns, modelValue]);
  // Sort is shareable (UX#3): the URL param `s_<storageKey>` wins so a copied
  // link reproduces the exact sort; else the urlSortFallback bridge (an OLD
  // URL schema decoded by the page — still link intent, so it outranks the
  // viewer's own default); else the operator's personal localStorage default;
  // else initialSort. Namespaced by storageKey so two tables on one page
  // never collide. Writes hit BOTH the URL (replace — no history spam) and
  // localStorage. Widths stay localStorage-only (per-browser ergonomics, not
  // view state worth sharing).
  const [searchParams, setSearchParams] = useSearchParams();
  const urlKey = `s_${storageKey}`;
  const urlSort = searchParams.get(urlKey);
  // v0.10.831 — `sortIds` OPT-IN ve kimlik BURADA TÜRETİLMEZ.
  //
  // İlk taslak kümeyi `declaredColumns`tan türetiyordu ve bu /endpoints'in
  // yazılı sözleşmesini kırıyordu (Endpoints.tsx:431-434): o sayfa hook'a
  // YALNIZ görünür kolonları veriyor ve gizlenmiş bir kolona göre sıralama
  // BİLEREK yaşıyor ("serverSort forwards the persisted sort id to the fetch
  // regardless of visibility"). Yani kolon listesi "geçerli kimlikler" kümesi
  // DEĞİL; kümeyi ancak sunucunun ne kabul ettiğini bilen SAYFA verebilir.
  const knownSortSig = sortIds ? sortIds.join('|') : '';
  const [sort, setSortState] = useState<SortState>(() =>
    resolveInitialSort(storageKey, urlSort, initialSort, urlSortFallback, persistSort, sortIds));
  // v0.9.695 — kalıcı genişlikler KOLON TANIMINA MÜHÜRLÜ.
  //
  // İmza, yakalandığı kolon kümesiyle uyuşmuyorsa genişlikler atılıyor.
  // Sürümsüz haldeyken bayat bir harita yeni kolon tanımını sessizce
  // eziyordu — v0.9.660'ın Users düzeltmesi tam bu yüzden operatörün
  // ekranına hiç ulaşmadı (gerekçe: lib/dataTable.ts columnLayoutSig).
  const layoutSig = useMemo(() => columnLayoutSig(declaredColumns), [declaredColumns]);
  const [colWidths, setColWidths] = useState<Record<string, number>>(() =>
    readPersistedWidths(getItem<unknown>(widthLSKey, null), layoutSig));

  // v0.10.831 — kalıcılık OPERATÖRÜN EYLEMİNE bağlı, `sort` state'ine değil.
  //
  // Önceden bu bir efektti ve `sort` her değiştiğinde — MOUNT DAHİL —
  // yazıyordu. Üç sessiz sonucu vardı: (a) paylaşılan bir linkin sıralaması
  // ziyaretçinin KİŞİSEL varsayılanını kalıcı olarak eziyordu, (b) bayat bir
  // kimlik doğrulamayla reddedilince kayıt `initialSort`la eziliyordu —
  // self-heal yalnız URL için vardı, localStorage için YOKTU, (c) kayıt
  // "operatör ne seçti" değil "tablo en son ne gösterdi" oluyordu.
  // Artık tek yazıcı `setSort`: başlık tıklaması, klavye ve sayfanın
  // programatik çağrısı (Inbox ön-ayarları) — hepsi gerçek birer eylem.
  const persist = useCallback((s: SortState) => {
    if (persistSort) setItem(sortLSKey, s); // v0.10.669 — persistSort:false yazmaz
  }, [persistSort, sortLSKey]);
  useEffect(() => {
    setItem(widthLSKey, { sig: layoutSig, widths: colWidths });
  }, [colWidths, widthLSKey, layoutSig]);

  // Apply a sort to state + URL, then notify the page (serverSort pages
  // re-fetch off this — or off the returned `sort`, same thing).
  const setSort = useCallback((s: SortState) => {
    setSortState(s);
    persist(s); // v0.10.831 — kalıcılık yalnız BURADA (gerçek bir eylem)
    setSearchParams(prev => {
      const next = new URLSearchParams(prev);
      const v = formatSortParam(s);
      if (v) next.set(urlKey, v);
      else next.delete(urlKey);
      return next;
    }, { replace: true });
    onSortChange?.(s);
    server?.onSort?.(s);
  }, [setSearchParams, urlKey, onSortChange, server, persist]);

  // Back/forward — or an inbound shared link — changes the URL sort: restore
  // it into state. Guarded to fire only on a genuine difference, which also
  // stops a loop with setSort's own write. onSortChange fires here too so a
  // serverSort page that relies on the callback re-fetches on back/forward.
  //
  // v0.10.831 — `sortIds` verilmişse burada da DOĞRULAMA var: mount kapısını
  // geçen bayat bir kimlik geri/ileri ya da gelen bir linkle ikinci kapıdan
  // sızamaz. Reddedilen parametre URL'de OLDUĞU GİBİ BIRAKILIYOR (inceleme
  // kararı 2026-09-20): yeniden yazmak her bayat linkte fazladan bir URL
  // yazımı demekti ve görünen hiçbir şeyi düzeltmiyordu — durum ve başlık
  // zaten doğru. `knownSortSig` bağımlılık: kümeyi sonradan büyüten bir sayfa
  // ilk turda reddettiği geçerli sıralamayı küme gelince SAHİPLENİR.
  // Kalıcılık burada ÇAĞRILMIYOR — gelen bir link operatörün kişisel
  // varsayılanı değildir.
  useEffect(() => {
    const fromUrl = parseSortParam(urlSort);
    const valid = fromUrl && sortIdKnown(fromUrl.id, sortIds) ? fromUrl : null;
    if (valid && (valid.id !== sort.id || valid.dir !== sort.dir)) {
      setSortState(valid);
      onSortChange?.(valid);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [urlSort, knownSortSig]);

  const toggleSort = useCallback((id: string) => {
    // v0.10.939 (tablo standardı T8) — eylem kolonu sıralanmaz; bir
    // `sortValue` unutulmuş olsa bile (sahte sıralanabilir, T3).
    if (columns.find(c => c.id === id)?.kind === 'actions') return;
    // resolveToggle is the pure half (lib/dataTable.ts): null = column
    // unknown / not sortable → no-op, no state write, no callback.
    const next = resolveToggle(columns, sort, id);
    if (next) setSort(next);
  }, [columns, sort, setSort]);

  // v0.9.988 (D6.5) — Pointer Events. `mousedown`/`mousemove`/`mouseup`
  // üçlüsü dokunmatik bir cihazda HİÇ ateşlemez (tarayıcı yalnız bir
  // TAP'in ardından sentetik mouse olayı üretir; sürükleme üretmez).
  // Yani kolon genişliği 68 tablonun hepsinde telefonda/tablette elle
  // düzeltilemiyordu — TraceWaterfall'da v0.9.983'te kapatılan boşluğun
  // paylaşılan katmandaki eşi. Pointer olayları fare + dokunma + kalemi
  // tek yolla kapsıyor, masaüstü davranışı bit bit aynı kalıyor.
  //
  // `pointercancel` de dinleniyor: dokunmada tarayıcı jesti devralırsa
  // (kaydırma, geri-kaydır) `pointerup` GELMEZ ve dinleyiciler sonsuza
  // dek asılı kalırdı.
  const startResize = useCallback((id: string, e: ReactPointerEvent) => {
    e.preventDefault();
    e.stopPropagation();
    const col = columns.find(c => c.id === id);
    const startX = e.clientX;
    const startW = colWidths[id] ?? col?.width ?? DEFAULT_W;
    const min = col?.minWidth ?? DEFAULT_MIN;
    const onMove = (ev: PointerEvent) => {
      const w = Math.max(min, startW + (ev.clientX - startX));
      setColWidths(s => ({ ...s, [id]: w }));
    };
    const onUp = () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      window.removeEventListener('pointercancel', onUp);
    };
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    window.addEventListener('pointercancel', onUp);
  }, [columns, colWidths]);

  const resizeBy = useCallback((id: string, deltaPx: number) => {
    const col = columns.find(c => c.id === id);
    const min = col?.minWidth ?? DEFAULT_MIN;
    setColWidths(prev => ({ ...prev, [id]: Math.max(min, (prev[id] ?? col?.width ?? DEFAULT_W) + deltaPx) }));
  }, [columns]);
  const resetLayout = useCallback(() => setColWidths({}), []);

  // serverSort mode returns `rows` verbatim (reference-equal) — the
  // backend's ORDER BY already shaped the page; see computeSortedRows.
  const sortedRows = useMemo(
    () => computeSortedRows(rows, columns, sort, !!serverSort),
    [rows, sort, columns, serverSort],
  );

  // App-wide keyboard nav (UX#4). useTableNav owns the selected index + j/k/
  // gg/G/Enter/o/Esc bindings + auto-scroll; inert when onOpen is absent (no
  // bindings) so a plain display table doesn't capture the keys. "/" focuses
  // the page filter when both onOpen + searchRef are supplied.
  // v0.10.249 — sunucu demeti: son satırda j → sonraki sayfa, ilk satırda
  // k → önceki (onPageBoundary v0.9.1018; Traces hiç bağlamamıştı).
  const onPageBoundary = useCallback((dir: 'next' | 'prev') => {
    if (!server) return false;
    if (dir === 'next' && server.hasMore) { server.onPage(server.page + 1); return true; }
    if (dir === 'prev' && server.page > 0) { server.onPage(server.page - 1); return true; }
    return false;
  }, [server]);
  const nav = useTableNav<T>(sortedRows, { onOpen, enabled: !!onOpen, pageId: storageKey, onPageBoundary: server ? onPageBoundary : undefined });
  // v0.10.249 — satır seçimi: kontrollü (value/onChange) ya da iç durum.
  const [selInner, setSelInner] = useState<SelectionState>(EMPTY_SELECTION);
  const selIds = selectionOpt?.value ?? selInner.ids;
  const orderedIds = useMemo(
    () => (selectionOpt ? sortedRows.map(r => selectionOpt.getRowId(r)) : []),
    [sortedRows, selectionOpt],
  );
  const applySel = useCallback((next: SelectionState) => {
    setSelInner(next);
    selectionOpt?.onChange?.(next.ids);
  }, [selectionOpt]);
  useEffect(() => {
    if (!selectionOpt) return;
    const pruned = pruneSelection(selInner, orderedIds);
    if (pruned !== selInner) applySel(pruned);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [orderedIds]);
  const selectionApi = useMemo<DataTableSelection<T> | null>(() => {
    if (!selectionOpt) return null;
    const cur = (): SelectionState => ({ ids: selIds, anchor: selInner.anchor });
    return {
      ids: selIds,
      mode: selectionOpt.mode,
      getRowId: selectionOpt.getRowId,
      isSelected: row => selIds.has(selectionOpt.getRowId(row)),
      toggle: row => applySel(toggleRow(cur(), selectionOpt.getRowId(row), selectionOpt.mode)),
      range: row => applySel(selectionOpt.mode === 'multi'
        ? rangeSelect(cur(), orderedIds, selectionOpt.getRowId(row))
        : toggleRow(cur(), selectionOpt.getRowId(row), 'single')),
      all: () => { if (selectionOpt.mode === 'multi') applySel(selectAll(orderedIds)); },
      clear: () => applySel(EMPTY_SELECTION),
    };
  }, [selectionOpt, selIds, selInner.anchor, orderedIds, applySel]);
  useShortcuts(
    onOpen && searchRef
      ? [{ keys: '/', label: 'Focus filter', group: 'Lists', handler: () => searchRef.current?.focus() }]
      : [],
    [onOpen, searchRef, storageKey],
  );
  // v0.9.926 — satır KENDİ tablosunun kimliğini de taşıyor. Oto-kaydırma
  // eskiden `document.querySelector('[data-row-idx=N]')` ile belgedeki
  // İLK eşleşeni buluyordu: iki tablolu bir sayfada j/k bir tabloda
  // seçim yaparken kaydırma DİĞERİNDE oluyordu. Kimliği satıra basmak,
  // sarmalayıcıya basmaktan daha ucuz (sayfaların `<div>`ine dokunmuyor).
  const openable = !!onOpen;
  const navigate = useNavigate();
  // v0.10.939 (tablo standardı T7) — `rowProps(i)` satırsız çağrı uyarısı
  // tablo başına BİR kez (her satır × her render konsolu boğardı).
  const warnedRowArg = useRef(false);
  const rowProps = useCallback(
    (index: number, rowArg?: T): RowProps => {
      /* v0.10.939 (tablo standardı T7) — getRowHref'li tabloda satır AÇIKÇA
         verilmeli: `sortedRows[index]` gruplu/süzülmüş/sayfalanmış listede
         başka bir satırdır ve yedek tık sessizce YANLIŞ href'e gider.
         Geliştirmede sesli (üretim paketinde bu dal yok). */
      if (import.meta.env.DEV && getRowHref && rowArg === undefined && !warnedRowArg.current) {
        warnedRowArg.current = true;
        console.error(`useDataTable(${storageKey}): getRowHref'li tabloda rowProps(i, row) — satırı açıkça verin; `
          + 'rowProps(i) dt.sortedRows[i]\'yi varsayar (gruplu/süzülmüş listede yanlış satır).');
      }
      const row = rowArg !== undefined ? rowArg : sortedRows[index];
      /* v0.10.939 (tablo standardı T2) — TEK seçili görünüm: klavye imleci
         (nav.selected) YA DA seçim API'sinin seçili saydığı satır. */
      const selected = nav.selected === index
        || (selectionApi !== null && row !== undefined && selectionApi.isSelected(row));
      const base: RowProps = {
        'data-row-idx': index,
        'data-table-id': storageKey,
        /* v0.10.933 (tablo standardı T2) — anahtar YALNIZ onOpen'lı tabloda
           basılır: `'data-row-action': undefined` yayılsaydı, yayılım sırası
           satırdaki açık bir işareti (rowClickHandlers vb.) sıfırlayabilirdi. */
        ...(openable ? { 'data-row-action': true as const } : {}),
        className: selected ? 'row-selected' : undefined,
      };
      /* v0.10.939 (tablo standardı T7) — getRowHref'li satır: işaret + YEDEK
         fare işleyicisi, lib/utils `rowClickHandlers`tan (Services'in
         yardımcısı): etkileşimli öğeden gelen olay yok sayılır, ⌘/Ctrl/Shift
         ve orta tık yeni sekme. Link hücreleri kendi tıkını durdurur (rowLink
         onClick); buraya yalnız ownLink/eylem hücresinin boşluğu düşer. */
      const href = getRowHref && row !== undefined ? getRowHref(row) : null;
      if (!href) return base;
      return {
        ...base,
        ...rowClickHandlers(href, () => navigate(href, { replace: rowLinkReplace })),
      };
    },
    [nav, storageKey, openable, sortedRows, getRowHref, navigate, rowLinkReplace, selectionApi],
  );

  // v0.9.988 (D6.1) — dar ekran süzgeci. `narrow` false olduğu sürece
  // liste bugünkü `!headerHidden` süzgeciyle BİREBİR aynı; hiçbir kolon
  // `mobileHide` taşımadığı için bu sürümde telefonda da aynı.
  // v0.10.939 — karar saf çekirdekte (visibleColumns); süzgeç ColumnDef
  // ÖĞELERİNİ korur ki hücre bayrakları tipte kaybolmasın.
  const narrow = useIsNarrow();
  const visible = useMemo(() => {
    const keep = new Set<DataTableColumn<T>>(visibleColumns(columns, narrow));
    return columns.filter(c => keep.has(c));
  }, [columns, narrow]);

  // ── v0.10.939 (tablo standardı dilim 2) — hücre sözleşmesi ─────────────
  // Bayraklar BİLDİRİLEN kolondan okunur (columnModel gizlese de tanım aynı).
  const colById = useMemo(() => new Map(declaredColumns.map(c => [c.id, c])), [declaredColumns]);
  // T7 — satırın TEK Tab durağı: görünür sırada ilk sarılabilir kolon.
  const firstLinkColId = useMemo(
    () => visible.find(c => !c.ownLink && c.kind !== 'actions')?.id ?? null,
    [visible],
  );
  const linkHref = useCallback((row: T, colId: string): string | null => {
    if (!getRowHref) return null;
    const c = colById.get(colId);
    if (!c || c.ownLink || c.kind === 'actions') return null;
    return getRowHref(row) || null;
  }, [getRowHref, colById]);
  const rowLink = useCallback((row: T, colId: string): RowLinkProps | null => {
    const href = linkHref(row, colId);
    if (!href) return null;
    return {
      to: href,
      className: 'row-link',
      ...(colId === firstLinkColId ? {} : { tabIndex: -1 as const }),
      ...(rowLinkReplace ? { replace: true } : {}),
      onClick: stopRowClick,
    };
  }, [linkHref, firstLinkColId, rowLinkReplace]);
  const cellProps = useCallback((row: T, colId: string, value?: unknown): CellProps => {
    const c = colById.get(colId);
    if (!c) return {};
    const actions = c.kind === 'actions';
    const trunc = c.truncate ?? 'end';
    const tone = !actions && c.tone ? c.tone(row) : undefined;
    // v0.10.939 (tablo standardı T7) — `row-cell` BURADA YOK: sınıf "dolgu
    // linke" demek ve linki çizen <DataTableCell> basar. cellProps'u elle
    // yayan (linksiz) bir hücre dolgusunu kaybetmesin.
    const className = [
      c.numeric && !actions ? 'num' : '',
      c.mono && !actions ? 'mono' : '',
      tone ? TONE_CLASS[tone] : '',
      trunc === 'wrap' && !actions ? 'cell-wrap' : '',
      // v0.10.939 (tablo standardı T8) — `col-actions` (kolon türü); içteki
      // `.cell-actions` sarmalayıcısı (td > .cell-actions) ayrı bir şey.
      actions ? 'col-actions' : '',
    ].filter(Boolean).join(' ');
    const out: CellProps = {};
    if (className) out.className = className;
    // v0.10.939 (tablo standardı T11) — sayı kırpılmaz (sağa yaslı kısa
    // değer): `numeric` kolonda ipucu yalnız `truncate` AÇIKÇA verilmişse.
    // Kimlik / mono / middle kolonları ipucunu tutar.
    const titled = !c.numeric || c.truncate !== undefined || !!c.mono;
    if (!actions && titled && trunc !== 'wrap' && typeof value === 'string' && value !== '') out.title = value;
    return out;
  }, [colById]);

  return {
    storageKey, columns, visibleColumns: visible, narrow, sortedRows, sort, toggleSort, setSort, colWidths, startResize, resizeBy, resetLayout, nav, rowProps,
    allColumns: declaredColumns, selection: selectionApi, server: server ?? null, getRowHref: getRowHref ?? null,
    tableProps: TABLE_PROPS, cellProps, rowLink,
  };
}

// resolveInitialSort — the precedence the hook restores a sort with, hoisted
// so a page can ask the SAME question before the hook exists.
//
// v0.9.319 — serverSort pages have a chicken-and-egg problem: the fetch needs
// the sort, but the sort lives in a hook that needs the fetched rows. The
// Inbox resolves it by seeding its query state from this function, so the
// first request and the header arrow agree at mount. Duplicating the
// precedence at the call site instead would drift the moment this order
// changes.
//
// Order: URL `s_<storageKey>` (a shared link reproduces the sender's sort) >
// urlSortFallback (an older URL schema the page decoded — still link intent) >
// localStorage (the viewer's personal default) > initialSort.
//
// v0.10.669 — persist=false: localStorage basamağı atlanır (URL > fallback >
// initialSort). Traces "her ziyarette start time desc" için.
// v0.10.831 — `knownSortIds`: SAYFANIN beyan ettiği kabul edilebilir sıralama
// kimlikleri (hook'ta `sortIds` prop'u). Verildiğinde her basamak (URL /
// köprü / localStorage) kimliğe göre DOĞRULANIR ve tanınmayan değer bir
// sonraki basamağa düşer; en sonda sayfanın kendi `initialSort`u durur —
// tablo asla "sırasız" görünmez. Verilmezse davranış bayt bayt eskisi
// (lib/dataTable sortIdKnown). Küme KOLON LİSTESİNDEN TÜRETİLMEZ: /endpoints
// hook'a yalnız görünür kolonları verir ve gizli kolona göre sıralama bilerek
// yaşar (Endpoints.tsx:431-434).
export function resolveInitialSort(
  storageKey: string,
  urlSort: string | null,
  initialSort?: SortState,
  urlSortFallback?: SortState | null,
  persist = true,
  knownSortIds?: readonly string[],
): SortState {
  const fallback = initialSort ?? { id: null, dir: 'desc' };
  const ok = (s: SortState | null | undefined): SortState | null =>
    (s && sortIdKnown(s.id, knownSortIds) ? s : null);
  return ok(parseSortParam(urlSort)) ?? ok(urlSortFallback)
    ?? ok(persist ? getItem<SortState>(dtSortKey(storageKey), fallback) : null)
    ?? fallback;
}

// ColResizeHandle — drop-in resize grip for tables that keep their OWN
// <th>s (e.g. server-sorted Services, non-sortable LogTable) but still
// want the shared column-resize behaviour. The host <th> must be
// position:relative so the absolutely-positioned .col-resize-handle (see
// globals.css) anchors to its right edge. mousedown starts the drag;
// click is stopped so the handle can live inside a sort-on-click <th>
// without triggering a sort. (v0.7.54)
// v0.9.662 — ÇİFT TIK düzeni sıfırlar.
//
// Sürüklenen genişlik localStorage'a yazılıyor ve kalıcı. O genişlik
// tabloyu ekrandan taşırırsa operatörün geri dönüş yolu yoktu — v0.9.660'ta
// Users tablosunda tam bu oldu (kolon 0'a çöktü, tablo taştı). Kurtuluş
// yolu HATANIN YAPILDIĞI yerde duruyor: tutamağın kendisi.
//
// Burada olmasının sebebi kapsam: tutamak TEK paylaşılan bileşen, yani bu
// tek satır useDataTable kullanan HER tabloyu kapsıyor. Sayfa başına buton
// eklemek 40 dosyalık bir süpürme olurdu ve yarısı unutulurdu.
export function ColResizeHandle<T>({ dt, colId }: { dt: DataTable<T>; colId: string }) {
  // v0.9.988 (D6.5) — `onPointerDown`. `touch-action: none` CSS'te
  // (`.col-resize-handle`): onsuz tarayıcı ilk parmak hareketinde
  // jesti KAYDIRMA olarak devralır ve `pointermove`ları kesip
  // `pointercancel` atar — yani olay dinlense bile sürükleme olmaz.
  return <span className="col-resize-handle"
    onPointerDown={e => dt.startResize(colId, e)}
    onDoubleClick={e => { e.stopPropagation(); dt.resetLayout(); }}
    onClick={e => e.stopPropagation()}
    title="Drag to resize · double-click to reset all column widths" />;
}

// DataTableColgroup — emits the <colgroup> that makes table-layout:fixed
// respect (and resize) per-column widths. `leading` is the px width of any
// leading non-data columns (expand chevron, checkbox) rendered before the
// managed columns; `trailing` is the same for non-data columns rendered
// after them (actions cell, "+ Add column" manager — v0.8.306).
//
// v0.9.1030 — kaba-sığdırma. `table-layout:fixed` bir tablo, kolon
// genişliklerinin toplamı `width:100%`ü aşarsa yine de büyür; is-fit kap
// masaüstünde `overflow: visible` (D2.1) olduğundan taşma `#content`e
// çıkıp SAYFAYI yatay kaydırıyordu (operatör: /inbox "iframe gibi").
// Kap ResizeObserver ile ölçülür; YALNIZ yatay kaydırmayan kapta
// (getComputedStyle overflowX === 'visible') fitColumnWidths devreye
// girer. Kaydıran kaplar (is-fit dışı geniş tablolar, ≤1024 D2 bandı)
// ve ölçümün olmadığı ortamlar (jsdom) beyan genişlikleriyle AYNEN
// kalır — sığdırma saf çekirdekte, tablo-güdümlü testle.
export function DataTableColgroup<T>({ dt, leading, trailing }: { dt: DataTable<T>; leading?: number[]; trailing?: number[] }) {
  const ref = useRef<HTMLTableColElement | null>(null);
  const [fitPx, setFitPx] = useState(0); // 0 = ölçüm yok (fail-open)
  useEffect(() => {
    // v0.10.357 — Operator-reported (Traces: "sağa sola kaydırma olmasın, bir
    // türlü düzeltemedik"): sanal tablonun kabı `.vt-scroll`, `.table-wrap`
    // değil → closest null → sığdırma sanal tabloda HİÇ devreye girmiyordu;
    // v0.9.1334'ün "muhafaza kaldırıldı" düzeltmesi VirtualTable'a ulaşmadı
    // (tested-but-unreachable sınıfı, ikinci kez). İki kap da ölçülür.
    const wrap = ref.current?.closest('.table-wrap, .vt-scroll');
    if (!wrap || typeof ResizeObserver === 'undefined') return;
    // v0.9.1334 — MUHAFAZA KALDIRILDI, sığdırma her kapta ölçülüyor.
    //
    // Eskisi `getComputedStyle(wrap).overflowX !== 'visible'` ise
    // sığdırmayı KAPATIYORDU. O gün (v0.9.1030) doğruydu: sığdırma
    // `is-fit` kaplar için yazılmıştı ve onlar masaüstünde
    // `overflow: visible` taşıyordu. Ama v0.9.1078'de (operatör kararı,
    // yüzen şeritler) `is-fit`in `overflow: visible` kaçışı SÖKÜLDÜ ve
    // artık HER `.table-wrap` `overflow-x: auto` (globals.css:1157).
    // Yani muhafaza o günden beri HER ZAMAN kapatıyor: fitColumnWidths
    // üretimde ULAŞILAMAZ hale geldi. dataTableFit.test.ts saf çekirdeği
    // yeşil tutuyordu — hiçbir şey onun ULAŞILDIĞINI pinlemiyordu.
    //
    // Operatör aynı şikâyeti İKİ KEZ bildirdi: v0.9.1030 ("/inbox iframe
    // gibi") ve 2026-08-24 ("neden sayfa yatayda kayıyor"). İkincisinin
    // sebebi birincinin çaresinin kapatılmasıydı.
    //
    // NİYE GÜVENLİ: fitColumnWidths sığan tabloya DOKUNMUYOR — toplam
    // kaba sığıyorsa `null` döner (lib/dataTable.ts:332) ve çağıran beyan
    // edilen genişlikleri aynen kullanır. Yani etki alanı yalnız ŞU AN
    // TAŞAN tablolar. Ölçüm yoksa (jsdom, ilk mount) yine `null`:
    // fail-open.
    //
    // İKİ MEKANİZMA BESTELENİYOR: min genişlikler bile sığmazsa fonksiyon
    // tabanları döndürüyor (:338) — taşma sınırlı kalır ve kabın
    // `overflow-x: auto`su güvenlik ağı olarak devreye girer. Eski
    // muhafaza aslında "ağ var mı" sorusunun vekiliydi; ağ varken de
    // sığdırmak daha iyi ilk cevap.
    const measure = () => setFitPx(wrap.clientWidth);
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(wrap);
    return () => ro.disconnect();
  }, []);
  // v0.10.387 (dış skill denetimi C10) — çağıran satır içi dizi geçiyor
  // (`leading={[24]}`), yani her render yeni referans ve memo hiç isabet
  // etmiyordu; bağımlılık PRİMİTİF toplam (rerender-memo-with-default-value).
  const fixedPx = (leading ?? []).reduce((s, w) => s + w, 0) + (trailing ?? []).reduce((s, w) => s + w, 0);
  const fitted = useMemo(() => {
    if (!fitPx) return null;
    return fitColumnWidths(
      dt.visibleColumns.map(c => ({
        id: c.id,
        px: dt.colWidths[c.id] ?? (c.flex ? null : c.width ?? DEFAULT_W),
        min: c.minWidth ?? DEFAULT_MIN,
      })),
      fixedPx,
      fitPx,
    );
  }, [fitPx, dt.visibleColumns, dt.colWidths, fixedPx]);
  return (
    <colgroup ref={ref}>
      {(leading ?? []).map((w, i) => <col key={`lead-${i}`} style={{ width: w }} />)}
      {dt.visibleColumns.map(c => {
        // v0.9.542 — flex kolon SÜRÜKLENMEDİYSE 'auto': table-layout:fixed
        // artan genişliği ona verir, diğerleri kendi genişliğinde kalır.
        // Sürüklendiği an colWidths dolar ve sabit genişliğe döner —
        // operatörün eli her zaman kazanır (kaba SIĞDIĞI sürece).
        const w = fitted?.[c.id]
          ?? dt.colWidths[c.id] ?? (c.flex ? 'auto' : c.width ?? DEFAULT_W);
        return <col key={c.id} style={{ width: w }} />;
      })}
      {(trailing ?? []).map((w, i) => <col key={`trail-${i}`} style={{ width: w }} />)}
    </colgroup>
  );
}

// (ResetLayoutButton — v0.9.660 → v0.10.939 SİLİNDİ. Sayfa başına "Reset
// columns" düğmesi 123 tablonun 18'inde, her birinde başka bir yerdeydi;
// sıfırlama artık DataTableHead'in ⋯ menüsünde, HER tabloda aynı yerde —
// HeadMenu.tsx. Uyum katmanı bırakılmadı.)

// DataTableHead — the full <thead><tr> built from the column defs: each
// sortable column is clickable (▲▼↕ glyph + aria-sort, matching the
// house .sortable/.sorted CSS) and every column gets a right-edge resize
// handle. `leading` slots in any non-data header cells (e.g. expand col);
// `trailing` slots them AFTER the managed columns — the caller owns that
// <th>, so a dropdown affordance (Explore's "+ Add column" manager) isn't
// clipped by the managed cells' overflow:hidden (v0.8.306). `renderLabel`
// lets a caller decorate a header label (e.g. LogTable's hover-×
// remove-column affordance) without touching the pure core's string
// label type.
export function DataTableHead<T>({ dt, leading, trailing, renderLabel, stickyLeftBase = 0 }: {
  dt: DataTable<T>;
  leading?: ReactNode;
  trailing?: ReactNode;
  renderLabel?: (c: DataTable<T>['columns'][number]) => ReactNode;
  // v0.9.1256 — leading (yönetilmeyen) hücrelerin toplam genişliği;
  // sola sabit zincir onların ardından başlar.
  stickyLeftBase?: number;
}) {
  // v0.10.939 (tablo standardı S8) — ⋯ menüsü SON görünür yönetilen başlıkta
  // (yeni hücre yok: kolon genişlikleri + yapışkan başlık aynen). `trailing`
  // verilmiş tabloda o hücre sayfanındır; menü ondan önceki son kolonda
  // durur (dilim 3'te eylem sütunları `kind: 'actions'`e göçünce menü
  // boş başlıklı eylem sütununa, yani satırın sağ ucuna oturur).
  const cols = dt.visibleColumns;
  const menuHostId = cols.some(c => c.kind !== 'actions') ? cols[cols.length - 1]?.id : undefined;
  return (
    // v0.9.928 — başlık da tablonun kimliğini taşıyor: bir kolona tıklayıp
    // sıralamak "bu tabloyla çalışıyorum" demektir ve j/k sahipliği o tıkla
    // buraya geçmeli. Kimlik `<thead>`de çünkü tıklar `<th>`ye, glife veya
    // resize tutamağına gelir; `closest()` üçünü de buraya toplar.
    <thead data-table-id={dt.storageKey}>
      <tr>
        {leading}
        {cols.map(c => {
          // v0.10.939 (tablo standardı T8) — eylem kolonu: etiket boş (ad
          // aria-label'da), sıralanmaz, boyutlanmaz, sağa yaslı.
          const actions = c.kind === 'actions';
          const sortable = !!c.sortValue && !actions;
          const active = dt.sort.id === c.id && !actions;
          const align = actions ? 'right' : c.align ?? (c.numeric ? 'right' : 'left');
          const menuHost = c.id === menuHostId;
          // v0.10.939 (tablo standardı T8) — eylem kolonu `col-actions` (td ile aynı).
          const cls = [c.numeric && !actions ? 'num' : '', sortable ? 'sortable' : '', active ? 'sorted' : '', c.stickyRight ? 'sticky-right' : '', c.stickyLeft ? 'sticky-left' : '', actions ? 'col-actions' : '', menuHost ? 'dt-menu-host' : '',
            // Kayıtlı genişlik varken ⋯ hep görünür: sıfırlanacak bir şey var ve
            // taşan tabloda son başlık kaydırılmış olabilir (eski görünür düğmenin işi).
            menuHost && Object.keys(dt.colWidths).length > 0 ? 'dt-menu-host--dirty' : '']
            .filter(Boolean).join(' ');
          // v0.10.939 (tablo standardı S8) — ⋯ taşıyan başlığın adı etiketi:
          // yoksa columnheader adı içerikten hesaplanır ve tetiğin
          // aria-label'ı eklenir ("N Tablo seçenekleri"). Tetik kendi adıyla
          // ayrı bir düğme olarak kalır.
          const thLabel = actions
            ? (c.label || 'Eylemler')
            : menuHost && typeof c.label === 'string' ? (c.label || 'Eylemler') : undefined;
          // v0.9.1256 — sola sabit başlıkların kümülatif left'i (saf
          // çekirdek; resize edilmiş genişlik anında yansır).
          const leftOff = c.stickyLeft
            ? stickyLeftOffsets(dt.visibleColumns, dt.colWidths, DEFAULT_W)[c.id]
            : undefined;
          return (
            <th key={c.id}
                className={cls || undefined}
                aria-label={thLabel}
                onClick={sortable ? () => dt.toggleSort(c.id) : undefined}
                // v0.10.249 — klavye: sıralanabilir başlık odaklanır; Enter/Space
                // sıralar, Shift+←/→ 8 px daraltır/genişletir (audit §9).
                tabIndex={sortable ? 0 : undefined}
                onKeyDown={sortable ? (e) => headKeyDown(e, dt, c.id) : undefined}
                aria-sort={active ? (dt.sort.dir === 'asc' ? 'ascending' : 'descending') : (sortable ? 'none' : undefined)}
                style={{
                  left: leftOff,

                  // v0.9.697 — `position` ARTIK INLINE DEĞİL (globals.css:
                  // `thead th { position: relative }`).
                  //
                  // Operatör-bildirimi: "Kolonların ismi ilk satırın üzerine
                  // denk geliyor." Ölçüm (prod, scrollTop=0): wrap.top=206,
                  // th.top=294 — başlık 87px aşağıda ve 87px tam olarak
                  // --controls-h.
                  //
                  // Buradaki inline 'relative', `.table-wrap.is-fit thead th`
                  // kuralının `position: sticky`'sini eziyordu; ama aynı
                  // kuralın `top: var(--controls-h)`'si SATIR İÇİ DEĞİL, yani
                  // uygulanmaya devam ediyordu. relative + top:87px = başlığı
                  // 87px aşağı KAYDIR ve yerini akışta BOŞ BIRAK → satırlar
                  // yukarı çıkıyor, başlık üstlerine çiziliyor.
                  //
                  // Eski yorum mekanizmayı doğru anlatıp ("inline 'relative'
                  // would win over it") yalnız sticky-right'ı istisna tutmuştu;
                  // yapışkan BAŞLIĞI da ezdiği görülmemişti. Yapışkan bar
                  // olmayan sayfalarda --controls-h tanımsız (top:0) olduğu
                  // için kayma sıfırdı — kusur o yüzden seçici göründü.
                  //
                  // CSS'e taşımak üç varyantı da doğru çözüyor: taban
                  // `thead th` relative (resize tutamağının çapası), `.is-fit`
                  // ve `.sticky-right` daha yüksek özgüllükle sticky'ye
                  // çeviriyor — sticky de konumlanmış bir değer, tutamak yine
                  // çapalanıyor.
                  textAlign: align,
                  overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                  userSelect: 'none',
                }}>
              {/* v0.10.933 (tablo standardı T3) — sayısal (sağa yaslı) kolonda ok
                  etiketin SOLUNDA (boşluk sağa alınır): boştayken görünmez ok
                  yuvası solda kalır, görünen etiket sağa yaslı sayılarla aynı
                  hizada biter. `align` çözümü `numeric`i zaten sağa çeviriyor;
                  servicesTable gibi `align: 'right'` bildiren sayısal kolonlar da
                  kapsanır. Sayısal olmayan kolon değişmedi. */}
              {sortable && align === 'right' && (
                <span className="sort-arrow" style={{ marginLeft: 0, marginRight: 4 }}>{active ? (dt.sort.dir === 'desc' ? '▼' : '▲') : '↕'}</span>
              )}
              {actions ? null : renderLabel ? renderLabel(c) : c.label}
              {sortable && align !== 'right' && (
                <span className="sort-arrow">{active ? (dt.sort.dir === 'desc' ? '▼' : '▲') : '↕'}</span>
              )}
              {/* MK3 (v0.9.919) — elle basılan kopya yerine PAYLAŞILAN
                  tutamak. v0.9.662'nin "çıkmaz sokaktan kurtuluş yolu"
                  (çift tık → resetLayout) `ColResizeHandle`e yazılmıştı
                  ama o bileşenin depoda SIFIR çağrı yeri vardı: her
                  DataTableHead tablosu burada kendi `<span>`ini basıyordu
                  ve o kopyada `onDoubleClick` YOKTU. Yani kurtuluş yolu
                  117 tablonun HİÇBİRİNE ulaşmıyordu — yazılmış,
                  test edilmiş, bağlanmamış (v0.9.660 sınıfı). Tek satır
                  delegasyon hepsini kapsıyor. */}
              {!actions && <ColResizeHandle dt={dt} colId={c.id} />}
              {menuHost && <DataTableHeadMenu dt={dt} />}
            </th>
          );
        })}
        {trailing}
      </tr>
    </thead>
  );
}

// headKeyDown — v0.10.249: başlık klavye sözleşmesi (jsdom testli).
export const HEAD_RESIZE_STEP_PX = 8;
export function headKeyDown<T>(e: { key: string; shiftKey: boolean; preventDefault: () => void; target?: EventTarget | null; currentTarget?: EventTarget | null }, dt: DataTable<T>, colId: string) {
  // v0.10.925 — başlık İÇİNDEKİ bir düğmenin (LogTable kolon-kaldır ×)
  // Enter/Boşluk'u kabarcıklanıp sıralamaya dönüyordu: klavye kolonu
  // kaldıramıyordu. Sıralama yalnız başlığın KENDİSİ odaktayken.
  if ((e.key === 'Enter' || e.key === ' ') && e.target !== e.currentTarget) return;
  if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault();
    dt.toggleSort(colId);
    return;
  }
  if (e.shiftKey && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
    e.preventDefault();
    dt.resizeBy(colId, e.key === 'ArrowRight' ? HEAD_RESIZE_STEP_PX : -HEAD_RESIZE_STEP_PX);
  }
}
