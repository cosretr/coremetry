import { useSyncExternalStore } from 'react';
import { normalizeDensity, type Density } from './density';

// useDensity — v0.10.1615 (shadcn Faz 1 R7, plan §7.3, karar a, §12 Q9).
// `<html data-density>` (index.html açılış betiği + DensityToggle) için
// React girişi; yeni sağlayıcı yok, ad değişikliği yok. useThemeTick'in
// birebir biçimi: modül düzeyinde TEK MutationObserver +
// useSyncExternalStore. Okuma normalizeDensity'den (bilinmeyen değer →
// comfortable).
//
// DENSITY_SIZE (Q9): Coremetry yoğunluk adları KALIR; shadcn tarafı boyut
// adları comfortable → md, compact → sm, dense → xs (özel `xs` varyantı:
// shadcn/button.tsx cva `xs`). Eşleme tek yerde, burada; coremetry/*
// bileşikleri (DataTable satırı, FilterBar denetimleri, StatusBadge) bunu
// okur. Satır ritmi yine --row-h / --hit-min token'larından (globals.css).
export type DensitySize = 'md' | 'sm' | 'xs';
export const DENSITY_SIZE: Readonly<Record<Density, DensitySize>> = {
  comfortable: 'md',
  compact: 'sm',
  dense: 'xs',
};

const listeners = new Set<() => void>();
let observer: MutationObserver | null = null;

function read(): Density {
  if (typeof document === 'undefined') return 'comfortable';
  return normalizeDensity(document.documentElement.getAttribute('data-density'));
}

function subscribe(cb: () => void): () => void {
  if (!observer && typeof MutationObserver !== 'undefined') {
    observer = new MutationObserver(() => listeners.forEach(l => l()));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-density'] });
  }
  listeners.add(cb);
  return () => { listeners.delete(cb); };
}

export function useDensity(): Density {
  return useSyncExternalStore(subscribe, read, () => 'comfortable');
}

export function useDensitySize(): DensitySize {
  return DENSITY_SIZE[useDensity()];
}
