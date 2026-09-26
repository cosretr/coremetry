import { useCallback, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from 'react';
import type { DataTable, DataTableSelection } from './DataTable';
import { Button } from '../Button';
import { ActionRow } from '../ActionRow';

// BulkBar — v0.10.939 (tablo standardı T8): toplu işlemin TEK çubuğu.
//
// Envanter üç toplu çubuk buldu ve üçü de başkaydı: Problems İngilizce
// "N selected" + accent2 kenar + solda ikincil "Clear", sağda boyutsuz birincil;
// Inbox Türkçe "N seçili" + birincil "Onayla (n)" SOLDA, "Seçimi temizle"
// sağda (K5 "iptal solda, onay sağda" kuralının tersi); NoisyRules sayısız,
// iki eylemi araç çubuğunun içinde. Üçü de kendi `Set` durumunu tutuyor;
// useDataTable'ın `selection` seçeneğinin (toggle/range/all/clear, id ile)
// benimseyeni 0.
//
// SÖZLEŞME (BulkBar.contract.test.tsx):
//   • Seçim `dt.selection`dan okunur — sayfa ikinci bir Set TUTMAZ. Tablo
//     `selection` seçeneği olmadan kurulduysa (dt.selection null) hiçbir şey
//     basılmaz (ne çubuk ne kap).
//   • Görünür çubuk yalnız N > 0 iken; N = 0'da GÖRÜNÜR hiçbir şey yok (araç
//     çubuğu normal triyajda sessiz kalır — Inbox v0.9.252 gerekçesi). Ama
//     iki düğüm HER ZAMAN bağlı (aşağıda): odak kabı + canlı bölge.
//   • "N seçili" · isteğe bağlı bağlam satırı (children) · sağda
//     [Temizle][birincil] — sıra ActionRow atomunun YAPISINDA (K5), çağıranın
//     disiplininde değil.
//   • TAM BİR birincil eylem: `action` bir düğme değil bir TANIM; çubuk onu
//     `<Button variant="primary" size="sm">` olarak kendisi basar — yan yana
//     iki dolu buton ya da elle ikinci renk YAZILAMAZ (Pager `mode`/`count`
//     zorunluluğunun aynı hilesi). Yıkıcı toplu eylem çubuğa değil ⋯ menüye +
//     ConfirmDialog'a (T8).
//   • "Temizle" `dt.selection.clear()` çağırır, sonra `onClear` (sayfanın
//     kendi bağlam notunu sıfırlaması için); birincil yüklenirken devre dışı.
//   • Seçili satırın tonu `.row-selected` (T2): onu `dt.rowProps(i)` basar
//     (seçim kimliği `dt.selection.isSelected` ile — çekirdek düzeltmesi,
//     DataTable.tsx rowProps). Çubuk satırlara DOKUNMAZ; satırın sınıfı için
//     sayfa `{...dt.rowProps(i)}` yaymak zorunda.
//
// v0.10.939 (tablo standardı T8) — ODAK <body>'YE DÜŞMEZ. Seçim boşalınca
// çubuk (ve odaklı düğmesi) DOM'dan kalkar; tarayıcı odağı <body>'ye atar ve
// klavye kullanıcısı sayfanın başına fırlar. İki yol, ikisi de kapatıldı:
//   • "Temizle": odak ÖNCE taşınır, SONRA seçim boşaltılır.
//   • Sayfa birincil eylemden sonra seçimi kendisi boşaltır (Inbox "Onayla"
//     deseni; zamanını çubuk bilmez): gövde bileşeninin useLayoutEffect
//     temizliği, React düğmeyi DOM'dan kaldırmadan ÖNCE çalışır — odak
//     gövdenin içindeyse oradan taşınır. Odak başka yerdeyse DOKUNULMAZ.
// Hedef: `returnFocusRef` bağlı ve odak alabiliyorsa o (sayfanın anlamlı
// yeri: arama kutusu, tablo kabı); değilse HER ZAMAN bağlı kap
// (`[data-bulk-bar-host]`, tabIndex=-1, N = 0'da görünür içeriği yok —
// çubuğun durduğu yer). Kap blok bir <div>: çubuk tablonun üstünde durur;
// boşluklu (gap) bir flex araç çubuğuna konursa boş kap bir aralık ekler.
//
// v0.10.939 (tablo standardı T8) — DUYURU: kapta HER ZAMAN bağlı, görünmez
// (`.sr-only`) TEK canlı bölge (role=status, polite, atomic). Canlı bölge
// içeriği değişmeden ÖNCE ağaçta olmalı — eskiden sayaç kendisi aria-live'dı
// ama N > 0 ile birlikte doğduğu için İLK seçim hiç duyurulmuyordu. Şimdi
// ilk seçim dahil her değişim "N seçili", boşalma "Seçim temizlendi". Görünür
// sayaç canlı bölge DEĞİL (çift duyuru olmasın). Bağlanışta (değişim yok)
// sessiz.

export interface BulkBarAction {
  /** Düğme metni (ör. `Onayla (${n})`). */
  label: string;
  /** Seçili kimliklerle çağrılır — sayfa ikinci bir Set tutmasın diye. */
  onClick: (ids: ReadonlySet<string>) => void;
  disabled?: boolean;
  loading?: boolean;
}

export interface BulkBarProps<T> {
  dt: DataTable<T>;
  /** TEK birincil eylem (tanım; çubuk düğmeyi kendisi basar). */
  action: BulkBarAction;
  /** Sayacın yanındaki bağlam satırı (ör. "3 onaylanabilir · 1 atlanacak"). */
  children?: ReactNode;
  /** "Temizle" seçimi boşalttıktan SONRA çağrılır. */
  onClear?: () => void;
  /**
   * v0.10.939 (tablo standardı T8) — seçim boşalınca (Temizle ya da sayfanın
   * birincilden sonraki temizliği) odağın ineceği sayfa hedefi. Bağlı değilse
   * ya da odak alamıyorsa çubuğun her zaman bağlı kabına iner.
   */
  returnFocusRef?: RefObject<HTMLElement | null>;
}

// v0.10.939 (tablo standardı T8) — canlı bölgenin iki cümlesi.
const clearedText = 'Seçim temizlendi';
const countText = (n: number) => `${n} seçili`;

interface BulkBarBodyProps<T> {
  sel: DataTableSelection<T>;
  n: number;
  action: BulkBarAction;
  children?: ReactNode;
  onClear?: () => void;
  /** Kararlı (useCallback []) — gövde bağlıyken temizlik yeniden koşmasın. */
  landFocus: () => void;
}

// v0.10.939 (tablo standardı T8) — yalnız N > 0 iken bağlı gövde. Ayrı bir
// bileşen çünkü useLayoutEffect temizliği silinen bileşende, altındaki DOM
// düğümleri kaldırılmadan ÖNCE çalışır: odak hâlâ düğmedeyken yakalanır.
function BulkBarBody<T>({ sel, n, action, children, onClear, landFocus }: BulkBarBodyProps<T>) {
  const barRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const barEl = barRef.current;
    return () => {
      if (barEl && barEl.contains(document.activeElement)) landFocus();
    };
  }, [landFocus]);
  return (
    <div ref={barRef} className="bulk-bar" role="group" aria-label="Toplu işlem">
      <span className="bulk-bar-count">{countText(n)}</span>
      {children && <span className="bulk-bar-meta">{children}</span>}
      <span className="bulk-bar-actions">
        <ActionRow inline
          secondary={
            <Button variant="secondary" size="sm" disabled={action.loading}
              onClick={() => { landFocus(); sel.clear(); onClear?.(); }}>
              Temizle
            </Button>
          }
          confirm={
            <Button variant="primary" size="sm" disabled={action.disabled} loading={action.loading}
              onClick={() => action.onClick(sel.ids)}>
              {action.label}
            </Button>
          } />
      </span>
    </div>
  );
}

export function BulkBar<T>({ dt, action, children, onClear, returnFocusRef }: BulkBarProps<T>) {
  const sel = dt.selection;
  const n = sel ? sel.ids.size : 0;

  // v0.10.939 (tablo standardı T8) — odak iniş noktası. `returnFocusRef` bir
  // ref'te tutulur ki `landFocus` kararlı kalsın: çağıran her render'da yeni
  // bir ref nesnesi verse bile gövdenin temizliği bağlıyken koşmaz.
  const hostRef = useRef<HTMLDivElement>(null);
  const returnRef = useRef(returnFocusRef);
  useLayoutEffect(() => { returnRef.current = returnFocusRef; }, [returnFocusRef]);
  const landFocus = useCallback(() => {
    const page = returnRef.current?.current;
    if (page && page.isConnected) {
      page.focus();
      if (document.activeElement === page) return;
    }
    hostRef.current?.focus({ preventScroll: true });
  }, []);

  // v0.10.939 (tablo standardı T8) — duyuru metni N'nin DEĞİŞİMİNDEN türer
  // (render sırasında durum ayarı; efekt + ikinci boyama yok). Bağlanış
  // sessiz: değişim yok.
  const [seenN, setSeenN] = useState(n);
  const [said, setSaid] = useState('');
  if (n !== seenN) {
    setSeenN(n);
    setSaid(n > 0 ? countText(n) : clearedText);
  }

  if (!sel) return null;
  return (
    <div ref={hostRef} tabIndex={-1} data-bulk-bar-host="">
      <span className="sr-only" role="status" aria-live="polite" aria-atomic="true">{said}</span>
      {n > 0 && (
        <BulkBarBody sel={sel} n={n} action={action} onClear={onClear} landFocus={landFocus}>
          {children}
        </BulkBarBody>
      )}
    </div>
  );
}
