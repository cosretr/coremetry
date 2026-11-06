// MLTraceDots — v0.10.1872: nokta grafiğinin verisi. Zaman artan (unix sn),
// çizginin üstü dolu / altı boş seri; çizgi yoksa hepsi "alt" (uydurma sınır yok).
import { describe, it, expect } from 'vitest';
import { dotSeries } from './MLTraceDots';

describe('dotSeries', () => {
  const dots = [{ t: 3000, ms: 700 }, { t: 1000, ms: 1500 }, { t: 2000, ms: 1200 }];
  it('sıralar ve çizgiye göre ayırır (eşit = alt)', () => {
    expect(dotSeries(dots, 1200)).toEqual({ times: [1, 2, 3], above: [1500, null, null], below: [null, 1200, 700] });
  });
  it('çizgi yoksa hepsi alt', () => {
    expect(dotSeries(dots, null).above).toEqual([null, null, null]);
  });
});
