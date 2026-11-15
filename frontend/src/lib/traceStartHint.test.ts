import { describe, expect, it, beforeEach } from 'vitest';
import { rememberTraceStart, resetTraceStartHint, traceStartFor } from './traceStartHint';
import { contextStarter } from './chatContext';

const ID = '0123456789ABCDEF0123456789abcdef';

describe('traceStartHint (v0.10.2201)', () => {
  beforeEach(() => resetTraceStartHint());

  it('en erken span başlangıcını ms olarak tutar, kimlik büyük/küçük harf duyarsız', () => {
    rememberTraceStart(ID, [{ startTime: 3_000_000_000 }, { startTime: 2_000_000_000 }, { startTime: 0 }]);
    expect(traceStartFor(ID.toLowerCase())).toBe(2000);
    expect(traceStartFor('ffffffffffffffffffffffffffffffff')).toBeUndefined();
  });

  it('boş span listesi ipucu yazmaz', () => {
    rememberTraceStart(ID, []);
    expect(traceStartFor(ID)).toBeUndefined();
  });

  it('trace başlangıç çipi bilinen başlangıcı explainAsk.startMs ile taşır', () => {
    expect(contextStarter('/trace', `?id=${ID}`)?.explainAsk?.startMs).toBeUndefined();
    rememberTraceStart(ID, [{ startTime: 1_700_000_000_000_000_000 }]);
    expect(contextStarter('/trace', `?id=${ID}`)?.explainAsk).toEqual({ kind: 'trace', id: ID.toLowerCase(), src: 'starter', startMs: 1_700_000_000_000 });
  });
});
