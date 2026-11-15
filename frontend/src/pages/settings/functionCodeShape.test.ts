import { describe, it, expect } from 'vitest';
import { summarizeShape } from './functionCodeShape';

// v0.10.1226 — function_code biçim ölçümü özeti.
describe('summarizeShape', () => {
  it('uyan/uymayan toplamları ve en çok 10 biçim dışı kova', () => {
    const rows = [
      { conforms: true, len: 7, distinct: 3000, spans: 9e6, example: 'BQRZ001' },
      ...Array.from({ length: 12 }, (_, i) => ({ conforms: false, len: 20 + i, distinct: 100 + i, spans: 50, example: 'x' })),
    ];
    const s = summarizeShape(rows);
    expect(s.okDistinct).toBe(3000);
    expect(s.offDistinct).toBe(Array.from({ length: 12 }, (_, i) => 100 + i).reduce((a, b) => a + b, 0));
    expect(s.offSpans).toBe(600);
    expect(s.topOff).toHaveLength(10);
    expect(s.topOff[0].len).toBe(31);
  });
  it('boş', () => {
    expect(summarizeShape([])).toEqual({ okDistinct: 0, okSpans: 0, offDistinct: 0, offSpans: 0, topOff: [] });
  });
});
