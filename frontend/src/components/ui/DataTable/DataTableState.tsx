import { useCallback, useLayoutEffect, useRef, type CSSProperties, type ReactNode, type RefObject } from 'react';
import type { DataTable } from './DataTable';
import { LinkButton } from '../LinkButton';
import { QueryErrorInline } from '@/components/QueryError';
import { Skeleton } from '@/components/Skeleton';
import { DATA_TABLE_STATE_TEXT } from './dataTableStateText';
// v0.10.939 (tablo standardı T12) — genişliği bilinmeyen kolonun colgroup'ta
// aldığı px; iskelet orantısı colgroup'u taklit ettiği için AYNI sabit
// (eskiden burada elle kopyası vardı — iki değer ayrışırsa iskelet kayar).
import { DEFAULT_W } from './rowHeight';

// DataTableState — v0.10.939 (tablo standardı T12): boş / eşleşme yok /
// yükleniyor / hata TABLONUN İÇİNDE, başlık yerinde kalır.
//
// Bugün 88 `.table-wrap` dosyasının 63'ü tablo boşken tabloyu bütünüyle bir
// `<Empty>` ile DEĞİŞTİRİYOR: her yoklamada düzen zıplıyor, hangi sütunların
// boş döndüğü görünmüyor ve ~63 Empty başlığı aslında bir HATA ("boş" ile
// "okunamadı" aynı kutuda). Karar (docs/DECISIONS.md "Tablo standardı" T12):
// durum tek satır, sütun başlıkları durur, üç ayrı metin.
//
//   <tbody>
//     {rows.length ? rows.map(renderRow)
//       : <DataTableState dt={dt} kind={data === undefined ? 'loading' : …} />}
//   </tbody>
//
// SÖZLEŞME (DataTableState.contract.test.tsx):
//   • HER tür TEK `<tr>` basar, içinde TEK `<td colSpan>`; colSpan =
//     leading + GÖRÜNÜR kolonlar (dt.visibleColumns: headerHidden ve dar
//     ekranda mobileHide düşmüş) + trailing — VirtualTable'ın boş satırıyla
//     aynı sayım (v0.10.249). Tek satır olması `tbody tr:last-child`,
//     j/k satır indeksleri ve aria-rowcount'u bozmaz.
//   • Satır tıklanmaz: role/data-row-action İŞARETİ YOK → imleç + hover yok
//     (T2). `data-dt-state` yalnız kanca (benimseme kapısı, dilim 4).
//   • `leading`/`trailing` DataTableColgroup'un px dizileriyle AYNI şekil:
//     sayfa colgroup'a verdiği diziyi buraya da verir, sayım kaymaz.
//
// Türler:
//   empty    "Bu aralıkta veri yok" — sorgu başarılı, pencere boş.
//   no-match "Eşleşme yok" + (onClearFilters verilirse) "Filtreleri temizle";
//            veri VAR, filtre hepsini eledi — kurtuluş yolu yanında.
//   loading  N iskelet çizgisi, her biri tam bir satır (--row-h, yoğunlukla
//            değişir); çizgi hücreleri kolon genişliklerine orantılı, sayısal
//            kolonda sağa yaslı. role=status + aria-busy (Spinner/LoaderMark
//            ile aynı ev deseni).
//   error    QueryErrorInline aynı yuvada — "bu bir hata, boş sonuç değil"
//            (QueryError v0.9.858 dürüstlüğü); onRetry verilirse ↻.
// Tek nötr ikon kuralı: empty/no-match ikonsuz (mockup), hata satırının tek
// ikonu QueryErrorInline'ın kendi ⚠'i.
//
// v0.10.939 (tablo standardı T12) — ODAK GERİ DÖNÜŞÜ. "Filtreleri temizle" ve
// "↻ Retry" kendi kendini kaldıran eylemler: filtre silinince satırlar gelir
// (durum satırı gider), yeniden deneme 'loading'e döner (düğme gider).
// Odaklı düğme DOM'dan kalkınca tarayıcı odağı <body>'ye atar ve klavye
// kullanıcısı sayfanın başına fırlar. `onClearFilters`/`onRetry` benimseyen
// sayfa odağı KENDİSİ yönetmez — bileşen geri verir:
//   • gövde (`.dt-state-body`) ayrı bir bileşen; useLayoutEffect temizliği,
//     React düğmeyi DOM'dan kaldırmadan ÖNCE çalışır. Odak gövdenin
//     içindeyse (yalnız o zaman) taşınır; başka yerdeyse DOKUNULMAZ.
//   • gövde `tür:eylem-var-mı` anahtarıyla bağlanır: no-match → empty gibi
//     bir geçişte gövde aynı <div> kalıp yalnız düğmesini kaybetseydi temizlik
//     hiç koşmazdı.
//   • hedef: `returnFocusRef` bağlı ve odak alabiliyorsa o (sayfanın filtre
//     kutusu gibi); değilse tablonun kabı (`.table-wrap` / `.vt-scroll`,
//     DataTableColgroup'un ölçtüğü aynı kaplar; yoksa `<table>`). Kap odak
//     alamıyorsa GEÇİCİ tabIndex=-1 alır ve odak çıkınca geri alınır.
//
// v0.10.967 — tablo standardı dilim 5 (recipe §5):
//   • P-1 `detail`: sayfanın CTA'sı / bağlantısı / kısa önerisi (Link,
//     Button, kopyalanan komut) empty / no-match / error satırında, türün
//     kendi içeriğinin (mesaj + "Filtreleri temizle" / "↻ Retry")
//     ARKASINDA, aynı `.dt-state-body` içinde. Dilim 4'te bu CTA'lar ne
//     silinebildi ne tablonun dışına park edilebildi; tablo "blocked" kaldı.
//     loading'de yok sayılır (iskelet eylem taşımaz). Detail KENDİ odak
//     bekçisi olan ayrı yuvada (`DetailSlot`, StateBody'nin aynısı); gövde
//     anahtarı yine yalnız `tür:eylem-var-mı`. Detail'in varlığı gövde
//     anahtarına girseydi detail'in gelip gitmesi (başka bir sorguya ya da
//     yetkiye bağlı CTA) gövdeyi bütünüyle yeniden bağlar, hâlâ çizilen
//     "↻ Retry" / "Filtreleri temizle"den odağı kaba ya da filtre kutusuna
//     kaçırırdı. Yuva giderken odak içindeyse odak aynı kuralla geri döner.
//     SINIR: yuva bağlı kalırken İÇİNDEKİ odaklı düğme kalkarsa bekçi bunu
//     göremez — sayfa detail'in eylem kümesini `detailKey`de söyler (değişince
//     yuva yeniden bağlanır), ya da düğmeyle birlikte detail'i düşürür.
//   • P-2 `colSpan`: statik tablo (`useDataTable` yok) `dt` YERİNE thead'deki
//     <th> sayısını verir. empty / no-match / error hücresi dt'li sürümle
//     birebir aynı (odak dönüşü dahil); loading kolon orantısı olmadığı için
//     N tam genişlik çizgi. `dt` ile `colSpan`dan TAM OLARAK biri — ikisi de
//     yoksa ya da ikisi birden verilirse derleme hatası (tip kapısı,
//     DataTableState.contract.test.tsx).

export type DataTableStateKind = 'empty' | 'no-match' | 'loading' | 'error';

/** Türü ve içeriği seçen ortak alanlar — dt'li ve statik sürümde aynı. */
interface DataTableStateBase {
  kind: DataTableStateKind;
  /**
   * Türün varsayılan satırını ezer (ör. admin tablosunda "Henüz kullanıcı
   * yok"; hata türünde sunucunun metni). loading'de durum etiketi olur.
   */
  message?: string;
  /**
   * v0.10.967 (P-1) — empty / no-match / error: türün kendi içeriğinin
   * arkasına, aynı hücrede basılan sayfa içeriği: CTA düğmesi, `<Link>`,
   * kısa öneri ya da kopyalanan komut. loading'de yok sayılır. `false` /
   * `null` / `''` → hiç basılmaz (`detail={cond && <Link/>}` güvenli).
   *
   * Odak: detail GİDERKEN (yanlış değere döner ya da tür değişir) odak
   * içindeyse `returnFocusRef`e / tablonun kabına döner; detail gelip
   * giderken türün kendi düğmesi ("↻ Retry", "Filtreleri temizle") odağını
   * korur. SINIR: detail yerinde kalırken içindeki odaklı düğme kalkarsa
   * (ör. "Pencereyi genişlet" gider, ipucu metni kalır) bu GÖRÜLMEZ ve odak
   * <body>'ye düşer — o zaman `detailKey` verin ya da düğmeyle birlikte
   * detail'i de düşürün.
   */
  detail?: ReactNode;
  /**
   * v0.10.967 (P-1) — detail'in EYLEM KÜMESİNİN kimliği (ör.
   * `canWiden ? 'widen' : 'hint'`). Değişince detail yuvası yeniden bağlanır;
   * odak yuvanın içindeyse `returnFocusRef`e / tablonun kabına döner.
   * Yalnız detail'in odak alabilen öğeleri değişince değiştirin: metin
   * değişimi için değil (yuva yeniden bağlanır, odaklı düğme odağını kaybeder).
   * Türün kendi düğmelerine dokunmaz.
   */
  detailKey?: string;
  /**
   * no-match: verilirse "Filtreleri temizle" LinkButton'u basılır. Düğme
   * kendini kaldırınca (satırlar geldi) odağı bileşen geri verir —
   * `returnFocusRef`e, yoksa tablonun kabına; sayfa odak YÖNETMEZ.
   */
  onClearFilters?: () => void;
  /**
   * error: verilirse QueryErrorInline "↻ Retry" basar. Yeniden deneme
   * 'loading'e dönüp düğmeyi kaldırırsa odak `onClearFilters` ile aynı
   * kuralla geri verilir.
   */
  onRetry?: () => void;
  /**
   * v0.10.939 (tablo standardı T12) — "Filtreleri temizle" / "↻ Retry"
   * kendini kaldırınca odağın ineceği sayfa hedefi (ör. filtre kutusu).
   * Bağlı değilse ya da odak alamıyorsa tablonun kabı (`.table-wrap` /
   * `.vt-scroll`, yoksa `<table>`).
   */
  returnFocusRef?: RefObject<HTMLElement | null>;
  /** loading: iskelet çizgisi sayısı (TableSkeleton varsayılanıyla aynı: 8). */
  skeletonRows?: number;
}

/**
 * `useDataTable` tablosu: colSpan ve iskelet orantısı `dt`den (+ leading /
 * trailing). Sayfaların hesapladığı durum tipi `Omit<DataTableStateProps<Row>, 'dt'>`
 * bu yüzden değişmedi.
 */
export interface DataTableStateProps<T> extends DataTableStateBase {
  dt: DataTable<T>;
  /** v0.10.967 (P-2) — dt varken colSpan verilemez (sayım dt'nin). */
  colSpan?: never;
  /** Yönetilmeyen baş kolonların px genişlikleri (DataTableColgroup `leading` ile aynı dizi). */
  leading?: number[];
  /** Yönetilmeyen son kolonların px genişlikleri (DataTableColgroup `trailing` ile aynı dizi). */
  trailing?: number[];
}

/**
 * v0.10.967 (P-2) — statik tablo (`dt` yok): `colSpan` = thead'deki <th>
 * sayısı, tek başına toplam (leading / trailing yok). Hesaplanan durum için
 * `Omit<DataTableStateStaticProps, 'colSpan'>`.
 */
export interface DataTableStateStaticProps extends DataTableStateBase {
  colSpan: number;
  dt?: never;
  leading?: never;
  trailing?: never;
}

/**
 * v0.10.939 (tablo standardı T12) — odağı sayfa hedefine, yoksa tablonun
 * kabına indir. `from` henüz DOM'da (temizlik kaldırmadan önce koşar).
 */
function landFocus(from: HTMLElement, page: HTMLElement | null | undefined) {
  if (page && page.isConnected) {
    page.focus();
    if (document.activeElement === page) return;
  }
  const wrap = from.closest<HTMLElement>('.table-wrap, .vt-scroll') ?? from.closest<HTMLElement>('table');
  if (!wrap) return;
  if (!wrap.hasAttribute('tabindex')) {
    // Geçici: kap bir odak durağı DEĞİL; odak çıkınca öznitelik geri alınır
    // ki sayfanın sekme sırası ve tık davranışı kalıcı değişmesin.
    wrap.tabIndex = -1;
    wrap.addEventListener('blur', () => wrap.removeAttribute('tabindex'), { once: true });
  }
  // Kullanıcı zaten tablonun önünde (eylemi oradan etkinleştirdi) — kaydırma yok.
  wrap.focus({ preventScroll: true });
}

/**
 * v0.10.939 (tablo standardı T12) — empty/no-match/error gövdesi. Silinirken
 * (düğmesiyle birlikte) odak içindeyse `onFocusOrphaned` çağrılır; temizlik
 * React DOM düğümlerini kaldırmadan ÖNCE koşar. `onFocusOrphaned` kararlı
 * olmalı (useCallback []) — yoksa gövde bağlıyken temizlik koşar.
 */
function StateBody({ children, onFocusOrphaned }: {
  children: ReactNode;
  onFocusOrphaned: (from: HTMLElement) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const body = ref.current;
    return () => {
      if (body && body.contains(document.activeElement)) onFocusOrphaned(body);
    };
  }, [onFocusOrphaned]);
  return <div ref={ref} className="dt-state-body">{children}</div>;
}

/**
 * v0.10.967 (P-1) — detail yuvası, StateBody ile aynı bekçi: yuva giderken
 * (detail yanlış değere döndü, `detailKey` değişti ya da gövde yeniden
 * bağlandı) odak içindeyse `onFocusOrphaned`. Ayrı bileşen olması gövdenin
 * anahtarını detail'den bağımsız tutar: detail gelip giderken türün kendi
 * düğmesi aynı DOM düğümü kalır, odağı kaçmaz.
 */
function DetailSlot({ children, onFocusOrphaned }: {
  children: ReactNode;
  onFocusOrphaned: (from: HTMLElement) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const slot = ref.current;
    return () => {
      if (slot && slot.contains(document.activeElement)) onFocusOrphaned(slot);
    };
  }, [onFocusOrphaned]);
  return <div ref={ref} data-dt-state-detail="">{children}</div>;
}

interface SkelCell { key: string; flex: string; num: boolean; bar: boolean; full?: boolean }

// v0.10.967 (P-2) — statik tablonun iskelet çizgisi: colgroup orantısı yok
// (kolon genişliği bilinmiyor), çizgi başına TEK tam genişlik hücre ve çubuk.
// Uydurma kolon bölmesi gerçek satır gelince kayardı; dürüst olan tam çizgi.
const STATIC_SKEL_LINE: SkelCell[] = [{ key: 'line', flex: '1 1 0px', num: false, bar: true, full: true }];

/**
 * İskelet çizgisinin hücreleri — `<colgroup>` genişliklerinin flex karşılığı.
 * Sabit kolon kendi px'inde; sürüklenmemiş flex kolon artanı emer. Hiç flex
 * kolon yoksa `table-layout:fixed` artanı genişlikle orantılı dağıtır — grow =
 * px aynı dağılımı verir. Yaklaşık (fitColumnWidths'i taklit etmez); amaç
 * gerçek satır geldiğinde gözün sütun hizasında kalması.
 */
function skeletonCells<T>(dt: DataTable<T>, leading: number[], trailing: number[]): SkelCell[] {
  const cols = dt.visibleColumns;
  const anyFlex = cols.some(c => c.flex && dt.colWidths[c.id] == null);
  const fixed = (prefix: string) => (w: number, i: number): SkelCell =>
    ({ key: `${prefix}-${i}`, flex: `0 0 ${w}px`, num: false, bar: false });
  return [
    ...leading.map(fixed('lead')),
    ...cols.map((c): SkelCell => {
      const px = dt.colWidths[c.id] ?? (c.flex ? null : c.width ?? DEFAULT_W);
      const align = c.align ?? (c.numeric ? 'right' : 'left');
      return {
        key: c.id,
        flex: px == null ? '1 1 0px' : `${anyFlex ? 0 : px} 1 ${px}px`,
        num: align === 'right',
        bar: true,
      };
    }),
    ...trailing.map(fixed('trail')),
  ];
}

/**
 * v0.10.967 (P-1) — detail basılacak mı: `cond && <Link/>` gibi yanlış
 * değerler (false / null / undefined / '') yuva basmaz (DOM detail'siz
 * sürümle birebir aynı kalır).
 */
function hasContent(node: ReactNode): boolean {
  return node !== undefined && node !== null && typeof node !== 'boolean' && node !== '';
}

export function DataTableState<T>(props: DataTableStateProps<T> | DataTableStateStaticProps) {
  const { kind, message, detail, detailKey, onClearFilters, onRetry, skeletonRows = 8, returnFocusRef } = props;
  const leading = props.leading ?? [];
  const trailing = props.trailing ?? [];
  // v0.10.967 (P-2) — dt varsa sayım dt'nin (görünür kolonlar); yoksa statik
  // tablonun verdiği colSpan. Tip ikisini birden yasaklar; JS'den ikisi
  // birden gelirse dt kazanır (colgroup'la aynı sayım).
  const span = props.dt
    ? leading.length + props.dt.visibleColumns.length + trailing.length
    : props.colSpan;

  // v0.10.939 (tablo standardı T12) — `returnFocusRef` bir ref'te: iniş
  // işleyicisi kararlı kalır (çağıran her render'da yeni ref nesnesi verse de).
  const returnRef = useRef(returnFocusRef);
  useLayoutEffect(() => { returnRef.current = returnFocusRef; }, [returnFocusRef]);
  const onFocusOrphaned = useCallback((from: HTMLElement) => landFocus(from, returnRef.current?.current), []);

  if (kind === 'loading') {
    const cells = props.dt ? skeletonCells(props.dt, leading, trailing) : STATIC_SKEL_LINE;
    return (
      <tr data-dt-state={kind}>
        <td colSpan={span} className="dt-state dt-state--loading">
          <div role="status" aria-busy="true" aria-label={message ?? DATA_TABLE_STATE_TEXT.loading}>
            {Array.from({ length: skeletonRows }, (_, ri) => (
              <div key={ri} className="dt-state-skel" aria-hidden="true">
                {cells.map((c, ci) => {
                  const style: CSSProperties = { flex: c.flex };
                  return (
                    <span key={c.key} style={style}
                      className={c.num ? 'dt-state-skel-cell dt-state-skel-cell--num' : 'dt-state-skel-cell'}>
                      {/* TableSkeleton'un genişlik ritmi: ilk veri kolonu kimlik
                          gibi geniş, diğerleri satırdan satıra değişen kısa çubuk. */}
                      {c.bar && (
                        <Skeleton height="0.8em"
                          width={c.full ? '100%'
                            : ci === leading.length ? `${50 + (ri % 3) * 10}%` : `${30 + (ri % 4) * 8}%`} />
                      )}
                    </span>
                  );
                })}
              </div>
            ))}
          </div>
        </td>
      </tr>
    );
  }

  // v0.10.939 (tablo standardı T12) — eylemin varlığı anahtarda: düğme
  // giderse gövde yeniden bağlanır ve temizliği odağı yakalar.
  const hasAction = (kind === 'no-match' && !!onClearFilters) || (kind === 'error' && !!onRetry);
  // v0.10.967 (P-1) — detail anahtarda DEĞİL: kendi bekçisi DetailSlot'ta
  // (başlıktaki gerekçe). Anahtara girerse detail'in gelip gitmesi hâlâ
  // çizilen Retry / "Filtreleri temizle"yi yeniden bağlar ve odağı kaçırır.
  const withDetail = hasContent(detail);
  return (
    <tr data-dt-state={kind}>
      <td colSpan={span} className="dt-state">
        <StateBody key={`${kind}:${hasAction ? 'action' : 'none'}`}
          onFocusOrphaned={onFocusOrphaned}>
          {kind === 'error' && (
            <QueryErrorInline text={message ?? DATA_TABLE_STATE_TEXT.error} onRetry={onRetry} />
          )}
          {kind === 'empty' && <span>{message ?? DATA_TABLE_STATE_TEXT.empty}</span>}
          {kind === 'no-match' && (
            <>
              <span>{message ?? DATA_TABLE_STATE_TEXT.noMatch}</span>
              {onClearFilters && (
                <>
                  <span aria-hidden="true">·</span>
                  <LinkButton onClick={onClearFilters}>{DATA_TABLE_STATE_TEXT.clearFilters}</LinkButton>
                </>
              )}
            </>
          )}
          {/* v0.10.967 (P-1) — sayfanın CTA'sı / bağlantısı türün kendi
              içeriğinin arkasında; tek sarmalayıcı: sayfanın satır içi cümlesi
              (metin + Link) tek flex öğesi kalır, gap'le parçalanmaz. <div>:
              kopyalanan komut gibi blok içerik de geçerli iç içelik.
              `detailKey` değişince yalnız bu yuva yeniden bağlanır. */}
          {withDetail && (
            <DetailSlot key={detailKey} onFocusOrphaned={onFocusOrphaned}>{detail}</DetailSlot>
          )}
        </StateBody>
      </td>
    </tr>
  );
}
