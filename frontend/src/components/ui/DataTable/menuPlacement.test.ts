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

  // v0.10.1141 — composer'daki model menüsü: tercih ÜST, çapanın SOL kenarına hizalı.
  it.each([
    ['prefer top: üst sığıyorsa üstte', { left: 40, top: 700, width: 80, height: 24 }, { left: 40, top: 700 - MENU_GAP - 40, side: 'top' }],
    ['prefer top: üst sığmıyorsa alta döner', { left: 40, top: 20, width: 80, height: 24 }, { left: 40, top: 44 + MENU_GAP, side: 'bottom' }],
    ['align start: sağ kenardan taşamaz (telefon genişliği)', { left: 950, top: 700, width: 40, height: 24 }, { left: 1000 - MENU_MARGIN - 190, top: 656, side: 'top' }],
  ] as const)('%s', (_name, anchor, want) => {
    expect(placeMenu(anchor, MENU, VP, { prefer: 'top', align: 'start' })).toEqual(want);
  });
  it('telefon: menü viewport\'tan geniş değilse kenar payında kalır', () => {
    const p = placeMenu({ left: 12, top: 600, width: 90, height: 28 }, { width: 280, height: 160 }, { width: 360, height: 640 }, { prefer: 'top', align: 'start' });
    expect(p.side).toBe('top');
    expect(p.left).toBeGreaterThanOrEqual(MENU_MARGIN);
    expect(p.left + 280).toBeLessThanOrEqual(360 - MENU_MARGIN);
  });
});
