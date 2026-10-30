import { describe, it, expect } from 'vitest';
import { formatChip, formatChips, parseChip, parseChips, FILTER_OPS, type FilterChip } from './filterChips';

// filterChips — v0.10.1617 (shadcn Faz 1 R9). Tablo güdümlü: HER işleç
// (değer+birim kuralının çip karşılığı — bir dalı atlamak, o dalın
// kodlayıcısını sınanmamış bırakır).
describe('filterChips kodlayıcı', () => {
  const ROUND: [FilterChip, string][] = [
    [{ key: 'service.name', op: '=', value: 'checkout' }, 'service.name=checkout'],
    [{ key: 'service.name', op: '!=', value: 'checkout' }, 'service.name!=checkout'],
    [{ key: 'http.route', op: '~', value: '/api/*' }, 'http.route~/api/*'],
    [{ key: 'http.route', op: '!~', value: '/health' }, 'http.route!~/health'],
    [{ key: 'db.system', op: 'exists' }, 'db.system exists'],
    [{ key: 'db.system', op: '!exists' }, 'db.system !exists'],
    [{ key: 'msg', op: '=', value: 'hello world' }, 'msg="hello world"'],
    [{ key: 'msg', op: '=', value: 'say "hi"' }, 'msg="say \\"hi\\""'],
    [{ key: 'path', op: '=', value: 'C:\\tmp' }, 'path="C:\\\\tmp"'],
    [{ key: 'expr', op: '~', value: 'a=b' }, 'expr~"a=b"'],
    [{ key: 'empty', op: '=', value: '' }, 'empty=""'],
    [{ key: 'k8s.pod', op: '=', value: 'çğüşıö-1' }, 'k8s.pod=çğüşıö-1'],
  ];

  it('her işleç tabloda en az bir kez', () => {
    expect(new Set(ROUND.map(([c]) => c.op))).toEqual(new Set(FILTER_OPS));
  });

  it.each(ROUND)('%o ⇄ %s', (chip, text) => {
    expect(formatChip(chip)).toBe(text);
    expect(parseChip(text)).toEqual(chip);
  });

  it('liste gidiş-dönüş (tırnak içi boşluk ve exists birlikte)', () => {
    const chips = ROUND.map(([c]) => c);
    const s = formatChips(chips);
    expect(parseChips(s)).toEqual(chips);
  });

  it('biçim dışı → null (sessiz düşürme yok)', () => {
    for (const bad of ['', 'noop', '=v', 'k=', 'k="open', 'k="a"b', 'k=a b', 'k=a"b', 'k maybe']) {
      expect(parseChip(bad), bad).toBeNull();
    }
    expect(parseChips('a=1 bad b=2')).toBeNull();
    expect(parseChips('a="unterminated')).toBeNull();
  });

  it('geçersiz anahtar biçimlenmez', () => {
    expect(() => formatChip({ key: 'a b', op: '=', value: 'x' })).toThrow();
    expect(() => formatChip({ key: '', op: 'exists' })).toThrow();
  });
});
