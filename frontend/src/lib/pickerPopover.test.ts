import { describe, it, expect } from 'vitest';
import { placePickerPop, PICK_GAP } from './pickerPopover';
import { TIP_MARGIN } from './tipPlacement';

// v0.10.1089 — seçici açılır listesinin yerleşimi (operatör: liste sayfa
// kenarında kırpılıyordu). Kural: tercih alt, sığmazsa üst, ikisi de
// sığmazsa geniş taraf + yükseklik o boşluğa iner; çapa ASLA örtülmez;
// yatayda girdinin sol kenarına hizalı, viewport'a kıstırılmış.

const VP = { width: 1440, height: 900 };

describe('placePickerPop', () => {
  it.each([
    // ad, çapa, liste boyu, beklenen taraf
    ['bol yer → alt', { left: 100, top: 100, width: 220, height: 30 }, { width: 300, height: 400 }, 'bottom'],
    ['dipte → üste çevrilir', { left: 100, top: 800, width: 220, height: 30 }, { width: 300, height: 400 }, 'top'],
    ['ikisi de dar → geniş taraf (alt)', { left: 100, top: 300, width: 220, height: 30 }, { width: 300, height: 800 }, 'bottom'],
    ['ikisi de dar → geniş taraf (üst)', { left: 100, top: 560, width: 220, height: 30 }, { width: 300, height: 800 }, 'top'],
  ] as const)('%s', (_n, anchor, pop, side) => {
    const p = placePickerPop(anchor, pop, VP);
    expect(p.side).toBe(side);
    // Çapa örtülmez: liste ya tamamen altında ya tamamen üstünde.
    const h = Math.min(pop.height, p.maxHeight);
    if (side === 'bottom') expect(p.top).toBe(anchor.top + anchor.height + PICK_GAP);
    else expect(p.top + h).toBeLessThanOrEqual(anchor.top - PICK_GAP);
    // Viewport'un içinde kalır.
    expect(p.top).toBeGreaterThanOrEqual(TIP_MARGIN);
    expect(p.top + h).toBeLessThanOrEqual(VP.height - TIP_MARGIN);
  });

  it('sığmayan tarafta maxHeight boşluğa iner', () => {
    const p = placePickerPop({ left: 100, top: 300, width: 220, height: 30 }, { width: 300, height: 800 }, VP);
    expect(p.maxHeight).toBe(VP.height - TIP_MARGIN - (300 + 30 + PICK_GAP));
  });

  it('sol kenara hizalı; sağ kenarda viewport içine kıstırılır', () => {
    expect(placePickerPop({ left: 240, top: 100, width: 220, height: 30 }, { width: 300, height: 200 }, VP).left).toBe(240);
    expect(placePickerPop({ left: 1300, top: 100, width: 120, height: 30 }, { width: 400, height: 200 }, VP).left)
      .toBe(VP.width - TIP_MARGIN - 400);
    expect(placePickerPop({ left: -20, top: 100, width: 120, height: 30 }, { width: 200, height: 200 }, VP).left)
      .toBe(TIP_MARGIN);
  });
});
