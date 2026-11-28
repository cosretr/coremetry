import { describe, it, expect } from 'vitest';
import { count, compact, decimal, fixed, localize, sig3Plain } from './number';
import { duration, durationNs, durationSec } from './duration';
import { percent } from './percent';
import { bytes } from './bytes';
import { relTime, agoTime, absTime, clock, tsCompact } from './time';
import { errorTone } from './tone';
import type { Lang } from './lang';

// lib/format birim testleri — v0.10.2484 (denetim dilim 1). Her işlev × TR/EN;
// sınırlar: 0, birim altı, birim sınırında yuvarlama, negatif, NaN/undefined → "—",
// ondalık virgül kuralı (TR).

type Row = [unknown, Lang, string];

describe('localize / sig3Plain', () => {
  it.each([
    ['1234.5', 'tr', '1.234,5'], ['1234.5', 'en', '1,234.5'],
    ['-1234567', 'tr', '-1.234.567'], ['999', 'en', '999'], ['0.5', 'tr', '0,5'],
  ] as Row[])('localize(%s, %s) = %s', (s, l, want) => expect(localize(s as string, l)).toBe(want));

  it.each([
    [0, '0'], [1.2345, '1.23'], [12.345, '12.3'], [123.45, '123'], [1234.5, '1235'],
    [0.000123456, '0.000123'], [1.5, '1.5'], [2, '2'], [-12.345, '-12.3'], [0.999, '0.999'],
  ] as Array<[number, string]>)('sig3Plain(%s) = %s', (v, want) => expect(sig3Plain(v)).toBe(want));
});

describe('count / decimal / fixed', () => {
  it.each([
    [1234567, 'tr', '1.234.567'], [1234567, 'en', '1,234,567'], [0, 'tr', '0'],
    [999.6, 'en', '1,000'], [-4321, 'tr', '-4.321'], [NaN, 'tr', '—'], [undefined, 'en', '—'], [null, 'tr', '—'],
  ] as Row[])('count(%s, %s) = %s', (v, l, want) => expect(count(v as number, l)).toBe(want));

  it.each([
    [12.345, 'tr', '12,3'], [12.345, 'en', '12.3'], [0, 'en', '0'], [1234.5, 'tr', '1.235'],
    [Infinity, 'en', '—'],
  ] as Row[])('decimal(%s, %s) = %s', (v, l, want) => expect(decimal(v as number, l)).toBe(want));

  it('fixed: sabit hane + TR virgül', () => {
    expect(fixed(1234.5, 1, 'tr')).toBe('1.234,5');
    expect(fixed(1234.5, 1, 'en')).toBe('1,234.5');
    expect(fixed(0.125, 2, 'tr')).toBe('0,13');
    expect(fixed(undefined, 2, 'tr')).toBe('—');
  });
});

describe('compact', () => {
  it.each([
    [0, 'en', '0'], [999, 'tr', '999'], [12.345, 'tr', '12,3'],
    [1500, 'en', '1.5K'], [1500, 'tr', '1,5 bin'],
    [3.4e6, 'en', '3.4M'], [3.4e6, 'tr', '3,4 mn'],
    [2.8e9, 'en', '2.8B'], [2.8e9, 'tr', '2,8 mr'],
    [1e12, 'en', '1.0T'], [1e12, 'tr', '1,0 tn'],
    [-1500, 'en', '-1.5K'], [NaN, 'tr', '—'], [undefined, 'en', '—'],
  ] as Row[])('compact(%s, %s) = %s', (v, l, want) => expect(compact(v as number, l)).toBe(want));
});

describe('duration (ms, 3 anlamlı hane, tek birim)', () => {
  it.each([
    [0, 'tr', '0 ms'], [850, 'tr', '850 ms'], [850, 'en', '850 ms'],
    [1240, 'tr', '1,24 s'], [1240, 'en', '1.24 s'], [12_400, 'tr', '12,4 s'],
    [192_000, 'tr', '3,2 dk'], [192_000, 'en', '3.2 min'],
    [5_400_000, 'tr', '1,5 sa'], [5_400_000, 'en', '1.5 h'],
    [2 * 86_400_000, 'tr', '2 gün'], [2 * 86_400_000, 'en', '2 d'],
    [0.85, 'en', '850 µs'], [0.0005, 'tr', '500 ns'], [12.345, 'tr', '12,3 ms'],
    // birim sınırında yuvarlama üst birime taşır
    [999.7, 'en', '1 s'], [59_980, 'tr', '1 dk'], [0.9996, 'en', '1 ms'],
    [-850, 'tr', '-850 ms'], [NaN, 'tr', '—'], [undefined, 'en', '—'], [null, 'tr', '—'],
  ] as Row[])('duration(%s, %s) = %s', (v, l, want) => expect(duration(v as number, l)).toBe(want));

  it('durationNs / durationSec', () => {
    expect(durationNs(850, 'en')).toBe('850 ns');
    expect(durationNs(1_234_567, 'tr')).toBe('1,23 ms');
    expect(durationNs(2e9, 'en')).toBe('2 s');
    expect(durationSec(90, 'tr')).toBe('1,5 dk');
    expect(durationSec(undefined, 'tr')).toBe('—');
    expect(durationSec(0, 'en')).toBe('0 s');
  });
});

describe('percent (ölçek tipte zorunlu)', () => {
  it.each([
    [12.345, 'tr', 'pct', '%12,3'], [12.345, 'en', 'pct', '12.3%'],
    [0.12345, 'tr', 'ratio', '%12,3'], [0.12345, 'en', 'ratio', '12.3%'],
    [0, 'tr', 'pct', '%0'], [100, 'en', 'pct', '100%'], [0.0123, 'en', 'pct', '0.0123%'],
    [-5, 'tr', 'pct', '-%5'], [-5, 'en', 'pct', '-5%'], [1234, 'tr', 'pct', '%1.234'],
    [NaN, 'tr', 'pct', '—'], [undefined, 'en', 'ratio', '—'],
  ] as Array<[unknown, Lang, 'pct' | 'ratio', string]>)('percent(%s, %s, %s) = %s', (v, l, from, want) => {
    expect(percent(v as number, l, { from })).toBe(want);
  });

  it('digits: sabit ondalık', () => {
    expect(percent(1.5, 'tr', { from: 'pct', digits: 2 })).toBe('%1,50');
    expect(percent(0.015, 'en', { from: 'ratio', digits: 1 })).toBe('1.5%');
    expect(percent(-0.001, 'en', { from: 'pct', digits: 1 })).toBe('0.0%');
  });
});

describe('bytes (IEC)', () => {
  it.each([
    [0, 'en', '0 B'], [512, 'tr', '512 B'], [1536, 'en', '1.50 KiB'], [1536, 'tr', '1,50 KiB'],
    [5 * 1024 ** 3, 'en', '5.00 GiB'], [150 * 1024 ** 2, 'tr', '150 MiB'],
    [1023.9 * 1024, 'en', '1.00 MiB'], [-2048, 'tr', '-2,00 KiB'],
    [NaN, 'en', '—'], [undefined, 'tr', '—'],
  ] as Row[])('bytes(%s, %s) = %s', (v, l, want) => expect(bytes(v as number, l)).toBe(want));

  it('fixed hane', () => {
    expect(bytes(248.3 * 1024 ** 2, 'en', 1)).toBe('248.3 MiB');
    expect(bytes(248.3 * 1024 ** 2, 'tr', 1)).toBe('248,3 MiB');
  });
});

describe('relTime', () => {
  const now = 1_800_000_000_000;
  const ago = (s: number) => (now - s * 1000) * 1e6;
  it.each([
    [ago(30), 'tr', '30 sn önce'], [ago(30), 'en', '30 s ago'],
    [ago(3), 'tr', 'az önce'], [ago(3), 'en', 'just now'],
    [ago(300), 'tr', '5 dk önce'], [ago(300), 'en', '5 min ago'],
    [ago(3 * 3600), 'tr', '3 sa önce'], [ago(3 * 3600), 'en', '3 h ago'],
    [ago(86400), 'en', '1 day ago'], [ago(2 * 86400), 'tr', '2 gün önce'], [ago(2 * 86400), 'en', '2 days ago'],
    [ago(-300), 'tr', '5 dk sonra'], [ago(-300), 'en', 'in 5 min'],
    [0, 'tr', '—'], [NaN, 'en', '—'], [undefined, 'tr', '—'],
  ] as Row[])('relTime(%s, %s) = %s', (ns, l, want) => expect(relTime(ns as number, l, now)).toBe(want));

  it('agoTime: gelecek damga (saat kayması) "az önce"', () => {
    expect(agoTime(ago(-300), 'tr', now)).toBe('az önce');
    expect(agoTime(ago(-300), 'en', now)).toBe('just now');
    expect(agoTime(ago(300), 'en', now)).toBe('5 min ago');
    expect(agoTime(0, 'tr', now)).toBe('—');
  });
});

describe('mutlak zaman (izleyicinin saat dilimi)', () => {
  const d = new Date(2026, 10, 27, 9, 5, 7, 42);
  it('absTime / clock / tsCompact', () => {
    expect(absTime(d.getTime() * 1e6)).toBe('27.11.2026 09:05:07');
    expect(clock(d)).toBe('09:05:07');
    expect(tsCompact(d.getTime() * 1e6, d.getTime())).toBe('27.11 09:05:07');
    expect(absTime(0)).toBe('—');
  });
});

describe('errorTone (yüzde)', () => {
  it.each([
    [0, undefined], [0.99, undefined], [1, 'warn'], [4.99, 'warn'], [5, 'err'], [42, 'err'],
    [NaN, undefined], [undefined, undefined], [null, undefined],
  ] as Array<[unknown, string | undefined]>)('errorTone(%s) = %s', (v, want) => {
    expect(errorTone(v as number)).toBe(want);
  });
});
