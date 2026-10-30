// coremetry/overlay/DropdownMenu — v0.10.1615 (shadcn Faz 1 R7, plan §7.2).
// Denetimli; Esc lib/escLayer yığınına ait (escBridge). Alt menü içeriği de
// köprüye bağlı: Radix alt menüde Esc'te kök menüyü kapatıp olayı
// preventDefault ediyor — köprüsüz o da yığını atlardı.
import type { ComponentProps, ReactNode } from 'react';
import * as S from '@/components/shadcn/dropdown-menu';
import { EscBridgeProvider, useEscapeKeyDown, useOverlayEsc } from './escBridge';

export interface DropdownMenuProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  modal?: boolean;
  children?: ReactNode;
}

export function DropdownMenu({ open, onOpenChange, modal, children }: DropdownMenuProps) {
  const esc = useOverlayEsc(open, onOpenChange);
  return (
    <EscBridgeProvider value={esc.onEscapeKeyDown}>
      <S.DropdownMenu open={open} onOpenChange={esc.onOpenChange} modal={modal}>{children}</S.DropdownMenu>
    </EscBridgeProvider>
  );
}

export function DropdownMenuContent({ onEscapeKeyDown, ...props }: ComponentProps<typeof S.DropdownMenuContent>) {
  return <S.DropdownMenuContent {...props} onEscapeKeyDown={useEscapeKeyDown(onEscapeKeyDown)} />;
}

export function DropdownMenuSubContent({ onEscapeKeyDown, ...props }: ComponentProps<typeof S.DropdownMenuSubContent>) {
  return <S.DropdownMenuSubContent {...props} onEscapeKeyDown={useEscapeKeyDown(onEscapeKeyDown)} />;
}

export {
  DropdownMenuTrigger, DropdownMenuGroup, DropdownMenuItem, DropdownMenuCheckboxItem,
  DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuLabel, DropdownMenuSeparator,
  DropdownMenuShortcut, DropdownMenuSub, DropdownMenuSubTrigger,
} from '@/components/shadcn/dropdown-menu';
