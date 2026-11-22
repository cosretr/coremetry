// dock — CoSRE yan paneli. v0.10.2459 (operatör 2026-11-22): varsayılan
// yeniden ÜSTTE (overlay) — sayfa genişliği değişmez. Wave 3'ün (v0.10.2413)
// docked kipi isteğe bağlı: panel başlığındaki "Sabitle" düğmesi; seçim
// izleyici başına localStorage'da (lib/storage try/catch sarar). Docked iken
// kök öğe `data-cosre-docked` + `--cosre-dock-w` taşır; globals.css ≥1280 px'te
// #main'i o kadar daraltır. Tek yazar: aynı anda tek panel.
import { useCallback, useLayoutEffect, useState } from 'react';
import { getRaw, setRaw } from '@/lib/storage';

export const COSRE_DOCK_ATTR = 'data-cosre-docked';
export const COSRE_DOCK_VAR = '--cosre-dock-w';
export const COSRE_DOCK_PREF_KEY = 'coremetry-cosre-docked';

export function useCosreDock(active: boolean, width: number): void {
  useLayoutEffect(() => {
    if (!active) return;
    const el = document.documentElement;
    el.setAttribute(COSRE_DOCK_ATTR, '');
    el.style.setProperty(COSRE_DOCK_VAR, `${Math.round(width)}px`);
    return () => {
      el.removeAttribute(COSRE_DOCK_ATTR);
      el.style.removeProperty(COSRE_DOCK_VAR);
    };
  }, [active, width]);
}

/** İzleyicinin "panel sabitli" tercihi; varsayılan false (overlay). */
export function useCosreDockPref(): [boolean, (v: boolean) => void] {
  const [docked, setDocked] = useState(() => getRaw(COSRE_DOCK_PREF_KEY) === '1');
  const set = useCallback((v: boolean) => { setDocked(v); setRaw(COSRE_DOCK_PREF_KEY, v ? '1' : '0'); }, []);
  return [docked, set];
}
