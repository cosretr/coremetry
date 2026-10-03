// pickerPopover.ts — v0.10.1089 (seçici açılır listesi: ortak popover). Saf
// yardımcılar: yerleşim (placePickerPop) + satır kimliği (pickerOptionId).
//
// SAF: bir seçici girdisine (çapa) bağlı açılır listenin viewport
// koordinatı + o tarafta kullanılabilecek en büyük yükseklik. Kardeşleri:
// `placeTip` (ipucu — çapaya ORTALANIR) ve `placeMenu` (⋯ menüsü — çapanın
// SAĞ kenarına hizalanır). Seçici listesi girdinin SOL kenarına hizalanır
// (yazılan metnin hemen altında okunur) ve ikisinden farklı olarak çapayı
// ASLA örtmez: alta da üste de sığmıyorsa liste kıstırılmaz, geniş tarafa
// konur ve yüksekliği o tarafın boşluğuna indirilir — operatör yazarken
// girdisinin üstüne binen bir liste, yazdığını göremez demektir.
//
// Kural: tercih ALT; liste alta sığmıyor ve üste sığıyorsa ÜST; ikisine de
// sığmıyorsa boşluğu geniş olan taraf + `maxHeight` o boşluğa iner. Yatayda
// kenar payı içinde kıstırılır (dar ekranda sağ kenarda kesilmez).

import { TIP_MARGIN, type TipRect, type TipSize } from '@/lib/tipPlacement';

/** Girdi ile liste arasındaki boşluk (px) — diğer açılır yüzeylerin 4 px'i. */
export const PICK_GAP = 4;

export interface PickPos {
  left: number;
  top: number;
  side: 'top' | 'bottom';
  /** Seçilen tarafta listenin alabileceği en büyük yükseklik (px). */
  maxHeight: number;
}

export function placePickerPop(anchor: TipRect, pop: TipSize, viewport: TipSize): PickPos {
  const maxLeft = Math.max(TIP_MARGIN, viewport.width - TIP_MARGIN - pop.width);
  const left = Math.round(Math.min(Math.max(anchor.left, TIP_MARGIN), maxLeft));

  const roomBelow = Math.max(0, viewport.height - TIP_MARGIN - (anchor.top + anchor.height + PICK_GAP));
  const roomAbove = Math.max(0, anchor.top - PICK_GAP - TIP_MARGIN);
  let side: PickPos['side'];
  if (pop.height <= roomBelow) side = 'bottom';
  else if (pop.height <= roomAbove) side = 'top';
  else side = roomBelow >= roomAbove ? 'bottom' : 'top';

  const room = side === 'bottom' ? roomBelow : roomAbove;
  const maxHeight = Math.floor(room);
  const height = Math.min(pop.height, maxHeight);
  const top = side === 'bottom'
    ? Math.round(anchor.top + anchor.height + PICK_GAP)
    : Math.round(anchor.top - PICK_GAP - height);
  return { left, top, side, maxHeight };
}

/** Satırın DOM id'si — input'un `aria-activedescendant`ı bununla kurulur. */
export const pickerOptionId = (listId: string, i: number) => `${listId}-opt-${i}`;
