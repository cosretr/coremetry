// coremetry/overlay/Sheet — v0.10.1615 (shadcn Faz 1 R7, plan §7.2). Drawer
// hedefi (sağ kenar). Denetimli; Esc lib/escLayer yığınına ait (escBridge).
import type { ComponentProps, ReactNode } from 'react';
import * as S from '@/components/shadcn/sheet';
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

export function SheetContent({ onEscapeKeyDown, ...props }: ComponentProps<typeof S.SheetContent>) {
  return <S.SheetContent {...props} onEscapeKeyDown={useEscapeKeyDown(onEscapeKeyDown)} />;
}

export { SheetTrigger, SheetClose, SheetHeader, SheetFooter, SheetTitle, SheetDescription } from '@/components/shadcn/sheet';
