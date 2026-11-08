// traceJumps — v0.10.1953 hızlı atlama hedefleri (saf).
import { describe, it, expect } from 'vitest';
import { traceJumps, errorMessageOf, HOT_MIN_SHARE } from './traceJumps';
import type { SpanRow } from './types';

let n = 0;
const sp = (p: Partial<SpanRow> & { serviceName: string; durationMs: number }): SpanRow => ({
  traceId: 't', spanId: p.spanId ?? `s${n++}`, parentSpanId: '', name: 'op', kind: 'internal', startTime: 0,
  statusCode: 'unset', statusMessage: '', attributes: {}, events: null, ...p,
} as SpanRow);

describe('traceJumps', () => {
  it('en uzun dış çağrı ve kök hata (hatalı çocuğu olmayan)', () => {
    const root = sp({ spanId: 'r', serviceName: 'api-gateway', durationMs: 2400, kind: 'server', statusCode: 'error' });
    const pay = sp({ spanId: 'p', parentSpanId: 'r', serviceName: 'payments-mobile', durationMs: 1600, kind: 'client', dbSystem: 'oracle', startTime: 10 });
    const card = sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'card-service', durationMs: 300, kind: 'server', statusCode: 'error', startTime: 20 });
    const ins = sp({ spanId: 'i', parentSpanId: 'c', serviceName: 'card-service', durationMs: 250, kind: 'client', statusCode: 'error', startTime: 30,
      events: [{ name: 'exception', timeNano: 1, attributes: { 'exception.message': 'ORA-00001: unique constraint violated' } }] });
    const j = traceJumps([root, pay, card, ins])!;
    expect(j.hot).toEqual({ spanId: 'p', ms: 1600 });
    expect(j.firstError).toEqual({ spanId: 'i', service: 'card-service', message: 'ORA-00001: unique constraint violated' });
  });
  it('payı eşiğin altındaki çağrı ve hatasız trace: çip yok', () => {
    const r = sp({ spanId: 'r2', serviceName: 'api', durationMs: 1000, kind: 'server' });
    const c = sp({ spanId: 'c2', parentSpanId: 'r2', serviceName: 'api', durationMs: 1000 * HOT_MIN_SHARE - 1, kind: 'client' });
    expect(traceJumps([r, c])).toEqual({});
    expect(traceJumps([])).toBeNull();
  });
  it('hata mesajı: exception olayı > öznitelik > durum mesajı', () => {
    expect(errorMessageOf(sp({ serviceName: 'x', durationMs: 1, statusMessage: 'boom' }))).toBe('boom');
    expect(errorMessageOf(sp({ serviceName: 'x', durationMs: 1, statusMessage: 'boom', attributes: { 'exception.message': 'attr' } }))).toBe('attr');
  });
});
