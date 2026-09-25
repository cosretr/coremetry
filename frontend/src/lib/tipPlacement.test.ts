import { describe, it, expect } from 'vitest';
import { placeTip, TIP_GAP, TIP_MARGIN } from './tipPlacement';

// v0.10.919 — Tooltip yerleşimi: üst tercih, sığmazsa alta çevir, yatayda
// ortala ve kıstır. Tablo-testli; DOM yok (jsdom ölçüm yapamaz).
const VP = { width: 1000, height: 800 };
const TIP = { width: 120, height: 30 };

describe('placeTip', () => {
  it('yer varken üstte ve çapaya ortalı', () => {
    const p = placeTip({ left: 440, top: 400, width: 120, height: 24 }, TIP, VP);
    expect(p).toEqual({ left: 440, top: 400 - TIP_GAP - 30, side: 'top' });
  });

  it('üstte yer yoksa alta çevrilir', () => {
    const p = placeTip({ left: 440, top: 10, width: 120, height: 24 }, TIP, VP);
    expect(p.side).toBe('bottom');
    expect(p.top).toBe(10 + 24 + TIP_GAP);
  });

  it('alt tercih edilir ama altta yer yoksa üste çevrilir', () => {
    const p = placeTip({ left: 440, top: 780, width: 120, height: 16 }, TIP, VP, 'bottom');
    expect(p.side).toBe('top');
  });

  it('sol kenarda kıstırılır', () => {
    expect(placeTip({ left: 0, top: 400, width: 20, height: 20 }, TIP, VP).left).toBe(TIP_MARGIN);
  });

  it('sağ kenarda kıstırılır', () => {
    expect(placeTip({ left: 990, top: 400, width: 10, height: 20 }, TIP, VP).left).toBe(VP.width - TIP_MARGIN - TIP.width);
  });

  it('viewport\'tan geniş kutu sol kenara yaslanır (negatif left yok)', () => {
    expect(placeTip({ left: 10, top: 400, width: 10, height: 20 }, { width: 2000, height: 30 }, VP).left).toBe(TIP_MARGIN);
  });

  it('iki taraf da sığmıyorsa geniş taraf seçilir ve dikeyde kıstırılır', () => {
    const tall = { width: 100, height: 700 };
    const p = placeTip({ left: 400, top: 100, width: 50, height: 20 }, tall, VP);
    expect(p.side).toBe('bottom');
    expect(p.top).toBeGreaterThanOrEqual(TIP_MARGIN);
    expect(p.top + tall.height).toBeLessThanOrEqual(VP.height - TIP_MARGIN);
  });

  it('ölçümsüz (jsdom sıfırları) güvenli sonuç verir', () => {
    const p = placeTip({ left: 0, top: 0, width: 0, height: 0 }, { width: 0, height: 0 }, VP);
    expect(p.left).toBe(TIP_MARGIN);
    expect(p.top).toBeGreaterThanOrEqual(TIP_MARGIN);
  });
});
