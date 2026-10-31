// CosreSheet — v0.10.1647 (CoSRE v2 Faz 2b B1, plan §4.1). v2 sohbetinin
// SUNUMSAL kabuğu: coremetry Sheet'i, v1 çekmecesinin davranışıyla.
//   • modal={false}: sohbet sayfaya EŞLİK eder (v1 backdrop={false}) — perde
//     yok, arkası tıklanır/kaydırılır. Radix'in dışarı-tık kapatması
//     (onInteractOutside) engellenir; yerine v1 kuralı: yalnız BOŞ alana
//     tıklama kapatır (lib/emptyAreaClose).
//   • Esc: coremetry/overlay escBridge (tek Esc yığını, v1 useEscLayer ile aynı).
//   • Genişlik: v1 ile aynı anahtar + kelepçe (lib/cosre/sheetWidth); inline
//     style YOK — `--cosre-sheet-w` ref'ten yazılır, sınıf okur. <640px tam genişlik.
//   • Başlık: AiMark + "CoSRE" + v1'in bağlam pini (CosreHeaderContext) + eylemler
//     (Tam sayfada aç, Temizle) + ✕ (IA-2; v0.10.1650 plan §10: v1 başlık eylemleri).
// v0.10.1650 (B2): gövde çağıranın — v1 karşılaması/composer'ı + v2 cevap yolu
// (CosreChatV2). Bu dosya sunumsal kalır (katalog statik içerikle çizer).
// components/ui'den HİÇBİR şey içe aktarılmaz (uiImportRatchet, ui/ donmuş).
import { useCallback, useEffect, useLayoutEffect, useRef, type ReactNode } from 'react';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/coremetry/overlay';
import { AiMark } from '@/components/ai/CopilotFab';
import { isEmptyAreaClick } from '@/lib/emptyAreaClose';
import { COSRE_V2_STRINGS as T } from './strings';

export interface CosreSheetProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  /** px; lib/cosre/sheetWidth ile hesaplanır. */
  width: number;
  /** Katalog: portal içeriğine hücre teması (data-theme/data-density). */
  contentProps?: Record<`data-${string}`, string>;
  /** Başlıkta marka ile eylemler arası (v1 bağlam pini); yoksa boşluk. */
  headerContext?: ReactNode;
  /** Başlık eylemleri (Tam sayfada aç, Temizle). */
  headerActions?: ReactNode;
  /** araç seti dilim 5 — başlıktaki kapsam rozeti (evidence/ScopeBadge); yoksa çizilmez. */
  headerBadge?: ReactNode;
  /** Gövde (karşılama / mesajlar + composer). */
  children?: ReactNode;
}

export function CosreSheet({ open, onOpenChange, width, contentProps, headerContext, headerActions, headerBadge, children }: CosreSheetProps) {
  const panelRef = useRef<HTMLDivElement | null>(null);
  const setPanel = useCallback((el: HTMLDivElement | null) => {
    panelRef.current = el;
    el?.style.setProperty('--cosre-sheet-w', `${width}px`);
  }, [width]);
  // v0.10.1664 (B6) — tutamak sürüklerken genişlik değişir; Radix'in birleşik
  // ref'i yeniden çağrılmayabilir → açık panele ayrıca yazılır.
  useLayoutEffect(() => { panelRef.current?.style.setProperty('--cosre-sheet-w', `${width}px`); }, [width]);

  // v1 Drawer closeOnEmptyClick ile aynı: metin seçimi kapatmaz; dinleyici bir
  // tık sonra bağlanır ki açan tıklama hemen kapatmasın.
  const closeRef = useRef(onOpenChange);
  useEffect(() => { closeRef.current = onOpenChange; }, [onOpenChange]);
  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (e.button !== 0 || e.defaultPrevented) return;
      if ((window.getSelection()?.toString() ?? '') !== '') return;
      if (isEmptyAreaClick(e.target, panelRef.current)) closeRef.current(false);
    };
    const t = setTimeout(() => document.addEventListener('click', onClick), 0);
    return () => { clearTimeout(t); document.removeEventListener('click', onClick); };
  }, [open]);

  return (
    <Sheet open={open} onOpenChange={onOpenChange} modal={false}>
      <SheetContent
        side="right"
        ref={setPanel}
        aria-label={T.sheetLabel}
        data-cosre-v2=""
        onInteractOutside={e => e.preventDefault()}
        // Açılış odağı composer'ın (v1 gibi; ChatComposer autoFocus) — Radix ilk düğmeye taşımasın.
        onOpenAutoFocus={e => e.preventDefault()}
        onCloseAutoFocus={e => {
          // Odak FAB'a döner (kapanışta FAB yeniden çizilir); sayfada başka
          // bir öğeye tıklanarak kapandıysa odak orada kalır.
          e.preventDefault();
          const a = document.activeElement;
          if (a && a !== document.body && a.isConnected) return;
          requestAnimationFrame(() => document.querySelector<HTMLElement>('.cm-ai-fab')?.focus());
        }}
        // v0.10.1714 — bg-card (= --bg1, v1 Drawer ile aynı): shadcn bg-background
        // = --bg0 SAYFA zemini, panel sayfaya karışıyordu (bgZeroIsPageOnly kuralı).
        className="w-full gap-0 bg-card p-0 sm:w-(--cosre-sheet-w) sm:max-w-none"
        {...contentProps}>
        <SheetHeader className="cosre-head flex-row items-center gap-3 border-b px-4 py-3 pr-12">
          <AiMark size={20} />
          <SheetTitle>{T.title}</SheetTitle>
          <SheetDescription className="sr-only">{T.description}</SheetDescription>
          {headerBadge}
          {headerContext ?? <span className="flex-1" />}
          {headerActions}
        </SheetHeader>
        {children}
      </SheetContent>
    </Sheet>
  );
}
