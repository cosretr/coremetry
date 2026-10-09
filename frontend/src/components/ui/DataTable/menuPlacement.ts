// menuPlacement.ts — v0.10.939 (tablo standardı S8): başlık ⋯ menüsünün
// viewport koordinatı. SAF (DOM yok) — tablo-güdümlü test menuPlacement.test.
//
// Neden `placeTip` değil: ipucu çapaya ORTALANIR; menü tetiğin SAĞ kenarına
// hizalanır (tetik başlık satırının sağ ucunda, ortalanmış bir menü tablonun
// dışına taşardı). Kural: tercih ALT; sığmıyor ve üst sığıyorsa ÜST; ikisi de
// sığmıyorsa geniş taraf + dikey kıstırma. Yatayda kenar payı içinde kıstırılır.
// Menü `position: fixed` çizilir (HeadMenu.tsx): `.table-wrap`ın ve `th`nin
// `overflow`u onu kırpamaz — kısa bir tabloda bile menü satırların altına taşar.

import type { TipRect, TipSize } from '@/lib/tipPlacement';

/** Tetik ile menü arasındaki boşluk (px) — diğer açılır menülerin 4 px'i. */
export const MENU_GAP = 4;
/** Viewport kenarından en az uzaklık (px). */
export const MENU_MARGIN = 8;

export interface MenuPos { left: number; top: number; side: 'top' | 'bottom' }

/** v0.10.1141 — isteğe bağlı tercih: `prefer: 'top'` (composer'daki model
 *  menüsü yukarı açılır; yalnız üst sığmazsa alta), `align: 'start'` (menü
 *  çapanın SOL kenarına hizalı). Verilmezse eski kural aynen. */
export interface MenuPlaceOpts { prefer?: 'top' | 'bottom'; align?: 'start' | 'end' }

export function placeMenu(anchor: TipRect, menu: TipSize, viewport: TipSize, opts: MenuPlaceOpts = {}): MenuPos {
  const maxLeft = Math.max(MENU_MARGIN, viewport.width - MENU_MARGIN - menu.width);
  const rawLeft = opts.align === 'start' ? anchor.left : anchor.left + anchor.width - menu.width;
  const left = Math.round(Math.min(Math.max(rawLeft, MENU_MARGIN), maxLeft));

  const below = anchor.top + anchor.height + MENU_GAP;
  const above = anchor.top - MENU_GAP - menu.height;
  const fitsBelow = below + menu.height <= viewport.height - MENU_MARGIN;
  const fitsAbove = above >= MENU_MARGIN;
  let side: MenuPos['side'] = opts.prefer === 'top'
    ? (fitsAbove || !fitsBelow ? 'top' : 'bottom')
    : (fitsBelow || !fitsAbove ? 'bottom' : 'top');
  if (!fitsBelow && !fitsAbove) {
    const roomAbove = anchor.top;
    const roomBelow = viewport.height - (anchor.top + anchor.height);
    side = roomBelow >= roomAbove ? 'bottom' : 'top';
  }
  const rawTop = side === 'bottom' ? below : above;
  const maxTop = Math.max(MENU_MARGIN, viewport.height - MENU_MARGIN - menu.height);
  const top = Math.round(Math.min(Math.max(rawTop, MENU_MARGIN), maxTop));
  return { left, top, side };
}
