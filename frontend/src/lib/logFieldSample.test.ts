// logFieldSample — v0.10.1852 (Log Search Faz 2 dilim 2.1).
import { describe, it, expect } from 'vitest';
import { fieldStatsEst, fieldSampleLabel, FIELD_SAMPLE_TARGET } from './logFieldSample';

describe('fieldStatsEst', () => {
  it('eşiğin altında / bilinmiyorsa gönderilmez (istek bugünkü gibi)', () => {
    expect(fieldStatsEst(undefined)).toBeUndefined();
    expect(fieldStatsEst(0)).toBeUndefined();
    expect(fieldStatsEst(FIELD_SAMPLE_TARGET - 1)).toBeUndefined();
    expect(fieldStatsEst(Number.NaN)).toBeUndefined();
  });
  it('eşik ve üstünde tam sayı', () => {
    expect(fieldStatsEst(FIELD_SAMPLE_TARGET)).toBe(200_000);
    expect(fieldStatsEst(12_480_000.7)).toBe(12_480_000);
  });
});

describe('fieldSampleLabel', () => {
  it('örneklenmediyse etiket yok', () => {
    expect(fieldSampleLabel(undefined)).toBeNull();
    expect(fieldSampleLabel({})).toBeNull();
    expect(fieldSampleLabel({ sampled: false, probability: 0.05 })).toBeNull();
  });
  it('yüzde basamakları', () => {
    expect(fieldSampleLabel({ sampled: true, probability: 0.05 })).toBe('~ örneklenmiş (%5)');
    expect(fieldSampleLabel({ sampled: true, probability: 0.5 })).toBe('~ örneklenmiş (%50)');
    expect(fieldSampleLabel({ sampled: true, probability: 0.002 })).toBe('~ örneklenmiş (%0,2)');
    expect(fieldSampleLabel({ sampled: true, probability: 0.0005 })).toBe('~ örneklenmiş (%0,05)');
  });
});
