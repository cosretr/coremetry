// coremetry/overlay/Popover — v0.10.1615 (shadcn Faz 1 R7, plan §7.2).
// Denetimli; Esc lib/escLayer yığınına ait (escBridge).
import type { ComponentProps, ReactNode } from 'react';
import * as S from '@/components/shadcn/popover';
import { cn } from '@/lib/cn';
import { OVERLAY_BORDER } from './surface';
import { EscBridgeProvider, useEscapeKeyDown, useOverlayEsc } from './escBridge';

export interface PopoverProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  modal?: boolean;
  children?: ReactNode;
}

export function Popover({ open, onOpenChange, modal, children }: PopoverProps) {
  const esc = useOverlayEsc(open, onOpenChange);
  return (
    <EscBridgeProvider value={esc.onEscapeKeyDown}>
      <S.Popover open={open} onOpenChange={esc.onOpenChange} modal={modal}>{children}</S.Popover>
    </EscBridgeProvider>
  );
}

export function PopoverContent({ onEscapeKeyDown, className, ...props }: ComponentProps<typeof S.PopoverContent>) {
  return <S.PopoverContent {...props} className={cn(OVERLAY_BORDER, className)} onEscapeKeyDown={useEscapeKeyDown(onEscapeKeyDown)} />;
}

export { PopoverTrigger, PopoverAnchor, PopoverHeader, PopoverTitle, PopoverDescription } from '@/components/shadcn/popover';
