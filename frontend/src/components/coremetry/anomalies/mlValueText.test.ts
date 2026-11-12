// v0.10.2044 — cümle ile grafik aynı ölçüyü söyler: detay cümlesindeki değer
// panelin olay aralığındaki tepesiyle yazılır (birim, binlik, Türkçe ek).
// Sunucu biçimlemesinin aynası (internal/ml explain_text.go). Fikstür nötr.
import { describe, it, expect } from 'vitest';
import { numDative, peakSpan, spokenLastWord, trDec, valueText, withPanelPeak } from './mlValueText';

describe('Türkçe sayı + ek', () => {
  it.each<[number, string]>([[162, 'iki'], [2800, 'yüz'], [40000, 'bin'], [1150, 'elli'], [0, 'sıfır'], [3_000_000, 'milyon']])('%d → %s', (n, w) => {
    expect(spokenLastWord(n)).toBe(w);
  });
  it.each<[string, string]>([
    ['162', "162'ye"], ['2.800', "2.800'e"], ['40.000', "40.000'e"], ['1,34', "1,34'e"], ['6', "6'ya"], ['9', "9'a"], ['10', "10'a"], ['0', "0'a"],
  ])('%s → %s', (t, want) => { expect(numDative(t)).toBe(want); });
  it('binlik nokta, ondalık virgül, sondaki sıfır atılır', () => {
    expect(trDec(2800, 2)).toBe('2.800');
    expect(trDec(1340.5, 2)).toBe('1.340,5');
    expect(trDec(1.3399, 2)).toBe('1,34');
    expect(trDec(12345678, 2)).toBe('12.345.678');
  });
});

describe('birim', () => {
  it.each<[number, string | undefined, string, boolean]>([
    [2800, undefined, '2.800', false],
    [1.5 * 2 ** 30, 'bytes', '1,5 GB', true],
    [512 * 2 ** 20, 'bytes', '512 MB', true],
    [0.25, 's', '250 ms', true],
    [2.5, 's', '2,5 sn', true],
    [1250, 'ms', '1,3 sn', true],
    [0.42, 's/s', '0,42 sn/sn', true],
    [3.2, 'core', '3,2 çekirdek', true],
  ])('%d %s → %s', (v, u, text, hasUnit) => {
    expect(valueText(v, u)).toEqual({ text, hasUnit });
  });
});

describe('withPanelPeak', () => {
  const value = { text: "162'ye çıktı (5 dakikalık ölçüm)", measured: '162 (5 dakikalık ölçüm)', unit: '', direction: 'up' };
  const summary = "orders-consumer: Consumer lag 162'ye çıktı (5 dakikalık ölçüm); normalde hiç görülmez. 16:00'dan beri sürüyor (12 dakika).";
  it('değer parçası panel tepesiyle; geri kalan aynen', () => {
    expect(withPanelPeak(summary, value, 2800))
      .toBe("orders-consumer: Consumer lag 2.800'e çıktı (grafikteki tepe); normalde hiç görülmez. 16:00'dan beri sürüyor (12 dakika).");
  });
  it('birimli: seviyesine; düşüşte en düşük değer', () => {
    expect(peakSpan(1.5 * 2 ** 30, { unit: 'bytes', direction: 'up' })).toBe('1,5 GB seviyesine çıktı (grafikteki tepe)');
    expect(peakSpan(9, { unit: '', direction: 'down' })).toBe("9'a düştü (grafikteki en düşük değer)");
  });
  it('panel verisi yok / parça yok → sunucu cümlesi aynen', () => {
    expect(withPanelPeak(summary, value, null)).toBe(summary);
    expect(withPanelPeak(summary, undefined, 2800)).toBe(summary);
    expect(withPanelPeak('başka cümle', value, 2800)).toBe('başka cümle');
    expect(withPanelPeak(summary, value, Number.NaN)).toBe(summary);
  });
});
