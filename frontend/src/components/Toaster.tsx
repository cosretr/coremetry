import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { subscribeToasts, type ToastEntry } from '@/lib/toast';

// Toaster — v0.10.1619 (shadcn Faz 1 R10, plan §1.4 A; §12 Q1). Kabuktaki
// tek toast yüzeyi artık bir KAPI: eager ve küçük, sonner'ı içe aktarmaz
// (eslint kabuk bloğu `sonner` yasağı; bundle-budget ilk yükte sonner'ı
// yasaklıyor). İlk toast'ta çiziciyi (coremetry/Toaster, sonner içinde)
// tembel çeker; o yüklenene kadar gelen girdiler sıraya alınır ve çizici
// mount olunca aynen yeniden oynatılır — CopilotChatGate (v0.10.1599)
// deseni. İlk toast bir chunk getirmesi kadar gecikir, sonrakiler anında.
// lib/toast.ts (toast.success/error/info/warning) değişmedi.
// Eski el yapımı çizici (v0.5.455) ve `.toast-*` CSS'i bu sürümde silindi.

const CoremetryToaster = lazy(() => import('./coremetry/Toaster'));

export function Toaster() {
  const [mounted, setMounted] = useState(false);
  const queue = useRef<ToastEntry[]>([]);
  const live = useRef(false);

  useEffect(() => subscribeToasts(t => {
    if (live.current) return; // çizici kendi aboneliğiyle alır
    queue.current.push(t);
    setMounted(true);
  }), []);

  if (!mounted) return null;
  return (
    <Suspense fallback={null}>
      <CoremetryToaster queue={queue} live={live} />
    </Suspense>
  );
}
