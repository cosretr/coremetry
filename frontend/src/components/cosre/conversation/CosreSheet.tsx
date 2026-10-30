// CosreSheet — v0.10.1647 (CoSRE v2 Faz 2b B1, plan §4.1). v2 sohbetinin
// SUNUMSAL kabuğu: coremetry Sheet'i, v1 çekmecesinin davranışıyla.
//   • modal={false}: sohbet sayfaya EŞLİK eder (v1 backdrop={false}) — perde
//     yok, arkası tıklanır/kaydırılır. Radix'in dışarı-tık kapatması
//     (onInteractOutside) engellenir; yerine v1 kuralı: yalnız BOŞ alana
//     tıklama kapatır (lib/emptyAreaClose).
//   • Esc: coremetry/overlay escBridge (tek Esc yığını, v1 useEscLayer ile aynı).
//   • Genişlik: v1 ile aynı anahtar + kelepçe (lib/cosre/sheetWidth); inline
//     style YOK — `--cosre-sheet-w` ref'ten yazılır, sınıf okur. <640px tam genişlik.
//   • Başlık: AiMark + "CoSRE" + Yeni + Tam sayfa (/cosre, v1) + ✕ (IA-2).
// B1 gövdesi boş durum + PASİF composer (gönderim yok; konuşma B2'de bağlanır).
// components/ui'den HİÇBİR şey içe aktarılmaz (uiImportRatchet, ui/ donmuş).
import { useCallback, useEffect, useRef } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '@/components/shadcn/button';
import { Textarea } from '@/components/shadcn/textarea';
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
}

export function CosreSheet({ open, onOpenChange, width, contentProps }: CosreSheetProps) {
  const panelRef = useRef<HTMLDivElement | null>(null);
  const setPanel = useCallback((el: HTMLDivElement | null) => {
    panelRef.current = el;
    el?.style.setProperty('--cosre-sheet-w', `${width}px`);
  }, [width]);

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
        onCloseAutoFocus={e => {
          // Odak FAB'a döner (kapanışta FAB yeniden çizilir); sayfada başka
          // bir öğeye tıklanarak kapandıysa odak orada kalır.
          e.preventDefault();
          const a = document.activeElement;
          if (a && a !== document.body && a.isConnected) return;
          requestAnimationFrame(() => document.querySelector<HTMLElement>('.cm-ai-fab')?.focus());
        }}
        className="w-full gap-0 p-0 sm:w-(--cosre-sheet-w) sm:max-w-none"
        {...contentProps}>
        <SheetHeader className="flex-row items-center gap-2 border-b px-4 py-3 pr-12">
          <AiMark size={20} />
          <SheetTitle className="flex-1">{T.title}</SheetTitle>
          <SheetDescription className="sr-only">{T.description}</SheetDescription>
          <Button variant="ghost" size="sm" disabled title={T.newChatTitle}>{T.newChat}</Button>
          <Button variant="ghost" size="sm" asChild>
            <Link to="/cosre" title={T.fullPageTitle}>{T.fullPage}</Link>
          </Button>
        </SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-2 p-6 text-center">
          <p className="text-sm font-semibold text-foreground">{T.emptyTitle}</p>
          <p className="text-sm text-muted-foreground">{T.emptyBody}</p>
        </div>
        {/* B1: pasif composer yerinde (form + gönder düğmesi B2'de, Composer.tsx). */}
        <div className="border-t p-3">
          <Textarea aria-label={T.composerLabel} placeholder={T.composerPlaceholder} rows={2} disabled />
        </div>
      </SheetContent>
    </Sheet>
  );
}
