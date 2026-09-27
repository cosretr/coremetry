import { describe, it, expect } from 'vitest';
import { fmtBytesTr, fmtClockNs, fmtClockSec, fmtCores, fmtCoresShort, fmtDurNs, fmtPct, fmtRatio } from './traceMetricsFmt';

// v0.10.968 — ondalık VİRGÜL, yerel ayar API'si yok (TZ=UTC koşulur).
describe('traceMetricsFmt', () => {
  it('süre: ≥1 sn iki ondalık, altı ms', () => {
    expect(fmtDurNs(1.21e9)).toBe('1,21 sn');
    expect(fmtDurNs(1e9)).toBe('1,00 sn');
    expect(fmtDurNs(440e6)).toBe('440 ms');
    expect(fmtDurNs(999.4e6)).toBe('999 ms');
    expect(fmtDurNs(1.5e6)).toBe('1,5 ms');
    expect(fmtDurNs(0.35e6)).toBe('0,35 ms');
    expect(fmtDurNs(0)).toBe('0 ms');
  });
  it('çekirdek: uzun ve kısa biçim, sondaki sıfır atılır', () => {
    expect(fmtCores(0.97)).toBe('0,97 çekirdek');
    expect(fmtCores(1)).toBe('1 çekirdek');
    expect(fmtCores(0.5)).toBe('0,5 çekirdek');
    expect(fmtCores(0.004)).toBe('0,004 çekirdek');
    expect(fmtCoresShort(0.62)).toBe('0,62 c');
  });
  it('bellek: GiB / MiB / KiB / B', () => {
    expect(fmtBytesTr(1.42 * 1024 ** 3)).toBe('1,42 GiB');
    expect(fmtBytesTr(2 * 1024 ** 3)).toBe('2 GiB');
    expect(fmtBytesTr(512 * 1024 ** 2)).toBe('512 MiB');
    expect(fmtBytesTr(1.5 * 1024)).toBe('1,5 KiB');
    expect(fmtBytesTr(12)).toBe('12 B');
  });
  it('yüzde, oran', () => {
    expect(fmtPct(0.97)).toBe('%97');
    expect(fmtPct(1.244)).toBe('%124');
    expect(fmtRatio(2.5)).toBe('2,5 katı');
    expect(fmtRatio(1.24)).toBe('1,2 katı');
    expect(fmtRatio(2)).toBe('2 katı');
  });
  it('saat (yerel; test TZ=UTC)', () => {
    const ms = Date.UTC(2026, 8, 26, 12, 4, 31, 402);
    expect(fmtClockNs(ms * 1e6)).toBe('12:04:31.402');
    expect(fmtClockSec(Date.UTC(2026, 8, 26, 12, 16, 48) / 1000)).toBe('12:16:48');
  });
});
