// menuPlacement.test.ts — v0.10.939 (tablo standardı S8): başlık ⋯ menüsünün
// saf yerleşimi. Menü tetiğin SAĞ kenarına hizalanır (ipucu gibi ortalanmaz),
// tercih ALT; viewport kenar payı içinde kıstırılır.
import { describe, it, expect } from 'vitest';
import { placeMenu, MENU_GAP, MENU_MARGIN } from './menuPlacement';

const VP = { width: 1000, height: 800 };
const MENU = { width: 190, height: 40 };

describe('placeMenu', () => {
  it.each([
    // [ad, çapa, beklenen]
    ['altta, sağ kenara hizalı', { left: 500, top: 100, width: 20, height: 20 }, { left: 520 - 190, top: 120 + MENU_GAP, side: 'bottom' }],
    ['sol kenara taşarsa kenar payında kıstırılır', { left: 10, top: 100, width: 20, height: 20 }, { left: MENU_MARGIN, top: 124, side: 'bottom' }],
    ['sağ kenardan taşamaz', { left: 990, top: 100, width: 20, height: 20 }, { left: 1000 - MENU_MARGIN - 190, top: 124, side: 'bottom' }],
    ['altta yer yoksa üste çevrilir', { left: 500, top: 760, width: 20, height: 20 }, { left: 330, top: 760 - MENU_GAP - 40, side: 'top' }],
  ] as const)('%s', (_name, anchor, want) => {
    expect(placeMenu(anchor, MENU, VP)).toEqual(want);
  });

  it('iki taraf da sığmıyorsa geniş taraf + dikey kıstırma', () => {
    const tall = { width: 190, height: 700 };
    const p = placeMenu({ left: 500, top: 300, width: 20, height: 20 }, tall, VP);
    expect(p.side).toBe('bottom'); // alt (480) > üst (300)
    expect(p.top).toBe(800 - MENU_MARGIN - 700);
  });
});
