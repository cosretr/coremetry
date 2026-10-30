// coremetry/overlay/Dialog — v0.10.1615 (shadcn Faz 1 R7, plan §7.2).
// shadcn dialog'un DENETİMLİ sarmalayıcısı; Esc lib/escLayer yığınına ait
// (escBridge.ts). Radix overlay'leri yalnız bu dizinden içe aktarılır
// (ESLint no-restricted-imports).
import type { ComponentProps, ReactNode } from 'react';
import * as S from '@/components/shadcn/dialog';
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

export function DialogContent({ onEscapeKeyDown, ...props }: ComponentProps<typeof S.DialogContent>) {
  return <S.DialogContent {...props} onEscapeKeyDown={useEscapeKeyDown(onEscapeKeyDown)} />;
}

export { DialogTrigger, DialogClose, DialogHeader, DialogFooter, DialogTitle, DialogDescription } from '@/components/shadcn/dialog';
