import { createContext, useContext, type ReactNode } from 'react';

// coremetry/Confirm — Faz 2 dilim 1 R3. Bağlam + kanca (Radix'siz; kapı ve
// sayfalar bunu içe aktarır, çizici değil).

export interface ConfirmOptions {
  /** Soru cümlesi. Ör. "Remove cluster?" */
  title: string;
  /** ZORUNLU: neyin etkileneceği (ui/ConfirmDialog ile aynı tip kararı). */
  body: ReactNode;
  /** Eylemin kendisi ("Delete runbook"); varsayılan "OK" yok. */
  confirmLabel: string;
  cancelLabel?: string;
  /** Yıkıcı mı? Onay düğmesi destructive, değilse default. */
  danger?: boolean;
}

export const ConfirmCtx = createContext<((opts: ConfirmOptions) => Promise<boolean>) | null>(null);

/** `const confirm = useConfirm();` → `if (!await confirm({…})) return;` */
export function useConfirm(): (opts: ConfirmOptions) => Promise<boolean> {
  const fn = useContext(ConfirmCtx);
  if (!fn) throw new Error('useConfirm: <ConfirmGate> ağaçta yok');
  return fn;
}
