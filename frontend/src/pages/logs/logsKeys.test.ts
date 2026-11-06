// logsKeys — v0.10.1857 (Log Search Faz 2 dilim 2.6).
import { describe, it, expect } from 'vitest';
import { ensureFilter, logRowKeyAction } from './logsKeys';
import type { LogRow } from '@/lib/types';
import type { LogFilter } from '@/lib/logFilters';

const row = (p: Partial<LogRow>) => ({ id: 1, serviceName: 'orders-api', traceId: '9c1d07aa5b3e4f0a8d217c66f201e41f', ...p } as LogRow);

describe('logRowKeyAction', () => {
  it('f: service hapı eklenir; tekrar basmak değiştirmez', () => {
    const a = logRowKeyAction('f', row({}), []);
    expect(a).toEqual({ kind: 'pills', next: [{ key: 'service', value: 'orders-api', negated: false, disabled: false }] });
    const again = logRowKeyAction('f', row({}), a.kind === 'pills' ? a.next : []);
    expect(again).toEqual({ kind: 'none' });
  });
  it('e: hariç tut; var olan pozitif hap çevrilir', () => {
    const pos: LogFilter[] = [{ key: 'service', value: 'orders-api', negated: false, disabled: false }];
    expect(logRowKeyAction('e', row({}), pos)).toEqual({ kind: 'pills', next: [{ key: 'service', value: 'orders-api', negated: true, disabled: false }] });
    expect(logRowKeyAction('e', row({ serviceName: 'payments-mobile' }), pos)).toEqual({
      kind: 'pills', next: [...pos, { key: 'service', value: 'payments-mobile', negated: true, disabled: false }],
    });
  });
  it('t: trace varsa git, yoksa no-op; satır yoksa / servis yoksa no-op', () => {
    expect(logRowKeyAction('t', row({}), [])).toEqual({ kind: 'trace', traceId: '9c1d07aa5b3e4f0a8d217c66f201e41f' });
    expect(logRowKeyAction('t', row({ traceId: '' }), [])).toEqual({ kind: 'none' });
    expect(logRowKeyAction('f', null, [])).toEqual({ kind: 'none' });
    expect(logRowKeyAction('f', row({ serviceName: '' }), [])).toEqual({ kind: 'none' });
  });
  it('ensureFilter: kapalı hap açılır; aralık / varlık hapı kimlik dışında', () => {
    const off: LogFilter[] = [{ key: 'service', value: 'a', negated: false, disabled: true }];
    expect(ensureFilter(off, 'service', 'a', false)).toEqual([{ key: 'service', value: 'a', negated: false, disabled: false }]);
    const range: LogFilter[] = [{ key: 'service', value: 'a', negated: false, disabled: false, op: 'gte' }];
    expect(ensureFilter(range, 'service', 'a', false)).toHaveLength(2);
  });
});
