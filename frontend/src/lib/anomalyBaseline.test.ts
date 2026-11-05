// v0.10.1792 — Davis F1.4: tabansızlık + oran etiketi (backend
// chstore.AnomalyBaselineless ikizi). Eski satır (baselineKind yok) peakRatio
// 0'dan okunur; tabanlı satırda 0 "sıfıra düştü" → "×0.0" doğru.
import { describe, it, expect } from 'vitest';
import { anomalyBaselineless, anomalyRatioLabel } from './anomalyBaseline';
import { anomalyDirectionBaselineLine } from '@/features/anomalies/anomalyDetail';

describe('anomalyBaseline', () => {
  it.each([
    [{ peakRatio: 0 }, true],
    [{ peakRatio: 3 }, false],
    [{ peakRatio: 0, baselineKind: 'none' as const }, true],
    [{ peakRatio: 4, baselineKind: 'none' as const }, true],
    [{ peakRatio: 0, baselineKind: 'seasonal' as const }, false],
  ])('%o → tabansız %s', (e, want) => {
    expect(anomalyBaselineless(e)).toBe(want);
  });

  it('oran etiketi', () => {
    expect(anomalyRatioLabel({ peakRatio: 0 })).toBe('taban yok');
    expect(anomalyRatioLabel({ peakRatio: 4.24 })).toBe('×4.2');
    expect(anomalyRatioLabel({ peakRatio: 0, baselineKind: 'consecutive' })).toBe('×0.0');
  });

  it('yön · taban satırı', () => {
    expect(anomalyDirectionBaselineLine({})).toBe('bilinmiyor');
    expect(anomalyDirectionBaselineLine({ direction: 'up' })).toBe('artış · taban bilinmiyor');
    expect(anomalyDirectionBaselineLine({ baselineKind: 'consecutive', baselineValue: 4 })).toBe('yön bilinmiyor · son 24 saatin tabanı 4');
    expect(anomalyDirectionBaselineLine({ direction: 'down', baselineKind: 'none', baselineValue: 9 })).toBe('düşüş · taban yok (ilk görülme)');
  });
});
