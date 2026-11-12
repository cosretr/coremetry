// coremetry/dashboard/grid — v0.10.1999 (Dashboards Faz 3 dilim 3.5; audit
// §5.3 "Izgara: 12 kolon, akış sıralı (x/y yok)"; operatör S8).
//
// Panel JSON'unda isteğe bağlı `span` (2..12). Yoksa `width × 3` — eski
// 4 kolonluk panolar piksel yakın AYNI dizilir (1→3, 2→6, 3→9, 4→12); göç
// gerekmez (panel dizisi opak JSON). Serbest x/y yerleşim YOK: sıra panel
// dizisinin sırası, yalnız genişlik basamağı inceldi.
//
// Duyarlı kural eski .grid-4'ün aynısı: ≤1024 px iki kolon (span ≤6 → yarım,
// fazlası tam), ≤640 px tek kolon. Sınıf adları AŞAĞIDA TAM yazılı: Tailwind
// kaynağı (components/coremetry) yalnız düz metin sınıfları üretir;
// şablonla kurulan ad üretilmezdi.
import type { Panel } from '@/lib/types';

export const SPAN_MIN = 2;
export const SPAN_MAX = 12;
/** Düzenleyicide sunulan basamaklar (mockup görünüm 6: 4 · 6 · 8 · 12 + 3 · 9). */
export const SPAN_CHOICES = [3, 4, 6, 8, 9, 12] as const;

/** SAF: panelin 12 kolondaki genişliği. */
export function panelSpan(p: Pick<Panel, 'width' | 'span'>): number {
  if (typeof p.span === 'number' && Number.isFinite(p.span)) {
    return Math.max(SPAN_MIN, Math.min(SPAN_MAX, Math.round(p.span)));
  }
  const w = Math.max(1, Math.min(4, Math.round(p.width || 4)));
  return w * 3;
}

/** SAF: span → eski 1..4 genişlik (tip alanı zorunlu kalır; tahmini piksel
 *  hesapları ve eski okuyucular için en yakın basamak). */
export function widthForSpan(span: number): 1 | 2 | 3 | 4 {
  return Math.max(1, Math.min(4, Math.round(span / 3))) as 1 | 2 | 3 | 4;
}

const SPAN_CLASS: Record<number, string> = {
  2: 'col-span-2 max-lg:col-span-6 max-sm:col-span-12',
  3: 'col-span-3 max-lg:col-span-6 max-sm:col-span-12',
  4: 'col-span-4 max-lg:col-span-6 max-sm:col-span-12',
  5: 'col-span-5 max-lg:col-span-6 max-sm:col-span-12',
  6: 'col-span-6 max-lg:col-span-6 max-sm:col-span-12',
  7: 'col-span-7 max-lg:col-span-12 max-sm:col-span-12',
  8: 'col-span-8 max-lg:col-span-12 max-sm:col-span-12',
  9: 'col-span-9 max-lg:col-span-12 max-sm:col-span-12',
  10: 'col-span-10 max-lg:col-span-12 max-sm:col-span-12',
  11: 'col-span-11 max-lg:col-span-12 max-sm:col-span-12',
  12: 'col-span-12',
};

/** Izgara kabının sınıfı. */
export const DASH_GRID_CLASS = 'grid grid-cols-12 gap-3';

/** Panel kartının kolon sınıfı. */
export function panelSpanClass(p: Pick<Panel, 'width' | 'span'>): string {
  return SPAN_CLASS[panelSpan(p)];
}
