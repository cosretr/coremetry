// coremetry/overlay/AlertDialog — Faz 2 dilim 1 R3 (plan G2). shadcn
// alert-dialog'un DENETİMLİ sarmalayıcısı; Esc lib/escLayer yığınına ait
// (escBridge.ts — Dialog/Sheet ile aynı desen). Radix AlertDialog dış
// tıkla KAPANMAZ (onay diyaloğu: yalnız Cancel / Action / Esc) ve açılışta
// odağı Cancel'a verir (Enter'a hazır parmak yıkıcı yolu seçmesin).
import type { ComponentProps, ReactNode } from 'react';
import * as S from '@/components/shadcn/alert-dialog';
import { cn } from '@/lib/cn';
import { OVERLAY_SURFACE } from './surface';
import { EscBridgeProvider, useEscapeKeyDown, useOverlayEsc } from './escBridge';

export interface AlertDialogProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  children?: ReactNode;
}

export function AlertDialog({ open, onOpenChange, children }: AlertDialogProps) {
  const esc = useOverlayEsc(open, onOpenChange);
  return (
    <EscBridgeProvider value={esc.onEscapeKeyDown}>
      <S.AlertDialog open={open} onOpenChange={esc.onOpenChange}>{children}</S.AlertDialog>
    </EscBridgeProvider>
  );
}

export type AlertDialogContentProps = ComponentProps<typeof S.AlertDialogContent>;

export function AlertDialogContent({ onEscapeKeyDown, className, ...props }: AlertDialogContentProps) {
  return <S.AlertDialogContent {...props} className={cn(OVERLAY_SURFACE, className)} onEscapeKeyDown={useEscapeKeyDown(onEscapeKeyDown)} />;
}

export {
  AlertDialogHeader, AlertDialogFooter, AlertDialogTitle, AlertDialogDescription,
  AlertDialogAction, AlertDialogCancel,
} from '@/components/shadcn/alert-dialog';
