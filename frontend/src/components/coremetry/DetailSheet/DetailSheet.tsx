// coremetry/DetailSheet — v0.10.1669 (etkileşim dilimi S1, plan §2). `ui/Drawer`ın
// halefi: coremetry/overlay/Sheet üstünde (Radix odak yönetimi + escBridge — tek
// Esc yığını, ⌘K üstteyken Esc yalnız paleti kapatır). Drawer'ın her özelliği
// taşınır (types.ts): perde/odak tuzağı (modal), eşlik kipi + boş alana tık
// kapatma, sol kenar tutamağı (resizeKey, Q9), ariaLabel, başlık + ✕; ek olarak
// `pageHref` (varlık tutarlılığı: her çekmece öznesinin tam sayfası).
// Inline stil YOK: genişlik `--sheet-w` ref'ten yazılır, sınıf okur; <640px tam
// genişlik. İrtifa Drawer'ınkiyle aynı rung'lar (modal: --z-drawer perde /
// --z-drawer-panel panel; eşlik: --z-nav) — tokens.css utilities katmanı.
// CosreSheet ayrı kabuk kalır (sohbet ≠ detay); kurallar aynı.
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { Sheet, SheetContent, SheetTitle } from '@/components/coremetry/overlay';
import { isEmptyAreaClick } from '@/lib/emptyAreaClose';
import { storedDrawerWidth } from '@/lib/drawerWidth';
import { getRaw } from '@/lib/storage';
import { CM_STRINGS } from '../strings';
import { SheetResizer } from './SheetResizer';
import type { DetailSheetProps } from './types';

const viewport = () => (typeof window === 'undefined' ? 1920 : window.innerWidth);

export function DetailSheet({
  open, onOpenChange, title, badges, pageHref, width = 560, resizeKey, modal = true,
  closeOnEmptyClick = false, ariaLabel = CM_STRINGS.detailSheet, footer, bodyClassName,
  contentProps, children,
}: DetailSheetProps) {
  const [userW, setUserW] = useState<number | null>(() =>
    resizeKey && getRaw(resizeKey) ? storedDrawerWidth(getRaw(resizeKey), width, viewport()) : null);
  const effW = userW ?? Math.min(width, viewport());

  const panelRef = useRef<HTMLDivElement | null>(null);
  const opener = useRef<Element | null>(null);
  const setPanel = useCallback((el: HTMLDivElement | null) => {
    panelRef.current = el;
    el?.style.setProperty('--sheet-w', `${effW}px`);
  }, [effW]);
  // Sürüklerken Radix'in birleşik ref'i yeniden çağrılmayabilir → açık panele ayrıca yaz.
  useLayoutEffect(() => { panelRef.current?.style.setProperty('--sheet-w', `${effW}px`); }, [effW]);

  // Eşlik kipi + closeOnEmptyClick: Drawer kuralı — metin seçimi kapatmaz,
  // dinleyici bir tık sonra bağlanır ki açan tıklama hemen kapatmasın.
  const closeRef = useRef(onOpenChange);
  useEffect(() => { closeRef.current = onOpenChange; }, [onOpenChange]);
  useEffect(() => {
    if (!open || modal || !closeOnEmptyClick) return;
    const onClick = (e: MouseEvent) => {
      if (e.button !== 0 || e.defaultPrevented) return;
      if ((window.getSelection()?.toString() ?? '') !== '') return;
      if (isEmptyAreaClick(e.target, panelRef.current)) closeRef.current(false);
    };
    const t = setTimeout(() => document.addEventListener('click', onClick), 0);
    return () => { clearTimeout(t); document.removeEventListener('click', onClick); };
  }, [open, modal, closeOnEmptyClick]);

  return (
    <Sheet open={open} onOpenChange={onOpenChange} modal={modal}>
      <SheetContent
        side="right"
        ref={setPanel}
        aria-label={ariaLabel}
        aria-describedby={undefined}
        closeLabel={CM_STRINGS.close}
        data-detail-sheet=""
        data-modal={modal ? 'true' : 'false'}
        // Eşlik kipinde sayfayla çalışmak çekmeceyi kapatmaz (Drawer backdrop=false).
        onInteractOutside={modal ? undefined : e => e.preventDefault()}
        // Odak dönüşü (Drawer lastFocus kuralı): Radix tetikleyicisiz denetimli
        // diyalogda odağı yalnız Trigger'a döndürür — açan öğe burada saklanır.
        onOpenAutoFocus={() => { opener.current = document.activeElement; }}
        onCloseAutoFocus={e => {
          const o = opener.current;
          opener.current = null;
          if (!(o instanceof HTMLElement) || !o.isConnected) return;
          // Eşlik kipinde sayfada başka öğeye tıklanarak kapandıysa odak orada kalır.
          const a = document.activeElement;
          if (!modal && a && a !== document.body && a.isConnected) { e.preventDefault(); return; }
          e.preventDefault();
          o.focus({ preventScroll: true });
        }}
        className="cm-detail-sheet w-full gap-0 p-0 sm:w-(--sheet-w) sm:max-w-none"
        {...contentProps}>
        {resizeKey && (
          <SheetResizer storageKey={resizeKey} width={effW} onWidth={setUserW} onReset={() => setUserW(null)} />
        )}
        <div className="flex items-center gap-3 border-b px-5 py-3 pr-12">
          <SheetTitle className="min-w-0 truncate text-sm font-semibold">{title}</SheetTitle>
          {badges}
          <span className="flex-1" />
          {pageHref && (
            <Link to={pageHref} className="shrink-0 text-xs text-[var(--accent)] hover:underline" data-sheet-page-link="">
              {CM_STRINGS.openPage}
            </Link>
          )}
        </div>
        <div className={bodyClassName ?? 'min-h-0 flex-1 overflow-y-auto px-5 py-4'}>{children}</div>
        {footer && <div className="flex items-center justify-end gap-2 border-t px-5 py-3">{footer}</div>}
      </SheetContent>
    </Sheet>
  );
}
