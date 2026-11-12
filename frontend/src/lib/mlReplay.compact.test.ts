// v0.10.2032 — sade grafik: üst üste binen tetik etiketleri yerine tek bant,
// tek "tespit HH:MM" etiketi.
import { describe, it, expect } from 'vitest';
import { compactRegions } from './mlReplay';

describe('compactRegions', () => {
  const clock = (s: number) => (s === 600 ? '16:10' : '??');
  it('anomali bölgesi tek bant, tetikler düşer', () => {
    const r = compactRegions([
      { fromSec: 600, toSec: 1500, label: 'anomali' },
      { fromSec: 600, toSec: 900, label: 'tetik' },
      { fromSec: 900, toSec: 1200, label: 'tetik' },
    ], clock);
    expect(r).toEqual([{ fromSec: 600, toSec: 1500, label: 'tespit 16:10' }]);
  });
  it('bölge yoksa boş', () => {
    expect(compactRegions([], clock)).toEqual([]);
  });
});
