import { describe, it, expect } from 'vitest';
import { fmtEtaDays, fixedHalfEven, etaChipLabel, evaluatorQuietLabel } from './fmtEta';

// v0.10.901 — fmtDays (Go) ile aynı tablo: her birim bir vaka; tam-ikili
// yarımlar Go'nun çifte yuvarlamasıyla (10.5 saat → "10", 1.25 → "1.2").
describe('fmtEtaDays', () => {
  it.each([
    [0, '0 dakika'],
    [0.02, '29 dakika'],
    [0.5 / 24, '30 dakika'],
    [0.0417, '1 saat'],
    [0.375, '9 saat'],
    [0.4375, '10 saat'],   // 10.5 saat — tam yarım, çifte → 10 (Go ile aynı)
    [0.45139, '11 saat'],
    [0.9375, '22 saat'],   // 22.5 saat → 22
    [23.9 / 24, '24 saat'],
    [0.99, '24 saat'],
    [1, '1.0 gün'],
    [1.25, '1.2 gün'],     // tam yarım → çift
    [1.35, '1.4 gün'],     // ikili tam yarım değil → normal yuvarlama
    [1.5, '1.5 gün'],
    [1.94, '1.9 gün'],
    [2.25, '2.2 gün'],
    [4.95139, '5.0 gün'],
    [6.25, '6.2 gün'],     // Go TestFmtDays ile aynı vaka
    [9.99, '10.0 gün'],
    [10, '10 gün'],
    [10.5, '10 gün'],
    [12, '12 gün'],
    [12.5, '12 gün'],
    [12.6, '13 gün'],
    [42.6, '43 gün'],
  ])('%s gün → %s', (days, want) => {
    expect(fmtEtaDays(days)).toBe(want);
  });

  it('geçersiz değer sessiz', () => {
    expect(fmtEtaDays(NaN)).toBe('—');
    expect(fmtEtaDays(-1)).toBe('—');
    expect(fmtEtaDays(Infinity)).toBe('—');
  });
});

describe('fixedHalfEven', () => {
  it.each([
    [0.5, 0, '0'], [1.5, 0, '2'], [2.5, 0, '2'], [10.5, 0, '10'], [11.5, 0, '12'],
    [0.15, 1, '0.1'],   // 0.15 ikilide yarımın altı → aşağı (Go %.1f ile aynı)
    [0.25, 1, '0.2'], [0.35, 1, '0.3'], [0.75, 1, '0.8'], [1.05, 1, '1.1'],
    [2.675, 2, '2.67'], // klasik: ikilide 2.67499… → aşağı
    [1.125, 2, '1.12'], [1.375, 2, '1.38'],
  ])('%s @%d → %s', (x, d, want) => {
    expect(fixedHalfEven(x, d)).toBe(want);
  });
});

describe('etaChipLabel', () => {
  it('tavanda "dolu", aksi hâlde "≈ … kaldı"', () => {
    expect(etaChipLabel(0)).toBe('dolu (projeksiyon tavanda)');
    expect(etaChipLabel(1.5)).toBe('≈ 1.5 gün kaldı');
    expect(etaChipLabel(0.375)).toBe('≈ 9 saat kaldı');
  });
});

describe('evaluatorQuietLabel', () => {
  it('ok/yok → boş; stale → dk; failing/unknown → cümle', () => {
    expect(evaluatorQuietLabel(undefined)).toBe('');
    expect(evaluatorQuietLabel({ status: 'ok', ageSec: 30 })).toBe('');
    expect(evaluatorQuietLabel({ status: 'stale', ageSec: 610 })).toBe('değerlendirici 10 dk sessiz');
    expect(evaluatorQuietLabel({ status: 'stale', ageSec: -1 })).toBe('değerlendirici sessiz');
    expect(evaluatorQuietLabel({ status: 'failing', ageSec: 5 })).toBe('değerlendirici hata veriyor');
    expect(evaluatorQuietLabel({ status: 'unknown', ageSec: -1 })).toBe('değerlendirici durumu bilinmiyor');
  });
});
