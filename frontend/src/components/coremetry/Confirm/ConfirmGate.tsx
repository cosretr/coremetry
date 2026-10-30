import { lazy, Suspense, useCallback, useRef, useState, type ReactNode } from 'react';
import { ConfirmCtx, type ConfirmOptions } from './context';

// coremetry/Confirm/ConfirmGate — Faz 2 dilim 1 R3 (plan G2). shadcn
// alert-dialog üstündeki onay diyaloğunun KAPISI: eager ve küçük, Radix'i
// içe aktarmaz (kabuk ESLint bloğu bu dosyayı da kapsar; bundle-budget ilk
// yükte Radix'i yasaklar). İlk istekte çiziciyi (ConfirmDialog.tsx, Radix
// içinde) tembel çeker — ToasterGate / CopilotChatGate deseni.
//
// Sözleşme ui/ConfirmDialog ile AYNI (o atom bu dilimden sonra da yaşar):
//  - `useConfirm()(opts) → Promise<boolean>`; imza aynı (title, body,
//    confirmLabel, cancelLabel, danger);
//  - tek uçuş: açıkken gelen ikinci istek BİRİNCİYİ false ile kapatır
//    (askıda bir await görünmeyen bir hatadır);
//  - resolve yalnız BİR kez (Esc + Cancel + dış tık aynı anda gelebilir).
//
// App.tsx bu kapıyı eski ConfirmProvider'ın YANINA bağlar; bir sayfa iki
// kancadan YALNIZ birini kullanır (uiImportRatchet ConfirmDialog tavanı
// geçirilen sayfanın eskiye dönmesini kırmızı yapar).

type Pending = { opts: ConfirmOptions; resolve: (ok: boolean) => void };

const ConfirmDialog = lazy(() => import('./ConfirmDialog'));

export function ConfirmGate({ children }: { children: ReactNode }) {
  const [pending, setPending] = useState<Pending | null>(null);
  const pendingRef = useRef<Pending | null>(null);

  const settle = useCallback((ok: boolean) => {
    const p = pendingRef.current;
    pendingRef.current = null;
    setPending(null);
    p?.resolve(ok);
  }, []);

  const request = useCallback((opts: ConfirmOptions) => new Promise<boolean>(resolve => {
    if (pendingRef.current) pendingRef.current.resolve(false);
    const next = { opts, resolve };
    pendingRef.current = next;
    setPending(next);
  }), []);

  return (
    <ConfirmCtx.Provider value={request}>
      {children}
      {pending && (
        <Suspense fallback={null}>
          <ConfirmDialog opts={pending.opts} onSettle={settle} />
        </Suspense>
      )}
    </ConfirmCtx.Provider>
  );
}
