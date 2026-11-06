// timeStack — v0.10.1871: yığılı çubuk matrisi. Kümülatif üst + ayrı taban;
// yığın dışı seri (gölge çubuk, çizgi) ham kalır; null yığında 0.
import { describe, it, expect } from 'vitest';
import { stackMatrix } from './timeStack';

describe('stackMatrix', () => {
  it('ilk seri en altta, taban önceki toplam', () => {
    const m = stackMatrix([
      { type: 'bar', data: [7, 7, null] }, // gölge: yığın dışı
      { type: 'bar', stack: 'log', data: [1, 2, 3] },
      { type: 'bar', stack: 'log', data: [4, null, 5] },
      { type: 'line', stack: 'log', data: [9, 9, 9] }, // çizgi yığına girmez
    ]);
    expect(m.draw).toEqual([[7, 7, null], [1, 2, 3], [5, 2, 8], [9, 9, 9]]);
    expect(m.base).toEqual([null, [0, 0, 0], [1, 2, 3], null]);
  });
  it('ayrı yığın anahtarları birbirine eklenmez', () => {
    const m = stackMatrix([{ type: 'bar', stack: 'a', data: [1] }, { type: 'bar', stack: 'b', data: [2] }]);
    expect(m.draw).toEqual([[1], [2]]);
  });
});
