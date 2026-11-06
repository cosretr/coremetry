// logsGroup — v0.10.1884 (Log Search Faz 3 dilim 3.5).
import { describe, it, expect } from 'vitest';
import { groupRowsByTrace, parseLogsGroup, traceGroupSummary } from './logsGroup';
import type { LogRow } from '@/lib/types';

const r = (id: number, traceId: string) => ({ id, traceId } as LogRow);
const dur = (ms: number) => `${ms} ms`;

describe('groupRowsByTrace', () => {
  it('ilk görülme sırası, grup içi sıra korunur, trace\'siz en sonda', () => {
    const g = groupRowsByTrace([r(1, 'a'), r(2, ''), r(3, 'b'), r(4, 'a'), r(5, ''), r(6, 'b')]);
    expect(g.rows.map(x => x.id)).toEqual([1, 4, 3, 6, 2, 5]);
    expect([...g.counts]).toEqual([['a', 2], ['b', 2], ['', 2]]);
  });
  it('boş', () => {
    expect(groupRowsByTrace([]).rows).toEqual([]);
  });
});

describe('başlık', () => {
  it('bulunan / örneklenmiş / bilinmiyor', () => {
    expect(traceGroupSummary({ state: 'found', durationMs: 1840, errorCount: 1, rootName: 'POST /api/orders' }, 2, dur))
      .toBe('1840 ms · ✕ hata var · kök POST /api/orders · 2 log (bu örnekte)');
    expect(traceGroupSummary({ state: 'sampled' }, 1, dur)).toBe("trace ClickHouse'ta yok (sampling Collector'da) · 1 log (bu örnekte)");
    expect(traceGroupSummary(undefined, 3, dur)).toBe('3 log (bu örnekte)');
  });
  it('URL değeri', () => {
    expect(parseLogsGroup('trace')).toBe('trace');
    expect(parseLogsGroup('x')).toBe('');
  });
});
