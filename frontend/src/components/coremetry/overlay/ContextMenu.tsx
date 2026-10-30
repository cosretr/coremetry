// coremetry/overlay/ContextMenu — v0.10.1615 (shadcn Faz 1 R7, plan §7.2).
// Denetimli (Radix 1.8.1 ContextMenu kökü `open` alıyor); Esc lib/escLayer
// yığınına ait (escBridge). Alt menü içeriği de köprüye bağlı.
import type { ComponentProps, ReactNode } from 'react';
import * as S from '@/components/shadcn/context-menu';
import { EscBridgeProvider, useEscapeKeyDown, useOverlayEsc } from './escBridge';

export interface ContextMenuProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  modal?: boolean;
  children?: ReactNode;
}

export function ContextMenu({ open, onOpenChange, modal, children }: ContextMenuProps) {
  const esc = useOverlayEsc(open, onOpenChange);
  return (
    <EscBridgeProvider value={esc.onEscapeKeyDown}>
      <S.ContextMenu open={open} onOpenChange={esc.onOpenChange} modal={modal}>{children}</S.ContextMenu>
    </EscBridgeProvider>
  );
}

export function ContextMenuContent({ onEscapeKeyDown, ...props }: ComponentProps<typeof S.ContextMenuContent>) {
  return <S.ContextMenuContent {...props} onEscapeKeyDown={useEscapeKeyDown(onEscapeKeyDown)} />;
}

export function ContextMenuSubContent({ onEscapeKeyDown, ...props }: ComponentProps<typeof S.ContextMenuSubContent>) {
  return <S.ContextMenuSubContent {...props} onEscapeKeyDown={useEscapeKeyDown(onEscapeKeyDown)} />;
}

export {
  ContextMenuTrigger, ContextMenuGroup, ContextMenuSub, ContextMenuRadioGroup, ContextMenuSubTrigger,
  ContextMenuItem, ContextMenuCheckboxItem, ContextMenuRadioItem, ContextMenuLabel,
  ContextMenuSeparator, ContextMenuShortcut,
} from '@/components/shadcn/context-menu';
