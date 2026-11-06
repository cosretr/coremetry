// v0.10.1889 (Log Search 3.6) — birleşik görünümün saf log → span dağıtımı.
import { describe, it, expect } from 'vitest';
import { groupTraceLogs, spansWithLogsAndAncestors } from './traceLogRows';
import type { LogRow, SpanRow } from './types';

const span = (spanId: string, parentSpanId = ''): SpanRow => ({ spanId, parentSpanId } as SpanRow);
const log = (id: number, timestamp: number, spanId: string): LogRow => ({
  id, timestamp, spanId, severity: 9, severityText: 'INFO', body: `m${id}`, serviceName: 'orders-api',
  traceId: 't', attributes: {}, resourceAttributes: {},
});

describe('groupTraceLogs', () => {
  const spans = [span('aa01'), span('bb02', 'aa01')];
  it('span_id ile dağıtır (harf duyarsız), zaman sırası, bağlanamayanlar ayrı', () => {
    const g = groupTraceLogs([log(1, 30, 'BB02'), log(2, 10, 'aa01'), log(3, 20, ''), log(4, 5, 'zz99'), log(5, 15, 'bb02')], spans);
    expect(g.bySpan.get('aa01')?.map(l => l.id)).toEqual([2]);
    expect(g.bySpan.get('bb02')?.map(l => l.id)).toEqual([5, 1]);
    expect(g.orphans.map(l => l.id)).toEqual([4, 3]);
    expect(g.linked).toBe(3);
  });
  it('boş girdi', () => {
    const g = groupTraceLogs([], spans);
    expect(g.bySpan.size).toBe(0);
    expect(g.orphans).toEqual([]);
  });
});

describe('spansWithLogsAndAncestors', () => {
  it('loglu span + ataları; kardeş yok', () => {
    const spans = [span('r'), span('a', 'r'), span('b', 'a'), span('c', 'r')];
    expect([...spansWithLogsAndAncestors(spans, new Set(['b']))].sort()).toEqual(['a', 'b', 'r']);
  });
});
