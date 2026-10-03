// dataTable — the pure, testable core of Coremetry's shared
// sortable + resizable table primitive (v0.7.53). The project
// principle: EVERY data table is column-sortable and
// column-resizable. The React glue lives in
// components/DataTable.tsx; the sort math + click semantics live
// here so they're unit-tested in isolation (CLAUDE.md #11), the
// same way tableColumns.ts backs the Traces reorder feature.

export type SortDir = 'asc' | 'desc';

// DataTableColumn describes one column for the shared primitive.
// A column with no `sortValue` is not clickable-to-sort (e.g. an
// expand-chevron or an actions column); it still participates in
// the fixed layout + resize.
export interface DataTableColumn<T> {
  id: string;
  label: string;
  // Accessor used for client-side sorting. Omit → column isn't sortable.
  sortValue?: (row: T) => number | string | null | undefined;
  // Direction applied when this column is FIRST clicked. Defaults to
  // 'desc' (numeric tables want biggest-first); set 'asc' for names.
  naturalDir?: SortDir;
  align?: 'left' | 'right';
  // Apply the .num class (right-align + tabular-nums). Implies right
  // align unless `align` overrides.
  numeric?: boolean;
  // Default column width (px) for the fixed-layout colgroup + the
  // resize starting point.
  width?: number;
  // Resize floor (px).
  minWidth?: number;
  // Sortable-only dimension that is NOT rendered as a header/column —
  // e.g. a composite "impact" score a preset button sorts by. Excluded
  // from <DataTableHead>/<DataTableColgroup>; still resolvable by
  // sortRows + setSort. Keeps body cells aligned to visible headers.
  headerHidden?: boolean;
  // Pin this column to the RIGHT edge of the scroll container
  // (v0.8.573 — Endpoints "Traces"). Wide tables overflow .table-wrap's
  // horizontal scroll and the scrollbar sits below the rows, so a
  // trailing action column is effectively invisible on laptop widths.
  // DataTableHead emits the .sticky-right th; the caller must mirror
  // the class on the matching body <td> (and give it an OPAQUE
  // background when the row carries an inline tint — sticky cells
  // float over scrolled content). Only meaningful on the LAST visible
  // column: multiple pinned columns would need per-column right
  // offsets, which nothing needs yet.
  stickyRight?: boolean;
  // v0.9.1256 (operatör: /endpoints + /databases "yatayda kayma —
  // kimlik kayboluyor") — SOL sabitleme. Yalnız İLK N ardışık görünür
  // kolonda anlamlı; kümülatif left ofsetleri stickyLeftOffsets saf
  // çekirdeğinden gelir (resize'lı genişliklerle canlı). is-fit
  // (kaydırmayan) kaplarda görsel etkisi yok — zaten kaymaz.
  stickyLeft?: boolean;
  // flex — v0.9.542. Bu kolon ARTAN genişliği emer; diğerleri kendi
  // genişliğinde kalır.
  //
  // Neden gerekti (operatör: "boşluklar güzel durmuyor, fit olsun"):
  // table-layout:fixed, kolon genişlikleri toplamı tablodan darsa
  // artanı TÜM kolonlara orantılı dağıtır. Geniş ekranda bu, her
  // kolonun arasına birkaç on piksel boşluk serpiyor ve tablo
  // "dağılmış" görünüyor — v0.9.501'de Trend kolonunda yaşanan sınıfın
  // aynısı. Tek bir kolon esner ve artan oraya giderse düzen kasıtlı
  // görünür.
  //
  // Emici kolon içerik olarak EN ÇOK yer isteyen olmalı (Traces'te
  // Operation). Operatör o kolonu elle sürüklerse kalıcı genişlik
  // kazanır ve esneklik biter — beklenen davranış, sürüklenen genişlik
  // her zaman kazanır.
  flex?: boolean;
  // mobileHide — v0.9.988 (dar ekran denetimi D6). Telefon
  // genişliğinde (<640px) bu kolon düşer.
  //
  // OPT-IN ve VARSAYILAN false. 68 tablo bu primitifi besliyor; bir
  // "akıllı" varsayılan (ör. "numerik olmayan üçüncü kolondan sonrası
  // düşsün") masaüstünde kimsenin istemediği kolonları kaybettirirdi.
  // Kolonun düşüp düşmeyeceği bir ÜRÜN kararıdır, tabloyu tanıyan
  // sayfa verir.
  //
  // KAPSAM SINIRI — bu bayrak `<colgroup>` ve `<thead>`i kapsar
  // (DataTableColgroup/DataTableHead `visibleColumns`tan okur). GÖVDE
  // hücrelerini sayfa basıyor: bir kolonu işaretleyen sayfa kendi
  // `<td>`sini de `dt.visibleColumns`/`dt.narrow` üzerinden atlamak
  // ZORUNDA, yoksa hücreler bir kolon kayar. Bugün hiçbir kolon
  // işaretli değil, yani bu sürümde davranış değişikliği YOK — mekanizma
  // kuruldu, ilk tüketici (log tablosunun kolon alt-kümesi, D5.7'nin
  // ikinci yarısı) kendi sürümünde gelecek.
  mobileHide?: boolean;
  // priority — v0.10.1068 (operatör: "Kolonlar kayıyor, sığmıyor; sayfa
  // responsive değil"). Kap, kolonların okunurluk TABANLARINI bile
  // taşıyamadığında hangi kolonun önce düşeceği: 1 = asla gizlenmez,
  // büyük sayı = önce gizlenir; eşitlikte SAĞDAKİ önce gider. Verilmezse
  // ilk görünür kolon, esneyen (flex) kolon ve eylem kolonu 1, diğerleri 2
  // (fitColumnWidths → defaultPriority). Gizlenen kolon başlıkta "+N sütun"
  // olur ve operatör oradan geri açabilir (aynı storageKey'de kalıcı).
  // Rehber: kimlik/metin ve durum/öncelik 1; sayılar 2; zamanlar ve
  // sahip/atanan 3.
  priority?: number;
}

// visibleColumns — `<colgroup>`/`<thead>`de GERÇEKTEN çizilen kolonlar.
//
// İki süzgeç tek yerde: `headerHidden` (sıralanabilir ama çizilmeyen
// bileşik boyut) ve `mobileHide` (yalnız dar ekranda düşen kolon).
// Saf ve tablo-güdümlü test edilebilir olması için burada; React
// yapıştırıcısı components/DataTable.tsx'te.
//
// Genişlik-kalıcılığı imzasına (columnLayoutSig) BİLEREK girmiyor:
// imza "beyan edilen GENİŞLİK niyeti"ni damgalıyor ve bir kolonun dar
// ekranda düşmesi onun masaüstü genişliğini değiştirmiyor. İmzaya
// eklemek, hiçbir kolon işaretli olmasa bile her tablonun sürüklenmiş
// genişliklerini bu sürümde bir kez atardı — bedava regresyon.
export function visibleColumns<T>(
  columns: DataTableColumn<T>[],
  narrow: boolean,
): DataTableColumn<T>[] {
  return columns.filter(c => !c.headerHidden && !(narrow && c.mobileHide));
}

export interface SortState {
  id: string | null;
  dir: SortDir;
}

// nextSort encodes the click semantics shared by every Coremetry
// table (matches the long-standing Services.tsx toggleSort): clicking
// the active column flips direction; clicking a new column selects it
// at its natural starting direction.
export function nextSort<T>(cur: SortState, col: DataTableColumn<T>): SortState {
  if (cur.id === col.id) {
    return { id: col.id, dir: cur.dir === 'desc' ? 'asc' : 'desc' };
  }
  return { id: col.id, dir: col.naturalDir ?? 'desc' };
}

// compareValues — type-aware comparator for two PRESENT (non-null)
// values, returning the pre-direction ordering. Numbers compare
// numerically; everything else via locale string compare. Null handling
// lives in sortRows (nulls are direction-independent — always last).
function compareValues(a: number | string, b: number | string): number {
  if (typeof a === 'number' && typeof b === 'number') return a - b;
  return String(a).localeCompare(String(b));
}

// sortRows — STABLE client-side sort by a column's accessor, returning
// a NEW array (never mutates input). Two invariants:
//  • Stability — re-sorting by a column with many ties (e.g. all the
//    same db_system) preserves the server's original order within each
//    group (tiebreak on original index).
//  • Nulls last — null/undefined accessor values sink to the BOTTOM
//    regardless of direction (missing data shouldn't jump to the top
//    when sorting ascending; matches the SLO list's intent).
// Returns the input unchanged when the column is absent or non-sortable.
export function sortRows<T>(
  rows: T[],
  col: DataTableColumn<T> | undefined,
  dir: SortDir,
): T[] {
  if (!col || !col.sortValue) return rows;
  const acc = col.sortValue;
  const mul = dir === 'asc' ? 1 : -1;
  return rows
    .map((r, i) => ({ r, i, v: acc(r) }))
    .sort((x, y) => {
      const a = x.v;
      const b = y.v;
      if (a == null && b == null) return x.i - y.i;
      if (a == null) return 1;  // x is null → sinks below y
      if (b == null) return -1; // y is null → x stays above
      const c = compareValues(a, b) * mul;
      return c !== 0 ? c : x.i - y.i; // stable tiebreak on original index
    })
    .map(x => x.r);
}

// ---------------------------------------------------------------------------
// v0.8.251 — serverSort support. The hook (components/DataTable.tsx) gained
// an optional serverSort mode for server-paged tables (Services first): the
// sort STATE machinery — URL `s_<storageKey>` param, localStorage
// persistence, header click semantics — is shared with client mode, but the
// ordering itself is the backend's ORDER BY. The pure halves live below so
// both modes stay unit-tested in the node vitest harness; nextSort/sortRows
// above are untouched (contract unchanged) — mode selection COMPOSES them.

// parseSortParam — decode the URL sort param "<colId>.<dir>" → SortState.
// Returns null for a missing / malformed value so the caller falls back to
// localStorage. colId may itself contain dots, so split on the LAST one.
// (Moved here from components/DataTable.tsx in v0.8.251 so the URL codec
// round-trip is pinned by unit tests.)
export function parseSortParam(s: string | null): SortState | null {
  if (!s) return null;
  const i = s.lastIndexOf('.');
  if (i <= 0) return null;
  const dir = s.slice(i + 1);
  if (dir !== 'asc' && dir !== 'desc') return null;
  return { id: s.slice(0, i), dir: dir as SortDir };
}

// sortIdKnown — bu TABLO böyle bir sıralanabilir kolon tanıdı mı?
// (v0.10.831, operatör-bildirimli /traces incelemesinin yan bulgusu)
//
// parseSortParam bir KODEK: "<id>.<yön>" biçimini doğrular, kimliğin
// ANLAMINI değil. Bayat ya da elle yazılmış bir `?s_<key>=` (eski bir
// yapıdan kalan 'startTime', silinmiş bir kolon, başka bir tablonun
// kimliği) böylece tablonun sıralama DURUMU oluyordu ve iki yerde sessizce
// yalan söylüyordu:
//   · hiçbir başlık aktif görünmüyor — operatör listenin neye göre sıralı
//     olduğunu göremiyor (aria-sort hiçbir th'de 'ascending'/'descending'
//     değil),
//   · sunucu-sıralı sayfalarda (Traces listesi) sayfanın dt.sort → sunucu
//     çevirici efekti tanımadığı kimlikte erken dönüyor, yani paylaşılan
//     link bir sıralama VAAT EDİYOR ama sunucu kendi varsayılanıyla çekiyor.
//
// `knownIds` verilmezse doğrulama YAPILMAZ (çağıranların çoğu kolon
// kümesini bilmez; sözleşme geriye dönük aynı). `null` kimlik = "sıralama
// yok" ve her zaman geçerli.
export function sortIdKnown(id: string | null, knownIds?: readonly string[]): boolean {
  if (!knownIds) return true;
  if (id === null) return true;
  return knownIds.includes(id);
}

// formatSortParam — SortState → "<colId>.<dir>" URL value; null when no
// column is active (the hook deletes the param instead of writing it).
// Inverse of parseSortParam for every non-empty id, including ids that
// themselves contain dots.
export function formatSortParam(s: SortState): string | null {
  return s.id ? `${s.id}.${s.dir}` : null;
}

// resolveToggle — the pure half of the hook's toggleSort: look up the
// clicked column and produce the next sort state, or null when the column
// is unknown / not sortable (the hook no-ops and onSortChange never fires).
// Mode-independent: in serverSort mode this exact value is what the hook
// reports via onSortChange / the returned `sort`, so the page re-fetches
// with the new ORDER BY.
export function resolveToggle<T>(
  columns: DataTableColumn<T>[],
  cur: SortState,
  id: string,
): SortState | null {
  const col = columns.find(c => c.id === id);
  if (!col || !col.sortValue) return null;
  return nextSort(cur, col);
}

// computeSortedRows — the row pipeline behind the hook's sortedRows memo.
// serverSort=true returns `rows` VERBATIM (reference-equal): the backend
// already applied its ORDER BY, and any client-side reorder — or even a
// defensive copy — would contradict the server page and defeat memoized
// children. Client mode is the existing sortRows path, contract unchanged.
export function computeSortedRows<T>(
  rows: T[],
  columns: DataTableColumn<T>[],
  sort: SortState,
  serverSort: boolean,
): T[] {
  if (serverSort) return rows;
  const col = sort.id ? columns.find(c => c.id === sort.id) : undefined;
  return sortRows(rows, col, sort.dir);
}

// columnLayoutSig — kalıcı kolon genişliklerinin HANGİ kolon tanımına
// karşı yakalandığını damgalar (v0.9.695).
//
// Operatör-bildirimi: "Manage users sayfasında user tablosunun kolonları,
// ilk header kullanıcının üzerinde çıkıyor."
//
// KÖK NEDEN — v0.9.660'ın kendi düzeltmesi operatöre HİÇ ULAŞMADI.
// O sürümde Users kolonları yeniden tanımlandı (team: flex → sabit 145,
// email tek emici). Ama DataTableColgroup şunu yapıyor:
//
//     const w = dt.colWidths[c.id] ?? (c.flex ? 'auto' : c.width ?? …)
//
// `colWidths` localStorage'dan (`dt.<key>.widths`) geliyor ve SÜRÜMSÜZDÜ.
// Kolonları bir kez sürüklemiş bir tarayıcıda ESKİ harita yeni tanımı
// tamamen eziyor: düzeltilmiş bütçe hiç uygulanmıyor, tablo eskisi gibi
// bozuk kalıyor. Kod doğru, ekran yanlış — ve `git log` düzeltmenin
// gemide olduğunu söylediği için bu tuzak sessiz.
//
// v0.9.660 bu katmanı BİLİYORDU ("Users tablosunun kaymasında bu ikinci
// katmandı") ama yalnız elle basılan bir Reset butonu ekledi. Kaçış
// kapısı, düzeltme değil: operatörün önce butonu keşfetmesi gerekiyor.
//
// İMZA NEYİ KAPSIYOR: id + beyan edilen genişlik niyeti (width/flex/
// minWidth) + headerHidden. Kolon EKLENMESİ/ÇIKMASI kadar bir kolonun
// genişlik niyetinin DEĞİŞMESİ de imzayı değiştiriyor — operatörün
// vakasında değişen tam olarak buydu, id kümesi aynı kalmıştı.
//
// TAKAS, açıkça: beyan edilen bir genişliği değiştirdiğimde o tablonun
// sürüklenmiş genişlikleri sıfırlanır. İSTENEN bu — bir genişliği
// elle değiştirmemin nedeni neredeyse her zaman düzenin taşmasıdır ve
// tam o durumda bayat sürükleme GİTMELİ. Kalıcı düzen yalnız yakalandığı
// kolon kümesine göre anlamlıdır.
export function columnLayoutSig<T>(columns: DataTableColumn<T>[]): string {
  // FNV-1a — cache anahtarlarındaki ile aynı aile (uzunluk değil, içerik).
  let h = 0x811c9dc5;
  for (const c of columns) {
    const part = `${c.id}:${c.width ?? ''}:${c.flex ? 'f' : ''}:${c.minWidth ?? ''}:${c.headerHidden ? 'h' : ''};`;
    for (let i = 0; i < part.length; i++) {
      h ^= part.charCodeAt(i);
      h = Math.imul(h, 0x01000193);
    }
  }
  return (h >>> 0).toString(36);
}

// PersistedLayout — diskteki şekil. `sig` uyuşmazsa genişlikler ATILIR.
//
// ESKİ ŞEKİL (çıplak Record<string, number>) bilinçli olarak atılıyor:
// tam da onu geçersiz kılmak için bu imza yazıldı, taşımak amacı
// bozardı (CLAUDE.md: kaldırırken geriye-uyum şimi ekleme).
export interface PersistedLayout {
  sig: string;
  widths: Record<string, number>;
  // v0.10.1068 — operatörün "+N sütun"dan GERİ AÇTIĞI kolonlar: dar kapta da
  // otomatik gizlenmez. Aynı anahtarda, aynı imzaya mühürlü (kolon tanımı
  // değişince genişliklerle birlikte düşer).
  shown?: string[];
}

export function readPersistedWidths(
  stored: unknown,
  sig: string,
): Record<string, number> {
  if (!stored || typeof stored !== 'object') return {};
  const p = stored as Partial<PersistedLayout>;
  if (typeof p.sig !== 'string' || p.sig !== sig) return {};
  if (!p.widths || typeof p.widths !== 'object') return {};
  return p.widths as Record<string, number>;
}

export function readPersistedShown(stored: unknown, sig: string): string[] {
  if (!stored || typeof stored !== 'object') return [];
  const p = stored as Partial<PersistedLayout>;
  if (typeof p.sig !== 'string' || p.sig !== sig) return [];
  return Array.isArray(p.shown) ? p.shown.filter((s): s is string => typeof s === 'string') : [];
}

// ── fitColumnWidths — v0.9.1030 ─────────────────────────────────────────
//
// Operator-reported: /inbox'ta Assignee tarafında tablo "bozuluyor",
// sayfa iframe gibi görünüyor. Kök: `.table-wrap.is-fit` masaüstünde
// `overflow: visible` (D2.1'in yapışkan-başlık kazancı) ve
// `table-layout:fixed` bir tablo, kolon genişliklerinin TOPLAMI
// `width:100%`ü aşarsa yine de büyür. Sürüklenen genişlikler px olarak
// kalıcı olduğundan geniş monitörde kaydedilen düzen dar laptopta
// toplam > kap eder; taşma is-fit kabında değil `#content`te kaydırma
// üretir — sol kenar kırpılır, sayfa "iframe" gibi iç-kaydırmalı olur
// (v0.9.660 Users taşmasının yapısal hâli; o gün gelen çare yalnız
// reset butonuydu).
//
// Sözleşme (v0.10.1068'te yeniden yazıldı — aşağıdaki "BÜTÇELİ SIĞDIRMA"):
// beyan+kalıcı px genişlikler kabı AŞAMAZ; yatay taşma yalnız öncelik-1
// kolonların tabanları bile sığmadığında kalır. Ölçüm yoksa (containerPx ≤
// 0: jsdom, ilk mount) null = fail-open.
//
// ── BÜTÇELİ SIĞDIRMA — v0.10.1068 ───────────────────────────────────────
//
// Operatör (prod, ~1440px laptop, sidebar açık, Exceptions): "Kolonlar
// kayıyor, sığmıyor; sayfa responsive değil ve bu hemen hemen her tabloda
// böyle. Kötü bir deneyim." Üç ayrı kusur birleşiyordu:
//   1. Taban 48px'ti (beyan edilmemiş minWidth): 168px'lik zaman damgası
//      48'e kadar ezilebiliyordu — "sığdı" ama okunmuyordu. Esneyen metin
//      kolonu (Exception) da 48'le yetiniyordu.
//   2. v0.10.1057 sürüklenen (pinned) kolonu HİÇ küçültmüyordu: bir kez
//      geniş ekranda sürüklenmiş genişlik dar laptopta tabloyu taşırıyordu.
//   3. Tabanlar sığmayınca tek çare yatay kaydırmaydı; son kolon kartın
//      kenarında "As…" diye kesiliyordu.
//
// Sıra (her adım bir öncekine yetmezse):
//   a. Beyan + kalıcı genişlikler sığıyorsa dokunulmaz; esneyen kolon
//      ('auto') artanı emer.
//   b. Sürüklenmemiş kolonlar oransal küçülür (taban kilitli waterfall);
//      esneyen kolon en az tabanını korur.
//   c. Sürüklenmiş (pinned) kolonlar da oransal küçülür — v0.10.1057'nin
//      REVİZYONU: sürükleme SIĞDIĞI sürece aynen kazanır, taşırmadan önce
//      geri çekilir.
//   d. Tabanlar bile sığmıyorsa en büyük `priority`li kolon (eşitlikte en
//      sağdaki) gizlenir; tekrarlanır. Öncelik-1 ve operatörün geri açtığı
//      (forceShow) kolon gizlenmez. Gizleme varsa son görünür kolona
//      "+N sütun" tetiği için `reservePx` eklenir.
//   e. Kalan öncelik-1 tabanlar bile sığmıyorsa herkes tabanda, kap kaydırır.

export interface FitColumnInput {
  id: string;
  /** Beyan/kalıcı px; sürüklenmemiş esneyen kolon için null ('auto'). */
  px: number | null;
  /** Okunurluk tabanı — `fitFloor` (minWidth ya da beyanın ~%60'ı). */
  min: number;
  /** v0.10.1057 — operatörün SÜRÜKLEDİĞİ kolon (kalıcı genişlik). v0.10.1068:
   *  sığdığı sürece px'i aynen; sığmazsa sürüklenmemişlerden SONRA küçülür. */
  pinned?: boolean;
  /** v0.10.1068 — 1 = asla gizlenmez; büyük = önce gizlenir. Verilmezse 1. */
  priority?: number;
  /** v0.10.1068 — operatör "+N sütun"dan geri açtı: otomatik gizlenmez. */
  forceShow?: boolean;
  /** v0.10.1068 — `flex` beyan etmemiş tablonun seçilmiş metin kolonu
   *  (pickFlexColumn): küme sığmadığında 'auto' olur (sonuçta `auto`). */
  flex?: boolean;
}

export interface FitResult {
  /** Değişen kolonların px'i; listede olmayan beyan/kalıcı genişliğinde (ya da 'auto') kalır. */
  widths: Record<string, number>;
  /** Kaba sığmadığı için gizlenen kolonlar (gizlenme sırasıyla). */
  hidden: string[];
  /** Beyanı px olduğu hâlde 'auto' çizilecek (artanı/açığı emen) kolon. */
  auto: string | null;
}

// fitFloor — sığdırmanın OKUNURLUK tabanı (resize'ın sürükleme tabanı değil:
// operatör elle 48'e kadar daraltabilir). minWidth beyan edilmişse o; yoksa
// esneyen kolon için 160, diğerleri için beyanın %60'ı (en az 48, en çok
// beyanın kendisi). v0.10.1068 öncesi taban düz 48'di.
export const FIT_MIN_PX = 48;
export const FLEX_FLOOR_PX = 160;
export function fitFloor(c: { width?: number; minWidth?: number; flex?: boolean }, defaultW: number): number {
  if (c.minWidth != null) return c.minWidth;
  if (c.flex) return FLEX_FLOOR_PX;
  const w = c.width ?? defaultW;
  return Math.min(w, Math.max(FIT_MIN_PX, Math.round(w * 0.6)));
}

// pickFlexColumn — esneyen kolon. Beyan edilmiş `flex` yoksa (v0.10.1068) en
// geniş METİN kolonu (sayısal / eylem / headerHidden değil) seçilir: artan
// genişlik oraya gider ve tablo daralınca o kolon tabanını korur. Hiç aday
// yoksa null (table-layout:fixed artanı orantılı dağıtır — eski davranış).
export function pickFlexColumn<T>(
  cols: (DataTableColumn<T> & { kind?: string })[],
  defaultW: number,
): string | null {
  const declared = cols.find(c => c.flex);
  if (declared) return declared.id;
  let best: string | null = null;
  let bestW = -1;
  for (const c of cols) {
    if (c.numeric || c.kind === 'actions' || c.headerHidden) continue;
    const w = c.width ?? defaultW;
    if (w > bestW) { best = c.id; bestW = w; }
  }
  return best;
}

// defaultPriority — `priority` verilmemiş kolonun önceliği: ilk görünür kolon
// (kimlik), esneyen kolon ve eylem kolonu 1 (asla gizlenmez), diğerleri 2.
export function defaultPriority(
  c: { priority?: number; kind?: string },
  isFirst: boolean,
  isFlex: boolean,
): number {
  if (c.priority != null) return c.priority;
  if (isFirst || isFlex || c.kind === 'actions') return 1;
  return 2;
}

// waterfall — oransal küçültme, taban kilitlemeli: tabana çarpan kolon
// kilitlenir, kalan pay kilitsizlere yeniden oranlanır. `target` ≥ Σmin
// varsayılır (çağıran garanti eder).
function waterfall(cols: { id: string; px: number; min: number }[], target: number, out: Record<string, number>) {
  const locked = new Set<string>();
  for (;;) {
    const freePx = cols.reduce((s, c) => s + (locked.has(c.id) ? 0 : c.px), 0);
    const room = target - cols.reduce((s, c) => s + (locked.has(c.id) ? c.min : 0), 0);
    const f = freePx > 0 ? room / freePx : 0;
    let relocked = false;
    for (const c of cols) {
      if (locked.has(c.id)) continue;
      if (c.px * f < c.min) { locked.add(c.id); relocked = true; }
    }
    if (!relocked) {
      for (const c of cols) out[c.id] = locked.has(c.id) ? c.min : Math.min(c.px, Math.floor(c.px * f));
      return;
    }
  }
}

export function fitColumnWidths(
  cols: FitColumnInput[],
  fixedExtraPx: number,
  containerPx: number,
  // "+N sütun" tetiğinin payı: yalnız bir kolon gizlendiğinde ayrılır ve son
  // görünür kolona eklenir (tetik o başlığın sağ ucuna biner).
  reservePx = 0,
): FitResult | null {
  if (!(containerPx > 0)) return null;
  const budget = containerPx - fixedExtraPx;
  // a — beyan (+ kalıcı) küme sığıyorsa dokunma.
  if (cols.reduce((s, c) => s + (c.px ?? c.min), 0) <= budget) {
    return { widths: splitAutos(cols, {}, budget), hidden: [], auto: null };
  }
  // Sığmıyor: seçilmiş metin kolonu (`flex`, sürüklenmemiş) 'auto' olur —
  // açığı önce O tabanına kadar emer, sonra diğerleri küçülür. Eşikte
  // süreklidir: 'auto' kolon tam beyanına eşit genişlik alır.
  let auto: string | null = null;
  const work = cols.map(c => {
    if (c.flex && !c.pinned && c.px != null && auto == null) { auto = c.id; return { ...c, px: null }; }
    return c;
  });
  // d — gizleme: tabanlar (+ gizleme varsa tetik payı) bütçeye sığana dek.
  const shown = [...work];
  const hidden: string[] = [];
  const need = () => shown.reduce((s, c) => s + c.min, 0) + (hidden.length ? reservePx : 0);
  while (need() > budget) {
    let pick = -1;
    for (let i = 0; i < shown.length; i++) {
      const c = shown[i];
      const p = c.priority ?? 1;
      if (p <= 1 || c.forceShow) continue;
      if (pick < 0 || p >= (shown[pick].priority ?? 1)) pick = i; // eşitlikte sağdaki
    }
    if (pick < 0) break; // e — gizlenecek aday yok: tabanlarda taşar
    hidden.push(shown[pick].id);
    shown.splice(pick, 1);
  }
  const out: Record<string, number> = {};
  const reserve = hidden.length ? reservePx : 0;
  // 'auto' (sürüklenmemiş esneyen) kolon kalan alanı alır ama tabanını ister.
  const autoMin = shown.reduce((s, c) => s + (c.px == null ? c.min : 0), 0);
  const avail = budget - autoMin - reserve;
  const fixed = shown.filter(c => c.px != null) as (FitColumnInput & { px: number })[];
  const sum = fixed.reduce((s, c) => s + c.px, 0);
  if (sum > avail) {
    const free = fixed.filter(c => !c.pinned);
    const pinned = fixed.filter(c => c.pinned);
    const pinnedPx = pinned.reduce((s, c) => s + c.px, 0);
    const freeMin = free.reduce((s, c) => s + c.min, 0);
    if (freeMin <= avail - pinnedPx) {
      // b — sürüklenmemişler küçülür, sürüklenenler aynen.
      waterfall(free, avail - pinnedPx, out);
    } else {
      // c — sürüklenmemişler tabanda; sürüklenenler kalan alana küçülür.
      for (const c of free) out[c.id] = c.min;
      const room = avail - freeMin;
      const pinnedMin = pinned.reduce((s, c) => s + Math.min(c.min, c.px), 0);
      if (pinnedMin <= room) waterfall(pinned.map(c => ({ ...c, min: Math.min(c.min, c.px) })), room, out);
      else for (const c of pinned) out[c.id] = Math.min(c.min, c.px); // e
    }
  }
  if (reserve > 0) {
    const last = shown[shown.length - 1];
    if (last && last.px != null) out[last.id] = (out[last.id] ?? last.px) + reserve;
  }
  return { widths: splitAutos(shown, out, budget), hidden, auto };
}

// splitAutos — birden çok 'auto' kolon (ör. Clusters pod tablosunda Namespace +
// Pod) varsa tarayıcı kalan alanı onlara EŞİT böler ve büyük tabanlı kolon
// (Pod 180) tabanının altına düşer (ölçüm: 146 px). Kalan alan tabanlarla
// orantılı paylaştırılır; sonuncusu 'auto' kalır ve yuvarlama artığını alır.
// Tek 'auto' kolonda dokunulmaz.
function splitAutos(shown: FitColumnInput[], out: Record<string, number>, budget: number): Record<string, number> {
  const autos = shown.filter(c => c.px == null && out[c.id] == null);
  if (autos.length < 2) return out;
  const used = shown.reduce((s, c) => s + (out[c.id] ?? c.px ?? 0), 0);
  const room = budget - used;
  const minSum = autos.reduce((s, c) => s + c.min, 0);
  for (const c of autos.slice(0, -1)) out[c.id] = room > minSum ? Math.floor(room * (c.min / minSum)) : c.min;
  return out;
}

// hiddenCellCss — gizlenen kolonların gövde/elle-başlık hücresini düşüren
// kapsamlı kural (saf; test edilir). `k`: 1-tabanlı hücre sırası, `n`: tam
// satırın hücre sayısı — `:nth-child(k):nth-last-child(n-k+1)` yalnız TAM
// sayılı satırda eşleşir.
export function hiddenCellCss(
  fitId: string, nLead: number, nCells: number,
  visibleIds: string[], hidden: ReadonlySet<string>,
): string {
  if (!hidden.size) return '';
  const scope = `table:has(> colgroup[data-dt-fit="${fitId}"])`;
  const sels: string[] = [];
  visibleIds.forEach((id, i) => {
    if (!hidden.has(id)) return;
    const k = nLead + i + 1;
    const nth = `:nth-child(${k}):nth-last-child(${nCells - k + 1})`;
    sels.push(`${scope} > tbody > tr > td${nth}`, `${scope} > thead > tr > th${nth}`);
  });
  return `${sels.join(',\n')} { display: none; }`;
}

// stickyLeftOffsets — v0.9.1256 saf çekirdek: görünür kolon listesi +
// efektif genişliklerden (resize edilmiş ?? beyan ?? varsayılan) sola
// sabit kolonların kümülatif left ofsetleri. stickyLeft olmayan ilk
// kolonda zincir KESİLİR: aradan sabitlenmemiş kolon atlayıp sonrakini
// sabitlemek, kaydırmada üst üste binen yüzer kolonlar üretir.
export function stickyLeftOffsets(
  cols: { id: string; stickyLeft?: boolean; width?: number }[],
  colWidths: Record<string, number>,
  defaultW = 120,
  // base — yönetilen kolonlardan ÖNCE duran leading hücrelerin (genişlet
  // oku vb.) toplam genişliği; onlar da sola sabitlenir ve zincir onların
  // ardından başlar (DependenciesTable 24px oku).
  base = 0,
): Record<string, number> {
  const out: Record<string, number> = {};
  let acc = base;
  for (const c of cols) {
    if (!c.stickyLeft) break;
    out[c.id] = acc;
    acc += colWidths[c.id] ?? c.width ?? defaultW;
  }
  return out;
}
