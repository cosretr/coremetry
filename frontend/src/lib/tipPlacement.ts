// tipPlacement.ts — v0.10.919 (Tooltip atomu, buton bütünlüğü Seçenek B).
//
// SAF: bir ELEMANA bağlı ipucu kutusunun viewport koordinatı. Kardeşi
// `chartTooltip.placeTooltip` imleç NOKTASINA ve kap koordinatına göre
// yerleştirir; buradaki çapa bir dikdörtgen ve koordinat viewport
// (Tooltip `position: fixed` çizilir).
//
// Kural: tercih edilen taraf (varsayılan üst) sığmıyorsa karşı tarafa
// çevrilir; ikisi de sığmıyorsa geniş olan seçilip dikeyde kıstırılır.
// Yatayda çapaya ortalanır ve `TIP_MARGIN` içinde kıstırılır.
//
// CSS'te `transform` ile ortalama YAPILMAZ: v0.9.631 dersi — JS'in
// kıstırdığı left/top'un üstüne binen bir transform kıstırmayı iptal eder
// (chartTooltipPlacement.test.ts). Hesap tam kutu köşesini verir.

export interface TipRect { left: number; top: number; width: number; height: number }
export interface TipSize { width: number; height: number }
export type TipSide = 'top' | 'bottom';
export interface TipPos { left: number; top: number; side: TipSide }

/** Çapa ile kutu arasındaki boşluk (px). */
export const TIP_GAP = 6;
/** Viewport kenarından en az uzaklık (px). */
export const TIP_MARGIN = 8;

export function placeTip(anchor: TipRect, tip: TipSize, viewport: TipSize, prefer: TipSide = 'top'): TipPos {
  const maxLeft = Math.max(TIP_MARGIN, viewport.width - TIP_MARGIN - tip.width);
  const centered = anchor.left + anchor.width / 2 - tip.width / 2;
  const left = Math.round(Math.min(Math.max(centered, TIP_MARGIN), maxLeft));

  const above = anchor.top - TIP_GAP - tip.height;
  const below = anchor.top + anchor.height + TIP_GAP;
  const fitsAbove = above >= TIP_MARGIN;
  const fitsBelow = below + tip.height <= viewport.height - TIP_MARGIN;

  let side: TipSide;
  if (prefer === 'top') side = fitsAbove || !fitsBelow ? 'top' : 'bottom';
  else side = fitsBelow || !fitsAbove ? 'bottom' : 'top';
  if (!fitsAbove && !fitsBelow) {
    // İkisi de sığmıyor: daha çok yer olan taraf, sonra dikey kıstırma.
    const roomAbove = anchor.top;
    const roomBelow = viewport.height - (anchor.top + anchor.height);
    side = roomAbove >= roomBelow ? 'top' : 'bottom';
  }
  const rawTop = side === 'top' ? above : below;
  const maxTop = Math.max(TIP_MARGIN, viewport.height - TIP_MARGIN - tip.height);
  const top = Math.round(Math.min(Math.max(rawTop, TIP_MARGIN), maxTop));
  return { left, top, side };
}
