// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import {
  pushHistory, matchHistory, sanitizeHistory, readLogsQueryHistory, recordLogsQuery, LOGS_HISTORY_CAP,
} from './logsQueryHistory';
import { logsQueryHistoryKey } from './storage';

// v0.10.1820 (Log Search Faz 1 dilim 1.4) — /logs yerel sorgu geçmişi.
describe('pushHistory', () => {
  it('en yeni önde, tekil, tavanlı; boş no-op', () => {
    let l = pushHistory([], 'level:error', 1);
    l = pushHistory(l, 'service.name:orders-api', 2);
    l = pushHistory(l, '  level:error  ', 3);
    expect(l).toEqual([{ q: 'level:error', at: 3 }, { q: 'service.name:orders-api', at: 2 }]);
    expect(pushHistory(l, '   ', 4)).toBe(l);
    let big = l;
    for (let i = 0; i < 30; i++) big = pushHistory(big, `q${i}`, 10 + i);
    expect(big).toHaveLength(LOGS_HISTORY_CAP);
    expect(big[0].q).toBe('q29');
  });
  it('aşırı uzun metin kaydedilmez', () => {
    expect(pushHistory([], 'x'.repeat(2001), 1)).toEqual([]);
  });
});

describe('matchHistory', () => {
  const l = [{ q: 'level:error and service.name:orders-api', at: 3 }, { q: 'payments-mobile timeout', at: 2 }, { q: 'level:warn', at: 1 }];
  it('harf duyarsız alt dize, sıra korunur', () => {
    expect(matchHistory(l, 'LEVEL').map(e => e.q)).toEqual(['level:error and service.name:orders-api', 'level:warn']);
  });
  it('boş metin hepsini (limitli) verir; birebir aynı girdi öneri değildir', () => {
    expect(matchHistory(l, '', 2)).toHaveLength(2);
    expect(matchHistory(l, 'level:warn').map(e => e.q)).toEqual([]);
  });
});

describe('sanitizeHistory', () => {
  it('bozuk / yabancı / kopya girdiler atılır', () => {
    expect(sanitizeHistory('x')).toEqual([]);
    expect(sanitizeHistory([{ q: 'a', at: 1 }, { q: 'a', at: 2 }, { q: 5, at: 1 }, null, { q: ' ', at: 1 }, { q: 'b', at: NaN }, { q: 'c', at: 9 }]))
      .toEqual([{ q: 'a', at: 1 }, { q: 'c', at: 9 }]);
  });
});

// recentMetrics.test emsali: Map destekli localStorage (Node sürümünden bağımsız).
let store = new Map<string, string>();
let throwing = false;
function stubStorage() {
  store = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => { if (throwing) throw new Error('denied'); return store.get(k) ?? null; },
    setItem: (k: string, v: string) => { if (throwing) throw new Error('denied'); store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); },
  });
}

describe('kayıt (localStorage)', () => {
  beforeEach(() => { throwing = false; stubStorage(); });
  afterEach(() => { vi.unstubAllGlobals(); });
  it('kullanıcı başına ayrı anahtar; kayıt + geri okuma', () => {
    recordLogsQuery('u1', 'level:error', 10);
    recordLogsQuery('u2', 'timeout', 11);
    expect(readLogsQueryHistory('u1')).toEqual([{ q: 'level:error', at: 10 }]);
    expect(readLogsQueryHistory('u2')).toEqual([{ q: 'timeout', at: 11 }]);
    expect(store.get(logsQueryHistoryKey('u1'))).toContain('level:error');
    recordLogsQuery(undefined, 'anon q', 12);
    expect(readLogsQueryHistory(undefined)[0].q).toBe('anon q');
  });
  it('bozuk JSON ya da erişilemeyen depo sayfayı çökertmez', () => {
    store.set(logsQueryHistoryKey('u1'), '{bozuk');
    expect(readLogsQueryHistory('u1')).toEqual([]);
    throwing = true;
    expect(readLogsQueryHistory('u1')).toEqual([]);
    expect(() => recordLogsQuery('u1', 'x', 1)).not.toThrow();
  });
});
