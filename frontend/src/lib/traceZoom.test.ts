import { describe, it, expect } from 'vitest';
import { clampTraceZoom, clipBar, traceZoomCodec } from './traceZoom';

describe('traceZoomCodec', () => {
  it('gidiş-dönüş; bozuk / ters değer → null', () => {
    expect(traceZoomCodec.decode('100_2000')).toEqual([100, 2000]);
    expect(traceZoomCodec.encode([100.4, 2000])).toBe('100_2000');
    expect(traceZoomCodec.encode(null)).toBe('');
    expect(traceZoomCodec.decode(null)).toBeNull();
    expect(traceZoomCodec.decode('2000_100')).toBeNull();
    expect(traceZoomCodec.decode('abc')).toBeNull();
  });
});

describe('clampTraceZoom', () => {
  it('trace sınırına kırpar, tam trace → null, çok dar pencereyi genişletir', () => {
    expect(clampTraceZoom([-5, 500_000], 1e6)).toEqual([0, 500_000]);
    expect(clampTraceZoom([0, 5000], 1000_000)).toEqual([0, 5000]);
    expect(clampTraceZoom([0, 2e6], 1e6)).toBeNull();
    const z = clampTraceZoom([500_000, 500_001], 1e9)!;
    expect(z[1] - z[0]).toBe(5e6);
  });
});

describe('clipBar', () => {
  it('pencere içi / kısmen / dışarıda', () => {
    expect(clipBar(10, 20, 0, 100)).toEqual({ s: 10, e: 20, leftPct: 10, widthPct: 10 });
    expect(clipBar(-10, 20, 0, 100)).toEqual({ s: 0, e: 20, leftPct: 0, widthPct: 20 });
    expect(clipBar(150, 160, 0, 100)).toBeNull();
  });
});
