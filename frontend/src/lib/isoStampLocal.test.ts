// v0.10.1794 — Operator-reported: log detayındaki ES UTC damgaları ("Z")
// ham basılıyordu, tabloyla 3 saat farklı okundu. Yerel saat, ms korunur;
// damga olmayan değer null (ham kalır).
import { describe, it, expect } from 'vitest';
import { isoStampLocal, fmtDateTime } from './utils';

describe('isoStampLocal (v0.10.1794)', () => {
  it('Z / ±hh:mm / ±hhmm / virgüllü ms → yerel dd.mm.yyyy HH:mm:ss.mmm', () => {
    const ms = Date.UTC(2026, 10, 5, 7, 23, 41, 67);
    const want = fmtDateTime(ms) + '.067';
    expect(isoStampLocal('2026-11-05T07:23:41.067687331Z')).toBe(want);
    expect(isoStampLocal('2026-11-05T10:23:41.067+03:00')).toBe(want);
    expect(isoStampLocal('2026-11-05T10:23:41,067+0300')).toBe(want);
    expect(isoStampLocal('2026-11-05T07:23:41Z')).toBe(fmtDateTime(ms - 67) + '.000');
  });
  it('damga olmayan değerler null', () => {
    for (const v of ['container', '2026-11-05', '1793852621693', 'KafkaConsumerProxy.java:46', '2026-11-05T07:23:41']) {
      expect(isoStampLocal(v), v).toBeNull();
    }
  });
});
