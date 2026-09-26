import { useEffect, useState, FormEvent } from 'react';
import { Button } from './ui/Button';
// v0.10.831 — konum metni saf çekirdekte (lib/pagerPosition.ts): bileşen
// modülünden export etmek uyarı tabanını büyütürdü, ve kural zaten sayfadan
// bağımsız.
import { pagePositionLabel } from '@/lib/pagerPosition';

// Pager — depodaki TEK "daha fazla satır nasıl gelir" yüzeyi.
//
// ——— SÖZLEŞME (v0.9.1014) ————————————————————————————————————
//
// v0.9.1013'e kadar atomun tek tüketicisi /traces'ti; diğer beş
// sayfalama yüzeyi elle çizilmişti ve her biri kendi kararını
// vermişti — konum (tablo altı / tablo üstü / ortada), vurgu (Next
// birincil mi ikincil mi), sayının ANLAMI (kesin mi tavanlı mı),
// commit anı (Enter mı blur mu). Beş yüzey, beş cevap.
//
// İki prop ZORUNLU ve ikisi de tip düzeyinde (Button.variant
// hilesinin aynısı — v0.9.1005): statik tarama tahmin eder, `tsc`
// ZORLAR.
//
//   mode  — 'offset' (← Prev / sayfa girdisi / Next →) ya da
//           'cursor' (↓ daha fazla yükle, satırlar BİRİKİR).
//           Bu bir görünüm tercihi değil bir VERİ modeli beyanı:
//           keyset imleçli bir yüzeyde "sayfa 7'ye git" ifade
//           edilemez, çünkü 7'nin imleci ancak 6 çekilerek bilinir.
//
//   count — `total` sayısının ne DEMEK olduğu. Depodaki en pahalı
//           sessiz yalan buradaydı: v0.9.288'de "of 10,000" ES'in
//           `track_total_hits` tavanıydı, gerçek sayı değil; v0.9.638'de
//           /traces total'ı Pager'a vermeyi BIRAKTI çünkü tavanlı bir
//           sayı operatörü listenin ULAŞAMAYACAĞI sayfalara yolluyordu.
//           Artık sayının anlamını beyan etmek zorunlu:
//             'exact'  — kesin. YALNIZ bunda `total` son sayfayı türetir.
//             'capped' — tavana dayandı; "N+" basılır, son sayfa TÜRETİLMEZ.
//             'approx' — yaklaşık; "~N" basılır, son sayfa TÜRETİLMEZ.
//             'skip'   — sayılmadı. `total` tip düzeyinde YASAK.
//
// ——— Konum ve vurgu ——————————————————————————————————————————
//
// Şerit tablonun ALTINDA ve AKIŞTA. v0.9.645 onu dibe yapıştırmıştı
// ("Next ekranın dışında kalıyordu"); v0.9.1078'de OPERATÖR KARARIYLA
// (2026-08-16, "yüzen şeritler güzel gelmiyor") yapışkanlık ve
// `stickyBottom` prop'u tamamen söküldü — şerit listenin sonunda
// oturur. Gutenberg diyagonali gereği "ileri" eylemi SAĞDA ve
// şeritteki TEK vurgulu kontrol o — Prev/Last ikincil.
//
// ——— "Son sayfa" tek anlam ————————————————————————————————————
//
// `lastReachablePage` açık kaçış kapısı olarak duruyor: çağıran hem
// KESİN hem SUNULABİLİR bir son sayfa hesapladığında verir. Tavanlı
// bir `total`dan asla türetilmez — v0.9.638'in kararı korunuyor,
// artık `count` ile tip düzeyinde çivili.

export type PagerMode = 'offset' | 'cursor';
export type PagerCount = 'skip' | 'approx' | 'exact' | 'capped';

// `skip` beyan eden bir yüzey `total` SMUGGLE EDEMEZ. Sayının anlamı
// ile sayının varlığı tek bir tip kararına bağlanıyor.
type CountDecl =
  | { count: 'skip'; total?: never }
  | { count: 'exact' | 'approx' | 'capped'; total?: number };

interface PagerCommon {
  extras?: React.ReactNode;
}

interface OffsetOnly {
  mode: 'offset';
  page: number;
  pageSize: number;
  hasMore?: boolean;
  onPage: (next: number) => void;
  // YALNIZ hem kesin hem ulaşılabilir olduğunda verilir; verilmezse
  // "Last" hiç çizilmez.
  lastReachablePage?: number;
  // v0.10.711 (operatör: "Next yanında last page butonu olsun") —
  // lastReachablePage VERİLEMEDİĞİNDE (sayı tavanlı / bütçe ötesinde /
  // sayılmadı) listenin SONUNA gitmenin sayfa-numarasız yolu: çağıran
  // sıralamayı tersine çevirip sayfa 0'a döner (en eski 50 = son sayfanın
  // içeriği). Sunulamayan sayfaya yollamaz (v0.9.638 kararı korunur);
  // etiket/başlık çağıranın (ters sıradayken "⇤ First").
  onEnd?: () => void;
  endLabel?: string;
  // v0.10.831 (operator-reported, İKİNCİ kez: "Last diyince sayfa numarası
  // hâlâ 1 gözüküyor") — TERS kip beyanı.
  //
  // v0.10.727 bu sorunu girdinin yanına bir ETİKET koyarak ("Sondan sayfa")
  // çözmeye çalışmıştı; kutu yine "1" yazıyordu ve operatör aynı şikâyeti
  // tekrarladı. Ölçüm (pages/tracesReversePager.test.tsx) şikâyeti doğruladı:
  // sıra gerçekten dönüyor, YANILTAN şey göstergenin KENDİSİ — ters kipte
  // "1" "listenin başındayım" diye okunuyor.
  //
  // Karar (operatör onayı 2026-09-20): ters kipte numara kutusu HİÇ
  // çizilmez, konum SONDAN yazılır — 1 → "Son sayfa", 2 → "Sondan 2.".
  // Sayı eklenmiyor, uydurulmuyor: toplam sayfa sayısı tavanlı sayımda
  // zaten TÜRETİLEMEZ (v0.9.638) ve /traces onu bilerek istemiyor.
  //
  // Kutunun gitmesi bir kayıp değil kasıt: ters kipte "N. sayfaya git"
  // operatörün karşılığını bilmediği bir koordinat (N sondan mı baştan mı?).
  // İleri kipe dönüş tek tık ("⇤ First") ve orada kutu aynen duruyor.
  reverse?: boolean;
  // Ters kipteki konum metninin açıklaması (title) — cümle çağıranın,
  // çünkü "son"un ne demek olduğunu (hangi sıra, neden numara yok) yalnız
  // sayfa bilir.
  reverseTitle?: string;
  endTitle?: string;
}

interface CursorOnly {
  mode: 'cursor';
  hasMore: boolean;
  onMore: () => void;
  loading?: boolean;
  // Birikmiş satır sayısı — dürüst son için ("… yüklendi").
  loaded?: number;
  moreLabel?: string;
  doneLabel?: React.ReactNode;
}

export type PagerProps = PagerCommon & CountDecl & (OffsetOnly | CursorOnly);

// countLabel — sayının anlamını GÖRÜNÜR kılar. Tavanlı bir sayının
// yanındaki "+" ve yaklaşık bir sayının önündeki "~" tesadüfi
// tipografi değil: operatör "12.847 kayıt" ile "en az 12.847 kayıt"
// arasındaki farkı bilmeden kapasite kararı veremez.
export function countLabel(count: PagerCount, total: number | undefined): string | null {
  if (count === 'skip' || total === undefined) return null;
  const n = total.toLocaleString();
  if (count === 'capped') return `${n}+`;
  if (count === 'approx') return `~${n}`;
  return n;
}

// derivedLastPage — son sayfayı YALNIZ kesin sayıdan türet.
//
// Bu fonksiyon ayrı ve saf, çünkü çivilenmesi gereken kural tam
// olarak bu: v0.9.638'in olayı "tavanlı total son sayfayı sürdü"
// idi ve o hata bir bileşenin içinde gömülü kaldığı sürece test
// edilemezdi.
export function derivedLastPage(
  count: PagerCount, total: number | undefined, pageSize: number,
): number | null {
  if (count !== 'exact' || total === undefined || pageSize <= 0) return null;
  return Math.max(0, Math.ceil(total / pageSize) - 1);
}

// cursorProgress — birikimli bir listede "neredeyim" cümlesi.
//
// Saf ve ayrı, çünkü çivilenmesi gereken DÜRÜSTLÜK burada: v0.9.288'de
// /logs "showing 200 of 10,000" basıyordu ve o 10.000 ES'in
// `track_total_hits` tavanıydı — gerçek sayı değil. Tavanlı sayı artık
// "+" ile geliyor ve bu birleştirme tek yerde yaşıyor, üç yüzeyin
// kendi cümlesini kurmasına gerek kalmıyor.
export function cursorProgress(
  loaded: number | undefined, count: PagerCount, total: number | undefined,
): string | null {
  const label = countLabel(count, total);
  if (loaded !== undefined && label) return `showing ${loaded.toLocaleString()} of ${label}`;
  if (label) return label;
  if (loaded !== undefined) return `showing ${loaded.toLocaleString()}`;
  return null;
}

export function Pager(props: PagerProps) {
  const { count, total, extras } = props;
  const cls = 'pager';
  const label = countLabel(count, total);

  if (props.mode === 'cursor') {
    const { hasMore, onMore, loading, loaded, moreLabel, doneLabel } = props;
    const progress = cursorProgress(loaded, count, total);
    return (
      <div className={cls} data-pager-mode="cursor">
        {hasMore ? (
          <>
            <Button variant="primary" size="sm" onClick={onMore} loading={loading}>
              {moreLabel ?? '↓ Load more'}
            </Button>
            {progress && <span style={{ color: 'var(--text2)' }}>{progress}</span>}
          </>
        ) : (
          // Biten listede ilerleme cümlesi TEKRAR olurdu — dürüst son
          // sayıyı zaten taşıyor.
          <span style={{ color: 'var(--text3)' }}>
            {doneLabel ?? (loaded !== undefined
              ? `penceredeki tüm eşleşmeler yüklendi (${loaded.toLocaleString()} satır)`
              : 'tümü yüklendi')}
          </span>
        )}
        {extras && <span style={{ color: 'var(--text2)' }}>· {extras}</span>}
      </div>
    );
  }

  return <OffsetPager {...props} cls={cls} label={label} />;
}

function OffsetPager({
  page, pageSize, hasMore, onPage, lastReachablePage, onEnd, endLabel, endTitle, reverse, reverseTitle, count, total, extras, cls, label,
}: PagerCommon & CountDecl & OffsetOnly & { cls: string; label: string | null }) {
  const [draft, setDraft] = useState(String(page + 1));

  // Prev/Next ile sayfa değişince girdi senkron kalsın.
  useEffect(() => { setDraft(String(page + 1)); }, [page]);

  const lastPage = derivedLastPage(count, total, pageSize);
  const atEnd = lastPage !== null ? page >= lastPage : !hasMore;
  // v0.10.831 — ters kipte konum metni; ileri kipte null (kutu çizilir).
  const position = pagePositionLabel(page, reverse === true);
  // v0.10.831 (inceleme, 2026-09-20) — BİTİŞ YUVASI ters kipte `onEnd`e bağlı.
  //
  // Ölçülen tuzak: ters kipte kesin bir `lastReachablePage` varken o düğme
  // `onPage(lastReachablePage)` çağırıyordu; tık sayfayı 5'e götürüyor,
  // gösterge "Sondan 6." diyor, sıra HÂLÂ ters ve `lastReachablePage > page`
  // artık yanlış olduğu için İKİ bitiş düğmesi birden kayboluyordu — şeritten
  // geri dönüş yolu kalmıyordu (Prev×5 ya da başlığa iki tık).
  //
  // Ters kipte "başa dön" bir SAYFA SIÇRAMASI değil bir SIRA işlemidir: onu
  // yalnız `onEnd` yapabilir (çağıran sırayı düzeltip sayfa 0'a döner).
  // Dolayısıyla ters kipte tek bitiş düğmesi çizilir ve sayısal sıçrama dalı
  // hiç çizilmez. İleri kip bayt bayt aynı.
  const endAction = onEnd && (reverse === true || lastReachablePage === undefined);
  const showJump = !reverse && !endAction
    && lastReachablePage !== undefined && lastReachablePage > page;

  const commit = (e?: FormEvent) => {
    if (e) e.preventDefault();
    const n = parseInt(draft, 10);
    if (isNaN(n) || n < 1) { setDraft(String(page + 1)); return; }
    let target = n - 1;
    if (lastPage !== null) target = Math.min(target, lastPage);
    target = Math.max(0, target);
    if (target !== page) onPage(target);
    setDraft(String(target + 1));
  };

  return (
    <div className={cls} data-pager-mode="offset">
      {/* v0.10.831 — ters kipte yön BELİRSİZ kalmasın. Metin yalnız KONUM
          sözcükleriyle konuşuyor ("sondan N."), zaman sözcükleriyle değil:
          atom hangi eksende sıralandığını BİLMEZ ve "daha eski/yeni kayıtlar"
          demek yalnız zaman ekseninde doğru olurdu (çağıranın `reverseTitle`i
          bilerek sıra-nötr). Davranış aynı kaldı, söylenen değişti. */}
      <Button variant="secondary" size="sm"
        onClick={() => onPage(Math.max(0, page - 1))} disabled={page === 0}
        title={reverse ? '"Son sayfa" yönünde bir konum geri' : undefined}>
        ← Prev
      </Button>

      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
        {position !== null ? (
          // Ters kip: SAYI KUTUSU YOK. Konum sondan yazılır — operatörün
          // şikâyeti tam olarak kutudaki "1"di (v0.10.727 → v0.10.831).
          // v0.10.831 (inceleme) — KESİN toplam ters kipte de görünür:
          // "sayı türetilemez" gerekçesi yalnız TAVANLI sayımda geçerli,
          // `lastPage` zaten yalnız `count === 'exact'` iken doluyor.
          <>
            <span title={reverseTitle} style={{ fontVariantNumeric: 'tabular-nums' }}>{position}</span>
            {lastPage !== null && (
              <span style={{ color: 'var(--text3)' }}>/ {lastPage + 1}</span>
            )}
          </>
        ) : (
        <>
        <span>Page</span>
        <form onSubmit={commit} style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
          <input value={draft}
            onChange={e => setDraft(e.target.value)}
            // Ö5 (v0.9.1014) — commit YALNIZ Enter'da. Öncesinde `onBlur`
            // da commit ediyordu ve bu iki şekilde ısırıyordu: (a) yarım
            // yazılmış bir sayı (operatör "12"yi silip "3" yazacakken
            // sekmeye bastı) sessizce bir fetch tetikliyordu; (b) Tab ile
            // şeritte gezinmek sayfa atlatıyordu. Blur artık GERİ ALIR —
            // "yazdım ama onaylamadım" hâli kaybolmuş sayılır, uydurulmaz.
            onBlur={() => setDraft(String(page + 1))}
            inputMode="numeric"
            aria-label="Go to page"
            title="Enter ile git"
            style={{
              width: 56, textAlign: 'center', fontFamily: 'var(--font-mono)',
              fontVariantNumeric: 'tabular-nums', padding: '3px 6px',
            }} />
          {lastPage !== null && (
            <span style={{ color: 'var(--text3)' }}>/ {lastPage + 1}</span>
          )}
        </form>
        </>
        )}
        {/* Tavanlı/yaklaşık sayı son sayfayı SÜRMEZ ama görünür kalır. */}
        {lastPage === null && label && (
          <span style={{ color: 'var(--text2)' }}>· {label}</span>
        )}
        {extras && <span style={{ color: 'var(--text2)' }}>· {extras}</span>}
      </span>

      {/* Gutenberg: ileri eylemi SAĞDA ve şeritteki TEK vurgulu kontrol. */}
      <Button variant="primary" size="sm" onClick={() => onPage(page + 1)} disabled={atEnd}
        title={reverse ? 'Sondan bir sonraki sayfa (listenin başına doğru)' : undefined}>
        Next →
      </Button>
      {/* v0.10.711 — kesin son sayfa yoksa "sona git" (sıra tersi) düğmesi;
          kesin sayfa varsa o kazanır (aynı yerde tek düğme). */}
      {endAction && (
        <Button variant="secondary" size="sm" onClick={onEnd}
          title={endTitle ?? 'Listenin sonuna git'}>
          {endLabel ?? 'Last ⇥'}
        </Button>
      )}
      {showJump && (
        <Button variant="secondary" size="sm" onClick={() => onPage(lastReachablePage)}
          title={`Son sayfaya git (${lastReachablePage + 1})`}>
          Last ⇥
        </Button>
      )}
    </div>
  );
}
