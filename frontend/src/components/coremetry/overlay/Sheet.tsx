// coremetry/overlay/Sheet — v0.10.1615 (shadcn Faz 1 R7, plan §7.2). Drawer
// hedefi (sağ kenar). Denetimli; Esc lib/escLayer yığınına ait (escBridge).
import type { ComponentProps, ReactNode } from 'react';
import { XIcon } from 'lucide-react';
import * as S from '@/components/shadcn/sheet';
import { cn } from '@/lib/cn';
import { OVERLAY_SURFACE } from './surface';
import { CM_STRINGS } from '../strings';
import { EscBridgeProvider, useEscapeKeyDown, useOverlayEsc } from './escBridge';

export interface SheetProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  modal?: boolean;
  children?: ReactNode;
}

export function Sheet({ open, onOpenChange, modal, children }: SheetProps) {
  const esc = useOverlayEsc(open, onOpenChange);
  return (
    <EscBridgeProvider value={esc.onEscapeKeyDown}>
      <S.Sheet open={open} onOpenChange={esc.onOpenChange} modal={modal}>{children}</S.Sheet>
    </EscBridgeProvider>
  );
}

// v0.10.1619 (R10): üretilen kapat düğmesi İngilizce "Close" → kapatılır,
// aynı sınıflarla Türkçe etiketli düğme burada (coremetry/strings).
const CLOSE_CLASS = 'absolute top-4 right-4 rounded-xs opacity-70 ring-offset-background transition-opacity hover:opacity-100 focus:ring-2 focus:ring-ring focus:ring-offset-2 focus:outline-hidden disabled:pointer-events-none data-[state=open]:bg-secondary';

export type SheetContentProps = ComponentProps<typeof S.SheetContent> & { closeLabel?: string };

export function SheetContent({ onEscapeKeyDown, showCloseButton = true, closeLabel = CM_STRINGS.close, className, children, ...props }: SheetContentProps) {
  return (
    <S.SheetContent {...props} className={cn(OVERLAY_SURFACE, className)} showCloseButton={false} onEscapeKeyDown={useEscapeKeyDown(onEscapeKeyDown)}>
      {children}
      {showCloseButton && (
        <S.SheetClose className={CLOSE_CLASS}>
          <XIcon className="size-4" />
          <span className="sr-only">{closeLabel}</span>
        </S.SheetClose>
      )}
    </S.SheetContent>
  );
}

export { SheetTrigger, SheetClose, SheetHeader, SheetFooter, SheetTitle, SheetDescription } from '@/components/shadcn/sheet';
