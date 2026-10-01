// errRateDelta.test — v0.10.1025 (Databases dilim 3). Err rate karosunun
// farkı YÜZDE PUAN; renk yalnız ≥ 0,05 pp kötüleşmede; iyileşme nötr;
// |Δ| < 0,005 pp çizilmez.
import { describe, it, expect } from 'vitest';
import { errRatePpDelta, ERR_PP_HIDE_BELOW, ERR_PP_WORSE_AT } from './errRateDelta';

describe('errRatePpDelta — yüzde puan, oran değil', () => {
  it('fark mutlak puan: %0,10 → %0,20 "+0.10 pp" (göreli +%100 DEĞİL)', () => {
    expect(errRatePpDelta(0.2, 0.1)).toEqual({ text: '+0.10 pp', worse: true, delta: 0.1 });
  });

  it('büyük oranda küçük göreli fark yine puanla okunur: %40 → %44 "+4.00 pp"', () => {
    expect(errRatePpDelta(44, 40)?.text).toBe('+4.00 pp');
  });

  it('iyileşme işaretli ama RENKSİZ (K5/T9)', () => {
    const d = errRatePpDelta(1.0, 1.42);
    expect(d?.text).toBe('-0.42 pp');
    expect(d?.worse).toBe(false);
  });

  it('küçük kötüleşme (< 0,05 pp) çizilir ama renk almaz', () => {
    const d = errRatePpDelta(0.53, 0.5);
    expect(d?.text).toBe('+0.03 pp');
    expect(d?.worse).toBe(false);
  });

  it('eşik tam 0,05 pp: kötüleşme sayılır — kayan nokta eşiği kaydırmaz', () => {
    // 0.15 - 0.10 === 0.04999999999999999 kayan noktada.
    expect(errRatePpDelta(0.15, 0.1)?.worse).toBe(true);
    expect(errRatePpDelta(0.1 + ERR_PP_WORSE_AT, 0.1)?.worse).toBe(true);
  });

  it('v0.10.1025 R6a — karar GÖSTERİLEN değerde: +0.0499 ve +0.05 aynı yazı, aynı renk', () => {
    const a = errRatePpDelta(1.0499, 1);
    const b = errRatePpDelta(1.05, 1);
    expect(a?.text).toBe('+0.05 pp');
    expect(b?.text).toBe('+0.05 pp');
    expect(a?.worse).toBe(true);
    expect(b?.worse).toBe(true);
    // Bir altı: "+0.04 pp" — nötr.
    expect(errRatePpDelta(1.0449, 1)).toMatchObject({ text: '+0.04 pp', worse: false });
    // İyileşme hangi büyüklükte olursa olsun renksiz.
    expect(errRatePpDelta(1, 1.0499)).toMatchObject({ text: '-0.05 pp', worse: false });
  });

  it('|Δ| < 0,005 pp çizilmez (iki ondalıkta 0.00 basardı)', () => {
    expect(errRatePpDelta(0.504, 0.5)).toBeNull();
    expect(errRatePpDelta(0.5, 0.504)).toBeNull();
    expect(errRatePpDelta(2, 2)).toBeNull();
    expect(errRatePpDelta(0, 0)).toBeNull();
  });

  it('eşik tam 0,005 pp: çizilir', () => {
    expect(errRatePpDelta(ERR_PP_HIDE_BELOW, 0)).not.toBeNull();
  });

  it('0 → pozitif: puan farkı, sonsuz oran değil', () => {
    expect(errRatePpDelta(1.25, 0)).toEqual({ text: '+1.25 pp', worse: true, delta: 1.25 });
  });

  it('sayı olmayan girdi çizilmez', () => {
    expect(errRatePpDelta(Number.NaN, 1)).toBeNull();
    expect(errRatePpDelta(1, Number.POSITIVE_INFINITY)).toBeNull();
  });
});
