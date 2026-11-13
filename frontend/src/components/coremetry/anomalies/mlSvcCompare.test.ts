// v0.10.2089 — servis düzeyi karşılaştırma özeti (SAF).
import { describe, it, expect } from 'vitest';
import { SVC_DIFF_TR, svcCompareSummary } from './mlSvcCompare';

describe('svcCompareSummary', () => {
  it('sayılar ve kesik okuma', () => {
    expect(svcCompareSummary({ matched: 3, missed: 1 }, 14, false)).toBe('Son 14 gün: 3 eşleşti, 1 kaçırdı, 0 fazladan, 0 geç.');
    expect(svcCompareSummary({}, 7, true)).toMatch(/Okuma tavanına değdi/);
    expect(SVC_DIFF_TR.late).toBe('Geç (15 dk+)');
  });
});
