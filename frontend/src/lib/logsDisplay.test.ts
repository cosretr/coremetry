import { describe, it, expect } from 'vitest';
import { readLogsDisplay, writeLogsDisplay, pageSlice, LOGS_DEFAULT_DISPLAY, parseLines, parseDensity, parsePageSize } from './logsDisplay';

// v0.10.1830 — doküman tablosu görünüm parametreleri (rh / hh / den / pp).
describe('readLogsDisplay', () => {
  it('parametre yok → taban (bugünkü görünüm), açık anahtar yok', () => {
    expect(readLogsDisplay(new URLSearchParams(''))).toEqual({ display: LOGS_DEFAULT_DISPLAY, explicit: [] });
    const wrapped = { ...LOGS_DEFAULT_DISPLAY, rowLines: 'auto' as const };
    expect(readLogsDisplay(new URLSearchParams('q=x'), wrapped).display.rowLines).toBe('auto');
  });
  it('geçerli değerler okunur ve açık sayılır', () => {
    expect(readLogsDisplay(new URLSearchParams('rh=2&hh=auto&den=compact&pp=250'))).toEqual({
      display: { rowLines: 2, headerLines: 'auto', density: 'compact', pageSize: 250 },
      explicit: ['rh', 'hh', 'den', 'pp'],
    });
  });
  it.each([
    ['rh=4', 'rh'], ['rh=0', 'rh'], ['hh=foo', 'hh'], ['den=tight', 'den'], ['pp=99', 'pp'], ['pp=', 'pp'],
  ])('bilinmeyen değer (%s) tabana düşer ve açık sayılmaz', (q, k) => {
    const r = readLogsDisplay(new URLSearchParams(q));
    expect(r.display).toEqual(LOGS_DEFAULT_DISPLAY);
    expect(r.explicit).not.toContain(k);
  });
});

describe('writeLogsDisplay', () => {
  it('yabancı parametreler korunur; açık seçim varsayılana eşit olsa da yazılır', () => {
    const out = writeLogsDisplay(new URLSearchParams('q=level%3Aerror&range=1h'), { rowLines: 1, density: 'expanded' });
    expect(out.get('q')).toBe('level:error');
    expect(out.get('range')).toBe('1h');
    expect(out.get('rh')).toBe('1');
    expect(out.get('den')).toBe('expanded');
    expect(out.has('hh')).toBe(false);
  });
  it('yaz → oku döngüsü', () => {
    const d = { rowLines: 3 as const, headerLines: 2 as const, density: 'normal' as const, pageSize: 50 as const };
    expect(readLogsDisplay(writeLogsDisplay(new URLSearchParams(), d)).display).toEqual(d);
  });
});

describe('parse yardımcıları', () => {
  it('kenarlar', () => {
    expect([parseLines('1'), parseLines('3'), parseLines('auto'), parseLines('AUTO'), parseLines(null)]).toEqual([1, 3, 'auto', null, null]);
    expect([parseDensity('normal'), parseDensity('')]).toEqual(['normal', null]);
    expect([parsePageSize('500'), parsePageSize('1000'), parsePageSize('50')]).toEqual([500, null, 50]);
  });
});

describe('pageSlice', () => {
  const rows = Array.from({ length: 230 }, (_, i) => i);
  it('sayfa sayısı + dilim + taşan sayfanın kırpılması', () => {
    expect(pageSlice(rows, 0, 100)).toMatchObject({ page: 0, pages: 3 });
    expect(pageSlice(rows, 2, 100).rows).toEqual(rows.slice(200));
    expect(pageSlice(rows, 9, 100).page).toBe(2);
    expect(pageSlice(rows, -1, 100).page).toBe(0);
    expect(pageSlice([], 0, 100)).toEqual({ rows: [], page: 0, pages: 1 });
  });
});
