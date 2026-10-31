// coremetry/overlay/Dialog — v0.10.1615 (shadcn Faz 1 R7, plan §7.2).
// shadcn dialog'un DENETİMLİ sarmalayıcısı; Esc lib/escLayer yığınına ait
// (escBridge.ts). Radix overlay'leri yalnız bu dizinden içe aktarılır
// (ESLint no-restricted-imports).
import type { ComponentProps, ReactNode } from 'react';
import { XIcon } from 'lucide-react';
import * as S from '@/components/shadcn/dialog';
import { cn } from '@/lib/cn';
import { OVERLAY_SURFACE } from './surface';
import { Button } from '@/components/shadcn/button';
import { CM_STRINGS } from '../strings';
import { EscBridgeProvider, useEscapeKeyDown, useOverlayEsc } from './escBridge';

export interface DialogProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  modal?: boolean;
  children?: ReactNode;
}

export function Dialog({ open, onOpenChange, modal, children }: DialogProps) {
  const esc = useOverlayEsc(open, onOpenChange);
  return (
    <EscBridgeProvider value={esc.onEscapeKeyDown}>
      <S.Dialog open={open} onOpenChange={esc.onOpenChange} modal={modal}>{children}</S.Dialog>
    </EscBridgeProvider>
  );
}

// v0.10.1619 (R10): üretilen dosyanın kapat düğmesi İngilizce "Close"
// taşır → üretileni kapatıp (showCloseButton={false}) aynı sınıflarla
// Türkçe etiketli düğmeyi burada çiziyoruz (coremetry/strings).
const CLOSE_CLASS = "absolute top-4 right-4 rounded-xs opacity-70 ring-offset-background transition-opacity hover:opacity-100 focus:ring-2 focus:ring-ring focus:ring-offset-2 focus:outline-hidden disabled:pointer-events-none data-[state=open]:bg-accent data-[state=open]:text-muted-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4";

export type DialogContentProps = ComponentProps<typeof S.DialogContent> & { closeLabel?: string };

export function DialogContent({ onEscapeKeyDown, showCloseButton = true, closeLabel = CM_STRINGS.close, className, children, ...props }: DialogContentProps) {
  return (
    <S.DialogContent {...props} className={cn(OVERLAY_SURFACE, className)} showCloseButton={false} onEscapeKeyDown={useEscapeKeyDown(onEscapeKeyDown)}>
      {children}
      {showCloseButton && (
        <S.DialogClose className={CLOSE_CLASS}>
          <XIcon />
          <span className="sr-only">{closeLabel}</span>
        </S.DialogClose>
      )}
    </S.DialogContent>
  );
}

export type DialogFooterProps = ComponentProps<typeof S.DialogFooter> & { closeLabel?: string };

export function DialogFooter({ showCloseButton = false, closeLabel = CM_STRINGS.close, children, ...props }: DialogFooterProps) {
  return (
    <S.DialogFooter {...props} showCloseButton={false}>
      {children}
      {showCloseButton && (
        <S.DialogClose asChild>
          <Button variant="outline">{closeLabel}</Button>
        </S.DialogClose>
      )}
    </S.DialogFooter>
  );
}

export { DialogTrigger, DialogClose, DialogHeader, DialogTitle, DialogDescription } from '@/components/shadcn/dialog';
