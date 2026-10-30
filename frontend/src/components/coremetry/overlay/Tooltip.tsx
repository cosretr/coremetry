// coremetry/overlay/Tooltip — v0.10.1615 (shadcn Faz 1 R7, plan §7.2/§7.5).
// KENDİ TooltipProvider'ını taşır: kabukta küresel bir sağlayıcı Radix'i ilk
// yüke sokardı (plan §10). Açıklık isteğe bağlı denetimli (verilmezse iç
// durum) — ipucu çoğunlukla imleçle açılır; Esc yine yığına ait (escBridge),
// yani Drawer içindeki bir ipucunda Esc önce ipucunu kapatır.
import { useState, type ComponentProps, type ReactNode } from 'react';
import * as S from '@/components/shadcn/tooltip';
import { EscBridgeProvider, useEscapeKeyDown, useOverlayEsc } from './escBridge';

export interface TooltipProps {
  open?: boolean;
  onOpenChange?(open: boolean): void;
  delayDuration?: number;
  children?: ReactNode;
}

export function Tooltip({ open, onOpenChange, delayDuration, children }: TooltipProps) {
  const [inner, setInner] = useState(false);
  const isOpen = open ?? inner;
  const esc = useOverlayEsc(isOpen, (o) => { setInner(o); onOpenChange?.(o); });
  return (
    <S.TooltipProvider delayDuration={delayDuration}>
      <EscBridgeProvider value={esc.onEscapeKeyDown}>
        <S.Tooltip open={isOpen} onOpenChange={esc.onOpenChange}>{children}</S.Tooltip>
      </EscBridgeProvider>
    </S.TooltipProvider>
  );
}

export function TooltipContent({ onEscapeKeyDown, ...props }: ComponentProps<typeof S.TooltipContent>) {
  return <S.TooltipContent {...props} onEscapeKeyDown={useEscapeKeyDown(onEscapeKeyDown)} />;
}

export { TooltipTrigger } from '@/components/shadcn/tooltip';
