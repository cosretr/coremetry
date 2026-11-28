import { describe, it, expect } from 'vitest';
import { stripNotifySrc } from './notifySrc';
import { pageContext } from './pageContext';

// v0.10.2484 — bildirim kaynağı işareti güvenle düşer: diğer parametreler ve
// derin bağlantı (?problem=) korunur; işaretsiz adres dokunulmaz; sohbet
// bağlamı parametreyi hiç taşımaz (sorgu / önbellek anahtarına girmez).
describe('stripNotifySrc', () => {
  it('yalnız src=notify silinir, kalan parametreler sırayla korunur', () => {
    expect(stripNotifySrc('?problem=anomaly-auto%3Asvc%3Aorders-api&src=notify')).toBe('?problem=anomaly-auto%3Asvc%3Aorders-api');
    expect(stripNotifySrc('?src=notify')).toBe('');
    expect(stripNotifySrc('src=notify&tab=signals')).toBe('?tab=signals');
  });
  it('işaret yoksa ya da başka değerse dokunulmaz', () => {
    expect(stripNotifySrc('?problem=p1')).toBeNull();
    expect(stripNotifySrc('?problem=p1&src=other')).toBeNull();
    expect(stripNotifySrc('')).toBeNull();
  });
  it('sayfa bağlamı src taşımaz (deep link aynı bağlamı verir)', () => {
    expect(pageContext('/problems', '?problem=p1&src=notify')).toEqual(pageContext('/problems', '?problem=p1'));
  });
});
