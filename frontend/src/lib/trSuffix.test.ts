// trSuffix.test.ts — Clusters S4 (v0.10.2169). Sözleşme: ek, sayının son
// SÖYLENEN sözcüğüne göre seçilir; sabit "'i" yazan cümle "3'i", "6'i" üretir.
import { describe, it, expect } from 'vitest';
import { trPoss, trPossLoc } from './trSuffix';

describe('trPoss — iyelik', () => {
  it.each([
    [1, "1'i"], [2, "2'si"], [3, "3'ü"], [4, "4'ü"], [5, "5'i"],
    [6, "6'sı"], [7, "7'si"], [8, "8'i"], [9, "9'u"],
    [10, "10'u"], [20, "20'si"], [30, "30'u"], [40, "40'ı"], [50, "50'si"],
    [60, "60'ı"], [70, "70'i"], [80, "80'i"], [90, "90'ı"],
    [100, "100'ü"], [1000, "1000'i"], [1_000_000, "1000000'u"], [1_000_000_000, "1000000000'ı"],
    [0, "0'ı"],
  ])('%i → %s', (n, want) => {
    expect(trPoss(n)).toBe(want);
  });

  it('çok basamaklıda son söylenen sözcük belirler', () => {
    expect(trPoss(12)).toBe("12'si"); // on iki
    expect(trPoss(16)).toBe("16'sı"); // on altı
    expect(trPoss(23)).toBe("23'ü"); // yirmi üç
    expect(trPoss(101)).toBe("101'i"); // yüz bir
    expect(trPoss(110)).toBe("110'u"); // yüz on
    expect(trPoss(140)).toBe("140'ı"); // yüz kırk
    expect(trPoss(200)).toBe("200'ü"); // iki yüz
    expect(trPoss(2000)).toBe("2000'i"); // iki bin
    expect(trPoss(2500)).toBe("2500'ü"); // iki bin beş yüz
    expect(trPoss(3_000_000)).toBe("3000000'u"); // üç milyon
  });

  it('kesir kırpılır', () => {
    expect(trPoss(3.9)).toBe("3'ü");
  });
});

describe('trPossLoc — iyelik + bulunma', () => {
  it.each([
    [1, "1'inde"], [2, "2'sinde"], [3, "3'ünde"], [4, "4'ünde"], [5, "5'inde"],
    [6, "6'sında"], [7, "7'sinde"], [8, "8'inde"], [9, "9'unda"],
    [10, "10'unda"], [20, "20'sinde"], [30, "30'unda"], [40, "40'ında"], [50, "50'sinde"],
    [60, "60'ında"], [70, "70'inde"], [80, "80'inde"], [90, "90'ında"],
    [100, "100'ünde"], [1000, "1000'inde"],
  ])('%i → %s', (n, want) => {
    expect(trPossLoc(n)).toBe(want);
  });
});
