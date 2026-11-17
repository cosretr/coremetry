// aggResult.test.ts — v0.10.1505 regresyon: Aggregated sekmesinde boş cevap
// (`null`) hata satırı çiziyordu; gerçek hata sınıfı başlıkta söylenmiyordu.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { aggErrorClass, aggErrorHeadline, aggRowsOf } from './aggResult';

describe('aggResult — saf', () => {
  it('null / undefined cevap = sıfır grup (hata değil)', () => {
    expect(aggRowsOf(null)).toEqual([]);
    expect(aggRowsOf(undefined)).toEqual([]);
    const rows = [{ groupKey: 'x' }] as never;
    expect(aggRowsOf(rows)).toBe(rows);
  });
  it.each([
    ['Request timed out after 60s — try a narrower time range or fewer filters', 'timeout'],
    ['HTTP 500: code: 159, message: Timeout exceeded: elapsed 25.1 seconds', 'timeout'],
    ['HTTP 504: gateway', 'timeout'],
    ['HTTP 500: code: 241, message: Memory limit (total) exceeded', 'memory'],
    ['HTTP 400: invalid having JSON: unexpected end', 'invalid'],
    ['HTTP 500: code: 47, message: Missing columns', 'generic'],
    ['', 'generic'],
    [null, 'generic'],
  ] as const)('%s → %s', (msg, want) => {
    expect(aggErrorClass(msg)).toBe(want);
  });
  it('başlıklar sınıfa göre ayrışır; yalnız timeout "zaman aşımı" der', () => {
    expect(aggErrorHeadline('Request timed out after 60s')).toMatch(/zaman aşımına uğradı/);
    expect(aggErrorHeadline('HTTP 500: code: 47')).not.toMatch(/zaman aşımı/);
  });
  it('Traces.tsx cevabı aggRowsOf ile alır ve başlığı aggErrorHeadline ile kurar', () => {
    const src = readFileSync(resolve(__dirname, '../Traces.tsx'), 'utf8');
    expect(src).toContain('setAgg(aggRowsOf(a))');
    expect(src).toContain('message: aggErrorHeadline(aggErr, lang)'); // v0.10.2314 — arayüz dili
    expect(src).not.toContain('Toplu sorgu hata verdi ya da zaman aşımına uğradı');
  });
});
