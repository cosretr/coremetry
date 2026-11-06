import { describe, it, expect } from 'vitest';
import { ratioPhrase } from './ratioPhrase';

// v0.10.1896 — oran ifadesi merdiveni (Go ml.RatioPhrase kısa biçimiyle aynı eşikler).
describe('ratioPhrase', () => {
  it.each([
    [10, 10, 'normal aralıkta'],
    [11.1, 10, 'normal aralıkta'],
    [12, 10, 'biraz yüksek'],
    [15, 10, 'yaklaşık 1,5 kat'],
    [18, 10, 'yaklaşık 2 kat'],
    [34, 10, 'yaklaşık 3 kat'],
    [200, 10, 'yaklaşık 20 kat'],
    [201, 10, '20 kattan fazla'],
    [5, 0, 'normalde hiç görülmez'],
    [0, 0, 'normal aralıkta'],
    [0, 10, 'durdu'],
    [8, 10, 'biraz düşük'],
    [6, 10, 'yaklaşık 1,5 kat az'],
    [3, 10, 'yaklaşık 3 kat az'],
    [0.4, 10, 'neredeyse durdu'],
  ])('%s / %s → %s', (o, b, want) => expect(ratioPhrase(o, b)).toBe(want));
  it('asla "×" ya da ondalık oran yazmaz', () => {
    for (let o = 0; o <= 300; o += 7) expect(ratioPhrase(o, 10)).not.toMatch(/×|\d\.\d/);
  });
});
