import { useCallback, useEffect, useState } from 'react';

// deepThink.ts — v0.10.1150 "Derin düşün": composer'daki Derin anahtarı.
// VARSAYILAN KAPALI; durum kullanıcı başına localStorage'da (chatProfileStore /
// composerTools ile aynı anahtar biçimi). Depolama yoksa / atarsa: kapalı,
// anahtar yine bellekte çalışır. Açıkken istek gövdesine context.deep: true
// gider (internal/api/chat_deep.go); kapalıyken gövde bayt bayt eski.
//
// Aynı sekmedeki composer'lar (çekmece / /cosre / ✨ Explain sohbeti) aynı
// kullanıcı için senkron kalır (dinleyici kümesi).

const DEEP_PREFIX = 'cosre.deepThink.';

export const DEEP_THINK_TOOLTIP = 'Daha çok kaynak okur, daha çok adım atar; cevap daha yavaş gelir.';

export function deepThinkKey(user: string | undefined | null): string {
  return DEEP_PREFIX + (user || 'anon');
}

export function readDeepThink(user: string | undefined | null): boolean {
  try {
    return window.localStorage.getItem(deepThinkKey(user)) === '1';
  } catch {
    return false;
  }
}

const listeners = new Set<(user: string, on: boolean) => void>();

export function writeDeepThink(user: string | undefined | null, on: boolean): void {
  try { window.localStorage.setItem(deepThinkKey(user), on ? '1' : '0'); } catch { /* yok — bellekte */ }
  const me = user || 'anon';
  listeners.forEach(l => l(me, on));
}

/** Kullanıcının Derin tercihi + değiştirici (kalıcı, sekme içi senkron). */
export function useDeepThink(user: string | undefined | null): [boolean, (on: boolean) => void] {
  const [state, setState] = useState(() => ({ user: user || 'anon', on: readDeepThink(user) }));
  const me = user || 'anon';
  // Kimlik geç gelebilir (/api/auth/me): değişince kayıtlı tercih yeniden okunur.
  if (state.user !== me) setState({ user: me, on: readDeepThink(user) });
  useEffect(() => {
    const l = (u: string, on: boolean) => { if (u === me) setState({ user: me, on }); };
    listeners.add(l);
    return () => { listeners.delete(l); };
  }, [me]);
  const set = useCallback((on: boolean) => {
    setState({ user: me, on });
    writeDeepThink(me, on);
  }, [me]);
  return [state.user === me ? state.on : readDeepThink(user), set];
}
