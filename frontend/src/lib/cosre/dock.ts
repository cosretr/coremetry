// dock — wave 3 (CoSRE yan paneli DOCKED). Panel açıkken kök öğe
// `data-cosre-docked` + `--cosre-dock-w` taşır; globals.css ≥1280 px'te
// #main'i o kadar daraltır → panel içeriğin ÜSTÜNE binmez, yanına oturur
// (Datadog Bits / Grafana yan paneli). Dar ekranda kural yok: panel eskisi
// gibi üstte (telefonda tam genişlik Sheet). Tek yazar: aynı anda tek panel.
import { useLayoutEffect } from 'react';

export const COSRE_DOCK_ATTR = 'data-cosre-docked';
export const COSRE_DOCK_VAR = '--cosre-dock-w';

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
