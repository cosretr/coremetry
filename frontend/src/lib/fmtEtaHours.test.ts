import { describe, it, expect } from 'vitest';
import { fmtEtaHours, dbEtaChip } from './fmtEtaHours';

// v0.10.909 — her birim + her durum (v0.6.36 birim dersi).
describe('fmtEtaHours', () => {
  it.each([
    [0.004, '1 dk'], [0.5, '30 dk'], [0.99, '59 dk'], [1, '1.0 saat'], [3.46, '3.5 saat'],
    [9.94, '9.9 saat'], [10, '10 saat'], [23.6, '24 saat'],
  ])('%s → %s', (h, want) => expect(fmtEtaHours(h)).toBe(want));
  it('geçersiz', () => { expect(fmtEtaHours(NaN)).toBe('—'); expect(fmtEtaHours(-1)).toBe('—'); });
});

describe('dbEtaChip', () => {
  const base = { points: 25, source: 'vm', windowH: 2 } as const;
  it('ok ≤6 sa kırmızı, band title\'da', () => {
    const c = dbEtaChip({ ...base, status: 'ok', hours: 3.5, loHours: 2.8, hiHours: 4.6, r2: 0.93 });
    expect(c).toMatchObject({ badge: true, tone: 'b-err', text: '⌛ ≈ 3.5 saat · R² 0.93' });
    expect(c?.title).toMatch(/Aralık 2.8 saat – 4.6 saat/);
    expect(c?.title).toMatch(/VictoriaMetrics/);
  });
  it('ok 6–24 sa sarı; geniş band "N+"', () => {
    expect(dbEtaChip({ ...base, status: 'ok', hours: 18, loHours: 9, hiOpen: true, wide: true, r2: 0.7 }))
      .toMatchObject({ tone: 'b-warn', text: '⌛ 9.0 saat+ · R² 0.70' });
  });
  it('tavanda "dolu"; tahmin yok soluk + sebep; alan yoksa null', () => {
    expect(dbEtaChip({ ...base, status: 'at_limit' })).toMatchObject({ text: '⌛ dolu (eğilim tavanda)', tone: 'b-err' });
    expect(dbEtaChip({ ...base, status: 'none', reason: 'düz', source: 'ch' }))
      .toMatchObject({ badge: false, tone: 'b-gray', text: '⌛ yok · düz' });
    expect(dbEtaChip(undefined)).toBeNull();
  });
});
