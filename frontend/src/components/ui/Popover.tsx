import {
  useCallback, useEffect, useLayoutEffect, useRef,
  type FocusEvent as ReactFocusEvent, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type RefObject,
} from 'react';
import { Link } from 'react-router-dom';
import { useEscLayer } from '@/lib/escLayer';
import { placeMenu, type MenuPlaceOpts } from './DataTable/menuPlacement';

// Popover — v0.10.968 (Trace › Metrics yeniden tasarımı; tasarım sistemi
// §1 "bulamazsan ui/ altına yaz + barrel'a ekle"). Çapaya bağlı küçük yüzey:
// satır ⋯ menüleri, "+N" çip menüsü, kapsam diyaloğu. Depoda aynı iş
// DataTable/HeadMenu.tsx'te tek kullanımlık kuruluydu (başlık ⋯ menüsü);
// davranış oradan genelleştirildi, sözleşme Popover.contract.test.tsx'te.
//
//   • kind 'menu'  → role=menu; ↑ ↓ Home End `[role^="menuitem"]` arasında
//                     gezer (menuitem, menuitemcheckbox, menuitemradio);
//                     Tab menüyü kapatır ve odağı çapaya verir.
//     kind 'dialog' → role=dialog; odak içeride serbest, dışarı çıkınca kapanır.
//   • Yerleşim `position: fixed`, koordinat placeMenu'dan (çapanın sağ
//     kenarına hizalı, alta sığmazsa üste). `.table-wrap`/`td` overflow'u
//     kırpmaz. `fixed`i yakalayan bir ata (transform/contain) varsa ölçülen
//     fark kadar düzeltilir (HeadMenu ile aynı yol). Kaydırma/boyutta yeniden
//     yerleşir; çapa DOM'dan düşerse kapanır.
//   • Esc `useEscLayer` ile (tek Esc kanalı; bu dosyada Escape karşılaştırması
//     YOK — escLayer.test tüm ağacı tarar). Popover/ipucu kendi katmanını
//     açık yüzeyin ÜSTÜNE koyar (LIFO): Esc önce menüyü kapatır.
//   • Dışarıya pointerdown (popover ve çapa dışı) kapatır.
//   • Açılınca ilk odaklanabilir öğe (menüde ilk etkin menuitem) odak alır;
//     kapanırken odak popover'ın içindeyse çapaya DÖNER (useLayoutEffect
//     temizliği DOM düğümleri kalkmadan koşar — DataTableState deseni).

export interface PopoverProps {
  anchorRef: RefObject<HTMLElement | null>;
  open: boolean;
  onClose: () => void;
  kind: 'menu' | 'dialog';   // role=menu (↑↓ Home End among [role^="menuitem"]) | role=dialog
  ariaLabel: string;
  width?: number;            // px, default 240
  /** v0.10.1141 — yerleşim tercihi (varsayılan: alt, sağ kenara hizalı). */
  placement?: MenuPlaceOpts;
  /** v0.10.1141 — menüde açılışta odaklanacak öğe (ör. seçili `[aria-checked="true"]`); yoksa ilk öğe. */
  initialFocus?: string;
  children: ReactNode;
}

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';
const MENU_ITEMS = '[role^="menuitem"]';

export function Popover({ anchorRef, open, onClose, kind, ariaLabel, width = 240, placement, initialFocus, children }: PopoverProps) {
  const popRef = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  const closeToAnchor = useCallback(() => {
    onCloseRef.current();
    anchorRef.current?.focus({ preventScroll: true });
  }, [anchorRef]);
  useEscLayer(open, closeToAnchor);

  // Dışarı pointerdown: yakalama fazında (satırın kendi işleyicisi durdursa da).
  useEffect(() => {
    if (!open) return;
    const onDown = (e: Event) => {
      const t = e.target as Node | null;
      if (!t) return;
      if (popRef.current?.contains(t) || anchorRef.current?.contains(t)) return;
      onCloseRef.current();
    };
    document.addEventListener('pointerdown', onDown, true);
    return () => document.removeEventListener('pointerdown', onDown, true);
  }, [open, anchorRef]);

  if (!open) return null;
  return (
    <PopoverSurface popRef={popRef} anchorRef={anchorRef} kind={kind} ariaLabel={ariaLabel} width={width}
      prefer={placement?.prefer} align={placement?.align} initialFocus={initialFocus}
      onClose={() => onCloseRef.current()} onCloseToAnchor={closeToAnchor}>
      {children}
    </PopoverSurface>
  );
}

// Açık yüzey AYRI bileşen: temizliği (odak dönüşü) React onun <div>'ini
// DOM'dan kaldırmadan ÖNCE koşar — silinen alt ağaçtaki layout temizlikleri
// ana düğüm çıkarılmadan önce çağrılır (DataTableState StateBody deseni).
function PopoverSurface({ popRef, anchorRef, kind, ariaLabel, width, prefer, align, initialFocus, onClose, onCloseToAnchor, children }: {
  popRef: RefObject<HTMLDivElement>;
  anchorRef: RefObject<HTMLElement | null>;
  kind: 'menu' | 'dialog';
  ariaLabel: string;
  width: number;
  prefer?: MenuPlaceOpts['prefer'];
  align?: MenuPlaceOpts['align'];
  initialFocus?: string;
  onClose: () => void;
  onCloseToAnchor: () => void;
  children: ReactNode;
}) {
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  const place = useCallback(() => {
    const a = anchorRef.current;
    const pop = popRef.current;
    if (!pop) return;
    if (!a || !a.isConnected) { closeRef.current(); return; }
    const r = a.getBoundingClientRect();
    const want = placeMenu(
      { left: r.left, top: r.top, width: r.width, height: r.height },
      { width: pop.offsetWidth, height: pop.offsetHeight },
      { width: window.innerWidth, height: window.innerHeight },
      { prefer, align },
    );
    // Ölçülen yerleşim doğrudan DOM'a: React stili her render'da ezmesin.
    pop.style.left = `${want.left}px`;
    pop.style.top = `${want.top}px`;
    const got = pop.getBoundingClientRect();
    const dx = got.left - want.left;
    const dy = got.top - want.top;
    if (dx || dy) {
      pop.style.left = `${want.left - dx}px`;
      pop.style.top = `${want.top - dy}px`;
    }
  }, [anchorRef, popRef, prefer, align]);

  useLayoutEffect(() => {
    const pop = popRef.current;
    if (!pop) return;
    // Açılıştaki çapa: kapanışta odak ona döner (yüzey çapa başına bir kez bağlanır).
    const anchor = anchorRef.current;
    place();
    const preferred = initialFocus ? pop.querySelector<HTMLElement>(initialFocus) : null;
    const first = preferred ?? (kind === 'menu'
      ? pop.querySelector<HTMLElement>(`${MENU_ITEMS}:not([aria-disabled="true"]):not(:disabled)`)
      : pop.querySelector<HTMLElement>(FOCUSABLE));
    (first ?? pop).focus({ preventScroll: true });
    let raf = 0;
    const onMove = () => {
      if (raf) return;
      raf = requestAnimationFrame(() => { raf = 0; place(); });
    };
    window.addEventListener('resize', onMove);
    document.addEventListener('scroll', onMove, true);
    return () => {
      if (raf) cancelAnimationFrame(raf);
      window.removeEventListener('resize', onMove);
      document.removeEventListener('scroll', onMove, true);
      // Kapanış: odak içerideyse çapaya döner (düğüm henüz DOM'da).
      if (pop.contains(document.activeElement)) anchor?.focus({ preventScroll: true });
    };
  }, [kind, place, anchorRef, popRef, initialFocus]);

  const onKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (kind !== 'menu') return;
    if (e.key === 'Tab') { e.preventDefault(); onCloseToAnchor(); return; }
    const list = Array.from(e.currentTarget.querySelectorAll<HTMLElement>(MENU_ITEMS));
    if (list.length === 0) return;
    const at = list.indexOf(document.activeElement as HTMLElement);
    let next = -1;
    if (e.key === 'ArrowDown') next = at < 0 ? 0 : (at + 1) % list.length;
    else if (e.key === 'ArrowUp') next = at < 0 ? list.length - 1 : (at - 1 + list.length) % list.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = list.length - 1;
    if (next >= 0) {
      e.preventDefault();
      list[next].focus({ preventScroll: true });
    }
  };

  // Diyalog: odak popover'ın ve çapanın dışına çıkınca kapanır (Tab ile terk).
  const onBlur = (e: ReactFocusEvent<HTMLDivElement>) => {
    if (kind !== 'dialog') return;
    const to = e.relatedTarget as Node | null;
    if (!to) return;
    if (popRef.current?.contains(to) || anchorRef.current?.contains(to)) return;
    closeRef.current();
  };

  return (
    <div ref={popRef} role={kind} aria-label={ariaLabel} tabIndex={-1}
      className="ui-popover" style={{ width }}
      onKeyDown={onKeyDown} onBlur={onBlur}>
      {children}
    </div>
  );
}

// PopoverLinkItem — v0.10.968: GEZİNEN menü satırı (satır ⋯ menüsündeki "Pod
// sayfasında aç ↗", "Servis sayfasına git"). MenuItem bir DÜĞME ve gezinmez;
// gerçek `<Link>` orta tık / ⌘-tık / "yeni sekmede aç"ı tarayıcıya bırakır
// (tablo standardı T7). Görünüm menü satırıyla aynı (`.menuitem` tabanı
// yalnız ui/ içinde yazılır — primitiveClasses kapısı).
export function PopoverLinkItem({ to, onSelect, children }: { to: string; onSelect?: () => void; children: ReactNode }) {
  return (
    <Link role="menuitem" className="menuitem" to={to} onClick={onSelect}>
      <span className="menuitem-label">{children}</span>
    </Link>
  );
}
