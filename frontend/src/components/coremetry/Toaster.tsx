// coremetry/Toaster — v0.10.1619 (shadcn Faz 1 R10, plan §1.4 A; §12 Q1).
// sonner çizici. TEMBEL: yalnız components/Toaster.tsx kapısı ilk toast'ta
// `import('./coremetry/Toaster')` ile çeker — sonner ilk yüke binmez
// (vite.config.ts `toast` grubu, scripts/bundle-budget.mjs yasak listesi).
// lib/toast.ts API'si DEĞİŞMEDİ: bu bileşen subscribeToasts'a abone olur ve
// her girdiyi sonner'a iletir; kapı ilk açılıştan önce gelenleri sıraya
// alır, burada aynen yeniden oynatılır.
//
// Registry'nin sonner.tsx'i next-themes ister (kurulu değil) → sonner
// paketi doğrudan; tema lib/theme.ts + useThemeTick. Renkler sonner'ın
// kendi CSS değişkenleri üzerinden Coremetry token'larına bağlı (sonner
// stilleri çalışma anında KATMANSIZ enjekte edilir; Tailwind yardımcısı
// onları yenemez, değişken ezmesi yener).
import { useEffect, type CSSProperties, type MutableRefObject } from 'react';
import { Toaster as Sonner, toast as sonner } from 'sonner';
import { subscribeToasts, type ToastEntry } from '@/lib/toast';
import { currentTheme, isLightTheme } from '@/lib/theme';
import { useThemeTick } from '@/lib/useThemeTick';
import { CM_STRINGS } from './strings';

const TOKENS = {
  zIndex: 'var(--z-toast)',
  fontFamily: 'inherit',
  '--normal-bg': 'var(--popover)',
  '--normal-border': 'var(--border)',
  '--normal-text': 'var(--popover-foreground)',
  '--success-bg': 'var(--status-ok-bg)',
  '--success-border': 'var(--status-ok-border)',
  '--success-text': 'var(--status-ok-fg)',
  '--error-bg': 'var(--status-critical-bg)',
  '--error-border': 'var(--status-critical-border)',
  '--error-text': 'var(--status-critical-fg)',
  '--warning-bg': 'var(--status-warn-bg)',
  '--warning-border': 'var(--status-warn-border)',
  '--warning-text': 'var(--status-warn-fg)',
  '--info-bg': 'var(--status-info-bg)',
  '--info-border': 'var(--status-info-fg)',
  '--info-text': 'var(--status-info-fg)',
  '--border-radius': 'var(--radius-sm)',
} as CSSProperties;

/** lib/toast girdisini sonner'a iletir (id korunur: aynı girdi iki kez çizilmez). */
export function showEntry(t: ToastEntry): void {
  sonner[t.kind](t.message, { id: t.id, duration: t.durationMs });
}

export interface CoremetryToasterProps {
  /** Kapının ilk açılıştan önce biriktirdiği girdiler — mount'ta yeniden oynatılır. */
  queue: MutableRefObject<ToastEntry[]>;
  /** true iken kapı sıraya almayı bırakır (bu bileşen kendisi abone). */
  live: MutableRefObject<boolean>;
}

export default function CoremetryToaster({ queue, live }: CoremetryToasterProps) {
  useThemeTick();
  // Çocuk <Sonner>'ın effect'i (ToastState aboneliği) bu effect'ten ÖNCE
  // koşar → yeniden oynatılan girdiler kaybolmaz.
  useEffect(() => {
    const unsub = subscribeToasts(showEntry);
    live.current = true;
    for (const t of queue.current.splice(0)) showEntry(t);
    return () => { live.current = false; unsub(); };
  }, [queue, live]);
  return (
    <Sonner
      theme={isLightTheme(currentTheme()) ? 'light' : 'dark'}
      position="top-right"
      richColors
      closeButton
      containerAriaLabel={CM_STRINGS.notifications}
      toastOptions={{ closeButtonAriaLabel: CM_STRINGS.closeToast }}
      style={TOKENS}
    />
  );
}
