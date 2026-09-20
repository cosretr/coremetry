// pagerPosition.test.ts — v0.10.831.
//
// Çivilenen kural: aynı `page` iki kipte İKİ AYRI şey demek. Değer+birim
// şablonlarının dersi (v0.6.36) burada "yön" birimiyle yaşıyor — tablo her
// iki dalı ve kipin döndüğü sınırı geziyor. İleri dal `null` döndürmeli,
// yoksa şerit numara kutusunu çizmeyi bırakır (operatörün kaybettiği
// affordance tam olarak o).
import { describe, it, expect } from 'vitest';
import { pagePositionLabel } from './pagerPosition';

describe('pagePositionLabel — konum metni (v0.10.831)', () => {
  const cases: [number, boolean, string | null][] = [
    // ileri kip: metin YOK (numara kutusu çizilir)
    [0, false, null], [1, false, null], [9, false, null], [999, false, null],
    // ters kip: 1 = son sayfa, sonrası "Sondan N."
    [0, true, 'Son sayfa'],
    [1, true, 'Sondan 2.'],
    [2, true, 'Sondan 3.'],
    [9, true, 'Sondan 10.'],
    [199, true, 'Sondan 200.'],
    // savunmacı: negatif / kesirli sayfa metni bozmaz
    [-3, true, 'Son sayfa'],
    [1.7, true, 'Sondan 2.'],
  ];
  for (const [page, reverse, want] of cases) {
    it(`page=${page} reverse=${reverse} → ${want ?? 'null'}`, () => {
      expect(pagePositionLabel(page, reverse)).toBe(want);
    });
  }

  it('kipin DÖNDÜĞÜ sınır: aynı sayfa, iki ayrı cevap', () => {
    expect(pagePositionLabel(3, false)).toBeNull();
    expect(pagePositionLabel(3, true)).toBe('Sondan 4.');
  });
});
